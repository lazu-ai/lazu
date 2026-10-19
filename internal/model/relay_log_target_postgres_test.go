package model

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
)

func TestPostgresManualLogTargetTimingSurvivesCompletionAndExpires(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("isolated TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	_, db := postgresTelemetryFixture(t)
	client := redis.NewClient(options)
	previousClient, previousStream, previousRedis, previousQueue := common.RDB, common.RelayEventStream, common.RedisEnabled, common.RelayEventQueueEnabled
	stream := "lazu:test:media-timing-target:" + common.GenerateRequestID()
	common.RDB, common.RelayEventStream, common.RedisEnabled, common.RelayEventQueueEnabled = client, stream, true, true
	t.Cleanup(func() {
		_ = client.Del(context.Background(), stream, stream+":failures", stream+":dead", stream+":dead_count").Err()
		common.RDB, common.RelayEventStream, common.RedisEnabled, common.RelayEventQueueEnabled = previousClient, previousStream, previousRedis, previousQueue
		_ = client.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if err := ensureRelayEventGroup(ctx); err != nil {
		t.Fatal(err)
	}
	drain := func() {
		t.Helper()
		if err := consumeRelayEventBatch(ctx, "media-target", ">", relayEventReadNonBlocking); err != nil {
			t.Fatal(err)
		}
	}
	newCarrier := func() (string, *gin.Context, *RoutingOutcome) {
		t.Helper()
		requestID := common.GenerateRequestID()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set(common.RequestIdKey, requestID)
		status := 202
		outcome := &RoutingOutcome{RequestID: requestID, OutcomeAt: time.Now().UTC(), StatusCode: &status}
		if err := PersistManualRoutingOutcome(c, outcome); err != nil || !HasRequestLogTarget(c) {
			t.Fatalf("successful PostgreSQL media publication lost its target: err=%v", err)
		}
		drain()
		return requestID, c, outcome
	}
	patch := func(requestID string, value int) {
		t.Helper()
		if err := PublishRelayEvent(RelayEventTypeRequestDiagnostics, RequestDiagnosticsEvent{RequestID: requestID, Fields: map[string]interface{}{"gateway_post_ms": value}}); err != nil {
			t.Fatal(err)
		}
		drain()
	}
	read := func(requestID string) Log {
		t.Helper()
		var row Log
		if err := db.Where("request_id = ?", requestID).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row
	}

	requestID, _, _ := newCarrier()
	patch(requestID, 17)
	row := read(requestID)
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(row.Other), &fields); err != nil || row.Type != LogTypeUnknown || !hasProvisionalLogTarget(row.Other) || fields["gateway_post_ms"] != float64(17) {
		t.Fatalf("new provisional media carrier lost timing: type=%d other=%q err=%v", row.Type, row.Other, err)
	}
	if err := UpdateRequestLogDiagnostics(requestID, map[string]interface{}{"gateway_pre_ms": 3}); err != nil {
		t.Fatal(err)
	}
	final, _ := completeWriterFixture(requestID)
	if _, err := persistRelayWriterBatch(relayWriterBatch{results: []relayResultEvent{final}, diagnostics: []RequestDiagnosticsEvent{{RequestID: requestID, Fields: map[string]interface{}{"completion_diagnostic": true}}}}); err != nil {
		t.Fatal(err)
	}
	row = read(requestID)
	fields = nil
	if err := json.Unmarshal([]byte(row.Other), &fields); err != nil || row.Type != LogTypeConsume || row.AmountMicrousd != 123 || fields["gateway_post_ms"] != float64(17) || fields["gateway_pre_ms"] != float64(3) || fields["usage_source"] != "provider" || fields["completion_diagnostic"] != true {
		t.Fatalf("final consume lost carrier timing or facts: %+v type=%d amount=%d err=%v", fields, row.Type, row.AmountMicrousd, err)
	}
	if _, exists := fields[provisionalLogTargetKey]; exists {
		t.Fatal("final consume retained its provisional marker")
	}
	for i := 0; i < 2; i++ {
		if _, err := persistRelayWriterBatch(relayWriterBatch{results: []relayResultEvent{final}, diagnostics: []RequestDiagnosticsEvent{{RequestID: requestID, Fields: map[string]interface{}{"gateway_post_ms": 17}}}}); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&TelemetryEventReceipt{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("media completion replay duplicated receipts: count=%d err=%v", count, err)
	}
	var counter ModelRequestCounter
	if err := db.Where("model_name = ?", "fixture").First(&counter).Error; err != nil || counter.TotalRequests != 1 {
		t.Fatalf("media completion replay duplicated counters: %+v err=%v", counter, err)
	}

	expiredID, expiredContext, expiredOutcome := newCarrier()
	patch(expiredID, 18)
	if _, err := expireRequestLogDetailsTableTx(db, "logs", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	row = read(expiredID)
	if row.Type != LogTypeUnknown || row.Other != "" || row.RoutingOutcomeJSON == "" {
		t.Fatalf("retention did not strip only diagnostic details: type=%d other=%q routing=%q", row.Type, row.Other, row.RoutingOutcomeJSON)
	}
	patch(expiredID, 99)
	if err := UpdateRequestLogDiagnostics(expiredID, map[string]interface{}{"gateway_pre_ms": 99}); err != nil {
		t.Fatal(err)
	}
	// A late snapshot cannot restore the active marker on conflict.
	if err := PersistManualRoutingOutcome(expiredContext, expiredOutcome); err != nil {
		t.Fatal(err)
	}
	drain()
	patch(expiredID, 100)
	row = read(expiredID)
	if row.Type != LogTypeUnknown || row.Other != "" {
		t.Fatalf("late diagnostic or snapshot resurrected an expired carrier: type=%d other=%q", row.Type, row.Other)
	}
	legacyID := common.GenerateRequestID()
	if err := db.Create(&Log{RequestID: legacyID, Type: LogTypeUnknown, Other: ""}).Error; err != nil {
		t.Fatal(err)
	}
	patch(legacyID, 99)
	if row := read(legacyID); row.Other != "" || row.Type != LogTypeUnknown {
		t.Fatal("an existing unknown carrier acquired diagnostic metadata")
	}
	if pending, err := client.XPending(ctx, stream, common.RelayEventGroup).Result(); err != nil || pending.Count != 0 {
		t.Fatalf("valid or expired diagnostics were not acknowledged: %+v err=%v", pending, err)
	}
	if dead, err := client.XLen(ctx, stream+":dead").Result(); err != nil || dead != 0 {
		t.Fatalf("media target generated a dead letter: dead=%d err=%v", dead, err)
	}
}
