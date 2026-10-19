package model

import (
	"errors"
	"math"
	"testing"
)

func TestLedgerCheckedIntegerBoundaries(t *testing.T) {
	for _, value := range []struct {
		left, right, sum, difference  int64
		addOverflow, subtractOverflow bool
	}{
		{left: math.MaxInt64, right: 1, difference: math.MaxInt64 - 1, addOverflow: true},
		{left: math.MinInt64, right: -1, difference: math.MinInt64 + 1, addOverflow: true},
		{left: math.MaxInt64, right: -1, sum: math.MaxInt64 - 1, subtractOverflow: true},
		{left: math.MinInt64, right: 1, sum: math.MinInt64 + 1, subtractOverflow: true},
		{left: math.MinInt64, right: math.MinInt64, difference: 0, addOverflow: true},
		{left: 0, right: math.MinInt64, sum: math.MinInt64, subtractOverflow: true},
		{left: -1, right: math.MinInt64, difference: math.MaxInt64, addOverflow: true},
		{left: 9007199254741017, right: 623456789012345, sum: 9630656043753362, difference: 8383742465728672},
	} {
		sum, err := checkedLedgerAdd(value.left, value.right)
		if value.addOverflow != errors.Is(err, ErrLedgerCounterRange) || !value.addOverflow && sum != value.sum {
			t.Fatalf("add(%d,%d)=%d %v", value.left, value.right, sum, err)
		}
		difference, err := checkedLedgerSubtract(value.left, value.right)
		if value.subtractOverflow != errors.Is(err, ErrLedgerCounterRange) || !value.subtractOverflow && difference != value.difference {
			t.Fatalf("subtract(%d,%d)=%d %v", value.left, value.right, difference, err)
		}
	}
}
