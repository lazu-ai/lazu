package controller

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
)

const prometheusContentType = "text/plain; version=0.0.4; charset=utf-8"

// GetPrometheusMetrics exposes a minimal Prometheus scrape endpoint without
// adding a registry dependency to the hot path.
func GetPrometheusMetrics(c *gin.Context) {
	if !common.MetricsEnabled {
		c.Status(http.StatusNotFound)
		return
	}
	token := strings.TrimSpace(common.MetricsToken)
	if token == "" {
		c.Status(http.StatusNotFound)
		return
	}
	if !metricsTokenAuthorized(c, token) {
		c.Status(http.StatusUnauthorized)
		return
	}

	var b strings.Builder

	writePrometheusHelpType(&b, "lazu_info", "Lazu build and node metadata.", "gauge")
	writePrometheusSample(&b, "lazu_info", map[string]string{
		"version":  common.Version,
		"node":     common.NodeName,
		"provider": common.NodeProvider,
		"region":   common.NodeRegion,
	}, 1)

	writePrometheusHelpType(&b, "lazu_uptime_seconds", "Process uptime in seconds.", "gauge")
	writePrometheusSample(&b, "lazu_uptime_seconds", nil, float64(time.Now().Unix()-common.StartTime))

	systemStatus := common.GetSystemStatus()
	writePrometheusHelpType(&b, "lazu_system_cpu_usage_percent", "Latest sampled CPU usage percentage.", "gauge")
	writePrometheusSample(&b, "lazu_system_cpu_usage_percent", nil, systemStatus.CPUUsage)
	writePrometheusHelpType(&b, "lazu_system_memory_usage_percent", "Latest sampled memory usage percentage.", "gauge")
	writePrometheusSample(&b, "lazu_system_memory_usage_percent", nil, systemStatus.MemoryUsage)
	writePrometheusHelpType(&b, "lazu_system_disk_usage_percent", "Latest sampled disk usage percentage.", "gauge")
	writePrometheusSample(&b, "lazu_system_disk_usage_percent", nil, systemStatus.DiskUsage)
	writePrometheusHelpType(&b, "lazu_system_status_updated_at_seconds", "Unix timestamp of the latest system resource sample.", "gauge")
	writePrometheusSample(&b, "lazu_system_status_updated_at_seconds", nil, float64(systemStatus.UpdatedAt))

	httpStats := middleware.GetStats()
	writePrometheusHelpType(&b, "lazu_http_active_requests", "Current in-flight HTTP requests.", "gauge")
	writePrometheusSample(&b, "lazu_http_active_requests", nil, float64(httpStats.ActiveConnections))
	writePrometheusHelpType(&b, "lazu_http_requests_total", "HTTP requests completed by this process.", "counter")
	writePrometheusSample(&b, "lazu_http_requests_total", nil, float64(httpStats.RequestCount))
	writePrometheusHelpType(&b, "lazu_http_errors_total", "HTTP 5xx responses completed by this process.", "counter")
	writePrometheusSample(&b, "lazu_http_errors_total", nil, float64(httpStats.ErrorCount))
	writePrometheusHelpType(&b, "lazu_http_request_duration_seconds_sum", "Total HTTP request duration observed by this process.", "counter")
	writePrometheusSample(&b, "lazu_http_request_duration_seconds_sum", nil, float64(httpStats.TotalDurationUS)/1_000_000)
	writePrometheusHelpType(&b, "lazu_http_request_duration_seconds_max", "Maximum HTTP request duration observed by this process.", "gauge")
	writePrometheusSample(&b, "lazu_http_request_duration_seconds_max", nil, float64(httpStats.MaxDurationUS)/1_000_000)

	hotPath := common.SnapshotHotPathMetrics()
	writePrometheusHelpType(&b, "lazu_hot_path_metrics_enabled", "Whether hot-path instrumentation is enabled.", "gauge")
	writePrometheusSample(&b, "lazu_hot_path_metrics_enabled", nil, boolMetric(hotPath.Enabled))
	writePrometheusHelpType(&b, "lazu_hot_path_metrics_since_seconds", "Unix timestamp when hot-path metrics started.", "gauge")
	writePrometheusSample(&b, "lazu_hot_path_metrics_since_seconds", nil, float64(hotPath.SinceUnix))

	writePrometheusHelpType(&b, "lazu_hot_path_stage_total", "Observed hot-path stage executions by status.", "counter")
	writePrometheusHelpType(&b, "lazu_hot_path_stage_duration_seconds_sum", "Total observed hot-path stage duration.", "counter")
	writePrometheusHelpType(&b, "lazu_hot_path_stage_duration_seconds_max", "Maximum observed hot-path stage duration.", "gauge")
	writePrometheusHelpType(&b, "lazu_hot_path_stage_duration_seconds_avg", "Average observed hot-path stage duration.", "gauge")
	stageNames := sortedStageNames(hotPath.Stages)
	for _, name := range stageNames {
		stats := hotPath.Stages[name]
		writePrometheusSample(&b, "lazu_hot_path_stage_total", map[string]string{"stage": name, "status": "ok"}, float64(stats.Count-stats.ErrorCount))
		writePrometheusSample(&b, "lazu_hot_path_stage_total", map[string]string{"stage": name, "status": "error"}, float64(stats.ErrorCount))
		writePrometheusSample(&b, "lazu_hot_path_stage_duration_seconds_sum", map[string]string{"stage": name}, float64(stats.TotalDurationUS)/1_000_000)
		writePrometheusSample(&b, "lazu_hot_path_stage_duration_seconds_max", map[string]string{"stage": name}, float64(stats.MaxDurationUS)/1_000_000)
		writePrometheusSample(&b, "lazu_hot_path_stage_duration_seconds_avg", map[string]string{"stage": name}, stats.AvgDurationMS/1000)
	}

	writeHotPathStageHistogram(&b, stageNames, hotPath)

	writePrometheusHelpType(&b, "lazu_hot_path_counter_total", "Hot-path counters.", "counter")
	counterNames := sortedCounterNames(hotPath.Counters)
	for _, name := range counterNames {
		writePrometheusSample(&b, "lazu_hot_path_counter_total", map[string]string{"name": name}, float64(hotPath.Counters[name]))
	}
	writeProtocolMetrics(&b, hotPath)

	identityL1 := model.SnapshotIdentityL1()
	writePrometheusHelpType(&b, "lazu_identity_l1_enabled", "Whether the bounded process-local identity snapshot cache is enabled.", "gauge")
	writePrometheusSample(&b, "lazu_identity_l1_enabled", nil, boolMetric(identityL1.Enabled))
	writePrometheusHelpType(&b, "lazu_identity_l1_entries", "Current process-local identity snapshot cache entries.", "gauge")
	writePrometheusSample(&b, "lazu_identity_l1_entries", nil, float64(identityL1.Entries))
	writePrometheusHelpType(&b, "lazu_identity_l1_bytes", "Estimated process-local identity snapshot cache bytes.", "gauge")
	writePrometheusSample(&b, "lazu_identity_l1_bytes", nil, float64(identityL1.Bytes))
	writePrometheusHelpType(&b, "lazu_identity_l1_evictions_total", "Process-local identity snapshot cache evictions.", "counter")
	writePrometheusSample(&b, "lazu_identity_l1_evictions_total", nil, float64(identityL1.Evictions))

	writeRuntimeMetrics(&b)
	writePoolMetrics(&b)
	writeUpstreamConnMetrics(&b)

	c.Data(200, prometheusContentType, []byte(b.String()))
}

