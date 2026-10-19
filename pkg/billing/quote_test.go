package billing

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestQuoteBuildsLineItemsAndTotals(t *testing.T) {
	input := MicroUSD(2_000_000)
	output := MicroUSD(10_000_000)
	cacheRead := MicroUSD(200_000)
	request := MicroUSD(25)

	got := Quote(
		Usage{
			InputTokens:     1_000,
			OutputTokens:    500,
			CacheReadTokens: 250,
			RequestCount:    2,
		},
		PriceSheet{
			ModelName:                "model-a",
			InputPerMtokMicrousd:     &input,
			OutputPerMtokMicrousd:    &output,
			CacheReadPerMtokMicrousd: &cacheRead,
			RequestMicrousd:          &request,
			PricingVersion:           7,
		},
		[]Adjustment{
			{Source: "tier", Priority: 10, DiscountPct: decimal.NewFromInt(5), Active: true},
			{Source: "enterprise", Priority: 20, DiscountPct: decimal.NewFromInt(10), Active: true},
		},
	)

	if got.SubtotalMicrousd != 7100 {
		t.Fatalf("expected subtotal 7100, got %d", got.SubtotalMicrousd)
	}
	if got.Discount.Source != "enterprise" {
		t.Fatalf("expected enterprise discount, got %q", got.Discount.Source)
	}
	if got.Discount.AmountMicrousd != 710 {
		t.Fatalf("expected discount 710, got %d", got.Discount.AmountMicrousd)
	}
	if got.TotalMicrousd != 6390 {
		t.Fatalf("expected total 6390, got %d", got.TotalMicrousd)
	}
	if got.PricingVersion != 7 {
		t.Fatalf("expected pricing version 7, got %d", got.PricingVersion)
	}
	if len(got.LineItems) != 4 {
		t.Fatalf("expected 4 line items, got %#v", got.LineItems)
	}
}

func TestQuoteRecordsMissingPriceDimensions(t *testing.T) {
	input := MicroUSD(2_000_000)
	got := Quote(
		Usage{
			InputTokens:        1,
			CacheWrite5mTokens: 1_000,
		},
		PriceSheet{
			InputPerMtokMicrousd: &input,
		},
		nil,
	)

	if got.SubtotalMicrousd != 2 {
		t.Fatalf("expected only input charge, got %d", got.SubtotalMicrousd)
	}
	if len(got.LineItems) != 1 {
		t.Fatalf("expected 1 line item, got %#v", got.LineItems)
	}
	if len(got.MissingPriceDimensions) != 1 || got.MissingPriceDimensions[0] != "cache_write_5m" {
		t.Fatalf("expected missing cache_write_5m price, got %#v", got.MissingPriceDimensions)
	}
}

func TestQuoteIncludesExtraLineItems(t *testing.T) {
	request := MicroUSD(2_000)
	got := Quote(
		Usage{
			RequestCount: 1,
			ExtraLineItems: []LineItem{
				{
					Dimension:    "web_search",
					Quantity:     2,
					UnitMicrousd: 500,
				},
			},
		},
		PriceSheet{
			ModelName:       "extra-model",
			RequestMicrousd: &request,
		},
		nil,
	)

	if got.TotalMicrousd != 3_000 {
		t.Fatalf("expected request plus extra line total 3000, got %d", got.TotalMicrousd)
	}
	if len(got.LineItems) != 2 || got.LineItems[1].Dimension != "web_search" {
		t.Fatalf("expected extra line item to be preserved, got %#v", got.LineItems)
	}
}

func TestQuoteAddsMinimumChargeForSubMicrousdBillableUsage(t *testing.T) {
	input := MicroUSD(12_000)
	got := Quote(
		Usage{InputTokens: 12},
		PriceSheet{
			ModelName:            "tiny-embedding-model",
			InputPerMtokMicrousd: &input,
			PricingVersion:       3,
		},
		nil,
	)

	if got.SubtotalMicrousd != 1 || got.TotalMicrousd != 1 {
		t.Fatalf("expected tiny billable usage to charge 1 microusd, got subtotal=%d total=%d quote=%#v", got.SubtotalMicrousd, got.TotalMicrousd, got)
	}
	if len(got.LineItems) != 2 {
		t.Fatalf("expected usage line plus minimum charge line, got %#v", got.LineItems)
	}
	if got.LineItems[0].Dimension != "input" || got.LineItems[0].AmountMicrousd != 0 {
		t.Fatalf("expected original input line to preserve rounded amount 0, got %#v", got.LineItems[0])
	}
	if got.LineItems[1].Dimension != "minimum_charge" || got.LineItems[1].AmountMicrousd != 1 {
		t.Fatalf("expected minimum charge line, got %#v", got.LineItems[1])
	}
}

