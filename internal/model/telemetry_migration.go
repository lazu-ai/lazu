package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

func runOneTimeMigration(db *gorm.DB, name string, run func() error) error {
	var version SchemaVersion
	err := db.Where("scope = ?", name).First(&version).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err := run(); err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&SchemaVersion{Scope: name, Version: 1, AppliedAt: time.Now().UTC()}).Error
}

// TelemetryChannel is a metadata projection, never a control-plane authority.
// Copying it avoids joining telemetry to the core database. The grid and
// routing always read the authoritative channel/ability registry from core.
type TelemetryChannel struct {
	ID         int `gorm:"primaryKey"`
	Name       string
	Status     int
	Type       int
	SourceType string
	Group      string
}

func (TelemetryChannel) TableName() string { return "telemetry_channels" }
func RefreshTelemetryChannelProjection(ctx context.Context) error {
	if CoreDB() == nil || TelemetryDB() == nil {
		return nil
	}
	var rows []TelemetryChannel
	if err := CoreDB().WithContext(ctx).Unscoped().Table("channels").Select("id, name, status, type, source_type, " + commonGroupCol).Find(&rows).Error; err != nil {
		return err
	}
	return TelemetryDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := refreshTelemetryAbilityProjectionTx(ctx, tx); err != nil {
			return err
		}
		if len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, UpdateAll: true}).CreateInBatches(&rows, 500).Error; err != nil {
				return err
			}
		}
		ids := make([]int, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return tx.Where("id NOT IN ?", append(ids, 0)).Delete(&TelemetryChannel{}).Error
	})
}

// routingFactsAreViews is fixed by dialect and schema version, not checked on
// every request. SQLite retains compatibility tables for local installations.
func routingFactsAreViews(db *gorm.DB) bool { return db != nil && db.Dialector.Name() == "postgres" }

