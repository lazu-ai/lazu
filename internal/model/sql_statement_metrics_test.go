package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSQLStatementCountersCountEachChain(t *testing.T) {
	db, err := openWithSlowQueryCallbacks(gorm.Open(sqlite.Open(":memory:"), &gorm.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	type probe struct {
		ID   int
		Name string
	}
	if err := db.AutoMigrate(&probe{}); err != nil {
		t.Fatal(err)
	}
	before := SQLStatementCounts()
	db.Create(&probe{Name: "a"})
	var rows []probe
	db.Find(&rows)
	db.Model(&probe{}).Where("id = ?", 1).Update("name", "b")
	db.Where("id = ?", 1).Delete(&probe{})
	var count int64
	db.Raw("SELECT count(*) FROM probes").Scan(&count)
	after := SQLStatementCounts()
	for i, op := range SQLStatementOps {
		delta := after[i] - before[i]
		switch op {
		case "create", "query", "update", "delete":
			if delta != 1 {
				t.Errorf("%s: want 1 statement, got %d", op, delta)
			}
		}
	}
	if raw := after[5] - before[5] + after[4] - before[4]; raw != 1 {
		t.Errorf("raw scan: want 1 statement across row and raw, got %d", raw)
	}
}
