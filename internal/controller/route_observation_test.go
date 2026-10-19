package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"gorm.io/gorm"
)

func TestParseObservationReplyReadsEveryShape(t *testing.T) {
	cases := []struct {
		name string
		body string
		text string
		tool string
	}{
		{"openai text", `{"choices":[{"message":{"content":"HARBOUR-ABC"}}]}`, "HARBOUR-ABC", ""},
		{"openai tool", `{"choices":[{"message":{"content":null,"tool_calls":[{"function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]}}]}`, "", "get_weather"},
		{"anthropic", `{"content":[{"type":"text","text":"sure"},{"type":"tool_use","name":"get_weather","input":{"city":"Paris"}}]}`, "sure", "get_weather"},
		{"responses", `{"output":[{"type":"message","content":[{"text":"hi"}]},{"type":"function_call","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}]}`, "hi", "get_weather"},
		{"gemini", `{"candidates":[{"content":{"parts":[{"text":"ok"},{"functionCall":{"name":"get_weather","args":{"city":"Paris"}}}]}}]}`, "ok", "get_weather"},
	}
	for _, tc := range cases {
		text, calls := parseObservationReply([]byte(tc.body))
		if text != tc.text {
			t.Errorf("%s: text = %q, want %q", tc.name, text, tc.text)
		}
		if tc.tool == "" && len(calls) != 0 {
			t.Errorf("%s: calls = %+v, want none", tc.name, calls)
		}
		if tc.tool != "" {
			if len(calls) != 1 || calls[0].Name != tc.tool {
				t.Errorf("%s: calls = %+v, want one %s", tc.name, calls, tc.tool)
				continue
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(calls[0].Arguments), &args); err != nil || args["city"] != "Paris" {
				t.Errorf("%s: arguments = %q", tc.name, calls[0].Arguments)
			}
		}
	}
}

// testSourceChecks are round bars for these tests. They are made up for the
// tests and are not any deployment's settings.
var testSourceChecks = common.SourceChecksConfig{
	ObservationDays:      6,
	MinSamples:           12,
	MinSuccessRate:       0.96,
	MaxBillingRatio:      1.03,
	BillingFramingTokens: 100,
	LongContextTokens:    40_000,
	LongContextMinShare:  0.75,
	CheckEveryHours:      24,
}

// useTestSourceChecks judges watched routes by testSourceChecks for the rest
// of the test.
func useTestSourceChecks(t *testing.T) {
	t.Helper()
	previous := common.AppConfig.SourceChecks
	common.AppConfig.SourceChecks = testSourceChecks
	t.Cleanup(func() { common.AppConfig.SourceChecks = previous })
}

func run(kind string, passed bool, value float64) model.RouteObservationCheck {
	return model.RouteObservationCheck{Kind: kind, Passed: passed, Value: value, Detail: map[bool]string{false: "bad"}[passed]}
}

func TestObservationCheckVerdict(t *testing.T) {
	billing := model.ObservationCheckBilling
	if got := observationCheckVerdict(billing, nil); got.State != observationStatePending {
		t.Fatalf("no runs = %s, want pending", got.State)
	}
	if got := observationCheckVerdict(billing, []model.RouteObservationCheck{{Kind: billing, Skipped: true}}); got.State != observationStatePending {
		t.Fatalf("only skipped runs = %s, want pending", got.State)
	}
	got := observationCheckVerdict(billing, []model.RouteObservationCheck{run(billing, true, 1.01), run(billing, true, 1.03)})
	if got.State != observationStatePass || got.Runs != 2 || got.Passed != 2 || got.Value == nil || *got.Value != 1.03 {
		t.Fatalf("two passes = %+v, want pass 2/2 at the worst ratio 1.03", got)
	}
	// One bad afternoon is noise.
	got = observationCheckVerdict(billing, []model.RouteObservationCheck{run(billing, false, 1.4), run(billing, true, 1.0)})
	if got.State != observationStatePass || got.Passed != 1 || got.Runs != 2 {
		t.Fatalf("one early failure = %+v, want pass 1/2", got)
	}
	// The latest run failing always fails.
	got = observationCheckVerdict(billing, []model.RouteObservationCheck{run(billing, true, 1.0), run(billing, false, 1.38)})
	if got.State != observationStateFail || got.Detail != "bad" {
		t.Fatalf("latest failure = %+v, want fail", got)
	}
	// Two failures are a pattern.
	got = observationCheckVerdict(billing, []model.RouteObservationCheck{run(billing, false, 1.4), run(billing, false, 1.4), run(billing, true, 1.0)})
	if got.State != observationStateFail {
		t.Fatalf("two failures = %+v, want fail", got)
	}
	tools := model.ObservationCheckTools
	got = observationCheckVerdict(tools, []model.RouteObservationCheck{{Kind: tools, NotApplicable: true, Passed: true, Detail: "no tools"}})
	if got.State != observationStateNA || got.Detail != "no tools" {
		t.Fatalf("not applicable = %+v, want na", got)
	}
}