func TestQuoteIncludesNegativeExtraLineItems(t *testing.T) {
	request := MicroUSD(2_000)
	got := Quote(
		Usage{
			RequestCount: 1,
			ExtraLineItems: []LineItem{
				{
					Dimension:      "price_adjustment",
					Quantity:       1,
					UnitMicrousd:   -500,
					AmountMicrousd: -500,
				},
			},
		},
		PriceSheet{
			ModelName:       "discounted-task",
			RequestMicrousd: &request,
		},
		nil,
	)

	if got.TotalMicrousd != 1_500 {
		t.Fatalf("expected negative extra line item to reduce total to 1500, got %d", got.TotalMicrousd)
	}
	if len(got.LineItems) != 2 || got.LineItems[1].AmountMicrousd != -500 {
		t.Fatalf("expected negative extra line item to be preserved, got %#v", got.LineItems)
	}
}

func TestQuoteIncludesPerSecondPricing(t *testing.T) {
	second := MicroUSD(300_000)
	got := Quote(
		Usage{Seconds: 4},
		PriceSheet{
			ModelName:      "sora-2",
			SecondMicrousd: &second,
		},
		nil,
	)

	if got.TotalMicrousd != 1_200_000 {
		t.Fatalf("expected 4 seconds at 300000 microusd to charge 1200000, got %d", got.TotalMicrousd)
	}
	if len(got.LineItems) != 1 || got.LineItems[0].Dimension != "second" {
		t.Fatalf("expected one second line item, got %#v", got.LineItems)
	}
}

func TestQuoteDiscountAppliesToTheWholeOrder(t *testing.T) {
	input := MicroUSD(2_000_000)
	output := MicroUSD(10_000_000)
	cacheRead := MicroUSD(200_000)
	audioInput := MicroUSD(6_000_000)
	request := MicroUSD(25)

	got := Quote(
		Usage{
			InputTokens:      1_000,
			OutputTokens:     500,
			CacheReadTokens:  250,
			AudioInputTokens: 100,
			RequestCount:     2,
		},
		PriceSheet{
			ModelName:                 "model-a",
			InputPerMtokMicrousd:      &input,
			OutputPerMtokMicrousd:     &output,
			CacheReadPerMtokMicrousd:  &cacheRead,
			AudioInputPerMtokMicrousd: &audioInput,
			RequestMicrousd:           &request,
		},
		[]Adjustment{
			{Source: "tier3", Priority: 10, DiscountPct: decimal.NewFromInt(10), Active: true},
		},
	)

	if got.SubtotalMicrousd != 7_700 {
		t.Fatalf("expected subtotal 7700, got %d", got.SubtotalMicrousd)
	}
	// 10% of every line: input, output, cache read, audio and per-call.
	if got.Discount.AmountMicrousd != 770 {
		t.Fatalf("expected discount on the whole order to be 770, got %d", got.Discount.AmountMicrousd)
	}
	if got.TotalMicrousd != 6_930 {
		t.Fatalf("expected total 6930, got %d", got.TotalMicrousd)
	}
}

