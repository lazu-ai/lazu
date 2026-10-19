package model

import (
	"context"
	"reflect"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Preserve the existing independent policies when final routing facts move
// into logs. Permanent finance, rollups and event receipts are never cleaned
// by these detail-retention functions.
const (
	RoutingDecisionRetentionDays = 90
	RoutingAttemptRetentionDays  = 90
	RoutingOutcomeRetentionDays  = 365
	ChannelProbeRetentionDays    = 30
)

func effectiveTelemetryLogRetentionDays(days int) int {
	if days <= 0 || int64(days) > int64((1<<63-1)/(24*time.Hour)) {
		return 0
	}
	if days < ModelRankingWindowDays {
		return ModelRankingWindowDays
	}
	return days
}

// ExpireRequestLogDetails removes the original log reader's data without
// erasing a co-located routing outcome that still has its own valid lifetime.
// Only keys and routing JSON remain in a hidden LogTypeUnknown carrier.
func ExpireRequestLogDetails(ctx context.Context, cutoff time.Time) (int64, error) {
	db := TelemetryDB()
	if db == nil || cutoff.IsZero() {
		return 0, nil
	}
	if db.Dialector.Name() != "postgres" {
		result := db.WithContext(ctx).Where("created_at < ?", cutoff.Unix()).Delete(&Log{})
		return result.RowsAffected, result.Error
	}
	return expireRequestLogDetailsTx(db.WithContext(ctx), cutoff)
}

func expireRequestLogDetailsTx(tx *gorm.DB, cutoff time.Time) (int64, error) {
	return expireRequestLogDetailsTableTx(tx, "logs", cutoff)
}

func expireRequestLogDetailsTableTx(tx *gorm.DB, table string, cutoff time.Time) (int64, error) {
	parsed, err := schema.Parse(&Log{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		return 0, err
	}
	// Routing facts, JSON and typed columns alike, outlive request details;
	// expireTelemetryFactsTx retires them on their own schedule.
	protected := routingFactColumnSet()
	for _, column := range []string{"id", "public_id", "request_ts", "request_id", "created_at"} {
		protected[column] = true
	}
	updates := make(map[string]interface{}, len(parsed.Fields))
	zero := reflect.ValueOf(Log{})
	for _, field := range parsed.Fields {
		if field.DBName == "" || protected[field.DBName] {
			continue
		}
		// The pre-partition archive stopped gaining columns at schema 24;
		// clear only what it has, or one later Log field fails all retention.
		if table != "logs" && !tx.Migrator().HasColumn(table, field.DBName) {
			continue
		}
		value, _ := field.ValueOf(tx.Statement.Context, zero)
		updates[field.DBName] = value
	}
	// New media carriers keep only temporary diagnostic metadata. Expiry
	// removes its marker too, so a delayed patch or snapshot cannot revive it.
	result := tx.Model(&Log{}).Table(table).Where("created_at < ? AND (type <> ? OR ("+provisionalLogTargetSQL("other")+"))", cutoff.Unix(), LogTypeUnknown).Updates(updates)
	return result.RowsAffected, result.Error
}

func expireTelemetryFactsTx(tx *gorm.DB, now time.Time, logRetentionDays int) error {
	logTables := []string{"logs"}
	if tx.Migrator().HasTable("logs_unpartitioned_v24") {
		logTables = append(logTables, "logs_unpartitioned_v24")
	}
	if days := effectiveTelemetryLogRetentionDays(logRetentionDays); days > 0 {
		for _, table := range logTables {
			if _, err := expireRequestLogDetailsTableTx(tx, table, now.Add(-time.Duration(days)*24*time.Hour)); err != nil {
				return err
			}
		}
	}
	for _, fact := range []struct {
		column, jsonTime, legacyTable, legacyTime string
		layout                                    routingFactLayout
		timeColumn                                string
		days                                      int
	}{
		{"routing_decision_json", "decision_at", "routing_decision_legacy", "decision_at", routingDecisionLayout(), "rd_decision_at", RoutingDecisionRetentionDays},
		{"routing_outcome_json", "outcome_at", "routing_outcome_legacy", "outcome_at", routingOutcomeLayout(), "ro_outcome_at", RoutingOutcomeRetentionDays},
	} {
		cutoff := now.UTC().Add(-time.Duration(fact.days) * 24 * time.Hour)
		// A fact retires by its own time, which for media may be later than
		// admission; the JSON copy and every typed column go together.
		cleared := map[string]interface{}{fact.column: ""}
		for _, field := range fact.layout.fields {
			cleared[field.column] = gorm.Expr("NULL")
		}
		if err := tx.Model(&Log{}).Table("logs").
			Where(fact.layout.marker+" IS NOT NULL AND COALESCE("+fact.timeColumn+", to_timestamp(created_at)) < ?", cutoff).
			UpdateColumns(cleared).Error; err != nil {
			return err
		}
		// The pre-partition copy has no typed columns and no post-cutover facts.
		for _, table := range logTables[1:] {
			condition := "NULLIF(" + fact.column + ",'') IS NOT NULL AND COALESCE((NULLIF(" + fact.column + ",'')::jsonb ->> '" + fact.jsonTime + "')::timestamptz, to_timestamp(created_at)) < ?"
			if err := tx.Model(&Log{}).Table(table).Where(condition, cutoff).UpdateColumn(fact.column, "").Error; err != nil {
				return err
			}
		}
		if err := tx.Table(fact.legacyTable).Where(fact.legacyTime+" < ?", cutoff).Delete(map[string]interface{}{}).Error; err != nil {
			return err
		}
	}
	if days := effectiveTelemetryLogRetentionDays(logRetentionDays); days > 0 {
		// No read contract needs the stable request/public identity after its
		// final retained fact expires. Receipts keep redelivery idempotence.
		for _, table := range logTables {
			if err := tx.Table(table).Where("created_at < ? AND type = ? AND NULLIF(routing_decision_json,'') IS NULL AND NULLIF(routing_outcome_json,'') IS NULL", now.Add(-time.Duration(days)*24*time.Hour).Unix(), LogTypeUnknown).Delete(map[string]interface{}{}).Error; err != nil {
				return err
			}
		}
	}
	for _, fact := range []struct {
		table, timestamp string
		days             int
	}{
		{"routing_attempt_outcome", "outcome_at", RoutingAttemptRetentionDays},
		{"channel_probe_result", "probed_at", ChannelProbeRetentionDays},
	} {
		for _, table := range []string{fact.table, fact.table + "_unpartitioned_v24"} {
			if table != fact.table && !tx.Migrator().HasTable(table) {
				continue
			}
			if err := tx.Table(table).Where(fact.timestamp+" < ?", now.UTC().Add(-time.Duration(fact.days)*24*time.Hour)).Delete(map[string]interface{}{}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
