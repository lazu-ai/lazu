package model

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func TestPostgresOfflineMigrationAllowsLongPartitionTransaction(t *testing.T) {
	runtimeStore, offlineStore := migrationReviewPostgresStores(t)
	previousCore, previousTelemetry := coreDB, telemetryDB
	previousMaster := common.IsMasterNode
	previousPG, previousSQLite := common.UsingPostgreSQL, common.UsingSQLite
	previousRedisEnabled, previousRedis := common.RedisEnabled, common.RDB
	previousGroup, previousKey, previousTrue, previousFalse := commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal
	previousLogGroup, previousLogKey := logGroupCol, logKeyCol
	common.IsMasterNode = true
	common.RedisEnabled, common.RDB = false, nil
	common.UsingPostgreSQL, common.UsingSQLite = true, false
	initCol()
	t.Setenv("LAZU_MIGRATION_MODE", "auto")
	t.Cleanup(func() {
		coreDB, telemetryDB = previousCore, previousTelemetry
		common.IsMasterNode = previousMaster
		common.UsingPostgreSQL, common.UsingSQLite = previousPG, previousSQLite
		common.RedisEnabled, common.RDB = previousRedisEnabled, previousRedis
		commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal = previousGroup, previousKey, previousTrue, previousFalse
		logGroupCol, logKeyCol = previousLogGroup, previousLogKey
	})
	for _, test := range []struct {
		name       string
		store      *gorm.DB
		initialize func() error
		runtime    bool
	}{
		{"runtime-deadline-control", runtimeStore, func() error {
			// The fast initializer test covers InitDB's runtime wiring. Install
			// its same fence here without starting the runtime pricing ticker.
			SetCoreDB(runtimeStore)
			return InstallIdentityMutationFence(runtimeStore)
		}, true},
		{"offline-migration", offlineStore, InitDBForMigration, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			migrationReviewSchema(t, test.store)
			if err := test.store.Create(&SchemaVersion{Scope: schemaVersionPrimary, Version: SchemaVersionCurrent}).Error; err != nil {
				t.Fatal(err)
			}
			if err := test.store.Create(&Log{Id: 1, PublicID: "log_slow_partition", CreatedAt: time.Now().Unix(), RequestTS: 0}).Error; err != nil {
				t.Fatal(err)
			}
			// A legacy row has not passed the current insert hooks.
			if err := test.store.Exec("UPDATE logs SET request_ts = 0 WHERE id = 1").Error; err != nil {
				t.Fatal(err)
			}
			// One historical timestamp UPDATE takes longer than the runtime lease
			// deadline, reproducing a large production backfill with one row.
			if err := test.store.Exec(`CREATE FUNCTION delay_partition_backfill() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(11); RETURN NEW; END $$`).Error; err != nil {
				t.Fatal(err)
			}
			if err := test.store.Exec(`CREATE TRIGGER delay_partition_backfill BEFORE UPDATE OF request_ts ON logs FOR EACH ROW EXECUTE FUNCTION delay_partition_backfill()`).Error; err != nil {
				t.Fatal(err)
			}
			var schema string
			if err := test.store.Raw("SELECT current_schema()").Scan(&schema).Error; err != nil {
				t.Fatal(err)
			}
			dsn, err := url.Parse(os.Getenv("TEST_GATEWAY_CORE_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			if !test.runtime {
				dsn, err = url.Parse(os.Getenv("TEST_GATEWAY_TELEMETRY_DSN"))
				if err != nil {
					t.Fatal(err)
				}
			}
			query := dsn.Query()
			query.Set("search_path", schema)
			dsn.RawQuery = query.Encode()
			t.Setenv("DATABASE_URL", dsn.String())
			if err := test.initialize(); err != nil {
				t.Fatal(err)
			}
			raw, err := CoreDB().DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = raw.Close() })
			started := time.Now()
			err = migrateTelemetryPartitions(CoreDB())
			if elapsed := time.Since(started); elapsed < identityTransactionTimeout {
				t.Fatalf("slow backfill did not exercise the runtime transaction deadline: %s (%v)", elapsed, err)
			}
			if test.runtime {
				if !errors.Is(err, sql.ErrTxDone) && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("runtime no longer bounds long transactions: %v", err)
				}
				if test.store.Migrator().HasTable("logs_unpartitioned_v24") {
					t.Fatal("failed runtime transaction left a partial partition conversion")
				}
				var row Log
				if err := test.store.First(&row, 1).Error; err != nil || row.RequestTS != 0 {
					t.Fatalf("failed runtime transaction changed source timestamp: %+v %v", row, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("offline backfill was cut off by the runtime deadline: %v", err)
			}
			if err := AuditTelemetryPartitions(context.Background(), test.store); err != nil {
				t.Fatalf("long offline transaction lost historical facts: %v", err)
			}
			var marker SchemaVersion
			if err := test.store.First(&marker, "scope = ?", "telemetry_partition_v24").Error; err != nil {
				t.Fatalf("successful partition migration did not commit its marker: %v", err)
			}
		})
	}
}
