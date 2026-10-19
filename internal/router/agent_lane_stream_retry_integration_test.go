package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

// Reproduces the 2026-09-18 report: Codex on an agent-lane key against a reseller
// that answers a /v1/responses stream with a transport-level 200 and an in-band
// overload event. Before the first-event hold, the gateway forwarded that event,
// called the attempt a success, wrote a zero-token "$0 consumption" row, never
// signalled channel health, and could not retry because the response had already
// started. The customer saw a stalled turn and a $0 line they could not explain.
//
// The agent lane must not switch channels — another agent-lane channel can carry a
// different price — so the recovery has to happen on the same channel.
func TestCheapLaneRetriesInBandStreamFailureOnSameChannel(t *testing.T) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if attempt == 1 {
			// Exactly what one coding-agent pool sent: HTTP 200, then an in-band failure with
			// the provider's own bilingual text and no usage at all.
			io.WriteString(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"server_error\",\"message\":\"OpenAI servers are currently overloaded. / 当前 OpenAI 服务器过载\"}}}\n\n")
			return
		}
		io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered\"}\n\n")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fixture\",\"usage\":{\"input_tokens\":40,\"output_tokens\":6}}}\n\n")
	}))
	defer upstream.Close()
	allowLocalRelayUpstream(t, upstream.URL)

	if err := model.CoreDB().Create(&model.User{Id: 993, Username: "cheap-stream", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}).Error; err != nil {
		t.Fatal(err)
	}
	// Pin the model to the agent lane the way the connect dialog does, so the
	// request routes exactly like the customer's Codex key did.
	key := &model.Token{
		Id: 993, UserId: 993, Key: "cheap-stream-key", Group: model.ChannelLaneDirect,
		Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1),
		ModelLanes: model.TokenModelLaneMap{"cheap-stream-fixture": model.ChannelLaneAgent},
	}
	if err := key.Insert(); err != nil {
		t.Fatal(err)
	}
	priority, noBan := int64(10), 0
	channel := model.Channel{
		Id: 993, Type: constant.ChannelTypeOpenAI, Key: "local-upstream", Status: common.ChannelStatusEnabled,
		Name: "cheap-reverse", Models: "cheap-stream-fixture", Group: model.ChannelLaneAgent,
		BaseURL: &upstream.URL, Priority: &priority, AutoBan: &noBan,
		OtherSettings: `{"protocol_dialect":"openai"}`,
	}
	if err := model.CoreDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Ability{ChannelId: channel.Id, Group: model.ChannelLaneAgent, Model: "cheap-stream-fixture", Enabled: true, Priority: &priority}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Model{ModelName: "cheap-stream-fixture", Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact}).Error; err != nil {
		t.Fatal(err)
	}
	inputPrice, outputPrice := 1.0, 10.0
	if err := model.CoreDB().Create(&model.ModelSellPrice{ModelName: "cheap-stream-fixture", ChannelGroup: model.ChannelLaneAgent, InputPerMtok: &inputPrice, OutputPerMtok: &outputPrice, Active: true, PricingVersion: 1}).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(middleware.RequestId())
	SetRelayRouter(engine)
	gateway := httptest.NewServer(engine)
	defer gateway.Close()

	body, err := json.Marshal(map[string]any{
		"model":  "cheap-stream-fixture",
		"input":  []json.RawMessage{json.RawMessage(`{"role":"user","content":"hello"}`)},
		"stream": true,
		"store":  false,
	})
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
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request failed: status=%d body=%s", resp.StatusCode, raw)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream attempts = %d, want 2 (one failure, one retry)", got)
	}
	if !strings.Contains(string(raw), "recovered") {
		t.Fatalf("client did not receive the recovered turn: %s", raw)
	}
	// The held failure must never reach the client, or Codex shows an overload
	// banner for a turn that actually succeeded.
	if strings.Contains(string(raw), "currently overloaded") {
		t.Fatalf("forwarded the failed attempt to the client: %s", raw)
	}
	if got := resp.Header.Get("X-Lazu-Retries"); got != "1" {
		t.Fatalf("X-Lazu-Retries = %q, want \"1\"", got)
	}

	// The retry stays on the channel whose price the customer was quoted, and
	// the failure that caused it is visible to channel health — before the
	// first-event hold it was invisible, because the attempt looked successful.
	var attempts []model.RoutingAttemptOutcome
	if err := model.CoreDB().Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("recorded %d retried attempts, want 1", len(attempts))
	}
	failed := attempts[0]
	if failed.ChannelID != 993 {
		t.Fatalf("agent lane switched channels: attempt on channel %d", failed.ChannelID)
	}
	if failed.ErrorCode != string(types.ErrorCodeUpstreamOverloaded) {
		t.Fatalf("attempt error code = %q, want %q", failed.ErrorCode, types.ErrorCodeUpstreamOverloaded)
	}
	if failed.HealthEligible == nil || !*failed.HealthEligible || failed.HealthFailed == nil || !*failed.HealthFailed {
		t.Fatalf("in-band stream failure did not count against channel health: %+v", failed)
	}

	// Billing and the console view: one consume row for the successful turn, and
	// no zero-token "$0 consumption" row standing in for the failed attempt.
	deadline := time.Now().Add(5 * time.Second)
	var consume, errors []model.Log
	for time.Now().Before(deadline) {
		consume, errors = nil, nil
		if err := model.TelemetryDB().Where("type = ?", model.LogTypeConsume).Find(&consume).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.TelemetryDB().Where("type = ?", model.LogTypeError).Find(&errors).Error; err != nil {
			t.Fatal(err)
		}
		if len(consume) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(consume) != 1 {
		t.Fatalf("consume rows = %d, want 1: %s", len(consume), formatLogRows(consume))
	}
	if consume[0].PromptTokens != 40 || consume[0].CompletionTokens != 6 {
		t.Fatalf("billed the wrong usage: %s", formatLogRows(consume))
	}
	if len(errors) != 0 {
		t.Fatalf("a request that recovered left error rows for the customer: %s", formatLogRows(errors))
	}
}

