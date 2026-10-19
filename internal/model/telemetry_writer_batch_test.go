package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func writerTestMessage(t *testing.T, id, kind string, payload interface{}) redis.XMessage {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return redis.XMessage{ID: id, Values: map[string]interface{}{"type": kind, "payload": string(raw)}}
}

func TestRelayWriterBoundsCountCompleteRequestsAndPreserveEveryMessage(t *testing.T) {
	messages := make([]redis.XMessage, 0, 2004)
	for request := 0; request < 501; request++ {
		requestID := fmt.Sprintf("bounded-%d", request)
		for part := 0; part < 4; part++ {
			messages = append(messages, writerTestMessage(t, fmt.Sprintf("%d-%d", request, part), RelayEventTypeRequestDiagnostics, RequestDiagnosticsEvent{RequestID: requestID, Fields: map[string]interface{}{"part": part}}))
		}
	}
	batches := splitRelayWriterMessages(messages)
	if len(batches) != 2 || len(batches[0]) != 2000 || len(batches[1]) != 4 {
		t.Fatalf("500 requests must fit with four messages each: batch count=%d", len(batches))
	}
	seen := map[string]bool{}
	for _, batch := range batches {
		bounds := relayWriterBounds{}
		for _, message := range batch {
			if seen[message.ID] {
				t.Fatal("message duplicated while splitting")
			}
			seen[message.ID] = true
			bounds.add(message)
		}
		if len(bounds.requests) > 500 || bounds.messages > relayWriterMaxMessages || bounds.bytes > relayWriterMaxBytes {
			t.Fatalf("batch exceeds bounds: %+v", bounds)
		}
	}
	if len(seen) != len(messages) {
		t.Fatal("splitting lost messages")
	}
	// Repeated diagnostics for one request must still obey the message cap.
	oneRequest := make([]redis.XMessage, relayWriterMaxMessages+1)
	for i := range oneRequest {
		oneRequest[i] = writerTestMessage(t, fmt.Sprint(i), RelayEventTypeRequestDiagnostics, RequestDiagnosticsEvent{RequestID: "one"})
	}
	if batches := splitRelayWriterMessages(oneRequest); len(batches) != 2 || len(batches[0]) != relayWriterMaxMessages {
		t.Fatal("same-request entries bypassed message cap")
	}
	large := make([]redis.XMessage, 3)
	for i := range large {
		large[i] = writerTestMessage(t, fmt.Sprint(i), RelayEventTypeRequestDiagnostics, RequestDiagnosticsEvent{RequestID: "large", Fields: map[string]interface{}{"data": strings.Repeat("x", 6<<20)}})
	}
	if batches := splitRelayWriterMessages(large); len(batches) != 2 || len(batches[0]) != 2 {
		t.Fatal("same-request entries bypassed byte cap")
	}
	bounds := relayWriterBounds{}
	for _, message := range messages[:500] {
		bounds.add(message)
	}
	if bounds.readCount() != 375 {
		t.Fatalf("500 messages were treated as 500 requests: next count=%d", bounds.readCount())
	}
}

type writerSQLTrace struct {
	logger.Interface
	statements []string
}

func (trace *writerSQLTrace) Trace(ctx context.Context, begin time.Time, sql func() (string, int64), err error) {
	statement, _ := sql()
	trace.statements = append(trace.statements, statement)
	trace.Interface.Trace(ctx, begin, sql, err)
}

func completeWriterFixture(requestID string) (relayResultEvent, TelemetryFinancialReceipt) {
	event := telemetryFixture(requestID)
	event.RoutingDecision.ID = stableRoutingDecisionID(requestID)
	event.RoutingOutcome.DecisionID = event.RoutingDecision.ID
	event.ConsumeLog.Other = `{"usage_source":"provider"}`
	receipt := TelemetryFinancialReceipt{EventID: "settle:" + requestID, RequestID: requestID, RequestTS: event.ConsumeLog.RequestTS, Kind: "settle", PayerID: 7, ActorID: 7, ProjectID: 11, TokenID: 9, AmountMicrousd: 123, OccurredAt: time.Unix(event.ConsumeLog.CreatedAt, 0).UTC()}
	return event, receipt
}

