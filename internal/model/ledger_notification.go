package model

import (
	"sync"

	"github.com/lazu-ai/lazu/internal/ledger"
)

var ledgerSettlementNotifier struct {
	sync.RWMutex
	notify func([]ledger.Event)
}

// Notification queries and delivery belong to the background financial writer.
func SetLedgerSettlementNotifier(notify func([]ledger.Event)) {
	ledgerSettlementNotifier.Lock()
	defer ledgerSettlementNotifier.Unlock()
	ledgerSettlementNotifier.notify = notify
}

func notifyCommittedLedgerSettlements(events []ledger.Event) {
	if len(events) == 0 {
		return
	}
	ledgerSettlementNotifier.RLock()
	notify := ledgerSettlementNotifier.notify
	ledgerSettlementNotifier.RUnlock()
	if notify != nil {
		notify(events)
	}
}
