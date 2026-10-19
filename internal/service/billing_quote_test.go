package service

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestQuoteToQuotaUsesMicrousdDirectly(t *testing.T) {
	quota := QuoteToQuota(billing.QuoteResult{TotalMicrousd: 2_500})
	if quota != 2_500 {
		t.Fatalf("expected 2500 microusd to stay 2500 quota cache units, got %d", quota)
	}
}

func TestQuoteToQuotaClampsTinyBillableUsage(t *testing.T) {
	quota := QuoteToQuota(billing.QuoteResult{
		LineItems: []billing.LineItem{
			{
				Dimension:      "input",
				Quantity:       1,
				UnitMicrousd:   1,
				AmountMicrousd: 0,
			},
		},
		SubtotalMicrousd: 0,
		TotalMicrousd:    0,
	})
	if quota != 1 {
		t.Fatalf("expected tiny billable usage to clamp to 1 microusd, got %d", quota)
	}
}

func TestQuoteToQuotaKeepsFreeAndFullyDiscountedUsageFree(t *testing.T) {
	freeQuota := QuoteToQuota(billing.QuoteResult{
		LineItems: []billing.LineItem{
			{
				Dimension:      "input",
				Quantity:       1_000,
				UnitMicrousd:   0,
				AmountMicrousd: 0,
			},
		},
		SubtotalMicrousd: 0,
		TotalMicrousd:    0,
	})
	if freeQuota != 0 {
		t.Fatalf("expected free usage to stay free, got %d", freeQuota)
	}

	discountedQuota := QuoteToQuota(billing.QuoteResult{
		LineItems: []billing.LineItem{
			{
				Dimension:      "input",
				Quantity:       1_000,
				UnitMicrousd:   10_000,
				AmountMicrousd: 10,
			},
		},
		SubtotalMicrousd: 10,
		Discount: billing.Discount{
			Source:         "promo",
			Pct:            decimal.NewFromInt(100),
			AmountMicrousd: 10,
		},
		TotalMicrousd: 0,
	})
	if discountedQuota != 0 {
		t.Fatalf("expected fully discounted usage to stay free, got %d", discountedQuota)
	}
}

func TestQuotaDecimalToMicrousd(t *testing.T) {
	got := QuotaDecimalToMicrousd(decimal.NewFromInt(1_250))
	if got != 1_250 {
		t.Fatalf("expected 1250 cache units to equal 1250 microusd, got %d", got)
	}
}

