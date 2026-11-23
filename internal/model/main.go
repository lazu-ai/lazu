package model

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

var commonGroupCol string
var commonKeyCol string
var commonTrueVal string
var commonFalseVal string

var logKeyCol string
var logGroupCol string

func initCol() {
	// init common column names
	if common.UsingPostgreSQL {
		commonGroupCol = `"group"`
		commonKeyCol = `"key"`
		commonTrueVal = "true"
		commonFalseVal = "false"
	} else {
		commonGroupCol = "`group`"
		commonKeyCol = "`key`"
		commonTrueVal = "1"
		commonFalseVal = "0"
	}
	if os.Getenv("LOG_SQL_DSN") != "" {
		switch common.LogSqlType {
		case common.DatabaseTypePostgreSQL:
			logGroupCol = `"group"`
			logKeyCol = `"key"`
		default:
			logGroupCol = commonGroupCol
			logKeyCol = commonKeyCol
		}
	} else {
		// LOG_SQL_DSN 为空时，日志数据库与主数据库相同
		if common.UsingPostgreSQL {
			logGroupCol = `"group"`
			logKeyCol = `"key"`
		} else {
			logGroupCol = commonGroupCol
			logKeyCol = commonKeyCol
		}
	}
	// log sql type and database type
	//common.SysLog("Using Log SQL Type: " + common.LogSqlType)
}

// EnsureColumnNamesInitialized is useful in tests or auxiliary setups that
// replace the core store without going through InitDB/chooseDB.
func EnsureColumnNamesInitialized() {
	if commonGroupCol != "" && commonKeyCol != "" && logGroupCol != "" && logKeyCol != "" {
		return
	}
	initCol()
}

var coreDB *gorm.DB

var telemetryDB *gorm.DB

const (
	sqlPoolRoleAPI    = "api"
	sqlPoolRoleWorker = "worker"
)

type SQLPoolConfig struct {
	Role               string `json:"role"`
	Scope              string `json:"scope"`
	MaxIdleConnections int    `json:"max_idle_connections"`
	MaxOpenConnections int    `json:"max_open_connections"`
	MaxLifetimeSeconds int    `json:"max_lifetime_seconds"`
}

type SQLPoolStats struct {
	SQLPoolConfig
	OpenConnections   int     `json:"open_connections"`
	InUse             int     `json:"in_use"`
	Idle              int     `json:"idle"`
	WaitCount         int64   `json:"wait_count"`
	WaitDurationMS    float64 `json:"wait_duration_ms"`
	MaxIdleClosed     int64   `json:"max_idle_closed"`
	MaxIdleTimeClosed int64   `json:"max_idle_time_closed"`
	MaxLifetimeClosed int64   `json:"max_lifetime_closed"`
}

func gormOpenConfig() *gorm.Config {
	return &gorm.Config{
		PrepareStmt: false,
		Logger:      gormLogger(),
	}
}

func openWithSlowQueryCallbacks(db *gorm.DB, err error) (*gorm.DB, error) {
	if err != nil {
		return db, err
	}
	if err := registerSlowQueryCallbacks(db); err != nil {
		return db, err
	}
	return db, registerSQLStatementCounters(db)
}

