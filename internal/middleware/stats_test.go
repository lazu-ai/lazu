package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestStatsMiddlewareRecordsREDMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := globalStats
	globalStats = &HTTPStats{}
	t.Cleanup(func() { globalStats = previous })

	router := gin.New()
	router.Use(StatsMiddleware())
	router.GET("/failure", func(c *gin.Context) {
		c.Status(http.StatusServiceUnavailable)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/failure", nil))

	stats := GetStats()
	if stats.ActiveConnections != 0 {
		t.Fatalf("active connections = %d, want 0", stats.ActiveConnections)
	}
	if stats.RequestCount != 1 {
		t.Fatalf("request count = %d, want 1", stats.RequestCount)
	}
	if stats.ErrorCount != 1 {
		t.Fatalf("error count = %d, want 1", stats.ErrorCount)
	}
	if stats.TotalDurationUS < 0 || stats.MaxDurationUS < 0 {
		t.Fatalf("durations must not be negative: total=%d max=%d", stats.TotalDurationUS, stats.MaxDurationUS)
	}
}
