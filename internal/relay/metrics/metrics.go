// Package metrics tracks per-channel runtime state used by the composite
// router (in-flight count, recent latency, recent error rate, RPM/TPM
// buckets). It is updated by pre-/post-call hooks on every relay request
// and read by the router during channel selection.
//
// Backends are pluggable. The default in-memory backend is sufficient for
// single-process deployments and zero-extra-deps. A Redis-backed backend
// can be added later for HA without touching call sites — they only depend
// on the Store interface.
//
// Design notes:
//
//   - In-flight is hot-path (read on every Select, written on every
//     pre/post call), so it uses atomic.Int64.
//   - Latency samples use a fixed-size ring buffer (cap 10) — same as
//     LiteLLM's lowest_latency strategy. Cheap, cache-friendly, no
//     percentile bookkeeping required.
//   - Error window is a 64-bit bitset (most recent on the LSB). 64 samples
//     is enough to compute a stable rolling error rate without keeping
//     timestamped history.
//   - TPM/RPM use a 2-slot sliding window (current minute + previous
//     minute) instead of a map; bounded memory regardless of channel
//     lifetime.
//   - Cooldown is just a unix-nano deadline; the circuit breaker (added in
//     a follow-up commit) decides when to set/clear it.
package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// ErrorCategory classifies how a request's outcome should be interpreted
// for channel-health bookkeeping. Categories that indicate the channel
// itself is unhealthy (5xx / timeout / 429) count toward the error window;
// client-side errors (400/422/content-policy/context-window) do NOT —
// blaming the channel for malformed input would falsely cool it down. This
// mirrors Kong's "client errors don't trigger failover" rule.
type ErrorCategory int

const (
	// CategoryOK — request succeeded.
	CategoryOK ErrorCategory = iota
	// CategoryChannelFault — 5xx / network / timeout / connection error.
	// Counted as a failure against the channel.
	CategoryChannelFault
	// CategoryRateLimited — 429 / quota exhausted upstream. Counted as a
	// failure (channel is unable to serve right now).
	CategoryRateLimited
	// CategoryClientFault — 400/401/403/404/422 (non-429 4xx). NOT counted
	// — the request itself is malformed.
	CategoryClientFault
	// CategoryContentPolicy — content moderation rejection. NOT counted —
	// the prompt is the problem, fallback to a less-strict channel.
	CategoryContentPolicy
	// CategoryContextWindow — prompt exceeds channel context. NOT counted
	// — fallback to a larger-context channel.
	CategoryContextWindow
)

// CountsAgainstHealth reports whether this outcome should push a "fail"
// bit into the error window and potentially trip the circuit breaker.
func (c ErrorCategory) CountsAgainstHealth() bool {
	switch c {
	case CategoryChannelFault, CategoryRateLimited:
		return true
	default:
		return false
	}
}

// Snapshot is a point-in-time read of one channel's metrics, returned to
// the router. Computed on demand so the caller sees a consistent picture.
type Snapshot struct {
	InFlight        int64
	SamplesCount    int
	AvgLatencyMs    float64
	ErrorWindowSize int
	ErrorRate       float64
	RPMCurrent      int
	TPMCurrent      int
	CooldownUntil   time.Time
	Now             time.Time
	BreakerState    BreakerState
}

// InCooldown returns true when the channel should be skipped by routing.
// We compare against Snapshot.Now (not time.Now()) so a batched router
// pass sees a consistent view.
func (s Snapshot) InCooldown() bool {
	return s.Now.Before(s.CooldownUntil)
}

// Store is the contract the router and the hooks both depend on. All
// methods must be safe for concurrent use across goroutines.
type Store interface {
	OnPreCall(channelID int)
	OnPostCall(channelID int, latency time.Duration, tokens int, cat ErrorCategory)
	Snapshot(channelID int) Snapshot
	SetCooldown(channelID int, until time.Time)
}

// Default is the package-level store used by the relay pipeline. Test code
// can swap it out by assignment.
var Default Store = NewInMemoryStore()

// =====================================================================
// In-memory backend.
// =====================================================================

const (
	latencyRingCap = 10 // matches LiteLLM lowest_latency.max_latency_list_size
	errorWindowCap = 64
)

