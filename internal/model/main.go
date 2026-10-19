package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/pkg/billing"

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
	return db, registerSlowQueryCallbacks(db)
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

		if !common.IsMasterNode {
			if migrationMode() == "validate" {
				if err = ValidateSchemaVersion(); err != nil {
					return err
				}
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
			if migrationMode() == "validate" {
				return validateSchemaVersion(telemetryDB, schemaVersionLog)
			}
			return nil
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
		&OptionAuditLog{},
		&TokenAuditLog{},

		&TopUp{},

		&Model{},
		&ModelInfoReview{},
		&ModelMetadata{},
		&Vendor{},
		&PrefillGroup{},
		&MonthlyReport{},
		&ReportSubscription{},
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
		&SearchHealthSample{},
	}, append(RedisLedgerModels(), extensions.coreModels...)...)...)
	if err != nil {
		return err
	}
	if err = dropRemovedRoutingLabTables(coreDB); err != nil {
		return err
	}
	if err = dropRemovedConsoleFeatureTables(coreDB); err != nil {
		return err
	}
	if err = backfillRoutingDecisionIdempotencyKeys(coreDB); err != nil {
		return err
	}
	if err = migrateUserQuotaColumnsToBigint(coreDB); err != nil {
		return err
	}
	if err = migrateUserHasToppedUpFromTopUps(coreDB); err != nil {
		return err
	}
	if err = migrateChannelScopedProviderCostPrices(coreDB); err != nil {
		return err
	}
	if err = BackfillChannelSourceTypes(coreDB); err != nil {
		return err
	}
	// The lane migrations below normalise anything outside the lane registry to
	// 稳定. The registry itself lives in an option row that InitOptionMap has not
	// read yet at this point, so load it first: without this a custom lane's
	// sell prices, channels, abilities and tokens are rewritten to `direct` on
	// every master restart, silently undoing an operator's routing.
	if err = loadChannelLaneRegistry(coreDB); err != nil {
		return err
	}
	if err = migrateModelSellPriceChannelGroup(coreDB); err != nil {
		return err
	}
	if err = migrateModelLifecycleDefaults(coreDB); err != nil {
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
	if err = BackfillProtocolCapabilityIdentity(coreDB); err != nil {
		return err
	}
	if err = migrateTokenGroupDefaults(coreDB); err != nil {
		return err
	}
	if err = migrateLegacyUsageData(coreDB); err != nil {
		return err
	}
	if err = deleteLegacyPricingOptions(coreDB); err != nil {
		return err
	}
	if err = deleteLegacyMediaOptions(coreDB); err != nil {
		return err
	}
	if err = migrateQuotaRemindThreshold(coreDB); err != nil {
		return err
	}
	if err = deleteLegacyAuthOptions(coreDB); err != nil {
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
	if err = migrateLegacyUserSettingKeys(coreDB); err != nil {
		return err
	}
	if err = migrateUserNeedsPassword(coreDB); err != nil {
		return err
	}
	if err = migrateLegacyTokenKeys(coreDB); err != nil {
		return err
	}
	if err = migrateTokenKeyDigests(coreDB); err != nil {
		return err
	}
	if err = migrateChannelKeysToEncrypted(coreDB); err != nil {
		return err
	}
	if err = migrateLazuPublicIDs(coreDB); err != nil {
		return err
	}
	if err = MigrateLegacyAnnouncements(coreDB); err != nil {
		return err
	}
	if err = BackfillAnnouncementPublicationFields(coreDB); err != nil {
		return err
	}
	if err = dropLegacyPricingColumns(coreDB); err != nil {
		return err
	}
	if err = migrateAbilityRoleDefaults(coreDB); err != nil {
		return err
	}
	if err = migrateTeamProjects(coreDB); err != nil {
		return err
	}
	// After migrateTeamProjects: it still reads tokens.is_service, which this
	// drops (users.stripe_customer is copied onto projects first).
	if err = migrateSchemaV15(coreDB); err != nil {
		return err
	}
	if err = runExtensionMigrations(coreDB); err != nil {
		return err
	}
	if err = EnsureMonthlyReportUniqConstraint(); err != nil {
		return err
	}
	if err = migrateSchema33Core(coreDB); err != nil {
		return err
	}
	// Last migration: its transaction records the incompatible rollback floor.
	// No schema work may remain after that marker becomes visible to replicas.
	return cleanupLegacyPricingSeeds(coreDB)
}

const (
	retiredReferralCreditRefType              = "referral_retirement"
	retiredReferralCacheInvalidationTaskScope = "retired_referral_cache"
	retiredModelRankRecalculationTaskScope    = "retired_model_ranks"
)

func recordRetiredFeatureMigrationTask(tx *gorm.DB, scope string) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&SchemaVersion{
		Scope:     scope,
		Version:   1,
		AppliedAt: time.Now().UTC(),
	}).Error
}

