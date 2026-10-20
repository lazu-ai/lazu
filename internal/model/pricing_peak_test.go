package model

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestHourInUTCRanges(t *testing.T) {
	cases := []struct {
		ranges string
		hour   int
		want   bool
	}{
		// DeepSeek peak windows: UTC 01:00-04:00 and 06:00-10:00
		{"1-4,6-10", 0, false},
		{"1-4,6-10", 1, true},
		{"1-4,6-10", 3, true},
		{"1-4,6-10", 4, false}, // end is exclusive
		{"1-4,6-10", 5, false},
		{"1-4,6-10", 6, true},
		{"1-4,6-10", 9, true},
		{"1-4,6-10", 10, false},
		{"1-4,6-10", 23, false},
		// wraps midnight
		{"22-2", 23, true},
		{"22-2", 0, true},
		{"22-2", 1, true},
		{"22-2", 2, false},
		{"22-2", 12, false},
		// end may reach 24 (midnight)
		{"20-24", 23, true},
		{"20-24", 20, true},
		// malformed / empty / out-of-domain are ignored (no near-all-day surcharge)
		{"", 5, false},
		{"garbage", 5, false},
		{"5", 5, false},
		{"1--1", 12, false}, // negative end must not enter the wrap branch
		{"1--1", 0, false},
		{"25-1", 0, false}, // start out of domain
		{"25-1", 12, false},
		{"-3-5", 4, false},
	}
	for _, c := range cases {
		if got := hourInUTCRanges(c.hour, c.ranges); got != c.want {
			t.Errorf("hourInUTCRanges(%d, %q)=%v want %v", c.hour, c.ranges, got, c.want)
		}
	}
}

func TestPeakFactorAt(t *testing.T) {
	mult := 2.0
	price := ModelSellPrice{PeakHoursUTC: "1-4,6-10", PeakMultiplier: &mult}

	peak := time.Date(2026, 7, 20, 2, 30, 0, 0, time.UTC) // 02:30 UTC → peak
	off := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)  // 12:00 UTC → off-peak
	if f := price.PeakFactorAt(peak); f != 2.0 {
		t.Fatalf("expected peak factor 2.0, got %v", f)
	}
	if f := price.PeakFactorAt(off); f != 1.0 {
		t.Fatalf("expected off-peak factor 1.0, got %v", f)
	}

	// A non-UTC clock still resolves by its UTC hour: 10:30 Beijing = 02:30 UTC.
	beijing := time.FixedZone("CST", 8*3600)
	if f := price.PeakFactorAt(time.Date(2026, 7, 20, 10, 30, 0, 0, beijing)); f != 2.0 {
		t.Fatalf("expected peak factor 2.0 for 10:30 Beijing (=02:30 UTC), got %v", f)
	}

	// Unconfigured price never scales.
	if f := (ModelSellPrice{}).PeakFactorAt(peak); f != 1.0 {
		t.Fatalf("expected unconfigured factor 1.0, got %v", f)
	}
}

func TestToBillingPriceSheetAtAppliesPeak(t *testing.T) {
	in, out := 0.16, 0.32
	mult := 2.0
	price := ModelSellPrice{
		ModelName:      "deepseek-v4-flash",
		InputPerMtok:   &in,
		OutputPerMtok:  &out,
		PeakHoursUTC:   "1-4,6-10",
		PeakMultiplier: &mult,
	}

	peak := time.Date(2026, 7, 20, 2, 0, 0, 0, time.UTC)
	off := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	offSheet := price.ToBillingPriceSheetAt(off)
	peakSheet := price.ToBillingPriceSheetAt(peak)

	wantOffIn := *usdToMicrousdPtr(&in)
	if *offSheet.InputPerMtokMicrousd != wantOffIn {
		t.Fatalf("off-peak input should equal base, got %d want %d", *offSheet.InputPerMtokMicrousd, wantOffIn)
	}
	if *peakSheet.InputPerMtokMicrousd != 2*wantOffIn {
		t.Fatalf("peak input should double base, got %d want %d", *peakSheet.InputPerMtokMicrousd, 2*wantOffIn)
	}
	wantOffOut := *usdToMicrousdPtr(&out)
	if *peakSheet.OutputPerMtokMicrousd != 2*wantOffOut {
		t.Fatalf("peak output should double base, got %d want %d", *peakSheet.OutputPerMtokMicrousd, 2*wantOffOut)
	}
}

func TestToBillingPriceSheetBackwardCompatible(t *testing.T) {
	// No peak config → sheet identical to the plain conversion at any time.
	in, out := 0.16, 0.32
	price := ModelSellPrice{ModelName: "x", InputPerMtok: &in, OutputPerMtok: &out}
	sheet := price.ToBillingPriceSheetAt(time.Date(2026, 7, 20, 2, 0, 0, 0, time.UTC))
	if *sheet.InputPerMtokMicrousd != *usdToMicrousdPtr(&in) {
		t.Fatal("unconfigured price must not be scaled")
	}
	_ = billing.PriceSheet{}
}

func TestDeepSeekSellAndCostCalendars(t *testing.T) {
	in, out, cache, mult := 0.15, 0.6, 0.003, 2.0
	for _, name := range []string{"deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp", "deepseek-v4-pro"} {
		sell := ModelSellPrice{ModelName: name, InputPerMtok: &in, OutputPerMtok: &out, CacheReadPerMtok: &cache, PeakHoursUTC: "1-4,6-10", PeakMultiplier: &mult}
		cost := ProviderCostPrice{ModelName: name, Provider: "deepseek", InputPerMtok: &in, OutputPerMtok: &out, CacheReadPerMtok: &cache}
		for _, tt := range []struct {
			day    int
			factor int64
		}{{1, 1}, {10, 1}, {17, 1}, {20, 2}} {
			at := time.Date(2026, 10, tt.day, 2, 0, 0, 0, time.UTC)
			for _, sheet := range []billing.PriceSheet{sell.ToBillingPriceSheetAt(at), cost.ToBillingPriceSheetAt(at)} {
				if *sheet.InputPerMtokMicrousd != billing.MicroUSD(150000*tt.factor) || *sheet.OutputPerMtokMicrousd != billing.MicroUSD(600000*tt.factor) || *sheet.CacheReadPerMtokMicrousd != billing.MicroUSD(3000*tt.factor) {
					t.Fatalf("%s day %d: %+v", name, tt.day, sheet)
				}
			}
		}
		cost.Provider = "third-party"
		if got := cost.ToBillingPriceSheetAt(time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)); *got.InputPerMtokMicrousd != 150000 {
			t.Fatal("third-party fixed cost changed")
		}
	}
}
