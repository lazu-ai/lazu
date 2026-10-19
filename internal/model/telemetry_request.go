package model

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func requestTimestamp(requestID string, fallbackSeconds int64) int64 {
	raw := strings.TrimPrefix(requestID, common.RequestIDPrefix)
	if id, err := ulid.ParseStrict(raw); err == nil {
		return int64(id.Time())
	}
	if fallbackSeconds > 0 {
		return fallbackSeconds * 1000
	}
	return time.Now().UnixMilli()
}

const relayResultDecisionContextKey = "relay_result_decision"
const relayResultAttemptsContextKey = "relay_result_attempts"
const relayResultOutcomeBuilderContextKey = "relay_result_outcome_builder"
const relayResultPublishedContextKey = "relay_result_published"

func SetRelayOutcomeBuilder(c *gin.Context, build func() *RoutingOutcome) {
	c.Set(relayResultOutcomeBuilderContextKey, build)
}
func MarkRelayResultPublished(c *gin.Context) {
	if c != nil {
		c.Set(relayResultPublishedContextKey, true)
		MarkRequestLogTarget(c, c.GetString(common.RequestIdKey))
	}
}
func BuildRelayResultEnvelope(c *gin.Context) (string, bool, error) {
	if c == nil || (!relayEventWriterRequired() && !relayEventQueueAvailable()) {
		return "", false, nil
	}
	event := relayResultEvent{}
	if value, ok := c.Get(relayResultConsumeLogContextKey); ok {
		switch row := value.(type) {
		case relayConsumeLogEvent:
			event.ConsumeLog = &row
		case *relayConsumeLogEvent:
			event.ConsumeLog = row
		}
	}
	if event.ConsumeLog == nil {
		return "", false, nil
	}
	if value, ok := c.Get(relayResultDecisionContextKey); ok {
		event.RoutingDecision, _ = value.(*RoutingDecision)
	}
	if value, ok := c.Get(relayResultAttemptsContextKey); ok {
		event.RoutingAttempts, _ = value.([]RoutingAttemptOutcome)
	}
	if value, ok := c.Get(relayResultOutcomeBuilderContextKey); ok {
		if build, ok := value.(func() *RoutingOutcome); ok {
			event.RoutingOutcome = build()
		}
	}
	body, err := json.Marshal(event)
	return string(body), true, err
}

// QueueRoutingDecision stores the immutable request snapshot in the request's
// context. No SQL or auto-increment id is required before calling upstream.
func QueueRoutingDecision(c *gin.Context, decision *RoutingDecision) int64 {
	if c == nil {
		return RecordRoutingDecision(decision)
	}
	if decision != nil {
		copy := *decision
		copy.ID = stableRoutingDecisionID(copy.RequestID)
		c.Set(relayResultDecisionContextKey, &copy)
		return copy.ID
	}
	return 0
}
func stableRoutingDecisionID(requestID string) int64 {
	sum := sha256.Sum256([]byte(requestID))
	return int64(binary.BigEndian.Uint64(sum[:8]) & 0x7fffffffffffffff)
}

// A control-plane submission can make a provisional outcome durable before
// its final consume event exists. Completion upserts this same log identity;
// counters are updated only by the event transaction.
func persistRoutingSnapshot(requestID string, at time.Time, decision *RoutingDecision, outcome *RoutingOutcome) error {
	row := Log{RequestID: requestID, CreatedAt: at.Unix(), RequestTS: requestTimestamp(requestID, at.Unix())}
	ensureConsumeLogPublicID(&row)
	foldRelayRoutingIntoLog(&row, relayResultEvent{RoutingDecision: decision, RoutingOutcome: outcome})
	fields := []string{}
	if decision != nil {
		fields = append(fields, routingDecisionLogColumns()...)
	}
	if outcome != nil {
		fields = append(fields, routingOutcomeLogColumns()...)
		fields = append(fields, "channel_name")
	}
	return TelemetryDB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_ts"}, {Name: "public_id"}}, DoUpdates: clause.AssignmentColumns(fields)}).Create(&row).Error
}

// PersistManualRoutingOutcome makes the submission verdict durable in the
// event stream before a media job is exposed. The jobs writer owns SQL; the
// embedded fallback is used when no event infrastructure exists.
func PersistManualRoutingOutcome(c *gin.Context, outcome *RoutingOutcome) error {
	event := relayResultEvent{RoutingOutcome: outcome}
	if value, ok := c.Get(relayResultDecisionContextKey); ok {
		event.RoutingDecision, _ = value.(*RoutingDecision)
	}
	if value, ok := c.Get(relayResultAttemptsContextKey); ok {
		event.RoutingAttempts, _ = value.([]RoutingAttemptOutcome)
	}
	var err error
	if relayEventWriterRequired() || relayEventQueueAvailable() {
		err = PublishRelayEvent(RelayEventTypeRoutingSnapshot, event)
	} else {
		err = persistRoutingSnapshotEvent(event)
	}
	// PostgreSQL stores media submission routing facts in a log carrier;
	// the embedded legacy routing tables do not create a diagnostics target.
	if err == nil && outcome != nil && TelemetryDB() != nil && TelemetryDB().Dialector.Name() == "postgres" {
		MarkRequestLogTarget(c, outcome.RequestID)
	}
	return err
}