func TestPostgresRelayWriterCoalesces500RequestsAndProtectsReplays(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	trace := &writerSQLTrace{Interface: db.Logger.LogMode(logger.Silent)}
	db.Logger = trace
	batch := relayWriterBatch{}
	for i := 0; i < 500; i++ {
		event, receipt := completeWriterFixture(common.GenerateRequestID())
		batch.results = append(batch.results, event)
		batch.receipts = append(batch.receipts, receipt)
		batch.diagnostics = append(batch.diagnostics,
			RequestDiagnosticsEvent{RequestID: receipt.RequestID, Fields: map[string]interface{}{"gateway_post_ms": 12}},
			RequestDiagnosticsEvent{RequestID: receipt.RequestID, Fields: map[string]interface{}{"protocol_local_prompt_tokens": 99}})
	}
	// Duplicate messages inside one delivery must not count a request twice.
	batch.results = append(batch.results, batch.results[0])
	batch.receipts = append(batch.receipts, batch.receipts[0])
	transactionIDs := map[int64]bool{}
	if err := db.Callback().Create().Before("gorm:create").Register("test:writer-transaction", func(tx *gorm.DB) {
		var id int64
		if err := tx.Session(&gorm.Session{NewDB: true}).Raw("SELECT txid_current()").Scan(&id).Error; err != nil {
			t.Fatal(err)
		}
		transactionIDs[id] = true
	}); err != nil {
		t.Fatal(err)
	}
	found, err := persistRelayWriterBatch(batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 500 {
		t.Fatalf("diagnostics were not coalesced into 500 fresh logs: %d", len(found))
	}
	if len(transactionIDs) != 1 {
		t.Fatalf("receipts and all derived facts did not share one transaction: %v", transactionIDs)
	}
	if err := db.Callback().Create().Remove("test:writer-transaction"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range trace.statements {
		if strings.HasPrefix(statement, "SELECT") && strings.Contains(statement, "agent_sessions") || strings.HasPrefix(statement, "UPDATE") && (strings.Contains(statement, "thread_count") || strings.Contains(statement, "UPDATE logs AS logs")) {
			t.Fatalf("avoidable session or diagnostic SQL remains: %s", statement)
		}
	}
	assertCounts := func() {
		t.Helper()
		for _, model := range []interface{}{&Log{}, &TelemetryEventReceipt{}, &TelemetryFinancialReceipt{}} {
			var count int64
			if err := db.Model(model).Count(&count).Error; err != nil || count != 500 {
				t.Fatalf("%T count=%d err=%v", model, count, err)
			}
		}
		var session AgentSession
		if err := db.First(&session).Error; err != nil || session.RequestCount != 500 || session.ThreadCount != 0 || session.AmountMicrousd != 61500 {
			t.Fatalf("session lost returned id or repeated usage: %+v err=%v", session, err)
		}
		var counter ModelRequestCounter
		if err := db.Where("model_name = ?", "fixture").First(&counter).Error; err != nil || counter.TotalRequests != 500 {
			t.Fatalf("model repeated usage: %+v err=%v", counter, err)
		}
		var digest DigestUsageHour
		if err := db.Where("model_name = ?", "fixture").First(&digest).Error; err != nil || digest.Requests != 500 || digest.Spend != 61500 {
			t.Fatalf("digest repeated usage: %+v err=%v", digest, err)
		}
		var hourly DashboardUsageHourlyTotal
		if err := db.Where("scope_user_id = ?", 0).First(&hourly).Error; err != nil || hourly.RequestCount != 500 || hourly.OutcomeCount != 500 || hourly.AmountMicrousd != 61500 {
			t.Fatalf("hourly repeated usage: %+v err=%v", hourly, err)
		}
	}
	assertCounts()
	for i := 0; i < 2; i++ {
		if _, err := persistRelayWriterBatch(batch); err != nil {
			t.Fatal(err)
		}
		assertCounts()
	}
	var first Log
	if err := db.Where("request_id = ?", batch.results[0].ConsumeLog.RequestID).First(&first).Error; err != nil {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(first.Other), &fields); err != nil || fields["gateway_post_ms"] != float64(12) || fields["protocol_local_prompt_tokens"] != float64(99) || fields["usage_source"] != "provider" {
		t.Fatalf("coalescing overwrote facts: %s err=%v", first.Other, err)
	}
	// Redelivery is acknowledged without incrementing rollups. A retained
	// routing carrier must also never regain expired diagnostic metadata.
	if err := db.Model(&Log{}).Where("request_id = ?", first.RequestID).Updates(map[string]interface{}{"type": LogTypeUnknown, "other": ""}).Error; err != nil {
		t.Fatal(err)
	}
	found, err = persistRelayWriterBatch(batch)
	if err != nil || !found[first.RequestID] {
		t.Fatalf("expired diagnostic was not acknowledged: %v", err)
	}
	first = Log{}
	if err := db.Where("request_id = ?", batch.results[0].ConsumeLog.RequestID).First(&first).Error; err != nil || first.Type != LogTypeUnknown || first.Other != "" {
		t.Fatalf("replay resurrected expired facts: type=%d other=%q err=%v", first.Type, first.Other, err)
	}
	assertCounts()
	missingID := common.GenerateRequestID()
	found, err = persistRelayWriterBatch(relayWriterBatch{diagnostics: []RequestDiagnosticsEvent{{RequestID: missingID, Fields: map[string]interface{}{"late": 1}}}})
	if err != nil || found[missingID] {
		t.Fatalf("missing log must remain retryable: found=%v err=%v", found, err)
	}
	threads := relayWriterBatch{}
	for _, threadID := range []string{"sub-a", "sub-a", "sub-b"} {
		event, _ := completeWriterFixture(common.GenerateRequestID())
		event.ConsumeLog.ThreadID = threadID
		threads.results = append(threads.results, event)
	}
	threads.results = append(threads.results, threads.results[0])
	if _, err := persistRelayWriterBatch(threads); err != nil {
		t.Fatal(err)
	}
	main, _ := completeWriterFixture(common.GenerateRequestID())
	if _, err := persistRelayWriterBatch(relayWriterBatch{results: []relayResultEvent{main}}); err != nil {
		t.Fatal(err)
	}
	var session AgentSession
	if err := db.First(&session).Error; err != nil || session.ThreadCount != 2 || session.SubagentRequests != 3 || session.RequestCount != 504 {
		t.Fatalf("distinct subthreads or main continuation changed exact counts: %+v err=%v", session, err)
	}
}

func TestPostgresRelayConsumerRetainsEarlyDiagnosticsAndBoundsOversizedFacts(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("isolated TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	core, db := postgresTelemetryFixture(t)
	client := redis.NewClient(options)
	oldClient, oldStream := common.RDB, common.RelayEventStream
	stream := "lazu:test:writer-limits:" + common.GenerateRequestID()
	common.RDB, common.RelayEventStream = client, stream
	t.Cleanup(func() {
		_ = client.Del(context.Background(), stream, stream+":failures", stream+":dead", stream+":dead_count").Err()
		common.RDB, common.RelayEventStream = oldClient, oldStream
		_ = client.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := ensureRelayEventGroup(ctx); err != nil {
		t.Fatal(err)
	}
	publish := func(kind string, body interface{}) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]interface{}{"type": kind, "payload": string(raw)}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	early, receipt := completeWriterFixture(common.GenerateRequestID())
	early.ConsumeLog.SessionID = "early-session"
	early.ConsumeLog.ModelName = "early-model"
	publish(RelayEventTypeRequestDiagnostics, RequestDiagnosticsEvent{RequestID: receipt.RequestID, Fields: map[string]interface{}{"early": 12}})
	if err := consumeRelayEventBatch(ctx, "test-writer", ">", relayEventReadNonBlocking); err != nil {
		t.Fatal(err)
	}
	if pending, err := client.XPending(ctx, stream, common.RelayEventGroup).Result(); err != nil || pending.Count != 1 {
		t.Fatalf("diagnostic before its log was lost: %+v err=%v", pending, err)
	}
	// A spooled settlement may arrive much later than the five poison-event
	// attempts. Missing dependencies must not consume that failure budget.
	for range 8 {
		if err := consumeRelayEventBatch(ctx, "test-writer", "0", relayEventReadNonBlocking); err != nil {
			t.Fatal(err)
		}
	}
	if pending, err := client.XPending(ctx, stream, common.RelayEventGroup).Result(); err != nil || pending.Count != 1 {
		t.Fatalf("early diagnostic exhausted poison retry budget: %+v err=%v", pending, err)
	}
	if failures, err := client.HLen(ctx, stream+":failures").Result(); err != nil || failures != 0 {
		t.Fatalf("missing dependency counted as poison: %d, %v", failures, err)
	}
	if dead, err := client.XLen(ctx, stream+":dead").Result(); err != nil || dead != 0 {
		t.Fatalf("early diagnostic dead-lettered: %d, %v", dead, err)
	}
	publish(RelayEventTypeFinancialSettlement, receipt)
	publish(RelayEventTypeRelayResult, early)
	if err := consumeRelayEventBatch(ctx, "test-writer", ">", relayEventReadNonBlocking); err != nil {
		t.Fatal(err)
	}
	if err := consumeRelayEventBatch(ctx, "test-writer", "0", relayEventReadNonBlocking); err != nil {
		t.Fatal(err)
	}
	var stored Log
	if err := db.Where("request_id = ?", receipt.RequestID).First(&stored).Error; err != nil || !strings.Contains(stored.Other, `"early": 12`) {
		t.Fatalf("early diagnostic did not eventually commit: other=%q err=%v", stored.Other, err)
	}
	var session AgentSession
	if err := db.Where("session_id = ?", "early-session").First(&session).Error; err != nil || session.RequestCount != 1 {
		t.Fatalf("early diagnostic duplicated session: %+v err=%v", session, err)
	}
	if pending, err := client.XPending(ctx, stream, common.RelayEventGroup).Result(); err != nil || pending.Count != 0 {
		t.Fatalf("persisted diagnostic was not acknowledged: %+v err=%v", pending, err)
	}

	oversized, financial := completeWriterFixture(common.GenerateRequestID())
	oversized.ConsumeLog.Other = strings.Repeat("x", relayWriterMaxBytes)
	if err := core.AutoMigrate(&RedisLedgerOperation{}); err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(ledger.Event{Input: ledger.Input{RequestID: financial.RequestID, PayerID: 7, ActorID: 7, ProjectID: 11, TokenID: 9, Amount: 123, OccurredAt: financial.OccurredAt}, OperationID: financial.RequestID, Kind: "settle"})
	if err != nil {
		t.Fatal(err)
	}
	operation := RedisLedgerOperation{ID: financial.EventID, RequestID: financial.RequestID, Kind: "settle", PayerID: 7, ActorID: 7, ProjectID: 11, TokenID: 9, Amount: 123, Input: string(input), CreatedAt: financial.OccurredAt}
	if err := core.Create(&operation).Error; err != nil {
		t.Fatal(err)
	}
	publish(RelayEventTypeFinancialSettlement, financial)
	publish(RelayEventTypeRelayResult, oversized)
	if err := consumeRelayEventBatch(ctx, "test-writer", ">", relayEventReadNonBlocking); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := consumeRelayEventBatch(ctx, "test-writer", "0", relayEventReadNonBlocking); err != nil {
			t.Fatal(err)
		}
	}
	if dead, err := client.XLen(ctx, stream+":dead").Result(); err != nil || dead != 1 {
		t.Fatalf("oversized fact was not isolated in DLQ: dead=%d err=%v", dead, err)
	}
	if count, err := client.Get(ctx, stream+":dead_count").Int64(); err != nil || count != 1 {
		t.Fatalf("oversized fact missing observable DLQ count: count=%d err=%v", count, err)
	}
	var permanent RedisLedgerOperation
	if err := core.Where("id = ?", operation.ID).First(&permanent).Error; err != nil || permanent.Input != operation.Input || permanent.Amount != operation.Amount {
		t.Fatalf("telemetry limit changed permanent financial fact: %+v err=%v", permanent, err)
	}
	var minimal TelemetryFinancialReceipt
	if err := db.Where("event_id = ?", financial.EventID).First(&minimal).Error; err != nil || minimal.AmountMicrousd != 123 {
		t.Fatalf("telemetry limit dropped minimal financial receipt: %+v err=%v", minimal, err)
	}
	var count int64
	if err := db.Model(&Log{}).Where("request_id = ?", financial.RequestID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("oversized fact entered request logs: count=%d err=%v", count, err)
	}
}

func TestPostgresRelayConsumerReadsRequestGroupsAndAcknowledgesCommittedFacts(t *testing.T) {
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
	oldClient, oldStream := common.RDB, common.RelayEventStream
	stream := "lazu:test:writer-groups:" + common.GenerateRequestID()
	common.RDB, common.RelayEventStream = client, stream
	t.Cleanup(func() {
		_ = client.Del(context.Background(), stream, stream+":failures", stream+":dead", stream+":dead_count").Err()
		common.RDB, common.RelayEventStream = oldClient, oldStream
		_ = client.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := ensureRelayEventGroup(ctx); err != nil {
		t.Fatal(err)
	}
	pipe := client.Pipeline()
	for i := 0; i < 500; i++ {
		event, receipt := completeWriterFixture(common.GenerateRequestID())
		for _, part := range []struct {
			kind string
			body interface{}
		}{{RelayEventTypeFinancialSettlement, receipt}, {RelayEventTypeRelayResult, event}, {RelayEventTypeRequestDiagnostics, RequestDiagnosticsEvent{RequestID: receipt.RequestID, Fields: map[string]interface{}{"gateway_post_ms": 12}}}, {RelayEventTypeRequestDiagnostics, RequestDiagnosticsEvent{RequestID: receipt.RequestID, Fields: map[string]interface{}{"protocol_local_prompt_tokens": 99}}}} {
			raw, err := json.Marshal(part.body)
			if err != nil {
				t.Fatal(err)
			}
			pipe.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]interface{}{"type": part.kind, "payload": string(raw)}})
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := consumeRelayEventBatch(ctx, "test-writer", ">", time.Second); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&Log{}).Count(&count).Error; err != nil || count < 499 {
		t.Fatalf("mixed messages limited the first batch to partial request capacity: logs=%d err=%v", count, err)
	}
	// The final receipt may precede its other three entries at the boundary.
	// The next immediate batch completes it without losing or duplicating it.
	if err := consumeRelayEventBatch(ctx, "test-writer", ">", relayEventReadNonBlocking); err != nil {
		t.Fatal(err)
	}
	for _, model := range []interface{}{&Log{}, &TelemetryFinancialReceipt{}, &TelemetryEventReceipt{}} {
		if err := db.Model(model).Count(&count).Error; err != nil || count != 500 {
			t.Fatalf("%T count=%d err=%v", model, count, err)
		}
	}
	if remaining, err := client.XLen(ctx, stream).Result(); err != nil || remaining != 0 {
		t.Fatalf("committed messages not acknowledged/deleted: remaining=%d err=%v", remaining, err)
	}
	if pending, err := client.XPending(ctx, stream, common.RelayEventGroup).Result(); err != nil || pending.Count != 0 {
		t.Fatalf("committed messages remain pending: %+v err=%v", pending, err)
	}

	// One corrupted aggregate must not prevent an unrelated request from
	// committing. The failed request's receipt and facts remain uncommitted.
	good, goodReceipt := completeWriterFixture(common.GenerateRequestID())
	bad, badReceipt := completeWriterFixture(common.GenerateRequestID())
	bad.ConsumeLog.ModelName = "failed-group"
	if err := db.Create(&ModelRequestCounter{ModelName: "failed-group", TotalRequests: math.MaxInt64}).Error; err != nil {
		t.Fatal(err)
	}
	failedMessages := make([]redis.XMessage, 0, 4)
	for _, part := range []struct {
		kind string
		body interface{}
	}{{RelayEventTypeFinancialSettlement, goodReceipt}, {RelayEventTypeFinancialSettlement, badReceipt}, {RelayEventTypeRelayResult, bad}, {RelayEventTypeRelayResult, good}} {
		raw, _ := json.Marshal(part.body)
		id, err := client.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]interface{}{"type": part.kind, "payload": string(raw)}}).Result()
		if err != nil {
			t.Fatal(err)
		}
		failedMessages = append(failedMessages, redis.XMessage{ID: id, Values: map[string]interface{}{"type": part.kind, "payload": string(raw)}})
	}
	claimed, err := readRelayEventBatch(ctx, "test-writer", ">", relayEventReadNonBlocking)
	if err != nil || len(claimed) != 1 || len(claimed[0].Messages) != len(failedMessages) {
		t.Fatalf("failed-group test could not claim its messages: streams=%d err=%v", len(claimed), err)
	}
	if err := processRelayEventMessages(ctx, claimed[0].Messages); err != nil {
		t.Fatal(err)
	}
	for _, model := range []interface{}{&Log{}, &TelemetryFinancialReceipt{}} {
		if err := db.Model(model).Where("request_id = ?", goodReceipt.RequestID).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("unrelated %T did not commit: count=%d err=%v", model, count, err)
		}
		if err := db.Model(model).Where("request_id = ?", badReceipt.RequestID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("failed %T committed: count=%d err=%v", model, count, err)
		}
	}
	if failures, err := client.HLen(ctx, stream+":failures").Result(); err != nil || failures != 2 {
		t.Fatalf("failed group not left for retry: failures=%d err=%v", failures, err)
	}
	if pending, err := client.XPending(ctx, stream, common.RelayEventGroup).Result(); err != nil || pending.Count != 2 {
		t.Fatalf("uncommitted group was acknowledged: pending=%+v err=%v", pending, err)
	}
}

