package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"

	"github.com/lazu-ai/lazu/pkg/billing"
)

func seedPriceRow(name string) ModelSellPrice {
	input := 1.0
	return ModelSellPrice{
		ModelName:      name,
		ChannelGroup:   ChannelLaneDirect,
		InputPerMtok:   &input,
		OutputPerMtok:  &input,
		Source:         legacySeedPriceSource,
		Active:         true,
		PricingVersion: defaultPricingSeedVersion,
	}
}

func sellPriceNames(t *testing.T) map[string]bool {
	t.Helper()
	var rows []ModelSellPrice
	if err := coreDB.Find(&rows).Error; err != nil {
		t.Fatalf("failed to list sell prices: %v", err)
	}
	out := map[string]bool{}
	for _, row := range rows {
		out[row.ModelName] = true
	}
	return out
}

// The whole point of the cleanup: a seeded price survives exactly as long as
// something still uses the model, and the four signals are independent.
func TestCleanupLegacyPricingSeedsKeepsModelsStillInUse(t *testing.T) {
	db := withPricingTestDB(t, "legacy_seed_cleanup_keep")

	rows := []ModelSellPrice{
		seedPriceRow("listed-model"),
		seedPriceRow("served-model"),
		seedPriceRow("discounted-model"),
		seedPriceRow("repriced-model"),
		seedPriceRow("PaLM-2"),
		seedPriceRow("claude-instant-1"),
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("failed to create seed prices: %v", err)
	}

	// 1. listed in the catalog
	if err := db.Create(&Model{
		ModelName:       "listed-model",
		Status:          1,
		LifecycleStatus: ModelLifecycleActive,
	}).Error; err != nil {
		t.Fatalf("failed to create listed model: %v", err)
	}
	// A catalog row that is not listable must NOT keep its price.
	if err := db.Create(&Model{
		ModelName:       "PaLM-2",
		Status:          0,
		LifecycleStatus: ModelLifecycleRetired,
	}).Error; err != nil {
		t.Fatalf("failed to create retired model: %v", err)
	}
	// 2. reachable through an enabled channel
	if err := db.Create(&Ability{
		Group: ChannelLaneDirect, Model: "served-model", ChannelId: 1, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("failed to create ability: %v", err)
	}
	// A disabled ability is not a reason to keep anything.
	if err := db.Create(&Ability{
		Group: ChannelLaneDirect, Model: "claude-instant-1", ChannelId: 2, Enabled: false,
	}).Error; err != nil {
		t.Fatalf("failed to create disabled ability: %v", err)
	}
	// 3. carrying a live price-plan model rule
	createPlanRuleForSeedTest(t, db, PricePlan{Name: "kept", Lanes: PricePlanLanesDirect, Active: true}, "discounted-model")
	// 4. priced by someone other than the seeder
	other := seedPriceRow("repriced-model")
	other.Source = "manual"
	other.PricingVersion = 2
	other.ChannelGroup = ChannelLaneAgent
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("failed to create manual price: %v", err)
	}

	if err := cleanupLegacyPricingSeeds(db); err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}

	names := sellPriceNames(t)
	for _, kept := range []string{"listed-model", "served-model", "discounted-model", "repriced-model"} {
		if !names[kept] {
			t.Errorf("expected %q to keep its seeded price", kept)
		}
	}
	for _, removed := range []string{"PaLM-2", "claude-instant-1"} {
		if names[removed] {
			t.Errorf("expected unused seed %q to be removed", removed)
		}
	}
}

// A price an operator wrote is never the cleanup's business, whatever the
// model is. Only rows the seeder itself authored may be removed.
func TestCleanupLegacyPricingSeedsLeavesNonSeedRowsAlone(t *testing.T) {
	db := withPricingTestDB(t, "legacy_seed_cleanup_sources")

	// pricing_version 1 is NOT a seed marker: the historical price import
	// wrote it on every row it created, and those rows must survive.
	migrated := seedPriceRow("some-migrated-model")
	migrated.Source = "lazu_initial"
	manual := seedPriceRow("some-manual-model")
	manual.Source = "manual"
	if err := db.Create(&[]ModelSellPrice{migrated, manual}).Error; err != nil {
		t.Fatalf("failed to create rows: %v", err)
	}

	if err := cleanupLegacyPricingSeeds(db); err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}

	names := sellPriceNames(t)
	if !names["some-migrated-model"] || !names["some-manual-model"] {
		t.Fatalf("cleanup must only touch rows authored by the seeder, got %v", names)
	}
}