func gormLogger() gormlogger.Interface {
	level := gormLogLevel()
	thresholdMs := common.GetEnvOrDefault("GORM_SLOW_THRESHOLD_MS", 1000)
	if thresholdMs <= 0 {
		thresholdMs = 1000
	}
	return gormlogger.New(log.New(gin.DefaultWriter, "\r\n", log.LstdFlags), gormlogger.Config{
		SlowThreshold:             time.Duration(thresholdMs) * time.Millisecond,
		LogLevel:                  level,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

func gormLogLevel() gormlogger.LogLevel {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GORM_LOG_LEVEL"))) {
	case "silent", "off", "none":
		return gormlogger.Silent
	case "error":
		return gormlogger.Error
	case "warn", "warning":
		return gormlogger.Warn
	case "info", "debug":
		return gormlogger.Info
	}
	if !common.ShouldLog(common.LogLevelInfo) {
		return gormlogger.Error
	}
	return gormlogger.Warn
}

func chooseDB(envName string, isLog bool) (*gorm.DB, error) {
	defer func() {
		initCol()
	}()
	dsn := os.Getenv(envName)
	if envName == "TELEMETRY_DATABASE_URL" {
		dsn = telemetryDatabaseDSN()
	}
	dsnSource := envName
	if envName == "DATABASE_URL" {
		dsn, dsnSource = resolvePrimaryDatabaseDSN()
		if dsnSource == "SQL_DSN" {
			common.SysWarn("[config] SQL_DSN is deprecated for primary database; set DATABASE_URL instead")
		}
	}
	if dsn != "" {
		if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			// Use PostgreSQL
			common.SysDebug("using PostgreSQL as database")
			if !isLog {
				common.UsingPostgreSQL = true
			} else {
				common.LogSqlType = common.DatabaseTypePostgreSQL
			}
			postgresDialector := postgres.New(postgres.Config{
				DSN:                  dsn,
				PreferSimpleProtocol: true, // disables implicit prepared statement usage
			}).(*postgres.Dialector)
			return openWithSlowQueryCallbacks(gorm.Open(safePostgresDialector{Dialector: postgresDialector}, gormOpenConfig()))
		}
		if strings.HasPrefix(dsn, "local") {
			common.SysDebug(envName + " explicitly set to local, using SQLite as database")
			if !isLog {
				common.UsingSQLite = true
			} else {
				common.LogSqlType = common.DatabaseTypeSQLite
			}
			return openWithSlowQueryCallbacks(gorm.Open(sqlite.Open(common.SQLitePath), gormOpenConfig()))
		}
		return nil, fmt.Errorf("%s must use a PostgreSQL URL or the explicit local SQLite value", envName)
	}
	return nil, fmt.Errorf("%s not set", envName)
}

func resolvePrimaryDatabaseDSN() (string, string) {
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_URL")); dsn != "" {
		return dsn, "DATABASE_URL"
	}
	if dsn := strings.TrimSpace(common.AppConfig.Database.URL); dsn != "" {
		return dsn, "config.database.url"
	}
	if dsn := strings.TrimSpace(os.Getenv("SQL_DSN")); dsn != "" {
		return dsn, "SQL_DSN"
	}
	return "", ""
}

func sqlPoolRole() string {
	if role := strings.ToLower(strings.TrimSpace(os.Getenv("LAZU_DB_POOL_ROLE"))); role == sqlPoolRoleAPI || role == sqlPoolRoleWorker {
		return role
	}
	name := strings.ToLower(filepath.Base(os.Args[0]))
	if strings.Contains(name, "worker") {
		return sqlPoolRoleWorker
	}
	return sqlPoolRoleAPI
}

func resolveSQLPoolConfig(scope string) SQLPoolConfig {
	role := sqlPoolRole()
	return SQLPoolConfig{
		Role:               role,
		Scope:              scope,
		MaxIdleConnections: sqlPoolEnvInt(role, scope, "MAX_IDLE_CONNS", 10),
		MaxOpenConnections: sqlPoolEnvInt(role, scope, "MAX_OPEN_CONNS", 100),
		MaxLifetimeSeconds: sqlPoolEnvInt(role, scope, "MAX_LIFETIME", 60),
	}
}

func sqlPoolEnvInt(role string, scope string, suffix string, defaultValue int) int {
	for _, envName := range sqlPoolEnvNames(role, scope, suffix) {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			return common.GetEnvOrDefault(envName, defaultValue)
		}
	}
	return defaultValue
}

func sqlPoolEnvNames(role string, scope string, suffix string) []string {
	names := make([]string, 0, 4)
	if scope == "log" {
		names = append(names, "LOG_SQL_"+suffix)
	}
	if role == sqlPoolRoleWorker {
		names = append(names, "WORKER_SQL_"+suffix)
	} else {
		names = append(names, "API_SQL_"+suffix)
	}
	names = append(names, "SQL_"+suffix)
	return names
}

func configureSQLPool(sqlDB *sql.DB, scope string) SQLPoolConfig {
	config := resolveSQLPoolConfig(scope)
	if config.MaxOpenConnections > 0 && config.MaxIdleConnections > config.MaxOpenConnections {
		config.MaxIdleConnections = config.MaxOpenConnections
	}
	sqlDB.SetMaxIdleConns(config.MaxIdleConnections)
	sqlDB.SetMaxOpenConns(config.MaxOpenConnections)
	sqlDB.SetConnMaxLifetime(time.Second * time.Duration(config.MaxLifetimeSeconds))
	common.SysLog(fmt.Sprintf(
		"configured %s SQL pool role=%s max_open=%d max_idle=%d max_lifetime=%ds",
		scope,
		config.Role,
		config.MaxOpenConnections,
		config.MaxIdleConnections,
		config.MaxLifetimeSeconds,
	))
	return config
}

