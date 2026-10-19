package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// PrepareLegacyTelemetryRollback restores the old writable routing tables.
// Run with all writers stopped and retain the pre-migration database backup.
// The caller then copies all telemetry back to the legacy single core store.
func PrepareLegacyTelemetryRollback(ctx context.Context, db *gorm.DB) error {
	if db == nil || db.Dialector.Name() != "postgres" {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The views hide history by a precomputed flag; refresh it so the
		// materialized tables hold exactly one fact per request even if a
		// late log fact overlapped a legacy row after the last migration.
		if err := markSupersededRoutingHistory(tx); err != nil {
			return err
		}
		for _, model := range []interface{}{&RoutingDecision{}, &RoutingOutcome{}} {
			parsed, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
			if err != nil {
				return err
			}
			table, legacy := parsed.Table, parsed.Table+"_legacy"
			var isView bool
			if err := tx.Raw("SELECT EXISTS(SELECT 1 FROM pg_class WHERE oid = to_regclass(?) AND relkind = 'v')", table).Scan(&isView).Error; err != nil {
				return err
			}
			if !isView {
				continue // repeat after a successful rollback is safe
			}
			columns := make([]string, 0, len(parsed.DBNames))
			for _, column := range parsed.DBNames {
				columns = append(columns, column)
			}
			primary := []string{}
			for _, field := range parsed.PrimaryFields {
				primary = append(primary, field.DBName)
			}
			before, err := routingRollbackProof(tx, table, columns, primary)
			if err != nil {
				return err
			}
			// Materialize before changing the legacy table: the view can hide
			// superseded legacy rows and overlay newer quality/cost fields.
			// Keep those original rows in a recoverable archive, then restore
			// the exact reader-visible snapshot into the writable old table.
			snapshot := table + "_rollback_snapshot"
			if err := tx.Exec("CREATE TEMP TABLE " + snapshot + " ON COMMIT DROP AS SELECT " + strings.Join(columns, ",") + " FROM " + table).Error; err != nil {
				return err
			}
			if err := tx.Exec("CREATE TABLE IF NOT EXISTS " + table + "_rollback_archive_v24 AS TABLE " + legacy).Error; err != nil {
				return err
			}
			if err := tx.Exec("TRUNCATE TABLE " + legacy).Error; err != nil {
				return err
			}
			query := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s", legacy, strings.Join(columns, ","), strings.Join(columns, ","), snapshot)
			if err := tx.Exec(query).Error; err != nil {
				return err
			}
			after, err := routingRollbackProof(tx, legacy, columns, primary)
			if err != nil {
				return err
			}
			if before != after {
				return fmt.Errorf("rollback %s row count or checksum differs: view=%+v physical=%+v", table, before, after)
			}
			if err := tx.Exec("DROP VIEW " + table).Error; err != nil {
				return err
			}
			if err := tx.Exec("ALTER TABLE " + legacy + " RENAME TO " + table).Error; err != nil {
				return err
			}
			if table == "routing_decision" {
				if err := tx.Exec("SELECT setval(pg_get_serial_sequence('routing_decision','id'), GREATEST(COALESCE(MAX(id),1),1), COUNT(*) > 0) FROM routing_decision").Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func routingRollbackProof(tx *gorm.DB, table string, columns, primary []string) (MigrationTableProof, error) {
	proof := MigrationTableProof{}
	rows, err := tx.Table(table).Select(strings.Join(columns, ",")).Order(strings.Join(primary, ",")).Rows()
	if err != nil {
		return proof, err
	}
	defer rows.Close()
	hash := sha256.New()
	for rows.Next() {
		values := make([]interface{}, len(columns))
		pointers := make([]interface{}, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return proof, err
		}
		for i, value := range values {
			switch v := value.(type) {
			case []byte:
				values[i] = string(v)
			case time.Time:
				values[i] = v.UTC().Format(time.RFC3339Nano)
			}
			if value != nil && strings.HasSuffix(columns[i], "_usd") {
				// Views expose float8 while the legacy table stores decimal(12,8).
				// Compare at that physical precision rather than driver types.
				decimal := fmt.Sprint(values[i])
				if v, ok := value.(float64); ok {
					decimal = fmt.Sprintf("%.8f", v)
				}
				if number, ok := new(big.Rat).SetString(decimal); ok {
					values[i] = number.RatString()
				}
			}
		}
		raw, err := json.Marshal(values)
		if err != nil {
			return proof, err
		}
		hash.Write(raw)
		hash.Write([]byte{'\n'})
		proof.Rows++
	}
	proof.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return proof, rows.Err()
}

func ResetMigrationSequences(ctx context.Context, db *gorm.DB) error {
	if db == nil || db.Dialector.Name() != "postgres" {
		return nil
	}
	// Repair previously migrated parents before reading ownership metadata.
	// LIKE INCLUDING DEFAULTS preserves nextval but leaves ownership on the
	// retained original, so pg_get_serial_sequence(parent, column) was NULL.
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, spec := range telemetryPartitionSpecs {
			if err := repairTelemetryPartitionSequencesTx(tx, spec.table); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	var columns []struct{ TableName, ColumnName string }
	if err := db.WithContext(ctx).Raw("SELECT table_name,column_name FROM information_schema.columns WHERE table_schema = current_schema() AND column_default LIKE 'nextval(%'").Scan(&columns).Error; err != nil {
		return err
	}
	for _, column := range columns {
		// PostgreSQL catalog identifiers are quoted, never used as SQL values.
		table := `"` + strings.ReplaceAll(column.TableName, `"`, `""`) + `"`
		field := `"` + strings.ReplaceAll(column.ColumnName, `"`, `""`) + `"`
		query := "SELECT setval(pg_get_serial_sequence(?,?), GREATEST(COALESCE(MAX(" + field + "),1),1), COUNT(*) > 0) FROM " + table
		if err := db.WithContext(ctx).Exec(query, column.TableName, column.ColumnName).Error; err != nil {
			return err
		}
	}
	return nil
}

type telemetryPartitionSequence struct{ ColumnName, SequenceName string }

func telemetryPartitionSequences(tx *gorm.DB, table string) ([]telemetryPartitionSequence, error) {
	var sequences []telemetryPartitionSequence
	// Resolve the sequence used by the default, independent of its owner. Only
	// parent relations qualify: child and archive defaults may share it.
	err := tx.Raw("SELECT a.attname AS column_name,s.oid::regclass::text AS sequence_name FROM pg_attrdef ad JOIN pg_class parent ON parent.oid=ad.adrelid AND parent.relkind='p' JOIN pg_attribute a ON a.attrelid=ad.adrelid AND a.attnum=ad.adnum JOIN pg_depend d ON d.classid='pg_attrdef'::regclass AND d.objid=ad.oid AND d.refclassid='pg_class'::regclass JOIN pg_class s ON s.oid=d.refobjid AND s.relkind='S' WHERE ad.adrelid=to_regclass(?)", table).Scan(&sequences).Error
	return sequences, err
}

func repairTelemetryPartitionSequencesTx(tx *gorm.DB, table string) error {
	sequences, err := telemetryPartitionSequences(tx, table)
	if err != nil {
		return err
	}
	for _, sequence := range sequences {
		// SequenceName is PostgreSQL's quoted regclass representation. The
		// fixed table allowlist and quoted catalog column are SQL identifiers.
		column := `"` + strings.ReplaceAll(sequence.ColumnName, `"`, `""`) + `"`
		if err := tx.Exec("ALTER SEQUENCE " + sequence.SequenceName + " OWNED BY " + table + "." + column).Error; err != nil {
			return err
		}
	}
	return nil
}

func resetTelemetryPartitionSequencesTx(tx *gorm.DB, table string) error {
	sequences, err := telemetryPartitionSequences(tx, table)
	if err != nil {
		return err
	}
	for _, sequence := range sequences {
		column := `"` + strings.ReplaceAll(sequence.ColumnName, `"`, `""`) + `"`
		if err := tx.Exec("SELECT setval(?::regclass,GREATEST(COALESCE(MAX("+column+"),1),1),COUNT(*) > 0) FROM "+table, sequence.SequenceName).Error; err != nil {
			return err
		}
	}
	return nil
}

const LegacySchemaRollbackDefault = 23

// Versions 24 and 25 added price-following metadata and attribution/crawler
// data. These additive columns remain intact when preparing the legacy store.
func ValidateLegacySchemaRollbackVersion(version int) error {
	switch version {
	case 23, 24, 25:
		return nil
	default:
		return fmt.Errorf("unsupported legacy rollback schema %d: supported targets are 23, 24 and 25", version)
	}
}

// PrepareLegacyTelemetryWrites runs on the single Core store after copying the
// current telemetry back. Old writers omit request_ts and upsert attempts using
// the old two-column identity. Do not drop conflicting retained history to make
// these indexes succeed: such a conflict must block the rollback.
func PrepareLegacyTelemetryWrites(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("legacy rollback database is unavailable")
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, table := range []string{"logs", "routing_attempt_outcome", "channel_probe_result"} {
			var partitioned bool
			if err := tx.Raw("SELECT EXISTS(SELECT 1 FROM pg_class WHERE oid=to_regclass(?) AND relkind='p')", table).Scan(&partitioned).Error; err != nil {
				return err
			}
			if partitioned {
				return fmt.Errorf("legacy rollback requires unpartitioned Core table %s; copy telemetry into a separate Core store first", table)
			}
			if err := tx.Exec("ALTER TABLE " + table + " ALTER COLUMN request_ts SET DEFAULT 0").Error; err != nil {
				return err
			}
		}
		for _, index := range []struct{ name, table, columns string }{
			{"idx_legacy_rollback_log_public", "logs", "public_id"},
			{"idx_legacy_rollback_log_id", "logs", "id"},
			{"idx_legacy_rollback_attempt", "routing_attempt_outcome", "request_id,attempt_index"},
			{"idx_legacy_rollback_probe_id", "channel_probe_result", "id"},
		} {
			if err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS " + index.name + " ON " + index.table + " (" + index.columns + ")").Error; err != nil {
				return fmt.Errorf("prepare legacy writer identity on %s: %w", index.table, err)
			}
		}
		return nil
	})
}

// MarkLegacySchemaRollback retains the original schema-23 default for callers.
func MarkLegacySchemaRollback(ctx context.Context, db *gorm.DB) error {
	return MarkLegacySchemaRollbackToVersion(ctx, db, LegacySchemaRollbackDefault)
}

// ValidateLegacySchemaRollbackSource runs before any financial materialization.
// Recheck it in the final marker transaction so a failed or changed source is
// never advertised as compatible with a newer/unknown previous image.
func ValidateLegacySchemaRollbackSource(ctx context.Context, db *gorm.DB, version int) error {
	if err := ValidateLegacySchemaRollbackVersion(version); err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("legacy rollback database is unavailable")
	}
	var records []SchemaVersion
	if err := db.WithContext(ctx).Where("scope IN ?", []string{schemaVersionPrimary, schemaVersionLog}).Find(&records).Error; err != nil {
		return err
	}
	primary := false
	for _, record := range records {
		primary = primary || record.Scope == schemaVersionPrimary
		if record.Version < version || record.Version > SchemaVersionCurrent {
			return fmt.Errorf("cannot mark %s schema %d as legacy target %d", record.Scope, record.Version, version)
		}
	}
	if !primary {
		return fmt.Errorf("legacy rollback requires an initialized Core schema")
	}
	return nil
}

// MarkLegacySchemaRollbackToVersion is the final step after financial and
// telemetry materialization. It never removes newer columns or financial facts.
func MarkLegacySchemaRollbackToVersion(ctx context.Context, db *gorm.DB, version int) error {
	if err := ValidateLegacySchemaRollbackVersion(version); err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("legacy rollback database is unavailable")
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ValidateLegacySchemaRollbackSource(ctx, tx, version); err != nil {
			return err
		}
		// An old writer can create history outside the opening boundary. Never
		// reactivate that boundary by merely restoring the newer schema marker.
		if tx.Migrator().HasTable(&LedgerContinuityState{}) {
			if err := tx.Model(&LedgerContinuityState{}).Where("opening_id > 0").Update("status", "legacy_rollback").Error; err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		for _, scope := range []string{schemaVersionPrimary, schemaVersionLog} {
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope"}}, DoUpdates: clause.AssignmentColumns([]string{"version", "applied_at"})}).Create(&SchemaVersion{Scope: scope, Version: version, AppliedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		invalidateSchemaCatalogue(db)
	}
	return err
}
