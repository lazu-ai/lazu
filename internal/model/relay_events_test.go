package model

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestRelayEventXAddArgsUsesExactBoundedTelemetryRetention(t *testing.T) {
	previousStream := common.RelayEventStream
	common.RelayEventStream = "test:relay_events"
	t.Cleanup(func() {
		common.RelayEventStream = previousStream
	})

	args := relayEventXAddArgs(RelayEventTypeModelCounter, []byte(`{"model_name":"gpt-test"}`))
	if args.Stream != "test:relay_events" {
		t.Fatalf("expected stream test:relay_events, got %q", args.Stream)
	}
	if args.MaxLen != 100000 || args.Approx {
		t.Fatalf("expected exact bounded telemetry stream, got maxlen=%d approx=%v", args.MaxLen, args.Approx)
	}
	values, ok := args.Values.(map[string]any)
	if !ok {
		t.Fatalf("expected map values, got %T", args.Values)
	}
	if values["type"] != RelayEventTypeModelCounter {
		t.Fatalf("unexpected event type value: %+v", values)
	}
}

func TestRelayEventConsumerCanBeDisabledIndependentlyFromQueue(t *testing.T) {
	previousConsumerEnabled := common.RelayEventConsumerEnabled
	previousQueueEnabled := common.RelayEventQueueEnabled
	previousRedisEnabled := common.RedisEnabled
	previousRedis := common.RDB
	previousDB := coreDB
	previousLogDB := telemetryDB
	common.RelayEventConsumerEnabled = false
	common.RelayEventQueueEnabled = true
	common.RedisEnabled = true
	common.RDB = nil
	coreDB = nil
	telemetryDB = nil
	t.Cleanup(func() {
		common.RelayEventConsumerEnabled = previousConsumerEnabled
		common.RelayEventQueueEnabled = previousQueueEnabled
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedis
		coreDB = previousDB
		telemetryDB = previousLogDB
	})

	if relayEventConsumerAvailable() {
		t.Fatal("expected API relay consumer to remain disabled")
	}
	if !common.RelayEventQueueEnabled {
		t.Fatal("expected relay event publishing to remain enabled")
	}
}

func TestParseAutoClaimRelayEventReplySupportsRedis7(t *testing.T) {
	messages, next, err := parseAutoClaimRelayEventReply([]any{
		"0-0",
		[]any{
			[]any{
				"1710000000000-0",
				[]any{
					"type", RelayEventTypeModelCounter,
					"payload", `{"model_name":"gpt-test"}`,
				},
			},
		},
		[]any{"1700000000000-0"},
	})
	if err != nil {
		t.Fatalf("failed to parse Redis 7 XAUTOCLAIM reply: %v", err)
	}
	if next != "0-0" {
		t.Fatalf("unexpected next cursor %q", next)
	}
	if len(messages) != 1 || messages[0].ID != "1710000000000-0" {
		t.Fatalf("unexpected messages: %+v", messages)
	}
	if messages[0].Values["type"] != RelayEventTypeModelCounter {
		t.Fatalf("unexpected event type: %+v", messages[0].Values)
	}
}

