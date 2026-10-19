package model

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func followingPriceNativeFixture(t *testing.T) (*gorm.DB, *ledger.Engine) {
	t.Helper()
	dsn, endpoint := os.Getenv("TEST_GATEWAY_CORE_DSN"), os.Getenv("TEST_LEDGER_ADAPTER_REDIS_URL")
	if dsn == "" || endpoint == "" {
		t.Skip("isolated PostgreSQL and Redis fixtures required")
	}
	open := func(dsn string) *gorm.DB {
		db, err := gorm.Open(safePostgresDialector{postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}).(*postgres.Dialector)}, &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	admin := open(dsn)
	schema := "following_price_" + strings.ReplaceAll(strings.ToLower(common.GenerateRequestID()), "-", "")
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db := open(parsed.String())
	oldCore, oldRedis, oldEnabled := CoreDB(), common.RDB, common.RedisEnabled
	oldSnapshot, _ := activePricingSnapshot.Load().(*pricingSnapshot)
	options, err := redis.ParseURL(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	// This isolated fixture owns DB15 on the ledger agent's native Redis.
	options.DB = 15
	client := redis.NewClient(options)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	clear := func() {
		keys, err := client.Keys(ctx, ledger.Prefix+"*").Result()
		if err != nil {
			t.Error(err)
			return
		}
		keys = append(keys, pricingRegistryRedisKey, pricingRegistryRedisKey+":revision")
		if err := client.Del(ctx, keys...).Err(); err != nil {
			t.Error(err)
		}
	}
	clear()
	SetCoreDB(db)
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		clear()
		_ = client.Close()
		SetCoreDB(oldCore)
		common.RDB, common.RedisEnabled = oldRedis, oldEnabled
		activePricingSnapshot.Store(oldSnapshot)
		_ = admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error
		for _, connection := range []*gorm.DB{db, admin} {
			raw, _ := connection.DB()
			_ = raw.Close()
		}
	})
	models := []interface{}{&Model{}, &ModelSellPrice{}, &ProviderCostPrice{}, &ModelInfoReview{}, &User{}, &Token{}, &Project{}, &WalletLedger{}, &SpendBudget{}, &SpendBudgetCharge{}, &SpendBudgetAlert{}, &ProjectSpend{}, &MediaJob{}}
	models = append(models, RedisLedgerModels()...)
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapLedgerContinuity(ctx, db); err != nil {
		t.Fatal(err)
	}
	engine := ledger.New(client, redisLedgerStorage{})
	if err := BindLedgerContinuity(ctx, db, engine); err != nil {
		t.Fatal(err)
	}
	engine.SpoolDir = t.TempDir()
	return db, engine
}

func followingPriceQuote(t *testing.T, name string) billing.QuoteResult {
	t.Helper()
	price, ok := GetActiveModelSellPrice(name, ChannelLaneDirect)
	if !ok {
		t.Fatalf("missing stored price for %s", name)
	}
	return billing.Quote(billing.Usage{InputTokens: 1000}, price.ToBillingPriceSheet(), nil)
}

func TestPR380FollowingPriceUnitEqualityRejectsInvalidAndDifferentFacts(t *testing.T) {
	perToken := float64(1.3e-8)
	for _, item := range []struct {
		name string
		a, b float64
		want bool
	}{
		{"equivalent_fractional_units", perToken * 1e6, 0.013, true},
		{"different_prices", 2, 3, false},
		{"largest_finite_and_one", math.MaxFloat64, 1, false},
		{"zero_and_positive", 0, math.SmallestNonzeroFloat64, false},
		{"nonfinite_price", math.Inf(1), math.MaxFloat64, false},
		{"unknown_nan", math.NaN(), 1, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			if got := officialPriceUnitsEqual(item.a, item.b); got != item.want {
				t.Fatalf("catalogue unit equality(%g, %g)=%t want %t", item.a, item.b, got, item.want)
			}
		})
	}
}