func persistRoutingSnapshotEvent(event relayResultEvent) error {
	if event.RoutingOutcome == nil {
		return nil
	}
	return TelemetryDB().Transaction(func(tx *gorm.DB) error {
		if routingFactsAreViews(tx) {
			outcome := event.RoutingOutcome
			row := Log{RequestID: outcome.RequestID, CreatedAt: outcome.OutcomeAt.Unix(), RequestTS: requestTimestamp(outcome.RequestID, outcome.OutcomeAt.Unix()), Other: provisionalLogTargetJSON}
			ensureConsumeLogPublicID(&row)
			foldRelayRoutingIntoLog(&row, event)
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_ts"}, {Name: "public_id"}}, DoUpdates: clause.AssignmentColumns(append(append(routingDecisionLogColumns(), routingOutcomeLogColumns()...), "channel_name")), Where: clause.Where{Exprs: []clause.Expression{gorm.Expr("logs.type = ?", LogTypeUnknown)}}}).Create(&row).Error; err != nil {
				return err
			}
		} else {
			if event.RoutingDecision != nil {
				key := "primary:" + event.RoutingDecision.RequestID
				event.RoutingDecision.IdempotencyKey = &key
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(event.RoutingDecision).Error; err != nil {
					return err
				}
			}
			conflict := routingOutcomeConflictClause()
			conflict.Where = clause.Where{Exprs: []clause.Expression{gorm.Expr("routing_outcome.outcome_at <= excluded.outcome_at")}}
			if err := tx.Clauses(conflict).Create(event.RoutingOutcome).Error; err != nil {
				return err
			}
		}
		if len(event.RoutingAttempts) > 0 {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(event.RoutingAttempts, 500).Error
		}
		return nil
	})
}
func QueueRoutingAttempt(c *gin.Context, attempt *RoutingAttemptOutcome) error {
	if c == nil {
		return RecordRoutingAttemptOutcome(attempt)
	}
	if attempt == nil {
		return nil
	}
	var attempts []RoutingAttemptOutcome
	if v, ok := c.Get(relayResultAttemptsContextKey); ok {
		attempts, _ = v.([]RoutingAttemptOutcome)
	}
	attempts = append(attempts, *attempt)
	c.Set(relayResultAttemptsContextKey, attempts)
	return nil
}

func foldRelayRoutingIntoLog(log *Log, event relayResultEvent) {
	if event.RoutingDecision != nil {
		raw, _ := json.Marshal(event.RoutingDecision)
		log.RoutingDecisionJSON = string(raw)
		log.setRoutingDecisionColumns(event.RoutingDecision)
	}
	if event.RoutingOutcome != nil {
		raw, _ := json.Marshal(event.RoutingOutcome)
		log.RoutingOutcomeJSON = string(raw)
		log.setRoutingOutcomeColumns(event.RoutingOutcome)
		if event.RoutingOutcome.ChannelName != "" {
			log.ChannelName = event.RoutingOutcome.ChannelName
		}
	}
}

func persistRelayResultBatch(events []relayResultEvent) error {
	if len(events) == 0 || TelemetryDB() == nil {
		return nil
	}
	var committed []Log
	err := TelemetryDB().Transaction(func(tx *gorm.DB) error {
		var err error
		committed, _, err = persistRelayResultBatchTx(tx, events, nil)
		return err
	})
	if err != nil {
		return err
	}
	projectCommittedTelemetry(committed)
	return nil
}