func TestQuoteDiscountReachesCacheHeavyAgentRequests(t *testing.T) {
	// gpt-6-luna on the stable lane with a 60% key: 3 fresh input, 12,514
	// cache read, 390 cache write, 95 output. It once charged 203 of 222
	// (9% off) when only input/output were discounted.
	input, output := MicroUSD(100_000), MicroUSD(500_000)
	cacheRead, cacheWrite := MicroUSD(10_000), MicroUSD(125_000)
	got := Quote(
		Usage{InputTokens: 3, OutputTokens: 95, CacheReadTokens: 12_514, CacheWrite5mTokens: 390},
		PriceSheet{
			InputPerMtokMicrousd:        &input,
			OutputPerMtokMicrousd:       &output,
			CacheReadPerMtokMicrousd:    &cacheRead,
			CacheWrite5mPerMtokMicrousd: &cacheWrite,
		},
		[]Adjustment{{Source: "key", Priority: 10, DiscountPct: decimal.NewFromInt(40), Active: true}},
	)
	if got.SubtotalMicrousd != 222 {
		t.Fatalf("expected subtotal 222, got %d", got.SubtotalMicrousd)
	}
	if got.TotalMicrousd != 133 {
		t.Fatalf("expected 60%% of 222 = 133, got %d", got.TotalMicrousd)
	}
}

func TestQuoteAllowsSurchargeAdjustment(t *testing.T) {
	input := MicroUSD(1_000_000)
	got := Quote(
		Usage{InputTokens: 1_000},
		PriceSheet{
			ModelName:            "surcharge-model",
			InputPerMtokMicrousd: &input,
		},
		[]Adjustment{
			{Source: "group_surcharge", Priority: 10, DiscountPct: decimal.NewFromInt(-20), Active: true},
		},
	)

	if got.TotalMicrousd != 1_200 {
		t.Fatalf("expected negative discount pct to preserve 20%% surcharge, got %d", got.TotalMicrousd)
	}
	if got.Discount.AmountMicrousd != -200 {
		t.Fatalf("expected surcharge to be represented as negative discount amount, got %d", got.Discount.AmountMicrousd)
	}
}

func TestQuoteBillsImageOutputSeparatelyFromTextOutput(t *testing.T) {
	// gemini-3.1-flash-image: $3/Mtok text output, $60/Mtok image output.
	output := MicroUSD(3_000_000)
	imageOutput := MicroUSD(60_000_000)

	got := Quote(
		Usage{
			OutputTokens:      20,
			ImageOutputTokens: 1_120,
		},
		PriceSheet{
			ModelName:                  "gemini-3.1-flash-image",
			OutputPerMtokMicrousd:      &output,
			ImageOutputPerMtokMicrousd: &imageOutput,
			PricingVersion:             1,
		},
		nil,
	)

	var imageAmount, textAmount MicroUSD
	for _, item := range got.LineItems {
		switch item.Dimension {
		case "image_output":
			imageAmount = item.AmountMicrousd
		case "output":
			textAmount = item.AmountMicrousd
		}
	}
	// 1120 tokens * $60/Mtok = 67200 microUSD ($0.0672 per 1K image)
	if imageAmount != 67_200 {
		t.Fatalf("expected image_output amount 67200, got %d", imageAmount)
	}
	if textAmount != 60 {
		t.Fatalf("expected output amount 60, got %d", textAmount)
	}
	if got.TotalMicrousd != 67_260 {
		t.Fatalf("expected total 67260, got %d", got.TotalMicrousd)
	}
}

func TestQuoteFlagsUnpricedImageOutput(t *testing.T) {
	got := Quote(
		Usage{ImageOutputTokens: 1_120},
		PriceSheet{ModelName: "gemini-3.1-flash-image", PricingVersion: 1},
		nil,
	)
	if len(got.MissingPriceDimensions) != 1 || got.MissingPriceDimensions[0] != "image_output" {
		t.Fatalf("expected missing image_output dimension, got %v", got.MissingPriceDimensions)
	}
}

func TestCompareOfficialPricesEveryTokenLineAtListPrice(t *testing.T) {
	// The receipt that read 61%: a lane at 18% of list ($1.80 / $0.18 / $9)
	// on a cache-heavy request. At list ($10 / $1 / $50) it is 82% off.
	in, out, cache := MicroUSD(1_800_000), MicroUSD(9_000_000), MicroUSD(180_000)
	quote := Quote(Usage{InputTokens: 8_529, OutputTokens: 321, CacheReadTokens: 190_208}, PriceSheet{
		InputPerMtokMicrousd:     &in,
		OutputPerMtokMicrousd:    &out,
		CacheReadPerMtokMicrousd: &cache,
	}, nil)
	listIn, listOut, listCache := MicroUSD(10_000_000), MicroUSD(50_000_000), MicroUSD(1_000_000)
	got := CompareOfficial(quote, OfficialPrices{Input: &listIn, Output: &listOut, CacheRead: &listCache})
	if got == nil {
		t.Fatal("expected a comparison")
	}
	if want := MicroUSD(85_290 + 16_050 + 190_208); got.TotalMicrousd != want {
		t.Fatalf("official total = %d, want %d", got.TotalMicrousd, want)
	}
	// The receipt rounds (official − charged) / official.
	if pct := ((got.TotalMicrousd-quote.TotalMicrousd)*1000/got.TotalMicrousd + 5) / 10; pct != 82 {
		t.Fatalf("saving = %d%%, want 82%%", pct)
	}
}

