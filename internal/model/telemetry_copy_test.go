package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func telemetryCopySQLiteStores(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	open := func(name string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s-%s?mode=memory&cache=shared", t.Name(), name)), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		return db
	}
	return open("source"), open("target")
}

func telemetryCopyProof(t *testing.T, db *gorm.DB) MigrationProof {
	t.Helper()
	proof, err := BuildMigrationProof(context.Background(), db, "telemetry")
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func requireTelemetryCopyProof(t *testing.T, before, after MigrationProof) {
	t.Helper()
	if err := CompareMigrationProofs(before, after); err != nil {
		t.Fatal(err)
	}
}

func telemetryCopyPreservesRequestTimestamps(t *testing.T, source, target *gorm.DB) {
	t.Helper()
	for _, db := range []*gorm.DB{source, target} {
		if err := db.AutoMigrate(&Log{}, &TelemetryEventReceipt{}, &TelemetryFinancialReceipt{}); err != nil {
			t.Fatal(err)
		}
		if db.Dialector.Name() == "postgres" {
			migrationReviewSchema(t, db)
			if err := migrateTelemetryPartitions(db); err != nil {
				t.Fatal(err)
			}
		}
	}
	at := time.Date(2026, time.June, 9, 10, 11, 12, 345678000, time.UTC)
	for i, requestTS := range []int64{0, at.UnixMilli()} {
		key := fmt.Sprintf("history-%d", i)
		if err := source.Create(&TelemetryEventReceipt{EventKey: key, RequestTS: requestTS, CreatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
		if err := source.Create(&TelemetryFinancialReceipt{EventID: key, RequestID: key, RequestTS: requestTS, Kind: "settle", PayerID: 7, AmountMicrousd: 9007199254740993, OccurredAt: at}).Error; err != nil {
			t.Fatal(err)
		}
		// A known creation time must not replace an existing zero partition
		// key. In PostgreSQL this row belongs in the undated partition.
		log := Log{Id: i + 1, PublicID: "log_" + key, RequestID: key, RequestTS: requestTS, CreatedAt: at.Unix(), Type: LogTypeConsume, Other: "historical payload"}
		if err := source.Session(&gorm.Session{SkipHooks: true}).Create(&log).Error; err != nil {
			t.Fatal(err)
		}
	}
	original := telemetryCopyProof(t, source)
	for retry := 0; retry < 2; retry++ {
		if err := CopyTelemetryHistory(context.Background(), source, target); err != nil {
			t.Fatal(err)
		}
		requireTelemetryCopyProof(t, original, telemetryCopyProof(t, target))
	}
	for _, table := range []string{"telemetry_event_receipts", "telemetry_financial_receipts"} {
		if err := target.Table(table).Where("request_ts = 0").Update("request_ts", 123456).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := CopyTelemetryHistory(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	if err := CompareMigrationProofs(original, telemetryCopyProof(t, target)); err == nil {
		t.Fatal("normal copy unexpectedly overwrote existing receipt evidence")
	}
	for retry := 0; retry < 2; retry++ {
		if err := CopyTelemetryHistoryForRollback(context.Background(), source, target); err != nil {
			t.Fatal(err)
		}
		requireTelemetryCopyProof(t, original, telemetryCopyProof(t, target))
	}
	requireTelemetryCopyProof(t, original, telemetryCopyProof(t, source))
}

func TestTelemetryCopyPreservesRequestTimestamps(t *testing.T) {
	source, target := telemetryCopySQLiteStores(t)
	telemetryCopyPreservesRequestTimestamps(t, source, target)
}

func TestPostgresTelemetryCopyPreservesRequestTimestamps(t *testing.T) {
	source, target := migrationReviewPostgresStores(t)
	telemetryCopyPreservesRequestTimestamps(t, source, target)
}

func telemetryCopyBackfillsOnlyMissingPartitionTimestamps(t *testing.T, source, target *gorm.DB) {
	t.Helper()
	timeType := "datetime"
	if source.Dialector.Name() == "postgres" {
		timeType = "timestamptz"
	}
	for _, table := range []struct{ name, columns, oldKey, newKey string }{
		{"logs", "id bigint NOT NULL, public_id text NOT NULL, request_id text, created_at bigint, type bigint", "id", "request_ts, public_id"},
		{"routing_attempt_outcome", "request_id text NOT NULL, attempt_index bigint NOT NULL, outcome_at " + timeType + ", decision_id bigint, channel_id bigint", "request_id, attempt_index", "request_ts, request_id, attempt_index"},
		{"channel_probe_result", "id bigint NOT NULL, probed_at " + timeType, "id", "request_ts, id"},
	} {
		if err := source.Exec("CREATE TABLE " + table.name + " (" + table.columns + ", PRIMARY KEY (" + table.oldKey + "))").Error; err != nil {
			t.Fatal(err)
		}
		key := table.oldKey
		if target.Dialector.Name() == "postgres" {
			key = table.newKey
		} else if table.name == "routing_attempt_outcome" {
			// SQLite has no partitions; use a single-column fixture identity so
			// its ColumnTypes metadata can identify the rollback conflict key.
			key = "request_id"
		}
		ddl := "CREATE TABLE " + table.name + " (" + table.columns + ", request_ts bigint NOT NULL, PRIMARY KEY (" + key + "))"
		if target.Dialector.Name() == "postgres" {
			ddl += " PARTITION BY RANGE (request_ts)"
		}
		if err := target.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
		if target.Dialector.Name() == "postgres" {
			if err := target.Exec("CREATE TABLE " + table.name + "_undated PARTITION OF " + table.name + " DEFAULT").Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	at := time.Date(2026, time.June, 9, 10, 11, 12, 345000000, time.UTC)
	for i, stamp := range []interface{}{at, nil} {
		key := fmt.Sprintf("legacy-%d", i)
		var seconds interface{}
		if stamp != nil {
			seconds = at.Unix()
		}
		if err := source.Exec("INSERT INTO logs (id, public_id, request_id, created_at, type) VALUES (?, ?, ?, ?, 2)", i+1, "log_"+key, key, seconds).Error; err != nil {
			t.Fatal(err)
		}
		if err := source.Exec("INSERT INTO routing_attempt_outcome (request_id, attempt_index, outcome_at, decision_id, channel_id) VALUES (?, 1, ?, 7, 9)", key, stamp).Error; err != nil {
			t.Fatal(err)
		}
		if err := source.Exec("INSERT INTO channel_probe_result (id, probed_at) VALUES (?, ?)", i+1, stamp).Error; err != nil {
			t.Fatal(err)
		}
	}
	original := telemetryCopyProof(t, source)
	var copied MigrationProof
	for retry := 0; retry < 2; retry++ {
		if err := CopyTelemetryHistory(context.Background(), source, target); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"logs", "routing_attempt_outcome", "channel_probe_result"} {
			var timestamps []int64
			if err := target.Table(table).Order("request_ts").Pluck("request_ts", &timestamps).Error; err != nil {
				t.Fatal(err)
			}
			want := at.UnixMilli()
			if table == "logs" {
				want = at.Unix() * 1000
			}
			if len(timestamps) != 2 || timestamps[0] != 0 || timestamps[1] != want {
				t.Fatalf("%s copied historical timestamps: got %v, want [0 %d]", table, timestamps, want)
			}
		}
		if retry == 0 {
			copied = telemetryCopyProof(t, target)
		} else {
			requireTelemetryCopyProof(t, copied, telemetryCopyProof(t, target))
		}
	}
	if err := target.Table("logs").Where("id = 1").Update("type", 99).Error; err != nil {
		t.Fatal(err)
	}
	for retry := 0; retry < 2; retry++ {
		if err := CopyTelemetryHistoryForRollback(context.Background(), source, target); err != nil {
			t.Fatal(err)
		}
		requireTelemetryCopyProof(t, copied, telemetryCopyProof(t, target))
	}
	requireTelemetryCopyProof(t, original, telemetryCopyProof(t, source))
}

func TestTelemetryCopyBackfillsOnlyMissingPartitionTimestamps(t *testing.T) {
	source, target := telemetryCopySQLiteStores(t)
	telemetryCopyBackfillsOnlyMissingPartitionTimestamps(t, source, target)
}

func TestPostgresTelemetryCopyBackfillsOnlyMissingPartitionTimestamps(t *testing.T) {
	source, target := migrationReviewPostgresStores(t)
	telemetryCopyBackfillsOnlyMissingPartitionTimestamps(t, source, target)
}
