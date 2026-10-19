package model

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func TestInitDBLoadsPricingSnapshotForAPIProcess(t *testing.T) {
	originalDB := coreDB
	originalIsMasterNode := common.IsMasterNode
	originalPricingSnapshotEnabled := common.PricingSnapshotEnabled
	originalSQLitePath := common.SQLitePath
	originalUsingPostgreSQL := common.UsingPostgreSQL
	originalUsingSQLite := common.UsingSQLite
	originalActivePricingSnapshot := activePricingSnapshot.Load()
	t.Cleanup(func() {
		if coreDB != nil && coreDB != originalDB {
			sqlDB, _ := coreDB.DB()
			_ = sqlDB.Close()
		}
		coreDB = originalDB
		common.IsMasterNode = originalIsMasterNode
		common.PricingSnapshotEnabled = originalPricingSnapshotEnabled
		common.SQLitePath = originalSQLitePath
		common.UsingPostgreSQL = originalUsingPostgreSQL
		common.UsingSQLite = originalUsingSQLite
		activePricingSnapshot = atomic.Value{}
		if originalActivePricingSnapshot != nil {
			activePricingSnapshot.Store(originalActivePricingSnapshot)
		}
		pricingSnapshotOnce = sync.Once{}
	})

	databasePath := filepath.Join(t.TempDir(), "api-pricing.db") + "?_busy_timeout=30000"
	seedDB, err := gorm.Open(gormsqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open seed database: %v", err)
	}
	if err := seedDB.AutoMigrate(&SchemaVersion{}, &Announcement{}, &ModelSellPrice{}, &User{}, &Token{}, &File{}, &MediaJob{}, &SpendBudget{}, &SpendBudgetAlert{}, &SpendBudgetReservation{}, &SpendBudgetCharge{}, &TopUp{}, &SettlementOutbox{}, &SettlementFailure{}); err != nil {
		t.Fatalf("failed to migrate seed database: %v", err)
	}
	if err := seedDB.AutoMigrate(teamProjectSchemaModels()...); err != nil {
		t.Fatal(err)
	}
	if err := seedDB.Create(&SchemaVersion{Scope: schemaVersionPrimary, Version: SchemaVersionCurrent}).Error; err != nil {
		t.Fatalf("failed to seed schema version: %v", err)
	}
	inputPrice := 1.25
	if err := seedDB.Create(&ModelSellPrice{
		ModelName:      "api-snapshot-model",
		ChannelGroup:   ChannelLaneDirect,
		InputPerMtok:   &inputPrice,
		Active:         true,
		PricingVersion: 1,
	}).Error; err != nil {
		t.Fatalf("failed to seed model sell price: %v", err)
	}
	seedSQLDB, err := seedDB.DB()
	if err != nil {
		t.Fatalf("failed to access seed database: %v", err)
	}
	if err := seedSQLDB.Close(); err != nil {
		t.Fatalf("failed to close seed database: %v", err)
	}

	common.SQLitePath = databasePath
	common.UsingPostgreSQL = false
	common.UsingSQLite = false
	common.IsMasterNode = false
	common.PricingSnapshotEnabled = true
	activePricingSnapshot = atomic.Value{}
	pricingSnapshotOnce = sync.Once{}
	pricingSnapshotOnce.Do(func() {}) // Suppress the background ticker in this startup test.
	t.Setenv("DATABASE_URL", "local")
	t.Setenv("LAZU_MIGRATION_MODE", "validate")

	if err := InitDB(); err != nil {
		t.Fatalf("InitDB failed for API process: %v", err)
	}
	price, ok := getSnapshotActiveModelSellPrice("api-snapshot-model", ChannelLaneDirect)
	if !ok || price.InputPerMtok == nil || *price.InputPerMtok != inputPrice {
		t.Fatalf("expected API process pricing snapshot to load seeded price, got=%+v ok=%v", price, ok)
	}
}

func TestResolvePrimaryDatabaseDSNPriority(t *testing.T) {
	originalConfig := common.AppConfig
	t.Cleanup(func() {
		common.AppConfig = originalConfig
	})

	common.AppConfig = common.AppConfigStruct{}
	common.AppConfig.Database.URL = "postgresql://yaml/database"
	t.Setenv("DATABASE_URL", "postgresql://env/database")
	t.Setenv("SQL_DSN", "postgresql://legacy/database")

	dsn, source := resolvePrimaryDatabaseDSN()
	if dsn != "postgresql://env/database" || source != "DATABASE_URL" {
		t.Fatalf("expected DATABASE_URL to win, got dsn=%q source=%q", dsn, source)
	}
}