func TestCompareOfficialCarriesNonTokenLines(t *testing.T) {
	in, out, req := MicroUSD(1_000_000), MicroUSD(2_000_000), MicroUSD(5_000)
	quote := Quote(Usage{InputTokens: 1_000, OutputTokens: 1_000, RequestCount: 1}, PriceSheet{
		InputPerMtokMicrousd:  &in,
		OutputPerMtokMicrousd: &out,
		RequestMicrousd:       &req,
	}, nil)
	listIn, listOut := MicroUSD(2_000_000), MicroUSD(4_000_000)
	got := CompareOfficial(quote, OfficialPrices{Input: &listIn, Output: &listOut})
	if got == nil || got.TotalMicrousd != 2_000+4_000+5_000 {
		t.Fatalf("official total = %v, want 11000", got)
	}
}

func TestCompareOfficialRefusesWhatItCannotCompare(t *testing.T) {
	in, out := MicroUSD(1_000_000), MicroUSD(2_000_000)
	quote := Quote(Usage{InputTokens: 1_000, OutputTokens: 1_000}, PriceSheet{
		InputPerMtokMicrousd:  &in,
		OutputPerMtokMicrousd: &out,
	}, nil)
	if CompareOfficial(quote, OfficialPrices{Input: &in}) != nil {
		t.Fatal("an output line without a list price must not be compared")
	}
	tiered := quote
	tiered.ContextTier = &ContextTierApplied{Threshold: 272_000, InputTokens: 300_000}
	if CompareOfficial(tiered, OfficialPrices{Input: &in, Output: &out}) != nil {
		t.Fatal("long-context tier must not be compared against a single list price")
	}
	tiny := MicroUSD(100)
	minimum := Quote(Usage{InputTokens: 3, OutputTokens: 2}, PriceSheet{
		InputPerMtokMicrousd:  &tiny,
		OutputPerMtokMicrousd: &tiny,
	}, nil)
	if CompareOfficial(minimum, OfficialPrices{Input: &in, Output: &out}) != nil {
		t.Fatal("a minimum-charge quote must not claim a saving")
	}
	req := MicroUSD(5_000)
	perCall := Quote(Usage{RequestCount: 1}, PriceSheet{RequestMicrousd: &req}, nil)
	if CompareOfficial(perCall, OfficialPrices{Input: &in, Output: &out}) != nil {
		t.Fatal("a per-call quote has nothing to compare")
	}
	cache := MicroUSD(100_000)
	cached := Quote(Usage{InputTokens: 1_000, OutputTokens: 1_000, CacheReadTokens: 50_000}, PriceSheet{
		InputPerMtokMicrousd:     &in,
		OutputPerMtokMicrousd:    &out,
		CacheReadPerMtokMicrousd: &cache,
	}, nil)
	if CompareOfficial(cached, OfficialPrices{Input: &in, Output: &out}) != nil {
		t.Fatal("a cache line without a list price must not be counted at our price")
	}
	write1h := MicroUSD(2_000_000)
	longWrite := Quote(Usage{InputTokens: 1_000, OutputTokens: 1_000, CacheWrite1hTokens: 5_000}, PriceSheet{
		InputPerMtokMicrousd:        &in,
		OutputPerMtokMicrousd:       &out,
		CacheWrite1hPerMtokMicrousd: &write1h,
	}, nil)
	if CompareOfficial(longWrite, OfficialPrices{Input: &in, Output: &out, CacheRead: &cache, CacheWrite: &write1h}) != nil {
		t.Fatal("a one-hour cache write has no list price to compare")
	}
}
