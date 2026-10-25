package common

import (
	"errors"
	"testing"
	"time"
)

func TestHotPathMetricsDisabledByDefault(t *testing.T) {
	resetHotPathMetricsForTest()
	previous := HotPathMetricsEnabled
	HotPathMetricsEnabled = false
	defer func() { HotPathMetricsEnabled = previous }()

	ObserveHotPathStage(HotPathStageAuthTokenCache, time.Millisecond, false)
	IncrementHotPathCounter(HotPathCounterAuthTokenCacheHit)

	snapshot := SnapshotHotPathMetrics()
	if snapshot.Enabled {
		t.Fatalf("expected metrics to be disabled")
	}
	if len(snapshot.Stages) != 0 || len(snapshot.Counters) != 0 {
		t.Fatalf("expected disabled metrics to stay empty, got %#v", snapshot)
	}
}

func TestHotPathMetricsSnapshot(t *testing.T) {
	resetHotPathMetricsForTest()
	previous := HotPathMetricsEnabled
	HotPathMetricsEnabled = true
	defer func() { HotPathMetricsEnabled = previous }()

	ObserveHotPathStage(HotPathStageAuthTokenCache, 2*time.Millisecond, false)
	timer := StartHotPathStage(HotPathStageAuthTokenCache)
	timer.start = time.Now().Add(-3 * time.Millisecond)
	timer.Finish(errors.New("boom"))
	IncrementHotPathCounter(HotPathCounterAuthTokenCacheHit)
	IncrementHotPathCounter(HotPathCounterAuthTokenCacheHit)

	snapshot := SnapshotHotPathMetrics()
	stage := snapshot.Stages[HotPathStageAuthTokenCache]
	if stage.Count != 2 {
		t.Fatalf("expected 2 stage samples, got %d", stage.Count)
	}
	if stage.ErrorCount != 1 {
		t.Fatalf("expected 1 error sample, got %d", stage.ErrorCount)
	}
	if stage.TotalDurationUS <= 0 || stage.AvgDurationMS <= 0 {
		t.Fatalf("expected positive duration stats, got %#v", stage)
	}
	if snapshot.Counters[HotPathCounterAuthTokenCacheHit] != 2 {
		t.Fatalf("expected counter 2, got %#v", snapshot.Counters)
	}
}

func TestHotPathStageHistogramBuckets(t *testing.T) {
	resetHotPathMetricsForTest()
	previous := HotPathMetricsEnabled
	HotPathMetricsEnabled = true
	defer func() { HotPathMetricsEnabled = previous }()

	for _, duration := range []time.Duration{50 * time.Microsecond, 100 * time.Microsecond, 3 * time.Millisecond, 20 * time.Second} {
		ObserveHotPathStage(HotPathStageRoutingSelect, duration, false)
	}
	stats := SnapshotHotPathMetrics().Stages[HotPathStageRoutingSelect]
	at := func(boundUS int64) int64 {
		for i, bound := range HotPathStageBucketsUS {
			if bound == boundUS {
				return stats.Buckets[i]
			}
		}
		t.Fatalf("no bucket at %dus", boundUS)
		return 0
	}
	// Bounds are inclusive and cumulative; 20s lands only in +Inf.
	if at(100) != 2 || at(2_500) != 2 || at(5_000) != 3 || at(10_000_000) != 3 {
		t.Fatalf("unexpected buckets: %v", stats.Buckets)
	}
	if stats.HistogramCount != 4 || stats.Count != 4 {
		t.Fatalf("histogram count %d, count %d, want 4", stats.HistogramCount, stats.Count)
	}
}
