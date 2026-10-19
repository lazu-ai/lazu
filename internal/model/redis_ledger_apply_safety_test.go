package model

import (
	"context"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/ledger"
)

func TestLedgerApplyRejectsMalformedFinancialEventBeforeWrites(t *testing.T) {
	db := boundaryTestDB(t)
	if err := db.AutoMigrate(&RedisLedgerOperation{}, &WalletLedger{}, &ProjectSpend{}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []struct {
		name string
		edit func(*ledger.Event)
	}{
		{"above-exact-limit", func(e *ledger.Event) { e.Amount = 9007199254740992 }},
		{"negative-settlement", func(e *ledger.Event) { e.Amount = -1 }},
		{"positive-refund", func(e *ledger.Event) { e.Kind = "refund" }},
		{"mismatched-request-identity", func(e *ledger.Event) { e.OperationID = "other-operation" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			event := ledger.Event{Input: ledger.Input{RequestID: "invalid-event", PayerID: 1, ActorID: 1, TokenID: 2, ProjectID: 7, Amount: 70, OccurredAt: time.Now().UTC()}, OperationID: "invalid-event", Kind: "settle"}
			mutate.edit(&event)
			if err := (redisLedgerStorage{}).Apply(context.Background(), []ledger.Event{event}); err == nil {
				t.Fatal("malformed financial input accepted")
			}
			for _, row := range []interface{}{&RedisLedgerOperation{}, &WalletLedger{}, &ProjectSpend{}} {
				var count int64
				if err := db.Model(row).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("rejected event persisted %T: count=%d err=%v", row, count, err)
				}
			}
		})
	}
}
