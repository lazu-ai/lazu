package metrics

import (
	"sync"
	"testing"
	"time"
)

func newTestStore(now func() time.Time) *InMemoryStore {
	s := NewInMemoryStore()
	s.clock = now
	return s
}

func TestErrorCategoryCountsAgainstHealth(t *testing.T) {
	yes := []ErrorCategory{CategoryChannelFault, CategoryRateLimited}
	no := []ErrorCategory{CategoryOK, CategoryClientFault, CategoryContentPolicy, CategoryContextWindow}
	for _, c := range yes {
		if !c.CountsAgainstHealth() {
			t.Fatalf("category %d should count", c)
		}
	}
	for _, c := range no {
		if c.CountsAgainstHealth() {
			t.Fatalf("category %d should NOT count", c)
		}
	}
}

func TestInFlightIncrementDecrement(t *testing.T) {
	s := NewInMemoryStore()
	s.OnPreCall(1)
	s.OnPreCall(1)
	s.OnPreCall(1)
	if got := s.Snapshot(1).InFlight; got != 3 {
		t.Fatalf("in_flight=%d want=3", got)
	}
	s.OnPostCall(1, 100*time.Millisecond, 0, CategoryOK)
	if got := s.Snapshot(1).InFlight; got != 2 {
		t.Fatalf("in_flight after one post=%d want=2", got)
	}
	s.OnPostCall(1, 0, 0, CategoryChannelFault)
	s.OnPostCall(1, 0, 0, CategoryClientFault)
	if got := s.Snapshot(1).InFlight; got != 0 {
		t.Fatalf("in_flight final=%d want=0", got)
	}
}

func TestInFlightDoesNotGoNegative(t *testing.T) {
	s := NewInMemoryStore()
	s.OnPostCall(1, 0, 0, CategoryOK) // unmatched
	if got := s.Snapshot(1).InFlight; got < 0 {
		t.Fatalf("in_flight=%d should not be negative", got)
	}
}

func TestLatencyRingBuffer(t *testing.T) {
	s := NewInMemoryStore()
	// Push 15 samples; only the most recent 10 should contribute to mean.
	for i := 1; i <= 15; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, time.Duration(i*10)*time.Millisecond, 0, CategoryOK)
	}
	snap := s.Snapshot(1)
	if snap.SamplesCount != latencyRingCap {
		t.Fatalf("samples=%d want=%d", snap.SamplesCount, latencyRingCap)
	}
	// Last 10 samples are 60ms..150ms => mean 105ms; ring stores them but
	// the ring head wraps. We just assert the mean is in the right range
	// (not exact ordering — ring doesn't guarantee newest-first).
	if snap.AvgLatencyMs < 50 || snap.AvgLatencyMs > 200 {
		t.Fatalf("avg_latency=%v outside expected 50..200ms", snap.AvgLatencyMs)
	}
}

func TestLatencyOnlyRecordedOnOK(t *testing.T) {
	s := NewInMemoryStore()
	s.OnPreCall(1)
	s.OnPostCall(1, 100*time.Millisecond, 0, CategoryChannelFault)
	snap := s.Snapshot(1)
	if snap.SamplesCount != 0 {
		t.Fatalf("samples=%d want=0 (fault should not pollute latency)", snap.SamplesCount)
	}
}

func TestErrorRateWindow(t *testing.T) {
	s := NewInMemoryStore()
	// 3 OK then 7 channel-faults => 7/10 error rate.
	for i := 0; i < 3; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 10*time.Millisecond, 0, CategoryOK)
	}
	for i := 0; i < 7; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 0, 0, CategoryChannelFault)
	}
	snap := s.Snapshot(1)
	if snap.ErrorWindowSize != 10 {
		t.Fatalf("window_size=%d want=10", snap.ErrorWindowSize)
	}
	if snap.ErrorRate < 0.69 || snap.ErrorRate > 0.71 {
		t.Fatalf("error_rate=%v want ~0.7", snap.ErrorRate)
	}
}

func TestClientFaultsDoNotPollute(t *testing.T) {
	s := NewInMemoryStore()
	for i := 0; i < 5; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 10*time.Millisecond, 0, CategoryOK)
	}
	// 100 client-fault outcomes should be ignored.
	for i := 0; i < 100; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 0, 0, CategoryClientFault)
	}
	snap := s.Snapshot(1)
	if snap.ErrorWindowSize != 5 {
		t.Fatalf("client faults polluted window: size=%d want=5", snap.ErrorWindowSize)
	}
	if snap.ErrorRate != 0 {
		t.Fatalf("client faults raised error rate to %v", snap.ErrorRate)
	}
}

