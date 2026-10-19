package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestArrivalPacerAbsoluteSlotsHaveNoRoundingDrift(t *testing.T) {
	start := time.Unix(0, 0)
	for _, rate := range []int{3, 500, 1000, 2000, 10000} {
		p := newArrivalPacer(start, 60*time.Second, rate)
		if p.offered != int64(rate)*60 {
			t.Fatalf("rate %d offered %d", rate, p.offered)
		}
		for i := int64(0); i < p.offered; i++ {
			due, ok := p.claim()
			if !ok || due.Sub(start) != arrivalOffset(i, int64(rate)) || !due.Before(p.deadline) {
				t.Fatalf("lost or invalid arrival %d at rate %d", i, rate)
			}
		}
		if _, ok := p.claim(); ok {
			t.Fatal("arrival after load window")
		}
	}
	if arrivalOffset(30000, 500) != 60*time.Second || newArrivalPacer(start, 1500*time.Millisecond, 3).offered != 5 {
		t.Fatal("fractional arrival schedule drift")
	}
}

func TestArrivalPacerClaimsAreUniqueAcrossWorkers(t *testing.T) {
	p := newArrivalPacer(time.Unix(0, 0), time.Second, 500)
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[time.Time]bool{}
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				due, ok := p.claim()
				if !ok {
					return
				}
				mu.Lock()
				if seen[due] {
					t.Error("arrival dispatched twice")
				}
				seen[due] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 500 {
		t.Fatalf("lost arrival slots: %d", len(seen))
	}
}

func TestAwaitArrivalClockDelayDeadlineAndCancellation(t *testing.T) {
	start := time.Unix(0, 0)
	deadline := start.Add(time.Second)
	for _, tc := range []struct {
		name           string
		now, due, wake time.Duration
		cancel         bool
		want           bool
	}{
		{"future", 0, 100 * time.Millisecond, 100 * time.Millisecond, false, true},
		{"late before deadline", 200 * time.Millisecond, 100 * time.Millisecond, 0, false, true},
		{"wake after deadline", 0, 100 * time.Millisecond, time.Second, false, false},
		{"at deadline", time.Second, 0, 0, false, false},
		{"due at deadline", 0, time.Second, 0, false, false},
		{"cancel while waiting", 0, 100 * time.Millisecond, 100 * time.Millisecond, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			current := start.Add(tc.now)
			ok := awaitArrival(ctx, start.Add(tc.due), deadline, func() time.Time { return current }, func(_ context.Context, delay time.Duration) bool {
				if delay != tc.due-tc.now {
					t.Fatalf("unexpected wait %s", delay)
				}
				current = start.Add(tc.wake)
				if tc.cancel {
					cancel()
				}
				return !tc.cancel
			})
			if ok != tc.want {
				t.Fatalf("await=%v want=%v", ok, tc.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitUntil(ctx, time.Hour) {
		t.Fatal("canceled wait accepted arrival")
	}
}
