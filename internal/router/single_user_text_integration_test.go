package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// Exercise authentication, candidate filtering, request conversion, streaming
// and billing with a Responses upstream that deliberately rejects arrays.
func TestSingleUserTextChannelThroughLazu(t *testing.T) {
	runSingleUserTextChannelIntegration(t, "")
}

// liveResponsesURL is the upstream Responses endpoint a live run forwards to.
var liveResponsesURL string

// Explicit opt-in for short, paid acceptance calls. The credential is supplied
// by the operator's environment and is never written to logs or fixtures.
func TestSingleUserTextChannelLive(t *testing.T) {
	if os.Getenv("LAZU_SOL_LIVE_TEST") != "1" {
		t.Skip("live upstream acceptance is opt-in")
	}
	key := os.Getenv("LAZU_SOL_LIVE_KEY")
	endpoint := os.Getenv("LAZU_SOL_LIVE_URL")
	if key == "" || endpoint == "" {
		t.Fatal("LAZU_SOL_LIVE_KEY and LAZU_SOL_LIVE_URL (a Responses endpoint) are required")
	}
	liveResponsesURL = endpoint
	runSingleUserTextChannelIntegration(t, key)
}

func runSingleUserTextChannelIntegration(t *testing.T, liveKey string) {
	t.Helper()
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
	var calls atomic.Int32
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	liveClient := &http.Client{Transport: transport, Timeout: 60 * time.Second}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Input  string `json:"input"`
			Stream bool   `json:"stream"`
		}
		if r.URL.Path != "/v1/responses" {
			t.Errorf("wrong upstream endpoint: %s", r.URL.Path)
		}
		raw, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Error(readErr)
			w.WriteHeader(400)
			return
		}
		if err := json.Unmarshal(raw, &body); err != nil || body.Input != "Reply only OK." {
			t.Errorf("upstream received non-string or altered input: %+v (%v)", body, err)
			w.WriteHeader(400)
			return
		}
		if liveKey != "" {
			upstreamRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, liveResponsesURL, bytes.NewReader(raw))
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			upstreamRequest.Header.Set("Authorization", "Bearer "+liveKey)
			upstreamRequest.Header.Set("Content-Type", "application/json")
			upstreamRequest.Header.Set("User-Agent", "Mozilla/5.0")
			response, err := liveClient.Do(upstreamRequest)
			if err != nil {
				t.Error(err)
				w.WriteHeader(502)
				return
			}
			defer response.Body.Close()
			w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
			w.WriteHeader(response.StatusCode)
			if _, err := io.Copy(w, response.Body); err != nil {
				t.Error(err)
			}
			return
		}
		response := `{"id":"resp_sol_fixture","object":"response","model":"sol-fixture","created_at":1,"status":"completed","output":[{"id":"msg_fixture","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK","annotations":[]}]}],"usage":{"input_tokens":12,"output_tokens":2,"total_tokens":14}}`
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_sol_fixture\",\"model\":\"sol-fixture\",\"created_at\":1,\"status\":\"in_progress\"}}\n\n")
			fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"OK\"}\n\n")
			fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, response)
		}
	}))
	defer upstream.Close()
	allowLocalRelayUpstream(t, upstream.URL)
	if err := model.CoreDB().Create(&model.User{Id: 981, Username: "sol-fixture", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}).Error; err != nil {
		t.Fatal(err)
	}
	key := &model.Token{Id: 981, UserId: 981, Key: "single-text-fixture-key", Group: model.ChannelLaneDirect, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1)}
	if err := key.Insert(); err != nil {
		t.Fatal(err)
	}
	noBan := 0
	channel := model.Channel{Id: 981, Type: constant.ChannelTypeOpenAI, Key: "fixture-upstream", Name: "single-text", Status: common.ChannelStatusEnabled, Models: "sol-fixture", Group: model.ChannelLaneDirect, BaseURL: &upstream.URL, AutoBan: &noBan,
		OtherSettings: `{"protocol_dialect":"openai","responses_single_user_text":true,"capabilities":{"supports_chat_completions":false,"supports_responses":true,"supports_tools":false,"supports_vision":false,"supports_audio":false}}`}
	if liveKey != "" {
		mapping := `{"sol-fixture":"gpt-5.6-sol"}`
		channel.ModelMapping = &mapping
	}
	if err := model.CoreDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := channel.AddAbilities(nil); err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Model{ModelName: "sol-fixture", Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact}).Error; err != nil {
		t.Fatal(err)
	}
	inputPrice, outputPrice := 1.0, 10.0
	if err := model.CoreDB().Create(&model.ModelSellPrice{ModelName: "sol-fixture", ChannelGroup: model.ChannelLaneDirect, InputPerMtok: &inputPrice, OutputPerMtok: &outputPrice, Active: true, PricingVersion: 1}).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	engine := gin.New()
	engine.Use(middleware.RequestId())
	SetRelayRouter(engine)
	request := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer sk-"+key.Key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}
	for _, stream := range []bool{false, true} {
		for _, input := range []string{`"Reply only OK."`, `[{"role":"user","content":[{"type":"input_text","text":"Reply only OK."}]}]`} {
			w := request("/v1/responses", fmt.Sprintf(`{"model":"sol-fixture","input":%s,"stream":%t,"store":false}`, input, stream))
			if w.Code != 200 || !strings.Contains(w.Body.String(), "OK") || (stream && !strings.Contains(w.Body.String(), "response.completed")) {
				t.Fatalf("Responses stream=%v: status=%d body=%s", stream, w.Code, w.Body)
			}
		}
		w := request("/v1/chat/completions", fmt.Sprintf(`{"model":"sol-fixture","messages":[{"role":"user","content":"Reply only OK."}],"stream":%t}`, stream))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "OK") || (stream && !strings.Contains(w.Body.String(), "[DONE]")) {
			t.Fatalf("Chat stream=%v: status=%d body=%s", stream, w.Code, w.Body)
		}
	}
	var balanceBefore model.User
	if err := model.CoreDB().First(&balanceBefore, 981).Error; err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	for _, payload := range []string{
		`"input":[{"role":"user","content":"first"},{"role":"assistant","content":"second"}]`,
		`"input":"Reply only OK.","instructions":"system"`,
		`"input":"Reply only OK.","tools":[{"type":"web_search_preview"}]`,
		`"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/image.png"}]}]`,
		`"input":"Reply only OK.","store":true`,
		`"input":"Reply only OK.","previous_response_id":"resp_old"`,
	} {
		w := request("/v1/responses", `{"model":"sol-fixture",`+payload+`}`)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "protocol_bridge_unsupported") {
			t.Fatalf("restricted input not rejected clearly: %d %s", w.Code, w.Body)
		}
	}
	w := request("/v1/chat/completions", `{"model":"sol-fixture","messages":[{"role":"system","content":"system"},{"role":"user","content":"Reply only OK."}]}`)
	if w.Code != 400 {
		t.Fatalf("Chat instructions accepted: %d %s", w.Code, w.Body)
	}
	var balanceAfter model.User
	if err := model.CoreDB().First(&balanceAfter, 981).Error; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before || balanceAfter.Quota != balanceBefore.Quota {
		t.Fatal("rejected input reached upstream or changed the wallet")
	}
	if before != 6 {
		t.Fatalf("expected six successful upstream calls, got %d", before)
	}
}
