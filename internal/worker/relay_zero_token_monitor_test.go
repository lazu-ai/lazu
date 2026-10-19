package worker

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNextZeroTokenBreachCount(t *testing.T) {
	count := 0
	for i := 0; i < zeroTokenMonitorConsecutiveLimit; i++ {
		count = NextZeroTokenBreachCount(count, zeroTokenMonitorThreshold+0.001, 100)
	}
	if count != zeroTokenMonitorConsecutiveLimit {
		t.Fatalf("expected %d consecutive breaches, got %d", zeroTokenMonitorConsecutiveLimit, count)
	}
	if got := NextZeroTokenBreachCount(count, zeroTokenMonitorThreshold, 100); got != 0 {
		t.Fatalf("expected reset at threshold, got %d", got)
	}
	if got := NextZeroTokenBreachCount(count, zeroTokenMonitorThreshold+0.001, 0); got != count {
		t.Fatalf("expected empty windows to preserve unresolved state, got %d", got)
	}
	if got := NextZeroTokenBreachCount(count, zeroTokenMonitorThreshold+0.5, zeroTokenMonitorMinSamples-1); got != count {
		t.Fatalf("expected low-sample windows to preserve unresolved state, got %d", got)
	}
}

func TestRelayZeroTokenOneShotUsesWindowEvaluation(t *testing.T) {
	task := NewRelayZeroTokenMonitorOneShotTask()
	if !task.oneShot {
		t.Fatal("expected one-shot monitor to evaluate complete minute windows")
	}
}

func TestTruncateZeroTokenErrorPreservesUTF8(t *testing.T) {
	got := truncateZeroTokenError(strings.Repeat("错", 181), 180)
	if !utf8.ValidString(got) || got != strings.Repeat("错", 180)+"…" {
		t.Fatalf("unexpected UTF-8-safe truncation: %q", got)
	}
}
