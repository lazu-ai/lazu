package model

import (
	"fmt"
	"math"

	"github.com/lazu-ai/lazu/internal/ledger"
)

var ErrLedgerCounterRange = fmt.Errorf("%w: %w: financial counter exceeds signed 64-bit range", ledger.ErrUnavailable, ledger.ErrPermanentOperation)

func checkedLedgerAdd(left, right int64) (int64, error) {
	if right > 0 && left > math.MaxInt64-right || right < 0 && left < math.MinInt64-right {
		return 0, ErrLedgerCounterRange
	}
	return left + right, nil
}

func checkedLedgerSubtract(left, right int64) (int64, error) {
	if right > 0 && left < math.MinInt64+right || right < 0 && left > math.MaxInt64+right {
		return 0, ErrLedgerCounterRange
	}
	return left - right, nil
}
