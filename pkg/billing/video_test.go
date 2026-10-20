package billing

import "testing"

func microUSD(usd float64) *MicroUSD {
	value := USD(usd)
	return &value
}

func TestVideoLinesPriceByResolutionTier(t *testing.T) {
	flat := 0.05
	sheet := PriceSheet{
		SecondMicrousd: microUSD(0.03),
		Video:          &VideoPrices{SecondUSD: &flat, ResolutionSecondUSD: map[string]float64{"1080p": 0.1}},
	}
	quote := Quote(Usage{Video: &VideoUsage{Seconds: 5, Resolution: "1080p"}}, sheet, nil)
	if quote.TotalMicrousd != USD(0.5) || len(quote.LineItems) != 1 || quote.LineItems[0].Dimension != VideoDimensionSecond {
		t.Fatalf("1080p tier: %+v", quote)
	}
	quote = Quote(Usage{Video: &VideoUsage{Seconds: 5, Resolution: "720p"}}, sheet, nil)
	if quote.TotalMicrousd != USD(0.25) {
		t.Fatalf("tier fallback to the video second price: %+v", quote)
	}
	quote = Quote(Usage{Video: &VideoUsage{Seconds: 5, Resolution: "720p"}}, PriceSheet{SecondMicrousd: microUSD(0.03)}, nil)
	if quote.TotalMicrousd != USD(0.15) {
		t.Fatalf("flat second price unchanged for a sheet without video prices: %+v", quote)
	}
	quote = Quote(Usage{Seconds: 5}, PriceSheet{SecondMicrousd: microUSD(0.03)}, nil)
	if quote.TotalMicrousd != USD(0.15) {
		t.Fatalf("plain seconds still price as before: %+v", quote)
	}
}

func TestVideoLinesPreferProviderCostThenTokens(t *testing.T) {
	multiplier := 1.25
	cost := 0.4
	sheet := PriceSheet{
		OutputPerMtokMicrousd: microUSD(2),
		SecondMicrousd:        microUSD(0.03),
		Video:                 &VideoPrices{ProviderCostMultiplier: &multiplier},
	}
	quote := Quote(Usage{Video: &VideoUsage{Seconds: 5, OutputTokens: 108000, ProviderCostUSD: &cost}}, sheet, nil)
	if quote.TotalMicrousd != USD(0.5) || quote.LineItems[0].Dimension != VideoDimensionProviderCost {
		t.Fatalf("provider cost × multiplier wins: %+v", quote)
	}
	quote = Quote(Usage{Video: &VideoUsage{Seconds: 5, OutputTokens: 108000}}, sheet, nil)
	if quote.TotalMicrousd != USD(0.216) || quote.LineItems[0].Dimension != VideoDimensionOutputTokens {
		t.Fatalf("tokens × per-Mtok price next: %+v", quote)
	}
	quote = Quote(Usage{Video: &VideoUsage{Seconds: 5}}, sheet, nil)
	if quote.TotalMicrousd != USD(0.15) {
		t.Fatalf("seconds last: %+v", quote)
	}
}

func TestVideoLinesAudioSurchargeAndMissingPrice(t *testing.T) {
	ratio := 1.2
	sheet := PriceSheet{SecondMicrousd: microUSD(0.1), Video: &VideoPrices{AudioSurchargeRatio: &ratio}}
	quote := Quote(Usage{Video: &VideoUsage{Seconds: 10, Audio: true}}, sheet, nil)
	if quote.TotalMicrousd != USD(1.2) {
		t.Fatalf("audio surcharge: %+v", quote)
	}
	quote = Quote(Usage{Video: &VideoUsage{Seconds: 10}}, PriceSheet{}, nil)
	if quote.TotalMicrousd != 0 || len(quote.MissingPriceDimensions) != 1 || quote.MissingPriceDimensions[0] != VideoDimensionSecond {
		t.Fatalf("no price reports the missing dimension: %+v", quote)
	}
}