// writeHotPathStageHistogram exposes each stage's duration distribution as a
// Prometheus histogram, so quantiles can be computed across scrapes.
func writeHotPathStageHistogram(b *strings.Builder, stageNames []string, hotPath common.HotPathMetricsSnapshot) {
	const name = "lazu_hot_path_stage_latency_seconds"
	writePrometheusHelpType(b, name, "Hot-path stage duration distribution.", "histogram")
	for _, stage := range stageNames {
		stats := hotPath.Stages[stage]
		for i, bound := range common.HotPathStageBucketsUS {
			le := strconv.FormatFloat(float64(bound)/1_000_000, 'f', -1, 64)
			writePrometheusSample(b, name+"_bucket", map[string]string{"stage": stage, "le": le}, float64(stats.Buckets[i]))
		}
		writePrometheusSample(b, name+"_bucket", map[string]string{"stage": stage, "le": "+Inf"}, float64(stats.HistogramCount))
		writePrometheusSample(b, name+"_sum", map[string]string{"stage": stage}, float64(stats.TotalDurationUS)/1_000_000)
		writePrometheusSample(b, name+"_count", map[string]string{"stage": stage}, float64(stats.HistogramCount))
	}
}

// writeRuntimeMetrics reads Go runtime statistics at scrape time; nothing is
// recorded on the request path.
func writeRuntimeMetrics(b *strings.Builder) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	writePrometheusHelpType(b, "lazu_go_goroutines", "Goroutines that currently exist.", "gauge")
	writePrometheusSample(b, "lazu_go_goroutines", nil, float64(runtime.NumGoroutine()))
	writePrometheusHelpType(b, "lazu_go_heap_alloc_bytes", "Bytes of allocated heap objects.", "gauge")
	writePrometheusSample(b, "lazu_go_heap_alloc_bytes", nil, float64(mem.HeapAlloc))
	writePrometheusHelpType(b, "lazu_go_heap_inuse_bytes", "Bytes in in-use heap spans.", "gauge")
	writePrometheusSample(b, "lazu_go_heap_inuse_bytes", nil, float64(mem.HeapInuse))
	writePrometheusHelpType(b, "lazu_go_sys_bytes", "Bytes of memory obtained from the OS.", "gauge")
	writePrometheusSample(b, "lazu_go_sys_bytes", nil, float64(mem.Sys))
	writePrometheusHelpType(b, "lazu_go_gc_cycles_total", "Completed GC cycles.", "counter")
	writePrometheusSample(b, "lazu_go_gc_cycles_total", nil, float64(mem.NumGC))
	writePrometheusHelpType(b, "lazu_go_gc_pause_seconds_total", "Cumulative stop-the-world GC pause time.", "counter")
	writePrometheusSample(b, "lazu_go_gc_pause_seconds_total", nil, float64(mem.PauseTotalNs)/1e9)
	writePrometheusHelpType(b, "lazu_go_gc_last_pause_seconds", "Duration of the most recent GC pause.", "gauge")
	writePrometheusSample(b, "lazu_go_gc_last_pause_seconds", nil, float64(mem.PauseNs[(mem.NumGC+255)%256])/1e9)
}

