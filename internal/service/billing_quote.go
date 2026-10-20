package service

import (
	"encoding/json"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"github.com/lazu-ai/lazu/pkg/pricecalendar"
	"github.com/shopspring/decimal"
)

func QuoteModelUsage(relayInfo *relaycommon.RelayInfo, modelName string, usage billing.Usage, extraLineItems []billing.LineItem) billing.QuoteResult {
	return quoteModelUsageWithInputTotal(relayInfo, modelName, usage, extraLineItems, usage.InputTokenTotal())
}

// Tier selection uses all input tokens, even when some are priced separately.
func quoteModelUsageWithInputTotal(relayInfo *relaycommon.RelayInfo, modelName string, usage billing.Usage, extraLineItems []billing.LineItem, inputTokens int64) billing.QuoteResult {
	usage.ExtraLineItems = append(usage.ExtraLineItems, extraLineItems...)

	var sheet billing.PriceSheet
	var tier *billing.ContextTierApplied
	var peak *billing.PeakApplied
	if sellPrice, ok := model.GetActiveModelSellPrice(modelName, relayInfo.UsingGroup); ok {
		price, tiered := sellPrice.ForInputTokens(inputTokens)
		if tiered {
			tier = &billing.ContextTierApplied{Threshold: sellPrice.ContextTierThreshold, InputTokens: inputTokens}
		}
		now := time.Now()
		if pricecalendar.IsDeepSeek(modelName) && !relayInfo.StartTime.IsZero() {
			now = relayInfo.StartTime
		}
		sheet = price.ToBillingPriceSheetAt(now)
		if factor := price.PeakFactorAt(now); factor != 1 {
			peak = &billing.PeakApplied{Multiplier: factor}
		}
	}
	quote := billing.Quote(usage, sheet, nil)
	quote.ContextTier = tier
	quote.Peak = peak
	if quote.TotalMicrousd <= 0 {
		return quote
	}
	var officialPrices *billing.OfficialPrices
	if official, ok := model.GetModelOfficialPrice(modelName); ok {
		prices := officialPricesForInput(official, inputTokens)
		officialPrices = &prices
		quote.Official = billing.CompareOfficial(quote, prices)
	}
	if plan, ok := model.ResolvePlanDiscount(relayInfo.PricePlanID, relayInfo.UsingGroup, modelName); ok {
		quote = billing.ApplyPlanDiscount(quote, officialPrices, plan.Pct, billing.PlanDiscountApplied{
			ID: plan.PlanID, Name: plan.PlanName, Rule: plan.Rule, Subject: plan.Subject,
		})
	}
	return quote
}

// officialPricesForInput picks the lab's list prices for a request of this
// input length: its long-context tier past the lab's own threshold (which
// need not match ours), the base prices otherwise. A tier dimension the lab
// leaves out keeps its base price.
func officialPricesForInput(official model.ModelOfficialPrice, inputTokens int64) billing.OfficialPrices {
	prices := billing.OfficialPrices{
		Input:      perTokenUSDToPerMtokMicrousd(official.InputPerToken),
		Output:     perTokenUSDToPerMtokMicrousd(official.OutputPerToken),
		CacheRead:  usdToMicrousdPtr(official.CacheReadPerMtok),
		CacheWrite: usdToMicrousdPtr(official.CacheWritePerMtok),
	}
	tier := official.ContextTier
	if tier == nil || tier.Threshold <= 0 {
		return prices
	}
	prices.LengthAware = true
	if inputTokens <= tier.Threshold {
		return prices
	}
	pick := func(long *float64, base *billing.MicroUSD) *billing.MicroUSD {
		if long == nil {
			return base
		}
		return usdToMicrousdPtr(*long)
	}
	prices.Input = pick(tier.InputPerMtok, prices.Input)
	prices.Output = pick(tier.OutputPerMtok, prices.Output)
	prices.CacheRead = pick(tier.CacheReadPerMtok, prices.CacheRead)
	prices.CacheWrite = pick(tier.CacheWrite5mPerMtok, prices.CacheWrite)
	return prices
}

// officialPerTokenCeiling separates the catalogue's two units (see
// model.OfficialPerTokenCeiling).
const officialPerTokenCeiling = model.OfficialPerTokenCeiling

// perTokenUSDToPerMtokMicrousd converts a catalogue price to the billing
// sheet's unit (microUSD per million tokens).
func perTokenUSDToPerMtokMicrousd(price float64) *billing.MicroUSD {
	if price <= 0 {
		return nil
	}
	perMtokUSD := decimal.NewFromFloat(price)
	if price < officialPerTokenCeiling {
		perMtokUSD = perMtokUSD.Mul(decimal.NewFromInt(billing.TokensPerMtok))
	}
	value := billing.MicroUSD(perMtokUSD.
		Mul(decimal.NewFromInt(billing.MicroUSDPerUSD)).
		Round(0).
		IntPart())
	return &value
}

func QuoteToQuota(quote billing.QuoteResult) billing.MicroUSD {
	if quote.TotalMicrousd > 0 {
		return quote.TotalMicrousd
	}
	if quote.SubtotalMicrousd > 0 {
		return 0
	}
	for _, item := range quote.LineItems {
		if item.Quantity > 0 && item.UnitMicrousd > 0 {
			return 1
		}
	}
	return quote.TotalMicrousd
}

func QuotaDecimalToMicrousd(quota decimal.Decimal) billing.MicroUSD {
	if quota.LessThanOrEqual(decimal.Zero) {
		return 0
	}
	return billing.MicroUSD(quota.Round(0).IntPart())
}

func QuoteLineItemsJSON(quote billing.QuoteResult) string {
	if len(quote.LineItems) == 0 &&
		len(quote.MissingPriceDimensions) == 0 &&
		quote.Discount.Source == "" &&
		quote.TotalMicrousd == 0 {
		return ""
	}
	raw, err := json.Marshal(quote)
	if err != nil {
		return ""
	}
	return string(raw)
}

func usdToMicrousdPtr(value float64) *billing.MicroUSD {
	if value <= 0 {
		return nil
	}
	microusd := billing.MicroUSD(decimal.NewFromFloat(value).
		Mul(decimal.NewFromInt(billing.MicroUSDPerUSD)).
		Round(0).
		IntPart())
	return &microusd
}