func TestPR380FollowingPricePresentZeroContextTierPreservesFreeCache(t *testing.T) {
	db, _ := followingPriceNativeFixture(t)
	zero := 0.0
	catalog := Model{ModelName: "gpt-zero-tier-fixture", OfficialInputPrice: 2 / 1e6, OfficialOutputPrice: 8 / 1e6,
		OfficialCacheReadPerMtok: 0.5, OfficialContextTier: ModelContextTier{Threshold: 200_000, InputPerMtok: ratioFloat(4), CacheReadPerMtok: &zero}}
	if err := db.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	if err := SaveModelOpsTierConfigurations(catalog.ModelName, []ModelOpsTierConfig{{Lane: ChannelLaneDirect,
		Price: &ModelSellPrice{FollowOfficial: true, CacheReadPerMtok: ratioFloat(0.5)}}}); err != nil {
		t.Fatal(err)
	}
	price, ok := GetActiveModelSellPrice(catalog.ModelName, ChannelLaneDirect)
	if !ok || price.ContextTierCacheReadPerMtok == nil || *price.ContextTierCacheReadPerMtok != 0 {
		t.Fatalf("present official tier zero became unknown and would fall back to the positive base: %+v", price)
	}
	tier, applied := price.ForInputTokens(200_001)
	quote := billing.Quote(billing.Usage{CacheReadTokens: 200_001}, tier.ToBillingPriceSheet(), nil)
	if !applied || quote.TotalMicrousd != 0 {
		t.Fatalf("explicit free long-context cache billed %d, applied=%t", quote.TotalMicrousd, applied)
	}
	if tier.OutputPerMtok == nil || *tier.OutputPerMtok != 8 {
		t.Fatalf("omitted tier output must retain the known base output: %+v", tier)
	}
}

