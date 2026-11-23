package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func seedPriceRow(name string) ModelSellPrice {
	input := 1.0
	return ModelSellPrice{
		ModelName:      name,
		ChannelGroup:   ChannelLaneDirect,
		InputPerMtok:   &input,
		OutputPerMtok:  &input,
		Source:         "fixture",
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
