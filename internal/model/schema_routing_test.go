package model

import (
	"reflect"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRemovedRoutingLabMaterializedViews(t *testing.T) {
	want := []string{
		"strategy_perf_7d",
		"cf_calibration_30d",
		"feature_distribution_daily",
		"channel_perf_baseline",
	}
	if !reflect.DeepEqual(removedRoutingLabMaterializedViews, want) {
		t.Fatalf("unexpected retired RoutingLab materialized views, got=%v want=%v", removedRoutingLabMaterializedViews, want)
	}
}

func TestDropRemovedConsoleFeatureTablesRejectsOutstandingRedemptions(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:console_feature_cleanup?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	if err := db.Exec(`CREATE TABLE redemptions (
		id INTEGER PRIMARY KEY,
		status INTEGER NOT NULL,
		expired_time INTEGER NOT NULL,
		deleted_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create redemptions table: %v", err)
	}
	if err := db.Exec("INSERT INTO redemptions (id, status, expired_time) VALUES (?, ?, 0)", 1, retiredRedemptionStatusEnabled).Error; err != nil {
		t.Fatalf("insert outstanding redemption: %v", err)
	}

	err = dropRemovedConsoleFeatureTables(db)
	if err == nil || !strings.Contains(err.Error(), "enabled unexpired codes remain") {
		t.Fatalf("expected outstanding redemption protection, got %v", err)
	}
	if !db.Migrator().HasTable("redemptions") {
		t.Fatal("redemptions table was dropped despite outstanding code")
	}
}

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

func TestDropRemovedRoutingLabTablesPreservesFacts(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:routing_cleanup?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	for _, table := range []string{
		"routing_decision",
		"routing_outcome",
		"routing_experiment",
		"routing_experiment_audit",
		"routing_counterfactual",
		"routing_strategy_config",
	} {
		if err := db.Exec("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY)").Error; err != nil {
			t.Fatalf("create %s: %v", table, err)
		}
	}
	if err := dropRemovedRoutingLabTables(db); err != nil {
		t.Fatalf("drop RoutingLab tables: %v", err)
	}
	for _, table := range []string{"routing_decision", "routing_outcome"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected fact table %s to remain", table)
		}
	}
	for _, table := range []string{
		"routing_experiment",
		"routing_experiment_audit",
		"routing_counterfactual",
		"routing_strategy_config",
	} {
		if db.Migrator().HasTable(table) {
			t.Fatalf("expected removed table %s to be dropped", table)
		}
	}
}

func TestRetiredReferralCreditUserIDsAreDistinct(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:retired_referral_cache?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatalf("migrate wallet ledger: %v", err)
	}
	entries := []WalletLedger{
		{UserID: 2, DeltaMicrousd: 10, Kind: WalletLedgerKindBonus, RefType: retiredReferralCreditRefType, RefID: "user:2:one", PricingVersion: 1},
		{UserID: 2, DeltaMicrousd: 20, Kind: WalletLedgerKindBonus, RefType: retiredReferralCreditRefType, RefID: "user:2:two", PricingVersion: 1},
		{UserID: 7, DeltaMicrousd: 30, Kind: WalletLedgerKindBonus, RefType: retiredReferralCreditRefType, RefID: "user:7:one", PricingVersion: 1},
		{UserID: 9, DeltaMicrousd: 40, Kind: WalletLedgerKindBonus, RefType: "topup", RefID: "topup:9", PricingVersion: 1},
	}
	if err := db.Create(&entries).Error; err != nil {
		t.Fatalf("create wallet ledger entries: %v", err)
	}

	userIDs, err := retiredReferralCreditUserIDs(db)
	if err != nil {
		t.Fatalf("list retired referral credit users: %v", err)
	}
	if want := []int{2, 7}; !reflect.DeepEqual(userIDs, want) {
		t.Fatalf("unexpected retired referral credit users: got=%v want=%v", userIDs, want)
	}
}

func TestRetiredReferralCacheInvalidationTaskCompletesAfterCleanup(t *testing.T) {
	db := withPricingTestDB(t, "retired_referral_cache_cleanup")
	if err := db.AutoMigrate(&SchemaVersion{}); err != nil {
		t.Fatalf("migrate schema version: %v", err)
	}
	if err := db.Create(&WalletLedger{
		UserID:         2,
		DeltaMicrousd:  10,
		Kind:           WalletLedgerKindBonus,
		RefType:        retiredReferralCreditRefType,
		RefID:          "user:2:aff_quota_retirement",
		PricingVersion: 1,
	}).Error; err != nil {
		t.Fatalf("create migrated referral credit: %v", err)
	}
	if err := recordRetiredFeatureMigrationTask(db, retiredReferralCacheInvalidationTaskScope); err != nil {
		t.Fatalf("record retired referral cache task: %v", err)
	}

	if err := InvalidateRetiredReferralCreditUserCaches(); err != nil {
		t.Fatalf("invalidate migrated referral caches: %v", err)
	}
	pending, err := retiredFeatureMigrationTaskPending(db, retiredReferralCacheInvalidationTaskScope)
	if err != nil {
		t.Fatalf("check retired referral cache task: %v", err)
	}
	if pending {
		t.Fatal("expected successful referral cache cleanup to remove its task marker")
	}
}

func TestDropRemovedRoutingLabTablesRecordsPostMigrationTasks(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:retired_feature_tasks?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&User{}, &WalletLedger{}, &ModelMetadata{}, &SchemaVersion{}); err != nil {
		t.Fatalf("migrate retired feature task tables: %v", err)
	}
	if err := db.Exec("ALTER TABLE users ADD COLUMN aff_quota BIGINT DEFAULT 0").Error; err != nil {
		t.Fatalf("add retired referral balance column: %v", err)
	}
	if err := db.Exec("ALTER TABLE model_metadata ADD COLUMN quality_score REAL").Error; err != nil {
		t.Fatalf("add retired quality score column: %v", err)
	}
	if err := db.Create(&User{Id: 1, Username: "retired-feature-user", Quota: 100}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Exec("UPDATE users SET aff_quota = 25 WHERE id = 1").Error; err != nil {
		t.Fatalf("set retired referral balance: %v", err)
	}

	if err := dropRemovedRoutingLabTables(db); err != nil {
		t.Fatalf("drop retired feature schema: %v", err)
	}
	for _, scope := range []string{
		retiredReferralCacheInvalidationTaskScope,
		retiredModelRankRecalculationTaskScope,
	} {
		pending, err := retiredFeatureMigrationTaskPending(db, scope)
		if err != nil {
			t.Fatalf("check %s task: %v", scope, err)
		}
		if !pending {
			t.Fatalf("expected %s task marker after schema migration", scope)
		}
	}
	var user User
	if err := db.First(&user, 1).Error; err != nil {
		t.Fatalf("load migrated user: %v", err)
	}
	if user.Quota != 125 {
		t.Fatalf("expected migrated quota 125, got %d", user.Quota)
	}
}

func TestRetiredModelRankRecalculationCompletesAfterRefreshingScores(t *testing.T) {
	db := withPricingTestDB(t, "retired_model_rank_recalculation")
	if err := db.AutoMigrate(&SchemaVersion{}, &ModelMetadata{}); err != nil {
		t.Fatalf("migrate schema version and model metadata: %v", err)
	}
	if err := db.Create(&ModelMetadata{
		ModelName:      "retired-quality-model",
		FirstSeenAt:    time.Now(),
		WeeklyRequests: 1,
		VendorScore:    100,
		FinalRankScore: 75,
	}).Error; err != nil {
		t.Fatalf("create model metadata: %v", err)
	}
	if err := recordRetiredFeatureMigrationTask(db, retiredModelRankRecalculationTaskScope); err != nil {
		t.Fatalf("record retired model rank task: %v", err)
	}

	if err := RecalculateRetiredModelRatingRanks(); err != nil {
		t.Fatalf("recalculate migrated model ranks: %v", err)
	}
	var metadata ModelMetadata
	if err := db.Where("model_name = ?", "retired-quality-model").First(&metadata).Error; err != nil {
		t.Fatalf("load refreshed model metadata: %v", err)
	}
	if metadata.FinalRankScore != 90 {
		t.Fatalf("expected recalculated final score 90, got %v", metadata.FinalRankScore)
	}
	pending, err := retiredFeatureMigrationTaskPending(db, retiredModelRankRecalculationTaskScope)
	if err != nil {
		t.Fatalf("check retired model rank task: %v", err)
	}
	if pending {
		t.Fatal("expected successful rank recalculation to remove its task marker")
	}
}
