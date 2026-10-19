package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

const (
	RelayEventTypeConsumeLog            = "consume_log"
	RelayEventTypeRoutingDecision       = "routing_decision"
	RelayEventTypeRoutingOutcome        = "routing_outcome"
	RelayEventTypeRoutingAttemptOutcome = "routing_attempt_outcome"
	RelayEventTypeModelCounter          = "model_counter"
	RelayEventTypeRelayResult           = "relay_result"
	RelayEventTypeRequestDiagnostics    = "request_diagnostics"
	RelayEventTypeRoutingSnapshot       = "routing_snapshot"
	RelayEventTypeFinancialSettlement   = "financial_settlement"
	relayEventReadNonBlocking           = time.Duration(-1)
)

type RequestDiagnosticsEvent struct {
	RequestID string                 `json:"request_id"`
	Fields    map[string]interface{} `json:"fields"`
}

type relayModelCounterEvent struct {
	ModelName string `json:"model_name"`
}

var relayEventConsumerOnce sync.Once

func relayEventQueueAvailable() bool {
	return (relayEventWriterRequired() || common.RelayEventQueueEnabled) && common.RedisEnabled && common.RDB != nil
}

// PostgreSQL deployments always publish request telemetry to the jobs writer.
// Queue configuration and Redis outages must not restore API-side SQL writes.
func relayEventWriterRequired() bool {
	return CoreDB() != nil && CoreDB().Dialector.Name() == "postgres"
}

