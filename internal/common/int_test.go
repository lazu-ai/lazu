package common

import (
	"reflect"
	"testing"
)

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

func TestNormalizePositiveIntSlice(t *testing.T) {
	got := NormalizePositiveIntSlice([]int{7, 3, 0, -1, 3, 1, 7})
	want := []int{1, 3, 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected normalized slice: got=%v want=%v", got, want)
	}

	if got := NormalizePositiveIntSlice(nil); got != nil {
		t.Fatalf("expected nil for nil input, got=%v", got)
	}

	if got := NormalizePositiveIntSlice([]int{0, -1, -5}); got != nil {
		t.Fatalf("expected nil when no positive values, got=%v", got)
	}
}