func TestCleanupLegacyPricingSeedsRemovesSeedCostMirror(t *testing.T) {
	db := withPricingTestDB(t, "legacy_seed_cleanup_costs")
	input := 1.0
	rows := []ProviderCostPrice{
		{ModelName: "unused-model", Provider: "seed", ChannelID: 0, InputPerMtok: &input, Source: legacySeedPriceSource, PricingVersion: 1},
		{ModelName: "served-model", Provider: "seed", ChannelID: 0, InputPerMtok: &input, Source: legacySeedPriceSource, PricingVersion: 1},
		{ModelName: "unused-model", Provider: "upstream", ChannelID: 7, InputPerMtok: &input, PricingVersion: 1},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("failed to create cost prices: %v", err)
	}
	if err := db.Create(&Ability{
		Group: ChannelLaneDirect, Model: "served-model", ChannelId: 1, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("failed to create ability: %v", err)
	}

	if err := cleanupLegacyPricingSeeds(db); err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}

	var remaining []ProviderCostPrice
	if err := db.Find(&remaining).Error; err != nil {
		t.Fatalf("failed to list cost prices: %v", err)
	}
	var seedUnused, seedServed, channelScoped int
	for _, row := range remaining {
		switch {
		case row.Provider == "seed" && row.ModelName == "unused-model":
			seedUnused++
		case row.Provider == "seed" && row.ModelName == "served-model":
			seedServed++
		case row.ChannelID == 7:
			channelScoped++
		}
	}
	if seedUnused != 0 {
		t.Errorf("expected the unused seed cost mirror to be removed")
	}
	if seedServed != 1 {
		t.Errorf("expected a served model to keep its seed cost fallback")
	}
	if channelScoped != 1 {
		t.Errorf("expected a channel-scoped cost row to be untouched")
	}
}

// The wallet half of the bug: an opening balance written after real ledger
// history restates a balance the ledger already accounts for, so the ledger
// sums to roughly twice the user's quota.
func TestCleanupLegacyPricingSeedsRemovesDuplicateOpeningBalance(t *testing.T) {
	db := withPricingTestDB(t, "legacy_seed_cleanup_wallet")
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fifty := billing.MicroUSD(50 * billing.MicroUSDPerUSD)

	if err := db.Create(&[]User{{Id: 1, Username: "first", Quota: fifty * 2}, {Id: 2, Username: "duplicate", Quota: fifty}}).Error; err != nil {
		t.Fatal(err)
	}
	rows := []WalletLedger{
		// User 1 migrated correctly: the opening balance came first.
		{UserID: 1, DeltaMicrousd: fifty, Kind: "adjustment", RefType: openingBalanceRefType, RefID: "user:1", CreatedAt: base},
		{UserID: 1, DeltaMicrousd: fifty, Kind: WalletLedgerKindTopup, RefType: "topup", RefID: "topup:1", CreatedAt: base.Add(time.Hour)},
		// User 2 signed up later: their topup came first, and the seeder then
		// added an opening balance worth their whole balance.
		{UserID: 2, DeltaMicrousd: fifty, Kind: WalletLedgerKindTopup, RefType: "topup", RefID: "topup:2", CreatedAt: base.Add(time.Hour)},
		{UserID: 2, DeltaMicrousd: fifty, Kind: "adjustment", RefType: openingBalanceRefType, RefID: "user:2", CreatedAt: base.Add(2 * time.Hour)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("failed to create ledger rows: %v", err)
	}

	if err := cleanupLegacyPricingSeeds(db); err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}

	var firstUserOpenings, secondUserOpenings int64
	if err := db.Model(&WalletLedger{}).
		Where("user_id = ? AND ref_type = ?", 1, openingBalanceRefType).
		Count(&firstUserOpenings).Error; err != nil {
		t.Fatalf("failed to count: %v", err)
	}
	if err := db.Model(&WalletLedger{}).
		Where("user_id = ? AND ref_type = ?", 2, openingBalanceRefType).
		Count(&secondUserOpenings).Error; err != nil {
		t.Fatalf("failed to count: %v", err)
	}
	if firstUserOpenings != 1 {
		t.Errorf("expected a genuine first opening balance to survive, got %d", firstUserOpenings)
	}
	if secondUserOpenings != 0 {
		t.Errorf("expected the restated opening balance to be removed, got %d", secondUserOpenings)
	}

	// And no new opening balance is invented for a user who has none.
	var total int64
	if err := db.Model(&WalletLedger{}).Where("ref_type = ?", openingBalanceRefType).Count(&total).Error; err != nil {
		t.Fatalf("failed to count: %v", err)
	}
	if total != 1 {
		t.Errorf("expected exactly one opening balance overall, got %d", total)
	}
}

