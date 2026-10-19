package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func TestRequestLogTargetIsBoundToCapturedRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	requestID := common.GenerateRequestID()
	c.Set(common.RequestIdKey, requestID)
	if HasRequestLogTarget(nil) || HasRequestLogTarget(c) {
		t.Fatal("a request without a log acquired a target")
	}
	MarkRequestLogTarget(c, "")
	MarkRequestLogTarget(c, "different-request")
	if HasRequestLogTarget(c) {
		t.Fatal("an unrelated request acquired a target")
	}
	MarkRelayResultFlushExpected(c)
	if !queueConsumeLogForRelayResult(c, &Log{RequestID: requestID, Type: LogTypeConsume}) || !HasRequestLogTarget(c) {
		t.Fatal("captured facts did not establish the diagnostics target")
	}
	MarkRelayResultPublished(c)
	if !FlushRelayResultEvent(c, nil) || !HasRequestLogTarget(c) {
		t.Fatal("durable publication cleared the diagnostics target")
	}
	c.Set(common.RequestIdKey, common.GenerateRequestID())
	if HasRequestLogTarget(c) {
		t.Fatal("the target escaped its original request identity")
	}
}

func TestRequestLogTargetRequiresSuccessfulIndependentWrite(t *testing.T) {
	_, db := isolatedTelemetryStores(t)
	previousRedis, previousQueue, previousLogs := common.RedisEnabled, common.RelayEventQueueEnabled, common.LogConsumeEnabled
	common.RedisEnabled, common.RelayEventQueueEnabled, common.LogConsumeEnabled = false, false, true
	t.Cleanup(func() {
		common.RedisEnabled, common.RelayEventQueueEnabled, common.LogConsumeEnabled = previousRedis, previousQueue, previousLogs
	})
	for _, kind := range []string{"consume", "error"} {
		t.Run(kind, func(t *testing.T) {
			write := func(c *gin.Context) {
				if kind == "consume" {
					RecordConsumeLog(c, 0, RecordConsumeLogParams{ModelName: "target", AmountMicrousd: 123})
				} else {
					RecordErrorLog(c, 0, 0, "target", "", "fixture error", 0, 0, false, "", nil)
				}
			}
			newContext := func() *gin.Context {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				c.Set(common.RequestIdKey, common.GenerateRequestID())
				return c
			}
			c := newContext()
			write(c)
			if !HasRequestLogTarget(c) {
				t.Fatal("a persisted log did not establish its target")
			}
			if err := UpdateRequestLogDiagnostics(c.GetString(common.RequestIdKey), map[string]interface{}{"gateway_post_ms": 17}); err != nil {
				t.Fatal(err)
			}
			if err := db.Callback().Create().Before("gorm:create").Register("test:target-write-failure", func(tx *gorm.DB) { tx.AddError(errors.New("fixture write failure")) }); err != nil {
				t.Fatal(err)
			}
			failed := newContext()
			write(failed)
			_ = db.Callback().Create().Remove("test:target-write-failure")
			if HasRequestLogTarget(failed) {
				t.Fatal("a failed log write acquired a diagnostics target")
			}
		})
	}
}

