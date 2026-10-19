package model

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func withSchemaVersionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	previousDB := coreDB
	coreDB = db
	t.Cleanup(func() { coreDB = previousDB })
	return db
}

func TestRunSchemaMigrationsWithSingleConnectionPool(t *testing.T) {
	db := withSchemaVersionTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open SQL pool: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	done := make(chan error, 1)
	go func() {
		done <- runSchemaMigrations(db, schemaVersionPrimary, func() error {
			return db.Exec("CREATE TABLE single_connection_fixture (id INTEGER PRIMARY KEY)").Error
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run schema migration: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("migration blocked with a single-connection pool")
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("max open connections = %d, want restored value 1", got)
	}
}

func TestRunSchemaMigrationsRecordsCompatibleVersion(t *testing.T) {
	db := withSchemaVersionTestDB(t)
	type fixture struct {
		ID uint `gorm:"primaryKey"`
	}
	if err := runSchemaMigrations(db, schemaVersionPrimary, func() error {
		if err := db.AutoMigrate(append(teamProjectSchemaModels(), &User{}, &Token{}, &File{}, &MediaJob{}, &SpendBudget{}, &SpendBudgetAlert{}, &SpendBudgetReservation{}, &SpendBudgetCharge{}, &TopUp{}, &SettlementOutbox{}, &SettlementFailure{})...); err != nil {
			return err
		}
		return db.AutoMigrate(&fixture{}, &Announcement{})
	}); err != nil {
		t.Fatalf("run schema migration: %v", err)
	}
	if err := ValidateSchemaVersion(); err != nil {
		t.Fatalf("validate schema version: %v", err)
	}
}

func TestRunSchemaMigrationsRefusesToDowngradeNewerVersion(t *testing.T) {
	db := withSchemaVersionTestDB(t)
	if err := db.AutoMigrate(&SchemaVersion{}); err != nil {
		t.Fatalf("create schema version table: %v", err)
	}
	futureVersion := SchemaVersionCurrent + 1
	if err := db.Create(&SchemaVersion{
		Scope:   schemaVersionPrimary,
		Version: futureVersion,
	}).Error; err != nil {
		t.Fatalf("insert future schema version: %v", err)
	}

	migrationRan := false
	err := runSchemaMigrations(db, schemaVersionPrimary, func() error {
		migrationRan = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to downgrade") {
		t.Fatalf("expected downgrade refusal, got %v", err)
	}
	if migrationRan {
		t.Fatal("migration callback ran against a newer schema")
	}

	var record SchemaVersion
	if err := db.Where("scope = ?", schemaVersionPrimary).First(&record).Error; err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if record.Version != futureVersion {
		t.Fatalf("schema version = %d, want preserved version %d", record.Version, futureVersion)
	}
}

func TestValidateSchemaVersionRejectsMissingAndIncompatibleSchemas(t *testing.T) {
	db := withSchemaVersionTestDB(t)
	if err := ValidateSchemaVersion(); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected missing schema error, got %v", err)
	}
	if err := db.AutoMigrate(&SchemaVersion{}); err != nil {
		t.Fatalf("create schema version table: %v", err)
	}
	if err := db.Create(&SchemaVersion{Scope: schemaVersionPrimary, Version: SchemaVersionCurrent + 1}).Error; err != nil {
		t.Fatalf("insert schema version: %v", err)
	}
	if err := ValidateSchemaVersion(); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("expected incompatible schema error, got %v", err)
	}
}

func TestValidateSchemaVersionRejectsPreviousSchema(t *testing.T) {
	db := withSchemaVersionTestDB(t)
	if err := db.AutoMigrate(&SchemaVersion{}); err != nil {
		t.Fatalf("create schema version table: %v", err)
	}
	if err := db.Create(&SchemaVersion{
		Scope:   schemaVersionPrimary,
		Version: SchemaVersionCurrent - 1,
	}).Error; err != nil {
		t.Fatalf("insert previous schema version: %v", err)
	}
	if err := ValidateSchemaVersion(); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("expected previous schema to be incompatible, got %v", err)
	}
}

func TestPR380DefaultMigrationInitializesFreshDatabase(t *testing.T) {
	t.Setenv("LAZU_MIGRATION_MODE", "")
	withSchemaVersionTestDB(t)
	oldPG, oldSQLite := common.UsingPostgreSQL, common.UsingSQLite
	oldGroup, oldKey, oldTrue, oldFalse, oldLogGroup, oldLogKey := commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal, logGroupCol, logKeyCol
	common.UsingPostgreSQL, common.UsingSQLite = false, true
	initCol()
	t.Cleanup(func() {
		common.UsingPostgreSQL, common.UsingSQLite = oldPG, oldSQLite
		commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal, logGroupCol, logKeyCol = oldGroup, oldKey, oldTrue, oldFalse, oldLogGroup, oldLogKey
	})
	if err := applyPrimarySchemaPolicy(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchemaVersion(); err != nil {
		t.Fatal(err)
	}
}
