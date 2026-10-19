package model

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/types"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func withPricingTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()

	previousDB := coreDB
	previousRedisEnabled := common.RedisEnabled
	previousPricingSnapshotEnabled := common.PricingSnapshotEnabled
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	common.RedisEnabled = false
	common.PricingSnapshotEnabled = false
	EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(
		&User{},
		&Token{},
		&ModelSellPrice{},
		&ProviderCostPrice{},
		&PricePlan{},
		&PricePlanRule{},
		&WalletLedger{},
		// The seed cleanup decides what to keep by asking the catalog and the
		// enabled abilities, so both have to exist here.
		&Model{},
		&ModelMetadata{},
		&Ability{},
		// The cleanup records that it has run, so it needs somewhere to record it.
		&SchemaVersion{},
		&SettlementFailure{},
		&LedgerLegacySettlementWaiver{},
		&Log{},
		&TopUp{},
	); err != nil {
		t.Fatalf("failed to migrate pricing tables: %v", err)
	}
	if err := db.AutoMigrate(telemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = previousDB
		common.RedisEnabled = previousRedisEnabled
		common.PricingSnapshotEnabled = previousPricingSnapshotEnabled
	})
	return db
}

func TestProviderCostPriceUniqueKeyIncludesChannelID(t *testing.T) {
	db := withPricingTestDB(t, "provider_cost_channel_key")
	input := 1.25
	rows := []ProviderCostPrice{
		{
			ModelName:      "gpt-4o-mini",
			Provider:       "openai",
			ChannelID:      1,
			InputPerMtok:   &input,
			PricingVersion: 99,
		},
		{
			ModelName:      "gpt-4o-mini",
			Provider:       "openai",
			ChannelID:      2,
			InputPerMtok:   &input,
			PricingVersion: 99,
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("expected same model/provider/version across channels to insert: %v", err)
	}

	channelOneRows, err := ListProviderCostPricesForChannel(1)
	if err != nil {
		t.Fatalf("failed to list channel costs: %v", err)
	}
	if len(channelOneRows) != 1 || channelOneRows[0].ChannelID != 1 {
		t.Fatalf("expected exactly one channel-1 cost row, got %#v", channelOneRows)
	}
}

func TestEnrichRequestUsageFactAddsRequestMetadataAndCosts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "req_fact_123")
	common.SetContextKey(c, constant.ContextKeySessionID, "session-123")
	common.SetContextKey(c, constant.ContextKeyClientFramework, "codex")

	fact := enrichRequestUsageFact(c, 77, RecordConsumeLogParams{
		ChannelId:        12,
		TokenId:          34,
		ModelName:        "gpt-test",
		AmountMicrousd:   billing.MicroUSD(2500),
		PromptTokens:     100,
		CompletionTokens: 50,
		Other: map[string]interface{}{
			"provider_cost_usd": 0.001,
		},
	}, usagepkg.Fact{
		Version:          usagepkg.FactVersion,
		ProviderFamily:   "openai",
		PromptTokens:     100,
		CompletionTokens: 50,
		Dimensions: []usagepkg.DimensionUsage{
			{Name: usagepkg.DimensionImageGeneration, Quantity: 1, Unit: usagepkg.UnitRequest},
		},
	})

	if fact.RequestID != "req_fact_123" {
		t.Fatalf("request id: got %q", fact.RequestID)
	}
	if fact.SessionID != "session-123" {
		t.Fatalf("session id: got %q", fact.SessionID)
	}
	if fact.Source != "codex" {
		t.Fatalf("source: got %q", fact.Source)
	}
	if fact.UserID != 77 || fact.TokenID != 34 || fact.ChannelID != 12 || fact.ModelName != "gpt-test" {
		t.Fatalf("request identity not enriched: %+v", fact)
	}
	if fact.CustomerCostUSD == nil || math.Abs(*fact.CustomerCostUSD-0.0025) > 0.0000001 {
		t.Fatalf("customer cost: got %v", fact.CustomerCostUSD)
	}
	if fact.ProviderCostUSD == nil || math.Abs(*fact.ProviderCostUSD-0.001) > 0.0000001 {
		t.Fatalf("provider cost: got %v", fact.ProviderCostUSD)
	}
	if fact.GrossMarginUSD == nil || math.Abs(*fact.GrossMarginUSD-0.0015) > 0.0000001 {
		t.Fatalf("gross margin: got %v", fact.GrossMarginUSD)
	}

	parsed, ok := usagepkg.ParseFact(fact.JSONString())
	if !ok {
		t.Fatalf("parse enriched fact")
	}
	if parsed.SessionID != "session-123" || parsed.Source != "codex" {
		t.Fatalf("enriched fact did not survive JSON round trip: %+v", parsed)
	}
}

func TestAttachRoutingOutcomeUsageSnapshotReusesUsageFactCosts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	providerCost := 0.0012
	grossMargin := 0.0038
	attachRoutingOutcomeUsageSnapshot(c, RecordConsumeLogParams{
		AmountMicrousd: billing.MicroUSD(5000),
		UsageFact: usagepkg.Fact{
			Version:          usagepkg.FactVersion,
			PromptTokens:     10,
			CompletionTokens: 20,
			ProviderCostUSD:  &providerCost,
			GrossMarginUSD:   &grossMargin,
		},
	})

	snapshot, ok := common.GetContextKeyType[RoutingOutcomeUsageSnapshot](c, constant.ContextKeyRoutingOutcomeUsage)
	if !ok {
		t.Fatal("expected routing outcome usage snapshot")
	}
	if !snapshot.HasUpstreamCost || math.Abs(snapshot.UpstreamCostUSD-providerCost) > 0.0000001 {
		t.Fatalf("expected provider cost from usage fact, got %+v", snapshot)
	}
	if !snapshot.HasGrossMargin || math.Abs(snapshot.GrossMarginUSD-grossMargin) > 0.0000001 {
		t.Fatalf("expected gross margin from usage fact, got %+v", snapshot)
	}
}

func TestResolveUpstreamCostUsesChannelProviderCostPrice(t *testing.T) {
	db := withPricingTestDB(t, "provider_cost_quote_for_outcome")
	if err := db.AutoMigrate(&Channel{}); err != nil {
		t.Fatalf("failed to migrate channel table: %v", err)
	}
	input := 1.0
	output := 2.0
	if err := db.Create(&ProviderCostPrice{
		ModelName:      "gpt-test",
		Provider:       "manual",
		ChannelID:      42,
		InputPerMtok:   &input,
		OutputPerMtok:  &output,
		PricingVersion: 7,
	}).Error; err != nil {
		t.Fatalf("failed to create provider cost price: %v", err)
	}

	costUSD, ok := resolveUpstreamCostUSD(RecordConsumeLogParams{
		ChannelId: 42,
		ModelName: "gpt-test",
		Other:     map[string]interface{}{},
	}, usagepkg.Fact{
		Version:          usagepkg.FactVersion,
		PromptTokens:     1000,
		CompletionTokens: 2000,
		InputTokens:      1000,
		OutputTokens:     2000,
	})
	if !ok {
		t.Fatal("expected provider cost to be available")
	}
	if math.Abs(costUSD-0.005) > 0.0000001 {
		t.Fatalf("expected provider cost 0.005 USD, got %.8f", costUSD)
	}
}

