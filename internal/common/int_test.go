package common

import "testing"

func TestParseIntFromAny(t *testing.T) {
	if got := ParseIntFromAny(12); got != 12 {
		t.Fatalf("expected 12, got %d", got)
	}
	if got := ParseIntFromAny("34"); got != 34 {
		t.Fatalf("expected 34, got %d", got)
	}
	if got := ParseIntFromAny(float64(56)); got != 56 {
		t.Fatalf("expected 56, got %d", got)
	}
	if got := ParseIntFromAny("not-int"); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}
