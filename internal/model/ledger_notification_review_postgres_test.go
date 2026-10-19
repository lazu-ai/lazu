package model

import (
	"context"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/ledger"
)

func TestPR380LedgerWriterNotifiesOnlyCommittedCharges(t *testing.T) {
	db, _ := postgresLedgerBatchFixture(t)
	ledgerSettlementNotifier.RLock()
	previous := ledgerSettlementNotifier.notify
	ledgerSettlementNotifier.RUnlock()
	t.Cleanup(func() { SetLedgerSettlementNotifier(previous) })
	calls := 0
	SetLedgerSettlementNotifier(func(events []ledger.Event) {
		calls += len(events)
		var user User
		if err := db.First(&user, 1).Error; err != nil || user.Quota != 900 {
			t.Fatalf("notification preceded financial commit: balance=%d err=%v", user.Quota, err)
		}
	})
	event := ledgerBatchEvent("pr380-notification", 1, 2, 0, 100, time.Now().UTC())
	if err := (redisLedgerStorage{}).Apply(context.Background(), []ledger.Event{event}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("committed charge notifications=%d", calls)
	}
	if err := (redisLedgerStorage{}).Apply(context.Background(), []ledger.Event{event}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("duplicate financial delivery repeated notifications")
	}
	zero := ledgerBatchEvent("pr380-zero-notification", 1, 2, 0, 0, time.Now().UTC())
	refund := ledgerBatchEvent("pr380-refund-notification", 1, 2, 0, -50, time.Now().UTC())
	refund.Kind = "refund"
	if err := (redisLedgerStorage{}).Apply(context.Background(), []ledger.Event{zero, refund}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("zero charge or refund dispatched consumption notifications")
	}
	bad := ledgerBatchEvent("pr380-failed-notification", 999, 2, 0, 100, time.Now().UTC())
	if err := (redisLedgerStorage{}).Apply(context.Background(), []ledger.Event{bad}); err == nil {
		t.Fatal("missing payer accepted")
	}
	if calls != 1 {
		t.Fatal("rolled-back financial delivery notified")
	}
}
