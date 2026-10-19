package controller

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/model"
)

func TestCatalogPlanLongContextUsesOfficialPriceAndLaneCeiling(t *testing.T) {
	for _, tc := range []struct {
		name       string
		lane, want float64
	}{
		{"official anchor", 9, 6},
		{"lane ceiling", 3, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pricing := model.Pricing{
				InputPerMtok: float64Ptr(4.5), CacheReadPerMtok: float64Ptr(2),
				ContextTier: &model.ContextTierPrice{Threshold: 200_000, InputPerMtok: &tc.lane},
			}
			official := model.ModelOfficialPrice{
				InputPerToken: 5, CacheReadPerMtok: 2.5,
				ContextTier: &model.ContextTierPrice{Threshold: 200_000, InputPerMtok: float64Ptr(10)},
			}
			got := catalogPlanPrices(pricing, official, 40, 1)
			assertFloatPtr(t, "tier input", got.ContextTier.Input, tc.want)
			// Both prices inherit their base cache price; the plan must still
			// anchor on official ($2.50), not multiply the lane's $2 by 0.6.
			assertFloatPtr(t, "inherited cache", got.ContextTier.CacheRead, 1.5)
			assertFloatPtr(t, "base input", got.Input, 3)
			assertFloatPtr(t, "source tier unchanged", pricing.ContextTier.InputPerMtok, tc.lane)
		})
	}
}

func TestCatalogPlanUnknownOfficialTierFallsBackToLane(t *testing.T) {
	pricing := model.Pricing{
		InputPerMtok: float64Ptr(4),
		ContextTier:  &model.ContextTierPrice{Threshold: 200_000, InputPerMtok: float64Ptr(9)},
	}
	got := catalogPlanPrices(pricing, model.ModelOfficialPrice{InputPerToken: 5}, 50, 1)
	assertFloatPtr(t, "tier input", got.ContextTier.Input, 4.5)
}

func TestCatalogPlanIndependentThresholdsAndPeak(t *testing.T) {
	pricing := model.Pricing{InputPerMtok: float64Ptr(9), PeakMultiplier: float64Ptr(2), ContextTier: &model.ContextTierPrice{Threshold: 200_000, InputPerMtok: float64Ptr(18)}}
	official := model.ModelOfficialPrice{InputPerToken: 10, ContextTier: &model.ContextTierPrice{Threshold: 100_000, InputPerMtok: float64Ptr(20)}}
	got := catalogPlanPrices(pricing, official, 40, 1)
	assertFloatPtr(t, "base", got.Input, 6)
	if len(got.ContextTiers) != 2 || got.ContextTiers[0].Threshold != 100_000 || got.ContextTiers[1].Threshold != 200_000 {
		t.Fatalf("missing boundary: %+v", got.ContextTiers)
	}
	assertFloatPtr(t, "official boundary capped by lane", got.ContextTiers[0].Input, 9)
	assertFloatPtr(t, "both tiered", got.ContextTiers[1].Input, 12)
	assertFloatPtr(t, "peak does not multiply plan", got.PeakPricing.Input, 6)
	assertFloatPtr(t, "peak relaxes lane cap", got.PeakPricing.ContextTiers[0].Input, 12)
}
