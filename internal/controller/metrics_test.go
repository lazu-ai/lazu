package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

func TestPrometheusMetricsExposeRuntimePoolsAndStageHistogram(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousEnabled, previousToken, previousHot := common.MetricsEnabled, common.MetricsToken, common.HotPathMetricsEnabled
	common.MetricsEnabled, common.MetricsToken, common.HotPathMetricsEnabled = true, "metrics-test-token", true
	t.Cleanup(func() {
		common.MetricsEnabled, common.MetricsToken, common.HotPathMetricsEnabled = previousEnabled, previousToken, previousHot
	})
	common.ObserveHotPathStage(common.HotPathStageChatStreamEvent, 2*time.Millisecond, false)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	c.Request.Header.Set("X-Metrics-Token", "metrics-test-token")
	GetPrometheusMetrics(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"lazu_go_goroutines ",
		"lazu_go_heap_alloc_bytes ",
		"lazu_go_gc_pause_seconds_total ",
		`lazu_hot_path_stage_latency_seconds_bucket{le="0.0025",stage="relay.chat_stream_event"}`,
		`lazu_hot_path_stage_latency_seconds_bucket{le="+Inf",stage="relay.chat_stream_event"}`,
		`lazu_hot_path_stage_latency_seconds_count{stage="relay.chat_stream_event"}`,
		`lazu_upstream_connections_total{reused="true"}`,
		`lazu_upstream_connections_total{reused="false"}`,
		`lazu_upstream_dial_seconds_bucket{le="15"}`,
		`lazu_upstream_tls_handshake_seconds_count`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output lacks %q", want)
		}
	}
	// Every sample belongs to a family declared with # TYPE, and histogram
	// series use the declared family's _bucket/_sum/_count suffixes.
	declared := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.HasPrefix(line, "# TYPE ") {
			fields := strings.Fields(line)
			declared[fields[2]] = fields[3]
			continue
		}
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		name := strings.FieldsFunc(line, func(r rune) bool { return r == '{' || r == ' ' })[0]
		if _, ok := declared[name]; ok {
			continue
		}
		family := name
		for _, suffix := range []string{"_bucket", "_sum", "_count"} {
			family = strings.TrimSuffix(family, suffix)
		}
		if declared[family] != "histogram" {
			t.Fatalf("sample %q has no declared family", line)
		}
	}
}