func TestResolvePrimaryDatabaseDSNFallsBackToSQLDSN(t *testing.T) {
	originalConfig := common.AppConfig
	t.Cleanup(func() {
		common.AppConfig = originalConfig
	})

	common.AppConfig = common.AppConfigStruct{}
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SQL_DSN", "postgresql://legacy/database")

	dsn, source := resolvePrimaryDatabaseDSN()
	if dsn != "postgresql://legacy/database" || source != "SQL_DSN" {
		t.Fatalf("expected SQL_DSN fallback, got dsn=%q source=%q", dsn, source)
	}
}

func TestResolvePrimaryDatabaseDSNUsesYAMLBeforeSQLDSN(t *testing.T) {
	originalConfig := common.AppConfig
	t.Cleanup(func() {
		common.AppConfig = originalConfig
	})

	common.AppConfig = common.AppConfigStruct{}
	common.AppConfig.Database.URL = "postgresql://yaml/database"
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SQL_DSN", "postgresql://legacy/database")

	dsn, source := resolvePrimaryDatabaseDSN()
	if dsn != "postgresql://yaml/database" || source != "config.database.url" {
		t.Fatalf("expected yaml database url before SQL_DSN, got dsn=%q source=%q", dsn, source)
	}
}

func TestResolveSQLPoolConfigUsesRoleOverrideBeforeGlobal(t *testing.T) {
	clearSQLPoolEnv(t)
	t.Setenv("LAZU_DB_POOL_ROLE", "worker")
	t.Setenv("WORKER_SQL_MAX_OPEN_CONNS", "7")
	t.Setenv("WORKER_SQL_MAX_IDLE_CONNS", "2")
	t.Setenv("WORKER_SQL_MAX_LIFETIME", "45")
	t.Setenv("SQL_MAX_OPEN_CONNS", "99")

	config := resolveSQLPoolConfig("primary")
	if config.Role != sqlPoolRoleWorker {
		t.Fatalf("expected worker role, got %q", config.Role)
	}
	if config.MaxOpenConnections != 7 || config.MaxIdleConnections != 2 || config.MaxLifetimeSeconds != 45 {
		t.Fatalf("unexpected worker pool config: %#v", config)
	}
}

func TestResolveSQLPoolConfigUsesLogOverrideFirst(t *testing.T) {
	clearSQLPoolEnv(t)
	t.Setenv("LAZU_DB_POOL_ROLE", "api")
	t.Setenv("LOG_SQL_MAX_OPEN_CONNS", "4")
	t.Setenv("API_SQL_MAX_OPEN_CONNS", "12")
	t.Setenv("SQL_MAX_OPEN_CONNS", "99")

	config := resolveSQLPoolConfig("log")
	if config.MaxOpenConnections != 4 {
		t.Fatalf("expected log override to win, got %#v", config)
	}
}

func clearSQLPoolEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LAZU_DB_POOL_ROLE", "")
	for _, prefix := range []string{"API_SQL", "WORKER_SQL", "LOG_SQL", "SQL"} {
		for _, suffix := range []string{"MAX_OPEN_CONNS", "MAX_IDLE_CONNS", "MAX_LIFETIME"} {
			t.Setenv(prefix+"_"+suffix, "")
		}
	}
}

