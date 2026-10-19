package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// ModelOpsPoolWeight is one pool-local routing weight in a model tier save.
type ModelOpsPoolWeight struct {
	ChannelID int
	Weight    uint
}

// ModelOpsTierConfig groups the price and routing changes for one lane. A nil
// Price means the caller is only changing pool weights.
type ModelOpsTierConfig struct {
	Lane    string
	Price   *ModelSellPrice
	Weights []ModelOpsPoolWeight
}

// SaveModelOpsTierConfigurations commits every changed lane in one transaction.
// This prevents a row save from publishing a new price while leaving only some
// pool weights updated (or vice versa) when a later write fails.
func SaveModelOpsTierConfigurations(modelName string, tiers []ModelOpsTierConfig) error {
	if CoreDB() == nil {
		return fmt.Errorf("database is not initialized")
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "model"})
	}

	prepared := make([]ModelOpsTierConfig, 0, len(tiers))
	seenLanes := make(map[string]struct{}, len(tiers))
	pricingChanged := false
	weightsChanged := false
	for _, tier := range tiers {
		lane := NormalizeChannelLane(tier.Lane)
		if !IsKnownChannelLane(lane) {
			return types.Err(types.ErrorCodeLaneNotFound, types.D{"lane": tier.Lane})
		}
		if _, exists := seenLanes[lane]; exists {
			return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "tiers"})
		}
		seenLanes[lane] = struct{}{}
		tier.Lane = lane
		if tier.Price != nil {
			price := *tier.Price
			price.ID = 0
			price.ModelName = NormalizePricingModelName(modelName)
			price.ChannelGroup = lane
			price.Active = true
			price.PricingVersion = 0
			price.CreatedAt = time.Time{}
			price.UpdatedAt = time.Time{}
			if strings.TrimSpace(price.Source) == "" || price.Source == SellPriceSourceOfficialFollow {
				price.Source = "manual"
			}
			// A following row's derived fields are computed here, never taken
			// from the browser.
			derived, err := PrepareFollowingSellPrice(CoreDB(), price, modelName)
			if err != nil {
				return err
			}
			price = derived
			if err := validateModelSellPrice(price); err != nil {
				return err
			}
			tier.Price = &price
			pricingChanged = true
		}
		if len(tier.Weights) > 0 {
			weightsChanged = true
		}
		prepared = append(prepared, tier)
	}
	if !pricingChanged && !weightsChanged {
		return nil
	}

	version := time.Now().UnixNano()
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		priceIndex := int64(0)
		for _, tier := range prepared {
			if tier.Price != nil {
				price := *tier.Price
				price.PricingVersion = version + priceIndex
				priceIndex++
				if err := tx.Model(&ModelSellPrice{}).
					Where("model_name = ? AND channel_group = ? AND active = ?", price.ModelName, tier.Lane, true).
					Update("active", false).Error; err != nil {
					return err
				}
				if err := tx.Create(&price).Error; err != nil {
					return err
				}
			}
			for _, weight := range tier.Weights {
				var ability Ability
				if err := tx.Where("model = ? AND channel_id = ?", modelName, weight.ChannelID).
					Where(abilityGroupCol()+" = ?", tier.Lane).
					First(&ability).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return types.Err(types.ErrorCodeChannelRouteNotFound, types.D{"channel_id": weight.ChannelID, "model": modelName, "lane": tier.Lane})
					}
					return err
				}
				if err := tx.Model(&ability).Update("weight", weight.Weight).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if pricingChanged {
		if refreshErr := RefreshPricingSnapshot(); refreshErr != nil {
			common.SysLog("failed to refresh pricing snapshot after model ops tier save: " + refreshErr.Error())
		}
	}
	if weightsChanged {
		InitChannelCache()
	}
	return nil
}