func PublishRelayEvent(eventType string, payload any) error {
	if !relayEventQueueAvailable() {
		common.IncrementHotPathCounter(common.HotPathCounterRelayEventPublishFailed)
		return errors.New("relay event queue is not available")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	timer := common.StartHotPathStage(common.HotPathStageRelayEventPublish)
	err = common.RDB.Eval(ctx, `local before=redis.call('XLEN',KEYS[1]);redis.call('XADD',KEYS[1],'MAXLEN','=',ARGV[1],'*','type',ARGV[2],'payload',ARGV[3]);local after=redis.call('XLEN',KEYS[1]);local dropped=before+1-after;if dropped>0 then redis.call('INCRBY',KEYS[2],dropped) end;return 1`, []string{common.RelayEventStream, common.RelayEventStream + ":dropped"}, relayEventMaxLength(), eventType, string(raw)).Err()
	timer.Finish(err)
	if err != nil {
		common.IncrementHotPathCounter(common.HotPathCounterRelayEventPublishFailed)
	}
	return err
}

func relayEventXAddArgs(eventType string, raw []byte) *redis.XAddArgs {
	return &redis.XAddArgs{
		Stream: common.RelayEventStream,
		MaxLen: relayEventMaxLength(),
		Values: map[string]any{
			"type":    eventType,
			"payload": string(raw),
		},
	}
}

func StartRelayEventConsumer(ctx context.Context) {
	if !relayEventConsumerAvailable() {
		return
	}
	relayEventConsumerOnce.Do(func() { go RunRelayEventConsumer(ctx) })
}

// RunRelayEventConsumer blocks until cancellation, allowing all-in-one shutdown
// to join the consumer before closing the stores.
func RunRelayEventConsumer(ctx context.Context) {
	if !relayEventConsumerAvailable() {
		return
	}
	for ctx.Err() == nil {
		if err := ensureRelayEventGroup(ctx); err != nil {
			if !logRelayEventReadError(ctx, err) {
				return
			}
			continue
		}
		consumeRelayEvents(ctx)
		return
	}
}

func relayEventConsumerAvailable() bool {
	return common.RelayEventConsumerEnabled && relayEventQueueAvailable() && CoreDB() != nil && TelemetryDB() != nil
}

// DrainRelayEvents processes a bounded number of Redis Stream batches for
// externally scheduled runtimes. It lets one-shot Jobs Containers own relay
// persistence without leaving a database-writing consumer inside every API
// replica.
func DrainRelayEvents(ctx context.Context, consumer string, maxBatches int) (int, error) {
	if !relayEventConsumerAvailable() {
		return 0, errors.New("relay event consumer is not available")
	}
	if maxBatches <= 0 {
		maxBatches = 64
	}
	consumer = strings.TrimSpace(consumer)
	if consumer == "" {
		consumer = fmt.Sprintf("%s-%d", firstNonEmptyString(common.NodeName, "worker"), os.Getpid())
	}
	if err := ensureRelayEventGroup(ctx); err != nil {
		return 0, err
	}
	processed, reclaimedBatches, err := reclaimStaleRelayEventsLimited(ctx, consumer, maxBatches)
	if err != nil {
		return processed, err
	}

	for range maxBatches - reclaimedBatches {
		streams, err := readRelayEventBatch(ctx, consumer, ">", relayEventReadNonBlocking)
		if errors.Is(err, redis.Nil) {
			return processed, nil
		}
		if err != nil {
			return processed, err
		}
		batchSize := 0
		for _, stream := range streams {
			batchSize += len(stream.Messages)
			if err := processRelayEventMessages(ctx, stream.Messages); err != nil {
				return processed, err
			}
		}
		processed += batchSize
		if batchSize < 64 {
			return processed, nil
		}
	}
	return processed, nil
}

func ensureRelayEventGroup(ctx context.Context) error {
	err := common.RDB.XGroupCreateMkStream(ctx, common.RelayEventStream, common.RelayEventGroup, "0").Err()
	if err == nil || strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return err
}

func consumeRelayEvents(ctx context.Context) {
	consumer := strings.TrimSpace(common.RelayEventConsumer)
	if consumer == "" {
		consumer = fmt.Sprintf("%s-%d", firstNonEmptyString(common.NodeName, "api"), os.Getpid())
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := consumeRelayEventBatch(ctx, consumer, ">", time.Second); err != nil {
			if !logRelayEventReadError(ctx, err) {
				return
			}
			continue
		}
		if err := consumeRelayEventBatch(ctx, consumer, "0", relayEventReadNonBlocking); err != nil {
			if !logRelayEventReadError(ctx, err) {
				return
			}
		}
		if err := reclaimStaleRelayEvents(ctx, consumer); err != nil {
			if !logRelayEventReadError(ctx, err) {
				return
			}
		}
	}
}

func consumeRelayEventBatch(ctx context.Context, consumer string, id string, block time.Duration) error {
	streams, err := readRelayEventBatch(ctx, consumer, id, block)
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	messages := make([]redis.XMessage, 0, relayWriterMaxMessages)
	for _, stream := range streams {
		messages = append(messages, stream.Messages...)
	}
	// XREADGROUP's BLOCK wait ends when the first item arrives. A separate
	// flush deadline lets subsequent arrivals share the SQL transaction.
	// Pending retries and explicit drains remain immediate.
	if id == ">" && block > 0 && len(messages) > 0 {
		flushAt := time.Now().Add(time.Second)
		bounds := relayWriterBounds{}
		for _, message := range messages {
			bounds.add(message)
		}
		for bounds.readCount() > 0 {
			remaining := time.Until(flushAt)
			if remaining < time.Millisecond {
				break
			}
			more, readErr := readRelayEventBatchLimit(ctx, consumer, id, remaining, bounds.readCount())
			if errors.Is(readErr, redis.Nil) {
				break
			}
			if readErr != nil {
				// Already-read entries remain pending and will be retried by the
				// same consumer or reclaimed after a restart.
				return readErr
			}
			for _, stream := range more {
				messages = append(messages, stream.Messages...)
				for _, message := range stream.Messages {
					bounds.add(message)
				}
			}
		}
	}
	return processRelayEventMessages(ctx, messages)
}

func reclaimStaleRelayEvents(ctx context.Context, consumer string) error {
	_, _, err := reclaimStaleRelayEventsLimited(ctx, consumer, 0)
	return err
}

func reclaimStaleRelayEventsLimited(ctx context.Context, consumer string, maxBatches int) (int, int, error) {
	minIdle := time.Duration(common.GetEnvOrDefault("RELAY_EVENT_PENDING_MIN_IDLE_SECONDS", 60)) * time.Second
	if minIdle <= 0 {
		minIdle = time.Minute
	}
	start := "0-0"
	processed := 0
	batches := 0
	for maxBatches <= 0 || batches < maxBatches {
		messages, next, err := autoClaimRelayEventBatch(ctx, consumer, minIdle, start)
		if errors.Is(err, redis.Nil) {
			return processed, batches, nil
		}
		if err != nil {
			return processed, batches, err
		}
		if err := processRelayEventMessages(ctx, messages); err != nil {
			return processed, batches, err
		}
		processed += len(messages)
		if len(messages) > 0 {
			batches++
		}
		if next == "" || next == "0-0" || len(messages) == 0 {
			return processed, batches, nil
		}
		start = next
	}
	return processed, batches, nil
}

// go-redis v8 predates the third "deleted IDs" element added to XAUTOCLAIM
// replies by Redis 7. Read the raw response so both the Redis 6 two-element
// form and Redis 7 three-element form remain supported.
func autoClaimRelayEventBatch(
	ctx context.Context,
	consumer string,
	minIdle time.Duration,
	start string,
) ([]redis.XMessage, string, error) {
	raw, err := common.RDB.Do(
		ctx,
		"XAUTOCLAIM",
		common.RelayEventStream,
		common.RelayEventGroup,
		consumer,
		minIdle.Milliseconds(),
		start,
		"COUNT",
		500,
	).Result()
	if err != nil {
		return nil, "", err
	}
	return parseAutoClaimRelayEventReply(raw)
}

func parseAutoClaimRelayEventReply(raw any) ([]redis.XMessage, string, error) {
	reply, ok := raw.([]any)
	if !ok || len(reply) < 2 {
		return nil, "", fmt.Errorf("unexpected XAUTOCLAIM reply type %T", raw)
	}
	next, ok := redisReplyString(reply[0])
	if !ok {
		return nil, "", fmt.Errorf("unexpected XAUTOCLAIM cursor type %T", reply[0])
	}
	rawMessages, ok := reply[1].([]any)
	if !ok {
		return nil, "", fmt.Errorf("unexpected XAUTOCLAIM messages type %T", reply[1])
	}
	messages := make([]redis.XMessage, 0, len(rawMessages))
	for _, rawMessage := range rawMessages {
		messageParts, ok := rawMessage.([]any)
		if !ok || len(messageParts) != 2 {
			return nil, "", fmt.Errorf("unexpected XAUTOCLAIM message type %T", rawMessage)
		}
		id, ok := redisReplyString(messageParts[0])
		if !ok {
			return nil, "", fmt.Errorf("unexpected XAUTOCLAIM message id type %T", messageParts[0])
		}
		rawValues, ok := messageParts[1].([]any)
		if !ok || len(rawValues)%2 != 0 {
			return nil, "", fmt.Errorf("unexpected XAUTOCLAIM values type %T", messageParts[1])
		}
		values := make(map[string]any, len(rawValues)/2)
		for index := 0; index < len(rawValues); index += 2 {
			key, ok := redisReplyString(rawValues[index])
			if !ok {
				return nil, "", fmt.Errorf("unexpected XAUTOCLAIM field type %T", rawValues[index])
			}
			values[key] = rawValues[index+1]
		}
		messages = append(messages, redis.XMessage{ID: id, Values: values})
	}
	return messages, next, nil
}

func redisReplyString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case []byte:
		return string(typed), true
	default:
		return "", false
	}
}