func TestPR380FollowingPriceAliasPublishesSnapshotAndPreservesHeldVersion(t *testing.T) {
	for _, name := range []string{"claude-sonnet-5-thinking", "gemini-2.5-flash-thinking-128"} {
		t.Run(name, func(t *testing.T) {
			db, engine := followingPriceNativeFixture(t)
			ctx := context.Background()
			catalog := Model{ModelName: name, OfficialInputPrice: 2 / 1e6, OfficialOutputPrice: 8 / 1e6}
			if err := db.Create(&catalog).Error; err != nil {
				t.Fatal(err)
			}
			if err := SaveModelOpsTierConfigurations(name, []ModelOpsTierConfig{{Lane: ChannelLaneDirect, Price: &ModelSellPrice{FollowOfficial: true}}}); err != nil {
				t.Fatal(err)
			}
			oldQuote := followingPriceQuote(t, name)
			if oldQuote.TotalMicrousd != 2000 || oldQuote.PricingVersion <= 0 {
				t.Fatalf("initial quote %+v", oldQuote)
			}
			payer := User{Id: 752, Username: "following-price-payer", Quota: 1_000_000}
			if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&payer).Error; err != nil {
				t.Fatal(err)
			}
			held := ledger.Input{RequestID: "following-price-held-fixture", PayerID: payer.Id, ActorID: payer.Id, Unlimited: true, Amount: int64(oldQuote.TotalMicrousd), PricingVersion: oldQuote.PricingVersion, Model: name, OccurredAt: time.Now().UTC()}
			if err := engine.Reserve(ctx, held); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&catalog).Updates(map[string]interface{}{"official_input_price": 4 / 1e6, "official_output_price": 10 / 1e6}).Error; err != nil {
				t.Fatal(err)
			}
			changes, err := RecomputeFollowingSellPrices([]string{name}, nil, "alias integration fixture")
			if err != nil || len(changes) != 1 {
				t.Fatalf("normalized alias did not reprice: changes=%d err=%v", len(changes), err)
			}
			raw, err := engine.Redis.Get(ctx, pricingRegistryRedisKey).Bytes()
			if err != nil {
				t.Fatal(err)
			}
			var published pricingRegistry
			if err := json.Unmarshal(raw, &published); err != nil || len(published.SellPrices) != 1 {
				t.Fatalf("invalid published registry %+v err=%v", published, err)
			}
			if row := published.SellPrices[0]; row.ModelName != NormalizePricingModelName(name) || !row.FollowOfficial || row.PricingVersion == oldQuote.PricingVersion || row.InputPerMtok == nil || *row.InputPerMtok != 4 {
				t.Fatalf("new stored version did not reach shared registry: %+v", row)
			}
			calls := 0
			block := func(tx *gorm.DB) { calls++; tx.AddError(errors.New("unexpected warmed pricing SQL")) }
			for _, register := range []func(string, func(*gorm.DB)) error{db.Callback().Query().Before("gorm:query").Register, db.Callback().Row().Before("gorm:row").Register, db.Callback().Raw().Before("gorm:raw").Register} {
				if err := register("test:following_warm_sql", block); err != nil {
					t.Fatal(err)
				}
			}
			if err := refreshPricingSnapshotFromRedis(); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				quote := followingPriceQuote(t, name)
				if quote.TotalMicrousd != 4000 || quote.PricingVersion != published.SellPrices[0].PricingVersion {
					t.Fatalf("warm quote did not use published version: %+v", quote)
				}
			}
			if err := engine.Settle(ctx, held, int64(oldQuote.TotalMicrousd)); err != nil {
				t.Fatal(err)
			}
			if calls != 0 {
				t.Fatalf("warmed quote/held settlement made %d Core queries", calls)
			}
			db.Callback().Query().Remove("test:following_warm_sql")
			db.Callback().Row().Remove("test:following_warm_sql")
			db.Callback().Raw().Remove("test:following_warm_sql")
			if _, err := engine.Drain(ctx, "following-price-fixture"); err != nil {
				t.Fatal(err)
			}
			var op RedisLedgerOperation
			if err := db.First(&op, "id = ?", ledgerOperationID(held.RequestID, "settle")).Error; err != nil {
				t.Fatal(err)
			}
			var event ledger.Event
			if err := json.Unmarshal([]byte(op.Input), &event); err != nil || event.PricingVersion != oldQuote.PricingVersion || event.Amount != 2000 {
				t.Fatalf("accepted old version changed: %+v err=%v", event, err)
			}
			var receipt WalletLedger
			if err := db.First(&receipt, "ref_id = ?", held.RequestID).Error; err != nil || receipt.PricingVersion != oldQuote.PricingVersion || receipt.DeltaMicrousd != -2000 {
				t.Fatalf("SQL receipt repriced the old request: %+v err=%v", receipt, err)
			}
			if err := db.First(&payer, payer.Id).Error; err != nil || payer.Quota != 998_000 {
				t.Fatalf("SQL balance did not preserve old quote: quota=%d err=%v", payer.Quota, err)
			}
		})
	}
}

