package metrics

import (
	"testing"
	"time"
)

func TestDeriveBreakerState(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cases := []struct {
		name          string
		cooldownUntil time.Time
		want          BreakerState
	}{
		{"never_tripped_closed", time.Time{}, Closed},
		{"in_cooldown_open", now.Add(10 * time.Second), Open},
		{"cooldown_just_expired_half_open", now.Add(-1 * time.Second), HalfOpen},
		{"cooldown_long_expired_still_half_open", now.Add(-5 * time.Minute), HalfOpen},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveBreakerState(now, tc.cooldownUntil)
			if got != tc.want {
				t.Fatalf("deriveBreakerState=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestBreakerStateAllows(t *testing.T) {
	if !Closed.Allows() {
		t.Fatal("Closed must allow")
	}
	if !HalfOpen.Allows() {
		t.Fatal("HalfOpen must allow (probes)")
	}
	if Open.Allows() {
		t.Fatal("Open must NOT allow")
	}
}

func TestEvaluateBreakerTripsOnHighErrorRate(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	// Window full (20 samples), 11 of them are failures → trip.
	bits := uint64(0)
	for i := 0; i < 11; i++ {
		bits = (bits << 1) | 1
	}
	for i := 0; i < 9; i++ {
		bits = bits << 1 // push 0s for OK
	}
	d := evaluateBreaker(breakerInputs{
		now:             now,
		errorBits:       bits,
		errorWindowSize: 20,
		lastWasFault:    true,
	})
	if d.NewState != Open {
		t.Fatalf("state=%s want Open", d.NewState)
	}
	if !d.SetCooldown {
		t.Fatal("must SetCooldown")
	}
	wantUntil := now.Add(BreakerCooldown)
	if !d.CooldownUntil.Equal(wantUntil) {
		t.Fatalf("cooldown_until=%v want=%v", d.CooldownUntil, wantUntil)
	}
}

func TestEvaluateBreakerStaysClosedBelowThreshold(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	// 20 samples, 9 failures → under 50% → no trip.
	bits := uint64(0)
	for i := 0; i < 9; i++ {
		bits = (bits << 1) | 1
	}
	for i := 0; i < 11; i++ {
		bits = bits << 1
	}
	d := evaluateBreaker(breakerInputs{
		now:             now,
		errorBits:       bits,
		errorWindowSize: 20,
		lastWasFault:    true,
	})
	if d.NewState != Closed {
		t.Fatalf("state=%s want Closed", d.NewState)
	}
	if d.SetCooldown {
		t.Fatal("must NOT SetCooldown")
	}
}

func TestEvaluateBreakerNoTripUntilWindowFull(t *testing.T) {
	// Even 100% failure rate doesn't trip until window reaches
	// BreakerWindowSize — protects new channels from a single early fault.
	bits := uint64((1 << 5) - 1) // 5 consecutive failures
	d := evaluateBreaker(breakerInputs{
		now:             time.Unix(1_000_000, 0),
		errorBits:       bits,
		errorWindowSize: 5,
		lastWasFault:    true,
	})
	if d.NewState != Closed {
		t.Fatalf("state=%s want Closed (window not full)", d.NewState)
	}
}

func TestEvaluateBreakerOpenStaysOpen(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	d := evaluateBreaker(breakerInputs{
		now:           now,
		cooldownUntil: now.Add(15 * time.Second), // still in cooldown
		errorBits:     0xFF,
		lastWasFault:  true,
	})
	if d.NewState != Open {
		t.Fatalf("state=%s want Open (in cooldown)", d.NewState)
	}
	if d.SetCooldown {
		t.Fatal("in-cooldown post-call should not re-set cooldown")
	}
}

func TestEvaluateBreakerHalfOpenSuccessRecovers(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	d := evaluateBreaker(breakerInputs{
		now:           now,
		cooldownUntil: now.Add(-5 * time.Second), // cooldown elapsed
		errorBits:     0xFFFF,                    // lots of residual fail bits
		lastWasOK:     true,
	})
	if d.NewState != Closed {
		t.Fatalf("state=%s want Closed (probe success)", d.NewState)
	}
	if !d.ClearCooldown {
		t.Fatal("must clear cooldown on recovery")
	}
	if !d.ClearErrors {
		t.Fatal("must clear error window on recovery so residual fail bits don't immediately re-trip")
	}
}

func TestEvaluateBreakerHalfOpenFailureRetrips(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	d := evaluateBreaker(breakerInputs{
		now:           now,
		cooldownUntil: now.Add(-1 * time.Second),
		lastWasFault:  true,
	})
	if d.NewState != Open {
		t.Fatalf("state=%s want Open (probe failure)", d.NewState)
	}
	if !d.SetCooldown {
		t.Fatal("must re-trip cooldown on probe failure")
	}
}

func TestStoreOnPostCallTripsAfterRepeatedFailures(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := newTestStore(func() time.Time { return now })

	for i := 0; i < 20; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 0, 0, CategoryChannelFault)
	}
	snap := s.Snapshot(1)
	if snap.BreakerState != Open {
		t.Fatalf("breaker=%s want Open", snap.BreakerState)
	}
	if !snap.InCooldown() {
		t.Fatal("expected snapshot to report cooldown active")
	}
}

func TestStoreOnPostCallRecoveryAfterCooldown(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := newTestStore(func() time.Time { return now })

	for i := 0; i < 20; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 0, 0, CategoryChannelFault)
	}
	if s.Snapshot(1).BreakerState != Open {
		t.Fatal("expected tripped after 20 faults")
	}

	// Jump past cooldown — breaker goes HalfOpen.
	now = now.Add(BreakerCooldown + time.Second)
	if s.Snapshot(1).BreakerState != HalfOpen {
		t.Fatal("expected HalfOpen after cooldown elapses")
	}

	// Probe succeeds → recover.
	s.OnPreCall(1)
	s.OnPostCall(1, 50*time.Millisecond, 0, CategoryOK)
	snap := s.Snapshot(1)
	if snap.BreakerState != Closed {
		t.Fatalf("breaker=%s want Closed after probe success", snap.BreakerState)
	}
	if snap.ErrorWindowSize != 0 {
		t.Fatalf("error window not cleared on recovery: size=%d", snap.ErrorWindowSize)
	}
}

