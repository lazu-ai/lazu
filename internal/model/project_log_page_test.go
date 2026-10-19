package model

import (
	"context"
	"errors"
	"testing"
)

func TestProjectLogPagesKeepSQLitePaginationAndContext(t *testing.T) {
	db := setupProjectTestDB(t, false)
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	old := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = old })
	access := ProjectLogAccess{Project: Project{ID: 42, OwnerUserID: 1}, Viewer: ProjectMember{ProjectID: 42, UserID: 1, Role: ProjectRoleOwner, Status: ProjectMemberActive}}
	if err := db.Create(&[]Log{
		{Id: 10, ProjectID: 42, UserId: 1, Type: LogTypeConsume},
		{Id: 20, ProjectID: 42, UserId: 2, Type: LogTypeConsume},
		{Id: 30, ProjectID: 0, UserId: 1, Type: LogTypeError},
		{Id: 40, ProjectID: 43, UserId: 1, Type: LogTypeConsume},
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int{1, 100, 1} {
		logs, total, err := ListProjectLogsContext(context.Background(), access, ProjectLogFilter{}, offset, 1)
		if err != nil || total != 3 {
			t.Fatalf("count changed across page queries: %d %v", total, err)
		}
		if offset == 1 && (len(logs) != 1 || logs[0].Id != 20) || offset == 100 && len(logs) != 0 {
			t.Fatal("non-PostgreSQL page behavior changed")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := ListProjectLogsContext(ctx, access, ProjectLogFilter{}, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("count lost cancellation: %v", err)
	}
	if _, err := ListProjectLogPageContext(ctx, access, ProjectLogFilter{}, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("page lost cancellation: %v", err)
	}
}
