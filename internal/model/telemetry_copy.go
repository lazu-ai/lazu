package model

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CopyTelemetryHistory copies the source's physical telemetry tables without a
// cross-database transaction. Each target batch is atomic and conflict-safe;
// failed runs may be resumed while the original tables remain untouched.
func CopyTelemetryHistory(ctx context.Context, source, target *gorm.DB) error {
	return copyTelemetryHistory(ctx, source, target, false)
}

func CopyTelemetryHistoryForRollback(ctx context.Context, source, target *gorm.DB) error {
	return copyTelemetryHistory(ctx, source, target, true)
}

func copyTelemetryHistory(ctx context.Context, source, target *gorm.DB, overwrite bool) error {
	if source == nil || target == nil {
		return errors.New("both source and target stores are required")
	}
	if source == target {
		return nil
	}
	for _, table := range TelemetryTables {
		origin, destination := table, table
		if table == "routing_decision" || table == "routing_outcome" {
			// PostgreSQL compatibility views overlay logs onto physical history.
			// Copy the original rows as well as logs, including superseded facts
			// hidden by the view. GORM's HasTable does not include these views.
			if source.Dialector.Name() == "postgres" && source.Migrator().HasTable(table+"_legacy") {
				origin = table + "_legacy"
			}
			if target.Dialector.Name() == "postgres" && target.Migrator().HasTable(table+"_legacy") {
				destination = table + "_legacy"
			}
		}
		if !source.Migrator().HasTable(origin) || !target.Migrator().HasTable(destination) {
			continue
		}
		sourceColumns, err := source.Migrator().ColumnTypes(origin)
		if err != nil {
			return err
		}
		targetColumns, err := target.Migrator().ColumnTypes(destination)
		if err != nil {
			return err
		}
		permitted := make(map[string]struct{}, len(targetColumns))
		primary := []clause.Column{}
		primaryNames := map[string]bool{}
		for _, column := range targetColumns {
			permitted[column.Name()] = struct{}{}
			if yes, ok := column.PrimaryKey(); ok && yes {
				primary = append(primary, clause.Column{Name: column.Name()})
				primaryNames[column.Name()] = true
			}
		}
		if target.Dialector.Name() == "postgres" {
			// pgx/GORM's ColumnTypes metadata does not reliably report primary
			// keys, especially after restoring partitioned tables.
			var names []string
			if err := target.WithContext(ctx).Raw("SELECT a.attname FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey) WHERE i.indrelid = to_regclass(?) AND i.indisprimary ORDER BY a.attnum", destination).Scan(&names).Error; err != nil {
				return err
			}
			primary = nil
			primaryNames = map[string]bool{}
			for _, name := range names {
				primary = append(primary, clause.Column{Name: name})
				primaryNames[name] = true
			}
		}
		columns := []string{}
		sourceHasRequestTS := false
		for _, column := range sourceColumns {
			if column.Name() == "request_ts" {
				sourceHasRequestTS = true
			}
			if _, ok := permitted[column.Name()]; ok {
				columns = append(columns, column.Name())
			}
		}
		historicalTimeColumn := ""
		switch table {
		case "logs":
			historicalTimeColumn = "created_at"
		case "routing_attempt_outcome":
			historicalTimeColumn = "outcome_at"
		case "channel_probe_result":
			historicalTimeColumn = "probed_at"
		}
		_, targetHasRequestTS := permitted["request_ts"]
		fillRequestTS := historicalTimeColumn != "" && targetHasRequestTS && !sourceHasRequestTS
		if len(columns) == 0 {
			continue
		}
		sort.Strings(columns)
		quoted := make([]string, 0, len(columns))
		for _, column := range columns {
			quoted = append(quoted, `"`+column+`"`)
		}
		rows, err := source.WithContext(ctx).Table(origin).Select(strings.Join(quoted, ",")).Rows()
		if err != nil {
			return err
		}
		batch := make([]map[string]interface{}, 0, 500)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			err := target.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				conflict := clause.OnConflict{DoNothing: true}
				if overwrite && len(primary) > 0 {
					updates := []string{}
					for _, name := range columns {
						if !primaryNames[name] {
							updates = append(updates, name)
						}
					}
					conflict = clause.OnConflict{Columns: primary, DoUpdates: clause.AssignmentColumns(updates)}
				}
				writer := tx.Table(destination).Clauses(conflict)
				if tx.Dialector.Name() == "postgres" {
					// Map inserts have no model schema for GORM to infer RETURNING.
					// Otherwise it tries LastInsertId, unsupported by PostgreSQL.
					writer = writer.Clauses(clause.Returning{})
				}
				// The enclosing reader already limits this batch to 500. GORM's
				// CreateInBatches makes map slices unaddressable for RETURNING.
				return writer.Create(&batch).Error
			})
			batch = batch[:0]
			return err
		}
		for rows.Next() {
			row := make(map[string]interface{})
			if err := source.ScanRows(rows, &row); err != nil {
				rows.Close()
				return err
			}
			// Only old partition schemas without this column need a new key.
			// Existing values (including zero) are historical facts, and unknown
			// source times stay zero rather than becoming the migration's time.
			if fillRequestTS {
				if table == "logs" {
					row["request_ts"] = telemetryMapInt64(row[historicalTimeColumn]) * 1000
				} else {
					row["request_ts"] = telemetryMapUnixMilli(row[historicalTimeColumn])
				}
			}
			batch = append(batch, row)
			if len(batch) == 500 {
				if err := flush(); err != nil {
					rows.Close()
					return fmt.Errorf("copy %s: %w", table, err)
				}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if err := flush(); err != nil {
			return fmt.Errorf("copy %s: %w", table, err)
		}
	}
	return nil
}