func TestVideoPricesValidateAndScale(t *testing.T) {
	bad := -1.0
	if err := (&VideoPrices{SecondUSD: &bad}).Validate(); err == nil {
		t.Fatal("negative price must be refused")
	}
	price := 0.5
	scaled := (&VideoPrices{SecondUSD: &price, ResolutionSecondUSD: map[string]float64{"4k": 2}}).Scaled(2)
	if *scaled.SecondUSD != 1 || scaled.ResolutionSecondUSD["4k"] != 4 {
		t.Fatalf("scaled: %+v", scaled)
	}
	if !(&VideoPrices{}).IsEmpty() || (&VideoPrices{ProviderCostAuthoritative: true}).IsEmpty() {
		t.Fatal("emptiness")
	}
}

func TestVideoLinesProviderCostMultiplierByResolution(t *testing.T) {
	flat := 1.2
	cost := 1.0
	sheet := PriceSheet{Video: &VideoPrices{
		ProviderCostMultiplier:             &flat,
		ProviderCostMultiplierByResolution: map[string]float64{"720P": 1.07, "1080p": 1.48},
	}}
	cases := map[string]MicroUSD{"720p": USD(1.07), "1080p": USD(1.48), "480p": USD(1.2)}
	for resolution, want := range cases {
		quote := Quote(Usage{Video: &VideoUsage{Seconds: 5, Resolution: resolution, ProviderCostUSD: &cost}}, sheet, nil)
		if quote.TotalMicrousd != want || quote.LineItems[0].Dimension != VideoDimensionProviderCost {
			t.Fatalf("%s: a tier's own multiplier, else the flat one: %+v", resolution, quote)
		}
	}
	onlyTiers := PriceSheet{Video: &VideoPrices{ProviderCostMultiplierByResolution: map[string]float64{"720p": 1.07}}}
	quote := Quote(Usage{Video: &VideoUsage{Seconds: 5, Resolution: "480p", ProviderCostUSD: &cost}}, onlyTiers, nil)
	if quote.TotalMicrousd != USD(1) {
		t.Fatalf("an unlisted tier with no flat multiplier bills the cost at 1x: %+v", quote)
	}
}

func TestVideoLinesBillReferenceSecondsWithTheOutput(t *testing.T) {
	sheet := PriceSheet{Video: &VideoPrices{ResolutionSecondUSD: map[string]float64{"720p": 0.231}}}
	quote := Quote(Usage{Video: &VideoUsage{Seconds: 5, ReferenceSeconds: 30, Resolution: "720p"}}, sheet, nil)
	if quote.TotalMicrousd != USD(0.231*35) || quote.LineItems[0].Quantity != 35 {
		t.Fatalf("a hold covers 5 s of output and 30 s of reference: %+v", quote)
	}
}

func TestVideoPricesValidateMultiplierTiersAndPublicCopy(t *testing.T) {
	for _, bad := range []map[string]float64{{"720p": 0}, {"720p": -1}, {"720p": 101}, {" ": 1.1}} {
		if err := (&VideoPrices{ProviderCostMultiplierByResolution: bad}).Validate(); err == nil {
			t.Fatalf("multiplier tiers %v must be refused", bad)
		}
	}
	flat := 1.07
	audio := 1.5
	prices := &VideoPrices{
		ResolutionSecondUSD:                map[string]float64{"720p": 0.231},
		AudioSurchargeRatio:                &audio,
		ProviderCostMultiplier:             &flat,
		ProviderCostAuthoritative:          true,
		ProviderCostMultiplierByResolution: map[string]float64{"1080p": 1.48},
	}
	if err := prices.Validate(); err != nil {
		t.Fatalf("valid prices refused: %v", err)
	}
	public := prices.Public()
	if public.ProviderCostMultiplier != nil || public.ProviderCostAuthoritative || len(public.ProviderCostMultiplierByResolution) != 0 {
		t.Fatalf("the public copy must not publish the markup: %+v", public)
	}
	if !public.ProviderCostBilled || public.ResolutionSecondUSD["720p"] != 0.231 || public.AudioSurchargeRatio == nil {
		t.Fatalf("the public copy keeps per-second prices and says it follows cost: %+v", public)
	}
	if (&VideoPrices{ProviderCostBilled: true}).IsEmpty() {
		t.Fatalf("a public cost-billed copy is a price")
	}
	if (&VideoPrices{ResolutionSecondUSD: map[string]float64{"720p": 0.231}}).Public().ProviderCostBilled {
		t.Fatalf("a per-second lane is not cost-billed")
	}
}