// writePoolMetrics reports database and Redis connection pool statistics at
// scrape time, including how often callers waited for or failed to get a
// connection.
func writePoolMetrics(b *strings.Builder) {
	pools := model.DBPoolStats()
	names := make([]string, 0, len(pools))
	for name := range pools {
		names = append(names, name)
	}
	sort.Strings(names)
	writePrometheusHelpType(b, "lazu_db_pool_open_connections", "Open database connections, in use and idle.", "gauge")
	writePrometheusHelpType(b, "lazu_db_pool_in_use_connections", "Database connections currently in use.", "gauge")
	writePrometheusHelpType(b, "lazu_db_pool_idle_connections", "Idle database connections.", "gauge")
	writePrometheusHelpType(b, "lazu_db_pool_wait_total", "Times a caller waited for a database connection.", "counter")
	writePrometheusHelpType(b, "lazu_db_pool_wait_seconds_total", "Total time callers waited for a database connection.", "counter")
	for _, name := range names {
		stats := pools[name]
		labels := map[string]string{"db": name}
		writePrometheusSample(b, "lazu_db_pool_open_connections", labels, float64(stats.OpenConnections))
		writePrometheusSample(b, "lazu_db_pool_in_use_connections", labels, float64(stats.InUse))
		writePrometheusSample(b, "lazu_db_pool_idle_connections", labels, float64(stats.Idle))
		writePrometheusSample(b, "lazu_db_pool_wait_total", labels, float64(stats.WaitCount))
		writePrometheusSample(b, "lazu_db_pool_wait_seconds_total", labels, stats.WaitDuration.Seconds())
	}
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	redisStats := common.RDB.PoolStats()
	writePrometheusHelpType(b, "lazu_redis_pool_total_connections", "Redis connections in the pool.", "gauge")
	writePrometheusSample(b, "lazu_redis_pool_total_connections", nil, float64(redisStats.TotalConns))
	writePrometheusHelpType(b, "lazu_redis_pool_idle_connections", "Idle Redis connections in the pool.", "gauge")
	writePrometheusSample(b, "lazu_redis_pool_idle_connections", nil, float64(redisStats.IdleConns))
	writePrometheusHelpType(b, "lazu_redis_pool_hits_total", "Times a free Redis connection was found in the pool.", "counter")
	writePrometheusSample(b, "lazu_redis_pool_hits_total", nil, float64(redisStats.Hits))
	writePrometheusHelpType(b, "lazu_redis_pool_misses_total", "Times no free Redis connection was in the pool.", "counter")
	writePrometheusSample(b, "lazu_redis_pool_misses_total", nil, float64(redisStats.Misses))
	writePrometheusHelpType(b, "lazu_redis_pool_timeouts_total", "Times waiting for a Redis connection timed out.", "counter")
	writePrometheusSample(b, "lazu_redis_pool_timeouts_total", nil, float64(redisStats.Timeouts))
}

