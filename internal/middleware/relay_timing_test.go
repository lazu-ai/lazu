package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

type delayedAccessWriter struct{ written bool }

func (w *delayedAccessWriter) Write(data []byte) (int, error) {
	time.Sleep(5 * time.Millisecond)
	w.written = true
	return len(data), nil
}

func TestRelayTimingIncludesOuterAccessWriteWithoutControlEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("ACCESS_LOG", "true")
	oldWriter, oldMetrics := gin.DefaultWriter, common.HotPathMetricsEnabled
	defer func() { gin.DefaultWriter, common.HotPathMetricsEnabled = oldWriter, oldMetrics }()
	writer := &delayedAccessWriter{}
	gin.DefaultWriter, common.HotPathMetricsEnabled = writer, true
	router := gin.New()
	count := 0
	router.Use(common.FinishRelayTimingAfterMiddleware(func(_ *gin.Context, fields map[string]interface{}) {
		count++
		if !writer.written || fields["gateway_post_ms"].(float64) < 5 || fields[common.RelayPhaseMiddlewareFinish].(float64) < 5 {
			t.Fatalf("clock finished before access writer: %v", fields)
		}
	}))
	SetUpLogger(router)
	router.GET("/control", func(c *gin.Context) {
		// Log helpers/internal callers may have created a clock themselves;
		// only explicit relay routing may cause the outer diagnostic callback.
		common.EnsureRelayTiming(c)
		common.StartRelayPhase(c, common.RelayPhaseBilling).Finish()
		c.Status(http.StatusOK)
	})
	router.Use(common.TrackRelayTiming(func(_ *gin.Context, _ map[string]interface{}) { t.Fatal("duplicate inner finalization") }))
	router.GET("/v1/responses", func(c *gin.Context) { _, _ = c.Writer.Write([]byte("data: done\n\n")) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/control", nil))
	if count != 0 {
		t.Fatal("control-plane request published relay diagnostics")
	}
	writer.written = false
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/responses", nil))
	if count != 1 {
		t.Fatalf("expected one finalization, got %d", count)
	}
}
