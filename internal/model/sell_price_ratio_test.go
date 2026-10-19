package model

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/setting"
)

func ratioFloat(v float64) *float64 { return &v }

func TestDeriveFollowingSellPriceScalesOfficialAndKeepsTheRest(t *testing.T) {
	official := ModelOfficialPrice{
		InputPerToken:     3 / 1e6, // per-token unit
		OutputPerToken:    15,      // per-million unit
		CacheReadPerMtok:  0.3,
		CacheWritePerMtok: 3.75,
		ContextTier:       &ContextTierPrice{Threshold: 200_000, InputPerMtok: ratioFloat(6), OutputPerMtok: ratioFloat(22.5)},
	}
	row := ModelSellPrice{
		ModelName: "claude-sonnet-5-5", ChannelGroup: "cheap", FollowOfficial: true, PriceRatio: ratioFloat(50),
		InputPerMtok: ratioFloat(9), OutputPerMtok: ratioFloat(9),
		CacheWrite1hPerMtok: ratioFloat(6), ImageOutputPerMtok: ratioFloat(40), RequestUSD: ratioFloat(0.04),
		PeakHoursUTC: "1-4", PeakMultiplier: ratioFloat(2),
	}
	got := DeriveFollowingSellPrice(row, official)
	expect := func(name string, value *float64, want float64) {
		t.Helper()
		if value == nil || *value != want {
			t.Fatalf("%s = %v, want %v", name, value, want)
		}
	}
	expect("input", got.InputPerMtok, 1.5)
	expect("output", got.OutputPerMtok, 7.5)
	expect("cache read", got.CacheReadPerMtok, 0.15)
	expect("cache write", got.CacheWrite5mPerMtok, 1.875)
	expect("tier input", got.ContextTierInputPerMtok, 3)
	expect("tier output", got.ContextTierOutputPerMtok, 11.25)
	if got.ContextTierThreshold != 200_000 {
		t.Fatalf("tier threshold = %d", got.ContextTierThreshold)
	}
	// What the official price does not carry is never touched: an image or
	// per-call price lost here bills $0. The row has no 5-minute write to
	// scale from, so the 1-hour write is carried as typed.
	expect("cache write 1h", got.CacheWrite1hPerMtok, 6)
	expect("image output", got.ImageOutputPerMtok, 40)
	expect("per call", got.RequestUSD, 0.04)
	if got.PeakHoursUTC != "1-4" || got.PeakMultiplier == nil || *got.PeakMultiplier != 2 {
		t.Fatalf("peak pricing lost: %+v", got)
	}
}

func TestDeriveFollowingSellPriceKeepsPinnedFieldsAndUsesTheLaneRatio(t *testing.T) {
	if err := setting.UpdateChannelLanesByJSONString(""); err != nil {
		t.Fatal(err)
	}
	official := ModelOfficialPrice{InputPerToken: 0.4, OutputPerToken: 3.2, CacheReadPerMtok: 0.04,
		ContextTier: &ContextTierPrice{Threshold: 272_000, InputPerMtok: ratioFloat(0.8)}}
	row := ModelSellPrice{
		ModelName: "gpt-6-luna", ChannelGroup: "cheap", FollowOfficial: true,
		PinnedFields: "output, context_tier, nonsense",
		InputPerMtok: ratioFloat(1), OutputPerMtok: ratioFloat(2),
		CacheWrite5mPerMtok: ratioFloat(0.5),
	}
	got := DeriveFollowingSellPrice(row, official)
	if got.InputPerMtok == nil || *got.InputPerMtok != 0.2 {
		t.Fatalf("input should follow the agent lane's 50%%: %v", got.InputPerMtok)
	}
	if got.OutputPerMtok == nil || *got.OutputPerMtok != 2 {
		t.Fatalf("pinned output moved: %v", got.OutputPerMtok)
	}
	if got.CacheWrite5mPerMtok == nil || *got.CacheWrite5mPerMtok != 0.5 {
		t.Fatalf("a field without an official price must keep its value: %v", got.CacheWrite5mPerMtok)
	}
	if got.ContextTierThreshold != 0 {
		t.Fatalf("pinned (absent) tier was derived: %+v", got)
	}
	if got.PinnedFields != "output,context_tier" {
		t.Fatalf("pinned fields = %q", got.PinnedFields)
	}

	fixed := row
	fixed.FollowOfficial = false
	if out := DeriveFollowingSellPrice(fixed, official); *out.InputPerMtok != 1 {
		t.Fatal("a fixed row must never be derived")
	}
}

