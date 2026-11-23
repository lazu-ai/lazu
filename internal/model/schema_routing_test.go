package model

import (
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRoutingFactModelsMigrate(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:routing_schema?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&RoutingDecision{},
		&RoutingOutcome{},
	); err != nil {
		t.Fatalf("failed to migrate routing fact tables: %v", err)
	}
	for _, table := range []string{
		"routing_decision",
		"routing_outcome",
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected table %s to exist", table)
		}
	}
}
