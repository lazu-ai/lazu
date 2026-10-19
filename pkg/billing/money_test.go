package billing

import "testing"

func TestUSDConvertsToMicroUSD(t *testing.T) {
	if got := USD(1); got != 1_000_000 {
		t.Fatalf("expected $1 to equal 1000000 microUSD, got %d", got)
	}
	if got := USD(100).Dollars(); got != 100 {
		t.Fatalf("expected 100 USD round-trip, got %f", got)
	}
	if got := USD(0.0000004); got != 0 {
		t.Fatalf("expected sub-microUSD value to round to 0, got %d", got)
	}
	if got := USD(0.0000005); got != 1 {
		t.Fatalf("expected half-microUSD value to round to 1, got %d", got)
	}
}

func TestMicroUSDRejectsImplicitInt64AssignmentExample(t *testing.T) {
	// Negative compile-time example:
	// var raw int64 = 1_000_000
	// var amount MicroUSD = raw
	// Use MicroUSD(raw) or USD(1) at explicit unit boundaries instead.
	var raw int64 = 1_000_000
	amount := MicroUSD(raw)
	if amount != USD(1) {
		t.Fatalf("expected explicit conversion to preserve microUSD, got %d", amount)
	}
}