func TestMigrateDBOmitsRemovedAuditTables(t *testing.T) {
	originalDB := coreDB
	originalLOGDB := telemetryDB
	originalUsingPostgreSQL := common.UsingPostgreSQL
	originalUsingSQLite := common.UsingSQLite
	originalLogSqlType := common.LogSqlType
	originalCommonGroupCol := commonGroupCol
	originalCommonKeyCol := commonKeyCol
	originalCommonTrueVal := commonTrueVal
	originalCommonFalseVal := commonFalseVal
	originalLogGroupCol := logGroupCol
	originalLogKeyCol := logKeyCol
	t.Cleanup(func() {
		coreDB = originalDB
		telemetryDB = originalLOGDB
		common.UsingPostgreSQL = originalUsingPostgreSQL
		common.UsingSQLite = originalUsingSQLite
		common.LogSqlType = originalLogSqlType
		commonGroupCol = originalCommonGroupCol
		commonKeyCol = originalCommonKeyCol
		commonTrueVal = originalCommonTrueVal
		commonFalseVal = originalCommonFalseVal
		logGroupCol = originalLogGroupCol
		logKeyCol = originalLogKeyCol
	})

	db, err := gorm.Open(gormsqlite.Open("file:migrate_no_removed_audit_tables?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("failed to close sqlite db: %v", err)
		}
	})

	coreDB = db
	telemetryDB = db
	common.UsingPostgreSQL = false
	common.UsingSQLite = true
	common.LogSqlType = common.DatabaseTypeSQLite
	initCol()

	if err := migrateDB(); err != nil {
		t.Fatalf("migrateDB failed: %v", err)
	}
	if db.Migrator().HasTable(&Log{}) {
		t.Fatal("telemetry logs must not be created by core migration")
	}
	for _, table := range removedAuditTableNames() {
		if db.Migrator().HasTable(table) {
			t.Fatalf("%s should not be created by migrateDB", table)
		}
	}
}

func TestMigrateLOGDBOmitsRemovedAuditTables(t *testing.T) {
	originalCore := coreDB
	t.Cleanup(func() { coreDB = originalCore })
	originalLOGDB := telemetryDB
	originalLogSqlType := common.LogSqlType
	t.Cleanup(func() {
		telemetryDB = originalLOGDB
		common.LogSqlType = originalLogSqlType
	})

	db, err := gorm.Open(gormsqlite.Open("file:migrate_log_db_no_removed_audit_tables?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("failed to close sqlite db: %v", err)
		}
	})

	telemetryDB = db
	coreDB = db
	common.LogSqlType = common.DatabaseTypeSQLite
	EnsureColumnNamesInitialized()

	if err := RunPrimaryMigrations(); err != nil {
		t.Fatal(err)
	}
	InitOptionMap()
	if err := RunLogMigrations(); err != nil {
		t.Fatalf("migrateLOGDB failed: %v", err)
	}
	for _, model := range []struct {
		table any
		name  string
	}{
		{&Log{}, "logs"},
	} {
		if !db.Migrator().HasTable(model.table) {
			t.Fatalf("%s table should be created by migrateLOGDB", model.name)
		}
	}
	for _, table := range removedAuditTableNames() {
		if db.Migrator().HasTable(table) {
			t.Fatalf("%s should not be created by migrateLOGDB", table)
		}
	}
}

func TestMigrateUserHasToppedUpFromSuccessfulTopUps(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:migrate_has_topped_up?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("failed to close sqlite db: %v", err)
		}
	})

	if err := db.AutoMigrate(&User{}, &TopUp{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	if err := db.Create([]User{
		{Id: 1, Username: "paid"},
		{Id: 2, Username: "pending"},
		{Id: 3, Username: "already-paid", HasToppedUp: true},
	}).Error; err != nil {
		t.Fatalf("failed to seed users: %v", err)
	}
	if err := db.Create([]TopUp{
		{UserId: 1, TradeNo: "success-1", Status: common.TopUpStatusSuccess},
		{UserId: 2, TradeNo: "pending-2", Status: common.TopUpStatusPending},
		{UserId: 3, TradeNo: "success-3", Status: common.TopUpStatusSuccess},
	}).Error; err != nil {
		t.Fatalf("failed to seed topups: %v", err)
	}

	if err := migrateUserHasToppedUpFromTopUps(db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	var users []User
	if err := db.Order("id asc").Find(&users).Error; err != nil {
		t.Fatalf("failed to load users: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("expected 3 users, got %d", len(users))
	}
	if !users[0].HasToppedUp {
		t.Fatal("expected successful historical topup to mark user as topped up")
	}
	if users[1].HasToppedUp {
		t.Fatal("expected pending historical topup to stay unverified")
	}
	if !users[2].HasToppedUp {
		t.Fatal("expected existing topped-up flag to remain true")
	}
}

func removedAuditTableNames() []string {
	return []string{
		strings.Join([]string{"request", "logs"}, "_"),
		strings.Join([]string{"request", "log", "payloads"}, "_"),
	}
}
