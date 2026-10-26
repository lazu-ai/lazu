package model

import (
	"context"
	"testing"
	"time"
)

// The health refresh runs in a goroutine a request spawned, so it can still be
// going when the connection it started with is replaced — during shutdown, or
// between two tests. Reading the package handle twice, once to decide the
// table exists and once to run the query, let it pass the check and then
// dereference nil. That interleaving is closed by construction now: each query
// takes the handle once and uses that one value throughout, so no test can
// reproduce it. What this test pins is the weaker property that protects the
// same code path — a query that starts with no connection answers empty
// instead of reaching for one.
func TestChannelHealthQueriesSurviveAConnectionThatWentAway(t *testing.T) {
	original := coreDB
	coreDB = nil
	t.Cleanup(func() { coreDB = original })

	since := time.Now().Add(-time.Hour)
	if rows, err := QueryChannelHealthEvidence(since); err != nil || len(rows) != 0 {
		t.Fatalf("evidence: %v %d", err, len(rows))
	}
	if rows, err := QueryChannelHealthPercentiles(since); err != nil || len(rows) != 0 {
		t.Fatalf("percentiles: %v %d", err, len(rows))
	}
	if rows, err := QueryChannelHealthChannelPercentiles(since); err != nil || len(rows) != 0 {
		t.Fatalf("channel percentiles: %v %d", err, len(rows))
	}
	if rows, err := QueryChannelHealthLastProbesContext(context.Background(), since); err != nil || len(rows) != 0 {
		t.Fatalf("last probes: %v %d", err, len(rows))
	}
	if rows, err := QueryChannelHealthLastOutcomesContext(context.Background(), since); err != nil || len(rows) != 0 {
		t.Fatalf("last outcomes: %v %d", err, len(rows))
	}
	if rows, err := QueryChannelHealthBuckets(since, 3600, 24); err != nil || len(rows) != 0 {
		t.Fatalf("buckets: %v %d", err, len(rows))
	}
	if rows, err := LoadChannelHealthGrid(); err != nil || len(rows) != 0 {
		t.Fatalf("grid: %v %d", err, len(rows))
	}
	if source := ChannelHealthOutcomeSourceSQL(); source == "" {
		t.Fatal("outcome source went empty")
	}
}
