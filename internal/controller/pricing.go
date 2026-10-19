package controller

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

func GetPricing(c *gin.Context) {
	pricing := applyPricingLaneServiceability(model.GetPricing())
	response := gin.H{
		"success":            true,
		"data":               pricing,
		"vendors":            model.GetVendors(),
		"pricing_summary":    buildPricingSummary(pricing),
		"supported_endpoint": model.GetSupportedEndpointMap(),
	}
	if user := currentPricingUser(c); user != nil {
		rpm, tpm := setting.AccountRateLimit(user.HasToppedUp, user.RPMLimit, user.TPMLimit)
		response["rate_limit_ceiling"] = gin.H{"rpm": rpm, "tpm": tpm, "enabled": setting.ModelRequestRateLimitEnabled}
		// A customer on a price plan sees it here so the model page can show
		// their price beside the official and standard ones.
		response["price_plan"] = pricePlanViewFor(user.PricePlanID)
	}
	c.JSON(200, response)
}

// applyPricingLaneServiceability stamps each priced lane with what it can
// serve right now. A priced lane with no channel left is reported unavailable
// rather than dropped: the reader has to tell "we never sold it on this lane"
// apart from "this lane has nothing to route to today".
func applyPricingLaneServiceability(rows []model.Pricing) []model.Pricing {
	serviceability := model.GetLaneServiceability()
	result := make([]model.Pricing, 0, len(rows))
	for _, row := range rows {
		if len(row.Lanes) == 0 {
			result = append(result, row)
			continue
		}
		lanes := make([]model.PricingLane, 0, len(row.Lanes))
		for _, lane := range row.Lanes {
			state := serviceability[row.ModelName][model.NormalizeChannelLane(lane.ChannelGroup)]
			lane.Available = state.Available
			lane.ClientOnly = state.ClientOnly
			lanes = append(lanes, lane)
		}
		row.Lanes = lanes
		result = append(result, row)
	}
	return result
}

func buildPricingSummary(rows []model.Pricing) gin.H {
	total := len(rows)
	configured := 0
	missingModels := make([]string, 0)
	for _, row := range rows {
		if row.HasPrice {
			configured++
			continue
		}
		missingModels = append(missingModels, row.ModelName)
	}
	return gin.H{
		"total":          total,
		"configured":     configured,
		"missing":        total - configured,
		"missing_models": missingModels,
	}
}

func currentPricingUser(c *gin.Context) *model.UserBase {
	userID := c.GetInt("id")
	if userID <= 0 {
		return nil
	}
	user, err := model.GetUserCache(userID)
	if err != nil {
		return nil
	}
	return user
}

func GetModelSellPrices(c *gin.Context) {
	rows, err := model.ListModelSellPrices()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func UpsertModelSellPrice(c *gin.Context) {
	var payload model.ModelSellPrice
	if err := c.ShouldBindJSON(&payload); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	before, _ := model.GetActiveModelSellPrice(payload.ModelName, payload.ChannelGroup)
	if err := model.UpsertActiveModelSellPrice(payload); err != nil {
		httperr.Write(c, err)
		return
	}
	model.RefreshPricing()
	model.WriteAdminAuditLog(c, "pricing.sell_price.upsert", "pricing_sell_price", fmt.Sprintf("%s:%s", payload.ModelName, payload.ChannelGroup), payload.ModelName, before, payload)
	common.ApiSuccess(c, nil)
}

// DeleteModelSellPrice removes a model's stored prices. `lane` narrows it to
// one channel group; omitting it removes the model's prices in every lane.
//
// The request is refused while anything still depends on the price, because a
// model with no price is rejected at the relay rather than billed at zero. The
// error code names what is still holding it so the operator can act on it.
func DeleteModelSellPrice(c *gin.Context) {
	modelName := strings.TrimSpace(c.Query("model_name"))
	if modelName == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "model_name"})
		return
	}
	lane := strings.TrimSpace(c.Query("lane"))
	if blocker := model.ModelSellPriceRemovalBlocker(modelName, lane); blocker != nil {
		httperr.Write(c, blocker)
		return
	}
	before, err := model.DeleteModelSellPricesForModel(modelName, lane)
	removed := len(before)
	if removed > 0 {
		model.WriteAdminAuditLog(c, "pricing.sell_price.delete", "pricing_sell_price", fmt.Sprintf("%s:%s", modelName, lane), modelName, gin.H{"prices": before}, gin.H{"removed": removed})
	}
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.RefreshPricing()
	common.ApiSuccess(c, gin.H{"removed": removed})
}

func GetWalletLedgerReconciliationMismatches(c *gin.Context) {
	limit := 100
	if rawLimit := c.Query("limit"); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit < 0 {
			httperr.Invalid(c, "limit")
			return
		}
		limit = parsedLimit
	}
	rows, err := model.ListWalletLedgerReconciliationMismatches(limit)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}
