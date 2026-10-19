package model

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestGetUserWalletLedgerRangeAggregatesEveryMovementAndTreatsSeedAsBaseline(t *testing.T) {
	db := withPricingTestDB(t, "wallet_ledger_range")
	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(31 * 24 * time.Hour)
	rows := []WalletLedger{
		{UserID: 7, DeltaMicrousd: 10_000_000, Kind: WalletLedgerKindTopup, RefType: "opening", RefID: "before", PricingVersion: 1, CreatedAt: start.Add(-time.Hour)},
		{UserID: 7, DeltaMicrousd: 20_000_000, Kind: WalletLedgerKindAdjustment, RefType: openingBalanceRefType, RefID: "user:7", PricingVersion: 1, CreatedAt: start.Add(30 * time.Minute)},
		{UserID: 7, DeltaMicrousd: -2_000_000, Kind: WalletLedgerKindSpend, RefType: "relay", RefID: "spend", PricingVersion: 1, Metadata: `{"secret":"hidden"}`, CreatedAt: start.Add(time.Hour)},
		{UserID: 7, DeltaMicrousd: 1_000_000, Kind: WalletLedgerKindRefund, RefType: "refund", RefID: "refund", PricingVersion: 1, CreatedAt: start.Add(2 * time.Hour)},
		{UserID: 7, DeltaMicrousd: 500_000, Kind: WalletLedgerKindBonus, RefType: "affiliate_transfer", RefID: "bonus", PricingVersion: 1, CreatedAt: start.Add(3 * time.Hour)},
		{UserID: 7, DeltaMicrousd: -250_000, Kind: WalletLedgerKindAdjustment, RefType: "admin", RefID: "adjustment", PricingVersion: 1, CreatedAt: start.Add(4 * time.Hour)},
		{UserID: 7, DeltaMicrousd: -1_000_000, Kind: WalletLedgerKindSpend, RefType: "relay", RefID: "late-spend", PricingVersion: 1, CreatedAt: start.Add(23*time.Hour + 30*time.Minute)},
		{UserID: 7, DeltaMicrousd: 99_000_000, Kind: WalletLedgerKindTopup, RefType: "later", RefID: "end-boundary", PricingVersion: 1, CreatedAt: end},
		{UserID: 8, DeltaMicrousd: 88_000_000, Kind: WalletLedgerKindTopup, RefType: "other", RefID: "other-user", PricingVersion: 1, CreatedAt: start.Add(time.Hour)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create wallet ledger rows: %v", err)
	}

	ledger, err := GetUserWalletLedgerRange(7, start, end, time.FixedZone("UTC+8", 8*60*60))
	if err != nil {
		t.Fatalf("query wallet ledger range: %v", err)
	}
	if ledger.OpeningBalanceMicrousd != billing.MicroUSD(30_000_000) {
		t.Fatalf("expected opening balance 30000000 including the migration seed, got %d", ledger.OpeningBalanceMicrousd)
	}
	if len(ledger.Items) != 2 {
		t.Fatalf("expected two daily aggregates, got %#v", ledger.Items)
	}
	first := ledger.Items[0]
	if first.Date != "2026-08-01" || first.CreditMicrousd != 1_500_000 || first.SpendMicrousd != 2_250_000 || first.ClosingBalanceMicrousd != 29_250_000 {
		t.Fatalf("unexpected first daily aggregate: %#v", first)
	}
	second := ledger.Items[1]
	if second.Date != "2026-08-02" || second.CreditMicrousd != 0 || second.SpendMicrousd != 1_000_000 || second.ClosingBalanceMicrousd != 28_250_000 {
		t.Fatalf("unexpected second daily aggregate: %#v", second)
	}
}

func TestGetUserWalletLedgerRangeUsesDSTOffsetAtEachMovement(t *testing.T) {
	db := withPricingTestDB(t, "wallet_ledger_dst_range")
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load DST timezone: %v", err)
	}
	start := time.Date(2026, time.March, 7, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.March, 10, 0, 0, 0, 0, time.UTC)
	rows := []WalletLedger{
		// 23:30 EST on March 7. A fixed -04:00 offset would misbucket this as March 8.
		{UserID: 9, DeltaMicrousd: 1_000_000, Kind: WalletLedgerKindTopup, RefType: "topup", RefID: "before-dst", PricingVersion: 1, CreatedAt: time.Date(2026, time.March, 8, 4, 30, 0, 0, time.UTC)},
		// 00:30 EDT on March 9, after the spring-forward transition.
		{UserID: 9, DeltaMicrousd: -250_000, Kind: WalletLedgerKindSpend, RefType: "relay", RefID: "after-dst", PricingVersion: 1, CreatedAt: time.Date(2026, time.March, 9, 4, 30, 0, 0, time.UTC)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create DST wallet ledger rows: %v", err)
	}

	ledger, err := GetUserWalletLedgerRange(9, start, end, location)
	if err != nil {
		t.Fatalf("query DST wallet ledger range: %v", err)
	}
	if len(ledger.Items) != 2 || ledger.Items[0].Date != "2026-03-07" || ledger.Items[1].Date != "2026-03-09" {
		t.Fatalf("expected per-movement DST grouping, got %#v", ledger.Items)
	}
	if ledger.Items[0].ClosingBalanceMicrousd != 1_000_000 || ledger.Items[1].ClosingBalanceMicrousd != 750_000 {
		t.Fatalf("unexpected DST closing balances: %#v", ledger.Items)
	}
}

func TestGetUserWalletLedgerRangeRejectsInvalidRange(t *testing.T) {
	withPricingTestDB(t, "wallet_ledger_invalid_range")
	now := time.Now()
	if _, err := GetUserWalletLedgerRange(1, now, now, time.UTC); err == nil {
		t.Fatal("expected an invalid range error")
	}
}