// InitDBForMigration opens Core for an explicit, offline schema migration.
// Runtime writers must be stopped; this handle must never serve API or jobs.
func InitDBForMigration() error {
	return initDB(false)
}

func InitDB() error {
	return initDB(true)
}

func initDB(runtime bool) (err error) {
	db, err := chooseDB("DATABASE_URL", false)
	if err == nil {
		if common.DebugEnabled {
			db = db.Debug()
		}
		coreDB = db
		sqlDB, err := coreDB.DB()
		if err != nil {
			return err
		}
		configureSQLPool(sqlDB, "primary")
		if coreDB.Migrator().HasTable(&SettlementOutbox{}) {
			var pending bool
			if err := coreDB.Raw("SELECT EXISTS (SELECT 1 FROM settlement_outboxes WHERE status <> ?)", SettlementFailureStatusResolved).Scan(&pending).Error; err != nil {
				return err
			}
			if pending {
				return fmt.Errorf("settlement outbox still has unresolved entries; finish reconciliation on %s before upgrading", schemaUpgradeGateRelease)
			}
		}

		if !common.IsMasterNode {
			if err = ValidateSchemaVersion(); err != nil {
				return err
			}
		} else {
			common.SysDebug("database schema policy started")
			err = applyPrimarySchemaPolicy()
			if err != nil {
				return err
			}
		}
		if runtime {
			if err := InstallIdentityMutationFence(coreDB); err != nil {
				return err
			}
			InitPricingSnapshot()
		}
		return nil
	} else {
		common.FatalLog(err)
	}
	return err
}

func InitLogDB() (err error) {

	if telemetryDatabaseDSN() == "" {
		telemetryDB = coreDB
		if common.IsMasterNode {
			return applyLogSchemaPolicy()
		}
		return validateSchemaVersion(telemetryDB, schemaVersionLog)
	}
	db, err := chooseDB("TELEMETRY_DATABASE_URL", true)
	if err == nil {
		if common.DebugEnabled {
			db = db.Debug()
		}
		telemetryDB = db
		sqlDB, err := telemetryDB.DB()
		if err != nil {
			return err
		}
		configureSQLPool(sqlDB, "log")

		if !common.IsMasterNode {
			return validateSchemaVersion(telemetryDB, schemaVersionLog)
		}
		common.SysDebug("log database schema policy started")
		err = applyLogSchemaPolicy()
		return err
	} else {
		common.FatalLog(err)
	}
	return err
}