// The jobs writer combines receipts, fresh relay facts and diagnostics in one
// transaction. Only newly claimed relay events may add aggregate counters.
func persistRelayResultBatchTx(tx *gorm.DB, events []relayResultEvent, diagnostics map[string]map[string]interface{}) ([]Log, map[string]bool, error) {
	coalesced := make(map[string]bool)

	// Drained legacy messages can predate the combined snapshot payload.
	// Resolve their decision in one writer query; current requests carry IDs.
	missing := []string{}
	for _, event := range events {
		if event.RoutingOutcome != nil && event.RoutingOutcome.DecisionID == 0 {
			missing = append(missing, event.RoutingOutcome.RequestID)
		}
	}
	if len(missing) > 0 {
		var decisions []RoutingDecision
		if err := tx.Select("id,request_id").Where("request_id IN ? AND is_primary = ?", missing, true).Find(&decisions).Error; err != nil {
			return nil, nil, err
		}
		ids := map[string]int64{}
		for _, decision := range decisions {
			ids[decision.RequestID] = decision.ID
		}
		for i, event := range events {
			if event.RoutingOutcome != nil && event.RoutingOutcome.DecisionID == 0 {
				copy := *event.RoutingOutcome
				copy.DecisionID = ids[copy.RequestID]
				events[i].RoutingOutcome = &copy
			}
		}
	}
	fresh, err := claimTelemetryEventsTx(tx, events)
	if err != nil {
		return nil, nil, err
	}
	logs := make([]Log, 0, len(fresh))
	decisions := make([]RoutingDecision, 0, len(fresh))
	outcomes := make([]RoutingOutcome, 0, len(fresh))
	attempts := []RoutingAttemptOutcome{}
	for _, event := range fresh {
		var row Log
		if event.ConsumeLog != nil {
			row = event.ConsumeLog.toLog()
		} else if event.RoutingOutcome != nil {
			outcome := event.RoutingOutcome
			row = Log{RequestID: outcome.RequestID, CreatedAt: outcome.OutcomeAt.Unix(), Type: LogTypeError, ModelName: outcome.ModelName, Group: outcome.FinalGroup, ChannelName: outcome.ChannelName}
			if outcome.UserID != nil {
				row.UserId = *outcome.UserID
			}
			if outcome.ProjectID != nil {
				row.ProjectID = *outcome.ProjectID
			}
			if outcome.ChannelID != nil {
				row.ChannelId = *outcome.ChannelID
			}
			if outcome.StatusCode != nil && *outcome.StatusCode < 400 {
				row.Type = LogTypeConsume
			}
		} else {
			continue
		}
		if err := validateTelemetryLogNumbers(&row); err != nil {
			return nil, nil, err
		}
		ensureConsumeLogPublicID(&row)
		row.RequestTS = requestTimestamp(row.RequestID, row.CreatedAt)
		foldRelayRoutingIntoLog(&row, event)
		if fields := diagnostics[row.RequestID]; len(fields) > 0 && row.Type != LogTypeUnknown {
			if err := mergeLogDiagnostics(&row, fields); err != nil {
				return nil, nil, err
			}
			coalesced[row.RequestID] = true
		}
		logs = append(logs, row)
		if event.RoutingDecision != nil {
			decision := *event.RoutingDecision
			key := "primary:" + decision.RequestID
			decision.IdempotencyKey = &key
			decisions = append(decisions, decision)
		}
		if event.RoutingOutcome != nil {
			outcomes = append(outcomes, *event.RoutingOutcome)
		}
		attempts = append(attempts, event.RoutingAttempts...)
	}
	if len(logs) > 0 {
		conflict, err := relayLogConflictClause(tx)
		if err != nil {
			return nil, nil, err
		}
		if err := tx.Clauses(conflict).CreateInBatches(&logs, 500).Error; err != nil {
			return nil, nil, err
		}
	}
	// Compatibility tables are used by SQLite/local installs. Postgres uses
	// read-only views over the compact JSON stamped on the log itself.
	if !routingFactsAreViews(tx) {
		if len(decisions) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&decisions, 500).Error; err != nil {
				return nil, nil, err
			}
		}
		if len(outcomes) > 0 {
			if err := tx.Clauses(routingOutcomeConflictClause()).CreateInBatches(&outcomes, 500).Error; err != nil {
				return nil, nil, err
			}
		}
	}
	for i := range attempts {
		if attempts[i].RequestTS == 0 {
			attempts[i].RequestTS = requestTimestamp(attempts[i].RequestID, attempts[i].OutcomeAt.Unix())
		}
	}
	if len(attempts) > 0 {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&attempts, 500).Error; err != nil {
			return nil, nil, err
		}
	}
	if err := recordSessionBatchTx(tx, logs); err != nil {
		return nil, nil, err
	}
	if err := recordIncrementalBatchTx(tx, logs); err != nil {
		return nil, nil, err
	}
	if err := recordHourlyBatchTx(tx, logs, fresh); err != nil {
		return nil, nil, err
	}
	if err := recordUsageSlicesBatchTx(tx, logs, fresh); err != nil {
		return nil, nil, err
	}

	return logs, coalesced, nil
}

func projectCommittedTelemetry(committed []Log) {
	noteProjectBudgetRejections(committed)
	// Core activity and optional RAM usage export run after the canonical
	// telemetry facts and rollups commit. Export remains best-effort.
	for i := range committed {
		if committed[i].Type == LogTypeConsume {
			StampUserRequestActivity(committed[i].UserId, committed[i].CreatedAt)
			if common.DataExportEnabled {
				row := &committed[i]
				tokens, err := checkedTelemetryTokenTotal(row.PromptTokens, row.CompletionTokens)
				if err != nil || tokens > int64(^uint(0)>>1) {
					common.SysLog("usage export token counter is out of range")
					continue
				}
				LogUsageData(row.UserId, row.Username, row.ModelName, row.AmountMicrousd, row.CreatedAt, int(tokens))
			}
		}
	}
}