func TestPostgresRelayWriterOverflowRollsBackReceiptFactsAndDiagnostics(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	existing, _ := completeWriterFixture(common.GenerateRequestID())
	if _, err := persistRelayWriterBatch(relayWriterBatch{results: []relayResultEvent{existing}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ModelRequestCounter{ModelName: "overflow", TotalRequests: math.MaxInt64}).Error; err != nil {
		t.Fatal(err)
	}
	event, receipt := completeWriterFixture(common.GenerateRequestID())
	event.ConsumeLog.ModelName = "overflow"
	batch := relayWriterBatch{results: []relayResultEvent{event}, receipts: []TelemetryFinancialReceipt{receipt}, diagnostics: []RequestDiagnosticsEvent{{RequestID: existing.ConsumeLog.RequestID, Fields: map[string]interface{}{"must_rollback": 1}}, {RequestID: receipt.RequestID, Fields: map[string]interface{}{"coalesced": 1}}}}
	if _, err := persistRelayWriterBatch(batch); err == nil {
		t.Fatal("overflowed rollup transaction was accepted")
	}
	for _, model := range []interface{}{&Log{}, &TelemetryEventReceipt{}, &TelemetryFinancialReceipt{}} {
		var count int64
		query := db.Model(model)
		if _, ok := model.(*TelemetryEventReceipt); ok {
			query = query.Where("event_key = ?", "relay:"+receipt.RequestID)
		} else {
			query = query.Where("request_id = ?", receipt.RequestID)
		}
		if err := query.Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%T survived rollback count=%d err=%v", model, count, err)
		}
	}
	var stored Log
	if err := db.Where("request_id = ?", existing.ConsumeLog.RequestID).First(&stored).Error; err != nil || stored.Other != existing.ConsumeLog.Other {
		t.Fatalf("failed transaction changed old diagnostics: %q err=%v", stored.Other, err)
	}
	var session AgentSession
	if err := db.First(&session).Error; err != nil || session.RequestCount != 1 {
		t.Fatalf("failed transaction changed session: %+v err=%v", session, err)
	}
	if err := db.Model(&ModelRequestCounter{}).Where("model_name = ?", "overflow").Update("total_requests", 0).Error; err != nil {
		t.Fatal(err)
	}
	if found, err := persistRelayWriterBatch(batch); err != nil || !found[receipt.RequestID] || !found[existing.ConsumeLog.RequestID] {
		t.Fatalf("same batch could not retry after rollback: found=%v err=%v", found, err)
	}
}