func TestResolveUpstreamCostUsesTheSourcesLongContextTier(t *testing.T) {
	db := withPricingTestDB(t, "provider_cost_context_tier")
	if err := db.AutoMigrate(&Channel{}); err != nil {
		t.Fatalf("failed to migrate channel table: %v", err)
	}
	input, output, longInput, longOutput := 1.0, 2.0, 2.0, 3.0
	if err := db.Create(&ProviderCostPrice{
		ModelName: "gpt-test", Provider: "manual", ChannelID: 42, PricingVersion: 7,
		InputPerMtok: &input, OutputPerMtok: &output,
		ContextTierThreshold: 1000, ContextTierInputPerMtok: &longInput, ContextTierOutputPerMtok: &longOutput,
	}).Error; err != nil {
		t.Fatalf("failed to create provider cost price: %v", err)
	}
	cost := func(fresh, cached int) float64 {
		t.Helper()
		usd, ok := resolveUpstreamCostUSD(RecordConsumeLogParams{ChannelId: 42, ModelName: "gpt-test", Other: map[string]interface{}{}},
			usagepkg.Fact{Version: usagepkg.FactVersion, PromptTokens: fresh + cached, CompletionTokens: 1000,
				Dimensions: []usagepkg.DimensionUsage{
					{Name: usagepkg.DimensionInput, Quantity: int64(fresh), Unit: usagepkg.UnitToken},
					{Name: usagepkg.DimensionCacheRead, Quantity: int64(cached), Unit: usagepkg.UnitToken},
					{Name: usagepkg.DimensionOutput, Quantity: 1000, Unit: usagepkg.UnitToken},
				}})
		if !ok {
			t.Fatal("expected provider cost to be available")
		}
		return usd
	}
	// At the threshold: base cost, 1000*1 + 1000*2.
	if got := cost(1000, 0); math.Abs(got-0.003) > 0.0000001 {
		t.Fatalf("at threshold cost %.8f", got)
	}
	// Cache reads count toward the threshold: 900 fresh + 200 cached is long.
	// No cache price is set, so only 900*2 + 1000*3 is billable.
	if got := cost(900, 200); math.Abs(got-0.0048) > 0.0000001 {
		t.Fatalf("long cost %.8f", got)
	}
}

func TestResolveUpstreamCostUsesExplicitProviderCost(t *testing.T) {
	_ = withPricingTestDB(t, "provider_cost_explicit_for_outcome")
	costUSD, ok := resolveUpstreamCostUSD(RecordConsumeLogParams{
		ChannelId: 99,
		ModelName: "search-model",
		Other: map[string]interface{}{
			"provider_cost_usd": "0.0125",
		},
	}, usagepkg.Fact{
		Version: usagepkg.FactVersion,
	})
	if !ok {
		t.Fatal("expected explicit provider cost to be available")
	}
	if math.Abs(costUSD-0.0125) > 0.0000001 {
		t.Fatalf("expected provider cost 0.0125 USD, got %.8f", costUSD)
	}
}

