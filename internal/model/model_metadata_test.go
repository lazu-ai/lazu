package model

import (
	"math"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
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

func TestCatalogPopularityUsesOnlyRecentModelTotals(t *testing.T) {
	defer setupModelMetadataTestDB(t)()
	today := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	rows := []DashboardUsageDailyDimension{
		{DayStart: today.Unix(), DimensionType: "model", ModelName: "first", RequestCount: 8},
		{DayStart: today.AddDate(0, 0, -7).Unix(), DimensionType: "model", ModelName: "first", RequestCount: 7},
		{DayStart: today.Unix(), DimensionType: "model", ModelName: "second", RequestCount: 3},
		{DayStart: today.AddDate(0, 0, -8).Unix(), DimensionType: "model", ModelName: "old", RequestCount: 500},
		{DayStart: today.AddDate(0, 0, 1).Unix(), DimensionType: "model", ModelName: "future", RequestCount: 500},
		{DayStart: today.Unix(), DimensionType: "user", ModelName: "unrelated", RequestCount: 500},
	}
	if err := TelemetryDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	scores := CatalogPopularity(today.Add(3 * time.Hour))
	if len(scores) != 2 || math.Abs(scores["first"]-100) > 1e-9 || math.Abs(scores["second"]-50) > 1e-9 {
		t.Fatalf("catalog scores = %v", scores)
	}
}
