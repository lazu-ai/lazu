package service

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// servedModel lists the model on a live direct source so the price cache
// (built from enabled abilities) carries it.
func servedModel(name string, channelID int) []interface{} {
	return []interface{}{
		&model.Channel{Id: channelID, Type: constant.ChannelTypeOpenAI, Key: "test", Status: common.ChannelStatusEnabled,
			Models: name, Group: model.ChannelLaneDirect},
		&model.Ability{Group: model.ChannelLaneDirect, Model: name, ChannelId: channelID, Enabled: true},
	}
}

// A long request is compared against the lab's own long-context list price
// (judged on the lab's threshold), instead of dropping the comparison.
func TestLongRequestIsComparedAgainstTheOfficialLongContextPrice(t *testing.T) {
	db := setupModelInfoTestDB(t, "official_tier_compare")
	rows := []interface{}{
		&model.Model{ModelName: "gpt-5.5", Status: 1, OfficialInputPrice: 5 / 1e6, OfficialOutputPrice: 30 / 1e6,
			OfficialCacheReadPerMtok: 0.5,
			OfficialContextTier: model.ModelContextTier{Threshold: 272_000, InputPerMtok: float(10), OutputPerMtok: float(45),
				CacheReadPerMtok: float(1)}},
		&model.ModelSellPrice{ModelName: "gpt-5.5", ChannelGroup: model.ChannelLaneDirect, Active: true, PricingVersion: 1,
			InputPerMtok: float(4), OutputPerMtok: float(24), CacheReadPerMtok: float(0.4),
			ContextTierThreshold: 272_000, ContextTierInputPerMtok: float(8), ContextTierOutputPerMtok: float(36),
			ContextTierCacheReadPerMtok: float(0.8)},
	}
	rows = append(rows, servedModel("gpt-5.5", 9101)...)
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	model.RefreshPricing()
	info := &relaycommon.RelayInfo{UsingGroup: model.ChannelLaneDirect}

	short := QuoteModelUsage(info, "gpt-5.5", billing.Usage{InputTokens: 100_000, OutputTokens: 1_000}, nil)
	// 100000*5 + 1000*30 at list price.
	if short.Official == nil || short.Official.TotalMicrousd != 530_000 {
		t.Fatalf("short request official = %+v", short.Official)
	}

	long := QuoteModelUsage(info, "gpt-5.5", billing.Usage{InputTokens: 280_000, CacheReadTokens: 20_000, OutputTokens: 2_000}, nil)
	if long.ContextTier == nil {
		t.Fatal("expected the long-context tier")
	}
	// 280000*10 + 20000*1 + 2000*45 at the long-context list price.
	if long.Official == nil || long.Official.TotalMicrousd != 2_910_000 {
		t.Fatalf("long request official = %+v", long.Official)
	}
	// 280000*8 + 20000*0.8 + 2000*36
	if got := QuoteToQuota(long); got != 2_328_000 {
		t.Fatalf("long request charge = %d", got)
	}
}

// Without a known official tier, a request billed at our long-context tier
// still has no honest list-price comparison.
func TestLongRequestWithoutOfficialTierHasNoComparison(t *testing.T) {
	db := setupModelInfoTestDB(t, "official_tier_unknown")
	rows := []interface{}{
		&model.Model{ModelName: "gpt-5.4", Status: 1, OfficialInputPrice: 2.5 / 1e6, OfficialOutputPrice: 15 / 1e6},
		&model.ModelSellPrice{ModelName: "gpt-5.4", ChannelGroup: model.ChannelLaneDirect, Active: true, PricingVersion: 1,
			InputPerMtok: float(2.5), OutputPerMtok: float(15),
			ContextTierThreshold: 272_000, ContextTierInputPerMtok: float(5), ContextTierOutputPerMtok: float(22.5)},
	}
	rows = append(rows, servedModel("gpt-5.4", 9102)...)
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	model.RefreshPricing()
	long := QuoteModelUsage(&relaycommon.RelayInfo{UsingGroup: model.ChannelLaneDirect}, "gpt-5.4",
		billing.Usage{InputTokens: 300_000, OutputTokens: 1_000}, nil)
	if long.ContextTier == nil || long.Official != nil {
		t.Fatalf("tier=%+v official=%+v", long.ContextTier, long.Official)
	}
}

// The lab's threshold decides the official price even where ours differs:
// a request past ours but under the lab's compares against the base list.
func TestOfficialPricesFollowTheLabsThreshold(t *testing.T) {
	official := model.ModelOfficialPrice{InputPerToken: 2 / 1e6, OutputPerToken: 12 / 1e6,
		ContextTier: &model.ContextTierPrice{Threshold: 200_000, InputPerMtok: float(4), OutputPerMtok: float(18)}}
	under := officialPricesForInput(official, 200_000)
	if !under.LengthAware || *under.Input != 2_000_000 || *under.Output != 12_000_000 {
		t.Fatalf("at the threshold the base list price applies: %+v", under)
	}
	over := officialPricesForInput(official, 200_001)
	if *over.Input != 4_000_000 || *over.Output != 18_000_000 {
		t.Fatalf("past the threshold the long list price applies: in=%d out=%d", *over.Input, *over.Output)
	}
	if over.CacheRead != nil {
		t.Fatal("an unpublished cache price must stay unknown")
	}
}