func TestMigrateTokenGroupDefaultsResetsLegacyGroups(t *testing.T) {
	db := withPricingTestDB(t, "token_group_defaults")
	if err := db.Create(&User{Id: 1, Username: "token-user"}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create([]Token{
		{Id: 1, UserId: 1, Key: "token-vip", Group: "vip"},
		{Id: 2, UserId: 1, Key: "token-auto", Group: "auto"},
		{Id: 3, UserId: 1, Key: "token-default", Group: "default"},
	}).Error; err != nil {
		t.Fatalf("failed to create tokens: %v", err)
	}

	if err := migrateTokenGroupDefaults(db); err != nil {
		t.Fatalf("failed to migrate token groups: %v", err)
	}

	var groups []string
	if err := db.Model(&Token{}).Order("id ASC").Pluck("group", &groups).Error; err != nil {
		t.Fatalf("failed to load token groups: %v", err)
	}
	for i, group := range groups {
		if group != ChannelLaneDirect {
			t.Fatalf("expected token %d group to reset to direct, got %q", i+1, group)
		}
	}
}

func TestGetActiveModelSellPriceNormalizesVariantNames(t *testing.T) {
	db := withPricingTestDB(t, "pricing_normalized_name")
	input := 5.0
	if err := db.Create(&ModelSellPrice{
		ModelName:      "gpt-4o-gizmo-*",
		InputPerMtok:   &input,
		Active:         true,
		PricingVersion: 11,
	}).Error; err != nil {
		t.Fatalf("failed to insert sell price: %v", err)
	}

	price, ok := GetActiveModelSellPrice("gpt-4o-gizmo-custom")
	if !ok {
		t.Fatalf("expected normalized sell price")
	}
	if price.ModelName != "gpt-4o-gizmo-*" {
		t.Fatalf("expected wildcard price row, got %q", price.ModelName)
	}
}

func TestEnrichModelSellPricesCopiesActiveSellPriceFields(t *testing.T) {
	db := withPricingTestDB(t, "model_meta_sell_price_enrichment")
	input := 0.16
	output := 0.32
	cacheRead := 0.032
	if err := db.Create(&ModelSellPrice{
		ModelName:        "deepseek-v4-flash",
		ChannelGroup:     ChannelLaneDirect,
		InputPerMtok:     &input,
		OutputPerMtok:    &output,
		CacheReadPerMtok: &cacheRead,
		Active:           true,
		PricingVersion:   42,
	}).Error; err != nil {
		t.Fatalf("failed to insert sell price: %v", err)
	}

	rows := []*Model{{ModelName: "deepseek-v4-flash"}}
	EnrichModelSellPrices(rows)

	if rows[0].QuotaType == nil || *rows[0].QuotaType != 0 {
		t.Fatalf("expected per-token quota type, got %#v", rows[0].QuotaType)
	}
	if rows[0].InputPerMtok == nil || *rows[0].InputPerMtok != input {
		t.Fatalf("expected input price %v, got %#v", input, rows[0].InputPerMtok)
	}
	if rows[0].OutputPerMtok == nil || *rows[0].OutputPerMtok != output {
		t.Fatalf("expected output price %v, got %#v", output, rows[0].OutputPerMtok)
	}
	if rows[0].CacheReadPerMtok == nil || *rows[0].CacheReadPerMtok != cacheRead {
		t.Fatalf("expected cache read price %v, got %#v", cacheRead, rows[0].CacheReadPerMtok)
	}
	if rows[0].PricingVersion != 42 {
		t.Fatalf("expected pricing version 42, got %d", rows[0].PricingVersion)
	}
	if len(rows[0].Lanes) != 1 || rows[0].Lanes[0].ChannelGroup != ChannelLaneDirect {
		t.Fatalf("expected direct lane price, got %#v", rows[0].Lanes)
	}
	if rows[0].Lanes[0].ModelName != "deepseek-v4-flash" {
		t.Fatalf("expected lane price owner, got %#v", rows[0].Lanes[0])
	}
}

func TestEnrichModelSellPricesKeepsNormalizedLaneOwner(t *testing.T) {
	db := withPricingTestDB(t, "model_meta_normalized_lane_owner")
	input := 2.5
	if err := db.Create(&ModelSellPrice{
		ModelName:      "gpt-4o-gizmo-*",
		ChannelGroup:   ChannelLaneDirect,
		InputPerMtok:   &input,
		Active:         true,
		PricingVersion: 7,
	}).Error; err != nil {
		t.Fatalf("failed to insert wildcard sell price: %v", err)
	}

	rows := []*Model{{ModelName: "gpt-4o-gizmo-custom"}}
	EnrichModelSellPrices(rows)

	if len(rows[0].Lanes) != 1 || rows[0].Lanes[0].ModelName != "gpt-4o-gizmo-*" {
		t.Fatalf("expected wildcard lane owner, got %#v", rows[0].Lanes)
	}
}

func TestPricingSnapshotServesPricesUntilRefresh(t *testing.T) {
	db := withPricingTestDB(t, "pricing_snapshot_serves_cached_rows")
	common.PricingSnapshotEnabled = true
	input := 1.23
	if err := db.Create(&ModelSellPrice{
		ModelName:      "snapshot-model",
		ChannelGroup:   ChannelLaneDirect,
		InputPerMtok:   &input,
		Active:         true,
		PricingVersion: 1,
	}).Error; err != nil {
		t.Fatalf("failed to create sell price: %v", err)
	}
	if err := RefreshPricingSnapshot(); err != nil {
		t.Fatalf("failed to refresh pricing snapshot: %v", err)
	}

	if err := db.Model(&ModelSellPrice{}).Where("model_name = ?", "snapshot-model").Update("active", false).Error; err != nil {
		t.Fatalf("failed to mutate sell price after snapshot: %v", err)
	}

	price, ok := GetActiveModelSellPrice("snapshot-model", ChannelLaneDirect)
	if !ok {
		t.Fatalf("expected cached snapshot price")
	}
	if price.InputPerMtok == nil || math.Abs(*price.InputPerMtok-input) > 0.0001 {
		t.Fatalf("expected cached input price %.2f, got %+v", input, price.InputPerMtok)
	}

	if err := RefreshPricingSnapshot(); err != nil {
		t.Fatalf("failed to refresh pricing snapshot after coreDB mutation: %v", err)
	}
	if _, ok := GetActiveModelSellPrice("snapshot-model", ChannelLaneDirect); ok {
		t.Fatalf("expected refreshed snapshot to drop inactive sell price")
	}
}

func TestModelSellPriceSupportsIndependentDirectAndCheapLanes(t *testing.T) {
	db := withPricingTestDB(t, "pricing_lane_prices")
	directInput := 10.0
	cheapInput := 3.0
	nextCheapInput := 2.0

	if err := UpsertActiveModelSellPrice(ModelSellPrice{
		ModelName:      "gpt-lane-test",
		ChannelGroup:   ChannelLaneDirect,
		InputPerMtok:   &directInput,
		PricingVersion: 1,
	}); err != nil {
		t.Fatalf("failed to upsert direct price: %v", err)
	}
	if err := UpsertActiveModelSellPrice(ModelSellPrice{
		ModelName:      "gpt-lane-test",
		ChannelGroup:   ChannelLaneAgent,
		InputPerMtok:   &cheapInput,
		PricingVersion: 2,
	}); err != nil {
		t.Fatalf("failed to upsert cheap price: %v", err)
	}
	if err := UpsertActiveModelSellPrice(ModelSellPrice{
		ModelName:      "gpt-lane-test",
		ChannelGroup:   ChannelLaneAgent,
		InputPerMtok:   &nextCheapInput,
		PricingVersion: 3,
	}); err != nil {
		t.Fatalf("failed to upsert next cheap price: %v", err)
	}

	direct, ok := GetActiveModelSellPrice("gpt-lane-test", ChannelLaneDirect)
	if !ok || direct.InputPerMtok == nil || *direct.InputPerMtok != directInput {
		t.Fatalf("expected active direct price %.1f, got %#v ok=%v", directInput, direct, ok)
	}
	cheap, ok := GetActiveModelSellPrice("gpt-lane-test", ChannelLaneAgent)
	if !ok || cheap.InputPerMtok == nil || *cheap.InputPerMtok != nextCheapInput {
		t.Fatalf("expected active cheap price %.1f, got %#v ok=%v", nextCheapInput, cheap, ok)
	}

	var activeCount int64
	if err := db.Model(&ModelSellPrice{}).
		Where("model_name = ? AND active = ?", "gpt-lane-test", true).
		Count(&activeCount).Error; err != nil {
		t.Fatalf("failed to count active lane prices: %v", err)
	}
	if activeCount != 2 {
		t.Fatalf("expected one active direct and one active cheap price, got %d", activeCount)
	}
}

func TestCheapOnlyModelDefaultsToPricedLaneWithoutOverridingExplicitDirect(t *testing.T) {
	db := withPricingTestDB(t, "cheap_only_default_lane")
	if err := db.AutoMigrate(&Channel{}, &Vendor{}); err != nil {
		t.Fatalf("migrate catalog sources: %v", err)
	}
	vendor := Vendor{Name: "Anthropic", PublicID: "ven_cheap_only", Status: 1}
	if err := db.Create(&vendor).Error; err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	meta := Model{ModelName: "claude-opus-5-5", VendorID: vendor.Id, Status: 1, LifecycleStatus: ModelLifecycleActive}
	if err := db.Create(&meta).Error; err != nil {
		t.Fatalf("create model metadata: %v", err)
	}
	channel := Channel{Name: "reverse", Type: constant.ChannelTypeAnthropic, Status: common.ChannelStatusEnabled, Group: ChannelLaneAgent, Models: meta.ModelName}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("create agent-lane channel: %v", err)
	}
	ability := Ability{Group: ChannelLaneAgent, Model: meta.ModelName, ChannelId: channel.Id, Enabled: true, Role: AbilityRolePrimary}
	if err := db.Create(&ability).Error; err != nil {
		t.Fatalf("create cheap ability: %v", err)
	}
	input := 0.92
	if err := UpsertActiveModelSellPrice(ModelSellPrice{ModelName: meta.ModelName, ChannelGroup: ChannelLaneAgent, InputPerMtok: &input}); err != nil {
		t.Fatalf("save cheap sell price: %v", err)
	}
	lastGetPricingTime = time.Time{}
	t.Cleanup(func() { lastGetPricingTime = time.Time{} })
	pricing := GetPricing()
	if len(pricing) != 1 || pricing[0].InputPerMtok == nil || *pricing[0].InputPerMtok != input {
		t.Fatalf("agent-lane-only model should expose its default price, got %#v", pricing)
	}
	if candidates, lane, _ := LaneCandidatesForModel(meta.ModelName, nil, nil); lane != ChannelLaneAgent || len(candidates) != 1 || candidates[0] != ChannelLaneAgent {
		t.Fatalf("unpinned Key should route agent-lane-only model to cheap: lane=%q candidates=%#v", lane, candidates)
	}
	if candidates, lane, _ := LaneCandidatesForModel(meta.ModelName, TokenModelLaneMap{meta.ModelName: ChannelLaneDirect}, nil); lane != ChannelLaneDirect || len(candidates) != 1 || candidates[0] != ChannelLaneDirect {
		t.Fatalf("explicit model direct pin should remain strict: lane=%q candidates=%#v", lane, candidates)
	}
	if candidates, lane, _ := LaneCandidatesForModel(meta.ModelName, nil, TokenVendorGroupMap{vendor.PublicID: ChannelLaneDirect}); lane != ChannelLaneDirect || len(candidates) != 1 || candidates[0] != ChannelLaneDirect {
		t.Fatalf("explicit vendor direct pin should remain strict: lane=%q candidates=%#v", lane, candidates)
	}
	directInput := 4.0
	if err := UpsertActiveModelSellPrice(ModelSellPrice{ModelName: meta.ModelName, ChannelGroup: ChannelLaneDirect, InputPerMtok: &directInput}); err != nil {
		t.Fatalf("save direct sell price: %v", err)
	}
	if row := GetActiveModelSellPriceMap()[meta.ModelName]; row.InputPerMtok == nil || *row.InputPerMtok != directInput {
		t.Fatalf("direct price must take precedence over cheap, got %#v", row)
	}
}