func processRelayEventMessages(ctx context.Context, messages []redis.XMessage) error {
	for _, bounded := range splitRelayWriterMessages(messages) {
		if err := processRelayWriterMessages(ctx, bounded); err != nil {
			return err
		}
	}
	return nil
}

func processRelayWriterMessages(ctx context.Context, messages []redis.XMessage) error {
	batch := relayWriterBatch{}
	accepted := make([]redis.XMessage, 0, len(messages))
	for _, message := range messages {
		kind, _ := redisReplyString(message.Values["type"])
		payload, _ := redisReplyString(message.Values["payload"])
		if relayMessageBytes(message) > relayWriterMaxBytes {
			if err := failRelayEvent(ctx, message, errors.New("telemetry event exceeds writer byte limit")); err != nil {
				return err
			}
			continue
		}
		if kind != RelayEventTypeRelayResult && kind != RelayEventTypeFinancialSettlement && kind != RelayEventTypeRequestDiagnostics {
			// Pre-cutover messages keep their historical handlers until drained.
			if err := handleRelayEvent(kind, []byte(payload)); err != nil {
				if err := failRelayEvent(ctx, message, err); err != nil {
					return err
				}
			} else {
				ackAndDeleteRelayEventMessage(ctx, message)
			}
			continue
		}
		if err := batch.decode(kind, payload); err != nil {
			if err := failRelayEvent(ctx, message, err); err != nil {
				return err
			}
			continue
		}
		accepted = append(accepted, message)
	}
	if len(accepted) == 0 {
		return nil
	}
	found, err := persistRelayWriterBatch(batch)
	if err == nil {
		return acknowledgeRelayWriterMessages(ctx, accepted, found, nil)
	}
	if !permanentRelayStoreError(err) {
		return err
	}
	// A malformed request must not keep unrelated requests pending. Preserve
	// the receipt/facts/diagnostic transaction while isolating request groups.
	for _, group := range groupRelayWriterMessages(accepted) {
		one := relayWriterBatch{}
		for _, message := range group {
			kind, _ := redisReplyString(message.Values["type"])
			payload, _ := redisReplyString(message.Values["payload"])
			if err := one.decode(kind, payload); err != nil {
				return err // Every accepted payload was already decoded above.
			}
		}
		found, err := persistRelayWriterBatch(one)
		if err := acknowledgeRelayWriterMessages(ctx, group, found, err); err != nil {
			return err
		}
	}
	return nil
}