func retiredFeatureMigrationTaskPending(db *gorm.DB, scope string) (bool, error) {
	if db == nil || !db.Migrator().HasTable(&SchemaVersion{}) {
		return false, nil
	}
	var count int64
	if err := db.Model(&SchemaVersion{}).Where("scope = ?", scope).Count(&count).Error; err != nil {
		return false, fmt.Errorf("check retired feature migration task %s: %w", scope, err)
	}
	return count > 0, nil
}

func completeRetiredFeatureMigrationTask(db *gorm.DB, scope string) error {
	if err := db.Where("scope = ?", scope).Delete(&SchemaVersion{}).Error; err != nil {
		return fmt.Errorf("complete retired feature migration task %s: %w", scope, err)
	}
	return nil
}

// InvalidateRetiredReferralCreditUserCaches clears stale quota projections
// after the referral-credit migration. A durable task marker lets a failed
// startup retry the cleanup, then removes the work once it succeeds.
func InvalidateRetiredReferralCreditUserCaches() error {
	if coreDB == nil || !coreDB.Migrator().HasTable(&WalletLedger{}) {
		return nil
	}
	pending, err := retiredFeatureMigrationTaskPending(coreDB, retiredReferralCacheInvalidationTaskScope)
	if err != nil {
		return err
	}
	if !pending {
		return nil
	}
	userIDs, err := retiredReferralCreditUserIDs(coreDB)
	if err != nil {
		return err
	}
	for _, userID := range userIDs {
		if err := InvalidateUserCache(userID); err != nil {
			return fmt.Errorf("invalidate migrated referral credit cache for user %d: %w", userID, err)
		}
	}
	return completeRetiredFeatureMigrationTask(coreDB, retiredReferralCacheInvalidationTaskScope)
}

// RecalculateRetiredModelRatingRanks updates persisted ranking values after
// quality_score is removed. It runs after pricing loads so value scores use the
// current retained price data, and the task marker makes retries safe.
func RecalculateRetiredModelRatingRanks() error {
	if coreDB == nil || !coreDB.Migrator().HasTable(&ModelMetadata{}) {
		return nil
	}
	pending, err := retiredFeatureMigrationTaskPending(coreDB, retiredModelRankRecalculationTaskScope)
	if err != nil {
		return err
	}
	if !pending {
		return nil
	}
	if err := UpdateAllRankScores(); err != nil {
		return fmt.Errorf("recalculate retired model rating ranks: %w", err)
	}
	return completeRetiredFeatureMigrationTask(coreDB, retiredModelRankRecalculationTaskScope)
}

func retiredReferralCreditUserIDs(db *gorm.DB) ([]int, error) {
	var userIDs []int
	if err := db.Model(&WalletLedger{}).
		Distinct("user_id").
		Where("ref_type = ?", retiredReferralCreditRefType).
		Order("user_id").
		Pluck("user_id", &userIDs).Error; err != nil {
		return nil, fmt.Errorf("list migrated referral credit users: %w", err)
	}
	return userIDs, nil
}

