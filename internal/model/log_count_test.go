package model

import (
	"context"
	"path/filepath"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestLogListCountStopsAtTheLimit(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open(filepath.Join(t.TempDir(), "count.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	previous := ConfiguredTelemetryDB()
	SetTelemetryDB(db)
	t.Cleanup(func() { SetTelemetryDB(previous); sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	limit := LogCountExactLimit
	LogCountExactLimit = 5
	t.Cleanup(func() { LogCountExactLimit = limit })

	access := ProjectLogAccess{
		Project: Project{ID: 7, OwnerUserID: 1},
		Viewer:  ProjectMember{ProjectID: 7, UserID: 1, Role: ProjectRoleOwner, Status: ProjectMemberActive},
	}
	insert := func(n int, userID int) {
		t.Helper()
		for i := 0; i < n; i++ {
			if err := db.Create(&Log{ProjectID: 7, UserId: userID, Type: LogTypeConsume, CreatedAt: 1_800_000_000 + int64(i)}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	insert(5, 1)
	logs, total, atLeast, err := ListProjectLogsCappedContext(context.Background(), access, ProjectLogFilter{}, 0, 2)
	if err != nil || total != 5 || atLeast || len(logs) != 2 {
		t.Fatalf("at the limit: rows=%d total=%d atLeast=%t err=%v", len(logs), total, atLeast, err)
	}
	insert(3, 1)
	logs, total, atLeast, err = ListProjectLogsCappedContext(context.Background(), access, ProjectLogFilter{}, 0, 2)
	if err != nil || total != 5 || !atLeast || len(logs) != 2 {
		t.Fatalf("past the limit: rows=%d total=%d atLeast=%t err=%v", len(logs), total, atLeast, err)
	}
	// A filter is counted, not the whole table.
	insert(2, 9)
	_, total, atLeast, err = GetAllLogs(LogTypeConsume, 0, 0, "", "", 9, "", 0, 10, 0, "")
	if err != nil || total != 2 || atLeast {
		t.Fatalf("filtered admin count: total=%d atLeast=%t err=%v", total, atLeast, err)
	}
	_, total, atLeast, err = GetAllLogs(LogTypeConsume, 0, 0, "", "", 0, "", 0, 10, 0, "")
	if err != nil || total != 5 || !atLeast {
		t.Fatalf("admin count: total=%d atLeast=%t err=%v", total, atLeast, err)
	}
}
