package model

import (
	"bytes"
	"math"
	"os"
	"regexp"
	"testing"
)

func TestAccountLogBelongsToTheOwnedProject(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.Create(&User{Id: 5, Username: "owner-five"}).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", 5).Limit(1).Find(&project).Error; err != nil {
		t.Fatal(err)
	}
	if project.ID == 0 {
		project = Project{PublicID: "prj_account_log", OwnerUserID: 5, Name: "owned"}
		if err := db.Create(&project).Error; err != nil {
			t.Fatal(err)
		}
	}
	if log := accountLog(5, LogTypeTopup, "top-up"); log.ProjectID != project.ID || log.UserId != 5 || log.Username != "owner-five" || log.Type != LogTypeTopup {
		t.Fatalf("owner's account log: %+v", log)
	}
	if log := accountLog(77, LogTypeManage, "change"); log.ProjectID != 0 {
		t.Fatalf("a user without a project got project %d", log.ProjectID)
	}
}

func TestSQLiteSchema37BackfillsOwnerLogsAndIndexesProjects(t *testing.T) {
	core := setupProjectTestDB(t, true)
	if err := core.Create(&Project{ID: 7, PublicID: "prj_sqlite_v37", OwnerUserID: 1, Name: "a"}).Error; err != nil {
		t.Fatal(err)
	}
	telemetry := setupProjectTestDB(t, false)
	if err := telemetry.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.Create(&[]Log{
		{Id: 1, UserId: 1, Type: LogTypeTopup},
		{Id: 2, UserId: 2, Type: LogTypeTopup},
		{Id: 3, ProjectID: 9, UserId: 1, Type: LogTypeConsume},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillLogProjectIDs(core, telemetry, math.MinInt64); err != nil {
		t.Fatal(err)
	}
	var projects []int
	if err := telemetry.Model(&Log{}).Order("id").Pluck("project_id", &projects).Error; err != nil || len(projects) != 3 || projects[0] != 7 || projects[1] != 0 || projects[2] != 9 {
		t.Fatalf("backfilled projects %v %v", projects, err)
	}
	if err := ensureLogListIndexes(telemetry, false); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := telemetry.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'logs_project_created'").Scan(&count).Error; err != nil || count != 1 {
		t.Fatalf("project index missing: %d %v", count, err)
	}
	if err := ensureLogListIndexes(telemetry, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

// Every log list pages through orderLogsNewestFirst. A list ordered by id
// alone has no index that matches both its filter and its order, and on the
// partitioned table a page of a filter matching few rows walks the id index
// through everyone else's (schema_v37.go). TestPostgresLogPagesReadOnlyTheirPage
// measures the plans; this keeps new lists from ordering by id.
func TestLogListsOrderNewestFirst(t *testing.T) {
	byID := regexp.MustCompile(`(?i)Order\("(logs\.)?id (asc|desc)"\)|ORDER BY (logs\.)?id\b`)
	const helper = `tx.Order("logs.created_at DESC").Order("logs.id DESC")`
	for _, file := range []string{"log.go", "project_log.go", "usage_dashboard.go"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if match := byID.Find(bytes.ReplaceAll(body, []byte(helper), nil)); match != nil {
			t.Errorf("%s orders logs by id (%s); use orderLogsNewestFirst", file, match)
		}
	}
}
