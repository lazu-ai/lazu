package common

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	HotPathCounterIdentitySnapshotEvicted = "auth.identity_l1.evicted"
	HotPathStageAuthAdmission             = "auth.admission"
	HotPathStageAuthTokenCache            = "auth.token_cache"
	HotPathStageAuthTokenDBFallback       = "auth.token_db_fallback" // secret-scan: allow -- metric label
	HotPathStageAuthUserCache             = "auth.user_cache"
	HotPathStageAuthUserDBFallback        = "auth.user_db_fallback"
	HotPathStageRateLimitPreflight        = "rate_limit.preflight"
	HotPathStageRateLimitPostSuccess      = "rate_limit.post_success"
	HotPathStageRoutingSelect             = "routing.select"
	HotPathStageUpstreamRelay             = "upstream.relay"
	HotPathStageSettlementPreconsume      = "settlement.preconsume"
	HotPathStageSettlementPost            = "settlement.post"
	HotPathStageRelayEventPublish         = "events.publish"
	HotPathStageProtocolParse             = "protocol.parse"
	HotPathStageOutboundBody              = "protocol.outbound_body"
	HotPathStageProtocolStreamEvent       = "protocol.stream_event"

	HotPathCounterAuthTokenCacheHit         = "auth.token_cache.hit"  // secret-scan: allow -- metric label
	HotPathCounterAuthTokenCacheMiss        = "auth.token_cache.miss" // secret-scan: allow -- metric label
	HotPathCounterAuthUserCacheHit          = "auth.user_cache.hit"
	HotPathCounterAuthUserCacheMiss         = "auth.user_cache.miss"
	HotPathCounterAuthTokenDBFallback       = "auth.token_db_fallback" // secret-scan: allow -- metric label
	HotPathCounterAuthUserDBFallback        = "auth.user_db_fallback"
	HotPathCounterRelayEventPublishFailed   = "events.publish.failed"
	HotPathCounterProtocolPlanNative        = "protocol.plan.native"
	HotPathCounterProtocolPlanBridge        = "protocol.plan.bridge"
	HotPathCounterProtocolUnsupported       = "protocol.unsupported"
	HotPathCounterProtocolConversionFailed  = "protocol.conversion.failed"
	HotPathCounterProtocolUnsupportedPrefix = "protocol.unsupported.feature."
	HotPathCounterProtocolDegradedPrefix    = "protocol.degraded."
)

var (
	HotPathMetricsEnabled bool

	hotPathMetricsStartedAt = time.Now().Unix()
	hotPathStageMetrics     sync.Map // map[string]*hotPathStageMetric
	hotPathCounters         sync.Map // map[string]*atomic.Int64
)

type hotPathStageMetric struct {
	count           atomic.Int64
	errorCount      atomic.Int64
	totalDurationUS atomic.Int64
	maxDurationUS   atomic.Int64
}

type HotPathStageTimer struct {
	name    string
	start   time.Time
	enabled bool
}

type HotPathStageStats struct {
	Count           int64   `json:"count"`
	ErrorCount      int64   `json:"error_count"`
	TotalDurationUS int64   `json:"total_duration_us"`
	MaxDurationUS   int64   `json:"max_duration_us"`
	AvgDurationMS   float64 `json:"avg_duration_ms"`
}

type HotPathMetricsSnapshot struct {
	Enabled   bool                         `json:"enabled"`
	SinceUnix int64                        `json:"since_unix"`
	Stages    map[string]HotPathStageStats `json:"stages"`
	Counters  map[string]int64             `json:"counters"`
}

func StartHotPathStage(name string) HotPathStageTimer {
	if !HotPathMetricsEnabled || name == "" {
		return HotPathStageTimer{}
	}
	return HotPathStageTimer{
		name:    name,
		start:   time.Now(),
		enabled: true,
	}
}

func (timer HotPathStageTimer) Finish(err error) {
	timer.FinishFailed(err != nil)
}

func (timer HotPathStageTimer) FinishFailed(failed bool) {
	if !timer.enabled {
		return
	}
	ObserveHotPathStage(timer.name, time.Since(timer.start), failed)
}

func ObserveHotPathStage(name string, duration time.Duration, failed bool) {
	if !HotPathMetricsEnabled || name == "" {
		return
	}
	if duration < 0 {
		duration = 0
	}
	raw, _ := hotPathStageMetrics.LoadOrStore(name, &hotPathStageMetric{})
	metric := raw.(*hotPathStageMetric)
	durationUS := duration.Microseconds()
	metric.count.Add(1)
	if failed {
		metric.errorCount.Add(1)
	}
	metric.totalDurationUS.Add(durationUS)
	for {
		current := metric.maxDurationUS.Load()
		if durationUS <= current {
			break
		}
		if metric.maxDurationUS.CompareAndSwap(current, durationUS) {
			break
		}
	}
}

func IncrementHotPathCounter(name string) {
	if !HotPathMetricsEnabled || name == "" {
		return
	}
	IncrementOperationalCounter(name)
}

// Queue loss and worker failures remain visible even when sampled hot-path
// timing is disabled. These rare events do not add per-request timing work.
func IncrementOperationalCounter(name string) {
	if name == "" {
		return
	}
	raw, _ := hotPathCounters.LoadOrStore(name, &atomic.Int64{})
	raw.(*atomic.Int64).Add(1)
}

func SnapshotHotPathMetrics() HotPathMetricsSnapshot {
	snapshot := HotPathMetricsSnapshot{
		Enabled:   HotPathMetricsEnabled,
		SinceUnix: hotPathMetricsStartedAt,
		Stages:    map[string]HotPathStageStats{},
		Counters:  map[string]int64{},
	}
	hotPathStageMetrics.Range(func(key, value interface{}) bool {
		name, ok := key.(string)
		if !ok {
			return true
		}
		metric, ok := value.(*hotPathStageMetric)
		if !ok || metric == nil {
			return true
		}
		count := metric.count.Load()
		totalDurationUS := metric.totalDurationUS.Load()
		stats := HotPathStageStats{
			Count:           count,
			ErrorCount:      metric.errorCount.Load(),
			TotalDurationUS: totalDurationUS,
			MaxDurationUS:   metric.maxDurationUS.Load(),
		}
		if count > 0 {
			stats.AvgDurationMS = float64(totalDurationUS) / float64(count) / 1000
		}
		snapshot.Stages[name] = stats
		return true
	})
	hotPathCounters.Range(func(key, value interface{}) bool {
		name, ok := key.(string)
		if !ok {
			return true
		}
		counter, ok := value.(*atomic.Int64)
		if !ok || counter == nil {
			return true
		}
		snapshot.Counters[name] = counter.Load()
		return true
	})
	return snapshot
}
