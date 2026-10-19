package controller

import (
	"fmt"
	"net/http"
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

	c.Data(200, prometheusContentType, []byte(b.String()))
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
