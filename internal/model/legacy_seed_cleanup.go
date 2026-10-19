package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

/*
Cleanup for the retired built-in price seeder.

Until this migration the process booted with a 192-entry table of hardcoded
model prices and force-wrote every one of them into `model_sell_prices` (and
mirrored them into `provider_cost_prices`) on *every* start. Three consequences
this migration exists to undo:

  - The console's 折扣 matrix lists a row per priced model, so ~160 models the
    product has never served (PaLM-2, claude-instant-1, text-davinci-*, five
    generations of SparkDesk, …) were presented as things an operator could
    price.
  - Nothing could be removed. There is no delete endpoint for a sell price, and
    a row deleted by hand came back on the next boot. Deactivating it did not
    help either: the seeder's equality check covered `Active`, so the next boot
    flipped it back to true.
  - Its wallet counterpart kept issuing `opening_balance` ledger rows. That is
    correct exactly once, when the ledger was introduced and existing users had
    a balance but no history. Left running, a user who signed up and topped up
    between two boots got a *second* row worth their whole balance, so the
    ledger summed to twice the quota and the reconciliation report flagged them
    forever.

What is deliberately NOT done here:

  - A seed row a live model still depends on is kept. A model with no price is
    refused at the relay ("no price configured"), so the keep-set below is
    deliberately generous: any hint that something still uses the model keeps
    the row.
  - No opening-balance row is created. A user whose ledger does not sum to
    their quota now shows up in the reconciliation report, which is what that
    report is for. Inventing a balancing row is the bug this removes.
*/

const legacySeedPriceSource = defaultPricingSeedSource

// legacySeedCleanupScope marks this cleanup as done in schema_versions.
//
// Without it the ledger sweep below would re-run on every master boot: a
// correlated DELETE over the whole of wallet_ledgers, which on an established
// deployment is the request-spend history, with `ref_type` not the leading
// column of any index — all while startup holds the migration lock. Repeating
// full-table work on every boot is the habit this whole change exists to
// break, so this one does not get to keep it either.
const legacySeedCleanupScope = "legacy_seed_cleanup"

// cleanupLegacyPricingSeeds runs on the master migration path only, once. The
// work itself is idempotent, so a deployment that somehow loses the marker
// simply repeats it and finds nothing to remove.
func cleanupLegacyPricingSeeds(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	done, err := legacySeedCleanupDone(db)
	if err != nil {
		return err
	}
	if done {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return applyLegacySeedCleanup(tx)
	})
}

func applyLegacySeedCleanup(db *gorm.DB) error {
	removedPrices, err := deleteUnusedLegacySeedPrices(db)
	if err != nil {
		return err
	}
	removedCosts, err := deleteUnusedLegacySeedCostPrices(db)
	if err != nil {
		return err
	}
	removedLedger, err := deleteDuplicateOpeningBalanceLedger(db)
	if err != nil {
		return err
	}
	if removedPrices+removedCosts+removedLedger > 0 {
		common.SysLog(fmt.Sprintf(
			"legacy pricing seed cleanup: removed %d sell prices, %d provider cost prices, %d duplicate opening-balance ledger rows",
			removedPrices, removedCosts, removedLedger,
		))
	}
	// This is the final migration. Commit the incompatible floor with cleanup;
	// all preceding schema work has succeeded before replicas can see version 10.
	if db.Migrator().HasTable(&SchemaVersion{}) {
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "scope"}},
			DoUpdates: clause.AssignmentColumns([]string{"version", "applied_at"}),
		}).Create(&SchemaVersion{Scope: schemaVersionPrimary, Version: SchemaVersionCurrent, AppliedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
	}
	return markLegacySeedCleanupDone(db)
}

func legacySeedCleanupDone(db *gorm.DB) (bool, error) {
	if !db.Migrator().HasTable(&SchemaVersion{}) {
		return false, nil
	}
	var count int64
	if err := db.Model(&SchemaVersion{}).
		Where("scope = ?", legacySeedCleanupScope).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("check legacy seed cleanup marker: %w", err)
	}
	return count > 0, nil
}

