package billing

import (
	"testing"

	"github.com/shopspring/decimal"
)

func mp(v int64) *MicroUSD { m := MicroUSD(v); return &m }

func TestPlanDiscountIsTakenOffOfficialNotLanePrice(t *testing.T) {
	// Lane sells input at $2.25/M (9折 of the $2.50 list price). A 6折 plan
	// is 60% of list: $1.50/M, not 60% of $2.25.
	sheet := PriceSheet{InputPerMtokMicrousd: mp(2_250_000), OutputPerMtokMicrousd: mp(13_500_000)}
	official := OfficialPrices{Input: mp(2_500_000), Output: mp(15_000_000)}
	q := Quote(Usage{InputTokens: 1_000_000, OutputTokens: 100_000}, sheet, nil)
	got := ApplyPlanDiscount(q, &official, decimal.NewFromInt(40), PlanDiscountApplied{ID: 1, Name: "A", Rule: "vendor"})
	if got.TotalMicrousd != 1_500_000+900_000 {
		t.Fatalf("total = %d, want %d", got.TotalMicrousd, 2_400_000)
	}
	if got.Discount.AmountMicrousd != got.SubtotalMicrousd-got.TotalMicrousd || got.Discount.Plan == nil {
		t.Fatalf("discount = %+v", got.Discount)
	}
}

func TestPlanDiscountNeverChargesMoreThanTheLane(t *testing.T) {
	// An agent lane already at 3折 of list; a 95折 plan must not raise it.
	sheet := PriceSheet{InputPerMtokMicrousd: mp(750_000)}
	official := OfficialPrices{Input: mp(2_500_000)}
	q := Quote(Usage{InputTokens: 1_000_000}, sheet, nil)
	got := ApplyPlanDiscount(q, &official, decimal.NewFromInt(5), PlanDiscountApplied{})
	if got.TotalMicrousd != 750_000 || got.Discount.Plan != nil || !got.Discount.Pct.IsZero() {
		t.Fatalf("got %+v", got)
	}
}

func TestPlanDiscountWithoutOfficialPriceFallsBackToLanePrice(t *testing.T) {
	sheet := PriceSheet{RequestMicrousd: mp(50_000)}
	q := Quote(Usage{RequestCount: 2}, sheet, nil)
	got := ApplyPlanDiscount(q, nil, decimal.NewFromInt(40), PlanDiscountApplied{})
	if got.TotalMicrousd != 60_000 {
		t.Fatalf("total = %d, want 60000", got.TotalMicrousd)
	}
}
