package model

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestCacheLifetimePricesSurviveSettlementAndContextTier(t *testing.T) {
	var u dto.Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":300000,"output_tokens":10,"input_tokens_details":{"cache_write_tokens":290000,"cache_write_tokens_details":{"ttl_30m_tokens":100000,"ttl_12h_tokens":190000}}}`), &u); err != nil {
		t.Fatal(err)
	}
	fact := usagepkg.FactFromDTO(&u)
	usage := usageFactToBillingUsage(fact)
	price := ModelSellPrice{InputPerMtok: cachePrice(2), OutputPerMtok: cachePrice(10), CacheWrite30mPerMtok: cachePrice(2.5), CacheWrite12hPerMtok: cachePrice(5), ContextTierThreshold: 272000, ContextTierInputPerMtok: cachePrice(4), ContextTierOutputPerMtok: cachePrice(15), ContextTierCacheWrite30mPerMtok: cachePrice(5), ContextTierCacheWrite12hPerMtok: cachePrice(10)}
	tier, applied := price.ForInputTokens(usage.InputTokenTotal())
	if !applied {
		t.Fatal("cache writes must count toward context threshold")
	}
	q := billing.Quote(usage, tier.ToBillingPriceSheet(), nil)
	if q.TotalMicrousd != 2440150 || len(q.MissingPriceDimensions) != 0 {
		t.Fatalf("settlement quote: %+v", q)
	}
	cost := ProviderCostPrice{CacheWrite30mPerMtok: cachePrice(2), CacheWrite12hPerMtok: cachePrice(4), ContextTierThreshold: 272000, ContextTierCacheWrite30mPerMtok: cachePrice(4), ContextTierCacheWrite12hPerMtok: cachePrice(8)}
	costTier, ok := cost.ForInputTokens(300000)
	if !ok || *costTier.ToBillingPriceSheet().CacheWrite12hPerMtokMicrousd != 8000000 {
		t.Fatal("provider tier lost")
	}
	if err := validateModelSellPrice(ModelSellPrice{CacheWrite12hPerMtok: cachePrice(-1)}); err == nil {
		t.Fatal("accepted negative cache price")
	}
}
func cachePrice(v float64) *float64 { return &v }

func TestCacheLifetimePricePersistence(t *testing.T) {
	db := withPricingTestDB(t, t.Name())
	sell := ModelSellPrice{ModelName: "cache-lifetime-test", ChannelGroup: ChannelLaneDirect, InputPerMtok: cachePrice(2), CacheWrite30mPerMtok: cachePrice(2.5), CacheWrite12hPerMtok: cachePrice(5), ContextTierThreshold: 272000, ContextTierCacheWrite30mPerMtok: cachePrice(5), ContextTierCacheWrite12hPerMtok: cachePrice(10)}
	if err := UpsertActiveModelSellPrice(sell); err != nil {
		t.Fatal(err)
	}
	var got ModelSellPrice
	if err := db.Where("model_name = ? AND active = ?", sell.ModelName, true).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.CacheWrite30mPerMtok == nil || *got.CacheWrite30mPerMtok != 2.5 || got.CacheWrite12hPerMtok == nil || *got.CacheWrite12hPerMtok != 5 || got.ContextTierCacheWrite12hPerMtok == nil || *got.ContextTierCacheWrite12hPerMtok != 10 {
		t.Fatalf("lost sell prices: %+v", got)
	}
	cost := ProviderCostPrice{ModelName: sell.ModelName, CacheWrite30mPerMtok: cachePrice(2), CacheWrite12hPerMtok: cachePrice(4), ContextTierCacheWrite30mPerMtok: cachePrice(4), ContextTierCacheWrite12hPerMtok: cachePrice(8)}
	if err := ReplaceProviderCostPricesForChannel(123, []ProviderCostPrice{cost}); err != nil {
		t.Fatal(err)
	}
	rows, err := ListProviderCostPricesForChannel(123)
	if err != nil || len(rows) != 1 {
		t.Fatalf("cost reload: %v %+v", err, rows)
	}
	if rows[0].CacheWrite12hPerMtok == nil || *rows[0].CacheWrite12hPerMtok != 4 || rows[0].ContextTierCacheWrite30mPerMtok == nil || *rows[0].ContextTierCacheWrite30mPerMtok != 4 {
		t.Fatalf("lost cost prices: %+v", rows[0])
	}
}