func TestStoreOnPostCallProbeFailureRetrips(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := newTestStore(func() time.Time { return now })

	for i := 0; i < 20; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 0, 0, CategoryChannelFault)
	}
	now = now.Add(BreakerCooldown + time.Second)
	if s.Snapshot(1).BreakerState != HalfOpen {
		t.Fatal("expected HalfOpen")
	}

	// Probe still fails → re-trip.
	s.OnPreCall(1)
	s.OnPostCall(1, 0, 0, CategoryChannelFault)
	snap := s.Snapshot(1)
	if snap.BreakerState != Open {
		t.Fatalf("breaker=%s want Open after probe failure", snap.BreakerState)
	}
	if !snap.CooldownUntil.After(now) {
		t.Fatal("cooldown_until should be reset to future")
	}
}

func TestStoreClientFaultsDoNotTrip(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := newTestStore(func() time.Time { return now })

	// 100 client faults (4xx non-429) should not trip — they're the
	// caller's fault, not the channel's.
	for i := 0; i < 100; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 0, 0, CategoryClientFault)
	}
	if s.Snapshot(1).BreakerState != Closed {
		t.Fatalf("breaker=%s want Closed (client faults must not trip)", s.Snapshot(1).BreakerState)
	}
}

func TestStoreContentPolicyDoesNotTrip(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := newTestStore(func() time.Time { return now })
	for i := 0; i < 50; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 0, 0, CategoryContentPolicy)
	}
	if s.Snapshot(1).BreakerState != Closed {
		t.Fatalf("breaker=%s want Closed (content_policy must not trip)", s.Snapshot(1).BreakerState)
	}
}

func TestStoreContextWindowDoesNotTrip(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := newTestStore(func() time.Time { return now })
	for i := 0; i < 50; i++ {
		s.OnPreCall(1)
		s.OnPostCall(1, 0, 0, CategoryContextWindow)
	}
	if s.Snapshot(1).BreakerState != Closed {
		t.Fatalf("breaker=%s want Closed (context_window must not trip)", s.Snapshot(1).BreakerState)
	}
}

func TestCountFailsInWindow(t *testing.T) {
	// 0b10110101 — 5 ones in the lowest 8 bits.
	bits := uint64(0b10110101)
	if got := countFailsInWindow(bits, 8); got != 5 {
		t.Fatalf("count=%d want=5", got)
	}
	// Truncated window: only the lowest 4 bits → 0b0101 → 2.
	if got := countFailsInWindow(bits, 4); got != 2 {
		t.Fatalf("count4=%d want=2", got)
	}
	if got := countFailsInWindow(bits, 0); got != 0 {
		t.Fatalf("count0=%d want=0", got)
	}
}
