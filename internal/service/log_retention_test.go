package service

import (
	"fmt"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestAutomaticLogRetentionPreservesRankingWindow(t *testing.T) {
	for _, tt := range []struct {
		days int
		want int64
	}{
		{0, 5}, {-1, 5}, {7, 3}, {30, 3}, {45, 4},
		{int(maxLogRetentionDays), 5}, {int(maxLogRetentionDays) + 1, 5}, {int(^uint(0) >> 1), 5},
	} {
		t.Run(fmt.Sprint(tt.days), func(t *testing.T) {
			db, err := gorm.Open(gormsqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			previousDB, previousLogDB, previousDays := model.CoreDB(), model.ConfiguredTelemetryDB(), common.LogRetentionDays
			model.SetCoreDB(nil)
			model.SetTelemetryDB(db)
			common.LogRetentionDays = tt.days
			defer func() {
				model.SetCoreDB(previousDB)
				model.SetTelemetryDB(previousLogDB)
				common.LogRetentionDays = previousDays
			}()
			if err := db.Exec("CREATE TABLE logs (created_at INTEGER)").Error; err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			for _, age := range []int{8, 29, 30, 31, 46} {
				createdAt := now.Add(-time.Duration(age) * 24 * time.Hour)
				if err := db.Exec("INSERT INTO logs VALUES (?)", createdAt.Unix()).Error; err != nil {
					t.Fatal(err)
				}
			}
			RunLogRetentionOnce(now)
			var count int64
			if err := db.Table("logs").Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != tt.want {
				t.Fatalf("logs retained %d rows, want %d", count, tt.want)
			}
		})
	}
}

func TestContentRetentionRunsWithMetadataRetentionDisabled(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	previousDB, previousDays := model.CoreDB(), common.LogRetentionDays
	model.SetCoreDB(db)
	common.LogRetentionDays = 0
	t.Cleanup(func() { model.SetCoreDB(previousDB); common.LogRetentionDays = previousDays })
	t.Setenv("R2_ARCHIVE_RETENTION_DAYS", "180")
	if err := db.AutoMigrate(&model.AgentSession{}, &model.AgentSessionMessage{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -181)
	session := model.AgentSession{PublicID: "expired", UserID: 1, SessionID: "expired", FirstMessagePreview: "private", StartedAt: old.Unix(), AmountMicrousd: 100}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AgentSessionMessage{SessionPK: session.ID, Content: "private", CreatedAt: old}).Error; err != nil {
		t.Fatal(err)
	}
	RunLogRetentionOnce(now)
	var count int64
	if err := db.Model(&model.AgentSessionMessage{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&session, session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 || session.FirstMessagePreview != "" || session.AmountMicrousd != 100 {
		t.Fatal("content cleanup was skipped with metadata retention disabled")
	}
}
