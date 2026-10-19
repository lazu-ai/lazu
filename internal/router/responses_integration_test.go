package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// Exercise the real authentication, distribution, bridge, failover and wallet
// paths against deterministic upstreams, without production credentials.
func TestResponsesToolRoundtripThroughRelayAndFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()
	oldLog, oldSQLite, oldMemory := model.ConfiguredTelemetryDB(), common.UsingSQLite, common.MemoryCacheEnabled
	model.SetTelemetryDB(model.CoreDB())
	common.UsingSQLite = true
	common.MemoryCacheEnabled = false
	defer func() {
		drainRelayDiagnosticFixture(t)
		model.SetTelemetryDB(oldLog)
		common.UsingSQLite = oldSQLite
		common.MemoryCacheEnabled = oldMemory
	}()
	if err := model.CoreDB().AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().AutoMigrate(&model.Log{}, &model.WalletLedger{}, &model.RoutingDecision{}, &model.RoutingOutcome{}, &model.RoutingAttemptOutcome{}, &model.TelemetryEventReceipt{}); err != nil {
		t.Fatal(err)
	}
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		io.WriteString(w, `{"error":{"message":"fixture busy","type":"server_error","code":"upstream_overloaded"}}`)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls.Add(1)
		var request struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role, Content string
				Reasoning     string `json:"reasoning_content"`
				ToolCallID    string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("wrong bridge endpoint: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if request.Model != "roundtrip-fixture" {
			t.Errorf("failover changed model: %q", request.Model)
		}
		second, reasoning := false, false
		for _, m := range request.Messages {
			if m.Role == "assistant" && m.Reasoning == "Calculate first." {
				reasoning = true
			}
			if m.Role == "tool" && m.ToolCallID == "call_add" && m.Content == "3" {
				second = true
			}
		}
		if second && !reasoning {
			t.Error("second round lost reasoning history")
		}
		message := `{"role":"assistant","reasoning_content":"Calculate first.","content":null,"tool_calls":[{"index":0,"id":"call_add","type":"function","function":{"name":"add","arguments":"{\"a\":1,\"b\":2}"}}]}`
		finish := "tool_calls"
		if second {
			message, finish = `{"role":"assistant","content":"3"}`, "stop"
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"id\":\"chat_fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"roundtrip-fixture\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":null}]}\n\n", message)
			fmt.Fprintf(w, "data: {\"id\":\"chat_fixture\",\"model\":\"roundtrip-fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\ndata: [DONE]\n\n", finish)
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"chat_fixture","object":"chat.completion","model":"roundtrip-fixture","choices":[{"index":0,"message":%s,"finish_reason":%q}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, message, finish)
		}
	}))
	defer fallback.Close()
	allowLocalRelayUpstream(t, primary.URL)
	allowLocalRelayUpstream(t, fallback.URL)
	if err := model.CoreDB().Create(&model.User{Id: 991, Username: "roundtrip", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}).Error; err != nil {
		t.Fatal(err)
	}
	key := &model.Token{Id: 991, UserId: 991, Key: "roundtrip-fixture-key", Group: model.ChannelLaneDirect, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1)}
	if err := key.Insert(); err != nil {
		t.Fatal(err)
	}
	for i, url := range []string{primary.URL, fallback.URL} {
		priority, noBan := int64(10-i*10), 0
		channel := model.Channel{Id: 991 + i, Type: constant.ChannelTypeOpenAI, Key: "local-upstream", Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("fixture-%d", i), Models: "roundtrip-fixture", Group: model.ChannelLaneDirect, BaseURL: &url, Priority: &priority, AutoBan: &noBan, OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`}
		if err := model.CoreDB().Create(&channel).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.CoreDB().Create(&model.Ability{ChannelId: channel.Id, Group: model.ChannelLaneDirect, Model: "roundtrip-fixture", Enabled: true, Priority: &priority}).Error; err != nil {
			t.Fatal(err)
		}
	}
	inputPrice, outputPrice := 1.0, 10.0
	if err := model.CoreDB().Create(&model.Model{ModelName: "roundtrip-fixture", Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.ModelSellPrice{ModelName: "roundtrip-fixture", ChannelGroup: model.ChannelLaneDirect, InputPerMtok: &inputPrice, OutputPerMtok: &outputPrice, Active: true, PricingVersion: 1}).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	engine := gin.New()
	engine.Use(middleware.RequestId())
	SetRelayRouter(engine)
	gateway := httptest.NewServer(engine)
	defer gateway.Close()
	toolsJSON := json.RawMessage(`[{"type":"function","name":"add","parameters":{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"],"additionalProperties":false}}]`)
	history := []json.RawMessage{json.RawMessage(`{"role":"user","content":"Add 1 and 2."}`)}
	for turn := 0; turn < 2; turn++ {
		body, err := json.Marshal(map[string]any{"model": "roundtrip-fixture", "input": history, "tools": toolsJSON, "store": false, "max_output_tokens": 100})
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPost, gateway.URL+"/v1/responses", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer sk-"+key.Key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := gateway.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("round %d failed: status=%d err=%v body=%s", turn, resp.StatusCode, err, raw)
		}
		var response struct {
			Output []json.RawMessage `json:"output"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		if turn == 0 {
			if !bytes.Contains(raw, []byte(`"call_id":"call_add"`)) || !bytes.Contains(raw, []byte(`"summary":[]`)) {
				t.Fatalf("invalid first round: %s", raw)
			}
			history = append(history, response.Output...)
			history = append(history, json.RawMessage(`{"type":"function_call_output","call_id":"call_add","output":"3"}`))
		} else if !bytes.Contains(raw, []byte(`"text":"3"`)) {
			t.Fatalf("invalid continuation: %s", raw)
		}
	}
	// Optional external client proof: the normal Go regression needs no Python.
	if python := os.Getenv("LAZU_TEST_OPENAI_PYTHON"); python != "" {
		cmd := exec.Command(python, "-c", responsesSDKRoundtrip, gateway.URL+"/v1", "sk-"+key.Key)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("OpenAI SDK roundtrip: %v\n%s", err, out)
		} else {
			t.Log(strings.TrimSpace(string(out)))
		}
	}
	requests := int64(fallbackCalls.Load())
	if primaryCalls.Load() != fallbackCalls.Load() || requests < 2 {
		t.Fatalf("wrong attempt counts: %d/%d", primaryCalls.Load(), fallbackCalls.Load())
	}
	var user model.User
	if err := model.CoreDB().First(&user, 991).Error; err != nil {
		t.Fatal(err)
	}
	if user.Quota != billing.USD(1)-billing.MicroUSD(requests*60) {
		t.Fatalf("failover double-charged or lost reservation: balance=%d requests=%d", user.Quota, requests)
	}
	var logs []model.Log
	if err := model.CoreDB().Where("type = ?", model.LogTypeConsume).Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if int64(len(logs)) != requests {
		t.Fatalf("want one bill per final request: logs=%d requests=%d", len(logs), requests)
	}
	for _, log := range logs {
		fact, ok := usagepkg.ParseFact(log.UsageFact)
		if !ok {
			t.Fatalf("invalid persisted usage fact: %s", log.UsageFact)
		}
		measurement := fact.UsageRequestUsage().Measurement
		if measurement == nil || measurement.Input != dto.UsageSourceProvider || measurement.Output != dto.UsageSourceProvider {
			t.Fatalf("provider provenance lost between relay, persisted fact and receipt: %+v", measurement)
		}
	}
	var attempts, outcomes int64
	model.CoreDB().
		Model(&model.RoutingAttemptOutcome{}).Count(&attempts)
	model.CoreDB().
		Model(&model.RoutingOutcome{}).Count(&outcomes)
	if attempts != requests || outcomes != requests {
		t.Fatalf("incorrect request/attempt accounting: %d/%d requests=%d", attempts, outcomes, requests)
	}
	rows, err := model.QueryChannelHealthEvidence(time.Now().UTC().Add(-time.Hour))
	if err != nil || len(rows) != 2 {
		t.Fatalf("health lost failed attempts: rows=%+v err=%v", rows, err)
	}
	for _, row := range rows {
		wantErrors := int64(0)
		if row.ChannelId == 991 {
			wantErrors = requests
		}
		if row.RequestCount != requests || row.ErrorCount != wantErrors {
			t.Fatalf("failed attempt and final success health conflated: %+v", row)
		}
	}
}

const responsesSDKRoundtrip = `
import sys, openai
from openai import OpenAI
client = OpenAI(base_url=sys.argv[1], api_key=sys.argv[2], max_retries=0, timeout=15, http_client=openai.DefaultHttpxClient(trust_env=False))
tools = [{"type":"function","name":"add","parameters":{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"],"additionalProperties":False}}]
for streaming in (False, True):
    history = [{"role":"user","content":"Add 1 and 2."}]
    options = dict(model="roundtrip-fixture", input=history, tools=tools, store=False, max_output_tokens=100)
    if streaming:
        with client.responses.stream(**options) as stream:
            first = stream.get_final_response()
    else:
        first = client.responses.create(**options)
    assert first.status == "completed", first
    assert any(item.type == "reasoning" and item.summary == [] for item in first.output), first
    call = next(item for item in first.output if item.type == "function_call")
    history += [item.model_dump(exclude_unset=True) for item in first.output]
    history.append({"type":"function_call_output","call_id":call.call_id,"output":"3"})
    if streaming:
        with client.responses.stream(**options) as stream:
            final = stream.get_final_response()
    else:
        final = client.responses.create(**options)
    assert final.output_text == "3", final
print("OpenAI Python", openai.__version__, "JSON + SSE tool roundtrips passed")
`