func migrateDB() error {
	err := coreDB.AutoMigrate(append([]interface{}{
		&Channel{},
		&Token{},
		&User{}, &TelemetryCleanupJob{},
		&UserIdentity{},
		&SignupAttribution{},
		&Project{},
		&ProjectMember{},
		&ProjectInvite{},
		&BudgetRequest{},
		&ProjectRemoval{},
		&ProjectRemovalKey{},
		&ProjectBudgetHistory{}, &ProjectNotification{}, &TokenCredential{}, &TokenRotation{},
		&Option{},
		&Ability{},
		&RouteHealthState{},
		&AdminAuditLog{},
		&AccountDeletion{},
		&OptionAuditLog{},
		&TokenAuditLog{},

		&TopUp{},
		&ProjectAutoReload{},

		&Model{},
		&ModelInfoReview{},
		&ModelMetadata{},
		&Vendor{},
		&PrefillGroup{},
		&ProviderCostPrice{},
		&ModelSellPrice{},
		&PricePlan{},
		&PricePlanRule{},
		&WalletLedger{},
		&SettlementFailure{},
		&SettlementOutbox{},
		&DecisionGenerationRefund{},
		&SpendBudget{},
		&SpendBudgetAlert{},
		&SpendBudgetReservation{},
		&SpendBudgetCharge{}, &ProjectSpend{}, &ProjectDataMigration{},
		&EphemeralCredential{},
		&EphemeralCredentialAudit{},
		&MediaJob{},
		&Announcement{},
		&AnnouncementUserState{},
		&ReleaseDeployment{},
		&File{},

		&ChannelProbeBudget{},
		&ChannelProbeClaim{},
		&RouteObservation{},
		&RouteObservationCheck{},
	}, append(RedisLedgerModels(), extensions.coreModels...)...)...)
	if err != nil {
		return err
	}

	if err = migrateModelReleaseDates(coreDB); err != nil {
		return err
	}
	if err = migrateOfficialPricesToPerMillion(coreDB); err != nil {
		return err
	}
	if err = dropRetiredTierColumns(coreDB); err != nil {
		return err
	}
	if err = migrateFileStorageAccounting(coreDB); err != nil {
		return err
	}
	if err = dropRetiredPricingAdjustments(coreDB); err != nil {
		return err
	}

	if err = backfillDeepSeekDialectOnce(coreDB); err != nil {
		return err
	}

	if err = migrateQuotaRemindThreshold(coreDB); err != nil {
		return err
	}

	if err = deleteLegacyReferralOptions(coreDB); err != nil {
		return err
	}
	if err = deleteLegacyAutoGroupOptions(coreDB); err != nil {
		return err
	}
	if err = migrateRetiredUserRoles(coreDB); err != nil {
		return err
	}

	if err = migrateChannelKeysToEncrypted(coreDB); err != nil {
		return err
	}

	audit, err := readProjectMigrationAudit(coreDB)
	if err != nil {
		return err
	}
	if err := audit.Validate(); err != nil {
		return err
	}
	if err = runExtensionMigrations(coreDB); err != nil {
		return err
	}
	// Claude rows registered before Mode/Off existed take the seed table's
	// values once; the catalog row is the only source after that. Before the
	// schema_versions table exists (a database older than the marker, or a
	// fresh one with no rows) the fill runs unrecorded; it only writes blanks.
	if err = backfillClaudeReasoningPolicyOnce(coreDB); err != nil {
		return err
	}
	if err = migrateSchema33Core(coreDB); err != nil {
		return err
	}

	return coreDB.Exec("CREATE INDEX IF NOT EXISTS idx_wallet_ledgers_account_events ON wallet_ledgers (user_id, created_at DESC, id DESC) WHERE kind <> 'spend'").Error
}

func telemetryDatabaseDSN() string {
	if dsn := strings.TrimSpace(os.Getenv("TELEMETRY_DATABASE_URL")); dsn != "" {
		return dsn
	}
	return strings.TrimSpace(os.Getenv("LOG_SQL_DSN"))
}

func migrateLOGDB() error {
	if err := migrateTelemetrySchema(telemetryDB); err != nil {
		return err
	}
	if err := runOneTimeMigration(telemetryDB, "telemetry_copy_core_v24", func() error { return CopyTelemetryHistory(context.Background(), coreDB, telemetryDB) }); err != nil {
		return err
	}
	if err := migrateLazuLogPublicIDs(telemetryDB); err != nil {
		return err
	}

	if err := runOneTimeMigration(coreDB, "telemetry_lifecycle_v24", func() error {
		if err := backfillLifecycleFromLogs(coreDB, telemetryDB); err != nil {
			return err
		}
		return MigrateProjectSpendHistory()
	}); err != nil {
		return err
	}
	if err := runOneTimeMigration(telemetryDB, "telemetry_sessions_v24", func() error { return backfillAgentSessionStats(telemetryDB, telemetryDB) }); err != nil {
		return err
	}
	if err := runOneTimeMigration(telemetryDB, "telemetry_digest_v24", func() error { return backfillDigestRollupForMigration(time.Now().UTC()) }); err != nil {
		return err
	}
	if err := backfillIncrementalTelemetry(telemetryDB); err != nil {
		return err
	}
	if err := RefreshTelemetryChannelProjection(context.Background()); err != nil {
		return err
	}
	if err := runOneTimeMigration(telemetryDB, "telemetry_hourly_v24", func() error {
		// CopyTelemetryHistory preserves IDs without advancing PostgreSQL
		// sequences. Rebuilding one hour retains other hours, so its generated
		// IDs must start beyond all copied rows, including on a schema retry
		// after the copy marker has already committed. Preserve an already
		// advanced sequence, even if its generated rows have since been deleted.
		if telemetryDB.Dialector.Name() == "postgres" {
			for _, table := range []string{"dashboard_usage_hourly_totals", "dashboard_usage_hourly_dimensions"} {
				if err := telemetryDB.Exec(`WITH sequence_state AS (
					SELECT pg_get_serial_sequence(?, 'id')::regclass AS sequence_id
				), row_state AS (
					SELECT MAX(id) AS max_id, COUNT(*) > 0 AS populated FROM `+table+`
				)
				SELECT setval(sequence_id,
					GREATEST(COALESCE(max_id,1),COALESCE(pg_sequence_last_value(sequence_id),1)),
					populated OR pg_sequence_last_value(sequence_id) IS NOT NULL)
				FROM sequence_state CROSS JOIN row_state`, table).Error; err != nil {
					return fmt.Errorf("reset copied %s sequence: %w", table, err)
				}
			}
		}
		var first struct{ Min *int64 }
		if err := telemetryDB.Model(&Log{}).Select("MIN(created_at) AS min").Scan(&first).Error; err != nil {
			return err
		}
		if first.Min == nil {
			return nil
		}
		start := time.Unix(*first.Min, 0).UTC()
		cutoff := time.Now().UTC().AddDate(0, 0, -common.LogRetentionDays)
		if start.Before(cutoff) {
			start = cutoff
		}
		return RefreshDashboardUsageRollups(context.Background(), start, time.Now().UTC())
	}); err != nil {
		return err
	}
	if err := migrateTelemetryPartitions(telemetryDB); err != nil {
		return err
	}
	if err := migrateRoutingFactColumns(telemetryDB); err != nil {
		return err
	}
	if err := migrateRouteHealthRollup(telemetryDB); err != nil {
		return err
	}
	if err := migrateGatewayTimingColumns(telemetryDB); err != nil {
		return err
	}
	if err := migrateSchema33Telemetry(telemetryDB); err != nil {
		return err
	}
	if err := migrateSchema34Telemetry(telemetryDB); err != nil {
		return err
	}
	return migrateSchema37Telemetry(telemetryDB)
}