func TestRequestLogTargetManualRoutingAndFailedPublish(t *testing.T) {
	_, db := isolatedTelemetryStores(t)
	previousClient, previousRedis, previousQueue, previousLogs := common.RDB, common.RedisEnabled, common.RelayEventQueueEnabled, common.LogConsumeEnabled
	common.RDB, common.RedisEnabled, common.RelayEventQueueEnabled, common.LogConsumeEnabled = nil, false, false, true
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled, common.RelayEventQueueEnabled, common.LogConsumeEnabled = previousClient, previousRedis, previousQueue, previousLogs
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", nil)
	c.Set(common.RequestIdKey, common.GenerateRequestID())
	outcome := &RoutingOutcome{RequestID: c.GetString(common.RequestIdKey), OutcomeAt: time.Now().UTC()}
	if err := PersistManualRoutingOutcome(c, outcome); err != nil {
		t.Fatal(err)
	}
	if HasRequestLogTarget(c) {
		t.Fatal("legacy routing-only tables acquired a nonexistent log target")
	}
	var rows int64
	if err := db.Model(&Log{}).Count(&rows).Error; err != nil || rows != 0 {
		t.Fatalf("manual legacy routing unexpectedly wrote a log: rows=%d err=%v", rows, err)
	}
	// Name-only dialect wrapper exercises the publication requirement without
	// allowing SQL fallback; actual PostgreSQL carrier storage has its own test.
	oldDialect := CoreDB().Dialector
	CoreDB().Dialector = postgresBoundaryTestDialect{oldDialect}
	defer func() { CoreDB().Dialector = oldDialect }()
	if err := PersistManualRoutingOutcome(c, outcome); err == nil || HasRequestLogTarget(c) {
		t.Fatal("failed media publication established a diagnostics target")
	}
	RecordConsumeLog(c, 0, RecordConsumeLogParams{ModelName: "target", AmountMicrousd: 123})
	RecordErrorLog(c, 0, 0, "target", "", "fixture error", 0, 0, false, "", nil)
	if HasRequestLogTarget(c) {
		t.Fatal("failed independent publication established a diagnostics target")
	}
}

func TestNativeRedisTargetDiagnosticsRetryBeforeCapturedFacts(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("isolated TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	_, db := isolatedTelemetryStores(t)
	client := redis.NewClient(options)
	previousClient, previousStream, previousRedis, previousQueue := common.RDB, common.RelayEventStream, common.RedisEnabled, common.RelayEventQueueEnabled
	stream := "lazu:test:diagnostic-target:" + common.GenerateRequestID()
	common.RDB, common.RelayEventStream, common.RedisEnabled, common.RelayEventQueueEnabled = client, stream, true, true
	t.Cleanup(func() {
		_ = client.Del(context.Background(), stream, stream+":failures", stream+":dead", stream+":dead_count").Err()
		common.RDB, common.RelayEventStream, common.RedisEnabled, common.RelayEventQueueEnabled = previousClient, previousStream, previousRedis, previousQueue
		_ = client.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if err := ensureRelayEventGroup(ctx); err != nil {
		t.Fatal(err)
	}
	event := telemetryFixture(common.GenerateRequestID())
	requestID := event.ConsumeLog.RequestID
	if err := PublishRelayEvent(RelayEventTypeRequestDiagnostics, RequestDiagnosticsEvent{RequestID: requestID, Fields: map[string]interface{}{"gateway_post_ms": 17}}); err != nil {
		t.Fatal(err)
	}
	if err := consumeRelayEventBatch(ctx, "target", ">", relayEventReadNonBlocking); err != nil {
		t.Fatal(err)
	}
	if pending, err := client.XPending(ctx, stream, common.RelayEventGroup).Result(); err != nil || pending.Count != 1 {
		t.Fatalf("a diagnostic before captured facts must remain pending: %+v err=%v", pending, err)
	}
	if err := PublishRelayEvent(RelayEventTypeRelayResult, event); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{">", "0"} {
		if err := consumeRelayEventBatch(ctx, "target", id, relayEventReadNonBlocking); err != nil {
			t.Fatal(err)
		}
	}
	var row Log
	if err := db.Where("request_id = ?", requestID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(row.Other), &fields); err != nil || fields["gateway_post_ms"] != float64(17) || row.AmountMicrousd != 123 {
		t.Fatalf("retry lost timing or changed finance: %+v err=%v", fields, err)
	}
	if pending, err := client.XPending(ctx, stream, common.RelayEventGroup).Result(); err != nil || pending.Count != 0 {
		t.Fatalf("committed diagnostic still pending: %+v err=%v", pending, err)
	}
	if dead, err := client.XLen(ctx, stream+":dead").Result(); err != nil || dead != 0 {
		t.Fatalf("a valid late fact generated a dead letter: dead=%d err=%v", dead, err)
	}
}
