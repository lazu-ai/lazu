package publicsite

import (
	"testing"

	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestVideoPriceRowsListTiersCheapestFirst(t *testing.T) {
	prices := &billing.VideoPrices{
		ResolutionSecondUSD: map[string]float64{"1080p": 0.569, "480p": 0.103, "720p": 0.231},
		ProviderCostBilled:  true,
	}
	rows := VideoPriceRows(prices, "en")
	want := []ImagePriceRow{
		{"480p", "$0.103 / second"},
		{"720p", "$0.231 / second"},
		{"1080p", "$0.569 / second"},
	}
	if len(rows) != len(want)+2 {
		t.Fatalf("three tiers plus the reference and charge notes, got %+v", rows)
	}
	for i, row := range want {
		if rows[i] != row {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], row)
		}
	}
	if zh := VideoPriceRows(prices, "zh"); zh[0].Price != "$0.103 / 秒" || zh[3].Label != "参考视频" {
		t.Fatalf("Chinese rows: %+v", zh)
	}
	if got := VideoPriceRows(&billing.VideoPrices{ResolutionSecondUSD: map[string]float64{"720p": 0.2}}, "en"); len(got) != 1 {
		t.Fatalf("a lane that is not cost-billed has no settlement notes: %+v", got)
	}
	if VideoPriceRows(nil, "en") != nil {
		t.Fatalf("no video prices, no rows")
	}
}