func dropRemovedRoutingLabTables(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		migratedReferralCredit := false
		droppedModelRatingScore := false
		if tx.Dialector.Name() == common.DatabaseTypePostgreSQL {
			for _, view := range removedRoutingLabMaterializedViews {
				if err := tx.Exec(fmt.Sprintf(`DROP MATERIALIZED VIEW IF EXISTS "%s"`, view)).Error; err != nil {
					return fmt.Errorf("drop removed RoutingLab materialized view %s: %w", view, err)
				}
			}
		}
		for _, table := range []string{
			"routing_experiment_audit",
			"routing_counterfactual",
			"routing_experiment",
			"routing_strategy_config",
		} {
			if tx.Migrator().HasTable(table) {
				if err := tx.Migrator().DropTable(table); err != nil {
					return fmt.Errorf("drop removed RoutingLab table %s: %w", table, err)
				}
			}
		}
		if tx.Migrator().HasTable(&User{}) {
			if tx.Migrator().HasColumn(&User{}, "aff_quota") {
				type retiredAffQuota struct {
					ID       int
					AffQuota int64 `gorm:"column:aff_quota"`
				}
				var balances []retiredAffQuota
				if err := tx.Table("users").
					Select("id, aff_quota").
					Where("aff_quota > 0").
					Find(&balances).Error; err != nil {
					return fmt.Errorf("read retired referral balances: %w", err)
				}
				for _, balance := range balances {
					if err := adjustUserQuotaWithLedgerTx(tx, AdjustUserQuotaWithLedgerInput{
						UserID:         balance.ID,
						DeltaQuota:     billing.MicroUSD(balance.AffQuota),
						Kind:           WalletLedgerKindBonus,
						RefType:        retiredReferralCreditRefType,
						RefID:          fmt.Sprintf("user:%d:aff_quota_retirement", balance.ID),
						PricingVersion: defaultPricingSeedVersion,
						Metadata: map[string]any{
							"source": "aff_quota",
						},
					}); err != nil {
						return fmt.Errorf("migrate retired referral balance for user %d: %w", balance.ID, err)
					}
				}
				migratedReferralCredit = len(balances) > 0
			}
			for _, column := range []string{
				"aff_code",
				"aff_count",
				"aff_quota",
				"aff_history",
				"inviter_id",
			} {
				if tx.Migrator().HasColumn(&User{}, column) {
					if err := tx.Migrator().DropColumn(&User{}, column); err != nil {
						return fmt.Errorf("drop removed referral column %s: %w", column, err)
					}
				}
			}
		}
		if tx.Migrator().HasTable(&ModelMetadata{}) && tx.Migrator().HasColumn(&ModelMetadata{}, "quality_score") {
			if err := tx.Migrator().DropColumn(&ModelMetadata{}, "quality_score"); err != nil {
				return fmt.Errorf("drop removed model rating column quality_score: %w", err)
			}
			droppedModelRatingScore = true
		}
		if migratedReferralCredit {
			if err := recordRetiredFeatureMigrationTask(tx, retiredReferralCacheInvalidationTaskScope); err != nil {
				return fmt.Errorf("record retired referral cache invalidation: %w", err)
			}
		}
		if droppedModelRatingScore {
			if err := recordRetiredFeatureMigrationTask(tx, retiredModelRankRecalculationTaskScope); err != nil {
				return fmt.Errorf("record retired model rank recalculation: %w", err)
			}
		}
		return nil
	})
}

// dropRemovedConsoleFeatureTables removes features retired from the product.
// These tables had no retained business role after the public endpoints and
// console surfaces were removed, so keeping them would recreate dead schema on
// future deployments.
func dropRemovedConsoleFeatureTables(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := rejectOutstandingRedemptions(tx); err != nil {
			return err
		}
		for _, table := range []string{
			"redemptions",
			"referrals",
			"invite_codes",
			"referral_configs",
			"referral_rewards",
			"model_ratings",
			"model_rating_summaries",
			"rating_interactions",
		} {
			if tx.Migrator().HasTable(table) {
				if err := tx.Migrator().DropTable(table); err != nil {
					return fmt.Errorf("drop removed console feature table %s: %w", table, err)
				}
			}
		}
		return nil
	})
}