func migrateTelemetryPartitions(db *gorm.DB) error {
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return runOneTimeMigration(tx, "telemetry_partition_v24", func() error {
			for _, spec := range telemetryPartitionSpecs {
				// Immutable fallback for historical/non-ULID ids; new events use the ULID.
				if err := tx.Exec(fmt.Sprintf("UPDATE %s SET request_ts = COALESCE(%s,0) WHERE request_ts IS NULL OR request_ts = 0", spec.table, spec.historicalTime)).Error; err != nil {
					return err
				}
				if err := partitionTelemetryTableTx(tx, spec); err != nil {
					return err
				}
			}
			for _, model := range []interface{}{&RoutingDecision{}, &RoutingOutcome{}} {
				parsed, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
				if err != nil {
					return err
				}
				table := parsed.Table
				if err := tx.Exec("ALTER TABLE " + table + " RENAME TO " + table + "_legacy").Error; err != nil {
					return err
				}
				if err := createRoutingCompatibilityViewTx(tx, parsed, model); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

type telemetryPartitionSpec struct{ table, historicalTime, uniqueColumns string }

var telemetryPartitionSpecs = []telemetryPartitionSpec{
	{"logs", "created_at * 1000", "request_ts, public_id"},
	{"routing_attempt_outcome", "(EXTRACT(EPOCH FROM outcome_at) * 1000)::bigint", "request_ts, request_id, attempt_index"},
	{"channel_probe_result", "(EXTRACT(EPOCH FROM probed_at) * 1000)::bigint", "request_ts, id"},
}

func partitionTelemetryTableTx(tx *gorm.DB, spec telemetryPartitionSpec) error {
	table := spec.table
	old := table + "_unpartitioned_v24"
	if err := tx.Exec("ALTER TABLE " + table + " RENAME TO " + old).Error; err != nil {
		return err
	}
	if err := tx.Exec("CREATE TABLE " + table + " (LIKE " + old + " INCLUDING DEFAULTS INCLUDING GENERATED) PARTITION BY RANGE (request_ts)").Error; err != nil {
		return err
	}
	if err := repairTelemetryPartitionSequencesTx(tx, table); err != nil {
		return err
	}
	if err := tx.Exec("CREATE UNIQUE INDEX " + table + "_partition_identity ON " + table + " (" + spec.uniqueColumns + ")").Error; err != nil {
		return err
	}
	if err := tx.Exec("CREATE INDEX " + table + "_partition_ts ON " + table + " (request_ts)").Error; err != nil {
		return err
	}
	var bounds struct {
		Min int64
		Max int64
	}
	if err := tx.Table(old).Select("COALESCE(MIN(request_ts),0) AS min,COALESCE(MAX(request_ts),0) AS max").Scan(&bounds).Error; err != nil {
		return err
	}
	start := time.Now().UTC().AddDate(0, -4, 0)
	if bounds.Min > 0 {
		start = time.UnixMilli(bounds.Min).UTC()
	}
	end := time.Now().UTC().AddDate(0, 2, 0)
	if bounds.Max > time.Now().UnixMilli() {
		end = time.UnixMilli(bounds.Max).UTC().AddDate(0, 1, 0)
	}
	for at := monthStart(start); !at.After(monthStart(end)); at = at.AddDate(0, 1, 0) {
		if err := ensureTelemetryPartitionTx(tx, table, at); err != nil {
			return err
		}
	}
	if err := tx.Exec("CREATE TABLE " + table + "_undated PARTITION OF " + table + " DEFAULT").Error; err != nil {
		return err
	}
	for _, column := range []string{"id", "request_id", "created_at", "user_id", "token_id", "channel_id", "outcome_at", "probed_at"} {
		if tx.Migrator().HasColumn(table, column) {
			if err := tx.Exec("CREATE INDEX " + table + "_v24_" + column + " ON " + table + " (" + column + ")").Error; err != nil {
				return err
			}
		}
	}
	if err := tx.Exec("INSERT INTO " + table + " SELECT * FROM " + old).Error; err != nil {
		return err
	}
	if err := resetTelemetryPartitionSequencesTx(tx, table); err != nil {
		return err
	}
	// Keep the original table until the backup-environment verification. The CLI
	// compares row counts and keys; operators remove it only after acceptance.
	return nil
}
func monthStart(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
}
func ensureTelemetryPartitionTx(tx *gorm.DB, table string, at time.Time) error {
	at = monthStart(at)
	name := table + "_" + at.Format("200601")
	return tx.Exec(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM (%d) TO (%d)", name, table, at.UnixMilli(), at.AddDate(0, 1, 0).UnixMilli())).Error
}

// logRetentionDays affects request log details only. Routing and probe facts
// retain their own pre-migration policies, even though finals share logs.
func MaintainTelemetryPartitions(ctx context.Context, now time.Time, logRetentionDays int) error {
	db := TelemetryDB()
	if db == nil || db.Dialector.Name() != "postgres" {
		return nil
	}
	// Retention sweeps whole partitions once a day; their duration is not a
	// regression to page anyone about.
	return db.WithContext(WithSlowQueryExpected(ctx)).Transaction(func(tx *gorm.DB) error {
		if err := expireTelemetryFactsTx(tx, now, logRetentionDays); err != nil {
			return err
		}
		for _, spec := range telemetryPartitionSpecs {
			for offset := 0; offset <= 2; offset++ {
				if err := ensureTelemetryPartitionTx(tx, spec.table, monthStart(now).AddDate(0, offset, 0)); err != nil {
					return err
				}
			}
			var names []string
			if err := tx.Raw("SELECT child.relname FROM pg_inherits JOIN pg_class parent ON parent.oid = inhparent JOIN pg_class child ON child.oid = inhrelid JOIN pg_namespace ns ON ns.oid = parent.relnamespace WHERE parent.relname = ? AND ns.nspname = current_schema()", spec.table).Scan(&names).Error; err != nil {
				return err
			}
			retentionDays := RoutingOutcomeRetentionDays
			switch spec.table {
			case "logs":
				days := effectiveTelemetryLogRetentionDays(logRetentionDays)
				if days == 0 {
					continue // Disabled metadata retention must never drop logs.
				}
				retentionDays = days
			case "routing_attempt_outcome":
				retentionDays = RoutingAttemptRetentionDays
			case "channel_probe_result":
				retentionDays = ChannelProbeRetentionDays
			}
			cutoff := now.UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
			for _, name := range names {
				suffix := strings.TrimPrefix(name, spec.table+"_")
				start, err := time.Parse("200601", suffix)
				if err != nil {
					continue
				}
				if !start.AddDate(0, 1, 0).After(cutoff) {
					// request_ts identifies the request's admission month. A long
					// media outcome may finish much later, so the partition's age
					// cannot by itself prove all retained facts have expired.
					query := "SELECT EXISTS (SELECT 1 FROM " + name
					if spec.table == "logs" {
						query += " WHERE type <> 0 OR NULLIF(routing_decision_json,'') IS NOT NULL OR NULLIF(routing_outcome_json,'') IS NOT NULL OR rd_id IS NOT NULL OR ro_request_id IS NOT NULL"
					}
					var retained bool
					if err := tx.Raw(query + " LIMIT 1)").Scan(&retained).Error; err != nil {
						return err
					}
					if retained {
						continue
					}
					if err := tx.Exec("DROP TABLE " + name).Error; err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

// Projection views preserve the existing reader contract without storing the
// decision's unused large experiment columns or writing a second outcome row.
// A historical row is visible until retention removes its legacy table data.
func createRoutingCompatibilityViewTx(tx *gorm.DB, parsed *schema.Schema, model interface{}) error {
	layout := routingOutcomeLayout()
	if _, ok := model.(*RoutingDecision); ok {
		layout = routingDecisionLayout()
	}
	if err := tx.Exec("ALTER TABLE " + parsed.Table + "_legacy ADD COLUMN IF NOT EXISTS superseded boolean NOT NULL DEFAULT false").Error; err != nil {
		return err
	}
	return tx.Exec(routingFactViewSQL(layout, parsed)).Error
}

// markSupersededRoutingHistory flags pre-cutover routing facts whose request
// also has a fact on its log row, which the compatibility views then hide.
// The legacy tables are frozen at the cutover, so this only has to run once
// the log rows are projected.
func markSupersededRoutingHistory(db *gorm.DB) error {
	for _, fact := range []struct{ table, marker string }{
		{"routing_outcome_legacy", routingOutcomeLayout().marker},
		{"routing_decision_legacy", routingDecisionLayout().marker},
	} {
		var exists bool
		if err := db.Raw("SELECT to_regclass(?) IS NOT NULL", fact.table).Scan(&exists).Error; err != nil {
			return err
		}
		if !exists {
			continue // already rolled back to writable routing tables
		}
		if err := db.Exec("ALTER TABLE " + fact.table + " ADD COLUMN IF NOT EXISTS superseded boolean NOT NULL DEFAULT false").Error; err != nil {
			return err
		}
		if err := db.Exec("UPDATE " + fact.table + " old SET superseded = true WHERE NOT old.superseded AND EXISTS (SELECT 1 FROM logs newer WHERE newer.request_id = old.request_id AND newer." + fact.marker + " IS NOT NULL)").Error; err != nil {
			return fmt.Errorf("routing columns: mark superseded %s: %w", fact.table, err)
		}
	}
	return nil
}

// routingFactIndexes are the typed routing columns readers select by: a fact
// window by time, a decision by id or request, and a request's outcome.
var routingFactIndexes = []struct{ name, column string }{
	{"logs_ro_outcome_at", "ro_outcome_at"},
	{"logs_ro_user_outcome_at", "ro_user_id, ro_outcome_at"},
	{"logs_ro_request_id", "ro_request_id"},
	{"logs_rd_id", "rd_id"},
	{"logs_rd_request_id", "rd_request_id"},
	{"logs_rd_decision_at", "rd_decision_at"},
}

// retiredRoutingSnapshotIndexes were built by hand (the former
// ops/sql/telemetry-routing-snapshot-indexes.sql) over the JSON copies. They
// decoded every inserted document and no reader uses them any more.
var retiredRoutingSnapshotIndexes = []string{
	"lazu_logs_snapshot_outcome_req_v1",
	"lazu_logs_snapshot_decision_id_v1",
	"lazu_logs_snapshot_outcome_physical_req_v1",
	"lazu_logs_snapshot_decision_physical_req_v1",
}

// routingFactBackfillSpan is one UPDATE's request_ts range: a day of rows.
const routingFactBackfillSpan = int64(24 * time.Hour / time.Millisecond)

// backfillRoutingFactColumns fills the typed routing columns of every log row
// whose JSON copy has not been projected yet.
func backfillRoutingFactColumns(db *gorm.DB) error {
	return backfillRoutingFactColumnsBefore(db, math.MaxInt64)
}

// backfillRoutingFactColumnsBefore projects rows admitted before until (a
// request_ts). Each statement covers one day of request_ts starting at the
// next row still waiting, so sparse or skewed timestamps cost one statement
// per populated day, not one per calendar day. A document a column type
// cannot hold fails here, loudly, instead of in a later reader.
func backfillRoutingFactColumnsBefore(db *gorm.DB, until int64) error {
	for _, fact := range []struct {
		layout routingFactLayout
		json   string
	}{
		{routingOutcomeLayout(), "routing_outcome_json"},
		{routingDecisionLayout(), "routing_decision_json"},
	} {
		update := routingFactBackfillSQL(fact.layout, fact.json)
		pending := "SELECT MIN(request_ts) AS next FROM logs WHERE request_ts >= ? AND request_ts < ? AND " + fact.json + " <> '' AND " + fact.layout.marker + " IS NULL"
		cursor := int64(math.MinInt64)
		for {
			var next struct{ Next *int64 }
			if err := db.Raw(pending, cursor, until).Scan(&next).Error; err != nil {
				return fmt.Errorf("routing columns: find pending %s: %w", fact.json, err)
			}
			if next.Next == nil {
				break
			}
			to := *next.Next + routingFactBackfillSpan
			if to > until || to < *next.Next {
				to = until
			}
			if err := db.Exec(update, *next.Next, to).Error; err != nil {
				return fmt.Errorf("routing columns: backfill %s from %d: %w", fact.json, *next.Next, err)
			}
			cursor = to
		}
	}
	return nil
}

// PrefillRoutingFactColumns is the online half of the schema-30 upgrade. With
// the previous release still serving, it adds the typed routing columns (a
// metadata-only change under a short lock timeout) and projects every row
// admitted before the cutoff. The previous release keeps writing JSON only
// and may still revise recent rows (quality signals, late media verdicts), so
// rows after the cutoff are left to the schema migration, which projects
// whatever is still unprojected while writers are stopped. It records no
// schema version and changes no view or index; running it twice is harmless.
func PrefillRoutingFactColumns(ctx context.Context, db *gorm.DB, before time.Time) (int64, error) {
	if db == nil || db.Dialector.Name() != "postgres" {
		return 0, errors.New("routing column prefill needs the PostgreSQL Telemetry database")
	}
	parsed, err := schema.Parse(&Log{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		return 0, err
	}
	additions := []string{}
	for _, field := range parsed.Fields {
		if !strings.HasPrefix(field.DBName, routingOutcomeColumnPrefix) && !strings.HasPrefix(field.DBName, routingDecisionColumnPrefix) {
			continue
		}
		additions = append(additions, "ADD COLUMN IF NOT EXISTS "+field.DBName+" "+db.Migrator().FullDataTypeOf(field).SQL)
	}
	session := db.WithContext(WithSlowQueryExpected(ctx))
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		lastErr = session.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL lock_timeout = '3s'").Error; err != nil {
				return err
			}
			return tx.Exec("ALTER TABLE logs " + strings.Join(additions, ", ")).Error
		})
		if lastErr == nil || ctx.Err() != nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if lastErr != nil {
		return 0, fmt.Errorf("routing columns: add columns: %w", lastErr)
	}
	if err := backfillRoutingFactColumnsBefore(session, before.UnixMilli()); err != nil {
		return 0, err
	}
	var remaining int64
	err = session.Raw("SELECT count(*) FROM logs WHERE (routing_outcome_json <> '' AND ro_request_id IS NULL) OR (routing_decision_json <> '' AND rd_id IS NULL)").Scan(&remaining).Error
	return remaining, err
}

// migrateRoutingFactColumns moves PostgreSQL routing facts from JSON decoding
// to typed log columns. AutoMigrate has added the columns; this fills them for
// rows written before, indexes them and points the compatibility views at
// them. Rows are filled a day at a time so no statement holds a long range.
func migrateRoutingFactColumns(db *gorm.DB) error {
	if db.Dialector.Name() != "postgres" || !db.Migrator().HasTable("routing_outcome_legacy") {
		return nil
	}
	return runOneTimeMigration(db, "telemetry_routing_columns_v30", func() error {
		if err := backfillRoutingFactColumns(db); err != nil {
			return err
		}
		if err := markSupersededRoutingHistory(db); err != nil {
			return err
		}
		for _, index := range routingFactIndexes {
			if err := db.Exec("CREATE INDEX IF NOT EXISTS " + index.name + " ON logs (" + index.column + ")").Error; err != nil {
				return fmt.Errorf("routing columns: index %s: %w", index.name, err)
			}
		}
		for _, name := range retiredRoutingSnapshotIndexes {
			if err := db.Exec("DROP INDEX IF EXISTS " + name).Error; err != nil {
				return fmt.Errorf("routing columns: drop %s: %w", name, err)
			}
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			for _, model := range []interface{}{&RoutingDecision{}, &RoutingOutcome{}} {
				parsed, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
				if err != nil {
					return err
				}
				if err := tx.Exec("DROP VIEW IF EXISTS " + parsed.Table).Error; err != nil {
					return err
				}
				if err := createRoutingCompatibilityViewTx(tx, parsed, model); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("routing columns: views: %w", err)
		}
		for _, table := range []string{"logs", "routing_outcome_legacy", "routing_decision_legacy"} {
			if err := db.Exec("ANALYZE " + table).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

type TelemetryAbility Ability

func (TelemetryAbility) TableName() string { return "telemetry_abilities" }
func refreshTelemetryAbilityProjectionTx(ctx context.Context, tx *gorm.DB) error {
	var rows []TelemetryAbility
	if err := CoreDB().WithContext(ctx).Table("abilities").Find(&rows).Error; err != nil {
		return err
	}
	if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&TelemetryAbility{}).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.CreateInBatches(&rows, 500).Error
}

func migrateTelemetrySchema(db *gorm.DB) error {
	models := telemetrySchemaModels()
	filtered := make([]interface{}, 0, len(models))
	for _, row := range models {
		switch row.(type) {
		case *RoutingDecision:
			if db.Dialector.Name() == "postgres" && db.Migrator().HasTable("routing_decision_legacy") {
				continue
			}
		case *RoutingOutcome:
			if db.Dialector.Name() == "postgres" && db.Migrator().HasTable("routing_outcome_legacy") {
				continue
			}
		}
		filtered = append(filtered, row)
	}
	return db.AutoMigrate(filtered...)
}

// migrateRouteHealthRollup retires the materialized health snapshots and
// fills the hourly route health rows for the retention window, so the first
// page view after the upgrade reads hours rather than raw requests.
func migrateRouteHealthRollup(db *gorm.DB) error {
	return runOneTimeMigration(db, "telemetry_route_health_v31", func() error {
		if err := db.Exec("DROP TABLE IF EXISTS channel_health_materialized_snapshot").Error; err != nil {
			return fmt.Errorf("route health: drop snapshots: %w", err)
		}
		if err := RefreshRouteHealthRollup(WithSlowQueryExpected(context.Background()), time.Now().UTC()); err != nil {
			return fmt.Errorf("route health: backfill: %w", err)
		}
		return nil
	})
}