func TestPR380FollowingPriceExplicitZeroAndUnknownRemainDistinct(t *testing.T) {
	db, _ := followingPriceNativeFixture(t)
	for index, item := range []struct {
		name      string
		sources   ModelInfoSources
		wantInput float64
		wantCache float64
		wantWrite float64
	}{
		{"explicit_zero", ModelInfoSources{ModelInfoFieldOfficialPrice: ModelInfoSourceModelsDev, "official_input_price": ModelInfoSourceModelsDevExplicitZero, ModelInfoFieldOfficialCache: ModelInfoSourceModelsDev, "official_cache_read_per_mtok": ModelInfoSourceModelsDevExplicitZero, "official_cache_write_per_mtok": ModelInfoSourceModelsDevExplicitZero}, 0, 0, 0},
		{"group_only_unknown", ModelInfoSources{ModelInfoFieldOfficialPrice: ModelInfoSourceModelsDev, ModelInfoFieldOfficialCache: ModelInfoSourceModelsDev}, 2, 0.5, 1.25},
		{"field_present_not_zero", ModelInfoSources{ModelInfoFieldOfficialPrice: ModelInfoSourceModelsDev, "official_input_price": ModelInfoSourceModelsDev}, 2, 0.5, 1.25},
		{"legacy_unknown", nil, 2, 0.5, 1.25},
		{"manual_group_not_evidence", ModelInfoSources{ModelInfoFieldOfficialPrice: ModelInfoSourceManual, "official_input_price": ModelInfoSourceModelsDevExplicitZero, ModelInfoFieldOfficialCache: ModelInfoSourceManual, "official_cache_read_per_mtok": ModelInfoSourceModelsDevExplicitZero}, 2, 0.5, 1.25},
	} {
		t.Run(item.name, func(t *testing.T) {
			name := "zero-proof-" + item.name
			catalog := Model{ModelName: name, OfficialInputPrice: 0, OfficialOutputPrice: 6 / 1e6, InfoSources: item.sources}
			if err := db.Create(&catalog).Error; err != nil {
				t.Fatal(err)
			}
			price := ModelSellPrice{ModelName: name, ChannelGroup: ChannelLaneDirect, FollowOfficial: true, Active: true, PricingVersion: int64(index + 1), InputPerMtok: ratioFloat(2), OutputPerMtok: ratioFloat(8), CacheReadPerMtok: ratioFloat(0.5), CacheWrite5mPerMtok: ratioFloat(1.25), CacheWrite1hPerMtok: ratioFloat(2)}
			if err := db.Create(&price).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := RecomputeFollowingSellPrices([]string{name}, nil, "zero proof fixture"); err != nil {
				t.Fatal(err)
			}
			got, ok := GetActiveModelSellPrice(name)
			if !ok || got.InputPerMtok == nil || *got.InputPerMtok != item.wantInput || got.CacheReadPerMtok == nil || *got.CacheReadPerMtok != item.wantCache || got.OutputPerMtok == nil || *got.OutputPerMtok != 6 {
				t.Fatalf("zero/unknown billing distinction lost: %+v want input=%g cache=%g", got, item.wantInput, item.wantCache)
			}
			if got.CacheWrite5mPerMtok == nil || *got.CacheWrite5mPerMtok != item.wantWrite || got.CacheWrite1hPerMtok == nil || *got.CacheWrite1hPerMtok != item.wantWrite*1.6 {
				t.Fatalf("zero/unknown cache write lost: %+v want 5m=%g 1h=%g", got, item.wantWrite, item.wantWrite*1.6)
			}
		})
	}
	free := Model{ModelName: "zero-proof-all-free", InfoSources: ModelInfoSources{ModelInfoFieldOfficialPrice: ModelInfoSourceModelsDev, "official_input_price": ModelInfoSourceModelsDevExplicitZero, "official_output_price": ModelInfoSourceModelsDevExplicitZero}}
	if err := db.Create(&free).Error; err != nil {
		t.Fatal(err)
	}
	if err := SaveModelOpsTierConfigurations(free.ModelName, []ModelOpsTierConfig{{Lane: ChannelLaneDirect, Price: &ModelSellPrice{FollowOfficial: true}}}); err != nil {
		t.Errorf("explicitly published free input/output should be derivable: %v", err)
	}
	unknown := Model{ModelName: "zero-proof-all-unknown"}
	if err := db.Create(&unknown).Error; err != nil {
		t.Fatal(err)
	}
	if err := SaveModelOpsTierConfigurations(unknown.ModelName, []ModelOpsTierConfig{{Lane: ChannelLaneDirect, Price: &ModelSellPrice{FollowOfficial: true}}}); err == nil {
		t.Error("missing official prices were accepted as free")
	}
}

