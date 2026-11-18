package usage

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// https://developers.openai.com/api/docs/guides/prompt-caching#cache-lifetime
func TestOpenAICacheLifetimesAreExclusiveBillableInput(t *testing.T) {
	for _, tc := range []struct {
		name, raw   string
		short, long int64
	}{
		{"mixed", `{"input_tokens":2000,"output_tokens":10,"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":1600,"cache_write_tokens_details":{"ttl_30m_tokens":600,"ttl_12h_tokens":1000}}}`, 600, 1000},
		{"missing aggregate", `{"input_tokens":2000,"output_tokens":10,"input_tokens_details":{"cached_tokens":100,"cache_write_tokens_details":{"ttl_30m_tokens":600,"ttl_12h_tokens":1000}}}`, 600, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var u dto.Usage
			if err := json.Unmarshal([]byte(tc.raw), &u); err != nil {
				t.Fatal(err)
			}
			summary := SummaryFromDTO(&u)
			got := BillingUsageFromSummary(summary)
			if got.InputTokens != 300 || got.CacheReadTokens != 100 || got.CacheWrite30mTokens != tc.short || got.CacheWrite12hTokens != tc.long || got.CacheWrite5mTokens != 0 || got.CacheWrite1hTokens != 0 || got.InputTokenTotal() != 2000 {
				t.Fatalf("incorrect partition: %+v", got)
			}
			input, output, read, short, long := billing.MicroUSD(2_000_000), billing.MicroUSD(10_000_000), billing.MicroUSD(100_000), billing.MicroUSD(2_500_000), billing.MicroUSD(5_000_000)
			quote := billing.Quote(got, billing.PriceSheet{InputPerMtokMicrousd: &input, OutputPerMtokMicrousd: &output, CacheReadPerMtokMicrousd: &read, CacheWrite30mPerMtokMicrousd: &short, CacheWrite12hPerMtokMicrousd: &long}, nil)
			if quote.TotalMicrousd != 7210 || len(quote.MissingPriceDimensions) != 0 {
				t.Fatalf("quote: %+v", quote)
			}
			fact := FactFromProviderUsage(&u, nil, ProviderFamilyOpenAI)
			raw, err := json.Marshal(fact)
			if err != nil {
				t.Fatal(err)
			}
			restored, ok := ParseFact(string(raw))
			if !ok {
				t.Fatal("fact roundtrip")
			}
			if len(restored.Dimensions) != 5 {
				t.Fatalf("dimensions: %+v", restored.Dimensions)
			}
			encoded, err := json.Marshal(u.PromptTokensDetails)
			if err != nil {
				t.Fatal(err)
			}
			var details dto.InputTokenDetails
			if err = json.Unmarshal(encoded, &details); err != nil {
				t.Fatal(err)
			}
			if details.CacheWrite30mTokens != 600 || details.CacheWrite12hTokens != 1000 {
				t.Fatalf("wire lost lifetimes: %s", encoded)
			}
		})
	}
}

func TestOpenAILifetimeMissingPriceIsNotShortCache(t *testing.T) {
	short := billing.MicroUSD(2_500_000)
	q := billing.Quote(billing.Usage{CacheWrite12hTokens: 1000}, billing.PriceSheet{CacheWrite5mPerMtokMicrousd: &short, CacheWrite30mPerMtokMicrousd: &short}, nil)
	if len(q.MissingPriceDimensions) != 1 || q.MissingPriceDimensions[0] != "cache_write_12h" {
		t.Fatalf("must require 12h price: %+v", q)
	}
}