// When every attempt fails the customer does get one error row — exactly one,
// not one per attempt — and it speaks their language, not the operator's.
func TestExhaustedCheapLaneWritesOneCustomerErrorRow(t *testing.T) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"servers are currently overloaded\"}\n\n")
	}))
	defer upstream.Close()
	allowLocalRelayUpstream(t, upstream.URL)

	if err := model.CoreDB().Create(&model.User{Id: 994, Username: "cheap-exhausted", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}).Error; err != nil {
		t.Fatal(err)
	}
	key := &model.Token{
		Id: 994, UserId: 994, Key: "cheap-exhausted-key", Group: model.ChannelLaneDirect,
		Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1),
		ModelLanes: model.TokenModelLaneMap{"cheap-exhausted-fixture": model.ChannelLaneAgent},
	}
	if err := key.Insert(); err != nil {
		t.Fatal(err)
	}
	priority, noBan := int64(10), 0
	channel := model.Channel{
		Id: 994, Type: constant.ChannelTypeOpenAI, Key: "local-upstream", Status: common.ChannelStatusEnabled,
		Name: "cheap-reverse", Models: "cheap-exhausted-fixture", Group: model.ChannelLaneAgent,
		BaseURL: &upstream.URL, Priority: &priority, AutoBan: &noBan,
		OtherSettings: `{"protocol_dialect":"openai"}`,
	}
	if err := model.CoreDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Ability{ChannelId: channel.Id, Group: model.ChannelLaneAgent, Model: "cheap-exhausted-fixture", Enabled: true, Priority: &priority}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Model{ModelName: "cheap-exhausted-fixture", Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact}).Error; err != nil {
		t.Fatal(err)
	}
	inputPrice, outputPrice := 1.0, 10.0
	if err := model.CoreDB().Create(&model.ModelSellPrice{ModelName: "cheap-exhausted-fixture", ChannelGroup: model.ChannelLaneAgent, InputPerMtok: &inputPrice, OutputPerMtok: &outputPrice, Active: true, PricingVersion: 1}).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(middleware.RequestId())
	SetRelayRouter(engine)
	gateway := httptest.NewServer(engine)
	defer gateway.Close()

	body, _ := json.Marshal(map[string]any{
		"model":  "cheap-exhausted-fixture",
		"input":  []json.RawMessage{json.RawMessage(`{"role":"user","content":"hello"}`)},
		"stream": true,
		"store":  false,
	})
	req, _ := http.NewRequest(http.MethodPost, gateway.URL+"/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-"+key.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := gateway.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if got := calls.Load(); got != relayAttemptsWhenEveryRetryFails {
		t.Fatalf("upstream attempts = %d, want %d", got, relayAttemptsWhenEveryRetryFails)
	}
	// The customer's client prints this verbatim, so it must say what they can
	// do, in words the console uses.
	if !strings.Contains(string(raw), "pin the model to the direct lane") {
		t.Fatalf("final error carried no actionable hint: %s", raw)
	}

	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("error response was not JSON: %s", raw)
	}
	// The stable error code keeps its machine-readable name; only the prose the
	// customer reads has to avoid operator vocabulary.
	for _, banned := range []string{"upstream", "channel", "provider"} {
		if strings.Contains(strings.ToLower(payload.Error.Message), banned) {
			t.Fatalf("operator vocabulary %q reached the customer: %q", banned, payload.Error.Message)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	var errorRows, consumeRows []model.Log
	for time.Now().Before(deadline) {
		errorRows, consumeRows = nil, nil
		if err := model.TelemetryDB().Where("type = ?", model.LogTypeError).Find(&errorRows).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.TelemetryDB().Where("type = ?", model.LogTypeConsume).Find(&consumeRows).Error; err != nil {
			t.Fatal(err)
		}
		if len(errorRows) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(errorRows) != 1 {
		t.Fatalf("error rows = %d, want exactly 1 for one failed request: %s", len(errorRows), formatLogRows(errorRows))
	}
	// The zero-token "$0 consumption" row is what confused the customer in the
	// original report; a failed request must not produce one at all.
	if len(consumeRows) != 0 {
		t.Fatalf("a failed request was logged as consumption: %s", formatLogRows(consumeRows))
	}
}

// relayAttemptsWhenEveryRetryFails is the full attempt budget: the first send
// plus every retry, all against the one channel the agent lane may use.
const relayAttemptsWhenEveryRetryFails = 4

func formatLogRows(rows []model.Log) string {
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, fmt.Sprintf("{type=%d model=%s ch=%d in=%d out=%d amount=%d content=%q}",
			row.Type, row.ModelName, row.ChannelId, row.PromptTokens, row.CompletionTokens, row.AmountMicrousd, row.Content))
	}
	return "[" + strings.Join(parts, " ") + "]"
}
