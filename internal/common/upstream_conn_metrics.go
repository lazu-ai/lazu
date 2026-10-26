package common

import (
	"net/http/httptrace"
	"sync/atomic"
)

// Upstream connections the relay client used, split by whether the transport
// reused an idle one. A low reuse share means requests pay for TCP and TLS
// setup. Counted on every attempt; an atomic add per request.
var (
	upstreamConnsReused atomic.Int64
	upstreamConnsNew    atomic.Int64
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
// first response byte, and counts whether the connection was reused.
func UpstreamClientTrace(timing *RelayTiming) *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GotConn:              recordUpstreamConn,
		WroteHeaders:         timing.StartUpstream,
		GotFirstResponseByte: timing.FirstUpstreamByte,
	}
}
