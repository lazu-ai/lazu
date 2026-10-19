package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type coreFixture struct {
	ID    int
	Value string
}

func (coreFixture) TableName() string { return "users" }

type telemetryFixture struct {
	ID    int
	Value string
}

func (telemetryFixture) TableName() string { return "logs" }

func TestOwnershipGuardRejectsORMRawJoinAndTransaction(t *testing.T) {
	registry := ModelRegistry{"users": CoreBoundary, "logs": TelemetryBoundary, "schema_versions": ""}
	for _, owner := range []Boundary{CoreBoundary, TelemetryBoundary} {
		t.Run(string(owner), func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:ownership_%s?mode=memory&cache=shared", owner)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := db.DB()
			t.Cleanup(func() { _ = raw.Close() })
			if err := db.AutoMigrate(&coreFixture{}, &telemetryFixture{}); err != nil {
				t.Fatal(err)
			}
			if err := InstallModelGuard(db, owner, registry); err != nil {
				t.Fatal(err)
			}
			pool := GuardConnection(db.ConnPool, owner, registry)
			db.ConnPool, db.Statement.ConnPool = pool, pool
			good, bad := "users", "logs"
			if owner == TelemetryBoundary {
				good, bad = bad, good
			}
			if err := db.Table(good).Create(map[string]interface{}{"id": 1, "value": "allowed"}).Error; err != nil {
				t.Fatal(err)
			}
			assertBlocked := func(err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), "ownership violation") {
					t.Fatalf("expected ownership error, got %v", err)
				}
			}
			var count int64
			assertBlocked(db.Table(bad).Count(&count).Error)
			assertBlocked(db.Table(bad).Create(map[string]interface{}{"id": 2}).Error)
			assertBlocked(db.Raw("SELECT COUNT(*) FROM " + bad).Scan(&count).Error)
			assertBlocked(db.Exec("UPDATE "+bad+" SET value = ?", "wrong").Error)
			assertBlocked(db.Table(good).Joins("JOIN " + bad + " ON " + bad + ".id=" + good + ".id").Count(&count).Error)
			assertBlocked(db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Table(good).Create(map[string]interface{}{"id": 3}).Error; err != nil {
					return err
				}
				return tx.Exec("INSERT INTO " + bad + " (id) VALUES (3)").Error
			}))
			if err := db.Table(good).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("transaction not rolled back: count=%d err=%v", count, err)
			}
			if returned, err := db.DB(); err != nil || returned != raw {
				t.Fatalf("stable pool lost: %v", err)
			}
		})
	}
}

func TestOwnershipRegistrySQLSyntax(t *testing.T) {
	registry := ModelRegistry{"users": CoreBoundary, "logs": TelemetryBoundary}
	for _, sql := range []string{
		`SELECT * FROM public."logs"`, `DELETE FROM logs`, `INSERT INTO logs(id) VALUES (1)`,
		`SELECT * FROM users JOIN logs ON logs.id=users.id`, `WITH q AS (SELECT * FROM logs) SELECT * FROM q`,
		`UPDATE ONLY logs SET id=2`, `TRUNCATE TABLE logs`,
		`SELECT * FROM LOGS`, `SELECT * FROM users u, public.logs l WHERE u.id=l.id`, `TRUNCATE logs`,
	} {
		if err := registry.CheckSQL(CoreBoundary, sql); err == nil {
			t.Errorf("not rejected: %s", sql)
		}
	}
	for _, sql := range []string{`SELECT 'FROM logs' FROM users`, `SELECT 1 /* JOIN logs */ FROM users -- UPDATE logs`, `SELECT * FROM users`} {
		if err := registry.CheckSQL(CoreBoundary, sql); err != nil {
			t.Errorf("false rejection: %s: %v", sql, err)
		}
	}
}
