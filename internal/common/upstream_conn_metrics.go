package common

import (
	"crypto/tls"
	"net/http/httptrace"
	"sync/atomic"
	"time"
)

// Upstream connections the relay client used, split by whether the transport
// reused an idle one. A low reuse share means requests pay for TCP and TLS
// setup. Counted on every attempt; an atomic add per request.
var (
	upstreamConnsReused atomic.Int64
	upstreamConnsNew    atomic.Int64
)

// UpstreamDurationBounds are the histogram bounds, in seconds, for upstream
// dial and TLS handshake times. They reach past the 15 s a dial timeout would
// be set to, so the tail shows whether that limit would cut real dials.
var UpstreamDurationBounds = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15, 30, 60}

// DurationHistogram counts observations per bound (cumulative when read) and
// their total, with atomics only.
type DurationHistogram struct {
	counts    [len(UpstreamDurationBounds) + 1]atomic.Int64
	sumMicros atomic.Int64
}

func (h *DurationHistogram) observe(d time.Duration) {
	seconds := d.Seconds()
	index := len(UpstreamDurationBounds)
	for i, bound := range UpstreamDurationBounds {
		if seconds <= bound {
			index = i
			break
		}
	}
	h.counts[index].Add(1)
	h.sumMicros.Add(d.Microseconds())
}

// Snapshot returns cumulative counts per bound plus +Inf last, and the sum in
// seconds.
func (h *DurationHistogram) Snapshot() (cumulative [len(UpstreamDurationBounds) + 1]int64, sumSeconds float64) {
	var running int64
	for i := range h.counts {
		running += h.counts[i].Load()
		cumulative[i] = running
	}
	return cumulative, float64(h.sumMicros.Load()) / 1e6
}

// Upstream dial and TLS handshake durations of newly opened connections.
var (
	UpstreamDialDurations         DurationHistogram
	UpstreamTLSHandshakeDurations DurationHistogram
)

func recordUpstreamConn(info httptrace.GotConnInfo) {
	if info.Reused {
		upstreamConnsReused.Add(1)
		return
	}
	upstreamConnsNew.Add(1)
}

// UpstreamConnCounts returns how many upstream connections were reused and
// how many were newly opened since the process started.
func UpstreamConnCounts() (reused, opened int64) {
	return upstreamConnsReused.Load(), upstreamConnsNew.Load()
}

// UpstreamClientTrace is the trace attached to each upstream attempt: it
// starts the provider interval once request headers are written, marks the
// first response byte, counts whether the connection was reused, and times
// the dial and TLS handshake of a new one.
func UpstreamClientTrace(timing *RelayTiming) *httptrace.ClientTrace {
	// Atomic: dual-stack dialing can run connect callbacks concurrently.
	var dialStarted, handshakeStarted atomic.Int64
	since := func(started *atomic.Int64) (time.Duration, bool) {
		start := started.Load()
		if start == 0 {
			return 0, false
		}
		return time.Since(time.Unix(0, start)), true
	}
	return &httptrace.ClientTrace{
		ConnectStart: func(string, string) { dialStarted.Store(time.Now().UnixNano()) },
		ConnectDone: func(_, _ string, err error) {
			if d, ok := since(&dialStarted); ok && err == nil {
				UpstreamDialDurations.observe(d)
			}
		},
		TLSHandshakeStart: func() { handshakeStarted.Store(time.Now().UnixNano()) },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if d, ok := since(&handshakeStarted); ok && err == nil {
				UpstreamTLSHandshakeDurations.observe(d)
			}
		},
		GotConn:              recordUpstreamConn,
		WroteHeaders:         timing.StartUpstream,
		GotFirstResponseByte: timing.FirstUpstreamByte,
	}
}
