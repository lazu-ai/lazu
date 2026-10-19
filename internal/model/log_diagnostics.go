package model

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdateRequestLogDiagnosticsBatch merges independent diagnostic events for
// each request and updates one PostgreSQL batch. Missing rows remain unacked so
// a diagnostic published before its final log can be retried by the consumer.
func UpdateRequestLogDiagnosticsBatch(events []RequestDiagnosticsEvent) (map[string]bool, error) {
	if len(events) == 0 {
		return map[string]bool{}, nil
	}
	db := TelemetryDB()
	if db == nil {
		return nil, errors.New("telemetry database unavailable")
	}
	if db.Dialector.Name() == "postgres" {
		return updateRequestLogDiagnosticsBatchTx(db, events)
	}
	var found map[string]bool
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		found, err = updateRequestLogDiagnosticsBatchTx(tx, events)
		return err
	})
	return found, err
}

func updateRequestLogDiagnosticsBatchTx(db *gorm.DB, events []RequestDiagnosticsEvent) (map[string]bool, error) {
	merged := make(map[string]map[string]interface{}, len(events))
	for _, event := range events {
		if event.RequestID == "" || len(event.Fields) == 0 {
			continue
		}
		if merged[event.RequestID] == nil {
			merged[event.RequestID] = make(map[string]interface{}, len(event.Fields))
		}
		for field, value := range event.Fields {
			merged[event.RequestID][field] = value
		}
	}
	found := make(map[string]bool, len(merged))
	if len(merged) == 0 {
		return found, nil
	}
	if db.Dialector.Name() != "postgres" {
		for requestID, fields := range merged {
			if err := updateRequestLogDiagnosticsPortableTx(db, requestID, fields); errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			} else if err != nil {
				return nil, err
			}
			found[requestID] = true
		}
		return found, nil
	}
	values := make([]string, 0, len(merged))
	args := make([]interface{}, 0, len(merged)*3+2)
	var minimumTS, maximumTS int64
	allHaveTS := true
	for requestID, fields := range merged {
		raw, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		var requestTS interface{}
		if id, err := ulid.ParseStrict(strings.TrimPrefix(requestID, common.RequestIDPrefix)); err == nil {
			ts := int64(id.Time())
			requestTS = ts
			if minimumTS == 0 || ts < minimumTS {
				minimumTS = ts
			}
			if ts > maximumTS {
				maximumTS = ts
			}
		} else {
			allHaveTS = false
		}
		values = append(values, "(?::text, ?::bigint, ?::jsonb)")
		args = append(args, requestID, requestTS, string(raw))
	}
	query := "UPDATE logs AS logs SET other = CASE WHEN logs.type = 0 AND NOT (" + provisionalLogTargetSQL("logs.other") + ") THEN logs.other ELSE (COALESCE(NULLIF(logs.other, ''), '{}')::jsonb || patch.fields)::text END FROM (VALUES " + strings.Join(values, ",") + ") AS patch(request_id,request_ts,fields) WHERE logs.request_id = patch.request_id AND (patch.request_ts IS NULL OR logs.request_ts = patch.request_ts)"
	if allHaveTS {
		query += " AND logs.request_ts BETWEEN ? AND ?"
		args = append(args, minimumTS, maximumTS)
	}
	query += " RETURNING logs.request_id"
	var rows []struct{ RequestID string }
	if err := db.Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		found[row.RequestID] = true
	}
	return found, nil
}

// UpdateRequestLogDiagnostics only augments diagnostic fields after the relay
// ends. It never creates or changes accounting records. Locking the row keeps
// independently completed token-shadow and timing updates from overwriting one
// another; PostgreSQL's JSON merge takes one statement in production.
func UpdateRequestLogDiagnostics(requestID string, fields map[string]interface{}) error {
	if requestID == "" || len(fields) == 0 {
		return nil
	}
	db := TelemetryDB()
	if db == nil {
		return errors.New("telemetry database unavailable")
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	query := db.Where("request_id = ?", requestID)
	if id, parseErr := ulid.ParseStrict(strings.TrimPrefix(requestID, common.RequestIDPrefix)); parseErr == nil {
		// Public ids encode the exact partition key; old ids retain compatibility.
		query = query.Where("request_ts = ?", int64(id.Time()))
	}
	if db.Dialector.Name() == "postgres" {
		result := query.Model(&Log{}).UpdateColumn("other", gorm.Expr("CASE WHEN type = 0 AND NOT ("+provisionalLogTargetSQL("other")+") THEN other ELSE (COALESCE(NULLIF(other, ''), '{}')::jsonb || ?::jsonb)::text END", string(raw)))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return updateRequestLogDiagnosticsPortableTx(tx, requestID, fields)
	})
}

func mergeLogDiagnostics(row *Log, fields map[string]interface{}) error {
	current := map[string]interface{}{}
	if strings.TrimSpace(row.Other) != "" {
		if err := json.Unmarshal([]byte(row.Other), &current); err != nil {
			return err
		}
	}
	if current == nil {
		current = make(map[string]interface{})
	}
	for key, value := range fields {
		current[key] = value
	}
	merged, err := json.Marshal(current)
	if err != nil {
		return err
	}
	row.Other = string(merged)
	return nil
}

func updateRequestLogDiagnosticsPortableTx(tx *gorm.DB, requestID string, fields map[string]interface{}) error {
	var rows []Log
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "request_ts", "type", "other").Where("request_id = ?", requestID)
	if id, err := ulid.ParseStrict(strings.TrimPrefix(requestID, common.RequestIDPrefix)); err == nil {
		query = query.Where("request_ts = ?", int64(id.Time()))
	}
	if err := query.Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return gorm.ErrRecordNotFound
	}
	for _, row := range rows {
		if row.Type == LogTypeUnknown && !hasProvisionalLogTarget(row.Other) {
			continue
		}
		if err := mergeLogDiagnostics(&row, fields); err != nil {
			return err
		}
		if err := tx.Model(&Log{}).Where("id = ? AND request_ts = ?", row.Id, row.RequestTS).UpdateColumn("other", row.Other).Error; err != nil {
			return err
		}
	}
	return nil
}
