package billing

import "github.com/shopspring/decimal"

const MicroUSDPerUSD int64 = 1_000_000
const TokensPerMtok int64 = 1_000_000

type Usage struct {
	Images             *ImageUsage
	InputTokens        int64
	OutputTokens       int64
	CacheReadTokens    int64
	CacheWrite5mTokens int64
	CacheWrite1hTokens int64
	AudioInputTokens   int64
	AudioOutputTokens  int64
	ImageInputTokens   int64
	ImageOutputTokens  int64
	RequestCount       int64
	Seconds            int64
	ExtraLineItems     []LineItem
}

type PriceSheet struct {
	Images                      *ImagePrices
	ModelName                   string
	InputPerMtokMicrousd        *MicroUSD
	OutputPerMtokMicrousd       *MicroUSD
	CacheReadPerMtokMicrousd    *MicroUSD
	CacheWrite5mPerMtokMicrousd *MicroUSD
	CacheWrite1hPerMtokMicrousd *MicroUSD
	AudioInputPerMtokMicrousd   *MicroUSD
	AudioOutputPerMtokMicrousd  *MicroUSD
	ImageInputPerMtokMicrousd   *MicroUSD
	ImageOutputPerMtokMicrousd  *MicroUSD
	RequestMicrousd             *MicroUSD
	SecondMicrousd              *MicroUSD
	PricingVersion              int64
}

type Adjustment struct {
	Source      string
	Priority    int
	DiscountPct decimal.Decimal
	Active      bool
}

type Discount struct {
	Source         string          `json:"source,omitempty"`
	Pct            decimal.Decimal `json:"pct"`
	AmountMicrousd MicroUSD        `json:"amount_microusd"`
	// Plan describes a 专属折扣 (price plan) discount; nil for any other.
	Plan *PlanDiscountApplied `json:"plan,omitempty"`
}

// PlanDiscountApplied snapshots the price-plan rule a request was billed
// under, so editing the plan later does not rewrite the receipt.
type PlanDiscountApplied struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Rule    string `json:"rule"`
	Subject string `json:"subject,omitempty"`
}

type LineItem struct {
	Dimension      string   `json:"dimension"`
	Quantity       int64    `json:"quantity"`
	UnitMicrousd   MicroUSD `json:"unit_microusd"`
	AmountMicrousd MicroUSD `json:"amount_microusd"`
}

type QuoteResult struct {
	LineItems              []LineItem `json:"line_items"`
	MissingPriceDimensions []string   `json:"missing_price_dimensions,omitempty"`
	SubtotalMicrousd       MicroUSD   `json:"subtotal_microusd"`
	Discount               Discount   `json:"discount"`
	TotalMicrousd          MicroUSD   `json:"total_microusd"`
	PricingVersion         int64      `json:"pricing_version"`
	// ContextTier is set when the request's input passed the model's
	// long-context threshold and was billed at the long-context prices.
	ContextTier *ContextTierApplied `json:"context_tier,omitempty"`
	// Official is what the same usage would have cost at the model vendor's
	// published list price, snapshotted when the request was billed so a
	// later list-price change does not rewrite old receipts.
	Official *OfficialComparison `json:"official,omitempty"`
	// Peak is set when the request was billed inside the model's peak hours;
	// every unit price in LineItems already carries the multiplier.
	Peak *PeakApplied `json:"peak,omitempty"`
}

// PeakApplied records the time-of-day multiplier a request was priced at.
type PeakApplied struct {
	Multiplier float64 `json:"multiplier"`
}

// OfficialComparison prices a quote's usage at the vendor's list price.
type OfficialComparison struct {
	TotalMicrousd MicroUSD `json:"total_microusd"`
}

// OfficialPrices are a model's published list prices per Mtok; nil where
// the lab publishes none.
type OfficialPrices struct {
	Input      *MicroUSD
	Output     *MicroUSD
	CacheRead  *MicroUSD
	CacheWrite *MicroUSD // the lab's single cache-write price (5-minute)
	// LengthAware says the prices were already chosen for the request's
	// input length under the lab's own long-context tiers, so a quote billed
	// at our long-context tier can still be compared honestly.
	LengthAware bool
}

