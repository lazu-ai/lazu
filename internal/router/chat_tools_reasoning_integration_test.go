package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// gpt-6 refuses function tools on Chat Completions while it reasons. Through
// the real relay, a Chat request with tools and no effort of its own reaches
// the upstream with reasoning off — on the native Chat wire and across the
// Responses bridge alike — and an effort the client chose passes untouched.
func TestChatToolsReachGPT6WithReasoningOff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name     string
		settings string
		effort   string
		wantPath string
		want     string
	}{
		{"native chat", `{"protocol_dialect":"openai"}`, "", "/v1/chat/completions", "none"},
		{"responses bridge", `{"protocol_dialect":"openai","capabilities":{"supports_chat_completions":false,"supports_responses":true}}`, "", "/v1/responses", "none"},
		{"client effort kept", `{"protocol_dialect":"openai"}`, "high", "/v1/chat/completions", "high"},
		{"reseller untouched", `{"protocol_dialect":"openai_chat_compatible"}`, "", "/v1/chat/completions", ""},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			if err := model.CoreDB().AutoMigrate(&model.WalletLedger{}); err != nil {
				t.Fatal(err)
			}
			var gotPath, gotEffort string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					ReasoningEffort string `json:"reasoning_effort"`
					Reasoning       *struct {
						Effort string `json:"effort"`
					} `json:"reasoning"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				gotPath, gotEffort = r.URL.Path, body.ReasoningEffort
				if body.Reasoning != nil {
					gotEffort = body.Reasoning.Effort
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/responses") {
					io.WriteString(w, `{"id":"resp_1","object":"response","status":"completed","model":"gpt-6-astra","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","status":"completed","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}],"usage":{"input_tokens":40,"output_tokens":12,"total_tokens":52}}`)
					return
				}
				io.WriteString(w, `{"id":"chat_1","object":"chat.completion","model":"gpt-6-sol","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]}}],"usage":{"prompt_tokens":40,"completion_tokens":12,"total_tokens":52}}`)
			}))
			defer upstream.Close()
			allowLocalRelayUpstream(t, upstream.URL)
			id := 1200 + index
			if err := model.CoreDB().Create(&model.User{Id: id, Username: fmt.Sprintf("tools%d", id), Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}).Error; err != nil {
				t.Fatal(err)
			}
			key := &model.Token{Id: id, UserId: id, Key: fmt.Sprintf("chat-tools-fixture-key-%d", id), Group: model.ChannelLaneDirect, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1)}
			if err := key.Insert(); err != nil {
				t.Fatal(err)
			}
			url, priority, noBan := upstream.URL, int64(10), 0
			channel := model.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Key: "local-upstream", Status: common.ChannelStatusEnabled, Name: "fixture", Models: "gpt-6-sol", Group: model.ChannelLaneDirect, BaseURL: &url, Priority: &priority, AutoBan: &noBan, OtherSettings: tc.settings}
			if err := model.CoreDB().Create(&channel).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.CoreDB().Create(&model.Ability{ChannelId: channel.Id, Group: model.ChannelLaneDirect, Model: "gpt-6-sol", Enabled: true, Priority: &priority}).Error; err != nil {
				t.Fatal(err)
			}
			input, output := 1.0, 10.0
			if err := model.CoreDB().Create(&model.Model{ModelName: "gpt-6-sol", Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact}).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.CoreDB().Create(&model.ModelSellPrice{ModelName: "gpt-6-sol", ChannelGroup: model.ChannelLaneDirect, InputPerMtok: &input, OutputPerMtok: &output, Active: true, PricingVersion: 1}).Error; err != nil {
				t.Fatal(err)
			}
			model.RefreshPricing()
			engine := gin.New()
			engine.Use(middleware.RequestId())
			SetRelayRouter(engine)
			gateway := httptest.NewServer(engine)
			defer gateway.Close()
			request := map[string]any{
				"model":      "gpt-6-sol",
				"messages":   []any{map[string]any{"role": "user", "content": "Weather in Paris?"}},
				"max_tokens": 128,
				"tools": []any{map[string]any{"type": "function", "function": map[string]any{
					"name": "get_weather", "parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
				}}},
			}
			if tc.effort != "" {
				request["reasoning_effort"] = tc.effort
			}
			body, _ := json.Marshal(request)
			req, _ := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer sk-"+key.Key)
			req.Header.Set("Content-Type", "application/json")
			resp, err := gateway.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", resp.StatusCode, raw)
			}
			if gotPath != tc.wantPath || gotEffort != tc.want {
				t.Fatalf("upstream got %s effort %q, want %s effort %q", gotPath, gotEffort, tc.wantPath, tc.want)
			}
			if !bytes.Contains(raw, []byte(`"tool_calls"`)) || !bytes.Contains(raw, []byte(`get_weather`)) {
				t.Fatalf("client reply lost the tool call: %s", raw)
			}
		})
	}
}
