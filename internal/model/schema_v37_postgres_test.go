package model

import (
	"context"
	"math"
	"testing"
	"time"

	"gorm.io/gorm"
)

func schema37PostgresFixture(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	core, db := postgresTelemetryFixture(t)
	if err := core.AutoMigrate(&Project{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE logs ALTER COLUMN project_id DROP NOT NULL").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return partitionTelemetryTableTx(tx, telemetryPartitionSpecs[0]) }); err != nil {
		t.Fatal(err)
	}
	return core, db
}

func validLogIndex(t *testing.T, db *gorm.DB, name string) bool {
	t.Helper()
	var valid bool
	if err := db.Raw("SELECT COALESCE((SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass(?)), false)", name).Scan(&valid).Error; err != nil {
		t.Fatal(err)
	}
	return valid
}

func TestPostgresSchema37BuildsListIndexesOnlineAndResumes(t *testing.T) {
	_, db := schema37PostgresFixture(t)
	// The user index already exists under another name: it is not built twice.
	if err := db.Exec("CREATE INDEX existing_user_created ON logs (user_id, created_at)").Error; err != nil {
		t.Fatal(err)
	}
	// A concurrent build that failed left an invalid index on one partition.
	partition := "logs_" + time.Now().UTC().Format("200601")
	if err := db.Exec("CREATE INDEX " + partition + "_project_created ON " + partition + " (project_id, created_at)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE pg_index SET indisvalid = false WHERE indexrelid = '" + partition + "_project_created'::regclass").Error; err != nil {
		t.Skipf("cannot mark an index invalid without superuser: %v", err)
	}
	if err := ensureLogListIndexes(db, true); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"logs_project_created", "logs_token_created", "logs_channel_created"} {
		if !validLogIndex(t, db, index) {
			t.Fatalf("%s is not valid", index)
		}
	}
	var duplicates int64
	if err := db.Raw("SELECT count(*) FROM pg_indexes WHERE tablename = 'logs' AND indexdef LIKE '% USING btree (user_id, created_at)'").Scan(&duplicates).Error; err != nil || duplicates != 1 {
		t.Fatalf("user index built beside an equivalent one: %d %v", duplicates, err)
	}
	if err := ensureLogListIndexes(db, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	// A partition created later gets the indexes from its parent.
	if err := db.Transaction(func(tx *gorm.DB) error {
		return ensureTelemetryPartitionTx(tx, "logs", time.Now().UTC().AddDate(0, 6, 0))
	}); err != nil {
		t.Fatal(err)
	}
	var attached int64
	if err := db.Raw(`SELECT count(*) FROM pg_inherits i JOIN pg_index x ON x.indexrelid = i.inhrelid
		WHERE i.inhparent = 'logs_project_created'::regclass`).Scan(&attached).Error; err != nil {
		t.Fatal(err)
	}
	var partitions int64
	if err := db.Raw("SELECT count(*) FROM pg_inherits WHERE inhparent = 'logs'::regclass").Scan(&partitions).Error; err != nil || attached != partitions {
		t.Fatalf("project index on %d of %d partitions: %v", attached, partitions, err)
	}
}

func TestPostgresSchema37BackfillsOwnerLogsOnly(t *testing.T) {
	core, db := schema37PostgresFixture(t)
	if err := core.Create(&[]Project{{ID: 7, PublicID: "prj_v37_a", OwnerUserID: 1, Name: "a"}, {ID: 8, PublicID: "prj_v37_b", OwnerUserID: 2, Name: "b"}}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := func(when time.Time) int64 { return when.UnixMilli() }
	rows := []Log{
		{Id: 1, PublicID: "v37_1", RequestTS: at(now), CreatedAt: now.Unix(), UserId: 1, Type: LogTypeTopup},                                   // owner 1, no project
		{Id: 2, PublicID: "v37_2", RequestTS: 0, CreatedAt: now.Unix(), UserId: 2, Type: LogTypeManage},                                        // owner 2, undated partition
		{Id: 3, PublicID: "v37_3", RequestTS: at(now.AddDate(0, -2, 0)), CreatedAt: now.Unix(), UserId: 1, Type: LogTypeConsume},               // owner 1, older month
		{Id: 4, PublicID: "v37_4", RequestTS: at(now), CreatedAt: now.Unix(), ProjectID: 8, UserId: 1, Type: LogTypeConsume},                   // owner 1 working in project 8
		{Id: 5, PublicID: "v37_5", RequestTS: at(now), CreatedAt: now.Unix(), UserId: 3, Type: LogTypeConsume},                                 // owns no project
		{Id: 6, PublicID: "v37_6", RequestTS: at(now.Add(-48 * time.Hour)), CreatedAt: now.Unix(), UserId: 2, Type: LogTypeSystem},             // owner 2, NULL project below
		{Id: 7, PublicID: "v37_7", RequestTS: at(now.Add(-24 * time.Hour)), CreatedAt: now.Unix(), UserId: 1, Type: LogTypeConsume},            // owner 1, a day apart
		{Id: 8, PublicID: "v37_8", RequestTS: math.MaxInt64 / 2, CreatedAt: now.Unix(), UserId: 2, Type: LogTypeConsume},                       // far future, undated
		{Id: 9, PublicID: "v37_9", RequestTS: at(now), CreatedAt: now.Unix(), ProjectID: 0, UserId: 0, Type: LogTypeSystem},                    // no user
		{Id: 10, PublicID: "v37_10", RequestTS: at(now.Add(-time.Hour)), CreatedAt: now.Unix(), ProjectID: 7, UserId: 2, Type: LogTypeConsume}, // member of 7
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE logs SET project_id = NULL WHERE id = 6").Error; err != nil {
		t.Fatal(err)
	}
	want := map[int]int{1: 7, 2: 8, 3: 7, 4: 8, 5: 0, 6: 8, 7: 7, 8: 8, 9: 0, 10: 7}
	check := func() {
		t.Helper()
		var got []struct {
			ID        int
			ProjectID *int
		}
		if err := db.Raw("SELECT id, project_id FROM logs ORDER BY id").Scan(&got).Error; err != nil {
			t.Fatal(err)
		}
		for _, row := range got {
			project := 0
			if row.ProjectID != nil {
				project = *row.ProjectID
			}
			if project != want[row.ID] {
				t.Fatalf("log %d: project %d, want %d", row.ID, project, want[row.ID])
			}
		}
	}
	if err := PrefillSchema37(context.Background(), core, db); err != nil {
		t.Fatal(err)
	}
	check()
	// The previous release keeps writing project-less account logs until the
	// stopped step, which backfills them from the prefill's start.
	if err := db.Create(&Log{Id: 11, PublicID: "v37_11", RequestTS: at(time.Now()), CreatedAt: time.Now().Unix(), UserId: 1, Type: LogTypeTopup}).Error; err != nil {
		t.Fatal(err)
	}
	want[11] = 7
	old := coreDB
	coreDB = core
	t.Cleanup(func() { coreDB = old })
	if err := migrateSchema37Telemetry(db); err != nil {
		t.Fatal(err)
	}
	check()
	if err := backfillLogProjectIDs(core, db, math.MinInt64); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	check()
}