func TestRecomputeFollowingSellPricesRepricesOnlyFollowingRows(t *testing.T) {
	db := withPricingTestDB(t, "sell_price_follow_recompute")
	if err := setting.UpdateChannelLanesByJSONString(""); err != nil {
		t.Fatal(err)
	}
	catalog := []Model{
		{ModelName: "follows", OfficialInputPrice: 2 / 1e6, OfficialOutputPrice: 8 / 1e6},
		{ModelName: "fixed", OfficialInputPrice: 2 / 1e6, OfficialOutputPrice: 8 / 1e6},
	}
	if err := db.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	rows := []ModelSellPrice{
		{ModelName: "follows", ChannelGroup: "direct", Active: true, PricingVersion: 1, FollowOfficial: true,
			InputPerMtok: ratioFloat(1), OutputPerMtok: ratioFloat(4), ImageOutputPerMtok: ratioFloat(30)},
		{ModelName: "fixed", ChannelGroup: "direct", Active: true, PricingVersion: 2,
			InputPerMtok: ratioFloat(1), OutputPerMtok: ratioFloat(4)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	changes, err := RecomputeFollowingSellPrices(nil, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].ModelName != "follows" {
		t.Fatalf("changes = %+v", changes)
	}
	var active []ModelSellPrice
	if err := db.Where("active = ?", true).Order("model_name").Find(&active).Error; err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Fatalf("active rows = %d", len(active))
	}
	if *active[0].InputPerMtok != 1 || active[0].ModelName != "fixed" {
		t.Fatalf("fixed row changed: %+v", active[0])
	}
	follow := active[1]
	if *follow.InputPerMtok != 2 || *follow.OutputPerMtok != 8 || follow.Source != SellPriceSourceOfficialFollow {
		t.Fatalf("following row = %+v", follow)
	}
	if follow.ImageOutputPerMtok == nil || *follow.ImageOutputPerMtok != 30 {
		t.Fatalf("image price lost on recompute: %+v", follow.ImageOutputPerMtok)
	}
	// Nothing moved: no new version.
	again, err := RecomputeFollowingSellPrices(nil, nil, "test")
	if err != nil || len(again) != 0 {
		t.Fatalf("second recompute = %+v, %v", again, err)
	}
}

func TestDeriveFollowingSellPriceKeepsTheOneHourWriteInStep(t *testing.T) {
	official := ModelOfficialPrice{InputPerToken: 5, OutputPerToken: 25, CacheWritePerMtok: 6.25}
	row := ModelSellPrice{
		ModelName: "claude-opus-5", ChannelGroup: "cheap", FollowOfficial: true, PriceRatio: ratioFloat(50),
		InputPerMtok: ratioFloat(5), OutputPerMtok: ratioFloat(25),
		CacheWrite5mPerMtok: ratioFloat(6.25), CacheWrite1hPerMtok: ratioFloat(10),
	}
	got := DeriveFollowingSellPrice(row, official)
	if got.CacheWrite5mPerMtok == nil || *got.CacheWrite5mPerMtok != 3.125 {
		t.Fatalf("5m write = %v", got.CacheWrite5mPerMtok)
	}
	if got.CacheWrite1hPerMtok == nil || *got.CacheWrite1hPerMtok != 5 {
		t.Fatalf("1h write should keep its 1.6x ratio to the 5m write: %v", got.CacheWrite1hPerMtok)
	}
}
