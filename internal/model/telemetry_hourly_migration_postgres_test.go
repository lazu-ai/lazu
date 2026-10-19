package model

import (
	"context"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func TestPostgresTelemetrySplitReseedsHourlyBeforeRebuild(t *testing.T) {
	for _, test := range []struct {
		name                  string
		copyAlreadyCommitted  bool
		totalsAlreadyReseeded bool
		emptyHourlyHistory    bool
		aheadSequence         bool
	}{
		{name: "fresh-copy"},
		{name: "copy-marker-committed", copyAlreadyCommitted: true},
		{name: "dimension-sequence-only", copyAlreadyCommitted: true, totalsAlreadyReseeded: true},
		{name: "empty-hourly-history", emptyHourlyHistory: true},
		{name: "ahead-sequence", copyAlreadyCommitted: true, aheadSequence: true},
		{name: "empty-called-sequence", copyAlreadyCommitted: true, emptyHourlyHistory: true, aheadSequence: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, telemetry := migrationReviewPostgresStores(t)
			oldCore, oldTelemetry := coreDB, telemetryDB
			oldPG, oldSQLite := common.UsingPostgreSQL, common.UsingSQLite
			oldOptions := common.OptionMap
			oldRetention := common.LogRetentionDays
			oldGroup, oldKey, oldTrue, oldFalse, oldLogGroup, oldLogKey := commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal, logGroupCol, logKeyCol
			coreDB, telemetryDB = core, telemetry
			common.UsingPostgreSQL, common.UsingSQLite = true, false
			common.OptionMap = map[string]string{DigestRollupSinceOption: "1"}
			common.LogRetentionDays = 30
			initCol()
			t.Cleanup(func() {
				coreDB, telemetryDB = oldCore, oldTelemetry
				common.UsingPostgreSQL, common.UsingSQLite = oldPG, oldSQLite
				common.OptionMap = oldOptions
				common.LogRetentionDays = oldRetention
				commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal, logGroupCol, logKeyCol = oldGroup, oldKey, oldTrue, oldFalse, oldLogGroup, oldLogKey
			})
			if err := core.AutoMigrate(&SchemaVersion{}, &User{}, &Channel{}, &Ability{}, &Log{}, &DashboardUsageHourlyTotal{}, &DashboardUsageHourlyDimension{}); err != nil {
				t.Fatal(err)
			}
			// Core's lifecycle migration has already completed before splitting
			// Telemetry. It must not run again or mutate the original wallet.
			if err := core.Create(&SchemaVersion{Scope: "telemetry_lifecycle_v24", Version: 1}).Error; err != nil {
				t.Fatal(err)
			}
			if err := core.Session(&gorm.Session{SkipHooks: true}).Create(&User{Id: 7, Username: "hourly-fixture", PublicID: "user_hourly_fixture", Quota: 1234567}).Error; err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			currentHour := now.Truncate(time.Hour).Add(-time.Hour).Unix()
			retainedHour := now.AddDate(0, 0, -common.LogRetentionDays-2).Truncate(time.Hour).Unix()
			if err := core.Create(&Log{Id: 700, PublicID: "log_hourly_copy", RequestID: "hourly-copy", CreatedAt: currentHour + 300, Type: LogTypeConsume, UserId: 7, TokenId: 9, ModelName: "hourly-fixture", AmountMicrousd: 123}).Error; err != nil {
				t.Fatal(err)
			}
			if !test.emptyHourlyHistory {
				// ID 1 is outside the rebuilt window and must survive. Explicit
				// copied IDs leave nextval at 1, colliding with that retained row.
				for _, row := range []DashboardUsageHourlyTotal{
					{ID: 1, BucketHour: retainedHour, RequestCount: 7, AmountMicrousd: 456, UpdatedAt: now},
					{ID: 900, BucketHour: currentHour, RequestCount: 99, AmountMicrousd: 999, UpdatedAt: now},
				} {
					if err := core.Create(&row).Error; err != nil {
						t.Fatal(err)
					}
				}
				for _, row := range []DashboardUsageHourlyDimension{
					{ID: 1, BucketHour: retainedHour, DimensionType: dashboardDimensionModel, DimensionID: "retained", DimensionName: "retained", RequestCount: 7, AmountMicrousd: 456, UpdatedAt: now},
					{ID: 900, BucketHour: currentHour, DimensionType: dashboardDimensionModel, DimensionID: "hourly-fixture", DimensionName: "hourly-fixture", RequestCount: 99, AmountMicrousd: 999, UpdatedAt: now},
				} {
					if err := core.Create(&row).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			sourceBefore, err := BuildMigrationProof(context.Background(), core, "telemetry")
			if err != nil {
				t.Fatal(err)
			}
			if test.copyAlreadyCommitted {
				if err := migrateTelemetrySchema(telemetry); err != nil {
					t.Fatal(err)
				}
				if err := telemetry.AutoMigrate(&SchemaVersion{}); err != nil {
					t.Fatal(err)
				}
				if err := runOneTimeMigration(telemetry, "telemetry_copy_core_v24", func() error {
					return CopyTelemetryHistory(context.Background(), core, telemetry)
				}); err != nil {
					t.Fatal(err)
				}
				for _, table := range []string{"dashboard_usage_hourly_totals", "dashboard_usage_hourly_dimensions"} {
					var sequence struct{ LastValue *int64 }
					if err := telemetry.Raw("SELECT pg_sequence_last_value(pg_get_serial_sequence(?,'id')::regclass) AS last_value", table).Scan(&sequence).Error; err != nil {
						t.Fatal(err)
					}
					if sequence.LastValue != nil {
						t.Fatalf("explicit copy unexpectedly called the %s sequence: %d", table, *sequence.LastValue)
					}
					if test.aheadSequence {
						if err := telemetry.Exec("SELECT setval(pg_get_serial_sequence(?,'id'),10000,true)", table).Error; err != nil {
							t.Fatal(err)
						}
					}
				}
				if test.totalsAlreadyReseeded {
					if err := telemetry.Exec("SELECT setval(pg_get_serial_sequence('dashboard_usage_hourly_totals','id'),900,true)").Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := RunLogMigrations(); err != nil {
				t.Fatalf("split initialization collided with copied hourly IDs: %v", err)
			}
			var total DashboardUsageHourlyTotal
			if err := telemetry.Where("bucket_hour = ? AND scope_user_id = 0", currentHour).First(&total).Error; err != nil {
				t.Fatal(err)
			}
			var dimension DashboardUsageHourlyDimension
			if err := telemetry.Where("bucket_hour = ? AND scope_user_id = 0 AND dimension_type = ? AND dimension_id = ?", currentHour, dashboardDimensionModel, "hourly-fixture").First(&dimension).Error; err != nil {
				t.Fatal(err)
			}
			if total.RequestCount != 1 || total.AmountMicrousd != 123 || dimension.RequestCount != 1 || dimension.AmountMicrousd != 123 {
				t.Fatalf("hourly rebuild retained stale or duplicated aggregates: total=%+v dimension=%+v", total, dimension)
			}
			if test.aheadSequence && (total.ID <= 10000 || dimension.ID <= 10000) {
				t.Fatalf("rebuild reused previously generated IDs: total=%d dimension=%d", total.ID, dimension.ID)
			}
			if test.emptyHourlyHistory && !test.aheadSequence {
				for _, table := range []string{"dashboard_usage_hourly_totals", "dashboard_usage_hourly_dimensions"} {
					var firstID uint
					if err := telemetry.Table(table).Select("MIN(id)").Scan(&firstID).Error; err != nil || firstID != 1 {
						t.Fatalf("empty %s sequence skipped its first ID: %d %v", table, firstID, err)
					}
				}
			} else if !test.emptyHourlyHistory {
				if total.ID <= 900 || dimension.ID <= 900 {
					t.Fatalf("generated IDs did not exceed copied history: total=%d dimension=%d", total.ID, dimension.ID)
				}
				var retainedTotal DashboardUsageHourlyTotal
				var retainedDimension DashboardUsageHourlyDimension
				if err := telemetry.First(&retainedTotal, 1).Error; err != nil {
					t.Fatal(err)
				}
				if err := telemetry.First(&retainedDimension, 1).Error; err != nil {
					t.Fatal(err)
				}
				if retainedTotal.BucketHour != retainedHour || retainedTotal.RequestCount != 7 || retainedTotal.AmountMicrousd != 456 || !retainedTotal.UpdatedAt.Equal(now) || retainedDimension.BucketHour != retainedHour || retainedDimension.RequestCount != 7 || retainedDimension.AmountMicrousd != 456 || !retainedDimension.UpdatedAt.Equal(now) {
					t.Fatalf("rebuild changed retained history: total=%+v dimension=%+v", retainedTotal, retainedDimension)
				}
			}
			sourceAfter, err := BuildMigrationProof(context.Background(), core, "telemetry")
			if err != nil {
				t.Fatal(err)
			}
			if err := CompareMigrationProofs(sourceBefore, sourceAfter); err != nil {
				t.Fatalf("split migration changed source history: %v", err)
			}
			var wallet User
			if err := core.First(&wallet, 7).Error; err != nil || wallet.Quota != 1234567 {
				t.Fatalf("split migration changed Core wallet: amount=%d err=%v", wallet.Quota, err)
			}
			beforeRetry, err := BuildMigrationProof(context.Background(), telemetry, "telemetry")
			if err != nil {
				t.Fatal(err)
			}
			if err := RunLogMigrations(); err != nil {
				t.Fatalf("completed split initialization was not retryable: %v", err)
			}
			afterRetry, err := BuildMigrationProof(context.Background(), telemetry, "telemetry")
			if err != nil {
				t.Fatal(err)
			}
			if err := CompareMigrationProofs(beforeRetry, afterRetry); err != nil {
				t.Fatalf("completed initialization rebuilt facts on retry: %v", err)
			}
		})
	}
}