func TestUpsertModelSellPriceRejectsInvalidOrEmptyPrices(t *testing.T) {
	withPricingTestDB(t, "sell_price_validation")
	negative := -0.01
	if err := UpsertActiveModelSellPrice(ModelSellPrice{
		ModelName:    "negative-model",
		ChannelGroup: ChannelLaneDirect,
		RequestUSD:   &negative,
	}); !types.IsErrCode(err, types.ErrorCodeInvalidRequest) || err.(*types.APIError).Details["field"] != "request_usd" {
		t.Fatalf("expected negative price rejection, got %v", err)
	}
	if err := UpsertActiveModelSellPrice(ModelSellPrice{
		ModelName:    "empty-model",
		ChannelGroup: ChannelLaneDirect,
	}); !types.IsErrCode(err, types.ErrorCodeSellPriceMissing) {
		t.Fatalf("expected empty price rejection, got %v", err)
	}
}

func TestSaveModelOpsTierConfigurationsRollsBackWholeRow(t *testing.T) {
	db := withPricingTestDB(t, "model_ops_tier_config_atomic")
	if err := db.AutoMigrate(&Channel{}, &Ability{}); err != nil {
		t.Fatalf("failed to migrate model ops tables: %v", err)
	}
	original := 1.0
	if err := db.Create(&ModelSellPrice{ModelName: "atomic-model", ChannelGroup: ChannelLaneDirect, InputPerMtok: &original, Active: true, Source: "manual", PricingVersion: 1}).Error; err != nil {
		t.Fatalf("failed to create original price: %v", err)
	}
	if err := db.Create(&Channel{Id: 10, PublicID: "channel-10", Key: "test", Status: common.ChannelStatusEnabled, Name: "atomic-source", Group: ChannelLaneDirect}).Error; err != nil {
		t.Fatalf("failed to create source: %v", err)
	}
	if err := db.Create(&Ability{Group: ChannelLaneDirect, Model: "atomic-model", ChannelId: 10, Enabled: true, Weight: 5}).Error; err != nil {
		t.Fatalf("failed to create ability: %v", err)
	}

	updated := 2.0
	err := SaveModelOpsTierConfigurations("atomic-model", []ModelOpsTierConfig{{
		Lane:  ChannelLaneDirect,
		Price: &ModelSellPrice{InputPerMtok: &updated, Source: "manual"},
		Weights: []ModelOpsPoolWeight{
			{ChannelID: 10, Weight: 9},
			{ChannelID: 999, Weight: 1},
		},
	}})
	if err == nil {
		t.Fatal("expected missing source membership to fail the transaction")
	}

	var ability Ability
	if err := db.First(&ability, "model = ? AND channel_id = ?", "atomic-model", 10).Error; err != nil {
		t.Fatalf("failed to reload ability: %v", err)
	}
	if ability.Weight != 5 {
		t.Fatalf("expected weight rollback to 5, got %d", ability.Weight)
	}
	var active []ModelSellPrice
	if err := db.Where("model_name = ? AND channel_group = ? AND active = ?", "atomic-model", ChannelLaneDirect, true).Find(&active).Error; err != nil {
		t.Fatalf("failed to reload active price: %v", err)
	}
	if len(active) != 1 || active[0].InputPerMtok == nil || *active[0].InputPerMtok != original {
		t.Fatalf("expected original active price after rollback, got %#v", active)
	}
}

