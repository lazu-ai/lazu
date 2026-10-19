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
