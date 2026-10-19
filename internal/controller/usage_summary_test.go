package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func usageSummaryTestContext(path string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	return c
}

func TestParseUsageSummaryQueryRejectsUnboundedHistoryWindow(t *testing.T) {
	c := usageSummaryTestContext("/api/usage/summary?since=0&until=40000000")
	if _, _, _, err := parseUsageSummaryQuery(c); err == nil {
		t.Fatal("expected large explicit window to be rejected")
	}
}

func TestParseUsageSummaryQueryAllowsExplicitZeroPointWindow(t *testing.T) {
	c := usageSummaryTestContext("/api/usage/summary?since=0&until=0&granularity=hour")
	since, until, granularity, err := parseUsageSummaryQuery(c)
	if err != nil {
		t.Fatalf("expected zero point window to be allowed: %v", err)
	}
	if since != 0 || until != 0 || granularity != "hour" {
		t.Fatalf("unexpected parsed query: since=%d until=%d granularity=%s", since, until, granularity)
	}
}
