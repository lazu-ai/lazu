package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func setupModelMetadataTestDB(t *testing.T) func() {
	t.Helper()

	prevDB, prevTelemetry := coreDB, telemetryDB

	db, err := gorm.Open(gormsqlite.Open("file:model_metadata_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB, telemetryDB = db, db

	if err := db.AutoMigrate(&ModelMetadata{}, &ModelRequestCounter{}, &DashboardUsageDailyDimension{}); err != nil {
		t.Fatalf("failed to migrate model metadata table: %v", err)
	}

	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB, telemetryDB = prevDB, prevTelemetry
	}
}

func TestIncrementModelRequests_InsertAndConflictUpdate(t *testing.T) {
	cleanup := setupModelMetadataTestDB(t)
	defer cleanup()

	if err := IncrementModelRequests("gpt-4o"); err != nil {
		t.Fatalf("first increment failed: %v", err)
	}

	var counter ModelRequestCounter
	if err := TelemetryDB().Where("model_name = ?", "gpt-4o").First(&counter).Error; err != nil {
		t.Fatal(err)
	}
	if counter.TotalRequests != 1 {
		t.Fatalf("telemetry lifetime count = %d, want 1", counter.TotalRequests)
	}
	if err := IncrementModelRequests("gpt-4o"); err != nil {
		t.Fatalf("second increment failed: %v", err)
	}
	if err := TelemetryDB().Where("model_name = ?", "gpt-4o").First(&counter).Error; err != nil {
		t.Fatal(err)
	}
	if counter.TotalRequests != 2 {
		t.Fatalf("telemetry lifetime count = %d, want 2", counter.TotalRequests)
	}
	var metadataCount int64
	if err := CoreDB().Model(&ModelMetadata{}).Count(&metadataCount).Error; err != nil || metadataCount != 0 {
		t.Fatalf("request telemetry wrote core model metadata: count=%d err=%v", metadataCount, err)
	}
}

func TestModelRequestIncrementConflictClause_QualifiesCounterColumns(t *testing.T) {
	cleanup := setupModelMetadataTestDB(t)
	defer cleanup()

	now := time.Unix(1700000000, 0)
	record := ModelMetadata{
		ModelName:       "gpt-4o",
		FirstSeenAt:     now,
		TotalRequests:   1,
		WeeklyRequests:  1,
		MonthlyRequests: 1,
	}

	tx := coreDB.Session(&gorm.Session{DryRun: true}).
		Clauses(modelRequestIncrementConflictClause(now)).
		Create(&record)
	if tx.Error != nil {
		t.Fatalf("failed to build dry-run increment SQL: %v", tx.Error)
	}
	sql := tx.Statement.SQL.String()

	for _, column := range []string{"total_requests"} {
		if !containsQualifiedIncrement(sql, column) {
			t.Fatalf("expected %s increment to use a qualified column reference, got SQL: %s", column, sql)
		}
	}
}

func TestCalculateFinalScoreNormalizesRetiredQualityWeight(t *testing.T) {
	score := calculateFinalScore(&ModelMetadata{
		PopularityScore: 100,
		FreshnessScore:  100,
		ValueScore:      100,
		VendorScore:     100,
	})
	if score != 100 {
		t.Fatalf("expected fully scored model to remain at 100 after quality retirement, got %v", score)
	}
}

func assertModelRequestCounters(t *testing.T, meta ModelMetadata, expected, weekly, monthly int64) {
	t.Helper()

	list := []ModelMetadata{meta}
	if err := hydrateLifetimeModelCounts(list); err != nil {
		t.Fatal(err)
	}
	meta = list[0]
	if meta.TotalRequests != expected {
		t.Fatalf("expected total_requests=%d, got %d", expected, meta.TotalRequests)
	}
	if meta.WeeklyRequests != weekly {
		t.Fatalf("expected weekly_requests=%d, got %d", weekly, meta.WeeklyRequests)
	}
	if meta.MonthlyRequests != monthly {
		t.Fatalf("expected monthly_requests=%d, got %d", monthly, meta.MonthlyRequests)
	}
}

func containsQualifiedIncrement(sql string, column string) bool {
	return strings.Contains(sql, fmt.Sprintf(".`%s` + 1", column)) ||
		strings.Contains(sql, fmt.Sprintf(`."%s" + 1`, column))
}

func TestModelMetadataScheduledCounters(t *testing.T) {
	cleanup := setupModelMetadataTestDB(t)
	defer cleanup()
	previousLogDB := telemetryDB
	telemetryDB = coreDB
	defer func() { telemetryDB = previousLogDB }()
	for _, name := range []string{"active", "stale"} {
		if err := coreDB.Create(&ModelMetadata{ModelName: name, TotalRequests: 99, WeeklyRequests: 88, MonthlyRequests: 77}).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Unix()
	for _, entry := range []struct {
		name string
		kind int
		age  int64
	}{
		{"active", LogTypeConsume, 3600},
		{"active", LogTypeConsume, 10 * 24 * 3600},
		{"stale", LogTypeConsume, 31 * 24 * 3600},
		{"active", LogTypeConsume + 1, 3600},
		{"", LogTypeConsume, 3600},
	} {
		requests, errors := int64(1), int64(0)
		if entry.kind != LogTypeConsume {
			requests, errors = 0, 1
		}
		seedModelDailyCounter(t, entry.name, now-entry.age, requests, errors)
	}
	check := func(name string, weekly, monthly int64) {
		t.Helper()
		var meta ModelMetadata
		if err := coreDB.Where("model_name = ?", name).First(&meta).Error; err != nil {
			t.Fatal(err)
		}
		if meta.TotalRequests != 99 || meta.WeeklyRequests != weekly || meta.MonthlyRequests != monthly {
			t.Fatalf("%s: counters = %d/%d/%d, want 99/%d/%d", name, meta.TotalRequests, meta.WeeklyRequests, meta.MonthlyRequests, weekly, monthly)
		}
	}
	// Repeated hourly refreshes must replace, rather than accumulate, window counts.
	for i := 0; i < 2; i++ {
		if err := UpdateWeeklyRequestsFromLogs(); err != nil {
			t.Fatal(err)
		}
		if err := UpdateMonthlyRequestsFromLogs(); err != nil {
			t.Fatal(err)
		}
		check("active", 1, 2)
		check("stale", 0, 0)
	}
	if err := ResetWeeklyStats(); err != nil {
		t.Fatal(err)
	}
	check("active", 0, 2)
	if err := ResetMonthlyStats(); err != nil {
		t.Fatal(err)
	}
	check("active", 0, 0)
	// With no consumption aggregates, both refreshes still clear stale counters.
	if err := TelemetryDB().Where("1 = 1").Delete(&DashboardUsageDailyDimension{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := coreDB.Where("model_name = ?", "active").Model(&ModelMetadata{}).Updates(map[string]interface{}{"weekly_requests": 5, "monthly_requests": 6}).Error; err != nil {
		t.Fatal(err)
	}
	if err := UpdateWeeklyRequestsFromLogs(); err != nil {
		t.Fatal(err)
	}
	if err := UpdateMonthlyRequestsFromLogs(); err != nil {
		t.Fatal(err)
	}
	check("active", 0, 0)
	if err := coreDB.Model(&ModelMetadata{}).Update("total_requests", 0).Error; !errors.Is(err, gorm.ErrMissingWhereClause) {
		t.Fatalf("global update guard was not preserved: %v", err)
	}
}

func TestModelMetadataWindowRefreshRollsBackOnFailure(t *testing.T) {
	for _, tt := range []struct {
		name    string
		refresh func() error
	}{
		{"weekly", UpdateWeeklyRequestsFromLogs},
		{"monthly", UpdateMonthlyRequestsFromLogs},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setupModelMetadataTestDB(t)
			defer cleanup()
			prevLogDB := telemetryDB
			telemetryDB = coreDB
			defer func() { telemetryDB = prevLogDB }()
			for _, name := range []string{"a", "b", "stale"} {
				if err := coreDB.Create(&ModelMetadata{ModelName: name, TotalRequests: 99, WeeklyRequests: 8, MonthlyRequests: 9}).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"a", "b"} {
				seedModelDailyCounter(t, name, time.Now().Unix()-3600, 1, 0)
			}
			injected := errors.New("injected second model update failure")
			updates := 0
			if err := coreDB.Callback().Update().Before("gorm:update").Register("test:window_failure", func(tx *gorm.DB) {
				updates++
				// Reset succeeds, then the first model succeeds, then the second fails.
				if updates == 3 {
					tx.AddError(injected)
				}
			}); err != nil {
				t.Fatal(err)
			}
			defer coreDB.Callback().Update().Remove("test:window_failure")
			if err := tt.refresh(); !errors.Is(err, injected) {
				t.Fatalf("got %v, want injected failure", err)
			}
			var records []ModelMetadata
			if err := coreDB.Find(&records).Error; err != nil {
				t.Fatal(err)
			}
			if len(records) != 3 {
				t.Fatalf("got %d models", len(records))
			}
			for _, record := range records {
				if record.TotalRequests != 99 || record.WeeklyRequests != 8 || record.MonthlyRequests != 9 {
					t.Fatalf("partial update survived rollback: %+v", record)
				}
			}
		})
	}
}

func TestModelRequestSideEffectsDoNotChangeRollingSnapshot(t *testing.T) {
	cleanup := setupModelMetadataTestDB(t)
	defer cleanup()
	previousLogDB := telemetryDB
	telemetryDB = coreDB
	defer func() { telemetryDB = previousLogDB }()
	if err := EnsureModelMetadataForNames([]string{"active"}); err != nil {
		t.Fatal(err)
	}
	// The daily aggregate is committed before its deferred usage side effect runs.
	seedModelDailyCounter(t, "active", time.Now().Unix()-3600, 1, 0)
	if err := UpdateWeeklyRequestsFromLogs(); err != nil {
		t.Fatal(err)
	}
	if err := UpdateMonthlyRequestsFromLogs(); err != nil {
		t.Fatal(err)
	}
	if err := IncrementModelRequests("active"); err != nil {
		t.Fatal(err)
	}
	var meta ModelMetadata
	if err := coreDB.Where("model_name = ?", "active").First(&meta).Error; err != nil {
		t.Fatal(err)
	}
	assertModelRequestCounters(t, meta, 1, 1, 1)
	// A subsequent request updates lifetime usage without changing the last
	// published snapshot. The next refresh picks up both daily contributions exactly once.
	seedModelDailyCounter(t, "active", time.Now().Unix()-1800, 1, 0)
	if err := IncrementModelRequests("active"); err != nil {
		t.Fatal(err)
	}
	if err := coreDB.Where("model_name = ?", "active").First(&meta).Error; err != nil {
		t.Fatal(err)
	}
	assertModelRequestCounters(t, meta, 2, 1, 1)
	if err := UpdateWeeklyRequestsFromLogs(); err != nil {
		t.Fatal(err)
	}
	if err := UpdateMonthlyRequestsFromLogs(); err != nil {
		t.Fatal(err)
	}
	if err := coreDB.Where("model_name = ?", "active").First(&meta).Error; err != nil {
		t.Fatal(err)
	}
	assertModelRequestCounters(t, meta, 2, 2, 2)
}

func seedModelDailyCounter(t *testing.T, name string, at int64, requests, errors int64) {
	t.Helper()
	day := time.Unix(at, 0).UTC().Truncate(24 * time.Hour).Unix()
	row := DashboardUsageDailyDimension{DayStart: day, DimensionType: "model", ModelName: name, RequestCount: requests, ErrorCount: errors}
	updates := map[string]interface{}{"request_count": gorm.Expr("dashboard_usage_daily_dimensions.request_count + excluded.request_count"), "error_count": gorm.Expr("dashboard_usage_daily_dimensions.error_count + excluded.error_count")}
	if err := TelemetryDB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "day_start"}, {Name: "dimension_type"}, {Name: "dimension_id"}, {Name: "model_name"}}, DoUpdates: clause.Assignments(updates)}).Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}