type channelState struct {
	inFlight      atomic.Int64
	cooldownUntil atomic.Int64 // unix nano; 0 means "no cooldown"

	mu           sync.Mutex
	latencyMs    [latencyRingCap]float64
	latencyHead  int
	latencyCount int
	errorBits    uint64 // most recent in LSB
	errorCount   int    // ≤ errorWindowCap

	// Two-slot sliding window: each slot holds (minute_id, count).
	// We compare slot[i].minuteID to the current minute to decide whether
	// the slot is still in the rolling window. minuteID = -1 means empty.
	rpmSlots [2]bucketSlot
	tpmSlots [2]bucketSlot
}

type bucketSlot struct {
	minuteID int64
	value    int
}

// InMemoryStore is a single-process Store. Channels are allocated lazily
// on first reference and never reclaimed (channel count is bounded by ops
// configuration, so this is fine).
type InMemoryStore struct {
	mu       sync.RWMutex
	channels map[int]*channelState
	clock    func() time.Time // injectable for tests
}

// NewInMemoryStore builds a Store backed by process-local maps.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		channels: make(map[int]*channelState),
		clock:    time.Now,
	}
}

func (s *InMemoryStore) get(channelID int) *channelState {
	s.mu.RLock()
	st, ok := s.channels[channelID]
	s.mu.RUnlock()
	if ok {
		return st
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok = s.channels[channelID]; ok {
		return st
	}
	st = newChannelState()
	s.channels[channelID] = st
	return st
}

func newChannelState() *channelState {
	st := &channelState{}
	st.rpmSlots[0].minuteID = -1
	st.rpmSlots[1].minuteID = -1
	st.tpmSlots[0].minuteID = -1
	st.tpmSlots[1].minuteID = -1
	return st
}

// OnPreCall is invoked the moment a channel is selected, *before* the
// upstream HTTP request fires. It does two things:
//   - increments the in-flight counter
//   - increments the current-minute RPM bucket
func (s *InMemoryStore) OnPreCall(channelID int) {
	if channelID <= 0 {
		return
	}
	st := s.get(channelID)
	st.inFlight.Add(1)
	st.mu.Lock()
	defer st.mu.Unlock()
	minute := s.clock().Unix() / 60
	bumpBucket(&st.rpmSlots, minute, 1)
}

// OnPostCall is invoked after the upstream call returns (or fails). It
// pairs with OnPreCall so in-flight always decrements, regardless of
// outcome. Latency and tokens are recorded only on success-or-channel-fault
// — client-fault outcomes don't tell us anything about the channel's perf.
//
// The circuit breaker is evaluated here too: a single post-call event is
// the only place that has all the data needed to decide whether to trip,
// clear, or re-trip the breaker.
func (s *InMemoryStore) OnPostCall(channelID int, latency time.Duration, tokens int, cat ErrorCategory) {
	if channelID <= 0 {
		return
	}
	st := s.get(channelID)
	if st.inFlight.Load() > 0 {
		st.inFlight.Add(-1)
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if cat == CategoryOK && latency > 0 {
		ms := float64(latency.Milliseconds())
		st.latencyMs[st.latencyHead] = ms
		st.latencyHead = (st.latencyHead + 1) % latencyRingCap
		if st.latencyCount < latencyRingCap {
			st.latencyCount++
		}
	}

	// Push a bit into the error window for any outcome we are willing
	// to attribute to the channel (OK or channel-fault). Client-fault
	// outcomes are skipped so they don't bias the rolling error rate.
	healthCountsThisCall := cat == CategoryOK || cat.CountsAgainstHealth()
	if healthCountsThisCall {
		bit := uint64(0)
		if cat.CountsAgainstHealth() {
			bit = 1
		}
		st.errorBits = (st.errorBits << 1) | bit
		if st.errorCount < errorWindowCap {
			st.errorCount++
		}
	}

	if tokens > 0 && cat == CategoryOK {
		minute := s.clock().Unix() / 60
		bumpBucket(&st.tpmSlots, minute, tokens)
	}

	// Evaluate the circuit breaker. Pure function — inputs assembled
	// here from the just-mutated state, decision applied below. Skipped
	// for outcomes that didn't update the health window (e.g.
	// content-policy / context-window rejections); they don't tell us
	// anything about the channel's reliability.
	if !healthCountsThisCall {
		return
	}
	now := s.clock()
	var cooldown time.Time
	if cd := st.cooldownUntil.Load(); cd > 0 {
		cooldown = time.Unix(0, cd)
	}
	decision := evaluateBreaker(breakerInputs{
		now:             now,
		cooldownUntil:   cooldown,
		errorBits:       st.errorBits,
		errorWindowSize: st.errorCount,
		lastWasOK:       cat == CategoryOK,
		lastWasFault:    cat.CountsAgainstHealth(),
	})
	if decision.ClearErrors {
		st.errorBits = 0
		st.errorCount = 0
	}
	if decision.ClearCooldown {
		st.cooldownUntil.Store(0)
	}
	if decision.SetCooldown {
		st.cooldownUntil.Store(decision.CooldownUntil.UnixNano())
	}
}

// SetCooldown puts the channel out of rotation until the given time. Used
// by the circuit breaker; safe to call repeatedly (latest wins).
func (s *InMemoryStore) SetCooldown(channelID int, until time.Time) {
	if channelID <= 0 {
		return
	}
	st := s.get(channelID)
	st.cooldownUntil.Store(until.UnixNano())
}

// Snapshot returns a consistent point-in-time view used by the router.
func (s *InMemoryStore) Snapshot(channelID int) Snapshot {
	now := s.clock()
	if channelID <= 0 {
		return Snapshot{Now: now}
	}
	st := s.get(channelID)
	snap := Snapshot{
		InFlight: st.inFlight.Load(),
		Now:      now,
	}
	if cd := st.cooldownUntil.Load(); cd > 0 {
		snap.CooldownUntil = time.Unix(0, cd)
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if st.latencyCount > 0 {
		sum := 0.0
		for i := 0; i < st.latencyCount; i++ {
			sum += st.latencyMs[i]
		}
		snap.SamplesCount = st.latencyCount
		snap.AvgLatencyMs = sum / float64(st.latencyCount)
	}

	if st.errorCount > 0 {
		mask := uint64(1)<<uint(st.errorCount) - 1
		bits := st.errorBits & mask
		fails := popcount64(bits)
		snap.ErrorWindowSize = st.errorCount
		snap.ErrorRate = float64(fails) / float64(st.errorCount)
	}

	currentMinute := now.Unix() / 60
	snap.RPMCurrent = sumActiveBuckets(&st.rpmSlots, currentMinute)
	snap.TPMCurrent = sumActiveBuckets(&st.tpmSlots, currentMinute)
	snap.BreakerState = deriveBreakerState(now, snap.CooldownUntil)

	return snap
}

// =====================================================================
// helpers
// =====================================================================

// bumpBucket records `delta` into the slot for `minute`. If neither slot
// matches and one is older than (minute-1), we evict it. Caller must hold
// st.mu.
func bumpBucket(slots *[2]bucketSlot, minute int64, delta int) {
	for i := range slots {
		if slots[i].minuteID == minute {
			slots[i].value += delta
			return
		}
	}
	// No slot for this minute; evict the older one (if any) and reuse it.
	evictIdx := 0
	if slots[0].minuteID == -1 {
		evictIdx = 0
	} else if slots[1].minuteID == -1 {
		evictIdx = 1
	} else if slots[0].minuteID < slots[1].minuteID {
		evictIdx = 0
	} else {
		evictIdx = 1
	}
	slots[evictIdx].minuteID = minute
	slots[evictIdx].value = delta
}

// sumActiveBuckets sums the slots whose minute is current-or-previous. We
// don't sum slots older than that — they are "stale" and effectively
// expired.
func sumActiveBuckets(slots *[2]bucketSlot, currentMinute int64) int {
	total := 0
	for _, s := range slots {
		if s.minuteID == currentMinute || s.minuteID == currentMinute-1 {
			total += s.value
		}
	}
	return total
}

func popcount64(x uint64) int {
	count := 0
	for x != 0 {
		x &= x - 1
		count++
	}
	return count
}
