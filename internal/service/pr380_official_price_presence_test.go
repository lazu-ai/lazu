package service

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
)

func TestPR380OfficialPricePresenceRequiresAnObservedMatchingField(t *testing.T) {
	for _, item := range []struct {
		name            string
		input           float64
		cost            *ModelsDevCost
		source          string
		locked          bool
		wantInputProof  bool
		wantOutputProof bool
		wantCacheProof  bool
	}{
		{"explicit_zero", 2, &ModelsDevCost{Input: float(0), CacheRead: float(0)}, "", false, true, false, true},
		{"omitted_input", 0, &ModelsDevCost{Output: float(6)}, "", false, false, true, false},
		{"same_explicit_zero", 0, &ModelsDevCost{Input: float(0)}, "", false, true, false, false},
		{"positive_within_comparison_tolerance", 0, &ModelsDevCost{Input: float(1e-9)}, "", false, true, false, false},
		{"no_cost_metadata", 0, nil, "", false, false, false, false},
		{"manual_price_not_applied", 2, &ModelsDevCost{Input: float(0)}, model.ModelInfoSourceManual, false, false, false, false},
		{"locked_price_not_applied", 2, &ModelsDevCost{Input: float(0)}, "", true, false, false, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			db := setupModelInfoTestDB(t, "pr380_presence_"+item.name)
			catalog := model.Model{ModelName: "presence-fixture", OfficialInputPrice: item.input / 1e6, OfficialOutputPrice: 8 / 1e6, OfficialCacheReadPerMtok: 0.5, OfficialCacheWritePerMtok: 1.25, InfoLocked: item.locked,
				InfoSources: model.ModelInfoSources{model.ModelInfoFieldOfficialPrice: item.source}}
			if err := db.Create(&catalog).Error; err != nil {
				t.Fatal(err)
			}
			index := NewModelsDevIndex(ModelsDevCatalog{"openai": {ID: "openai", Models: map[string]ModelsDevModel{catalog.ModelName: {ID: catalog.ModelName, Cost: item.cost}}}})
			if _, err := reconcileModelInfo(index, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := db.First(&catalog, catalog.Id).Error; err != nil {
				t.Fatal(err)
			}
			for _, proof := range []struct {
				column string
				want   bool
			}{{"official_input_price", item.wantInputProof}, {"official_output_price", item.wantOutputProof}, {"official_cache_read_per_mtok", item.wantCacheProof}, {"official_cache_write_per_mtok", false}} {
				got := catalog.InfoSources.Get(proof.column)
				if (got == model.ModelInfoSourceModelsDev || got == model.ModelInfoSourceModelsDevExplicitZero) != proof.want {
					t.Errorf("field %s upstream presence=%q want proven=%t (sources=%v)", proof.column, got, proof.want, catalog.InfoSources)
				}
			}
			if item.wantInputProof {
				want := model.ModelInfoSourceModelsDev
				if item.cost.Input != nil && *item.cost.Input == 0 {
					want = model.ModelInfoSourceModelsDevExplicitZero
				}
				if got := catalog.InfoSources.Get("official_input_price"); got != want {
					t.Errorf("input provenance=%q want %q; positive facts cannot prove a free zero", got, want)
				}
			}
			if (item.source == model.ModelInfoSourceManual || item.locked) && catalog.OfficialInputPrice != 2/1e6 {
				t.Fatalf("protected price was overwritten: %g", catalog.OfficialInputPrice)
			}
		})
	}
}

func TestPR380OfficialPriceSameExplicitZeroImmediatelyRepricesFollowingRow(t *testing.T) {
	db := setupModelInfoTestDB(t, "pr380_same_zero_follow")
	catalog := model.Model{ModelName: "gpt-same-zero-fixture", OfficialOutputPrice: 8 / 1e6,
		InfoSources: model.ModelInfoSources{model.ModelInfoFieldOfficialPrice: model.ModelInfoSourceModelsDev}}
	if err := db.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	previous := model.ModelSellPrice{ModelName: catalog.ModelName, ChannelGroup: model.ChannelLaneDirect,
		Active: true, FollowOfficial: true, PricingVersion: 47, InputPerMtok: float(2), OutputPerMtok: float(8)}
	if err := db.Create(&previous).Error; err != nil {
		t.Fatal(err)
	}
	index := NewModelsDevIndex(ModelsDevCatalog{"openai": {ID: "openai", Models: map[string]ModelsDevModel{
		catalog.ModelName: {ID: catalog.ModelName, Cost: &ModelsDevCost{Input: float(0), Output: float(8)}},
	}}})
	if _, err := reconcileModelInfo(index, time.Now()); err != nil {
		t.Fatal(err)
	}
	var active model.ModelSellPrice
	if err := db.Where("model_name = ? AND active = ?", catalog.ModelName, true).First(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active.InputPerMtok == nil || *active.InputPerMtok != 0 || active.PricingVersion == previous.PricingVersion {
		t.Fatalf("new explicit-zero evidence did not immediately materialize a free version: %+v", active)
	}
	if active.OutputPerMtok == nil || *active.OutputPerMtok != 8 {
		t.Fatalf("unchanged known output price changed: %+v", active)
	}
	var versions int64
	if err := db.Model(&model.ModelSellPrice{}).Where("model_name = ?", catalog.ModelName).Count(&versions).Error; err != nil || versions != 2 {
		t.Fatalf("expected preserved old version and new free version; count=%d err=%v", versions, err)
	}
}
