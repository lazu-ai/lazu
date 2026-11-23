package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// ModelSellPriceRemovalBlocker reports why a model's price may not be deleted,
// or nil when it is safe to remove. A requested lane restricts route and
// discount dependencies to that lane. Catalog listing requires direct pricing.
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
	keep, err := sellPriceDependencies(CoreDB(), lane)
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

func sellPriceDependencies(db *gorm.DB, lane string) (map[string]types.ErrorCode, error) {
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
