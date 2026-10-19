package model

import (
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func setupMonthlyReportTestDB(t *testing.T) func() {
	t.Helper()

	previousDB := coreDB
	previousLogDB := telemetryDB
	previousPostgresFlag := common.UsingPostgreSQL

	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	telemetryDB = db
	common.UsingPostgreSQL = false

	if err := db.AutoMigrate(&MonthlyReport{}); err != nil {
		t.Fatalf("failed to migrate monthly reports: %v", err)
	}

	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = previousDB
		telemetryDB = previousLogDB
		common.UsingPostgreSQL = previousPostgresFlag
	}
}

func TestEnsureMonthlyReportUniqConstraint_DeduplicateAndEnforce(t *testing.T) {
	cleanup := setupMonthlyReportTestDB(t)
	defer cleanup()

	initialReports := []MonthlyReport{
		{
			UserID:  11,
			Year:    2025,
			Month:   1,
			Summary: JSONValue(`{"overview":{"total_requests":1}}`),
			Status:  "generating",
		},
		{
			UserID:  11,
			Year:    2025,
			Month:   1,
			Summary: JSONValue(`{"overview":{"total_requests":2}}`),
			Status:  "completed",
		},
		{
			UserID:  11,
			Year:    2025,
			Month:   2,
			Summary: JSONValue(`{"overview":{"total_requests":3}}`),
			Status:  "completed",
		},
	}
	if err := coreDB.Create(&initialReports).Error; err != nil {
		t.Fatalf("failed to seed reports: %v", err)
	}

	if err := EnsureMonthlyReportUniqConstraint(); err != nil {
		t.Fatalf("EnsureMonthlyReportUniqConstraint failed: %v", err)
	}

	var janCount int64
	if err := coreDB.Model(&MonthlyReport{}).
		Where("user_id = ? AND year = ? AND month = ?", 11, 2025, 1).
		Count(&janCount).Error; err != nil {
		t.Fatalf("failed to count reports: %v", err)
	}
	if janCount != 1 {
		t.Fatalf("expected deduplicated monthly report count=1, got %d", janCount)
	}

	var kept MonthlyReport
	if err := coreDB.Where("user_id = ? AND year = ? AND month = ?", 11, 2025, 1).
		First(&kept).Error; err != nil {
		t.Fatalf("failed to load kept report: %v", err)
	}
	if kept.Status != "completed" {
		t.Fatalf("expected latest report to be kept, got status=%s", kept.Status)
	}

	duplicate := MonthlyReport{
		UserID:  11,
		Year:    2025,
		Month:   1,
		Summary: JSONValue(`{"overview":{"total_requests":99}}`),
		Status:  "completed",
	}
	if err := coreDB.Create(&duplicate).Error; err == nil {
		t.Fatal("expected unique index to reject duplicate monthly report, got nil error")
	}
}