func TestObservationSuccessState(t *testing.T) {
	useTestSourceChecks(t)
	if _, state := observationSuccessState(0, 0); state != observationStatePending {
		t.Fatalf("no samples = %s", state)
	}
	if _, state := observationSuccessState(11, 0); state != observationStatePending {
		t.Fatalf("too few samples = %s", state)
	}
	if rate, state := observationSuccessState(100, 4); state != observationStatePass || *rate != 0.96 {
		t.Fatalf("96%% = %s, want pass at the bar", state)
	}
	if _, state := observationSuccessState(100, 5); state != observationStateFail {
		t.Fatalf("95%% = %s", state)
	}
}

func TestObservationDay(t *testing.T) {
	useTestSourceChecks(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	if day, complete := observationDay(now.Add(-2*time.Hour), now); day != 1 || complete {
		t.Fatalf("two hours in = day %d complete %t", day, complete)
	}
	if day, complete := observationDay(now.Add(-(3*24+1)*time.Hour), now); day != 4 || complete {
		t.Fatalf("three days in = day %d complete %t", day, complete)
	}
	if day, complete := observationDay(now.Add(-(6*24-1)*time.Hour), now); day != 6 || complete {
		t.Fatalf("an hour short = day %d complete %t", day, complete)
	}
	if day, complete := observationDay(now.Add(-8*24*time.Hour), now); day != 6 || !complete {
		t.Fatalf("eight days in = day %d complete %t", day, complete)
	}
}

func TestAggregateObservationViewsTakesTheWorst(t *testing.T) {
	useTestSourceChecks(t)
	low, high := 1.02, 1.38
	views := []observationView{
		{Day: 7, Complete: true, Ready: true, Samples: 100, SuccessRate: ptr(0.99), SuccessState: observationStatePass,
			Checks: []observationCheckView{{Kind: model.ObservationCheckBilling, State: observationStatePass, Runs: 7, Passed: 7, Value: &low}}},
		{Day: 4, Complete: false, Ready: false, Samples: 100, SuccessRate: ptr(0.95), SuccessState: observationStateFail,
			Checks: []observationCheckView{{Kind: model.ObservationCheckBilling, State: observationStateFail, Runs: 4, Passed: 3, Value: &high, Detail: "billed"}}},
	}
	got := aggregateObservationViews(views, 2)
	if got.Day != 4 || got.Complete || got.Ready || got.SuccessState != observationStateFail || got.Models != 2 {
		t.Fatalf("aggregate = %+v", got)
	}
	if got.DaysRequired != 6 || got.MinSamples != 12 || got.MinSuccessRate != 0.96 || got.LongContextTokens != 40_000 {
		t.Fatalf("aggregate bars = %+v, want the configured ones", got)
	}
	if got.SuccessRate == nil || *got.SuccessRate < 0.969 || *got.SuccessRate > 0.971 {
		t.Fatalf("pooled rate = %v, want 0.97", got.SuccessRate)
	}
	billing := got.Checks[0]
	if billing.State != observationStateFail || billing.Runs != 11 || billing.Passed != 10 || *billing.Value != high || billing.Detail != "billed" {
		t.Fatalf("billing = %+v", billing)
	}
}

func ptr(value float64) *float64 { return &value }

func setupRouteObservationControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	useTestSourceChecks(t)
	db := setupModelOpsControllerTestDB(t)
	if err := db.AutoMigrate(&model.RouteObservation{}, &model.RouteObservationCheck{}, &model.AdminAuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&model.Channel{
		Id: 40, PublicID: "ch-relay", Key: "k", Name: "Relay", Status: common.ChannelStatusEnabled,
		Group: model.ChannelLaneAgent, Models: "gpt-6-sol",
	}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := db.Create(&model.Ability{
		Group: model.ChannelLaneAgent, Model: "gpt-6-sol", ChannelId: 40, Enabled: true, Role: model.AbilityRoleInventory,
	}).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
	return db
}

// seedPassedObservation makes the relay's route one that has earned going
// live once its watch is over: enough clean probes, and every check passed.
func seedPassedObservation(t *testing.T, db *gorm.DB, started time.Time) {
	t.Helper()
	cell := model.RouteObservationCell{Lane: model.ChannelLaneAgent, ModelName: "gpt-6-sol", ChannelId: 40}
	if err := model.StartRouteObservations(nil, []model.RouteObservationCell{cell}, started); err != nil {
		t.Fatalf("start: %v", err)
	}
	samples := common.AppConfig.SourceChecks.MinSamples
	probes := make([]model.ChannelProbeResult, 0, samples)
	for i := 0; i < samples; i++ {
		probes = append(probes, model.ChannelProbeResult{
			Lane: cell.Lane, ModelName: cell.ModelName, ChannelId: 40,
			ProbedAt: time.Now().UTC().Add(-time.Duration(i+1) * time.Hour), Success: true, LatencyMs: 900,
		})
	}
	if err := db.Create(&probes).Error; err != nil {
		t.Fatalf("seed probes: %v", err)
	}
	for _, kind := range model.ObservationCheckKinds {
		if err := model.RecordRouteObservationCheck(model.RouteObservationCheck{
			Lane: cell.Lane, ModelName: cell.ModelName, ChannelId: 40, Kind: kind,
			CheckedAt: time.Now().UTC().Add(-time.Hour), Passed: true, Value: 1.0,
		}); err != nil {
			t.Fatalf("seed check: %v", err)
		}
	}
}

func putRole(role string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/channel_health/route_role", strings.NewReader(
		`{"channel_id":"ch-relay","lane":"cheap","model":"gpt-6-sol","role":"`+role+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	UpdateChannelHealthRouteRole(c)
	return recorder
}

func roleOf(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var ability model.Ability
	if err := db.Where("channel_id = ? AND model = ?", 40, "gpt-6-sol").First(&ability).Error; err != nil {
		t.Fatalf("load ability: %v", err)
	}
	return ability.Role
}

// A watched route cannot be taken live before its week is up.
func TestRouteRoleRefusesUnfinishedObservation(t *testing.T) {
	db := setupRouteObservationControllerTestDB(t)
	seedPassedObservation(t, db, time.Now().Add(-3*24*time.Hour))
	recorder := putRole(model.AbilityRoleBackup)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "route_observation_incomplete") {
		t.Fatalf("status = %d body = %s, want 409 route_observation_incomplete", recorder.Code, recorder.Body.String())
	}
	if role := roleOf(t, db); role != model.AbilityRoleInventory {
		t.Fatalf("role = %q, want still inventory", role)
	}
}

