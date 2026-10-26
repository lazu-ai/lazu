package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func QueryOutcomesNeedingBackfill(start time.Time, end time.Time) ([]RoutingOutcome, error) {
	return QueryOutcomesNeedingBackfillContext(context.Background(), start, end)
}

func QueryOutcomesNeedingBackfillContext(ctx context.Context, start time.Time, end time.Time) ([]RoutingOutcome, error) {
	var outcomes []RoutingOutcome
	store := TelemetryDB().WithContext(ctx)
	predicate := "outcome_at BETWEEN ? AND ? AND (user_retried_within_5min IS NULL OR user_switched_model IS NULL)"
	query, err := routingFactQuery(store, &RoutingOutcome{}, predicate, start, end)
	if err != nil {
		return nil, err
	}
	err = query.Find(&outcomes).Error
	return outcomes, err
}

func GetDecisionByRequestID(requestID string) (*RoutingDecision, error) {
	return GetDecisionByRequestIDContext(context.Background(), requestID)
}

func GetDecisionByRequestIDContext(ctx context.Context, requestID string) (*RoutingDecision, error) {
	var decision RoutingDecision
	query, err := routingFactQuery(TelemetryDB().WithContext(ctx), &RoutingDecision{}, "request_id = ? AND is_primary = ?", requestID, true)
	if err != nil {
		return nil, err
	}
	if err := query.First(&decision).Error; err != nil {
		return nil, err
	}
	return &decision, nil
}

func GetDecisionByID(id int64) (*RoutingDecision, error) {
	var decision RoutingDecision
	if err := TelemetryDB().Where("id = ?", id).First(&decision).Error; err != nil {
		return nil, err
	}
	return &decision, nil
}

func GetOutcomeByRequestID(requestID string) (*RoutingOutcome, error) {
	return GetOutcomeByRequestIDContext(context.Background(), requestID)
}

func GetOutcomeByRequestIDContext(ctx context.Context, requestID string) (*RoutingOutcome, error) {
	var outcome RoutingOutcome
	query, err := routingFactQuery(TelemetryDB().WithContext(ctx), &RoutingOutcome{}, "request_id = ?", requestID)
	if err != nil {
		return nil, err
	}
	if err := query.First(&outcome).Error; err != nil {
		return nil, err
	}
	return &outcome, nil
}

func RecordRoutingDecision(decision *RoutingDecision) int64 {
	if TelemetryDB() == nil || decision == nil || decision.RequestID == "" {
		return 0
	}
	if relayEventWriterRequired() {
		decision.ID = stableRoutingDecisionID(decision.RequestID)
		if err := PublishRelayEvent(RelayEventTypeRoutingDecision, decision); err != nil {
			common.SysLog("failed to publish routing decision telemetry: " + err.Error())
		}
		return decision.ID
	}
	id, err := createRoutingDecisionSync(decision)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to record routing decision: %v", err))
		return 0
	}
	return id
}

func createRoutingDecisionSync(decision *RoutingDecision) (int64, error) {
	if TelemetryDB() == nil || decision == nil {
		return 0, nil
	}
	if routingFactsAreViews(TelemetryDB()) {
		decision.ID = stableRoutingDecisionID(decision.RequestID)
		return decision.ID, persistRoutingSnapshot(decision.RequestID, decision.DecisionAt, decision, nil)
	}
	if decision.IsPrimary && decision.RequestID != "" {
		key := "primary:" + decision.RequestID
		decision.IdempotencyKey = &key
		result := TelemetryDB().Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "idempotency_key"}},
			DoNothing: true,
		}).Create(decision)
		if result.Error != nil {
			return 0, result.Error
		}
		if result.RowsAffected == 0 {
			var existing RoutingDecision
			if err := TelemetryDB().Select("id").Where("idempotency_key = ?", key).First(&existing).Error; err != nil {
				return 0, err
			}
			decision.ID = existing.ID
		}
		return decision.ID, nil
	}
	if err := TelemetryDB().Create(decision).Error; err != nil {
		return 0, err
	}
	return decision.ID, nil
}

