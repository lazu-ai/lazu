package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay/metrics"
	"gorm.io/gorm"
)

type decisionTelemetryRequiredDialect struct{ gorm.Dialector }

func (decisionTelemetryRequiredDialect) Name() string { return "postgres" }

// This test isolates the distributor's capture boundary. The router integration
// tests separately exercise the real adapter, billing and Redis telemetry writer.
func TestDistributeSuppressedDecisionsCapturesSettlementTelemetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, modelName := range []string{dto.OpenAIDecisionModel, "jev-1.13"} {
		t.Run(modelName, func(t *testing.T) {
			cleanup := setupDistributorTestDB(t)
			defer cleanup()
			oldMetrics, oldConsume, oldQueue := metrics.Default, common.LogConsumeEnabled, common.RelayEventQueueEnabled
			metrics.Default, common.LogConsumeEnabled, common.RelayEventQueueEnabled = metrics.NewInMemoryStore(), true, false
			defer func() {
				metrics.Default, common.LogConsumeEnabled, common.RelayEventQueueEnabled = oldMetrics, oldConsume, oldQueue
			}()
			channel := &model.Channel{Id: 7491, Type: constant.ChannelTypeOpenAI, Key: "synthetic-upstream-key", Status: common.ChannelStatusEnabled, Name: "decision-capture", Models: modelName, Group: model.ChannelLaneDirect}
			if err := model.CoreDB().Create(channel).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.CoreDB().Create(&model.Ability{Group: model.ChannelLaneDirect, Model: modelName, ChannelId: channel.Id, Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
			if err := (&model.Model{ModelName: modelName, Status: 1, LifecycleStatus: model.ModelLifecycleActive}).Insert(); err != nil {
				t.Fatal(err)
			}
			model.RefreshPricing()
			requestID := "req-decision-capture-" + modelName
			spool := &ledger.Engine{SpoolDir: filepath.Join(t.TempDir(), "spool")}
			var captured bool
			engine := gin.New()
			engine.POST("/v1/decisions", func(c *gin.Context) {
				c.Set(common.RequestIdKey, requestID)
				c.Set("decision_request", &dto.DecisionRequest{Model: modelName})
				common.SetContextKey(c, constant.ContextKeyUserId, 1)
				common.SetContextKey(c, constant.ContextKeyTokenId, 1)
				common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, fmt.Sprint(channel.Id))
				c.Next()
			}, SuppressRelayTelemetry(), Distribute(), func(c *gin.Context) {
				if got := metrics.Default.Snapshot(channel.Id); got.InFlight != 0 || got.RPMCurrent != 0 {
					t.Errorf("distributor duplicated manual pre-call metrics: %+v", got)
				}
				started := time.Now()
				RecordManualRelayPreCall(channel.Id)
				common.SetContextKey(c, constant.ContextKeyProviderAttempted, true)
				c.Set("use_channel", []string{fmt.Sprint(channel.Id)})
				RecordManualRelayPostCall(channel.Id, started, http.StatusOK, "")

				// Force the same no-SQL-fallback envelope policy as PostgreSQL,
				// after SQLite has completed routing. No Redis connection is used.
				db := model.CoreDB()
				dialect := db.Dialector
				db.Dialector = decisionTelemetryRequiredDialect{dialect}
				defer func() { db.Dialector = dialect }()
				var sqlWrites int
				callback := "decision_capture:no_sync_create"
				if err := db.Callback().Create().Before("gorm:create").Register(callback, func(*gorm.DB) { sqlWrites++ }); err != nil {
					t.Fatal(err)
				}
				defer db.Callback().Create().Remove(callback)
				model.RecordConsumeLog(c, 1, model.RecordConsumeLogParams{ModelName: modelName, PromptTokens: 171, CompletionTokens: 3, AmountMicrousd: 21, ChannelId: channel.Id, TokenId: 1, Group: model.ChannelLaneDirect})
				envelope, complete, err := model.BuildRelayResultEnvelope(c)
				if err != nil || !complete {
					t.Errorf("settlement must capture consume plus routing before the manual defer: complete=%v err=%v", complete, err)
					c.Status(http.StatusOK)
					return
				}
				var event struct {
					ConsumeLog *struct {
						RequestID string `json:"request_id"`
						Amount    int64  `json:"amount_microusd"`
					} `json:"consume_log"`
					Decision *model.RoutingDecision `json:"routing_decision"`
					Outcome  *model.RoutingOutcome  `json:"routing_outcome"`
				}
				if err := json.Unmarshal([]byte(envelope), &event); err != nil {
					t.Fatal(err)
				}
				if event.ConsumeLog == nil || event.Decision == nil || event.Outcome == nil {
					t.Fatalf("settlement envelope lacks a required fact: %s", envelope)
				}
				outcome := event.Outcome
				if event.ConsumeLog.RequestID != requestID || event.ConsumeLog.Amount != 21 || event.Decision.RequestID != requestID || outcome.RequestID != requestID || outcome.DecisionID != event.Decision.ID || outcome.ModelName != modelName {
					t.Fatalf("mismatched financial/routing identity: %s", envelope)
				}
				if outcome.StatusCode == nil || *outcome.StatusCode != 200 || outcome.ProviderAttempted == nil || !*outcome.ProviderAttempted || outcome.HealthEligible == nil || !*outcome.HealthEligible || outcome.HealthFailed == nil || *outcome.HealthFailed || outcome.ActualInputTokens == nil || *outcome.ActualInputTokens != 171 || outcome.ActualOutputTokens == nil || *outcome.ActualOutputTokens != 3 {
					t.Fatalf("routing snapshot lost status, health or provider usage: %+v", outcome)
				}
				input := ledger.Input{RequestID: requestID, PayerID: 1, ActorID: 1, TokenID: 1, Amount: 21, OccurredAt: time.Now().UTC()}
				for range 2 {
					if err := spool.PersistSettlementWithTelemetry(input, 21, "settle", envelope); err != nil {
						t.Fatal(err)
					}
				}
				model.MarkRelayResultPublished(c)
				RecordManualRoutingOutcome(c)
				if sqlWrites != 0 {
					t.Fatalf("capture or late manual flush made %d synchronous SQL writes", sqlWrites)
				}
				files, err := filepath.Glob(filepath.Join(spool.SpoolDir, "*.json"))
				if err != nil || len(files) != 1 {
					t.Fatalf("settlement replay created duplicate financial intents: files=%v err=%v", files, err)
				}
				raw, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				var durable ledger.Event
				if err := json.Unmarshal(raw, &durable); err != nil || durable.TelemetryEnvelope != envelope || durable.Amount != 21 {
					t.Fatalf("durable financial intent lost the complete envelope: %+v err=%v", durable, err)
				}
				captured = true
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, req)
			if response.Code != 200 || !captured {
				t.Fatalf("capture handler did not finish: status=%d body=%s", response.Code, response.Body.String())
			}
			if got := metrics.Default.Snapshot(channel.Id); got.InFlight != 0 || got.SamplesCount != 1 || got.RPMCurrent != 1 || got.ErrorWindowSize != 1 || got.ErrorRate != 0 {
				t.Fatalf("manual health must be recorded exactly once: %+v", got)
			}
		})
	}
}

func TestDistributeSuppressedOtherRoutesKeepManualCapturePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/videos", "/v1/chat/completions", "/v1/decisions/other"} {
		t.Run(path, func(t *testing.T) {
			cleanup := setupDistributorTestDB(t)
			defer cleanup()
			const modelName = "capture-boundary-model"
			channel := &model.Channel{Id: 7492, Type: constant.ChannelTypeOpenAI, Key: "synthetic-upstream-key", Status: common.ChannelStatusEnabled, Name: "capture-boundary", Models: modelName, Group: model.ChannelLaneDirect}
			if err := model.CoreDB().Create(channel).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.CoreDB().Create(&model.Ability{Group: model.ChannelLaneDirect, Model: modelName, ChannelId: channel.Id, Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
			if err := (&model.Model{ModelName: modelName, Status: 1, LifecycleStatus: model.ModelLifecycleActive}).Insert(); err != nil {
				t.Fatal(err)
			}
			model.RefreshPricing()
			called := false
			engine := gin.New()
			engine.POST(path, func(c *gin.Context) {
				c.Set(common.RequestIdKey, "req-suppressed-capture-boundary")
				common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, fmt.Sprint(channel.Id))
				c.Next()
			}, SuppressRelayTelemetry(), Distribute(), func(c *gin.Context) {
				if value, exists := c.Get("relay_result_flush_expected"); exists {
					t.Errorf("suppressed %s unexpectedly installed a consume capture marker: %v", path, value)
				}
				if _, exists := c.Get("relay_result_outcome_builder"); exists {
					t.Errorf("suppressed %s unexpectedly installed an automatic outcome builder", path)
				}
				called = true
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"`+modelName+`"}`))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, req)
			if response.Code != http.StatusOK || !called {
				t.Fatalf("boundary handler did not run: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