func TestQuoteModelUsageUsesExplicitSellPrice(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:quote_explicit_sell_price?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	if err := db.AutoMigrate(&model.ModelSellPrice{}); err != nil {
		t.Fatalf("failed to migrate pricing tables: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	}()

	inputPrice := 2.5
	outputPrice := 10.0
	if err := db.Create(&model.ModelSellPrice{
		ModelName:      "gpt-4o",
		InputPerMtok:   &inputPrice,
		OutputPerMtok:  &outputPrice,
		Active:         true,
		PricingVersion: 7,
	}).Error; err != nil {
		t.Fatalf("failed to insert sell price: %v", err)
	}

	quote := QuoteModelUsage(
		&relaycommon.RelayInfo{
			UserId:          1,
			OriginModelName: "gpt-4o",
		},
		"gpt-4o",
		billing.Usage{
			InputTokens:  1_000,
			OutputTokens: 500,
		},
		nil,
	)

	if got := QuoteToQuota(quote); got != 7_500 {
		t.Fatalf("expected explicit sell price to charge 7500 microusd, got %d quote=%#v", got, quote)
	}
	if quote.PricingVersion != 7 {
		t.Fatalf("expected pricing version 7, got %d", quote.PricingVersion)
	}
}

func TestQuoteModelUsageChargesCacheReadTokens(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:quote_cache_read_sell_price?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	if err := db.AutoMigrate(&model.ModelSellPrice{}); err != nil {
		t.Fatalf("failed to migrate pricing tables: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	}()

	inputPrice := 3.0
	outputPrice := 15.0
	cacheReadPrice := 0.3
	if err := db.Create(&model.ModelSellPrice{
		ModelName:        "claude-sonnet-4-6",
		InputPerMtok:     &inputPrice,
		OutputPerMtok:    &outputPrice,
		CacheReadPerMtok: &cacheReadPrice,
		Active:           true,
		PricingVersion:   7,
	}).Error; err != nil {
		t.Fatalf("failed to insert sell price: %v", err)
	}

	quote := QuoteModelUsage(
		&relaycommon.RelayInfo{
			UserId:          1,
			OriginModelName: "claude-sonnet-4-6",
		},
		"claude-sonnet-4-6",
		billing.Usage{
			InputTokens:     100,
			CacheReadTokens: 1_000,
		},
		nil,
	)

	if quote.TotalMicrousd != 600 {
		t.Fatalf("expected input plus cache read total 600 microusd, got %d quote=%#v", quote.TotalMicrousd, quote)
	}
	if len(quote.LineItems) != 2 {
		t.Fatalf("expected input and cache read line items, got %#v", quote.LineItems)
	}
	if quote.LineItems[1].Dimension != "cache_read" || quote.LineItems[1].AmountMicrousd != 300 {
		t.Fatalf("expected cache_read line item for 300 microusd, got %#v", quote.LineItems[1])
	}
}

func TestQuoteModelUsageReturnsZeroWhenSellPriceUnavailable(t *testing.T) {
	previousDB := model.CoreDB()
	model.SetCoreDB(nil)
	defer func() {
		model.SetCoreDB(previousDB)
	}()

	quote := QuoteModelUsage(
		&relaycommon.RelayInfo{
			UserId:     1,
			UsingGroup: "vip",
		},
		"unpriced-model",
		billing.Usage{InputTokens: 1_000_000},
		nil,
	)

	if quote.SubtotalMicrousd != 0 {
		t.Fatalf("expected missing sell price subtotal 0, got %d", quote.SubtotalMicrousd)
	}
	if quote.TotalMicrousd != 0 {
		t.Fatalf("expected missing sell price total 0, got %d", quote.TotalMicrousd)
	}
}

// A price plan (专属折扣) takes its percentage off the vendor's official list
// price, never off the lane's already-discounted price, and never lifts a line
// above the lane price: each line costs min(lane price, official × (1 − pct)).
// Only users on the plan get it; everyone else pays the lane price.
func TestQuoteModelUsageAppliesPricePlanOnlyToPlanMembers(t *testing.T) {
	db := setupModelInfoTestDB(t, "quote_price_plan_members")
	if err := db.AutoMigrate(&model.User{}, &model.PricePlan{}, &model.PricePlanRule{}); err != nil {
		t.Fatalf("migrate price plans: %v", err)
	}
	model.InvalidatePricePlanCache()
	t.Cleanup(model.InvalidatePricePlanCache)

	rows := []interface{}{
		// Official $5 in / $20 out; the direct lane already sells at 4 / 16.
		&model.Model{ModelName: "plan-model", Status: 1, OfficialInputPrice: 5 / 1e6, OfficialOutputPrice: 20 / 1e6},
		&model.ModelSellPrice{ModelName: "plan-model", ChannelGroup: model.ChannelLaneDirect, Active: true, PricingVersion: 1,
			InputPerMtok: float(4), OutputPerMtok: float(16)},
		&model.Model{ModelName: "plan-shallow-model", Status: 1, OfficialInputPrice: 5 / 1e6, OfficialOutputPrice: 20 / 1e6},
		&model.ModelSellPrice{ModelName: "plan-shallow-model", ChannelGroup: model.ChannelLaneDirect, Active: true, PricingVersion: 1,
			InputPerMtok: float(4), OutputPerMtok: float(16)},
	}
	rows = append(rows, servedModel("plan-model", 9201)...)
	rows = append(rows, servedModel("plan-shallow-model", 9202)...)
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	model.RefreshPricing()

	defaultPct := 40.0
	plan, err := model.SavePricePlan(model.PricePlan{Name: "OpenAI 6折", DefaultPct: &defaultPct, Lanes: model.PricePlanLanesDirect, Active: true},
		[]model.PricePlanRule{{Kind: model.PricePlanRuleModel, ModelName: "plan-shallow-model", Pct: 10}})
	if err != nil {
		t.Fatalf("save price plan: %v", err)
	}
	usage := billing.Usage{InputTokens: 1_000_000, OutputTokens: 100_000}

	// No plan: the lane price, 4 + 1.6.
	standard := QuoteModelUsage(&relaycommon.RelayInfo{UserId: 1, UsingGroup: model.ChannelLaneDirect}, "plan-model", usage, nil)
	if standard.TotalMicrousd != 5_600_000 || standard.Discount.Plan != nil {
		t.Fatalf("user without a plan should pay the lane price 5600000, got total=%d discount=%+v", standard.TotalMicrousd, standard.Discount)
	}

	// On the plan: 60% of official (3 + 1.2), below the lane's 4 + 1.6.
	member := &relaycommon.RelayInfo{UserId: 2, UsingGroup: model.ChannelLaneDirect, PricePlanID: plan.ID}
	discounted := QuoteModelUsage(member, "plan-model", usage, nil)
	if discounted.SubtotalMicrousd != 5_600_000 {
		t.Fatalf("plan must not change the lane subtotal, got %d", discounted.SubtotalMicrousd)
	}
	if discounted.TotalMicrousd != 4_200_000 {
		t.Fatalf("expected official x 0.6 = 4200000, got %d quote=%+v", discounted.TotalMicrousd, discounted)
	}
	if discounted.Discount.Source != "price_plan" || discounted.Discount.Plan == nil || discounted.Discount.Plan.ID != plan.ID ||
		discounted.Discount.Plan.Rule != model.PricePlanRuleSourceDefault {
		t.Fatalf("expected the plan default on the receipt, got %+v", discounted.Discount)
	}

	// A 10% model exception gives official x 0.9 = 4.5 / 18, above the lane's
	// own price, so the member pays the lane price and nothing more.
	shallow := QuoteModelUsage(member, "plan-shallow-model", usage, nil)
	if shallow.TotalMicrousd != 5_600_000 || shallow.Discount.AmountMicrousd != 0 {
		t.Fatalf("a plan must never charge above the lane price, got total=%d discount=%+v", shallow.TotalMicrousd, shallow.Discount)
	}

	// The plan covers the direct lane only.
	if err := db.Create(&model.ModelSellPrice{ModelName: "plan-model", ChannelGroup: model.ChannelLaneAgent, Active: true, PricingVersion: 1,
		InputPerMtok: float(2), OutputPerMtok: float(8)}).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	cheap := QuoteModelUsage(&relaycommon.RelayInfo{UserId: 2, UsingGroup: model.ChannelLaneAgent, PricePlanID: plan.ID}, "plan-model", usage, nil)
	if cheap.TotalMicrousd != 2_800_000 || cheap.Discount.Plan != nil {
		t.Fatalf("a direct-only plan must not touch agent-lane requests, got total=%d discount=%+v", cheap.TotalMicrousd, cheap.Discount)
	}
}

func TestRelayQuotaLedgerEventsCarryPricingVersion(t *testing.T) {
	previousDB := model.CoreDB()
	previousRedisEnabled := common.RedisEnabled
	db, err := gorm.Open(gormsqlite.Open("file:relay_quota_pricing_version?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	common.RedisEnabled = false
	model.EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.WalletLedger{}); err != nil {
		t.Fatalf("failed to migrate ledger tables: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		common.RedisEnabled = previousRedisEnabled
	}()

	if err := db.Create(&model.User{Id: 1, Username: "quota-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	relayInfo := &relaycommon.RelayInfo{
		UserId:          1,
		TokenId:         2,
		TokenKey:        "token-key",
		OriginModelName: "gpt-4o",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 3},
		PriceData: types.PriceData{
			PricingVersion: 42,
		},
	}
	if lazuErr := PreConsumeQuota(c, 100, relayInfo); lazuErr != nil {
		t.Fatalf("failed to preconsume quota: %v", lazuErr)
	}
	if err := PostConsumeQuota(relayInfo, 25, relayInfo.FinalPreConsumedQuota, false); err != nil {
		t.Fatalf("failed to settle quota: %v", err)
	}

	var rows []model.WalletLedger
	if err := db.Where("user_id = ?", 1).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("failed to load ledger rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two ledger rows, got %d", len(rows))
	}
	for _, row := range rows {
		if row.PricingVersion != 42 {
			t.Fatalf("expected pricing version 42 on ledger row, got %#v", row)
		}
		if !strings.Contains(row.Metadata, `"pricing_version":42`) {
			t.Fatalf("expected pricing_version metadata on ledger row, got %q", row.Metadata)
		}
	}
}

func TestRelayQuotaLedgerEventDataHandlesMissingChannelMeta(t *testing.T) {
	relayInfo := &relaycommon.RelayInfo{
		UserId:          1,
		TokenId:         2,
		OriginModelName: "gpt-4o",
		UsingGroup:      "default",
		PriceData: types.PriceData{
			PricingVersion: 42,
		},
	}

	refID, metadata := relayQuotaLedgerEventData(relayInfo, "relay_preconsume", 7)
	if !strings.HasPrefix(refID, "relay_preconsume:1:2:") {
		t.Fatalf("expected relay preconsume ref id, got %q", refID)
	}
	if metadata["channel_id"] != 0 {
		t.Fatalf("expected missing channel meta to use channel id 0, got %#v", metadata["channel_id"])
	}
	if metadata["pricing_version"] != int64(42) {
		t.Fatalf("expected pricing version 42, got %#v", metadata["pricing_version"])
	}
	if metadata["pre_consumed_quota"] != int64(7) {
		t.Fatalf("expected pre consumed quota 7, got %#v", metadata["pre_consumed_quota"])
	}
}

func TestOfficialPriceReadsBothCatalogueUnits(t *testing.T) {
	// $4 per million, stored per token (LiteLLM) and typed per million.
	for _, price := range []float64{0.000004, 4} {
		got := perTokenUSDToPerMtokMicrousd(price)
		if got == nil || *got != 4_000_000 {
			t.Fatalf("price %v -> %v, want 4,000,000 microUSD per Mtok", price, got)
		}
	}
	if perTokenUSDToPerMtokMicrousd(0) != nil {
		t.Fatal("no list price must stay nil")
	}
}

func TestQuoteModelUsageRecordsPeakMultiplier(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:quote_peak_sell_price?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	if err := db.AutoMigrate(&model.ModelSellPrice{}); err != nil {
		t.Fatalf("failed to migrate pricing tables: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	}()

	inputPrice := 1.0
	multiplier := 2.0
	if err := db.Create(&model.ModelSellPrice{
		ModelName:      "peak-model",
		InputPerMtok:   &inputPrice,
		PeakHoursUTC:   "0-24", // always peak, so the test does not depend on the clock
		PeakMultiplier: &multiplier,
		Active:         true,
		PricingVersion: 3,
	}).Error; err != nil {
		t.Fatalf("failed to insert sell price: %v", err)
	}

	quote := QuoteModelUsage(&relaycommon.RelayInfo{UserId: 1, OriginModelName: "peak-model"}, "peak-model",
		billing.Usage{InputTokens: 1_000}, nil)

	if quote.Peak == nil || quote.Peak.Multiplier != 2 {
		t.Fatalf("expected the ×2 peak to be recorded, got %#v", quote.Peak)
	}
	if quote.TotalMicrousd != 2_000 {
		t.Fatalf("expected 1,000 tokens at the doubled $2/M = 2,000 microusd, got %d", quote.TotalMicrousd)
	}
}