var legacyMediaOptionKeys = []string{
	"FileUploadPermission",
	"FileDownloadPermission",
	"ImageUploadPermission",
	"ImageDownloadPermission",
}

var legacyAuthOptionKeys = []string{
	"GitHubClientId",
	"GitHubClientSecret",
	"GitHubOAuthEnabled",
	// The removed GitHub option surface must not carry an old false value into
	// the current password + OIDC + social-provider authentication model.
	"PasswordLoginEnabled",
}

var legacyReferralOptionKeys = []string{
	"QuotaForInviter",
	"QuotaForInvitee",
}

func deleteLegacyReferralOptions(db *gorm.DB) error {
	return deleteLegacyOptionsByKey(db, legacyReferralOptionKeys)
}

// legacyAutoGroupOptionKeys belong to the inherited "auto group" fallback chain,
// which lanes (direct / cheap) replaced.
var legacyAutoGroupOptionKeys = []string{
	"AutoGroups",
	"DefaultUseAutoGroup",
	// The spend-tier registry; price plans and account rate limits replaced it.
	"TierThresholds",
	// Per-lane request limits; account rate limits replaced them.
	"ModelRequestRateLimitGroup",
}

func deleteLegacyAutoGroupOptions(db *gorm.DB) error {
	return deleteLegacyOptionsByKey(db, legacyAutoGroupOptionKeys)
}

// retiredUserRoles maps the inherited roles Lazu no longer has onto the two it
// keeps: guest (0) becomes a normal user (1), and the old admin tier (10)
// becomes an administrator (100).
var retiredUserRoles = map[int]int{
	0:  common.RoleCommonUser,
	10: common.RoleRootUser,
}

// migrateRetiredUserRoles rewrites retired role values in place. It is
// idempotent: once no row carries a retired value it changes nothing. Each
// rewritten row's session_version moves so a session minted under the old
// role is re-authenticated against the new one.
func migrateRetiredUserRoles(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&User{}) {
		return nil
	}
	for from, to := range retiredUserRoles {
		var ids []int
		if err := db.Unscoped().Model(&User{}).Where("role = ?", from).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			continue
		}
		if err := db.Unscoped().Model(&User{}).Where("id IN ? AND role = ?", ids, from).Updates(map[string]any{
			"role":            to,
			"session_version": gorm.Expr("session_version + ?", 1),
		}).Error; err != nil {
			return err
		}
		common.SysLog(fmt.Sprintf("migrated %d user(s) from retired role %d to role %d", len(ids), from, to))
		for _, id := range ids {
			if err := invalidateUserCache(id); err != nil {
				common.SysLog(fmt.Sprintf("failed to invalidate user cache for user %d: %s", id, err.Error()))
			}
		}
	}
	return nil
}