const retiredRedemptionStatusEnabled = 1

// rejectOutstandingRedemptions prevents an upgrade from silently discarding
// unclaimed monetary value. The redemption feature itself is retired, so an
// operator must honor or explicitly remove any still-valid code before this
// migration can drop the table.
func rejectOutstandingRedemptions(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("redemptions") {
		return nil
	}

	query := tx.Table("redemptions").Where(
		"status = ? AND (expired_time = 0 OR expired_time >= ?)",
		retiredRedemptionStatusEnabled,
		common.GetTimestamp(),
	)
	if tx.Migrator().HasColumn("redemptions", "deleted_at") {
		query = query.Where("deleted_at IS NULL")
	}

	var outstanding int64
	if err := query.Count(&outstanding).Error; err != nil {
		return fmt.Errorf("check outstanding redemption codes: %w", err)
	}
	if outstanding > 0 {
		return fmt.Errorf(
			"cannot retire redemption codes while %d enabled unexpired codes remain; honor or explicitly remove them before upgrading",
			outstanding,
		)
	}
	return nil
}

var removedRoutingLabMaterializedViews = []string{
	"strategy_perf_7d",
	"cf_calibration_30d",
	"feature_distribution_daily",
	"channel_perf_baseline",
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
	if err := dropLegacyPricingColumns(telemetryDB); err != nil {
		return err
	}
	if err := runOneTimeMigration(coreDB, "telemetry_lifecycle_v24", func() error {
		if err := reattributeRetiredServiceKeyLogs(coreDB, telemetryDB); err != nil {
			return err
		}
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
	return migrateSchema34Telemetry(telemetryDB)
}

type legacyUsageDataRow struct {
	UserID    int
	Username  string
	ModelName string
	CreatedAt int64
	TokenUsed int
	Count     int
	Quota     int
}

var legacyPricingOptionKeys = []string{
	"QuotaPerUnit",
	"ModelRatio",
	"CompletionRatio",
	"GroupRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"CreateCacheRatio1h",
	"UserTierRatio",
	"TierChannelRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
	"TopupGroupRatio",
	"ModelPrice",
	"UserUsableGroups",
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

const (
	legacyAcceptUnpricedModelSettingKey = "accept_unset_model_price"
	acceptUnpricedModelSettingKey       = "accept_unpriced_model"
)

func deleteLegacyPricingOptions(db *gorm.DB) error {
	return deleteLegacyOptionsByKey(db, legacyPricingOptionKeys)
}

func deleteLegacyMediaOptions(db *gorm.DB) error {
	return deleteLegacyOptionsByKey(db, legacyMediaOptionKeys)
}

func deleteLegacyAuthOptions(db *gorm.DB) error {
	return deleteLegacyOptionsByKey(db, legacyAuthOptionKeys)
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

// migrateLegacyUserSettingKeys strips the retired per-user unpriced-model
// opt-in, under both its current and its older key. Whether an unpriced model
// runs is the operator's call; the gateway no longer reads either key.
func migrateLegacyUserSettingKeys(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&User{}) {
		return nil
	}

	var users []User
	if err := db.Select("id", "setting").
		Where("setting LIKE ? OR setting LIKE ?",
			"%"+legacyAcceptUnpricedModelSettingKey+"%",
			"%"+acceptUnpricedModelSettingKey+"%").
		Find(&users).Error; err != nil {
		return err
	}
	for _, user := range users {
		if user.Setting == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(user.Setting), &raw); err != nil {
			common.SysLog(fmt.Sprintf("failed to parse legacy user setting for user %d: %s", user.Id, err.Error()))
			continue
		}
		_, hasLegacy := raw[legacyAcceptUnpricedModelSettingKey]
		_, hasCurrent := raw[acceptUnpricedModelSettingKey]
		if !hasLegacy && !hasCurrent {
			continue
		}
		delete(raw, legacyAcceptUnpricedModelSettingKey)
		delete(raw, acceptUnpricedModelSettingKey)
		settingBytes, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		if err := db.Model(&User{}).Where("id = ?", user.Id).Update("setting", string(settingBytes)).Error; err != nil {
			return err
		}
		if err := invalidateUserCache(user.Id); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate user cache for user %d: %s", user.Id, err.Error()))
		}
	}
	return nil
}

// migrateUserNeedsPassword moves credential state out of the mutable settings
// JSON. The migration is idempotent: it only promotes legacy true values and
// removes the obsolete key, never downgrading an already-protected account.
func migrateUserNeedsPassword(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&User{}) || !db.Migrator().HasColumn(&User{}, "needs_password") {
		return nil
	}

	var users []User
	if err := db.Unscoped().Select("id", "setting", "needs_password").
		Where("setting LIKE ?", "%needs_password%").
		Find(&users).Error; err != nil {
		return err
	}
	for _, user := range users {
		if strings.TrimSpace(user.Setting) == "" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(user.Setting), &raw); err != nil {
			common.SysLog(fmt.Sprintf("failed to parse needs_password setting for user %d: %s", user.Id, err.Error()))
			continue
		}
		encoded, exists := raw["needs_password"]
		if !exists {
			continue
		}
		var legacyNeedsPassword bool
		if err := json.Unmarshal(encoded, &legacyNeedsPassword); err != nil {
			common.SysLog(fmt.Sprintf("failed to parse needs_password value for user %d: %s", user.Id, err.Error()))
			continue
		}
		delete(raw, "needs_password")
		settingBytes, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		updates := map[string]any{"setting": string(settingBytes)}
		if legacyNeedsPassword {
			updates["needs_password"] = true
		}
		if err := db.Unscoped().Model(&User{}).Where("id = ?", user.Id).Updates(updates).Error; err != nil {
			return err
		}
		if err := invalidateUserCache(user.Id); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate user cache for user %d: %s", user.Id, err.Error()))
		}
	}
	return migrateLegacyOIDCPlaceholderPasswords(db)
}

