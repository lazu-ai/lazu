package router

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay/metrics"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/pkg/billing"
)

type decisionSnapshotMetrics struct {
	metrics.Store
	pre, post atomic.Int32
	category  atomic.Int32
}

func (s *decisionSnapshotMetrics) OnPreCall(channelID int) {
	s.pre.Add(1)
	s.Store.OnPreCall(channelID)
}

func (s *decisionSnapshotMetrics) OnPostCall(channelID int, latency time.Duration, tokens int, category metrics.ErrorCategory) {
	s.post.Add(1)
	s.category.Store(int32(category))
	s.Store.OnPostCall(channelID, latency, tokens, category)
}

// The existing embedded router fixture exercises actual parsing, authorization,
// channel selection, DecisionHelper, pricing and wallet settlement. Native Redis
// carries its batched telemetry through the real writer. This intentionally does
// not substitute a synthetic envelope for the handler's final usage/status.
func TestNativeRedisDecisionIntegrationSnapshotsOnce(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("isolated TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, refusal := range []bool{false, true} {
		name := "predicate"
		if refusal {
			name = "refusal"
		}
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			cleanup := setupModelCatalogRouteTestDB(t)
			defer cleanup()
			db := model.CoreDB()
			oldTelemetry := model.ConfiguredTelemetryDB()
			oldClient, oldStream := common.RDB, common.RelayEventStream
			oldRedis, oldQueue, oldConsumer, oldLogs := common.RedisEnabled, common.RelayEventQueueEnabled, common.RelayEventConsumerEnabled, common.LogConsumeEnabled
			oldSQLite, oldMemory, oldMetrics := common.UsingSQLite, common.MemoryCacheEnabled, metrics.Default
			client := redis.NewClient(options)
			stream := "lazu:test:decision-snapshot:" + common.GenerateRequestID()
			common.RDB, common.RelayEventStream = client, stream
			common.RedisEnabled, common.RelayEventQueueEnabled, common.RelayEventConsumerEnabled, common.LogConsumeEnabled = true, true, true, true
			common.UsingSQLite, common.MemoryCacheEnabled = true, false
			model.SetTelemetryDB(db)
			meter := &decisionSnapshotMetrics{Store: metrics.NewInMemoryStore()}
			metrics.Default = meter
			defer func() {
				drainRelayDiagnosticFixture(t)
				_ = client.Del(context.Background(), stream, stream+":failures", stream+":dead", stream+":dead_count", stream+":dropped").Err()
				common.RDB, common.RelayEventStream = oldClient, oldStream
				common.RedisEnabled, common.RelayEventQueueEnabled, common.RelayEventConsumerEnabled, common.LogConsumeEnabled = oldRedis, oldQueue, oldConsumer, oldLogs
				common.UsingSQLite, common.MemoryCacheEnabled, metrics.Default = oldSQLite, oldMemory, oldMetrics
				model.SetTelemetryDB(oldTelemetry)
				_ = client.Close()
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := client.Ping(ctx).Err(); err != nil {
				t.Fatal(err)
			}
			common.OptionMapRWMutex.Lock()
			oldOptions := common.OptionMap
			common.OptionMap = map[string]string{common.DecisionsEnabledOption: "true"}
			common.OptionMapRWMutex.Unlock()
			defer func() {
				common.OptionMapRWMutex.Lock()
				common.OptionMap = oldOptions
				common.OptionMapRWMutex.Unlock()
			}()
			service.InitTokenEncoders()
			if err := db.AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&model.Log{}, &model.WalletLedger{}, &model.RoutingDecision{}, &model.RoutingOutcome{}, &model.RoutingAttemptOutcome{}, &model.TelemetryEventReceipt{}, &model.SettlementOutbox{}, &model.SettlementFailure{}, &model.ProviderCostPrice{}, &model.AgentSession{}, &model.AgentSessionThread{}, &model.AgentSessionModel{}); err != nil {
				t.Fatal(err)
			}
			// Embedded billing may refund its conservative pre-charge. The net
			// exact charge and ledger row identities must remain stable on replay.
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			sqlDB.SetMaxOpenConns(1)
			fixtureID := 40_000 + int(decisionFixtureSequence.Add(10))
			user := model.User{Id: fixtureID, Username: "decision-snapshot", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			key := &model.Token{Id: fixtureID, UserId: fixtureID, Key: common.GenerateRequestID(), Name: "snapshot-fixture", Group: model.ChannelLaneDirect, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1), ModelLimitsEnabled: true, ModelLimits: dto.OpenAIDecisionModel}
			if err := key.Insert(); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/v1/decisions" || r.Header.Get("Authorization") != "Bearer snapshot-upstream" {
					t.Error("official native request identity/protocol changed")
				}
				var wire dto.OpenAIDecisionRequest
				if json.NewDecoder(r.Body).Decode(&wire) != nil || wire.Model != "gpt-6-luna" || len(wire.Questions) != 1 || !bytes.Contains(wire.Input, []byte("input_image")) {
					t.Error("native model, ordered questions or image lost upstream")
				}
				answer := `{"name":"red","type":"predicate","probability":0.99}`
				if refusal {
					answer = `{"name":"red","type":"refusal","reason":"insufficient evidence"}`
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"dec_synthetic","model":"gpt-6-luna","object":"decision","answers":[`+answer+`],"usage":{"input_tokens":1000,"output_tokens":0,"total_tokens":1000,"input_tokens_details":{"cached_tokens":200,"image_tokens":100,"text_tokens":700},"output_tokens_details":{"reasoning_tokens":0}}}`)
			}))
			defer upstream.Close()
			allowLocalRelayUpstream(t, upstream.URL)
			priority, noBan, tag := int64(10), 0, "openai"
			setting, base := `{"decision_dialect":"openai","max_in_flight":4}`, upstream.URL
			channel := model.Channel{Id: fixtureID, Type: constant.ChannelTypeOpenAI, Key: "snapshot-upstream", Status: common.ChannelStatusEnabled, Name: "official-decision", Models: dto.OpenAIDecisionModel, Group: model.ChannelLaneDirect, BaseURL: &base, Priority: &priority, AutoBan: &noBan, Setting: &setting, Tag: &tag}
			input, imageInput, zero, cost := .12, .12, 0.0, .10
			for _, row := range []interface{}{
				&channel,
				&model.Ability{ChannelId: channel.Id, Group: model.ChannelLaneDirect, Model: dto.OpenAIDecisionModel, Enabled: true, Priority: &priority},
				&model.Model{ModelName: dto.OpenAIDecisionModel, Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact, Capabilities: model.ModelCapabilities{SupportsVision: true}},
				&model.ModelSellPrice{ModelName: dto.OpenAIDecisionModel, ChannelGroup: model.ChannelLaneDirect, InputPerMtok: &input, ImageInputPerMtok: &imageInput, CacheReadPerMtok: &zero, OutputPerMtok: &zero, Active: true, PricingVersion: 73},
				&model.ProviderCostPrice{ModelName: dto.OpenAIDecisionModel, Provider: "openai", ChannelID: channel.Id, InputPerMtok: &cost, ImageInputPerMtok: &cost, CacheReadPerMtok: &zero, OutputPerMtok: &zero, PricingVersion: 73},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			model.RefreshPricing()
			var pngBody bytes.Buffer
			picture := image.NewRGBA(image.Rect(0, 0, 8, 8))
			picture.Set(0, 0, color.RGBA{R: 255, A: 255})
			if err := png.Encode(&pngBody, picture); err != nil {
				t.Fatal(err)
			}
			dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBody.Bytes())
			body, err := json.Marshal(map[string]interface{}{"model": "gpt-6-luna", "input": []interface{}{map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "input_text", "text": "Is the top-left pixel red?"}, map[string]interface{}{"type": "input_image", "image_url": dataURL}}}}, "questions": []interface{}{map[string]interface{}{"name": "red", "type": "predicate", "instructions": "Is the top-left pixel red?"}}})
			if err != nil {
				t.Fatal(err)
			}
			engine := gin.New()
			engine.Use(middleware.RequestId())
			var completed *gin.Context
			engine.Use(func(c *gin.Context) { c.Next(); completed = c.Copy() })
			SetRelayRouter(engine)
			request := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer sk-"+key.Key)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("native status=%d body=%s", response.Code, response.Body)
			}
			var reply struct {
				Model   string            `json:"model"`
				Answers []json.RawMessage `json:"answers"`
				Usage   struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil || reply.Model != "gpt-6-luna" || len(reply.Answers) != 1 || reply.Usage.InputTokens != 1000 {
				t.Fatalf("native official response/usage changed: %v %s", err, response.Body)
			}
			rid := response.Header().Get(common.RequestIdKey)
			if rid == "" || completed == nil || calls.Load() != 1 {
				t.Fatal("request identity, final context or single upstream attempt missing")
			}
			drainRelayDiagnosticFixture(t)
			messages, err := client.XRange(ctx, stream, "-", "+").Result()
			if err != nil {
				t.Fatal(err)
			}
			var replay map[string]interface{}
			for _, message := range messages {
				if message.Values["type"] == model.RelayEventTypeRelayResult {
					var payload map[string]interface{}
					if err := json.Unmarshal([]byte(message.Values["payload"].(string)), &payload); err != nil {
						t.Fatal(err)
					}
					if payload["consume_log"] != nil {
						if replay != nil {
							t.Fatal("multiple initial consume envelopes")
						}
						replay = payload
					}
				}
			}
			if replay == nil {
				t.Fatal("native consume log was not captured with its routing snapshot")
			}
			if _, err := model.DrainRelayEvents(ctx, "decision-snapshot-fixture", 8); err != nil {
				t.Fatal(err)
			}
			var log model.Log
			if err := db.Where("request_id = ?", rid).First(&log).Error; err != nil {
				t.Fatal(err)
			}
			var decision model.RoutingDecision
			var outcome model.RoutingOutcome
			if json.Unmarshal([]byte(log.RoutingDecisionJSON), &decision) != nil || json.Unmarshal([]byte(log.RoutingOutcomeJSON), &outcome) != nil {
				t.Fatal("final canonical log lacks complete decision/outcome JSON")
			}
			if decision.RequestID != rid || decision.ID == 0 || !decision.IsPrimary || decision.ChoseChannel == nil || *decision.ChoseChannel != channel.Id || decision.ChoseModel != dto.OpenAIDecisionModel || decision.ChoseProvider != "openai" || outcome.RequestID != rid || outcome.DecisionID != decision.ID {
				t.Fatalf("native routing identity incomplete: decision=%+v outcome=%+v", decision, outcome)
			}
			if outcome.ChannelID == nil || *outcome.ChannelID != channel.Id || outcome.StatusCode == nil || *outcome.StatusCode != 200 || outcome.ProviderAttempted == nil || !*outcome.ProviderAttempted || outcome.RetryCount == nil || *outcome.RetryCount != 0 || outcome.IsRetry == nil || *outcome.IsRetry || outcome.ErrorCode != "" || outcome.ErrorType != "" {
				t.Fatalf("native outcome captured before final provider/status: %+v", outcome)
			}
			if outcome.ActualInputTokens == nil || *outcome.ActualInputTokens != 1000 || outcome.CachedInputTokens == nil || *outcome.CachedInputTokens != 200 || outcome.ImageInputTokens == nil || *outcome.ImageInputTokens != 100 || outcome.ChargedCostUSD == nil || *outcome.ChargedCostUSD != .000096 {
				t.Fatalf("native outcome lost authoritative usage/settled amount: %+v", outcome)
			}
			if log.Type != model.LogTypeConsume || log.ModelName != dto.OpenAIDecisionModel || log.AmountMicrousd != 96 || log.PromptTokens != 1000 || log.CompletionTokens != 0 || log.PricingVersion != 73 || log.ChannelId != channel.Id || log.TokenId != key.Id {
				t.Fatalf("exact native consume log changed: %+v", log)
			}
			// Decode with explicit wire names rather than Go field-name matching.
			var fields map[string]json.RawMessage
			var metadata map[string]interface{}
			if json.Unmarshal([]byte(log.Other), &fields) != nil || json.Unmarshal(fields["decision"], &metadata) != nil || metadata["provider"] != "openai" || metadata["upstream_model"] != "gpt-6-luna" {
				t.Fatalf("native provider/usage provenance missing: %s", log.Other)
			}
			source, ok := metadata["usage_source"].(map[string]interface{})
			if !ok || source["input"] != "provider" || source["output"] != "provider" {
				t.Fatalf("provider usage measurement lost: %+v", metadata["usage_source"])
			}
			var updatedUser model.User
			var updatedKey model.Token
			deadline := time.Now().Add(2 * time.Second)
			for {
				if err := db.First(&updatedUser, user.Id).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.First(&updatedKey, key.Id).Error; err != nil {
					t.Fatal(err)
				}
				if user.Quota-updatedUser.Quota == 96 && key.RemainQuota-updatedKey.RemainQuota == 96 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("exact native charge did not settle once")
				}
				time.Sleep(10 * time.Millisecond)
			}
			var ledgerRows []model.WalletLedger
			if err := db.Where("user_id = ?", user.Id).Order("id").Find(&ledgerRows).Error; err != nil {
				t.Fatal(err)
			}
			assertOnce := func() {
				t.Helper()
				for _, target := range []struct {
					model interface{}
					where string
					id    string
				}{{&model.Log{}, "request_id = ?", rid}, {&model.TelemetryEventReceipt{}, "event_key = ?", "relay:" + rid}, {&model.RoutingDecision{}, "request_id = ?", rid}, {&model.RoutingOutcome{}, "request_id = ?", rid}} {
					var count int64
					if err := db.Model(target.model).Where(target.where, target.id).Count(&count).Error; err != nil || count != 1 {
						t.Fatalf("duplicate or absent persisted %T: count=%d err=%v", target.model, count, err)
					}
				}
				var dimension model.DashboardUsageDailyDimension
				if err := db.Where("dimension_type = ? AND dimension_id = ? AND model_name = ?", "key", key.Id, dto.OpenAIDecisionModel).First(&dimension).Error; err != nil || dimension.RequestCount != 1 || dimension.AmountMicrousd != 96 || dimension.PromptTokens != 1000 {
					t.Fatalf("telemetry aggregate counted more than once: %+v err=%v", dimension, err)
				}
				if meter.pre.Load() != 1 || meter.post.Load() != 1 || meter.category.Load() != int32(metrics.CategoryOK) || meter.Snapshot(channel.Id).InFlight != 0 || calls.Load() != 1 {
					t.Fatalf("manual native metrics duplicated: pre=%d post=%d provider=%d", meter.pre.Load(), meter.post.Load(), calls.Load())
				}
			}
			assertOnce()
			// A late finalization and at-least-once stream delivery cannot replace
			// the rich original fact or duplicate its receipt, counters or charge.
			middleware.RecordManualRoutingOutcome(completed)
			for range 2 {
				if err := model.PublishRelayEvent(model.RelayEventTypeRelayResult, replay); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := model.DrainRelayEvents(ctx, "decision-snapshot-fixture", 8); err != nil {
				t.Fatal(err)
			}
			assertOnce()
			var replayed model.Log
			if err := db.Where("request_id = ?", rid).First(&replayed).Error; err != nil || replayed.RoutingDecisionJSON != log.RoutingDecisionJSON || replayed.RoutingOutcomeJSON != log.RoutingOutcomeJSON || replayed.AmountMicrousd != log.AmountMicrousd {
				t.Fatal("late/replayed event changed the canonical routing/financial fact")
			}
			var afterRows []model.WalletLedger
			if err := db.Where("user_id = ?", user.Id).Order("id").Find(&afterRows).Error; err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(ledgerRows)
			after, _ := json.Marshal(afterRows)
			if !bytes.Equal(before, after) {
				t.Fatal("telemetry replay mutated wallet settlement")
			}
			var finalUser model.User
			var finalKey model.Token
			if db.First(&finalUser, user.Id).Error != nil || db.First(&finalKey, key.Id).Error != nil || finalUser.Quota != updatedUser.Quota || finalKey.RemainQuota != updatedKey.RemainQuota {
				t.Fatal("late/replayed event charged the wallet or key again")
			}
			if pending, err := client.XPending(ctx, stream, common.RelayEventGroup).Result(); err != nil || pending.Count != 0 {
				t.Fatalf("native telemetry pending=%+v err=%v", pending, err)
			}
			if dead, err := client.XLen(ctx, stream+":dead").Result(); err != nil || dead != 0 {
				t.Fatalf("native telemetry dead=%d err=%v", dead, err)
			}
		})
	}
}