func markLegacySeedCleanupDone(db *gorm.DB) error {
	if !db.Migrator().HasTable(&SchemaVersion{}) {
		return nil
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&SchemaVersion{
		Scope:     legacySeedCleanupScope,
		Version:   1,
		AppliedAt: time.Now().UTC(),
	}).Error
}

// legacySeedKeepSet collects every model name that must keep its seeded price.
// Membership is a union of four independent signals, and both the stored and
// the normalized spelling of each name is inserted so a lookup cannot miss on
// punctuation alone.
func legacySeedKeepSet(db *gorm.DB) (map[string]struct{}, error) {
	keep := make(map[string]struct{})
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		keep[name] = struct{}{}
		if normalized := NormalizePricingModelName(name); normalized != "" {
			keep[normalized] = struct{}{}
		}
	}

	// 1. Listed in the catalog. IsPubliclyListable is the product's own
	//    definition of 上架, reused here rather than re-expressed in SQL: the
	//    retired-name rules behind it are 33 Go patterns that SQL would drift
	//    from the moment either side changed.
	if db.Migrator().HasTable(&Model{}) {
		var models []Model
		if err := db.Select("model_name, status, lifecycle_status").Find(&models).Error; err != nil {
			return nil, err
		}
		for i := range models {
			if models[i].IsPubliclyListable() {
				add(models[i].ModelName)
			}
		}
	}

	// 2. Reachable through an enabled channel, listed or not. A model can be
	//    served while its catalog row is still a candidate. `inventory` is
	//    excluded for the same reason GetEnabledModels excludes it: those
	//    abilities are parked stock, never routed to, so a price kept for one
	//    is a price no request can need.
	if db.Migrator().HasTable(&Ability{}) {
		var served []string
		if err := db.Model(&Ability{}).
			Where("enabled = ? AND role <> ?", true, AbilityRoleInventory).
			Distinct().
			Pluck("model", &served).Error; err != nil {
			return nil, err
		}
		for _, name := range served {
			add(name)
		}
	}

	// 3. Carrying a 专属折扣 model rule. The price is what the discount
	//    applies to, so removing it would leave the rule pointing at nothing.
	discounted, err := planDiscountedModels(db, "")
	if err != nil {
		return nil, err
	}
	for _, name := range discounted {
		add(name)
	}

	// 4. Already priced by someone other than the seeder. The seed row is then
	//    superseded rather than load-bearing, but it costs nothing to keep and
	//    removing it could change which row wins a lane lookup.
	var priced []string
	if err := db.Model(&ModelSellPrice{}).
		Where("source <> ?", legacySeedPriceSource).
		Where("active = ?", true).
		Distinct().
		Pluck("model_name", &priced).Error; err != nil {
		return nil, err
	}
	for _, name := range priced {
		add(name)
	}

	return keep, nil
}

func deleteUnusedLegacySeedPrices(db *gorm.DB) (int64, error) {
	if !db.Migrator().HasTable(&ModelSellPrice{}) {
		return 0, nil
	}
	var seeded []string
	if err := db.Model(&ModelSellPrice{}).
		Where("source = ?", legacySeedPriceSource).
		Distinct().
		Pluck("model_name", &seeded).Error; err != nil {
		return 0, err
	}
	if len(seeded) == 0 {
		return 0, nil
	}
	keep, err := legacySeedKeepSet(db)
	if err != nil {
		return 0, err
	}
	removable := unkeptNames(seeded, keep)
	if len(removable) == 0 {
		return 0, nil
	}
	var removed int64
	for _, batch := range chunkStrings(removable, 200) {
		result := db.Where("source = ?", legacySeedPriceSource).
			Where("model_name IN ?", batch).
			Delete(&ModelSellPrice{})
		if result.Error != nil {
			return removed, result.Error
		}
		removed += result.RowsAffected
	}
	return removed, nil
}