// migrateLegacyOIDCPlaceholderPasswords covers accounts created by the old
// generic OIDC flow. Those rows received an undisclosed random password before
// UserIdentity and the needs_password marker existed. An OIDC identity row is
// the migration fence: after a legacy account has been claimed by the current
// resolver and the owner sets a real password, later startups must not turn
// the marker back on.
func migrateLegacyOIDCPlaceholderPasswords(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&UserIdentity{}) ||
		!db.Migrator().HasColumn(&User{}, "oidc_issuer") ||
		!db.Migrator().HasColumn(&User{}, "oidc_subject") ||
		!db.Migrator().HasColumn(&User{}, "needs_password") {
		return nil
	}

	var userIDs []int
	if err := db.Table("users AS legacy_users").
		Select("legacy_users.id").
		Joins("LEFT JOIN user_identities AS oidc_identity ON oidc_identity.user_id = legacy_users.id AND oidc_identity.provider = ?", "oidc").
		Where("TRIM(COALESCE(legacy_users.oidc_issuer, '')) <> ''").
		Where("TRIM(COALESCE(legacy_users.oidc_subject, '')) <> ''").
		Where("legacy_users.needs_password = ?", false).
		Where("oidc_identity.id IS NULL").
		Pluck("legacy_users.id", &userIDs).Error; err != nil {
		return err
	}
	if len(userIDs) == 0 {
		return nil
	}
	result := db.Unscoped().Model(&User{}).Where("id IN ?", userIDs).Update("needs_password", true)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(userIDs)) {
		return fmt.Errorf("migrate legacy OIDC password state: updated %d of %d users", result.RowsAffected, len(userIDs))
	}
	for _, userID := range userIDs {
		if err := invalidateUserCache(userID); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate legacy OIDC user cache for user %d: %s", userID, err.Error()))
		}
	}
	return nil
}