func TestCleanupLegacyPricingSeedsIsIdempotent(t *testing.T) {
	db := withPricingTestDB(t, "legacy_seed_cleanup_idempotent")
	if err := db.Create(&[]ModelSellPrice{seedPriceRow("PaLM-2"), seedPriceRow("keep-me")}).Error; err != nil {
		t.Fatalf("failed to create seed prices: %v", err)
	}
	if err := db.Create(&Ability{
		Group: ChannelLaneDirect, Model: "keep-me", ChannelId: 1, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("failed to create ability: %v", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := cleanupLegacyPricingSeeds(db); err != nil {
			t.Fatalf("cleanup attempt %d returned error: %v", attempt+1, err)
		}
	}
	names := sellPriceNames(t)
	if names["PaLM-2"] || !names["keep-me"] {
		t.Fatalf("unexpected state after two runs: %v", names)
	}
}

// The delete endpoint and the migration must agree, so the guard is the same
// keep-set asked about one model.
func TestModelSellPriceRemovalBlocker(t *testing.T) {
	db := withPricingTestDB(t, "legacy_seed_removal_blocker")
	if err := db.Create(&Model{
		ModelName: "listed-model", Status: 1, LifecycleStatus: ModelLifecycleActive,
	}).Error; err != nil {
		t.Fatalf("failed to create model: %v", err)
	}
	if err := db.Create(&Ability{
		Group: ChannelLaneDirect, Model: "served-model", ChannelId: 1, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("failed to create ability: %v", err)
	}

	if blocker := ModelSellPriceRemovalBlocker("listed-model"); blocker == nil {
		t.Error("expected a listed model's price to be protected")
	}
	if blocker := ModelSellPriceRemovalBlocker("served-model"); blocker == nil {
		t.Error("expected a served model's price to be protected")
	}
	if blocker := ModelSellPriceRemovalBlocker("PaLM-2"); blocker != nil {
		t.Errorf("expected an unused model to be deletable, got %v", blocker)
	}
}

// An ability parked as inventory is never routed to, so a price kept for it is
// a price no request can need.
func TestCleanupLegacyPricingSeedsIgnoresInventoryAbilities(t *testing.T) {
	db := withPricingTestDB(t, "legacy_seed_cleanup_inventory")
	if err := db.Create(&[]ModelSellPrice{
		seedPriceRow("parked-model"), seedPriceRow("routed-model"),
	}).Error; err != nil {
		t.Fatalf("failed to create seed prices: %v", err)
	}
	if err := db.Create(&[]Ability{
		{Group: ChannelLaneDirect, Model: "parked-model", ChannelId: 1, Enabled: true, Role: AbilityRoleInventory},
		{Group: ChannelLaneDirect, Model: "routed-model", ChannelId: 2, Enabled: true, Role: "primary"},
	}).Error; err != nil {
		t.Fatalf("failed to create abilities: %v", err)
	}

	if err := cleanupLegacyPricingSeeds(db); err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}

	names := sellPriceNames(t)
	if names["parked-model"] {
		t.Error("expected an inventory-only model's seed price to be removed")
	}
	if !names["routed-model"] {
		t.Error("expected a routable model to keep its seed price")
	}
	if blocker := ModelSellPriceRemovalBlocker("parked-model"); blocker != nil {
		t.Errorf("expected inventory not to block deletion, got %v", blocker)
	}
}

// ends_at passing stops the resolver applying a discount without deactivating
// its row, so `active` alone would pin the price forever.
func TestCleanupLegacyPricingSeedsIgnoresExpiredDiscounts(t *testing.T) {
	db := withPricingTestDB(t, "legacy_seed_cleanup_expired")
	if err := db.Create(&[]ModelSellPrice{
		seedPriceRow("expired-discount-model"), seedPriceRow("live-discount-model"),
	}).Error; err != nil {
		t.Fatalf("failed to create seed prices: %v", err)
	}
	past := time.Now().UTC().Add(-24 * time.Hour)
	future := time.Now().UTC().Add(24 * time.Hour)
	createPlanRuleForSeedTest(t, db, PricePlan{Name: "expired", Lanes: PricePlanLanesDirect, Active: true, EndsAt: &past}, "expired-discount-model")
	createPlanRuleForSeedTest(t, db, PricePlan{Name: "live", Lanes: PricePlanLanesDirect, Active: true, EndsAt: &future}, "live-discount-model")

	if err := cleanupLegacyPricingSeeds(db); err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}

	names := sellPriceNames(t)
	if names["expired-discount-model"] {
		t.Error("expected a closed discount window not to pin the price")
	}
	if !names["live-discount-model"] {
		t.Error("expected a discount still in its window to keep the price")
	}
}

// The ledger sweep is a correlated DELETE over the whole spend history. It runs
// once, not on every boot.
func TestCleanupLegacyPricingSeedsRunsOnce(t *testing.T) {
	db := withPricingTestDB(t, "legacy_seed_cleanup_once")
	firstSeed := seedPriceRow("PaLM-2")
	if err := db.Create(&firstSeed).Error; err != nil {
		t.Fatalf("failed to create seed price: %v", err)
	}
	if err := cleanupLegacyPricingSeeds(db); err != nil {
		t.Fatalf("first cleanup returned error: %v", err)
	}
	var marker int64
	if err := db.Model(&SchemaVersion{}).
		Where("scope = ?", legacySeedCleanupScope).Count(&marker).Error; err != nil {
		t.Fatalf("failed to count marker: %v", err)
	}
	if marker != 1 {
		t.Fatalf("expected the cleanup to record completion, got %d markers", marker)
	}

	// A seed row appearing afterwards is left alone: the pass is over.
	lateSeed := seedPriceRow("claude-instant-1")
	if err := db.Create(&lateSeed).Error; err != nil {
		t.Fatalf("failed to create late seed price: %v", err)
	}
	if err := cleanupLegacyPricingSeeds(db); err != nil {
		t.Fatalf("second cleanup returned error: %v", err)
	}
	if !sellPriceNames(t)["claude-instant-1"] {
		t.Error("expected the second run to be a no-op")
	}
}

func TestCleanupPreservesManualGlobalCosts(t *testing.T) {
	db := withPricingTestDB(t, "cleanup_manual_cost")
	rows := []ProviderCostPrice{
		{ModelName: "unused", Provider: "seed", Source: "manual", PricingVersion: 1},
		{ModelName: "unused", Provider: "seed", Source: legacySeedPriceSource, PricingVersion: 2},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := cleanupLegacyPricingSeeds(db); err != nil {
		t.Fatal(err)
	}
	var remaining []ProviderCostPrice
	if err := db.Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Source != "manual" {
		t.Fatalf("manual cost lost: %+v", remaining)
	}
	var floor SchemaVersion
	if err := db.Where("scope = ?", schemaVersionPrimary).First(&floor).Error; err != nil {
		t.Fatal(err)
	}
	if floor.Version <= 9 {
		t.Fatalf("old seeder binaries still allowed: %d", floor.Version)
	}
}

func TestCleanupRollsBackWhenMarkerFails(t *testing.T) {
	db := withPricingTestDB(t, "cleanup_atomic")
	row := seedPriceRow("unused")
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER reject_cleanup_marker BEFORE INSERT ON schema_versions WHEN NEW.scope = 'legacy_seed_cleanup' BEGIN SELECT RAISE(ABORT, 'marker failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := cleanupLegacyPricingSeeds(db); err == nil {
		t.Fatal("expected marker failure")
	}
	if !sellPriceNames(t)["unused"] {
		t.Fatal("cleanup was committed without marker")
	}
	var count int64
	db.Model(&SchemaVersion{}).Where("scope = ?", schemaVersionPrimary).Count(&count)
	if count != 0 {
		t.Fatal("schema floor committed without cleanup")
	}
}

func TestPriceRemovalScopesLaneDependencies(t *testing.T) {
	db := withPricingTestDB(t, "cleanup_lane_guard")
	if err := db.Create(&Model{ModelName: "listed-model", Status: 1, LifecycleStatus: ModelLifecycleActive}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Ability{Group: ChannelLaneDirect, Model: "listed-model", ChannelId: 1, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if reason := ModelSellPriceRemovalBlocker("listed-model", ChannelLaneAgent); reason != nil {
		t.Fatalf("unserved agent lane blocked: %v", reason)
	}
	if reason := ModelSellPriceRemovalBlocker("listed-model", ChannelLaneDirect); reason == nil {
		t.Fatal("listed stable price must stay")
	}
	createPlanRuleForSeedTest(t, db, PricePlan{Name: "stable only", Lanes: PricePlanLanesDirect, Active: true}, "listed-model")
	if reason := ModelSellPriceRemovalBlocker("listed-model", ChannelLaneAgent); reason != nil {
		t.Fatalf("other-lane discount blocked: %v", reason)
	}
	if err := db.Create(&Ability{Group: ChannelLaneAgent, Model: "listed-model", ChannelId: 2, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if reason := ModelSellPriceRemovalBlocker("listed-model", ChannelLaneAgent); reason == nil {
		t.Fatal("served agent lane must stay")
	}
}

func TestCleanupPreservesAmbiguousOpeningBalance(t *testing.T) {
	db := withPricingTestDB(t, "cleanup_ambiguous_wallet")
	if err := db.Create(&User{Id: 3, Username: "ambiguous", Quota: 100}).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	rows := []WalletLedger{
		{UserID: 3, DeltaMicrousd: 40, Kind: WalletLedgerKindTopup, RefType: "topup", RefID: "first", CreatedAt: base},
		{UserID: 3, DeltaMicrousd: 60, Kind: "adjustment", RefType: openingBalanceRefType, RefID: "user:3", CreatedAt: base.Add(time.Second)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := cleanupLegacyPricingSeeds(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&WalletLedger{}).Where("user_id = ?", 3).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal("already reconciled history was damaged")
	}
}

func TestDeletePricesReturnsEveryDeletedVersionAndLane(t *testing.T) {
	db := withPricingTestDB(t, "delete_prices_audit")
	rows := []ModelSellPrice{seedPriceRow("audit-model"), seedPriceRow("audit-model"), seedPriceRow("audit-model"), seedPriceRow("untouched")}
	rows[1].PricingVersion = 2
	rows[2].ChannelGroup = ChannelLaneAgent
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	removed, err := DeleteModelSellPricesForModel("audit-model", ChannelLaneDirect)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || removed[0].ID != rows[0].ID || removed[1].ID != rows[1].ID {
		t.Fatalf("incomplete audit rows: %+v", removed)
	}
	removed, err = DeleteModelSellPricesForModel("audit-model", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].ID != rows[2].ID {
		t.Fatalf("missing lane: %+v", removed)
	}
	if !sellPriceNames(t)["untouched"] {
		t.Fatal("unrelated model deleted")
	}
}

func TestDeletePricesReportsRefreshFailureAndRetries(t *testing.T) {
	db := withPricingTestDB(t, "delete_prices_refresh_failure")
	row := seedPriceRow("refresh-model")
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	previous := activePricingSnapshot.Load()
	t.Cleanup(func() {
		if previous != nil {
			activePricingSnapshot.Store(previous)
		} else {
			activePricingSnapshot.Store((*pricingSnapshot)(nil))
		}
	})
	common.PricingSnapshotEnabled = true
	if err := RefreshPricingSnapshot(); err != nil {
		t.Fatal(err)
	}
	// The snapshot refresh is the only unlocked read of the price table; the
	// delete's own lookup takes a row lock (a FOR clause), so it still passes.
	if err := db.Callback().Query().Before("gorm:query").Register("fail_snapshot_refresh", func(tx *gorm.DB) {
		if _, locking := tx.Statement.Clauses["FOR"]; tx.Statement.Table == "model_sell_prices" && !locking {
			tx.AddError(fmt.Errorf("injected refresh failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := DeleteModelSellPricesForModel("refresh-model", "")
	if err == nil || !strings.Contains(err.Error(), "snapshot refresh failed") {
		t.Fatalf("wanted refresh error, got %v", err)
	}
	if len(removed) != 1 || removed[0].ID != row.ID {
		t.Fatal("committed deletion must remain available for audit")
	}
	if err := db.Callback().Query().Remove("fail_snapshot_refresh"); err != nil {
		t.Fatal(err)
	}
	removed, err = DeleteModelSellPricesForModel("refresh-model", "")
	if err != nil || len(removed) != 0 {
		t.Fatalf("retry failed: %v %+v", err, removed)
	}
	snapshot, ok := currentPricingSnapshot()
	if !ok {
		t.Fatal("snapshot unavailable")
	}
	if _, exists := snapshot.findModelSellPrice("refresh-model", ChannelLaneDirect); exists {
		t.Fatal("deleted price survived retry")
	}
}

func createPlanRuleForSeedTest(t *testing.T, db *gorm.DB, plan PricePlan, modelName string) {
	t.Helper()
	if err := db.Create(&plan).Error; err != nil {
		t.Fatalf("failed to create plan: %v", err)
	}
	rule := PricePlanRule{PlanID: plan.ID, Kind: PricePlanRuleModel, ModelName: modelName, Pct: 10}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("failed to create plan rule: %v", err)
	}
}