func RecordRoutingOutcome(outcome *RoutingOutcome) error {
	if TelemetryDB() == nil || outcome == nil || outcome.RequestID == "" {
		return nil
	}
	if outcome.DecisionID == 0 {
		return nil
	}
	if relayEventWriterRequired() {
		return PublishRelayEvent(RelayEventTypeRoutingOutcome, outcome)
	}
	if relayEventQueueAvailable() {
		if err := PublishRelayEvent(RelayEventTypeRoutingOutcome, outcome); err == nil {
			return nil
		} else {
			common.SysLog("failed to publish routing outcome event, falling back to sync write: " + err.Error())
		}
	}
	return recordRoutingOutcomeSync(outcome)
}

// RecordRoutingOutcomeSync publishes durably before returning in PostgreSQL;
// embedded development persists directly when it has no event writer.
func RecordRoutingOutcomeSync(outcome *RoutingOutcome) error {
	if relayEventWriterRequired() {
		return PublishRelayEvent(RelayEventTypeRoutingOutcome, outcome)
	}
	return recordRoutingOutcomeSync(outcome)
}

func recordRoutingOutcomeSync(outcome *RoutingOutcome) error {
	if TelemetryDB() == nil || outcome == nil || outcome.RequestID == "" || outcome.DecisionID == 0 {
		return nil
	}
	if routingFactsAreViews(TelemetryDB()) {
		return persistRoutingSnapshot(outcome.RequestID, outcome.OutcomeAt, nil, outcome)
	}
	return TelemetryDB().Clauses(routingOutcomeConflictClause()).Create(outcome).Error
}

func routingOutcomeConflictClause() clause.OnConflict {
	return clause.OnConflict{
		Columns: []clause.Column{{Name: "request_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"outcome_at",
			"decision_id",
			"user_id",
			"project_id",
			"model_name",
			"channel_id",
			"final_group",
			"status_code",
			"error_code",
			"error_type",
			"provider_attempted",
			"health_eligible",
			"health_failed",
			"is_retry",
			"retry_count",
			"actual_input_tokens",
			"cached_input_tokens",
			"cache_write_tokens",
			"cache_write_5m_tokens",
			"cache_write_1h_tokens",
			"cache_miss_tokens",
			"actual_output_tokens",
			"reasoning_tokens",
			"audio_input_tokens",
			"audio_output_tokens",
			"image_input_tokens",
			"image_output_tokens",
			"charged_cost_usd",
			"upstream_cost_usd",
			"gross_margin_usd",
			"total_duration_ms",
			"was_streaming",
			"stream_aborted",
			"max_stall_ms",
			"finish_reason",
		}),
	}
}

func HasUserRetried(userID *int, promptHash string, start time.Time, end time.Time) (bool, error) {
	return HasUserRetriedContext(context.Background(), userID, promptHash, start, end)
}

func HasUserRetriedContext(ctx context.Context, userID *int, promptHash string, start time.Time, end time.Time) (bool, error) {
	if userID == nil || *userID == 0 || promptHash == "" {
		return false, nil
	}
	return routingDecisionExists(TelemetryDB().WithContext(ctx), "user_id = ? AND prompt_hash = ? AND decision_at > ? AND decision_at <= ?", *userID, promptHash, start, end)
}

func HasUserSwitchedModel(sessionID string, resolvedModel string, start time.Time, end time.Time) (bool, error) {
	return HasUserSwitchedModelContext(context.Background(), sessionID, resolvedModel, start, end)
}

func HasUserSwitchedModelContext(ctx context.Context, sessionID string, resolvedModel string, start time.Time, end time.Time) (bool, error) {
	if sessionID == "" || resolvedModel == "" {
		return false, nil
	}
	return routingDecisionExists(TelemetryDB().WithContext(ctx), "session_id = ? AND resolved_model <> ? AND decision_at > ? AND decision_at <= ?", sessionID, resolvedModel, start, end)
}