func migrateLegacyUsageData(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable("quota_data") {
		return nil
	}
	if !db.Migrator().HasTable("usage_data") {
		return nil
	}

	var rows []legacyUsageDataRow
	if err := db.Table("quota_data").
		Select("user_id, username, model_name, created_at, token_used, count, quota").
		Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var existing UsageData
		tx := db.Table("usage_data").
			Where("user_id = ? and username = ? and model_name = ? and created_at = ?", row.UserID, row.Username, row.ModelName, row.CreatedAt).
			First(&existing)
		if tx.Error != nil && tx.Error != gorm.ErrRecordNotFound {
			return tx.Error
		}
		if tx.RowsAffected > 0 {
			if err := db.Table("usage_data").
				Where("id = ?", existing.Id).
				Updates(map[string]any{
					"count":           gorm.Expr("count + ?", row.Count),
					"amount_microusd": gorm.Expr("amount_microusd + ?", row.Quota),
					"token_used":      gorm.Expr("token_used + ?", row.TokenUsed),
				}).Error; err != nil {
				return err
			}
			continue
		}
		usageRow := UsageData{
			UserID:         row.UserID,
			Username:       row.Username,
			ModelName:      row.ModelName,
			CreatedAt:      row.CreatedAt,
			TokenUsed:      row.TokenUsed,
			Count:          row.Count,
			AmountMicrousd: billing.MicroUSD(row.Quota),
		}
		if err := db.Table("usage_data").Create(&usageRow).Error; err != nil {
			return err
		}
	}
	return db.Migrator().DropTable("quota_data")
}

func migrateUserHasToppedUpFromTopUps(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&User{}) || !db.Migrator().HasTable(&TopUp{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&User{}, "has_topped_up") {
		return nil
	}
	session := db.Session(&gorm.Session{PrepareStmt: false, NewDB: true})
	successfulTopUpUsers := session.Model(&TopUp{}).
		Select("DISTINCT user_id").
		Where("status = ?", common.TopUpStatusSuccess)

	return session.Model(&User{}).
		Where("has_topped_up = ? AND id IN (?)", false, successfulTopUpUsers).
		Update("has_topped_up", true).Error
}

func dropLegacyPricingColumns(db *gorm.DB) error {
	return dropLegacyColumn(db, "logs", "quota")
}

func migrateChannelScopedProviderCostPrices(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&ProviderCostPrice{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&ProviderCostPrice{}, "channel_id") {
		if err := db.Migrator().AddColumn(&ProviderCostPrice{}, "ChannelID"); err != nil {
			return err
		}
	}
	switch db.Dialector.Name() {
	case "postgres":
		if err := db.Exec(`
DO $$
DECLARE
  old_idx text;
BEGIN
  SELECT indexname INTO old_idx
  FROM pg_indexes
  WHERE schemaname = current_schema()
    AND tablename = 'provider_cost_prices'
    AND indexdef ILIKE '%UNIQUE%'
    AND indexdef ILIKE '%model_name%'
    AND indexdef ILIKE '%provider%'
    AND indexdef ILIKE '%pricing_version%'
    AND indexdef NOT ILIKE '%channel_id%'
  LIMIT 1;
  IF old_idx IS NOT NULL THEN
    EXECUTE format('DROP INDEX %I', old_idx);
  END IF;
END $$;
`).Error; err != nil {
			return err
		}
		return db.Exec(`
CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_cost_prices_model_provider_channel_version
  ON provider_cost_prices (model_name, provider, channel_id, pricing_version);
`).Error
	default:
		if db.Migrator().HasIndex(&ProviderCostPrice{}, "idx_provider_cost_prices_provider_model_version") {
			if err := db.Migrator().DropIndex(&ProviderCostPrice{}, "idx_provider_cost_prices_provider_model_version"); err != nil {
				return err
			}
		}
		if !db.Migrator().HasIndex(&ProviderCostPrice{}, "idx_provider_cost_prices_model_provider_channel_version") {
			return db.Migrator().CreateIndex(&ProviderCostPrice{}, "idx_provider_cost_prices_model_provider_channel_version")
		}
	}
	return nil
}

