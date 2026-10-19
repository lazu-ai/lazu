package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"gorm.io/gorm"
)

func TestNativeRedisRelayTimingRequiresActualLogTarget(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("isolated TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()
	db := model.CoreDB()
	previousTelemetry := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	if err := db.AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	previousClient, previousStream := common.RDB, common.RelayEventStream
	previousRedis, previousQueue, previousConsumer, previousLogs := common.RedisEnabled, common.RelayEventQueueEnabled, common.RelayEventConsumerEnabled, common.LogConsumeEnabled
	stream := "lazu:test:router-diagnostic-target:" + common.GenerateRequestID()
	common.RDB, common.RelayEventStream = client, stream
	common.RedisEnabled, common.RelayEventQueueEnabled, common.RelayEventConsumerEnabled, common.LogConsumeEnabled = true, true, true, true
	defer func() {
		drainRelayDiagnosticFixture(t)
		_ = client.Del(context.Background(), stream, stream+":failures", stream+":dead", stream+":dead_count").Err()
		common.RDB, common.RelayEventStream = previousClient, previousStream
		common.RedisEnabled, common.RelayEventQueueEnabled, common.RelayEventConsumerEnabled, common.LogConsumeEnabled = previousRedis, previousQueue, previousConsumer, previousLogs
		model.SetTelemetryDB(previousTelemetry)
		_ = client.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	queryFailures := 0
	if err := db.Callback().Query().Before("gorm:query").Register("test:target-authority-outage", func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			queryFailures++
			tx.AddError(errors.New("fixture authority unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove("test:target-authority-outage")
	completed := 0
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		requestID := common.GenerateRequestID()
		c.Set(common.RequestIdKey, requestID)
		c.Header(common.RequestIdKey, requestID)
		c.Next()
	})
	engine.Use(common.FinishRelayTimingAfterMiddleware(func(c *gin.Context, fields map[string]interface{}) {
		completed++
		if _, ok := fields["gateway_post_ms"]; !ok {
			t.Fatal("the fixture failed to reach post-timing finalization")
		}
		service.RecordRelayTimingDiagnostics(c, fields)
	}))
	SetRelayRouter(engine)
	engine.GET("/fixture/public", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"fixture","input":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(common.WithInternalRelayIdentity(request.Context(), common.InternalRelayIdentity{TokenID: 1, UserID: 1}))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" || queryFailures != 1 {
		t.Fatalf("fixture did not reach the early authority 503: status=%d retry=%q failures=%d", response.Code, response.Header().Get("Retry-After"), queryFailures)
	}
	for _, path := range []string{"/v1/models", "/fixture/public"} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("public fixture %s returned %d", path, response.Code)
		}
	}
	drainRelayDiagnosticFixture(t)
	if completed != 3 {
		t.Fatalf("expected all three actual final timing callbacks, got %d", completed)
	}
	if length, err := client.XLen(ctx, stream).Result(); err != nil || length != 0 {
		t.Fatalf("requests without logs published diagnostics: length=%d err=%v", length, err)
	}
	if processed, err := model.DrainRelayEvents(ctx, "target-router", 6); err != nil || processed != 0 {
		t.Fatalf("empty target stream was not empty: processed=%d err=%v", processed, err)
	}
	if dead, err := client.XLen(ctx, stream+":dead").Result(); err != nil || dead != 0 {
		t.Fatalf("early failure produced a DLQ entry: dead=%d err=%v", dead, err)
	}

	// These handlers use the real log producers and final timing callback.
	// A spool carries real fsynced facts; its replay is published after the
	// HTTP response so the diagnostic can arrive before its final log.
	var spoolEnvelope string
	spool := &ledger.Engine{SpoolDir: filepath.Join(t.TempDir(), "spool")}
	for _, kind := range []string{"consume", "error", "captured", "spool"} {
		kind := kind
		engine.POST("/fixture/"+kind, func(c *gin.Context) {
			if kind == "captured" || kind == "spool" {
				model.MarkRelayResultFlushExpected(c)
			}
			if kind == "error" {
				model.RecordErrorLog(c, 0, 0, "target", "", "fixture error", 0, 0, false, "", nil)
			} else {
				model.RecordConsumeLog(c, 0, model.RecordConsumeLogParams{ModelName: "target", AmountMicrousd: 123})
			}
			if !model.HasRequestLogTarget(c) {
				t.Fatalf("%s did not establish a real timing target", kind)
			}
			if kind == "spool" {
				envelope, complete, err := model.BuildRelayResultEnvelope(c)
				if err != nil || !complete {
					t.Fatalf("spool lost its envelope: complete=%v err=%v", complete, err)
				}
				if err := spool.PersistSettlementWithTelemetry(ledger.Input{RequestID: c.GetString(common.RequestIdKey), PayerID: 1, ActorID: 1, TokenID: 1, Amount: 123, OccurredAt: time.Now().UTC()}, 123, "settle", envelope); err != nil {
					t.Fatal(err)
				}
				entries, err := os.ReadDir(spool.SpoolDir)
				if err != nil || len(entries) != 1 {
					t.Fatalf("missing durable spool: entries=%d err=%v", len(entries), err)
				}
				body, err := os.ReadFile(filepath.Join(spool.SpoolDir, entries[0].Name()))
				var event ledger.Event
				if err != nil || json.Unmarshal(body, &event) != nil || event.TelemetryEnvelope != envelope {
					t.Fatal("durable spool lost the exact rich fact")
				}
				spoolEnvelope = event.TelemetryEnvelope
				model.MarkRelayResultPublished(c)
			}
			if kind == "captured" || kind == "spool" {
				model.FlushRelayResultEvent(c, nil)
			}
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/fixture/"+kind, nil))
		drainRelayDiagnosticFixture(t)
		if kind == "spool" {
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(spoolEnvelope), &payload); err != nil {
				t.Fatal(err)
			}
			if err := model.PublishRelayEvent(model.RelayEventTypeRelayResult, payload); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := model.DrainRelayEvents(ctx, "target-router", 6); err != nil {
			t.Fatal(err)
		}
		var row model.Log
		requestID := response.Header().Get(common.RequestIdKey)
		if err := db.Where("request_id = ?", requestID).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		var fields map[string]interface{}
		if err := json.Unmarshal([]byte(row.Other), &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["gateway_post_ms"]; !ok {
			t.Fatalf("%s lost its final timing: %+v", kind, fields)
		}
		if kind == "error" && row.Type != model.LogTypeError || kind != "error" && (row.Type != model.LogTypeConsume || row.AmountMicrousd != 123) {
			t.Fatalf("%s diagnostics changed canonical log facts", kind)
		}
	}
	if pending, err := client.XPending(ctx, stream, common.RelayEventGroup).Result(); err != nil || pending.Count != 0 {
		t.Fatalf("valid target diagnostics did not drain: %+v err=%v", pending, err)
	}
	if dead, err := client.XLen(ctx, stream+":dead").Result(); err != nil || dead != 0 {
		t.Fatalf("valid targets produced DLQ entries: dead=%d err=%v", dead, err)
	}
}