// CompareOfficial prices the quote's input, output and cache lines at the
// official list prices. Audio, image and tool-call lines count at what was
// charged for them, so the comparison can only understate the saving. It
// returns nil when the quote cannot be compared honestly: a long-context tier
// the list prices were not chosen for (see LengthAware), a minimum charge, a billed token line
// with no list price (counting it at our own price is what once showed a
// cache-heavy request on an 18%-of-list lane as 61% off), or no input/output
// line at all.
func CompareOfficial(q QuoteResult, official OfficialPrices) *OfficialComparison {
	if q.ContextTier != nil && !official.LengthAware {
		return nil
	}
	// A request billed the one-microUSD minimum has token lines priced at zero;
	// comparing those against list price would claim a ~100% saving.
	for _, item := range q.LineItems {
		if item.Dimension == minimumChargeDimension {
			return nil
		}
	}
	total := MicroUSD(0)
	compared := false
	for _, item := range q.LineItems {
		var unit *MicroUSD
		switch item.Dimension {
		case "input":
			unit = official.Input
			compared = true
		case "output":
			unit = official.Output
			compared = true
		case "cache_read":
			unit = official.CacheRead
		case "cache_write_5m":
			unit = official.CacheWrite
		case "cache_write_1h":
			// No catalogue publishes the one-hour write price.
			unit = nil
		default:
			total += item.AmountMicrousd
			continue
		}
		if unit == nil || *unit <= 0 {
			return nil
		}
		total += tokenAmountMicrousd(item.Quantity, *unit)
	}
	if !compared {
		return nil
	}
	return &OfficialComparison{TotalMicrousd: total}
}

// ContextTierApplied records which long-context tier priced a request.
type ContextTierApplied struct {
	Threshold   int64 `json:"threshold"`
	InputTokens int64 `json:"input_tokens"`
}

// InputTokenTotal is every token the request sent: fresh text, cache reads
// and writes, audio and image input. Context tiers are judged on it.
func (u Usage) InputTokenTotal() int64 {
	return u.InputTokens + u.CacheReadTokens + u.CacheWrite5mTokens + u.CacheWrite1hTokens +
		u.AudioInputTokens + u.ImageInputTokens
}

const minimumChargeDimension = "minimum_charge"

func Quote(usage Usage, price PriceSheet, adjustments []Adjustment) QuoteResult {
	lineItems := make([]LineItem, 0, 10)
	missingPriceDimensions := make([]string, 0, 4)
	hasSubMicrousdBillableUsage := false
	addTokenLine := func(dimension string, quantity int64, unit *MicroUSD) {
		if quantity <= 0 {
			return
		}
		if unit == nil {
			missingPriceDimensions = append(missingPriceDimensions, dimension)
			return
		}
		amount := tokenAmountMicrousd(quantity, *unit)
		if amount == 0 && *unit > 0 {
			hasSubMicrousdBillableUsage = true
		}
		lineItems = append(lineItems, LineItem{
			Dimension:      dimension,
			Quantity:       quantity,
			UnitMicrousd:   *unit,
			AmountMicrousd: amount,
		})
	}
	addUnitLine := func(dimension string, quantity int64, unit *MicroUSD) {
		if quantity <= 0 || unit == nil {
			return
		}
		amount := MicroUSD(decimal.NewFromInt(quantity).Mul(decimal.NewFromInt(unit.Int64())).Round(0).IntPart())
		lineItems = append(lineItems, LineItem{
			Dimension:      dimension,
			Quantity:       quantity,
			UnitMicrousd:   *unit,
			AmountMicrousd: amount,
		})
	}

	addTokenLine("input", usage.InputTokens, price.InputPerMtokMicrousd)
	addTokenLine("output", usage.OutputTokens, price.OutputPerMtokMicrousd)
	addTokenLine("cache_read", usage.CacheReadTokens, price.CacheReadPerMtokMicrousd)
	addTokenLine("cache_write_5m", usage.CacheWrite5mTokens, price.CacheWrite5mPerMtokMicrousd)
	addTokenLine("cache_write_1h", usage.CacheWrite1hTokens, price.CacheWrite1hPerMtokMicrousd)
	addTokenLine("audio_input", usage.AudioInputTokens, price.AudioInputPerMtokMicrousd)
	addTokenLine("audio_output", usage.AudioOutputTokens, price.AudioOutputPerMtokMicrousd)
	addTokenLine("image_input", usage.ImageInputTokens, price.ImageInputPerMtokMicrousd)
	addTokenLine("image_output", usage.ImageOutputTokens, price.ImageOutputPerMtokMicrousd)
	addUnitLine("request", usage.RequestCount, price.RequestMicrousd)
	addUnitLine("second", usage.Seconds, price.SecondMicrousd)
	imageItems, imageMissing := imageLines(usage.Images, price.Images)
	lineItems = append(lineItems, imageItems...)
	missingPriceDimensions = append(missingPriceDimensions, imageMissing...)
	for _, item := range usage.ExtraLineItems {
		if item.Quantity <= 0 {
			continue
		}
		if item.AmountMicrousd == 0 {
			if item.UnitMicrousd == 0 {
				continue
			}
			item.AmountMicrousd = MicroUSD(decimal.NewFromInt(item.Quantity).
				Mul(decimal.NewFromInt(item.UnitMicrousd.Int64())).
				Round(0).
				IntPart())
		}
		if item.AmountMicrousd == 0 && item.UnitMicrousd > 0 {
			hasSubMicrousdBillableUsage = true
		}
		if item.AmountMicrousd == 0 {
			continue
		}
		lineItems = append(lineItems, item)
	}

	subtotal := MicroUSD(0)
	for _, item := range lineItems {
		subtotal += item.AmountMicrousd
	}
	if subtotal == 0 && hasSubMicrousdBillableUsage {
		lineItems = append(lineItems, LineItem{
			Dimension:      minimumChargeDimension,
			Quantity:       1,
			UnitMicrousd:   1,
			AmountMicrousd: 1,
		})
		subtotal = 1
	}

	// A discount (or, when negative, a surcharge) applies to the whole order:
	// cache, audio, image and per-call lines included. An account discount is
	// sold as "OpenAI 6折", not "6折 on the text tokens only".
	discount := pickAdjustment(adjustments)
	discountAmount := MicroUSD(decimal.NewFromInt(subtotal.Int64()).
		Mul(discount.DiscountPct).
		Div(decimal.NewFromInt(100)).
		Round(0).
		IntPart())
	total := subtotal - discountAmount
	if total < 0 {
		total = 0
	}

	return QuoteResult{
		LineItems:              lineItems,
		MissingPriceDimensions: missingPriceDimensions,
		SubtotalMicrousd:       subtotal,
		Discount: Discount{
			Source:         discount.Source,
			Pct:            discount.DiscountPct,
			AmountMicrousd: discountAmount,
		},
		TotalMicrousd:  total,
		PricingVersion: price.PricingVersion,
	}
}