func invalidatePostgresSchemaPlans(db *gorm.DB) {
	if db == nil || db.Dialector.Name() != "postgres" {
		return
	}
	session := db.Session(&gorm.Session{PrepareStmt: false, NewDB: true})
	if err := session.Exec("DISCARD PLANS").Error; err != nil {
		common.SysLog("failed to discard postgres cached plans after schema migration: " + err.Error())
	}
}

// loadChannelLaneRegistry reads the ChannelLanes option straight from the table
// so lane-aware migrations can run before the general option load. A missing or
// unreadable row leaves the two builtin lanes in place, which is the same state
// the registry starts in.
func loadChannelLaneRegistry(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Option{}) {
		return nil
	}
	var row Option
	err := db.Where("key = ?", "ChannelLanes").Limit(1).Find(&row).Error
	if err != nil || strings.TrimSpace(row.Value) == "" {
		return err
	}
	if applyErr := setting.UpdateChannelLanesByJSONString(row.Value); applyErr != nil {
		common.SysLog("failed to load the channel lane registry before migrations: " + applyErr.Error())
	}
	return nil
}

// migrateAbilityRoleDefaults gives pre-role ability rows the role they have
// always effectively had. Every route that existed before roles were
// introduced was one the router could pick, which is precisely "primary";
// leaving them NULL or blank would make NormalizeAbilityRole the only thing
// standing between a stale row and being read as inventory.
func migrateAbilityRoleDefaults(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Ability{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&Ability{}, "Role") {
		return nil
	}
	return db.Model(&Ability{}).
		Where("role IS NULL OR role = '' OR role NOT IN ?",
			[]string{AbilityRolePrimary, AbilityRoleBackup, AbilityRoleInventory}).
		Update("role", AbilityRolePrimary).Error
}

func migrateModelSellPriceChannelGroup(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&ModelSellPrice{}) {
		return nil
	}
	if db.Migrator().HasColumn(&ModelSellPrice{}, "ChannelGroup") {
		if err := db.Model(&ModelSellPrice{}).
			Where("channel_group IS NULL OR channel_group = '' OR channel_group = ? OR channel_group = ?", "default", "auto").
			Update("channel_group", ChannelLaneDirect).Error; err != nil {
			return err
		}
		// Every *registered* lane is legitimate here, not just the two builtins:
		// normalising a custom lane away would move real prices onto 稳定.
		if err := db.Model(&ModelSellPrice{}).
			Where("channel_group NOT IN ?", setting.KnownLaneSlugs()).
			Update("channel_group", ChannelLaneDirect).Error; err != nil {
			return err
		}
	}
	if db.Migrator().HasIndex(&ModelSellPrice{}, "idx_model_sell_prices_model_version") {
		if err := db.Migrator().DropIndex(&ModelSellPrice{}, "idx_model_sell_prices_model_version"); err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex(&ModelSellPrice{}, "idx_model_sell_prices_model_lane_version") {
		if err := db.Migrator().CreateIndex(&ModelSellPrice{}, "idx_model_sell_prices_model_lane_version"); err != nil {
			return err
		}
	}
	invalidatePostgresSchemaPlans(db)
	return nil
}

func migrateTokenGroupDefaults(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Token{}) {
		return nil
	}
	if err := migrateLaneGroupColumn(db, "tokens"); err != nil {
		return err
	}
	if err := migrateLaneGroupColumn(db, "channels"); err != nil {
		return err
	}
	if err := migrateLaneGroupColumn(db, "abilities"); err != nil {
		return err
	}
	switch db.Dialector.Name() {
	case "postgres":
		if err := db.Exec(`ALTER TABLE tokens ALTER COLUMN "group" SET DEFAULT 'direct'`).Error; err != nil {
			return err
		}
		return db.Exec(`ALTER TABLE channels ALTER COLUMN "group" SET DEFAULT 'direct'`).Error
	default:
		return nil
	}
}