func TestHandleRelayConsumeLogEventWritesLogAndModelCounter(t *testing.T) {
	db := withPricingTestDB(t, "relay_event_consume_log")
	previousLogDB := telemetryDB
	previousDataExport := common.DataExportEnabled
	telemetryDB = db
	common.DataExportEnabled = false
	t.Cleanup(func() {
		telemetryDB = previousLogDB
		common.DataExportEnabled = previousDataExport
	})
	if err := db.AutoMigrate(&ModelMetadata{}); err != nil {
		t.Fatalf("failed to migrate model metadata: %v", err)
	}
	row := Log{
		UserId:    1,
		RequestID: "request-1",
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeConsume,
		ModelName: "event-model",
		TokenName: "token",
		ChannelId: 2,
		TokenId:   3,
		UseTime:   1,
		IsStream:  false,
		Group:     ChannelLaneDirect,
		UsageFact: "{}",
		LineItems: "{}",
		Other:     "{}",
	}
	raw, err := json.Marshal(newRelayConsumeLogEvent(&row))
	if err != nil {
		t.Fatalf("failed to marshal consume log event: %v", err)
	}

	if err := handleRelayEvent(RelayEventTypeConsumeLog, raw); err != nil {
		t.Fatalf("failed to handle consume log event: %v", err)
	}

	var logCount int64
	if err := db.Model(&Log{}).Where("request_id = ?", "request-1").Count(&logCount).Error; err != nil {
		t.Fatalf("failed to count logs: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("expected one consume log, got %d", logCount)
	}
	var stored Log
	if err := db.Where("request_id = ?", "request-1").First(&stored).Error; err != nil {
		t.Fatalf("failed to load consume log: %v", err)
	}
	if stored.UserId != 1 || stored.ChannelId != 2 || stored.TokenId != 3 {
		t.Fatalf("expected queued log to preserve hidden db fields, got user=%d channel=%d token=%d", stored.UserId, stored.ChannelId, stored.TokenId)
	}
	if err := handleRelayEvent(RelayEventTypeConsumeLog, raw); err != nil {
		t.Fatalf("failed to replay consume log event: %v", err)
	}
	if err := db.Model(&Log{}).Where("request_id = ?", "request-1").Count(&logCount).Error; err != nil {
		t.Fatalf("failed to recount logs: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("expected replay to stay idempotent, got %d logs", logCount)
	}
	var meta ModelRequestCounter
	if err := db.Where("model_name = ?", "event-model").First(&meta).Error; err != nil {
		t.Fatalf("failed to load model metadata: %v", err)
	}
	if meta.TotalRequests != 1 {
		t.Fatalf("expected model counters to increment once, got %+v", meta)
	}
}

func TestConsumeLogSideEffectsBucketsUsageByLogCreatedAt(t *testing.T) {
	previousDataExport := common.DataExportEnabled
	CacheUsageDataLock.Lock()
	previousUsageCache := CacheUsageData
	CacheUsageData = make(map[string]*UsageData)
	CacheUsageDataLock.Unlock()
	t.Cleanup(func() {
		common.DataExportEnabled = previousDataExport
		CacheUsageDataLock.Lock()
		CacheUsageData = previousUsageCache
		CacheUsageDataLock.Unlock()
	})
	common.DataExportEnabled = true

	requestCreatedAt := int64(1712345678)
	consumeLogSideEffects(&Log{
		UserId:           7,
		Username:         "queued-user",
		AmountMicrousd:   42,
		CreatedAt:        requestCreatedAt,
		PromptTokens:     3,
		CompletionTokens: 4,
	}, false)

	CacheUsageDataLock.Lock()
	defer CacheUsageDataLock.Unlock()
	if len(CacheUsageData) != 1 {
		t.Fatalf("expected one usage cache entry, got %d", len(CacheUsageData))
	}
	for _, usageData := range CacheUsageData {
		expectedBucket := requestCreatedAt - requestCreatedAt%3600
		if usageData.CreatedAt != expectedBucket {
			t.Fatalf("expected usage bucket %d from log created_at, got %d", expectedBucket, usageData.CreatedAt)
		}
	}
}

func TestHandleRelayRoutingEventsLinksOutcomeToDecision(t *testing.T) {
	db := withPricingTestDB(t, "relay_event_routing")
	if err := db.AutoMigrate(&RoutingDecision{}, &RoutingOutcome{}); err != nil {
		t.Fatalf("failed to migrate routing tables: %v", err)
	}
	decision := RoutingDecision{
		RequestID:    "request-1",
		DecisionAt:   time.Now().UTC(),
		IsPrimary:    true,
		StrategyName: "test",
	}
	rawDecision, err := json.Marshal(decision)
	if err != nil {
		t.Fatalf("failed to marshal decision: %v", err)
	}
	if err := handleRelayEvent(RelayEventTypeRoutingDecision, rawDecision); err != nil {
		t.Fatalf("failed to handle routing decision event: %v", err)
	}

	outcome := RoutingOutcome{
		RequestID: "request-1",
		OutcomeAt: time.Now().UTC(),
	}
	rawOutcome, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("failed to marshal outcome: %v", err)
	}
	if err := handleRelayEvent(RelayEventTypeRoutingOutcome, rawOutcome); err != nil {
		t.Fatalf("failed to handle routing outcome event: %v", err)
	}

	var stored RoutingOutcome
	if err := db.Where("request_id = ?", "request-1").First(&stored).Error; err != nil {
		t.Fatalf("failed to load routing outcome: %v", err)
	}
	if stored.DecisionID == 0 {
		t.Fatalf("expected outcome decision id to be backfilled")
	}
}

func TestRecordRoutingOutcomeUpsertRefreshesCostFields(t *testing.T) {
	db := withPricingTestDB(t, "routing_outcome_upsert_costs")
	if err := db.AutoMigrate(&RoutingDecision{}, &RoutingOutcome{}); err != nil {
		t.Fatalf("failed to migrate routing tables: %v", err)
	}
	decision := RoutingDecision{
		RequestID:    "request-costs",
		DecisionAt:   time.Now().UTC(),
		IsPrimary:    true,
		StrategyName: "test",
	}
	if _, err := createRoutingDecisionSync(&decision); err != nil {
		t.Fatalf("failed to create decision: %v", err)
	}

	charged := 0.010
	upstream := 0.004
	margin := 0.006
	if err := recordRoutingOutcomeSync(&RoutingOutcome{
		RequestID:       "request-costs",
		OutcomeAt:       time.Now().UTC(),
		DecisionID:      decision.ID,
		ChargedCostUSD:  &charged,
		UpstreamCostUSD: &upstream,
		GrossMarginUSD:  &margin,
	}); err != nil {
		t.Fatalf("failed to create routing outcome: %v", err)
	}

	upstream = 0.007
	margin = 0.003
	if err := recordRoutingOutcomeSync(&RoutingOutcome{
		RequestID:       "request-costs",
		OutcomeAt:       time.Now().UTC(),
		DecisionID:      decision.ID,
		ChargedCostUSD:  &charged,
		UpstreamCostUSD: &upstream,
		GrossMarginUSD:  &margin,
	}); err != nil {
		t.Fatalf("failed to upsert routing outcome: %v", err)
	}

	var stored RoutingOutcome
	if err := db.Where("request_id = ?", "request-costs").First(&stored).Error; err != nil {
		t.Fatalf("failed to load routing outcome: %v", err)
	}
	if stored.UpstreamCostUSD == nil || math.Abs(*stored.UpstreamCostUSD-0.007) > 0.0000001 {
		t.Fatalf("expected upstream cost to be refreshed, got %+v", stored.UpstreamCostUSD)
	}
	if stored.GrossMarginUSD == nil || math.Abs(*stored.GrossMarginUSD-0.003) > 0.0000001 {
		t.Fatalf("expected gross margin to be refreshed, got %+v", stored.GrossMarginUSD)
	}
}

func TestBackfillRoutingDecisionIdempotencyKeys(t *testing.T) {
	db := withPricingTestDB(t, "routing_decision_idempotency_backfill")
	if err := db.AutoMigrate(&RoutingDecision{}); err != nil {
		t.Fatalf("failed to migrate routing decisions: %v", err)
	}
	decisionTime := time.Now().UTC()
	rows := []RoutingDecision{
		{
			RequestID:    "legacy-request",
			DecisionAt:   decisionTime,
			IsPrimary:    true,
			StrategyName: "test",
		},
		{
			RequestID:    "legacy-request",
			DecisionAt:   decisionTime.Add(time.Second),
			IsPrimary:    true,
			StrategyName: "test",
		},
		{
			RequestID:    "another-request",
			DecisionAt:   decisionTime,
			IsPrimary:    true,
			StrategyName: "test",
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("failed to create legacy decisions: %v", err)
	}

	if err := backfillRoutingDecisionIdempotencyKeys(db); err != nil {
		t.Fatalf("backfill failed: %v", err)
	}
	var stored []RoutingDecision
	if err := db.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("failed to load backfilled decisions: %v", err)
	}
	if got, want := *stored[0].IdempotencyKey, "primary:legacy-request"; got != want {
		t.Fatalf("canonical key = %q, want %q", got, want)
	}
	if got, want := *stored[1].IdempotencyKey, "legacy-primary:"+strconv.FormatInt(stored[1].ID, 10); got != want {
		t.Fatalf("duplicate key = %q, want %q", got, want)
	}
	if got, want := *stored[2].IdempotencyKey, "primary:another-request"; got != want {
		t.Fatalf("second canonical key = %q, want %q", got, want)
	}
	if err := backfillRoutingDecisionIdempotencyKeys(db); err != nil {
		t.Fatalf("repeat backfill failed: %v", err)
	}
}

func TestBackfillRoutingDecisionIdempotencyKeysSkipsOversizedHistory(t *testing.T) {
	db := withPricingTestDB(t, "routing_decision_idempotency_backfill_limit")
	if err := db.AutoMigrate(&RoutingDecision{}); err != nil {
		t.Fatalf("failed to migrate routing decisions: %v", err)
	}
	rows := []RoutingDecision{
		{RequestID: "legacy-1", DecisionAt: time.Now().UTC(), IsPrimary: true, StrategyName: "test"},
		{RequestID: "legacy-2", DecisionAt: time.Now().UTC(), IsPrimary: true, StrategyName: "test"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("failed to create legacy decisions: %v", err)
	}

	if err := backfillRoutingDecisionIdempotencyKeysWithLimit(db, 1); err != nil {
		t.Fatalf("bounded backfill failed: %v", err)
	}
	var stored []RoutingDecision
	if err := db.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("failed to load legacy decisions: %v", err)
	}
	for _, row := range stored {
		if row.IdempotencyKey != nil {
			t.Fatalf("oversized history should remain untouched, row %d has key %q", row.ID, *row.IdempotencyKey)
		}
	}
}

func TestHandleRelayResultEventWritesConsumeLogAndRoutingOutcome(t *testing.T) {
	db := withPricingTestDB(t, "relay_result_event")
	previousLogDB := telemetryDB
	previousDataExport := common.DataExportEnabled
	telemetryDB = db
	common.DataExportEnabled = false
	t.Cleanup(func() {
		telemetryDB = previousLogDB
		common.DataExportEnabled = previousDataExport
	})
	if err := db.AutoMigrate(&ModelMetadata{}, &RoutingDecision{}, &RoutingOutcome{}); err != nil {
		t.Fatalf("failed to migrate relay result tables: %v", err)
	}
	decision := RoutingDecision{
		RequestID:    "request-result",
		DecisionAt:   time.Now().UTC(),
		IsPrimary:    true,
		StrategyName: "test",
	}
	if _, err := createRoutingDecisionSync(&decision); err != nil {
		t.Fatalf("failed to create decision: %v", err)
	}
	row := Log{
		UserId:    1,
		RequestID: "request-result",
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeConsume,
		ModelName: "result-model",
		TokenName: "token",
		ChannelId: 2,
		TokenId:   3,
		UsageFact: "{}",
		LineItems: "{}",
		Other:     "{}",
	}
	outcome := RoutingOutcome{
		RequestID: "request-result",
		OutcomeAt: time.Now().UTC(),
	}
	event := relayResultEvent{
		ConsumeLog:     ptrRelayConsumeLogEvent(newRelayConsumeLogEvent(&row)),
		RoutingOutcome: &outcome,
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal relay result event: %v", err)
	}
	if err := handleRelayEvent(RelayEventTypeRelayResult, raw); err != nil {
		t.Fatalf("failed to handle relay result event: %v", err)
	}
	if err := handleRelayEvent(RelayEventTypeRelayResult, raw); err != nil {
		t.Fatalf("failed to replay relay result event: %v", err)
	}

	var logCount int64
	if err := db.Model(&Log{}).Where("request_id = ?", "request-result").Count(&logCount).Error; err != nil {
		t.Fatalf("failed to count logs: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("expected idempotent consume log, got %d", logCount)
	}
	var storedOutcome RoutingOutcome
	if err := db.Where("request_id = ?", "request-result").First(&storedOutcome).Error; err != nil {
		t.Fatalf("failed to load routing outcome: %v", err)
	}
	if storedOutcome.DecisionID == 0 {
		t.Fatalf("expected routing outcome decision id to be backfilled")
	}
}

func TestHandleRelayResultEventBatchIsIdempotent(t *testing.T) {
	db := withPricingTestDB(t, "relay_result_event_batch")
	previousLogDB := telemetryDB
	previousDataExport := common.DataExportEnabled
	telemetryDB = db
	common.DataExportEnabled = false
	t.Cleanup(func() {
		telemetryDB = previousLogDB
		common.DataExportEnabled = previousDataExport
	})
	if err := db.AutoMigrate(&ModelMetadata{}, &RoutingDecision{}, &RoutingOutcome{}); err != nil {
		t.Fatalf("failed to migrate relay result batch tables: %v", err)
	}
	events := make([]relayResultEvent, 0, 2)
	for i, requestID := range []string{"batch-request-1", "batch-request-2"} {
		decision := RoutingDecision{
			RequestID:    requestID,
			DecisionAt:   time.Now().UTC(),
			IsPrimary:    true,
			StrategyName: "test",
		}
		if _, err := createRoutingDecisionSync(&decision); err != nil {
			t.Fatalf("failed to create decision: %v", err)
		}
		row := Log{
			UserId:    1,
			RequestID: requestID,
			CreatedAt: common.GetTimestamp(),
			Type:      LogTypeConsume,
			ModelName: "batch-model",
			TokenName: "token",
			ChannelId: 2 + i,
			TokenId:   3 + i,
			UsageFact: "{}",
			LineItems: "{}",
			Other:     "{}",
		}
		outcome := RoutingOutcome{
			RequestID: requestID,
			OutcomeAt: time.Now().UTC(),
		}
		events = append(events, relayResultEvent{
			ConsumeLog:     ptrRelayConsumeLogEvent(newRelayConsumeLogEvent(&row)),
			RoutingOutcome: &outcome,
		})
	}

	if err := persistRelayResultBatch(events); err != nil {
		t.Fatalf("failed to handle relay result batch: %v", err)
	}
	if err := persistRelayResultBatch(events); err != nil {
		t.Fatalf("failed to replay relay result batch: %v", err)
	}

	var logCount int64
	if err := db.Model(&Log{}).Where("request_id LIKE ?", "batch-request-%").Count(&logCount).Error; err != nil {
		t.Fatalf("failed to count logs: %v", err)
	}
	if logCount != 2 {
		t.Fatalf("expected 2 idempotent consume logs, got %d", logCount)
	}
	var outcomeCount int64
	if err := db.Model(&RoutingOutcome{}).Where("request_id LIKE ?", "batch-request-%").Count(&outcomeCount).Error; err != nil {
		t.Fatalf("failed to count outcomes: %v", err)
	}
	if outcomeCount != 2 {
		t.Fatalf("expected 2 routing outcomes, got %d", outcomeCount)
	}
}

func TestCreateConsumeLogsBatchRunsSideEffectsOnlyForUniqueInsertedRows(t *testing.T) {
	db := withPricingTestDB(t, "relay_result_event_batch_side_effects")
	previousLogDB := telemetryDB
	previousDataExport := common.DataExportEnabled
	CacheUsageDataLock.Lock()
	previousUsageCache := CacheUsageData
	CacheUsageData = make(map[string]*UsageData)
	CacheUsageDataLock.Unlock()
	telemetryDB = db
	common.DataExportEnabled = true
	t.Cleanup(func() {
		telemetryDB = previousLogDB
		common.DataExportEnabled = previousDataExport
		CacheUsageDataLock.Lock()
		CacheUsageData = previousUsageCache
		CacheUsageDataLock.Unlock()
	})

	createdAt := int64(1712345678)
	logs := []Log{
		{
			PublicID:         "log_duplicate",
			RequestID:        "duplicate-request",
			CreatedAt:        createdAt,
			Type:             LogTypeConsume,
			UserId:           7,
			Username:         "side-effect-user",
			AmountMicrousd:   11,
			PromptTokens:     2,
			CompletionTokens: 3,
		},
		{
			PublicID:         "log_duplicate",
			RequestID:        "duplicate-request",
			CreatedAt:        createdAt,
			Type:             LogTypeConsume,
			UserId:           7,
			Username:         "side-effect-user",
			AmountMicrousd:   99,
			PromptTokens:     50,
			CompletionTokens: 50,
		},
	}
	if err := createConsumeLogsBatch(logs); err != nil {
		t.Fatalf("failed to create consume log batch: %v", err)
	}

	var logCount int64
	if err := db.Model(&Log{}).Where("public_id = ?", "log_duplicate").Count(&logCount).Error; err != nil {
		t.Fatalf("failed to count logs: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("expected one inserted consume log, got %d", logCount)
	}
	CacheUsageDataLock.Lock()
	defer CacheUsageDataLock.Unlock()
	totalCount := 0
	totalTokens := 0
	for _, usage := range CacheUsageData {
		totalCount += usage.Count
		totalTokens += usage.TokenUsed
	}
	if totalCount != 1 || totalTokens != 5 {
		t.Fatalf("expected side effects once with 5 tokens, got count=%d tokens=%d cache=%+v", totalCount, totalTokens, CacheUsageData)
	}
}

func ptrRelayConsumeLogEvent(event relayConsumeLogEvent) *relayConsumeLogEvent {
	return &event
}

func TestHandleRelayRoutingOutcomeCanRetryAfterDecisionArrives(t *testing.T) {
	db := withPricingTestDB(t, "relay_event_routing_retry")
	if err := db.AutoMigrate(&RoutingDecision{}, &RoutingOutcome{}); err != nil {
		t.Fatalf("failed to migrate routing tables: %v", err)
	}

	outcome := RoutingOutcome{
		RequestID: "request-retry",
		OutcomeAt: time.Now().UTC(),
	}
	rawOutcome, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("failed to marshal outcome: %v", err)
	}
	if err := handleRelayEvent(RelayEventTypeRoutingOutcome, rawOutcome); err == nil {
		t.Fatalf("expected missing decision error")
	}

	decision := RoutingDecision{
		RequestID:    "request-retry",
		DecisionAt:   time.Now().UTC(),
		IsPrimary:    true,
		StrategyName: "test",
	}
	rawDecision, err := json.Marshal(decision)
	if err != nil {
		t.Fatalf("failed to marshal decision: %v", err)
	}
	if err := handleRelayEvent(RelayEventTypeRoutingDecision, rawDecision); err != nil {
		t.Fatalf("failed to handle routing decision event: %v", err)
	}
	if err := handleRelayEvent(RelayEventTypeRoutingOutcome, rawOutcome); err != nil {
		t.Fatalf("failed to retry routing outcome event: %v", err)
	}

	var stored RoutingOutcome
	if err := db.Where("request_id = ?", "request-retry").First(&stored).Error; err != nil {
		t.Fatalf("failed to load routing outcome: %v", err)
	}
	if stored.DecisionID == 0 {
		t.Fatalf("expected retried outcome decision id to be backfilled")
	}
}