func tokenAmountMicrousd(tokens int64, perMtokMicrousd MicroUSD) MicroUSD {
	return MicroUSD(decimal.NewFromInt(tokens).
		Mul(decimal.NewFromInt(perMtokMicrousd.Int64())).
		Div(decimal.NewFromInt(TokensPerMtok)).
		Round(0).
		IntPart())
}

func pickAdjustment(adjustments []Adjustment) Adjustment {
	result := Adjustment{DiscountPct: decimal.Zero}
	for _, adjustment := range adjustments {
		if !adjustment.Active {
			continue
		}
		if result.Source == "" || adjustment.Priority > result.Priority {
			result = adjustment
		}
	}
	return result
}

// ApplyPlanDiscount prices a quote under a price plan (专属折扣). The plan's
// pct is taken off the vendor's official list price, never off the lane's
// already-discounted price, and never lifts a line above what the lane
// charges everyone: each line costs min(lane price, official × (1 − pct)).
// A line with no official price to anchor on (cache writes, images, per-call
// fees) takes the pct off the lane price instead. The plan replaces, never
// stacks on, any other discount in the quote.
func ApplyPlanDiscount(q QuoteResult, official *OfficialPrices, pct decimal.Decimal, plan PlanDiscountApplied) QuoteResult {
	if q.SubtotalMicrousd <= 0 || !pct.IsPositive() {
		return q
	}
	anchored := official != nil && (q.ContextTier == nil || official.LengthAware)
	total := MicroUSD(0)
	for _, item := range q.LineItems {
		if item.Dimension == minimumChargeDimension {
			total += item.AmountMicrousd
			continue
		}
		var unit *MicroUSD
		if anchored {
			switch item.Dimension {
			case "input":
				unit = official.Input
			case "output":
				unit = official.Output
			case "cache_read":
				unit = official.CacheRead
			case "cache_write_5m":
				unit = official.CacheWrite
			}
		}
		base := item.AmountMicrousd
		if unit != nil && *unit > 0 {
			base = tokenAmountMicrousd(item.Quantity, *unit)
		}
		line := MicroUSD(PlanDiscountPrice(decimal.NewFromInt(item.AmountMicrousd.Int64()), decimal.NewFromInt(base.Int64()), pct).Round(0).IntPart())
		total += line
	}
	if total >= q.SubtotalMicrousd {
		q.Discount = Discount{Pct: decimal.Zero}
		q.TotalMicrousd = q.SubtotalMicrousd
		return q
	}
	q.Discount = Discount{
		Source:         "price_plan",
		Pct:            pct,
		AmountMicrousd: q.SubtotalMicrousd - total,
		Plan:           &plan,
	}
	q.TotalMicrousd = total
	return q
}

// PlanDiscountPrice is shared by catalog projections and receipt settlement.
// The caller picks the official price for the input length (or lane fallback).
func PlanDiscountPrice(lane, anchor, pct decimal.Decimal) decimal.Decimal {
	if !pct.IsPositive() {
		return lane
	}
	keep := decimal.Max(decimal.Zero, decimal.NewFromInt(1).Sub(pct.Div(decimal.NewFromInt(100))))
	return decimal.Min(lane, anchor.Mul(keep))
}
