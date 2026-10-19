package model

import (
	"context"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

func TestDeleteOldLogPreservesRankingWindow(t *testing.T) {
	for _, tt := range []struct {
		name      string
		cutoffAge int
		rejected  bool
		deleted   int64
	}{
		{"future cutoff", -1, true, 0},
		{"current cutoff", 0, true, 0},
		{"inside ranking window", 29, true, 0},
		{"ranking boundary", 30, false, 2},
		{"older cutoff", 31, false, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, err := gorm.Open(gormsqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			previous := telemetryDB
			telemetryDB = db
			defer func() { telemetryDB = previous }()
			if err := db.Exec("CREATE TABLE logs (created_at INTEGER)").Error; err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			for _, age := range []int{1, 29, 30, 31, 46} {
				if err := db.Exec("INSERT INTO logs VALUES (?)", now.Add(-time.Duration(age)*24*time.Hour).Unix()).Error; err != nil {
					t.Fatal(err)
				}
			}
			cutoff := now.Add(-time.Duration(tt.cutoffAge) * 24 * time.Hour).Unix()
			deleted, err := DeleteOldLog(context.Background(), cutoff, 100)
			if tt.rejected {
				if !types.IsErrCode(err, types.ErrorCodeLogCleanupCutoffTooRecent) {
					t.Fatalf("expected actionable cutoff error, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if deleted != tt.deleted {
				t.Fatalf("deleted %d, want %d", deleted, tt.deleted)
			}
			var remaining int64
			if err := db.Table("logs").Count(&remaining).Error; err != nil {
				t.Fatal(err)
			}
			if remaining != 5-tt.deleted {
				t.Fatalf("remaining %d, want %d", remaining, 5-tt.deleted)
			}
		})
	}
}
