package model

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func slowQueryTestDB(t *testing.T, threshold time.Duration) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := registerSlowQueryCallbacksAt(db, threshold); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSlowQueryCallbacksRecordStatementsOverThreshold(t *testing.T) {
	DrainSlowQueries()
	db := slowQueryTestDB(t, 0)
	if err := db.Exec("CREATE TABLE things (id INTEGER PRIMARY KEY, name TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		var n int64
		if err := db.Raw("SELECT count(*) FROM things WHERE id = ? AND name = ?", i, "x").Scan(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	var missing struct{ ID int }
	_ = db.Table("things").Where("id = ?", 42).First(&missing).Error
	if err := db.WithContext(WithSlowQueryExpected(context.Background())).Exec("DELETE FROM things").Error; err != nil {
		t.Fatal(err)
	}
	queries, _ := DrainSlowQueries()
	counts := map[string]int{}
	for _, q := range queries {
		counts[q.Fingerprint] = q.Count
		if strings.Contains(q.Fingerprint, "DELETE") {
			t.Fatal("statements marked as expected must not be recorded")
		}
		if strings.Contains(q.Fingerprint, "LIMIT") {
			t.Fatal("a not-found lookup is not a slow statement")
		}
	}
	if counts["SELECT count(*) FROM things WHERE id = ? AND name = ?"] != 3 {
		t.Fatalf("placeholders must fold repeated statements into one shape: %+v", queries)
	}
	if again, _ := DrainSlowQueries(); len(again) != 0 {
		t.Fatal("drain must reset the tracker")
	}
}

func TestSlowQueryCallbacksIgnoreFastStatements(t *testing.T) {
	DrainSlowQueries()
	db := slowQueryTestDB(t, time.Hour)
	if err := db.Exec("SELECT 1").Error; err != nil {
		t.Fatal(err)
	}
	if queries, _ := DrainSlowQueries(); len(queries) != 0 {
		t.Fatalf("fast statements recorded: %+v", queries)
	}
}

func TestSlowQueryTrackerCapsFingerprints(t *testing.T) {
	DrainSlowQueries()
	for i := 0; i < slowQueryMaxFingerprints+5; i++ {
		slowQueries.record("SELECT "+strings.Repeat("x", i+1), time.Second, nil, time.Now())
	}
	queries, dropped := DrainSlowQueries()
	if len(queries) != slowQueryMaxFingerprints || dropped != 5 {
		t.Fatalf("got %d shapes, %d dropped", len(queries), dropped)
	}
}

func TestSlowQueryThresholdFollowsContextBudget(t *testing.T) {
	DrainSlowQueries()
	db := slowQueryTestDB(t, 0)
	if err := db.WithContext(WithSlowQueryThreshold(context.Background(), time.Hour)).Exec("SELECT 1").Error; err != nil {
		t.Fatal(err)
	}
	if queries, _ := DrainSlowQueries(); len(queries) != 0 {
		t.Fatalf("a statement inside its own budget was reported: %+v", queries)
	}
}