func UpdateOutcomeQualitySignalsContext(ctx context.Context, requestID string, retried bool, switched bool) error {
	store := TelemetryDB().WithContext(ctx)
	if routingFactsAreViews(store) {
		result := store.Model(&Log{}).Where("request_ts = ? AND request_id = ? AND ro_request_id IS NOT NULL", requestTimestamp(requestID, 0), requestID).
			Updates(map[string]interface{}{
				"routing_outcome_json":        gorm.Expr("(routing_outcome_json::jsonb || jsonb_build_object('user_retried_within_5min', ?::boolean, 'user_switched_model', ?::boolean))::text", retried, switched),
				"ro_user_retried_within_5min": retried,
				"ro_user_switched_model":      switched,
			})
		if result.Error != nil || result.RowsAffected > 0 {
			return result.Error
		}
		return store.Table("routing_outcome_legacy").Where("request_id = ?", requestID).Updates(map[string]interface{}{"user_retried_within_5min": retried, "user_switched_model": switched}).Error
	}
	return store.Model(&RoutingOutcome{}).
		Where("request_id = ?", requestID).
		Updates(map[string]any{
			"user_retried_within_5min": retried,
			"user_switched_model":      switched,
		}).Error
}

// routingFactQuery reads routing facts from the typed log columns and any
// pre-cutover history, filtering each branch before the correlated
// superseded-history lookup. OFFSET 0 keeps that lookup a per-row index probe
// instead of a full retained-history anti-join. request_ts is deliberately not
// a time filter: ingress time and the fact's own timestamp need not fall in
// the same window. Parsing GORM's cached Go schema performs no database probes.
func routingFactQuery(store *gorm.DB, fact interface{}, predicate string, args ...interface{}) (*gorm.DB, error) {
	if !routingFactsAreViews(store) {
		return store.Model(fact).Where(predicate, args...), nil
	}
	statement := &gorm.Statement{DB: store}
	if err := statement.Parse(fact); err != nil {
		return nil, err
	}
	table := statement.Schema.Table
	layout := routingDecisionLayout()
	if table == "routing_outcome" {
		layout = routingOutcomeLayout()
	}
	columns := map[string]string{}
	for _, field := range layout.fields {
		columns[field.dbName] = "snap." + field.column
	}
	for _, field := range layout.excluded {
		columns[field.dbName] = "NULL::" + field.sqlType
	}
	var projected, historical []string
	for _, field := range statement.Schema.Fields {
		if field.DBName == "" {
			continue
		}
		projected = append(projected, columns[field.DBName]+" AS "+field.DBName)
		historical = append(historical, "fact."+field.DBName)
	}
	sql := "SELECT fact.* FROM (SELECT " + strings.Join(projected, ",") + " FROM logs snap WHERE snap." + layout.marker + " IS NOT NULL) fact WHERE " + predicate +
		" UNION ALL SELECT " + strings.Join(historical, ",") + " FROM " + table + "_legacy fact WHERE (" + predicate + ") AND NOT EXISTS (" +
		"SELECT 1 FROM logs newer WHERE newer.request_id = fact.request_id AND newer." + layout.marker + " IS NOT NULL OFFSET 0)"
	values := append(append([]interface{}{}, args...), args...)
	source := store.Raw(sql, values...)
	return store.Table("(?) AS "+table, source), nil
}

func routingDecisionExists(store *gorm.DB, predicate string, args ...interface{}) (bool, error) {
	query, err := routingFactQuery(store, &RoutingDecision{}, predicate, args...)
	if err != nil {
		return false, err
	}
	var found struct{ Present bool }
	err = store.Raw("SELECT EXISTS (?) AS present", query.Select("1").Limit(1)).Scan(&found).Error
	return found.Present, err
}
