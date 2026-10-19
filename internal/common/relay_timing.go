package common

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const relayTimingKey = "gateway_relay_timing"
const relayTimingOuterKey = "gateway_relay_timing_outer"
const relayTimingTrackedKey = "gateway_relay_timing_tracked"

const (
	RelayPhaseBilling          = "gateway_billing_ms"
	RelayPhaseConsumeLog       = "gateway_consume_log_ms"
	RelayPhaseSettleEncode     = "gateway_settle_encode_ms"
	RelayPhaseSettle           = "gateway_settle_ms"
	RelayPhaseSettleEval       = "gateway_settle_eval_ms"
	RelayPhaseCapture          = "gateway_capture_ms"
	RelayPhaseRoutingFinish    = "gateway_routing_finish_ms"
	RelayPhaseMiddlewareFinish = "gateway_middleware_finish_ms"
)

// RelayTiming separates preparation, provider TTFB and local work after the
// final downstream byte. It is independent of stream event/answer TTFT.
type RelayTiming struct {
	mu                                             sync.Mutex
	started, outbound, firstByte, lastWrite, ended time.Time
	phases                                         map[string]time.Duration
	handlerEnded                                   time.Time
}

type RelayPhaseTimer struct {
	timing  *RelayTiming
	name    string
	started time.Time
}

// Detailed phases are optional diagnostics. Required pre/provider/post clocks
// remain independent, and no phase waits for its asynchronous diagnostic writer.
func StartRelayPhase(c *gin.Context, name string) RelayPhaseTimer {
	if !HotPathMetricsEnabled || c == nil {
		return RelayPhaseTimer{}
	}
	if timing := RelayTimingFromContext(c); timing != nil {
		return RelayPhaseTimer{timing: timing, name: name, started: time.Now()}
	}
	return RelayPhaseTimer{}
}

func (t RelayPhaseTimer) Finish() {
	if t.timing != nil {
		t.timing.AddPhase(t.name, time.Since(t.started))
	}
}

func (t *RelayTiming) AddPhase(name string, duration time.Duration) {
	if duration < 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.phases == nil {
		t.phases = make(map[string]time.Duration)
	}
	t.phases[name] += duration
}

func EnsureRelayTiming(c *gin.Context) *RelayTiming {
	if timing := RelayTimingFromContext(c); timing != nil {
		return timing
	}
	timing := &RelayTiming{started: time.Now()}
	c.Set(relayTimingKey, timing)
	return timing
}

func RelayTimingFromContext(c *gin.Context) *RelayTiming {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(relayTimingKey); ok {
		if timing, ok := value.(*RelayTiming); ok {
			return timing
		}
	}
	return nil
}
func (t *RelayTiming) StartUpstream() {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Retrying a provider must not charge prior provider waits to gateway
	// preparation. These fields describe the first attempted upstream boundary.
	if t.outbound.IsZero() {
		t.outbound = time.Now()
	}
}
func (t *RelayTiming) FirstUpstreamByte() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.firstByte.IsZero() {
		t.firstByte = time.Now()
	}
}
func (t *RelayTiming) End()         { t.mu.Lock(); t.ended = time.Now(); t.mu.Unlock() }
func (t *RelayTiming) handlerDone() { t.mu.Lock(); t.handlerEnded = time.Now(); t.mu.Unlock() }
func (t *RelayTiming) Fields() map[string]interface{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	fields := make(map[string]interface{}, 3+len(t.phases))
	for name, duration := range t.phases {
		fields[name] = float64(duration) / float64(time.Millisecond)
	}
	if HotPathMetricsEnabled && !t.handlerEnded.IsZero() && !t.ended.IsZero() {
		fields[RelayPhaseMiddlewareFinish] = float64(t.ended.Sub(t.handlerEnded)) / float64(time.Millisecond)
	}
	if !t.outbound.IsZero() {
		fields["gateway_pre_ms"] = float64(t.outbound.Sub(t.started)) / float64(time.Millisecond)
	}
	if !t.firstByte.IsZero() {
		fields["upstream_ttfb_ms"] = float64(t.firstByte.Sub(t.outbound)) / float64(time.Millisecond)
	}
	if !t.lastWrite.IsZero() && !t.ended.IsZero() {
		fields["gateway_post_ms"] = float64(t.ended.Sub(t.lastWrite)) / float64(time.Millisecond)
	}
	return fields
}

// TrackRelayTiming starts before authentication/body parsing. finish runs after
// all downstream middleware and handlers return, so gateway_post_ms includes
// synchronous settlement and every deferred hook.
func TrackRelayTiming(finish func(*gin.Context, map[string]interface{})) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(relayTimingTrackedKey, true)
		c.Request = c.Request.WithContext(WithRequestRedisMetrics(c.Request.Context()))
		timing := EnsureRelayTiming(c)
		c.Writer = &relayTimingWriter{ResponseWriter: c.Writer, timing: timing}
		c.Next()
		if c.GetBool(relayTimingOuterKey) {
			timing.handlerDone()
			return
		}
		timing.End()
		if finish != nil {
			finish(c, timing.Fields())
		}
	}
}

// FinishRelayTimingAfterMiddleware wraps the access logger and other outer
// middleware. Only the relay router creates a clock, so control-plane requests
// never publish relay diagnostics. Finalization happens once, after logging.
func FinishRelayTimingAfterMiddleware(finish func(*gin.Context, map[string]interface{})) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(relayTimingOuterKey, true)
		c.Next()
		// Standalone internal requests/log helpers can create a clock without
		// entering the relay router. They do not create relay diagnostics here.
		if !c.GetBool(relayTimingTrackedKey) {
			return
		}
		if value, ok := c.Get(relayTimingKey); ok {
			if timing, ok := value.(*RelayTiming); ok {
				timing.End()
				if finish != nil {
					finish(c, timing.Fields())
				}
			}
		}
	}
}

type relayTimingWriter struct {
	gin.ResponseWriter
	timing *RelayTiming
}

func (w *relayTimingWriter) wrote(n int) {
	if n > 0 {
		w.timing.mu.Lock()
		w.timing.lastWrite = time.Now()
		w.timing.mu.Unlock()
	}
}
func (w *relayTimingWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.wrote(n)
	return n, err
}
func (w *relayTimingWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	w.wrote(n)
	return n, err
}