func deleteLegacyOptionsByKey(db *gorm.DB, keys []string) error {
	if db == nil || !db.Migrator().HasTable(&Option{}) {
		return nil
	}
	values := make([]interface{}, 0, len(keys))
	for _, key := range keys {
		values = append(values, key)
	}
	if len(values) == 0 {
		return nil
	}
	return db.Clauses(clause.Where{Exprs: []clause.Expression{
		clause.IN{
			Column: clause.Column{Name: "key"},
			Values: values,
		},
	}}).Delete(&Option{}).Error
}

func quoteSQLIdentifier(identifier string, quote string) string {
	return quote + strings.ReplaceAll(identifier, quote, quote+quote) + quote
}

func dropLegacyColumn(db *gorm.DB, table string, column string) error {
	if db == nil {
		return nil
	}
	// Run the schema probe + ALTER through a session that disables GORM's
	// prepared-statement cache. Reusing a cached SELECT plan across an
	// ALTER TABLE on the same connection makes PostgreSQL fail the next
	// schema probe with "cached plan must not change result type"
	// (SQLSTATE 0A000). Each call gets a clean session, so back-to-back
	// drops on the same table no longer share a stale plan.
	//
	// Use raw SQL for the ALTER itself — Migrator().DropColumn() needs a
	// struct value to parse stmt.Schema; passing a string puts Schema=nil
	// and panics on a nil pointer deref inside gorm.io/driver/postgres.
	session := db.Session(&gorm.Session{PrepareStmt: false, NewDB: true})
	if !session.Migrator().HasTable(table) {
		return nil
	}
	if !session.Migrator().HasColumn(table, column) {
		return nil
	}
	quote := `"`
	return session.Exec(fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s",
		quoteSQLIdentifier(table, quote),
		quoteSQLIdentifier(column, quote))).Error
}

func closeDB(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	err = sqlDB.Close()
	return err
}

func CloseDB() error {
	if telemetryDB != coreDB {
		err := closeDB(telemetryDB)
		if err != nil {
			return err
		}
	}
	return closeDB(coreDB)
}

var (
	lastPingTime time.Time
	pingMutex    sync.Mutex
)

func PingDB() error {
	pingMutex.Lock()
	defer pingMutex.Unlock()

	if time.Since(lastPingTime) < time.Second*10 {
		return nil
	}

	sqlDB, err := coreDB.DB()
	if err != nil {
		log.Printf("Error getting sql.DB from GORM: %v", err)
		return err
	}

	err = sqlDB.Ping()
	if err != nil {
		log.Printf("Error pinging coreDB: %v", err)
		return err
	}

	lastPingTime = time.Now()
	common.SysLog("Database pinged successfully")
	return nil
}

// PingLogDB checks a separately configured log database. A shared primary/log
// database is already covered by PingDB.
func PingLogDB() error {
	if telemetryDB == nil || telemetryDB == coreDB {
		return nil
	}
	sqlDB, err := telemetryDB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}

func SnapshotSQLPoolStats() map[string]SQLPoolStats {
	stats := make(map[string]SQLPoolStats, 2)
	appendSQLPoolStats(stats, "primary", coreDB)
	if telemetryDB != nil && telemetryDB != coreDB {
		appendSQLPoolStats(stats, "log", telemetryDB)
	}
	return stats
}

func appendSQLPoolStats(result map[string]SQLPoolStats, scope string, db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		return
	}
	dbStats := sqlDB.Stats()
	result[scope] = SQLPoolStats{
		SQLPoolConfig:     resolveSQLPoolConfig(scope),
		OpenConnections:   dbStats.OpenConnections,
		InUse:             dbStats.InUse,
		Idle:              dbStats.Idle,
		WaitCount:         dbStats.WaitCount,
		WaitDurationMS:    float64(dbStats.WaitDuration) / float64(time.Millisecond),
		MaxIdleClosed:     dbStats.MaxIdleClosed,
		MaxIdleTimeClosed: dbStats.MaxIdleTimeClosed,
		MaxLifetimeClosed: dbStats.MaxLifetimeClosed,
	}
}