// deleteUnusedLegacySeedCostPrices clears the mirror the seeder wrote into
// provider_cost_prices. Those rows carry provider "seed" and channel 0, which
// the billing path falls back to when a channel has no cost of its own; they
// only feed the upstream-cost and margin figures, never what a customer is
// charged. The same keep-set applies so a served model keeps its fallback.
func deleteUnusedLegacySeedCostPrices(db *gorm.DB) (int64, error) {
	if !db.Migrator().HasTable(&ProviderCostPrice{}) {
		return 0, nil
	}
	var seeded []string
	if err := db.Model(&ProviderCostPrice{}).
		Where("provider = ?", "seed").
		Where("source = ?", legacySeedPriceSource).
		Where("channel_id = ?", 0).
		Distinct().
		Pluck("model_name", &seeded).Error; err != nil {
		return 0, err
	}
	if len(seeded) == 0 {
		return 0, nil
	}
	keep, err := legacySeedKeepSet(db)
	if err != nil {
		return 0, err
	}
	removable := unkeptNames(seeded, keep)
	if len(removable) == 0 {
		return 0, nil
	}
	var removed int64
	for _, batch := range chunkStrings(removable, 200) {
		result := db.Where("provider = ?", "seed").
			Where("source = ?", legacySeedPriceSource).
			Where("channel_id = ?", 0).
			Where("model_name IN ?", batch).
			Delete(&ProviderCostPrice{})
		if result.Error != nil {
			return removed, result.Error
		}
		removed += result.RowsAffected
	}
	return removed, nil
}

// deleteDuplicateOpeningBalanceLedger removes the opening-balance rows the
// seeder wrote for users who already had real ledger history. An opening
// balance is only meaningful as a user's first entry; one written after a topup
// or an adjustment restates a balance that the ledger already accounts for, and
// makes the ledger sum to roughly twice the user's quota.
//
// The user's genuine first opening balance is untouched, and no new one is
// created for a user who lacks one.
func deleteDuplicateOpeningBalanceLedger(db *gorm.DB) (int64, error) {
	if !db.Migrator().HasTable(&WalletLedger{}) {
		return 0, nil
	}
	// A late timestamp alone does not prove duplication. Only remove a row
	// when the remaining history exactly reconciles to the stored balance.
	// Ambiguous accounts stay visible in reconciliation for manual review.
	if !db.Migrator().HasTable(&User{}) {
		return 0, nil
	}
	result := db.
		Where("ref_type = ? AND kind = ? AND delta_microusd <> 0", openingBalanceRefType, "adjustment").
		Where(`EXISTS (
			SELECT 1 FROM users u WHERE u.id = wallet_ledgers.user_id
			AND u.quota = (SELECT COALESCE(SUM(history.delta_microusd), 0)
				FROM wallet_ledgers history WHERE history.user_id = wallet_ledgers.user_id
				AND history.id <> wallet_ledgers.id)
		)`).
		Where(`EXISTS (
			SELECT 1 FROM wallet_ledgers earlier
			WHERE earlier.user_id = wallet_ledgers.user_id
			  AND (earlier.ref_type IS NULL OR earlier.ref_type <> ?)
			  AND (earlier.created_at < wallet_ledgers.created_at
			       OR (earlier.created_at = wallet_ledgers.created_at
			           AND earlier.id < wallet_ledgers.id))
		)`, openingBalanceRefType).
		Delete(&WalletLedger{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func unkeptNames(names []string, keep map[string]struct{}) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, ok := keep[trimmed]; ok {
			continue
		}
		if normalized := NormalizePricingModelName(trimmed); normalized != "" {
			if _, ok := keep[normalized]; ok {
				continue
			}
		}
		out = append(out, name)
	}
	return out
}

func chunkStrings(values []string, size int) [][]string {
	if size <= 0 || len(values) <= size {
		return [][]string{values}
	}
	var out [][]string
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		out = append(out, values[start:end])
	}
	return out
}

