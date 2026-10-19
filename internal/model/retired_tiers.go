package model

import (
	"fmt"

	"gorm.io/gorm"
)

// retiredTierColumns held the spend tier (users.group), its manual lock and
// its downgrade clock. The tiers' discounts moved to price plans on
// 2026-10-01 (PR #351), so nothing reads these columns any more.
var retiredTierColumns = []string{"group", "tier_locked", "tier_downgrade_eligible_at"}

func dropRetiredTierColumns(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	for _, column := range retiredTierColumns {
		if !db.Migrator().HasColumn(&User{}, column) {
			continue
		}
		if err := db.Migrator().DropColumn(&User{}, column); err != nil {
			return fmt.Errorf("drop retired tier column users.%s: %w", column, err)
		}
	}
	return nil
}

// retiredPricingAdjustmentsTable held the spend tiers' per-model discounts.
// Its rows became price plans and were deactivated with PR #351; the table
// was exported before this drop.
const retiredPricingAdjustmentsTable = "pricing_adjustments"

func dropRetiredPricingAdjustments(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(retiredPricingAdjustmentsTable) {
		return nil
	}
	if err := db.Migrator().DropTable(retiredPricingAdjustmentsTable); err != nil {
		return fmt.Errorf("drop retired table %s: %w", retiredPricingAdjustmentsTable, err)
	}
	return nil
}