func writeUpstreamConnMetrics(b *strings.Builder) {
	reused, opened := common.UpstreamConnCounts()
	writePrometheusHelpType(b, "lazu_upstream_connections_total", "Upstream connections the relay client used, by whether an idle one was reused.", "counter")
	writePrometheusSample(b, "lazu_upstream_connections_total", map[string]string{"reused": "true"}, float64(reused))
	writePrometheusSample(b, "lazu_upstream_connections_total", map[string]string{"reused": "false"}, float64(opened))
	writeDurationHistogram(b, "lazu_upstream_dial_seconds", "Time to dial a new upstream connection.", &common.UpstreamDialDurations)
	writeDurationHistogram(b, "lazu_upstream_tls_handshake_seconds", "Time for the TLS handshake of a new upstream connection.", &common.UpstreamTLSHandshakeDurations)
}

func writeDurationHistogram(b *strings.Builder, name, help string, histogram *common.DurationHistogram) {
	cumulative, sum := histogram.Snapshot()
	writePrometheusHelpType(b, name, help, "histogram")
	for i, bound := range common.UpstreamDurationBounds {
		writePrometheusSample(b, name+"_bucket", map[string]string{"le": strconv.FormatFloat(bound, 'g', -1, 64)}, float64(cumulative[i]))
	}
	total := cumulative[len(cumulative)-1]
	writePrometheusSample(b, name+"_bucket", map[string]string{"le": "+Inf"}, float64(total))
	writePrometheusSample(b, name+"_sum", nil, sum)
	writePrometheusSample(b, name+"_count", nil, float64(total))
}

func writeProtocolMetrics(b *strings.Builder, hotPath common.HotPathMetricsSnapshot) {
	writePrometheusHelpType(b, "lazu_protocol_plan_total", "Text-generation protocol plans by mode.", "counter")
	writePrometheusSample(b, "lazu_protocol_plan_total", map[string]string{"mode": "native"}, float64(hotPath.Counters[common.HotPathCounterProtocolPlanNative]))
	writePrometheusSample(b, "lazu_protocol_plan_total", map[string]string{"mode": "bridge"}, float64(hotPath.Counters[common.HotPathCounterProtocolPlanBridge]))
	writePrometheusHelpType(b, "lazu_protocol_conversion_failures_total", "Protocol response conversion failures.", "counter")
	writePrometheusSample(b, "lazu_protocol_conversion_failures_total", nil, float64(hotPath.Counters[common.HotPathCounterProtocolConversionFailed]))
	writePrometheusHelpType(b, "lazu_protocol_unsupported_total", "Protocol requests rejected before upstream by feature.", "counter")
	for _, name := range sortedCounterNames(hotPath.Counters) {
		if !strings.HasPrefix(name, common.HotPathCounterProtocolUnsupportedPrefix) {
			continue
		}
		feature := strings.TrimPrefix(name, common.HotPathCounterProtocolUnsupportedPrefix)
		if feature == "" {
			feature = "unknown"
		}
		writePrometheusSample(b, "lazu_protocol_unsupported_total", map[string]string{"feature": feature}, float64(hotPath.Counters[name]))
	}
	if stats, ok := hotPath.Stages[common.HotPathStageProtocolStreamEvent]; ok {
		writePrometheusHelpType(b, "lazu_protocol_stream_event_duration_seconds", "Average protocol stream event conversion duration.", "gauge")
		writePrometheusSample(b, "lazu_protocol_stream_event_duration_seconds", nil, stats.AvgDurationMS/1000)
	}
}

func metricsTokenAuthorized(c *gin.Context, token string) bool {
	if c == nil {
		return false
	}
	if got := strings.TrimSpace(c.GetHeader("X-Metrics-Token")); got != "" {
		return got == token
	}
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[len("bearer "):]) == token
	}
	return false
}

func writePrometheusHelpType(b *strings.Builder, name, help, metricType string) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s %s\n", name, metricType)
}

func writePrometheusSample(b *strings.Builder, name string, labels map[string]string, value float64) {
	b.WriteString(name)
	if len(labels) > 0 {
		keys := make([]string, 0, len(labels))
		for key := range labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(b, `%s="%s"`, key, prometheusLabelEscape(labels[key]))
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	b.WriteByte('\n')
}

func prometheusLabelEscape(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
	return replacer.Replace(value)
}

func boolMetric(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func sortedStageNames(stages map[string]common.HotPathStageStats) []string {
	names := make([]string, 0, len(stages))
	for name := range stages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedCounterNames(counters map[string]int64) []string {
	names := make([]string, 0, len(counters))
	for name := range counters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
