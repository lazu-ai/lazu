package model

import (
	"math"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/dto"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestSeedreamPricesAndUsageSurvivePersistence(t *testing.T) {
	db := withPricingTestDB(t, "seedream_pricing_roundtrip")
	if err := db.AutoMigrate(&Channel{}); err != nil {
		t.Fatal(err)
	}
	hi, layer, highLayer, ref := .09, .0225, .045, .003
	p := &billing.ImagePrices{OutputUSD: .045, PixelThreshold: 2610000, HighResolutionOutputUSD: &hi, LayerUSD: &layer, HighResolutionLayerUSD: &highLayer, ReferenceUSD: &ref, FreeReferences: 1}
	row := ModelSellPrice{ModelName: "seedream-5.0-pro", ChannelGroup: "direct", RequestUSD: &p.OutputUSD, ImagePricing: p, Active: true}
	if err := UpsertActiveModelSellPrice(row); err != nil {
		t.Fatal(err)
	}
	var saved ModelSellPrice
	if err := db.Where("model_name = ? AND active = ?", row.ModelName, true).First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.ImagePricing == nil || *saved.ImagePricing.LayerUSD != .0225 {
		t.Fatalf("lost tiers: %+v", saved)
	}
	// Both cropped and full-size layers must survive the async event's JSON.
	fact := usagepkg.FactFromDTO(&dto.Usage{TotalTokens: 100, ImageUsage: &billing.ImageUsage{InputImages: 2, Outputs: []billing.ImageOutput{{Pixels: 2610000, Layer: true}, {Pixels: 2610001, Layer: true}}}})
	parsed, ok := usagepkg.ParseFact(fact.JSONString())
	if !ok || parsed.ImageUsage == nil {
		t.Fatalf("lost image units: %+v", parsed)
	}
	units := usageFactToBillingUsage(parsed)
	if units.RequestCount != 0 || units.OutputTokens != 0 {
		t.Fatal("images billed twice")
	}
	q := billing.Quote(units, saved.ToBillingPriceSheet(), nil)
	if q.TotalMicrousd != 70500 {
		t.Fatalf("retail total: %+v", q)
	}
	cost := ProviderCostPrice{ModelName: row.ModelName, ChannelID: 42, Provider: "morsinna", ImagePricing: p.Scaled(.8)}
	if err := db.Create(&cost).Error; err != nil {
		t.Fatal(err)
	}
	costUSD, ok := resolveUpstreamCostUSDAt(RecordConsumeLogParams{ChannelId: 42, ModelName: row.ModelName, Other: map[string]interface{}{}}, parsed, time.Now())
	if !ok || math.Abs(costUSD-.0564) > 1e-9 {
		t.Fatalf("wrong provider cost: %.9f %v", costUSD, ok)
	}
	// A normal admin edit cannot silently change just the displayed base price.
	wrong := .04
	saved.RequestUSD = &wrong
	if err := validateModelSellPrice(saved); err == nil {
		t.Fatal("allowed contradictory headline and image prices")
	}
}