func TestSyncFillsAndProposesTheOfficialContextTier(t *testing.T) {
	db := setupModelInfoTestDB(t, "official_tier_sync")
	m := model.Model{ModelName: "gemini-2.5-pro", OfficialInputPrice: 1.25 / 1e6, OfficialOutputPrice: 10 / 1e6}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	upstream := func(longInput float64) *ModelsDevIndex {
		entry := ModelsDevModel{ID: m.ModelName, Cost: &ModelsDevCost{Input: float(1.25), Output: float(10),
			Tiers: []ModelsDevCostTier{{Input: float(longInput), Output: float(15), CacheRead: float(0.25)}}}}
		entry.Cost.Tiers[0].Tier.Type, entry.Cost.Tiers[0].Tier.Size = "context", 200_000
		return NewModelsDevIndex(ModelsDevCatalog{"google": {ID: "google", Models: map[string]ModelsDevModel{m.ModelName: entry}}})
	}
	if _, err := reconcileModelInfo(upstream(2.5), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, row := range reviewsOf(t, db, model.ModelInfoReviewDiff) {
		if row.Field == model.ModelInfoFieldOfficialTier {
			t.Fatalf("an unset official tier must be filled, not proposed: %+v", row)
		}
	}
	var got model.Model
	if err := db.First(&got, m.Id).Error; err != nil {
		t.Fatal(err)
	}
	tier := got.OfficialContextTier
	if tier.Threshold != 200_000 || tier.InputPerMtok == nil || *tier.InputPerMtok != 2.5 || *tier.OutputPerMtok != 15 {
		t.Fatalf("official tier not filled: %+v", tier)
	}
	if got.InfoSources.Get(model.ModelInfoFieldOfficialTier) != model.ModelInfoSourceModelsDev {
		t.Fatalf("official tier source = %q", got.InfoSources.Get(model.ModelInfoFieldOfficialTier))
	}

	// A later change is taken and filed as a notice.
	if _, err := reconcileModelInfo(upstream(3), time.Now()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range reviewsOf(t, db, model.ModelInfoReviewPriceFollowed) {
		if row.Field == model.ModelInfoFieldOfficialTier {
			found = true
		}
	}
	if !found {
		t.Fatal("missing followed official tier notice")
	}
	if err := db.First(&got, m.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.OfficialContextTier.InputPerMtok == nil || *got.OfficialContextTier.InputPerMtok != 3 {
		t.Fatalf("followed tier wrote %+v", got.OfficialContextTier)
	}
}

// The list-price cost row takes the lab's tier, scaled to what the source
// pays; a source's own cost row is left as the operator typed it.
func TestListPriceCostRowTakesTheOfficialTier(t *testing.T) {
	db := setupModelInfoTestDB(t, "cost_official_tier")
	if err := db.AutoMigrate(&model.ProviderCostPrice{}); err != nil {
		t.Fatal(err)
	}
	rows := []interface{}{
		&model.Model{ModelName: "gpt-6-luna", Status: 1, OfficialInputPrice: 0.1 / 1e6, OfficialOutputPrice: 0.5 / 1e6,
			OfficialContextTier: model.ModelContextTier{Threshold: 272_000, InputPerMtok: float(0.2), OutputPerMtok: float(0.75)}},
		&model.ProviderCostPrice{ModelName: "gpt-6-luna", Provider: "seed", ChannelID: 0, PricingVersion: 1,
			InputPerMtok: float(0.05), OutputPerMtok: float(0.25)},
		&model.ProviderCostPrice{ModelName: "gpt-6-luna", Provider: "manual", ChannelID: 7, PricingVersion: 2,
			InputPerMtok: float(0.03), OutputPerMtok: float(0.15)},
	}
	rows = append(rows, servedModel("gpt-6-luna", 7)...)
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	model.RefreshPricing()

	list, ok := model.GetActiveProviderCostPrice("gpt-6-luna", 0)
	if !ok || list.ContextTierThreshold != 272_000 {
		t.Fatalf("list-price row lacks the official tier: %+v", list)
	}
	long, tiered := list.ForInputTokens(300_000)
	if !tiered || !floatsClose(*long.InputPerMtok, 0.1) || !floatsClose(*long.OutputPerMtok, 0.375) {
		t.Fatalf("list-price long cost in=%v out=%v", *long.InputPerMtok, *long.OutputPerMtok)
	}

	own, ok := model.GetActiveProviderCostPrice("gpt-6-luna", 7)
	if !ok || own.HasContextTier() {
		t.Fatalf("a source's own cost row must not inherit the lab tier: %+v", own)
	}
}