func TestPostgresRelayStoreOutageRemainsPendingAndRecovers(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	client := identityRedisTest(t)
	oldStream := common.RelayEventStream
	common.RelayEventStream = "lazu:test:store-outage:" + common.GenerateRequestID()
	ctx := context.Background()
	t.Cleanup(func() {
		client.Del(ctx, common.RelayEventStream, common.RelayEventStream+":failures", common.RelayEventStream+":dead")
		common.RelayEventStream = oldStream
	})
	if err := ensureRelayEventGroup(ctx); err != nil {
		t.Fatal(err)
	}
	event, _ := completeWriterFixture(common.GenerateRequestID())
	raw, _ := json.Marshal(event)
	id, err := client.XAdd(ctx, &redis.XAddArgs{Stream: common.RelayEventStream, Values: map[string]interface{}{"type": RelayEventTypeRelayResult, "payload": string(raw)}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readRelayEventBatch(ctx, "outage", ">", relayEventReadNonBlocking); err != nil {
		t.Fatal(err)
	}
	failure := &pgconn.PgError{Code: "08006", Message: "simulated connection failure"}
	if err := db.Callback().Create().Before("gorm:create").Register("test:outage", func(tx *gorm.DB) { tx.AddError(failure) }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		err := processRelayEventMessages(ctx, []redis.XMessage{{ID: id, Values: map[string]interface{}{"type": RelayEventTypeRelayResult, "payload": string(raw)}}})
		if !errors.Is(err, failure) {
			t.Errorf("attempt %d hid infrastructure error: %v", i, err)
		}
	}
	if n, _ := client.XLen(ctx, common.RelayEventStream+":dead").Result(); n != 0 {
		t.Errorf("valid events dead-lettered: %d", n)
	}
	if n, _ := client.HLen(ctx, common.RelayEventStream+":failures").Result(); n != 0 {
		t.Errorf("outage consumed retry budget: %d", n)
	}
	if pending, err := client.XPending(ctx, common.RelayEventStream, common.RelayEventGroup).Result(); err != nil || pending.Count != 1 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	db.Callback().Create().Remove("test:outage")
	if err := consumeRelayEventBatch(ctx, "outage", "0", relayEventReadNonBlocking); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := db.Model(&Log{}).Where("request_id = ?", event.ConsumeLog.RequestID).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("recovered logs=%d err=%v", n, err)
	}
	if n, _ := client.XLen(ctx, common.RelayEventStream).Result(); n != 0 {
		t.Fatalf("successful recovery left %d messages", n)
	}
}
