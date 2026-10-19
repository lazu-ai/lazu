package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lazu-ai/lazu/pkg/billing"
	"github.com/shopspring/decimal"
)

type planAmountExport struct {
	Models []Model `json:"models"`
	Sell   []struct {
		ModelName string   `json:"model_name"`
		Lane      string   `json:"channel_group"`
		Input     *float64 `json:"input_per_mtok"`
		Output    *float64 `json:"output_per_mtok"`
		Read      *float64 `json:"cache_read_per_mtok"`
		Write5m   *float64 `json:"cache_write5m_per_mtok"`
		Write1h   *float64 `json:"cache_write1h_per_mtok"`
	} `json:"sell"`
}

// replayPlanAmounts validates actual quote amounts for each migrated rule,
// including both cache-write periods. Percentage equality alone cannot prove
// what the new official-price anchor does to a bill.
func replayPlanAmounts(t *testing.T, dir, name, lane string, pct decimal.Decimal) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "pricing-export.json"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var data planAmountExport
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	var official Model
	for _, m := range data.Models {
		if m.ModelName == name {
			official = m
			break
		}
	}
	unit := func(v float64) *billing.MicroUSD {
		x := billing.MicroUSD(decimal.NewFromFloat(v).Mul(decimal.NewFromInt(1_000_000)).Round(0).IntPart())
		if x <= 0 {
			return nil
		}
		return &x
	}
	for _, sell := range data.Sell {
		if sell.ModelName != name || sell.Lane != lane {
			continue
		}
		cases := []struct {
			name     string
			value    *float64
			official float64
			usage    billing.Usage
		}{
			{name: "input", value: sell.Input, official: OfficialPerMtok(official.OfficialInputPrice), usage: billing.Usage{InputTokens: 1_000_000}},
			{name: "output", value: sell.Output, official: OfficialPerMtok(official.OfficialOutputPrice), usage: billing.Usage{OutputTokens: 1_000_000}},
			{name: "cache_read", value: sell.Read, official: official.OfficialCacheReadPerMtok, usage: billing.Usage{CacheReadTokens: 1_000_000}},
			{name: "cache_write_5m", value: sell.Write5m, official: official.OfficialCacheWritePerMtok, usage: billing.Usage{CacheWrite5mTokens: 1_000_000}},
			{name: "cache_write_1h", value: sell.Write1h, usage: billing.Usage{CacheWrite1hTokens: 1_000_000}},
		}
		for _, tc := range cases {
			if tc.value == nil {
				continue
			}
			u := unit(*tc.value)
			if u == nil {
				continue
			}
			sheet := billing.PriceSheet{InputPerMtokMicrousd: u, OutputPerMtokMicrousd: u, CacheReadPerMtokMicrousd: u, CacheWrite5mPerMtokMicrousd: u, CacheWrite1hPerMtokMicrousd: u}
			q := billing.Quote(tc.usage, sheet, nil)
			list := unit(tc.official)
			got := billing.ApplyPlanDiscount(q, &billing.OfficialPrices{Input: list, Output: list, CacheRead: list, CacheWrite: list}, pct, billing.PlanDiscountApplied{})
			anchor := *u
			if list != nil {
				anchor = *list
			}
			expected := billing.MicroUSD(decimal.NewFromInt(int64(anchor)).Mul(decimal.NewFromInt(100).Sub(pct)).Div(decimal.NewFromInt(100)).Round(0).IntPart())
			if expected > *u {
				expected = *u
			}
			if got.TotalMicrousd != expected {
				t.Fatalf("%s %s: billed %d want %d", name, tc.name, got.TotalMicrousd, expected)
			}
			old := billing.Quote(tc.usage, sheet, []billing.Adjustment{{Source: "legacy", Active: true, DiscountPct: pct}})
			if old.TotalMicrousd != got.TotalMicrousd {
				t.Logf("amount delta %s %s per 1M: %d → %d microUSD", name, tc.name, old.TotalMicrousd, got.TotalMicrousd)
			}
		}
		return
	}
	t.Fatalf("no sell price for migrated rule %s/%s", name, lane)
}