func TestPreConsumeQuotaWithLedgerWritesUserTokenAndLedger(t *testing.T) {
	db := withPricingTestDB(t, "pricing_preconsume_ledger")
	if err := db.Create(&User{Id: 1, Username: "quota-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	if err := PreConsumeQuotaWithLedger(PreConsumeQuotaWithLedgerInput{
		UserID:         1,
		TokenID:        2,
		TokenKey:       "token-key",
		Quota:          100,
		RefType:        "relay_preconsume",
		RefID:          "request-1",
		PricingVersion: 12,
	}); err != nil {
		t.Fatalf("failed to preconsume quota: %v", err)
	}

	var user User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if user.Quota != 900 {
		t.Fatalf("expected user quota 900, got %d", user.Quota)
	}

	var token Token
	if err := db.Select("remain_quota", "used_quota").First(&token, 2).Error; err != nil {
		t.Fatalf("failed to load token: %v", err)
	}
	if token.RemainQuota != 900 || token.UsedQuota != 100 {
		t.Fatalf("expected token remain=900 used=100, got remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}

	var ledger WalletLedger
	if err := db.Where("user_id = ? AND ref_id = ?", 1, "request-1").First(&ledger).Error; err != nil {
		t.Fatalf("failed to load ledger row: %v", err)
	}
	if ledger.DeltaMicrousd != -100 {
		t.Fatalf("expected ledger delta -100, got %d", ledger.DeltaMicrousd)
	}
}

func TestApplyTokenQuotaDeltaTxReturnsTokenKeyForTokenID(t *testing.T) {
	db := withPricingTestDB(t, "pricing_token_id_returns_key")
	if err := db.Create(&Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	var returnedKey string
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		returnedKey, err = applyTokenQuotaDeltaTx(tx, 2, "", 100)
		return err
	})
	if err != nil {
		t.Fatalf("failed to apply token quota delta: %v", err)
	}
	if returnedKey != "token-key" {
		t.Fatalf("expected token key to be returned for token id path, got %q", returnedKey)
	}
}

func TestApplyTokenQuotaDeltaTxUpdatesOnlyResolvedTokenCandidate(t *testing.T) {
	db := withPricingTestDB(t, "pricing_token_key_single_candidate")
	if err := db.Create(&Token{Id: 2, UserId: 1, Key: "sk-lazu-legacy-key", RemainQuota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create normalized token: %v", err)
	}
	if err := db.Create(&Token{Id: 3, UserId: 1, Key: "legacy-key", RemainQuota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create legacy token: %v", err)
	}

	var returnedKey string
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		returnedKey, err = applyTokenQuotaDeltaTx(tx, 0, "sk-legacy-key", 100)
		return err
	})
	if err != nil {
		t.Fatalf("failed to apply token quota delta: %v", err)
	}
	if returnedKey != "sk-lazu-legacy-key" {
		t.Fatalf("expected normalized token key to be returned, got %q", returnedKey)
	}

	var normalized Token
	if err := db.Select("remain_quota", "used_quota").First(&normalized, 2).Error; err != nil {
		t.Fatalf("failed to load normalized token: %v", err)
	}
	if normalized.RemainQuota != 900 || normalized.UsedQuota != 100 {
		t.Fatalf("expected only normalized token to be charged, got remain=%d used=%d", normalized.RemainQuota, normalized.UsedQuota)
	}
	var legacy Token
	if err := db.Select("remain_quota", "used_quota").First(&legacy, 3).Error; err != nil {
		t.Fatalf("failed to load legacy token: %v", err)
	}
	if legacy.RemainQuota != 1_000 || legacy.UsedQuota != 0 {
		t.Fatalf("expected legacy candidate to stay unchanged, got remain=%d used=%d", legacy.RemainQuota, legacy.UsedQuota)
	}
}

func TestPreConsumeQuotaWithLedgerRejectsInsufficientTokenWithoutPartialWrite(t *testing.T) {
	db := withPricingTestDB(t, "pricing_preconsume_token_insufficient")
	if err := db.Create(&User{Id: 1, Username: "quota-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 50}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	err := PreConsumeQuotaWithLedger(PreConsumeQuotaWithLedgerInput{
		UserID:         1,
		TokenID:        2,
		TokenKey:       "token-key",
		Quota:          100,
		RefType:        "relay_preconsume",
		RefID:          "request-1",
		PricingVersion: 12,
	})
	if err == nil || !strings.Contains(err.Error(), "token quota is not enough") {
		t.Fatalf("expected token quota error, got %v", err)
	}

	var user User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if user.Quota != 1_000 {
		t.Fatalf("expected user quota rollback to 1000, got %d", user.Quota)
	}
	var token Token
	if err := db.Select("remain_quota", "used_quota").First(&token, 2).Error; err != nil {
		t.Fatalf("failed to load token: %v", err)
	}
	if token.RemainQuota != 50 || token.UsedQuota != 0 {
		t.Fatalf("expected token unchanged, got remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}
	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("failed to count ledger rows: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("expected no ledger rows, got %d", ledgerCount)
	}
}

func TestPreConsumeQuotaWithLedgerAllowsUnlimitedToken(t *testing.T) {
	db := withPricingTestDB(t, "pricing_preconsume_unlimited_token")
	if err := db.Create(&User{Id: 1, Username: "quota-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 0, UnlimitedQuota: true}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	if err := PreConsumeQuotaWithLedger(PreConsumeQuotaWithLedgerInput{
		UserID:         1,
		TokenID:        2,
		TokenKey:       "token-key",
		Quota:          100,
		RefType:        "relay_preconsume",
		RefID:          "request-1",
		PricingVersion: 12,
	}); err != nil {
		t.Fatalf("expected unlimited token to pass, got %v", err)
	}

	var token Token
	if err := db.Select("remain_quota", "used_quota").First(&token, 2).Error; err != nil {
		t.Fatalf("failed to load token: %v", err)
	}
	if token.RemainQuota != -100 || token.UsedQuota != 100 {
		t.Fatalf("expected unlimited token remain=-100 used=100, got remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}
}

func TestPreConsumeQuotaWithLedgerConcurrentTokenCap(t *testing.T) {
	db := withPricingTestDB(t, "pricing_preconsume_token_concurrent")
	if err := db.Create(&User{Id: 1, Username: "quota-user", Quota: 10_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 500}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	var successCount atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := PreConsumeQuotaWithLedger(PreConsumeQuotaWithLedgerInput{
				UserID:         1,
				TokenID:        2,
				TokenKey:       "token-key",
				Quota:          100,
				RefType:        "relay_preconsume",
				RefID:          fmt.Sprintf("request-%d", i),
				PricingVersion: 12,
			})
			if err == nil {
				successCount.Add(1)
				return
			}
			if !strings.Contains(err.Error(), "token quota is not enough") && !strings.Contains(strings.ToLower(err.Error()), "locked") {
				t.Errorf("unexpected preconsume error: %v", err)
			}
		}()
	}
	wg.Wait()

	if successCount.Load() > 5 {
		t.Fatalf("expected at most five successful deductions, got %d", successCount.Load())
	}
	var token Token
	if err := db.Select("remain_quota", "used_quota").First(&token, 2).Error; err != nil {
		t.Fatalf("failed to load token: %v", err)
	}
	if token.RemainQuota < 0 || token.UsedQuota > 500 {
		t.Fatalf("token cap was exceeded: remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}
}

func TestAdjustRelayQuotaWithLedgerWritesSettlementAndRefundAtomically(t *testing.T) {
	db := withPricingTestDB(t, "pricing_relay_settlement_ledger")
	if err := db.Create(&User{Id: 1, Username: "relay-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	if err := AdjustRelayQuotaWithLedger(AdjustRelayQuotaWithLedgerInput{
		UserID:         1,
		TokenID:        2,
		TokenKey:       "token-key",
		QuotaDelta:     125,
		RefType:        "relay_settlement",
		RefID:          "settle-1",
		PricingVersion: 12,
	}); err != nil {
		t.Fatalf("failed to settle relay quota: %v", err)
	}
	if err := AdjustRelayQuotaWithLedger(AdjustRelayQuotaWithLedgerInput{
		UserID:         1,
		TokenID:        2,
		TokenKey:       "token-key",
		QuotaDelta:     -25,
		RefType:        "relay_settlement",
		RefID:          "refund-1",
		PricingVersion: 12,
	}); err != nil {
		t.Fatalf("failed to refund relay quota: %v", err)
	}

	var user User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if user.Quota != 900 {
		t.Fatalf("expected user quota 900, got %d", user.Quota)
	}

	var token Token
	if err := db.Select("remain_quota", "used_quota").First(&token, 2).Error; err != nil {
		t.Fatalf("failed to load token: %v", err)
	}
	if token.RemainQuota != 900 || token.UsedQuota != 100 {
		t.Fatalf("expected token remain=900 used=100, got remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}

	var ledgerRows []WalletLedger
	if err := db.Where("user_id = ?", 1).Order("ref_id ASC").Find(&ledgerRows).Error; err != nil {
		t.Fatalf("failed to load ledger rows: %v", err)
	}
	if len(ledgerRows) != 2 {
		t.Fatalf("expected two ledger rows, got %d", len(ledgerRows))
	}
	if ledgerRows[0].DeltaMicrousd != 25 || ledgerRows[1].DeltaMicrousd != -125 {
		t.Fatalf("unexpected ledger deltas: %#v", ledgerRows)
	}
}

func TestLegacyUsedQuotaUpdatesWithoutMemoryBatcher(t *testing.T) {
	db := withPricingTestDB(t, "pricing_legacy_used_quota_stats")
	if err := db.Create(&User{Id: 1, Username: "stats-user"}).Error; err != nil {
		t.Fatal(err)
	}
	UpdateUserUsedQuota(1, 100)
	UpdateUserUsedQuota(1, 25)
	var user User
	if err := db.First(&user, 1).Error; err != nil {
		t.Fatal(err)
	}
	if user.UsedQuota != 125 {
		t.Fatalf("legacy used quota=%d", user.UsedQuota)
	}
}

func TestAdjustUserQuotaWithLedgerWritesBalanceAndLedgerTogether(t *testing.T) {
	db := withPricingTestDB(t, "pricing_user_wallet_adjustment")
	if err := db.Create(&User{Id: 1, Username: "wallet-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	if err := AdjustUserQuotaWithLedger(AdjustUserQuotaWithLedgerInput{
		UserID:         1,
		DeltaQuota:     200,
		Kind:           WalletLedgerKindTopup,
		RefType:        "test_credit",
		RefID:          "credit-1",
		PricingVersion: 12,
	}); err != nil {
		t.Fatalf("failed to credit user quota: %v", err)
	}
	if err := AdjustUserQuotaWithLedger(AdjustUserQuotaWithLedgerInput{
		UserID:         1,
		DeltaQuota:     -50,
		Kind:           WalletLedgerKindAdjustment,
		RefType:        "test_debit",
		RefID:          "debit-1",
		PricingVersion: 12,
	}); err != nil {
		t.Fatalf("failed to debit user quota: %v", err)
	}

	var user User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if user.Quota != 1_150 {
		t.Fatalf("expected user quota 1150, got %d", user.Quota)
	}

	var ledgerRows []WalletLedger
	if err := db.Where("user_id = ?", 1).Order("ref_id ASC").Find(&ledgerRows).Error; err != nil {
		t.Fatalf("failed to load ledger rows: %v", err)
	}
	if len(ledgerRows) != 2 {
		t.Fatalf("expected two ledger rows, got %d", len(ledgerRows))
	}
	if ledgerRows[0].DeltaMicrousd != 200 || ledgerRows[1].DeltaMicrousd != -50 {
		t.Fatalf("unexpected ledger deltas: %#v", ledgerRows)
	}
}

func TestAdjustUserQuotaWithLedgerPreservesInterveningBalanceMovement(t *testing.T) {
	db := withPricingTestDB(t, "pricing_user_wallet_signed_adjustment")
	if err := db.Create(&User{Id: 1, Username: "wallet-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// The operator opened the dialog at 1,000, then a request spent 100 before
	// the submitted +200 adjustment arrived. A signed delta must retain both.
	staleDialogBalance := billing.MicroUSD(1_000)
	if err := AdjustUserQuotaWithLedger(AdjustUserQuotaWithLedgerInput{
		UserID:         1,
		DeltaQuota:     -100,
		Kind:           WalletLedgerKindSpend,
		RefType:        "test_spend",
		RefID:          "spend-before-adjustment",
		PricingVersion: 12,
	}); err != nil {
		t.Fatalf("failed to apply intervening spend: %v", err)
	}
	if err := AdjustUserQuotaWithLedger(AdjustUserQuotaWithLedgerInput{
		UserID:         1,
		DeltaQuota:     200,
		Kind:           WalletLedgerKindAdjustment,
		RefType:        "admin_user_adjustment",
		RefID:          "signed-adjustment",
		PricingVersion: 12,
	}); err != nil {
		t.Fatalf("failed to apply signed adjustment: %v", err)
	}

	var user User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if user.Quota != 1_100 {
		t.Fatalf("expected spend and signed adjustment to reconcile to 1100, got %d (stale dialog balance was %d)", user.Quota, staleDialogBalance)
	}

	var ledgerDelta billing.MicroUSD
	if err := db.Model(&WalletLedger{}).
		Where("user_id = ?", 1).
		Select("COALESCE(SUM(delta_microusd), 0)").
		Scan(&ledgerDelta).Error; err != nil {
		t.Fatalf("failed to sum ledger rows: %v", err)
	}
	if ledgerDelta != 100 {
		t.Fatalf("expected net ledger delta 100, got %d", ledgerDelta)
	}
}

func TestSetUserQuotaWithLedgerWritesAbsoluteDelta(t *testing.T) {
	db := withPricingTestDB(t, "pricing_set_user_quota")
	if err := db.Create(&User{Id: 1, Username: "set-quota-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	delta, err := SetUserQuotaWithLedger(1, 1_500, WalletLedgerKindAdjustment, "test_set", "set-1", nil)
	if err != nil {
		t.Fatalf("set user quota: %v", err)
	}
	if delta != 500 {
		t.Fatalf("expected delta 500, got %d", delta)
	}

	delta, err = SetUserQuotaWithLedger(1, 1_500, WalletLedgerKindAdjustment, "test_set", "set-2", nil)
	if err != nil {
		t.Fatalf("set same user quota: %v", err)
	}
	if delta != 0 {
		t.Fatalf("expected no-op delta 0, got %d", delta)
	}

	var user User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if user.Quota != 1_500 {
		t.Fatalf("expected quota 1500, got %d", user.Quota)
	}
	var ledgerRows []WalletLedger
	if err := db.Where("user_id = ?", 1).Find(&ledgerRows).Error; err != nil {
		t.Fatalf("failed to load ledger rows: %v", err)
	}
	if len(ledgerRows) != 1 || ledgerRows[0].DeltaMicrousd != 500 {
		t.Fatalf("unexpected ledger rows: %#v", ledgerRows)
	}
}

func TestSetUserQuotaWithLedgerConcurrentAbsoluteSetsUseCurrentBalance(t *testing.T) {
	db := withPricingTestDB(t, "pricing_set_user_quota_concurrent")
	if err := db.Create(&User{Id: 1, Username: "set-quota-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	targets := []billing.MicroUSD{1_200, 1_500}
	successfulTargets := make(chan billing.MicroUSD, len(targets))
	var unexpectedErrors atomic.Int64
	var wg sync.WaitGroup
	for i, target := range targets {
		i, target := i, target
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := SetUserQuotaWithLedger(1, target, WalletLedgerKindAdjustment, "test_set", fmt.Sprintf("set-concurrent-%d", i), nil)
			if err == nil {
				successfulTargets <- target
				return
			}
			if !strings.Contains(strings.ToLower(err.Error()), "locked") {
				t.Errorf("unexpected set quota error: %v", err)
				unexpectedErrors.Add(1)
			}
		}()
	}
	wg.Wait()
	close(successfulTargets)

	if unexpectedErrors.Load() > 0 {
		t.Fatalf("saw %d unexpected set quota errors", unexpectedErrors.Load())
	}
	successTargets := make(map[billing.MicroUSD]struct{})
	for target := range successfulTargets {
		successTargets[target] = struct{}{}
	}
	if len(successTargets) == 0 {
		t.Fatal("expected at least one successful set quota")
	}

	var user User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if _, ok := successTargets[user.Quota]; !ok {
		t.Fatalf("expected final quota to match one successful target %#v, got %d", successTargets, user.Quota)
	}

	var ledgerDelta billing.MicroUSD
	if err := db.Model(&WalletLedger{}).
		Where("user_id = ?", 1).
		Select("COALESCE(SUM(delta_microusd), 0)").
		Scan(&ledgerDelta).Error; err != nil {
		t.Fatalf("failed to sum ledger rows: %v", err)
	}
	if ledgerDelta != user.Quota-1_000 {
		t.Fatalf("ledger delta %d does not reconcile final quota %d from starting quota 1000", ledgerDelta, user.Quota)
	}
}

func TestLegacyUsageDataAndLogQuotaMigration(t *testing.T) {
	db := withPricingTestDB(t, "pricing_legacy_scaffold_migration")
	if err := db.AutoMigrate(&Log{}, &UsageData{}, &Option{}); err != nil {
		t.Fatalf("failed to migrate log and usage tables: %v", err)
	}
	if err := db.Exec("ALTER TABLE logs ADD COLUMN quota integer DEFAULT 0").Error; err != nil {
		t.Fatalf("failed to add legacy log quota column: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE quota_data (
			id integer primary key autoincrement,
			user_id integer,
			username text,
			model_name text,
			created_at integer,
			token_used integer,
			count integer,
			quota integer
		)
	`).Error; err != nil {
		t.Fatalf("failed to create legacy quota_data table: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO quota_data (user_id, username, model_name, created_at, token_used, count, quota)
		VALUES (1, 'alice', 'gpt-4o', 123, 50, 2, 200)
	`).Error; err != nil {
		t.Fatalf("failed to insert legacy quota data: %v", err)
	}
	if err := db.Table("usage_data").Create(&UsageData{
		UserID:         1,
		Username:       "alice",
		ModelName:      "gpt-4o",
		CreatedAt:      123,
		TokenUsed:      2,
		Count:          1,
		AmountMicrousd: 5,
	}).Error; err != nil {
		t.Fatalf("failed to insert existing usage row: %v", err)
	}
	if err := db.Create([]Option{
		{Key: "ModelRatio", Value: "{}"},
		{Key: "QuotaPerUnit", Value: "500000"},
		{Key: "SystemName", Value: "Lazu"},
	}).Error; err != nil {
		t.Fatalf("failed to insert legacy options: %v", err)
	}

	if err := migrateLegacyUsageData(db); err != nil {
		t.Fatalf("failed to migrate legacy usage data: %v", err)
	}
	if err := deleteLegacyPricingOptions(db); err != nil {
		t.Fatalf("failed to delete legacy pricing options: %v", err)
	}
	if db.Migrator().HasTable("quota_data") {
		t.Fatalf("expected quota_data table to be dropped")
	}
	if err := dropLegacyPricingColumns(db); err != nil {
		t.Fatalf("failed to drop legacy pricing columns: %v", err)
	}
	columnTypes, err := db.Migrator().ColumnTypes("logs")
	if err != nil {
		t.Fatalf("failed to inspect logs columns: %v", err)
	}
	for _, columnType := range columnTypes {
		if columnType.Name() == "quota" {
			t.Fatalf("expected logs.quota to be dropped")
		}
	}

	var usage UsageData
	if err := db.Table("usage_data").
		Where("user_id = ? and username = ? and model_name = ? and created_at = ?", 1, "alice", "gpt-4o", 123).
		First(&usage).Error; err != nil {
		t.Fatalf("failed to load migrated usage row: %v", err)
	}
	if usage.Count != 3 || usage.TokenUsed != 52 || usage.AmountMicrousd != 205 {
		t.Fatalf("unexpected migrated usage row: %#v", usage)
	}

	var options []Option
	if err := db.Find(&options).Error; err != nil {
		t.Fatalf("failed to load options: %v", err)
	}
	if len(options) != 1 || options[0].Key != "SystemName" {
		t.Fatalf("expected only non-pricing option to remain, got %#v", options)
	}
}

func TestLegacyUserSettingKeyMigration(t *testing.T) {
	db := withPricingTestDB(t, "pricing_legacy_user_setting")
	for _, u := range []User{
		{Id: 1, Username: "settings-user", Setting: `{"notify_type":"email","accept_unset_model_price":true}`},
		{Id: 2, Username: "settings-user-2", Setting: `{"notify_type":"email","accept_unpriced_model":true}`},
	} {
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("failed to create user: %v", err)
		}
	}

	if err := migrateLegacyUserSettingKeys(db); err != nil {
		t.Fatalf("failed to migrate legacy user setting: %v", err)
	}

	for _, id := range []int{1, 2} {
		var user User
		if err := db.Select("setting").First(&user, id).Error; err != nil {
			t.Fatalf("failed to load user setting: %v", err)
		}
		if strings.Contains(user.Setting, "accept_unset_model_price") || strings.Contains(user.Setting, "accept_unpriced_model") {
			t.Fatalf("expected retired unpriced-model keys to be removed, got %s", user.Setting)
		}
		if !strings.Contains(user.Setting, "notify_type") {
			t.Fatalf("expected other settings to survive, got %s", user.Setting)
		}
	}
}

func TestUserNeedsPasswordMigrationMovesSecurityStateOutOfSettings(t *testing.T) {
	db := withPricingTestDB(t, "user_needs_password_migration")
	if err := db.Create(&User{
		Id:       1,
		Username: "social-settings-user",
		Setting:  `{"onboarding_active":true,"needs_password":true,"future_setting":{"mode":"on"}}`,
	}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	deleted := User{
		Id:       2,
		Username: "deleted-social-settings-user",
		Setting:  `{"needs_password":true,"future_setting":{"mode":"deleted"}}`,
	}
	if err := db.Create(&deleted).Error; err != nil {
		t.Fatalf("failed to create deleted user: %v", err)
	}
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatalf("failed to soft-delete user: %v", err)
	}

	if err := migrateUserNeedsPassword(db); err != nil {
		t.Fatalf("failed to migrate needs_password: %v", err)
	}

	var user User
	if err := db.First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load migrated user: %v", err)
	}
	if !user.NeedsPassword || !user.GetSetting().NeedsPassword {
		t.Fatalf("dedicated needs_password state was not preserved: %+v", user)
	}
	if strings.Contains(user.Setting, "needs_password") {
		t.Fatalf("legacy credential key remained in mutable settings: %s", user.Setting)
	}
	if !strings.Contains(user.Setting, "future_setting") {
		t.Fatalf("migration erased an unknown setting: %s", user.Setting)
	}
	var migratedDeleted User
	if err := db.Unscoped().First(&migratedDeleted, deleted.Id).Error; err != nil {
		t.Fatalf("failed to load migrated soft-deleted user: %v", err)
	}
	if !migratedDeleted.NeedsPassword || strings.Contains(migratedDeleted.Setting, "needs_password") {
		t.Fatalf("soft-deleted user was not migrated: needs_password=%t setting=%s", migratedDeleted.NeedsPassword, migratedDeleted.Setting)
	}

	// Re-running the migration must not change the protected column.
	if err := migrateUserNeedsPassword(db); err != nil {
		t.Fatalf("idempotent migration failed: %v", err)
	}
	if err := db.First(&user, 1).Error; err != nil {
		t.Fatalf("reload migrated user: %v", err)
	}
	if !user.NeedsPassword {
		t.Fatal("idempotent migration downgraded needs_password")
	}
}

func TestUserNeedsPasswordMigrationMarksLegacyOIDCPlaceholderAccountsOnce(t *testing.T) {
	db := withPricingTestDB(t, "legacy_oidc_needs_password_migration")
	if err := db.AutoMigrate(&UserIdentity{}); err != nil {
		t.Fatalf("migrate user identities: %v", err)
	}

	// The legacy OIDC columns are no longer on the User struct (schema v15
	// drops them after this migration has run), so recreate them raw.
	for _, column := range []string{"oidc_issuer", "oidc_subject"} {
		if err := db.Exec("ALTER TABLE users ADD COLUMN " + column + " varchar(255)").Error; err != nil {
			t.Fatalf("add legacy column %s: %v", column, err)
		}
	}
	legacy := User{
		Id:       11,
		Username: "legacy-oidc-placeholder",
		Password: "legacy-placeholder-digest",
	}
	current := User{
		Id:       12,
		Username: "current-oidc-real-password",
		Password: "placeholder-current-password-hash",
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy OIDC user: %v", err)
	}
	if err := db.Create(&current).Error; err != nil {
		t.Fatalf("create current OIDC user: %v", err)
	}
	for id, subject := range map[int]string{legacy.Id: "legacy-subject", current.Id: "current-subject"} {
		if err := db.Exec("UPDATE users SET oidc_issuer = ?, oidc_subject = ? WHERE id = ?", "https://issuer.example.com", subject, id).Error; err != nil {
			t.Fatalf("set legacy OIDC columns: %v", err)
		}
	}
	if err := db.Create(&UserIdentity{
		UserID: current.Id, Provider: "oidc", Subject: "v1:current-subject", Email: current.Email,
	}).Error; err != nil {
		t.Fatalf("create current OIDC identity fence: %v", err)
	}

	if err := migrateUserNeedsPassword(db); err != nil {
		t.Fatalf("migrate legacy OIDC password state: %v", err)
	}
	var migratedLegacy, preservedCurrent User
	if err := db.First(&migratedLegacy, legacy.Id).Error; err != nil {
		t.Fatalf("reload legacy OIDC user: %v", err)
	}
	if err := db.First(&preservedCurrent, current.Id).Error; err != nil {
		t.Fatalf("reload current OIDC user: %v", err)
	}
	if !migratedLegacy.NeedsPassword {
		t.Fatal("legacy OIDC placeholder password was exposed as a usable password")
	}
	if preservedCurrent.NeedsPassword {
		t.Fatal("identity migration fence reclassified a current real password")
	}

	if err := migrateUserNeedsPassword(db); err != nil {
		t.Fatalf("rerun legacy OIDC migration: %v", err)
	}
	if err := db.First(&preservedCurrent, current.Id).Error; err != nil {
		t.Fatalf("reload current OIDC user after rerun: %v", err)
	}
	if preservedCurrent.NeedsPassword {
		t.Fatal("idempotent migration reclassified a current OIDC password")
	}
}

func TestNeedsPasswordProjectionMatchesCacheAndDatabaseFallback(t *testing.T) {
	withPricingTestDB(t, "user_needs_password_projection")
	user := User{
		Username:      "social-cache-user",
		Password:      "placeholder-password",
		Email:         "social-cache@example.com",
		Status:        common.UserStatusEnabled,
		NeedsPassword: true,
		// A stale JSON value must never override the dedicated column.
		Setting: `{"needs_password":false,"onboarding_active":true}`,
	}
	if err := user.Insert(0); err != nil {
		t.Fatalf("failed to create social user: %v", err)
	}

	cachedProjection := user.ToBaseUser()
	if !cachedProjection.GetSetting().NeedsPassword {
		t.Fatal("cache projection lost needs_password")
	}
	databaseFallback, err := GetUserCache(user.Id)
	if err != nil {
		t.Fatalf("database cache fallback failed: %v", err)
	}
	if !databaseFallback.GetSetting().NeedsPassword {
		t.Fatal("database cache fallback lost needs_password")
	}
	if cachedProjection.GetSetting().NeedsPassword != databaseFallback.GetSetting().NeedsPassword {
		t.Fatalf("cache and database projections disagree: cache=%t db=%t", cachedProjection.GetSetting().NeedsPassword, databaseFallback.GetSetting().NeedsPassword)
	}
	directSetting, err := GetUserSetting(user.Id, true)
	if err != nil {
		t.Fatalf("direct setting fallback failed: %v", err)
	}
	if !directSetting.NeedsPassword {
		t.Fatal("direct setting fallback lost needs_password")
	}
}

func TestWalletLedgerReconciliationUsesMicrousdCacheUnits(t *testing.T) {
	db := withPricingTestDB(t, "pricing_reconciliation")
	if err := db.Create(&User{Id: 1, Username: "wallet-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := AppendWalletLedgerEvent(1, 1_000, WalletLedgerKindAdjustment, "opening_balance", "user:1", 12, nil); err != nil {
		t.Fatalf("failed to append ledger: %v", err)
	}

	rec, err := ReconcileWalletLedgerForUser(1)
	if err != nil {
		t.Fatalf("failed to reconcile wallet: %v", err)
	}
	if rec.DeltaMicrousd != 0 {
		t.Fatalf("expected reconciled wallet, got delta %d", rec.DeltaMicrousd)
	}
}

func TestUserQuotaMicroUSDRoundTripAboveInt4Limit(t *testing.T) {
	db := withPricingTestDB(t, "quota_bigint_round_trip")
	quota := billing.MicroUSD(2_147_483_648)
	if err := migrateUserQuotaColumnsToBigint(db); err != nil {
		t.Fatalf("failed to migrate user quota columns: %v", err)
	}
	if err := db.Create(&User{
		Id:        1,
		Username:  "bigint-quota-user",
		Quota:     quota,
		UsedQuota: quota,
	}).Error; err != nil {
		t.Fatalf("failed to create high-quota user: %v", err)
	}

	var user User
	if err := db.Select("quota", "used_quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to reload high-quota user: %v", err)
	}
	if user.Quota != quota || user.UsedQuota != quota {
		t.Fatalf("expected quota columns to round-trip %d, got quota=%d used=%d", quota, user.Quota, user.UsedQuota)
	}
}