// Once it has passed, the route goes live and its observation ends.
func TestRouteRoleTakesPassedObservationLive(t *testing.T) {
	db := setupRouteObservationControllerTestDB(t)
	seedPassedObservation(t, db, time.Now().Add(-8*24*time.Hour))
	recorder := putRole(model.AbilityRoleBackup)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if role := roleOf(t, db); role != model.AbilityRoleBackup {
		t.Fatalf("role = %q, want backup", role)
	}
	var count int64
	db.Model(&model.RouteObservation{}).Count(&count)
	if count != 0 {
		t.Fatalf("observations = %d, want ended", count)
	}
}

func postGoLive(id string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/model_ops/sources/"+id+"/go_live", strings.NewReader(`{"role":"backup"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	GoLiveModelOpsSource(c)
	return recorder
}

func TestGoLiveSourceIsAllOrNothing(t *testing.T) {
	db := setupRouteObservationControllerTestDB(t)
	if recorder := postGoLive("ch-relay"); recorder.Code != http.StatusNotFound {
		t.Fatalf("nothing watched: status = %d, want 404", recorder.Code)
	}
	seedPassedObservation(t, db, time.Now().Add(-8*24*time.Hour))
	// A second model that joined yesterday holds the whole source back.
	if err := db.Create(&model.Ability{
		Group: model.ChannelLaneAgent, Model: "claude-fable-5-1", ChannelId: 40, Enabled: true, Role: model.AbilityRoleInventory,
	}).Error; err != nil {
		t.Fatalf("seed second ability: %v", err)
	}
	if err := model.StartRouteObservations(nil, []model.RouteObservationCell{
		{Lane: model.ChannelLaneAgent, ModelName: "claude-fable-5-1", ChannelId: 40},
	}, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("start second: %v", err)
	}
	if recorder := postGoLive("ch-relay"); recorder.Code != http.StatusConflict {
		t.Fatalf("partly ready: status = %d, want 409", recorder.Code)
	}
	if role := roleOf(t, db); role != model.AbilityRoleInventory {
		t.Fatalf("ready model went live alone: role = %q", role)
	}
	if err := model.EndRouteObservations(nil, 40, model.ChannelLaneAgent, []string{"claude-fable-5-1"}); err != nil {
		t.Fatalf("end second: %v", err)
	}
	if recorder := postGoLive("ch-relay"); recorder.Code != http.StatusOK {
		t.Fatalf("ready: status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if role := roleOf(t, db); role != model.AbilityRoleBackup {
		t.Fatalf("role = %q, want backup", role)
	}
}

// The tier card needs each source's role, its observation, and — for a route
// with no traffic — its probe record.
func TestModelOpsPoolsCarryRoleObservationAndProbeRecord(t *testing.T) {
	db := setupRouteObservationControllerTestDB(t)
	seedPassedObservation(t, db, time.Now().Add(-4*24*time.Hour))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/model_ops/pools?models=gpt-6-sol", nil)
	GetModelOpsPools(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Items map[string]map[string][]modelOpsPoolEntry `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	entries := payload.Data.Items["gpt-6-sol"][model.ChannelLaneAgent]
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	entry := entries[0]
	if entry.Role != model.AbilityRoleInventory || entry.Observation == nil {
		t.Fatalf("entry = %+v, want an inventory route under observation", entry)
	}
	if entry.Observation.Day != 5 || entry.Observation.Complete || entry.Observation.Ready {
		t.Fatalf("observation = %+v, want day 5 and not ready", entry.Observation)
	}
	if entry.Health.Source != "probe" || entry.Health.RequestCount == 0 || len(entry.Health.Buckets) == 0 {
		t.Fatalf("health = %+v, want the probe record", entry.Health)
	}
}

// fakeObservationUpstream is an OpenAI-compatible upstream with its own
// tokenizer (about 3.7 characters a token, unlike ours) and one of the faults
// the checks exist to catch — or one of the quirks they must not mistake for
// a fault:
//   - framing: a chat template that bills a fixed few dozen tokens on every
//     request, over the test's ratio but inside its framing allowance;
//   - thinks: a model that spends a small output cap on reasoning;
//   - chattools: a reasoning model that takes tools only on Responses.
func fakeObservationUpstream(t *testing.T, fault string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Input     string `json:"input"`
			Tools     []any  `json:"tools"`
			MaxTokens int    `json:"max_tokens"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/responses") {
			if fault != "chattools" || len(body.Tools) == 0 {
				t.Errorf("unexpected Responses request (fault %q)", fault)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "resp_1", "object": "response", "status": "completed", "model": "gpt-6-sol",
				"output": []any{map[string]any{
					"type": "function_call", "id": "fc_1", "call_id": "call_1", "status": "completed",
					"name": "get_weather", "arguments": `{"city":"Paris"}`,
				}},
				"usage": map[string]any{"input_tokens": 60, "output_tokens": 12, "total_tokens": 72},
			})
			return
		}
		if fault == "chattools" && len(body.Tools) > 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"message": "Function tools with reasoning_effort are not supported for gpt-6-sol in /v1/chat/completions.",
				"type":    "invalid_request_error", "param": "reasoning_effort",
			}})
			return
		}
		text := ""
		for _, message := range body.Messages {
			text += message.Content
		}
		prompt := len(text)*10/37 + 7
		if fault == "inject" {
			prompt += 4200
		}
		if fault == "framing" {
			prompt += 60
		}
		reply := "OK"
		if strings.HasPrefix(text, "Remember this code word: ") {
			word := strings.Fields(strings.TrimPrefix(text, "Remember this code word: "))[0]
			reply = word
			if fault == "truncate" && prompt > 20_000 {
				prompt = 20_000
				reply = "I cannot find a code word."
			}
			if fault == "thinks" && body.MaxTokens < 200 {
				reply = ""
			}
		}
		message := map[string]any{"role": "assistant", "content": reply}
		if len(body.Tools) > 0 && fault != "notools" {
			message = map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
				"id": "call_1", "type": "function",
				"function": map[string]any{"name": "get_weather", "arguments": `{"city":"Paris"}`},
			}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-1", "object": "chat.completion", "model": "gpt-6-sol",
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": prompt, "completion_tokens": 3, "total_tokens": prompt + 3},
		})
	}))
}