// Unknown errors remain pending: connection loss, shutdown, serialization and
// deadlocks must never consume the payload's dead-letter retry budget.
func permanentRelayStoreError(err error) bool {
	if errors.Is(err, ErrTelemetryCounterRange) {
		return true
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		code := state.SQLState()
		return strings.HasPrefix(code, "22") || strings.HasPrefix(code, "23")
	}
	return errors.Is(err, gorm.ErrDuplicatedKey) || errors.Is(err, gorm.ErrForeignKeyViolated)
}

func acknowledgeRelayWriterMessages(ctx context.Context, messages []redis.XMessage, found map[string]bool, failure error) error {
	if failure != nil && !permanentRelayStoreError(failure) {
		return failure
	}
	acked := make([]redis.XMessage, 0, len(messages))
	for _, message := range messages {
		err := failure
		kind, _ := redisReplyString(message.Values["type"])
		if err == nil && kind == RelayEventTypeRequestDiagnostics {
			payload, _ := redisReplyString(message.Values["payload"])
			var patch RequestDiagnosticsEvent
			_ = json.Unmarshal([]byte(payload), &patch)
			if patch.RequestID != "" && len(patch.Fields) > 0 && !found[patch.RequestID] {
				// A delayed/spooled result is a missing dependency, not poison.
				// Leave the patch pending until its result is persisted.
				continue
			}
		}
		if err != nil {
			if err := failRelayEvent(ctx, message, err); err != nil {
				return err
			}
		} else {
			acked = append(acked, message)
		}
	}
	return ackRelayEventMessageBatch(ctx, acked)
}

func ackRelayEventMessageBatch(ctx context.Context, messages []redis.XMessage) error {
	if len(messages) == 0 {
		return nil
	}
	pipe := common.RDB.TxPipeline()
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
		pipe.HDel(ctx, common.RelayEventStream+":failures", message.ID)
	}
	pipe.XAck(ctx, common.RelayEventStream, common.RelayEventGroup, ids...)
	pipe.XDel(ctx, common.RelayEventStream, ids...)
	_, err := pipe.Exec(ctx)
	return err
}

func processRelayResultEventMessages(ctx context.Context, messages []redis.XMessage) (bool, error) {
	for _, message := range messages {
		kind, _ := redisReplyString(message.Values["type"])
		if kind != RelayEventTypeRelayResult {
			return false, nil
		}
	}
	return true, processRelayEventMessages(ctx, messages)
}

func relayEventMaxLength() int64 {
	size := common.GetEnvOrDefault("TELEMETRY_STREAM_MAX_LENGTH", 100000)
	if size < 1 {
		size = 100000
	}
	return int64(size)
}

