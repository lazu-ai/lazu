package model

import (
	"errors"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/ledger"
)

// With the Redis ledger on, a budget charge written straight to SQL is the
// drift the reconciler kept mailing about. Every SQL-direct writer refuses.
func TestSpendBudgetChargeRefusedOutsideLedger(t *testing.T) {
	db := setupFileModelTestDB(t)
	if err := db.AutoMigrate(&Project{}, &SpendBudget{}, &SpendBudgetAlert{}, &SpendBudgetReservation{}, &SpendBudgetCharge{}, &ProjectSpend{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Project{ID: 7, OwnerUserID: 1, Name: "Guarded"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SpendBudget{ProjectID: 7, Scope: SpendBudgetScopeToken, TokenID: 2, Period: SpendBudgetPeriodMonth, LimitMicrousd: 1_000_000, Enabled: true, Enforcement: SpendBudgetEnforcementBlock}).Error; err != nil {
		t.Fatal(err)
	}
	previous := GatewayLedger()
	gatewayLedger.Store(ledger.New(nil, nil))
	t.Cleanup(func() { gatewayLedger.Store(previous) })

	input := SpendBudgetChargeInput{ProjectID: 7, PayerUserID: 1, ActorUserID: 1, UserID: 1, TokenID: 2, RequestID: "req-guard", DeltaMicrousd: 22, OccurredAt: time.Now().UTC()}
	if err := RecordSpendBudgetCharge(input); !errors.Is(err, ErrSpendBudgetChargeOutsideLedger) {
		t.Fatalf("RecordSpendBudgetCharge = %v, want ErrSpendBudgetChargeOutsideLedger", err)
	}
	if err := FinalizeProjectSpendBudget(input); !errors.Is(err, ErrSpendBudgetChargeOutsideLedger) {
		t.Fatalf("FinalizeProjectSpendBudget = %v, want ErrSpendBudgetChargeOutsideLedger", err)
	}
	zero := input
	zero.DeltaMicrousd = 0
	if err := RecordSpendBudgetCharge(zero); err != nil {
		t.Fatalf("a zero charge has nothing to refuse: %v", err)
	}
	for _, table := range []any{&SpendBudgetCharge{}, &ProjectSpend{}, &SpendBudgetAlert{}} {
		var count int64
		if err := db.Model(table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("refused charge wrote %d %T rows", count, table)
		}
	}

	// The same charge is the legacy accounting path once the ledger is off.
	gatewayLedger.Store(nil)
	if err := RecordSpendBudgetCharge(input); err != nil {
		t.Fatalf("legacy SQL accounting refused: %v", err)
	}
}