func seedObservationUpstreamRoute(t *testing.T, db *gorm.DB, baseURL string) model.RouteObservationCell {
	t.Helper()
	seedMediaJobControllerAuth(t, db)
	if err := db.Create(&model.Channel{
		Id: 70, Type: 1, Key: "upstream-key", Status: common.ChannelStatusEnabled, Name: "fake",
		Models: "gpt-6-sol", Group: model.ChannelLaneAgent, BaseURL: &baseURL,
	}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := db.Create(&model.Ability{
		Group: model.ChannelLaneAgent, Model: "gpt-6-sol", ChannelId: 70, Enabled: true, Role: model.AbilityRoleInventory,
	}).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
	input, output := 0.3, 1.8
	if err := db.Create(&model.ModelSellPrice{
		ModelName: "gpt-6-sol", ChannelGroup: model.ChannelLaneAgent, InputPerMtok: &input, OutputPerMtok: &output,
		Active: true, PricingVersion: 1, Source: "test",
	}).Error; err != nil {
		t.Fatalf("seed price: %v", err)
	}
	model.RefreshPricing()
	return model.RouteObservationCell{Lane: model.ChannelLaneAgent, ModelName: "gpt-6-sol", ChannelId: 70}
}

// The three checks, end to end through the real relay path, against an
// honest upstream and against each fault they exist to catch.
func TestObservationChecksAgainstFakeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useTestSourceChecks(t)
	// Both the API and the worker initialise the local tokenizer at startup.
	service.InitTokenEncoders()
	cases := []struct {
		fault string
		want  map[string]bool
	}{
		{"", map[string]bool{"billing": true, "long_context": true, "tools": true}},
		{"inject", map[string]bool{"billing": false, "long_context": true, "tools": true}},
		{"truncate", map[string]bool{"billing": true, "long_context": false, "tools": true}},
		{"notools", map[string]bool{"billing": true, "long_context": true, "tools": false}},
		{"framing", map[string]bool{"billing": true, "long_context": true, "tools": true}},
		{"thinks", map[string]bool{"billing": true, "long_context": true, "tools": true}},
		{"chattools", map[string]bool{"billing": true, "long_context": true, "tools": true}},
	}
	for _, tc := range cases {
		t.Run("fault="+tc.fault, func(t *testing.T) {
			db := setupMediaJobControllerTestDB(t)
			upstream := fakeObservationUpstream(t, tc.fault)
			defer upstream.Close()
			cell := seedObservationUpstreamRoute(t, db, upstream.URL)
			if tc.fault == "chattools" {
				// An official OpenAI surface serves Responses natively; a
				// chat-compatible reseller does not, and is never re-asked.
				if err := db.Model(&model.Channel{}).Where("id = ?", cell.ChannelId).
					Update("settings", `{"protocol_dialect":"openai"}`).Error; err != nil {
					t.Fatalf("set dialect: %v", err)
				}
			}
			for _, kind := range model.ObservationCheckKinds {
				check := runObservationCheck(t.Context(), cell, kind, 0)
				if check.Skipped {
					t.Fatalf("%s skipped: %s", kind, check.Detail)
				}
				if check.Passed != tc.want[kind] {
					t.Fatalf("%s passed = %t (value %.3f, %q), want %t", kind, check.Passed, check.Value, check.Detail, tc.want[kind])
				}
				if kind == model.ObservationCheckBilling && tc.fault == "" && (check.Value < 0.99 || check.Value > testSourceChecks.MaxBillingRatio) {
					t.Fatalf("honest billing ratio = %.3f, want about 1", check.Value)
				}
			}
		})
	}
}

// A backup that was never watched, or finished its watch, can become a
// primary: the observation gate only holds back routes still under watch.
func TestRouteRolePromotesBackupToPrimary(t *testing.T) {
	db := setupRouteObservationControllerTestDB(t)
	if err := db.Model(&model.Ability{}).Where("channel_id = ?", 40).
		Update("role", model.AbilityRoleBackup).Error; err != nil {
		t.Fatalf("make backup: %v", err)
	}
	recorder := putRole(model.AbilityRolePrimary)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if role := roleOf(t, db); role != model.AbilityRolePrimary {
		t.Fatalf("role = %q, want primary", role)
	}
}