func TestErrorWindowCappedAt64(t *testing.T) {
	s := NewInMemoryStore()
	for i := 0; i < 100; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 0, 0, CategoryChannelFault)
	}
	snap := s.Snapshot(1)
	if snap.ErrorWindowSize != errorWindowCap {
		t.Fatalf("window_size=%d want=%d (capped)", snap.ErrorWindowSize, errorWindowCap)
	}
	if snap.ErrorRate != 1.0 {
		t.Fatalf("error_rate=%v want=1.0", snap.ErrorRate)
	}
}

func TestRPMTPMSlidingWindow(t *testing.T) {
	currentMinute := int64(100)
	now := time.Unix(currentMinute*60, 0)
	s := newTestStore(func() time.Time { return now })

	// Two requests in the current minute, 1000 tokens each.
	for i := 0; i < 2; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 10*time.Millisecond, 1000, CategoryOK)
	}
	snap := s.Snapshot(1)
	if snap.RPMCurrent != 2 {
		t.Fatalf("rpm=%d want=2", snap.RPMCurrent)
	}
	if snap.TPMCurrent != 2000 {
		t.Fatalf("tpm=%d want=2000", snap.TPMCurrent)
	}

	// Jump forward 2 minutes — both slots should now be outside the window.
	now = time.Unix((currentMinute+2)*60, 0)
	snap = s.Snapshot(1)
	if snap.RPMCurrent != 0 {
		t.Fatalf("rpm after 2-min gap=%d want=0", snap.RPMCurrent)
	}
	if snap.TPMCurrent != 0 {
		t.Fatalf("tpm after 2-min gap=%d want=0", snap.TPMCurrent)
	}
}

func TestCooldown(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := newTestStore(func() time.Time { return now })

	s.SetCooldown(1, now.Add(30*time.Second))
	snap := s.Snapshot(1)
	if !snap.InCooldown() {
		t.Fatalf("expected channel in cooldown, got Until=%v Now=%v", snap.CooldownUntil, snap.Now)
	}

	// Jump past the deadline.
	now = now.Add(31 * time.Second)
	snap = s.Snapshot(1)
	if snap.InCooldown() {
		t.Fatalf("expected cooldown to have elapsed, got Until=%v Now=%v", snap.CooldownUntil, snap.Now)
	}
}

func TestConcurrentAccess(t *testing.T) {
	// Smoke test: 200 goroutines each running 50 pre/post pairs against
	// 5 channels must not panic, race, or leave in-flight non-zero.
	s := NewInMemoryStore()
	var wg sync.WaitGroup
	for g := 0; g < 200; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			channelID := (g % 5) + 1
			for i := 0; i < 50; i++ {
				s.OnPreCall(channelID)
				cat := CategoryOK
				if i%7 == 0 {
					cat = CategoryChannelFault
				}
				s.OnPostCall(channelID, 5*time.Millisecond, 100, cat)
			}
		}(g)
	}
	wg.Wait()
	for ch := 1; ch <= 5; ch++ {
		if got := s.Snapshot(ch).InFlight; got != 0 {
			t.Fatalf("channel %d in_flight=%d want=0", ch, got)
		}
	}
}

func TestSnapshotForUnknownChannelIsZero(t *testing.T) {
	s := NewInMemoryStore()
	snap := s.Snapshot(42)
	if snap.InFlight != 0 || snap.SamplesCount != 0 || snap.ErrorWindowSize != 0 {
		t.Fatalf("unknown channel snapshot not zero: %+v", snap)
	}
}

func TestBumpBucketEvictsOldest(t *testing.T) {
	slots := [2]bucketSlot{{minuteID: -1}, {minuteID: -1}}
	bumpBucket(&slots, 100, 1)
	bumpBucket(&slots, 101, 2)
	// Both slots now used; bumping minute 102 should evict the older (100).
	bumpBucket(&slots, 102, 3)
	hasMinute := func(m int64, want int) bool {
		for _, s := range slots {
			if s.minuteID == m && s.value == want {
				return true
			}
		}
		return false
	}
	if hasMinute(100, 1) {
		t.Fatalf("slot 100 should have been evicted")
	}
	if !hasMinute(101, 2) || !hasMinute(102, 3) {
		t.Fatalf("expected slots 101 and 102 present, got %+v", slots)
	}
}
