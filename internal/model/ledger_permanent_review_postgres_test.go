package model

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/ledger"
)

func TestPR380DeletedLedgerIdentitiesArePermanentFailures(t *testing.T) {
	postgresLedgerBatchFixture(t)
	for _, ids := range [][2]int{{999, 2}, {1, 999}} {
		event := ledgerBatchEvent("pr380-deleted", ids[0], ids[1], 0, 100, time.Now().UTC())
		if err := (redisLedgerStorage{}).Apply(context.Background(), []ledger.Event{event}); !errors.Is(err, ledger.ErrPermanentOperation) {
			t.Fatalf("deleted identity remains retryable: %v", err)
		}
	}
}

func TestPR380UnrepresentableFinancialCounterIsPermanent(t *testing.T) {
	if !errors.Is(ErrLedgerCounterRange, ledger.ErrPermanentOperation) || !errors.Is(ErrLedgerCounterRange, ledger.ErrUnavailable) {
		t.Fatal("unrepresentable immutable counters remain a transient retry")
	}
}