func TestPR380FollowingPriceAliasEquivalentUnitsAndTinyPrices(t *testing.T) {
	db, _ := followingPriceNativeFixture(t)
	catalog := []Model{
		{ModelName: "claude-equivalent-fixture-thinking", OfficialInputPrice: 2 / 1e6, OfficialOutputPrice: 8 / 1e6},
		{ModelName: "claude-equivalent-fixture#thinking", OfficialInputPrice: 2, OfficialOutputPrice: 8},
		{ModelName: "claude-fractional-fixture-thinking", OfficialInputPrice: 4e-7, OfficialOutputPrice: 1.6e-6},
		{ModelName: "claude-fractional-fixture#thinking", OfficialInputPrice: 0.4, OfficialOutputPrice: 1.6},
		{ModelName: "claude-fractional-small-fixture-thinking", OfficialInputPrice: 1.3e-8, OfficialOutputPrice: 1.9e-8},
		{ModelName: "claude-fractional-small-fixture#thinking", OfficialInputPrice: 0.013, OfficialOutputPrice: 0.019},
		{ModelName: "claude-tiny-fixture-thinking", OfficialInputPrice: 2e-10, OfficialOutputPrice: 8e-10},
	}
	if err := db.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	for index, item := range []struct {
		name string
		want float64
	}{{"claude-equivalent-fixture", 2}, {"claude-fractional-fixture", 0.4}, {"claude-fractional-small-fixture", 0.013}, {"claude-tiny-fixture", 0.0002}} {
		price := ModelSellPrice{ModelName: item.name, ChannelGroup: ChannelLaneDirect, FollowOfficial: true, Active: true, PricingVersion: int64(index + 1), InputPerMtok: ratioFloat(item.want / 2), OutputPerMtok: ratioFloat(item.want * 2)}
		if err := db.Create(&price).Error; err != nil {
			t.Fatal(err)
		}
		changes, err := RecomputeFollowingSellPrices([]string{item.name}, nil, "equivalent unit fixture")
		if err != nil || len(changes) != 1 || changes[0].After.InputPerMtok == nil || *changes[0].After.InputPerMtok != item.want {
			t.Fatalf("alias unit conversion changed prices: changes=%+v want=%g err=%v", changes, item.want, err)
		}
	}
}

func TestPR380FollowingPriceAmbiguousAliasesRejectWithoutPublishing(t *testing.T) {
	db, engine := followingPriceNativeFixture(t)
	catalog := []Model{{ModelName: "claude-alias-fixture-thinking", OfficialInputPrice: 2 / 1e6, OfficialOutputPrice: 8 / 1e6}, {ModelName: "claude-alias-fixture#thinking", OfficialInputPrice: 3 / 1e6, OfficialOutputPrice: 8 / 1e6}}
	if err := db.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	price := ModelSellPrice{ModelName: "claude-alias-fixture", ChannelGroup: ChannelLaneDirect, FollowOfficial: true, Active: true, PricingVersion: 1, InputPerMtok: ratioFloat(1), OutputPerMtok: ratioFloat(4)}
	if err := db.Create(&price).Error; err != nil {
		t.Fatal(err)
	}
	if err := RefreshPricingSnapshot(); err != nil {
		t.Fatal(err)
	}
	before, err := engine.Redis.Get(context.Background(), pricingRegistryRedisKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecomputeFollowingSellPrices([]string{catalog[0].ModelName}, nil, "ambiguous alias fixture"); err == nil {
		t.Fatal("ambiguous official aliases were silently skipped or chosen")
	}
	after, err := engine.Redis.Get(context.Background(), pricingRegistryRedisKey).Result()
	if err != nil || before != after {
		t.Fatalf("failed alias selection changed shared prices: same=%v err=%v", before == after, err)
	}
	var count int64
	if err := db.Model(&ModelSellPrice{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failed selection published price rows=%d err=%v", count, err)
	}
}