func migrateLaneGroupColumn(db *gorm.DB, table string) error {
	if db == nil || !db.Migrator().HasTable(table) {
		return nil
	}
	quote := "`"
	if db.Dialector.Name() == "postgres" {
		quote = `"`
	}
	groupCol := quoteSQLIdentifier("group", quote)
	tableName := quoteSQLIdentifier(table, quote)
	// Anything not in the registry is normalised to 稳定; the registry is the
	// full known set, so a lane an operator added keeps its rows.
	updateSQL := fmt.Sprintf(
		"UPDATE %s SET %s = ? WHERE %s IS NULL OR TRIM(%s) = ? OR %s = ? OR %s = ? OR %s NOT IN (?)",
		tableName,
		groupCol,
		groupCol,
		groupCol,
		groupCol,
		groupCol,
		groupCol,
	)
	return db.Exec(
		updateSQL,
		ChannelLaneDirect,
		"",
		"default",
		"auto",
		setting.KnownLaneSlugs(),
	).Error
}

var userQuotaBigintColumns = []string{"quota", "used_quota"}

// migrateUserQuotaColumnsToBigint widens the quota columns once. It used to
// issue the ALTER unconditionally on every master boot, which took an ACCESS
// EXCLUSIVE lock on `users` to do nothing; the column type is now checked
// first, so a converged database is left alone.
func migrateUserQuotaColumnsToBigint(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&User{}) {
		return nil
	}
	if !common.UsingPostgreSQL {
		return nil
	}
	for _, column := range userQuotaBigintColumns {
		if !db.Migrator().HasColumn(&User{}, column) {
			continue
		}
		wide, err := userQuotaColumnIsBigint(db, column)
		if err != nil {
			return err
		}
		if wide {
			continue
		}
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE users ALTER COLUMN %s TYPE bigint USING %s::bigint`, quoteSQLIdentifier(column, `"`), quoteSQLIdentifier(column, `"`))).Error; err != nil {
			return err
		}
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE users ALTER COLUMN %s SET DEFAULT 0`, quoteSQLIdentifier(column, `"`))).Error; err != nil {
			return err
		}
	}
	return nil
}

func userQuotaColumnIsBigint(db *gorm.DB, column string) (bool, error) {
	var dataType string
	// Scoped to the schema this connection actually resolves `users` in.
	// information_schema spans every schema in the database, so an unscoped
	// probe can read a namesake table's column type and conclude the widening
	// is already done when the live column is still an int4.
	err := db.Raw(
		`SELECT data_type FROM information_schema.columns
		 WHERE table_schema = current_schema()
		   AND table_name = 'users' AND column_name = ?`,
		column,
	).Scan(&dataType).Error
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(dataType), "bigint"), nil
}

func migrateModelLifecycleDefaults(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Model{}) || !db.Migrator().HasColumn(&Model{}, "LifecycleStatus") {
		return nil
	}
	now := common.GetTimestamp()
	var rows []Model
	if err := db.Select("id, model_name, lifecycle_status, source, last_seen_at, retire_after").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		updates := map[string]interface{}{}
		lifecycle := NormalizeModelLifecycleStatus(row.LifecycleStatus)
		if IsRetiredModelName(row.ModelName) {
			lifecycle = ModelLifecycleRetired
			if row.RetireAfter == 0 {
				updates["retire_after"] = now
			}
		}
		if row.LifecycleStatus != lifecycle {
			updates["lifecycle_status"] = lifecycle
		}
		if strings.TrimSpace(row.Source) == "" {
			updates["source"] = ModelSourceManual
		}
		if row.LastSeenAt == 0 {
			updates["last_seen_at"] = now
		}
		if len(updates) == 0 {
			continue
		}
		if err := db.Model(&Model{}).Where("id = ?", row.Id).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
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