func telemetryMapUnixMilli(value interface{}) int64 {
	switch v := value.(type) {
	case time.Time:
		if !v.IsZero() {
			return v.UnixMilli()
		}
	case *interface{}:
		if v != nil {
			return telemetryMapUnixMilli(*v)
		}
	}
	return 0
}

func telemetryMapInt64(value interface{}) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	case *interface{}:
		if v != nil {
			return telemetryMapInt64(*v)
		}
	}
	return 0
}

func backfillIncrementalTelemetry(db *gorm.DB) error {
	return runOneTimeMigration(db, "telemetry_rollup_v24", func() error {
		if err := db.Exec("INSERT INTO model_request_counters (model_name,total_requests,updated_at) SELECT model_name,COUNT(*),? FROM logs WHERE type = ? AND model_name <> '' GROUP BY model_name ON CONFLICT (model_name) DO NOTHING", time.Now().UTC(), LogTypeConsume).Error; err != nil {
			return err
		}
		// Stable receipts cover pre-cutover logs so replay after migration cannot
		// increment their already-copied session or request counters a second time.
		rows, err := db.Model(&Log{}).Where("type IN ?", []int{LogTypeConsume, LogTypeError}).Order("id").Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		batch := []Log{}
		flush := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				for i := range batch {
					log := &batch[i]
					key := "log:" + log.PublicID
					if log.RequestID != "" {
						key = "relay:" + log.RequestID
					}
					receipt := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&TelemetryEventReceipt{EventKey: key, RequestTS: log.RequestTS, CreatedAt: time.Now().UTC()})
					if receipt.Error != nil {
						return receipt.Error
					}
					if receipt.RowsAffected > 0 {
						if err := recordDailyUsageTx(tx, log); err != nil {
							return err
						}
					}

				}
				return nil
			})
		}
		for rows.Next() {
			var log Log
			if err := db.ScanRows(rows, &log); err != nil {
				return err
			}
			batch = append(batch, log)
			if len(batch) == 500 {
				if err := flush(); err != nil {
					return err
				}
				batch = batch[:0]
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(batch) > 0 {
			if err := flush(); err != nil {
				return err
			}
		}
		return nil
	})
}