func failRelayEvent(ctx context.Context, message redis.XMessage, failure error) error {
	_, err := common.RDB.Eval(ctx, `local tries=redis.call('HINCRBY',KEYS[2],ARGV[1],1);if tries<5 then return tries end;redis.call('XADD',KEYS[3],'*','original_id',ARGV[1],'type',ARGV[3],'payload',ARGV[4],'error',ARGV[5]);redis.call('INCR',KEYS[4]);redis.call('XACK',KEYS[1],ARGV[2],ARGV[1]);redis.call('XDEL',KEYS[1],ARGV[1]);redis.call('HDEL',KEYS[2],ARGV[1]);return tries`, []string{common.RelayEventStream, common.RelayEventStream + ":failures", common.RelayEventStream + ":dead", common.RelayEventStream + ":dead_count"}, message.ID, common.RelayEventGroup, message.Values["type"], message.Values["payload"], truncateString(failure.Error(), 512)).Result()
	return err
}

func ackAndDeleteRelayEventMessage(ctx context.Context, message redis.XMessage) {
	if err := common.RDB.XAck(ctx, common.RelayEventStream, common.RelayEventGroup, message.ID).Err(); err != nil {
		common.SysLog(fmt.Sprintf("failed to ack relay event %s: %v", message.ID, err))
		return
	}
	if err := common.RDB.XDel(ctx, common.RelayEventStream, message.ID).Err(); err != nil {
		common.SysLog(fmt.Sprintf("failed to delete acknowledged relay event %s: %v", message.ID, err))
	}
}

func logRelayEventReadError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	common.SysLog("failed to read relay events: " + err.Error())
	time.Sleep(time.Second)
	return true
}

func readRelayEventBatch(ctx context.Context, consumer string, id string, block time.Duration) ([]redis.XStream, error) {
	return readRelayEventBatchLimit(ctx, consumer, id, block, 500)
}

func readRelayEventBatchLimit(ctx context.Context, consumer string, id string, block time.Duration, count int64) ([]redis.XStream, error) {
	return common.RDB.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    common.RelayEventGroup,
		Consumer: consumer,
		Streams:  []string{common.RelayEventStream, id},
		Count:    count,
		Block:    block,
	}).Result()
}

func handleRelayEventMessage(message redis.XMessage) error {
	eventType, _ := message.Values["type"].(string)
	payload, _ := message.Values["payload"].(string)
	return handleRelayEvent(eventType, []byte(payload))
}

func handleRelayEvent(eventType string, payload []byte) error {
	switch eventType {
	case RelayEventTypeFinancialSettlement:
		var receipt TelemetryFinancialReceipt
		if err := json.Unmarshal(payload, &receipt); err != nil {
			return err
		}
		return persistFinancialTelemetry(receipt)
	case RelayEventTypeRequestDiagnostics:
		var event RequestDiagnosticsEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return err
		}
		return UpdateRequestLogDiagnostics(event.RequestID, event.Fields)
	case RelayEventTypeRelayResult:
		var event relayResultEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return err
		}
		return handleRelayResultEvent(event)
	case RelayEventTypeRoutingSnapshot:
		var event relayResultEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return err
		}
		return persistRoutingSnapshotEvent(event)
	case RelayEventTypeConsumeLog:
		var event relayConsumeLogEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return err
		}
		return persistRelayResultBatch([]relayResultEvent{{ConsumeLog: &event}})
	case RelayEventTypeRoutingDecision:
		var row RoutingDecision
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		_, err := createRoutingDecisionSync(&row)
		return err
	case RelayEventTypeRoutingOutcome:
		var row RoutingOutcome
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		if row.DecisionID == 0 {
			if decision, err := GetDecisionByRequestID(row.RequestID); err == nil && decision != nil {
				row.DecisionID = decision.ID
			} else {
				return fmt.Errorf("routing decision for request %s is not available", row.RequestID)
			}
		}
		return recordRoutingOutcomeSync(&row)
	case RelayEventTypeRoutingAttemptOutcome:
		var row RoutingAttemptOutcome
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		if row.DecisionID == 0 {
			if decision, err := GetDecisionByRequestID(row.RequestID); err == nil && decision != nil {
				row.DecisionID = decision.ID
			} else {
				return fmt.Errorf("routing decision for request %s is not available", row.RequestID)
			}
		}
		return recordRoutingAttemptOutcomeSync(&row)
	case RelayEventTypeModelCounter:
		var row relayModelCounterEvent
		if err := json.Unmarshal(payload, &row); err != nil {
			return err
		}
		return IncrementModelRequests(row.ModelName)
	default:
		return fmt.Errorf("unknown relay event type %q", eventType)
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
