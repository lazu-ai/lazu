package model

import "testing"

func TestTopUpNormalizeLedgerFields(t *testing.T) {
	topUp := TopUp{
		Amount:           10,
		CreditedMicrousd: 12_000_000,
		TradeNo:          "ref_test",
		PaymentMethod:    "stripe",
	}

	topUp.NormalizeLedgerFields()

	if topUp.PaidAmount != 10 {
		t.Fatalf("expected paid amount 10, got %f", topUp.PaidAmount)
	}
	if topUp.CreditedMicrousd != 12_000_000 {
		t.Fatalf("expected credited microusd 12000000, got %d", topUp.CreditedMicrousd)
	}
	if topUp.BonusMicrousd != 2_000_000 {
		t.Fatalf("expected bonus microusd 2000000, got %d", topUp.BonusMicrousd)
	}
}

func TestTopUpOrderUSDIsTheWholeDollarAmount(t *testing.T) {
	topUp := TopUp{Amount: 25}
	if got := topUp.OrderUSD(); got != 25 {
		t.Fatalf("OrderUSD=%v, want 25", got)
	}
	normalized := TopUp{Amount: 7}
	normalized.NormalizeLedgerFields()
	if normalized.CreditedMicrousd != 7_000_000 || normalized.BonusMicrousd != 0 {
		t.Fatalf("credit must default to the order amount without bonus: %+v", normalized)
	}
	if got := normalized.PaidCreditedMicrousd(); got != 7_000_000 {
		t.Fatalf("PaidCreditedMicrousd=%d, want 7000000", got)
	}
	legacy := TopUp{Amount: 3}
	if got := legacy.PaidCreditedMicrousd(); got != 3_000_000 {
		t.Fatalf("legacy row without credited_microusd falls back to amount, got %d", got)
	}
}