// ModelSellPriceRemovalBlocker reports why a model's price may not be deleted,
// or nil when it is safe to remove. It answers the same question the seed
// cleanup asks, for one model and on demand. A requested lane restricts route
// and discount dependencies to that lane; catalog listing requires direct pricing.
//
// The error carries a catalog code naming what still holds the price, so the
// console can tell the operator what to change first.
func ModelSellPriceRemovalBlocker(modelName string, requestedLane ...string) error {
	if CoreDB() == nil {
		return nil
	}
	name := strings.TrimSpace(modelName)
	if name == "" {
		return nil
	}
	lane := ""
	if len(requestedLane) > 0 && strings.TrimSpace(requestedLane[0]) != "" {
		lane = NormalizeChannelLane(requestedLane[0])
		if !IsKnownChannelLane(lane) {
			return types.Err(types.ErrorCodeLaneNotFound, types.D{"lane": requestedLane[0]})
		}
	}
	keep, err := legacySeedKeepSetExcludingPrices(CoreDB(), lane)
	if err != nil {
		// Refuse rather than delete on a failed check.
		return types.WrapErr(fmt.Errorf("check price dependencies of %s: %w", name, err), types.ErrorCodeInternalError)
	}
	if code, blocked := keep[name]; blocked {
		return types.Err(code, types.D{"model": name})
	}
	if normalized := NormalizePricingModelName(name); normalized != "" {
		if code, blocked := keep[normalized]; blocked {
			return types.Err(code, types.D{"model": name})
		}
	}
	return nil
}

// legacySeedKeepSetExcludingPrices is the keep-set without signal 4 (another
// price row exists), which is meaningless when the question is whether to
// delete the price rows themselves. Values carry the reason so the console can
// say what is holding the model rather than just refusing.
func legacySeedKeepSetExcludingPrices(db *gorm.DB, lane string) (map[string]types.ErrorCode, error) {
	keep := make(map[string]types.ErrorCode)
	add := func(name string, reason types.ErrorCode) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, exists := keep[name]; !exists {
			keep[name] = reason
		}
		if normalized := NormalizePricingModelName(name); normalized != "" {
			if _, exists := keep[normalized]; !exists {
				keep[normalized] = reason
			}
		}
	}

	if db.Migrator().HasTable(&Model{}) {
		var models []Model
		if err := db.Select("model_name, status, lifecycle_status").Find(&models).Error; err != nil {
			return nil, err
		}
		for i := range models {
			if models[i].IsPubliclyListable() && (lane == "" || lane == ChannelLaneDirect) {
				add(models[i].ModelName, types.ErrorCodeSellPriceModelListed)
			}
		}
	}

	if db.Migrator().HasTable(&Ability{}) {
		var served []string
		query := db.Model(&Ability{}).Where("enabled = ? AND role <> ?", true, AbilityRoleInventory)
		if lane != "" {
			query = query.Where("\"group\" = ?", lane)
		}
		if err := query.Distinct().
			Pluck("model", &served).Error; err != nil {
			return nil, err
		}
		for _, name := range served {
			add(name, types.ErrorCodeSellPriceModelServed)
		}
	}

	discounted, err := planDiscountedModels(db, lane)
	if err != nil {
		return nil, err
	}
	for _, name := range discounted {
		add(name, types.ErrorCodeSellPriceModelDiscounted)
	}

	return keep, nil
}

// planDiscountedModels lists the models a live price plan names in a model
// rule, limited to plans that cover lane when lane is set. A plan whose window
// has closed no longer holds its models.
func planDiscountedModels(db *gorm.DB, lane string) ([]string, error) {
	if !db.Migrator().HasTable(&PricePlan{}) || !db.Migrator().HasTable(&PricePlanRule{}) {
		return nil, nil
	}
	query := db.Table("price_plan_rules AS r").
		Joins("JOIN price_plans AS p ON p.id = r.plan_id").
		Where("r.kind = ? AND r.pct > 0 AND r.model_name <> ''", PricePlanRuleModel).
		Where("p.active = ?", true).
		Where("p.ends_at IS NULL OR p.ends_at > ?", time.Now().UTC())
	if lane != "" {
		query = query.Where("p.lanes IN ?", []string{PricePlanLanesAll, lane})
	}
	var names []string
	if err := query.Distinct().Pluck("r.model_name", &names).Error; err != nil {
		return nil, err
	}
	return names, nil
}
