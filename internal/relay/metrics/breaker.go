package metrics

import "time"

// Circuit-breaker constants. Numbers are calibrated against the LiteLLM
// defaults (allowed_fails=3/min, cooldown_time=30s) but expressed as a
// ratio over a fixed window so they don't drift with traffic volume.
//
//   - We look at the most recent BreakerWindowSize samples in the error
//     window. The window must be full before we'll trip — otherwise a
//     channel that has only ever served one request that happened to 5xx
//     would be cooled down forever.
//   - At ≥ BreakerFailThreshold failures inside that window we trip
//     (50% error rate over 20 samples).
//   - Cooldown is BreakerCooldown wall clock; afterwards the breaker
//     transitions to HalfOpen and the next post-call event resolves to
//     either Closed (success → clear residual error bits, full recovery)
//     or back to Open (still failing → another cooldown).
const (
	BreakerWindowSize    = 20
	BreakerFailThreshold = 10
	BreakerCooldown      = 30 * time.Second
)

// BreakerState is the public state of a channel's circuit breaker, surfaced
// on Snapshot. Routers read state.Allows() to decide whether the channel
// is eligible at all.
type BreakerState int

const (
	// Closed — channel is healthy; route as normal.
	Closed BreakerState = iota
	// Open — channel is in active cooldown; do not route to it.
	Open
	// HalfOpen — cooldown elapsed; channel is a "probe candidate".
	// Routers MAY pick it, but the per-channel MaxParallel limit
	// (typically 1 in this state) keeps the probe load minimal until
	// the next outcome flips it back to Closed or Open.
	HalfOpen
)

// Allows reports whether routing is permitted to select a channel in this
// state. Open is the only blocking state — HalfOpen explicitly permits
// probe traffic.
func (s BreakerState) Allows() bool { return s != Open }

func (s BreakerState) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// breakerInputs is the pure-data view of one channelState that the
// breaker evaluator operates on. Pulling these fields out of channelState
// lets evaluateBreaker remain a pure function — easy to unit-test, no
// mutex juggling.
type breakerInputs struct {
	now             time.Time
	cooldownUntil   time.Time // zero = never cooled down
	errorBits       uint64    // most-recent in LSB
	errorWindowSize int       // ≤ errorWindowCap
	lastWasOK       bool      // was the just-completed call an OK?
	lastWasFault    bool      // …a channel-fault / rate-limited?
}

// breakerDecision is the result of evaluating a breakerInputs. The store
// reads ClearCooldown / SetCooldown to mutate channelState; ClearErrors
// is honoured on the HalfOpen → Closed edge to give a recovered channel
// a clean slate rather than re-tripping on residual fail bits.
type breakerDecision struct {
	NewState      BreakerState
	SetCooldown   bool
	CooldownUntil time.Time
	ClearCooldown bool
	ClearErrors   bool
}

// deriveBreakerState reports the live state for read-only callers (router
// snapshot path). Pure function of (now, cooldownUntil).
//
//   - cooldownUntil zero       → never tripped → Closed
//   - cooldownUntil  >  now    → still in cooldown → Open
//   - cooldownUntil  ≤  now    → cooldown elapsed but breaker not yet
//     re-resolved by a probe outcome → HalfOpen
func deriveBreakerState(now, cooldownUntil time.Time) BreakerState {
	if cooldownUntil.IsZero() {
		return Closed
	}
	if cooldownUntil.After(now) {
		return Open
	}
	return HalfOpen
}

// evaluateBreaker decides what the breaker should do after the most
// recent OnPostCall. Pure function — no I/O, no locks.
//
// Decision tree:
//
//  1. Channel is currently in cooldown → keep Open. (We don't expect
//     post-call events while in cooldown because the router should have
//     filtered the channel out, but if one slips through — e.g. a
//     token-pinned-channel request that bypasses routing — we ignore it
//     for breaker purposes.)
//
//  2. Half-open and the post-call was OK → recovery. Clear cooldown,
//     clear error window so residual fail bits don't immediately re-trip.
//
//  3. Half-open and the post-call was a channel fault → re-trip with a
//     fresh cooldown. No special "successive failures" multiplier — we
//     keep it simple at v1.
//
//  4. Closed and ≥ BreakerFailThreshold faults in the last
//     BreakerWindowSize samples → trip. Requires the window to be full
//     so a single early fault on a new channel doesn't strand it.
//
//  5. Otherwise → stay Closed.
func evaluateBreaker(in breakerInputs) breakerDecision {
	currentState := deriveBreakerState(in.now, in.cooldownUntil)

	switch currentState {
	case Open:
		return breakerDecision{NewState: Open}

	case HalfOpen:
		if in.lastWasOK {
			return breakerDecision{
				NewState:      Closed,
				ClearCooldown: true,
				ClearErrors:   true,
			}
		}
		if in.lastWasFault {
			return breakerDecision{
				NewState:      Open,
				SetCooldown:   true,
				CooldownUntil: in.now.Add(BreakerCooldown),
			}
		}
		// Half-open but the post-call didn't update the health window
		// (e.g. it was a content-policy rejection that we explicitly
		// don't count). Keep the half-open posture — the next real
		// outcome will resolve us.
		return breakerDecision{NewState: HalfOpen}

	default: // Closed
		if in.errorWindowSize >= BreakerWindowSize {
			fails := countFailsInWindow(in.errorBits, BreakerWindowSize)
			if fails >= BreakerFailThreshold {
				return breakerDecision{
					NewState:      Open,
					SetCooldown:   true,
					CooldownUntil: in.now.Add(BreakerCooldown),
				}
			}
		}
		return breakerDecision{NewState: Closed}
	}
}

// countFailsInWindow returns the number of 1-bits among the LSB-most
// `windowSize` bits of bits. Used to compute the rolling error rate
// against the breaker threshold.
func countFailsInWindow(bits uint64, windowSize int) int {
	if windowSize <= 0 {
		return 0
	}
	if windowSize > 64 {
		windowSize = 64
	}
	mask := uint64(1)<<uint(windowSize) - 1
	if windowSize == 64 {
		mask = ^uint64(0)
	}
	return popcount64(bits & mask)
}
