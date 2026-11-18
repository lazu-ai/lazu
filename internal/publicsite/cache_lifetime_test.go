package publicsite

import (
	"strings"
	"testing"
)

func TestCacheWriteLifetimesAreLabeled(t *testing.T) {
	short, long := 2.5, 5.0
	lane := Lane{Key: "stable", Pricing: &Pricing{CacheWrite30m: &short, CacheWrite12h: &long}}
	view := buildLane(lane, Model{}, "en")
	if !strings.Contains(view.CacheWrite, "$2.50 (30m)") || !strings.Contains(view.CacheWrite, "$5.00 (12h)") || strings.Contains(view.CacheWrite, "(5m)") {
		t.Fatalf("wrong lifetime label: %q", view.CacheWrite)
	}
	row := tierRow(&ContextTier{Threshold: 272000, CacheWrite30m: &long, CacheWrite12h: &long}, ListPrice{}, nil, nil, false, "en")
	if !strings.Contains(row.CacheWrite, "(30m)") || !strings.Contains(row.CacheWrite, "(12h)") {
		t.Fatalf("tier lost lifetimes: %+v", row)
	}
}
