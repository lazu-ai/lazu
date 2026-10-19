package router

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/pkg/billing"
)

var decisionFixtureSequence atomic.Int32

func TestDecisionIntegration(t *testing.T) {
	for _, scenario := range []string{"native", "explicit-precedence", "generated", "invalid-generation", "native-failure", "invalid-native", "generator-forbidden", "ephemeral-forbidden", "generator-capability", "budget-denied", "native-fallback", "validation", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			// Rate-limit state survives individual database fixtures and -count.
			fixtureID := 10_000 + int(decisionFixtureSequence.Add(10))
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
			common.OptionMapRWMutex.Lock()
			oldOptions := common.OptionMap
			common.OptionMap = map[string]string{common.DecisionsEnabledOption: "true", common.DecisionQuestionGenerationEnabledOption: "true"}
			if scenario == "disabled" {
				common.OptionMap[common.DecisionsEnabledOption] = "false"
			}
			common.OptionMapRWMutex.Unlock()
			defer func() {
				common.OptionMapRWMutex.Lock()
				common.OptionMap = oldOptions
				common.OptionMapRWMutex.Unlock()
			}()
			service.InitTokenEncoders()
			if err := model.CoreDB().AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
				t.Fatal(err)
			}
			if err := model.CoreDB().AutoMigrate(&model.Log{}, &model.WalletLedger{}, &model.RoutingDecision{}, &model.RoutingOutcome{}, &model.RoutingAttemptOutcome{}, &model.TelemetryEventReceipt{}, &model.DecisionGenerationRefund{}, &model.SettlementOutbox{}, &model.AgentSession{}, &model.AgentSessionThread{}, &model.AgentSessionModel{}); err != nil {
				t.Fatal(err)
			}
			var nativeCalls, generatorCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/systemone" {
					nativeCalls.Add(1)
					var request map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if string(request["model"]) != `"jev-1.13.0"` {
						t.Errorf("model mapping=%s", request["model"])
					}
					if _, leaked := request["question_generation"]; leaked {
						t.Error("gateway control leaked")
					}
					if scenario == "invalid-native" {
						io.WriteString(w, `{"model":"jev-1.13.0","answers":{},"usage":{"input_tokens":457,"output_tokens":70}}`)
						return
					}
					if scenario == "native-failure" || (scenario == "native-fallback" && nativeCalls.Load() == 1) {
						w.WriteHeader(503)
						io.WriteString(w, `{"error":{"message":"busy"}}`)
						return
					}
					io.WriteString(w, `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.98,"confidence":0.91}},"usage":{"input_tokens":457,"output_tokens":70}}`)
					return
				}
				if r.URL.Path == "/v1/responses" {
					generatorCalls.Add(1)
					generated := `{"questions":[{"id":"urgent","type":"noul","instructions":"是否紧急？","choices":[],"levels":[]}]}`
					if scenario == "invalid-generation" {
						generated = `{"questions":[{"id":"urgent","type":"score","instructions":"是否紧急？","choices":[],"levels":[]}]}`
					}
					json.NewEncoder(w).Encode(map[string]interface{}{"id": "resp_fixture", "object": "response", "model": "gpt-6-luna", "status": "completed", "output": []interface{}{map[string]interface{}{"type": "message", "id": "msg_fixture", "role": "assistant", "status": "completed", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": generated, "annotations": []interface{}{}}}}}, "usage": map[string]int{"input_tokens": 20, "output_tokens": 10, "total_tokens": 30}})
					return
				}
				t.Errorf("wrong path=%s", r.URL.Path)
				w.WriteHeader(404)
			}))
			defer upstream.Close()
			allowLocalRelayUpstream(t, upstream.URL)
			user := model.User{Id: fixtureID, Username: "decision-integration", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}
			if err := model.CoreDB().Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			key := &model.Token{Id: fixtureID, UserId: fixtureID, Key: "decision-integration-key", Group: model.ChannelLaneDirect, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1)}
			if scenario == "explicit-precedence" {
				key.DecisionQuestionGeneration.Enabled = true
			}
			if scenario == "budget-denied" {
				key.RemainQuota = 1
			}
			if scenario == "generator-forbidden" {
				key.ModelLimitsEnabled = true
				key.ModelLimits = "jev-1.13"
			}
			if err := key.Insert(); err != nil {
				t.Fatal(err)
			}
			for i, name := range []string{"jev-1.13", "gpt-6-luna"} {
				priority, noBan := int64(10), 0
				setting := `{"decision_dialect":"typesafe","max_in_flight":4}`
				other := `{"protocol_dialect":"openai","capabilities":{"supports_responses":true,"supports_vision":true}}`
				if i == 1 {
					setting = `{}`
				}
				base := upstream.URL
				ch := model.Channel{Id: fixtureID + i, Type: constant.ChannelTypeOpenAI, Key: "fixture-only", Status: common.ChannelStatusEnabled, Name: name, Models: name, Group: model.ChannelLaneDirect, BaseURL: &base, Priority: &priority, AutoBan: &noBan, Setting: &setting, OtherSettings: other}
				if err := model.CoreDB().Create(&ch).Error; err != nil {
					t.Fatal(err)
				}
				if err := model.CoreDB().Create(&model.Ability{ChannelId: ch.Id, Group: model.ChannelLaneDirect, Model: name, Enabled: true, Priority: &priority}).Error; err != nil {
					t.Fatal(err)
				}
				if err := model.CoreDB().Create(&model.Model{ModelName: name, Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact, Capabilities: model.ModelCapabilities{SupportsResponseSchema: i == 1 && scenario != "generator-capability", SupportsVision: i == 1}}).Error; err != nil {
					t.Fatal(err)
				}
				in, out := 0.042, 0.0
				if i == 1 {
					in, out = 1, 10
				}
				if err := model.CoreDB().Create(&model.ModelSellPrice{ModelName: name, ChannelGroup: model.ChannelLaneDirect, InputPerMtok: &in, OutputPerMtok: &out, Active: true, PricingVersion: 1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "native-fallback" {
				priority, noBan := int64(5), 0
				setting, base := `{"decision_dialect":"typesafe","max_in_flight":4}`, upstream.URL
				ch := model.Channel{Id: fixtureID + 2, Type: constant.ChannelTypeOpenAI, Key: "fixture-backup", Status: common.ChannelStatusEnabled, Name: "backup", Models: "jev-1.13", Group: model.ChannelLaneDirect, BaseURL: &base, Priority: &priority, AutoBan: &noBan, Setting: &setting}
				if err := model.CoreDB().Create(&ch).Error; err != nil {
					t.Fatal(err)
				}
				if err := model.CoreDB().Create(&model.Ability{ChannelId: ch.Id, Group: model.ChannelLaneDirect, Model: "jev-1.13", Enabled: true, Priority: &priority}).Error; err != nil {
					t.Fatal(err)
				}
			}
			model.RefreshPricing()
			engine := gin.New()
			engine.Use(middleware.RequestId())
			SetRelayRouter(engine)
			body := `{"model":"jev-1.13","state":"服务器宕机，需要立即修复。","questions":{"urgent":{"type":"noul","instructions":"是否紧急？"}}}`
			if scenario != "native" && scenario != "explicit-precedence" && scenario != "validation" && scenario != "disabled" {
				body = `{"model":"jev-1.13","state":"服务器宕机，需要立即修复。","question_generation":{"enabled":true,"model":"gpt-6-luna"}}`
			}
			if scenario == "validation" {
				body = `{"model":"jev-1.13","state":"x","questions":{"urgent":{"type":"noul","instructions":"x"}},"stream":true}`
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewBufferString(body))
			request.Header.Set("Content-Type", "application/json")
			if scenario != "disabled" {
				if scenario == "ephemeral-forbidden" {
					credential, err := model.IssueEphemeralCredential(model.IssueEphemeralCredentialInput{UserID: fixtureID, TokenID: fixtureID, Models: []string{"jev-1.13"}})
					if err != nil {
						t.Fatal(err)
					}
					request.Header.Set("Authorization", "Bearer "+credential.Credential)
				} else {
					request.Header.Set("Authorization", "Bearer sk-"+key.Key)
				}
			}
			result := httptest.NewRecorder()
			engine.ServeHTTP(result, request)
			want := 200
			switch scenario {
			case "invalid-generation", "native-failure", "invalid-native":
				want = 502
			case "generator-forbidden", "ephemeral-forbidden":
				want = 403
			case "validation", "generator-capability":
				want = 400
			case "budget-denied":
				want = 429
			case "disabled":
				want = 404
			}
			if scenario == "native-failure" {
				if result.Code < 500 {
					t.Fatalf("expected failure got%d %s", result.Code, result.Body)
				}
			} else if result.Code != want {
				t.Fatalf("status=%d want=%d body=%s", result.Code, want, result.Body)
			}
			if scenario == "generator-forbidden" || scenario == "ephemeral-forbidden" || scenario == "generator-capability" || scenario == "budget-denied" || scenario == "validation" || scenario == "disabled" {
				if nativeCalls.Load() != 0 || generatorCalls.Load() != 0 {
					t.Fatal("rejected request reached provider")
				}
				if scenario == "ephemeral-forbidden" {
					var denied int64
					model.CoreDB().
						Model(&model.EphemeralCredentialAudit{}).Where("event_type = ? AND model_name = ?", model.EphemeralCredentialAuditDenied, "gpt-6-luna").Count(&denied)
					if denied != 1 {
						t.Fatalf("missing generator-scope denial audit: %d", denied)
					}
				}
				return
			}
			var updated model.Token
			// Native failures refund their pre-charge in background settlement.
			// Keep this fixture alive until both money balances settle; otherwise
			// the next scenario swaps the global DB underneath that refund.
			deadline := time.Now().Add(2 * time.Second)
			for {
				if err := model.CoreDB().First(&updated, key.Id).Error; err != nil {
					t.Fatal(err)
				}
				if scenario != "native-failure" && scenario != "invalid-native" {
					break
				}
				var settledUser model.User
				if err := model.CoreDB().First(&settledUser, user.Id).Error; err != nil {
					t.Fatal(err)
				}
				if key.RemainQuota-updated.RemainQuota == 120 && user.Quota-settledUser.Quota == 120 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("native refund did not settle: key charged %d wallet charged %d", key.RemainQuota-updated.RemainQuota, user.Quota-settledUser.Quota)
				}
				time.Sleep(10 * time.Millisecond)
			}
			var logs []model.Log
			model.CoreDB().
				Where("token_id = ? AND type IN ?", key.Id, []int{model.LogTypeConsume, model.LogTypeRefund}).Find(&logs)
			var charged billing.MicroUSD
			for _, log := range logs {
				charged += log.AmountMicrousd
			}
			if key.RemainQuota-updated.RemainQuota != charged {
				t.Fatalf("key charged %d logsum %d logs%+v", key.RemainQuota-updated.RemainQuota, charged, logs)
			}
			switch scenario {
			case "native", "explicit-precedence":
				if nativeCalls.Load() != 1 || len(logs) != 1 || charged != 19 {
					t.Fatalf("native billing/calls=%d charged=%d logs=%+v", nativeCalls.Load(), charged, logs)
				}
				if generatorCalls.Load() != 0 {
					t.Fatal("explicit questions triggered paid generation")
				}
				if scenario == "explicit-precedence" && !bytes.Contains(result.Body.Bytes(), []byte("explicit questions take precedence")) {
					t.Fatal("missing precedence warning")
				}
				if logs[0].CompletionTokens != 70 {
					t.Fatal("real output usage lost")
				}
			case "generated", "native-fallback":
				expectedNative := int32(1)
				if scenario == "native-fallback" {
					expectedNative = 2
				}
				if len(logs) != 2 || generatorCalls.Load() != 1 || nativeCalls.Load() != expectedNative {
					t.Fatalf("expected two independent calls logs=%+v", logs)
				}
				var reply map[string]json.RawMessage
				json.Unmarshal(result.Body.Bytes(), &reply)
				if len(reply["questions"]) == 0 || len(reply["question_generation"]) == 0 {
					t.Fatal("generated questions and usage missing")
				}
			case "invalid-generation":
				if charged != 0 || nativeCalls.Load() != 0 || len(logs) != 2 {
					t.Fatalf("invalid output not refunded: charge%d native%d logs%+v", charged, nativeCalls.Load(), logs)
				}
			case "native-failure", "invalid-native":
				if generatorCalls.Load() != 1 || !bytes.Contains(result.Body.Bytes(), []byte(`"questions"`)) {
					t.Fatalf("failure lost reusable questions: %s", result.Body)
				}
				if charged != 120 {
					t.Fatalf("should retain generation only, charge%d", charged)
				}
			}
			t.Logf("scenario=%s native=%d generator=%d charged=%d status=%d", scenario, nativeCalls.Load(), generatorCalls.Load(), charged, result.Code)
		})
	}
}
