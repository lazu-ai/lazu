package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
)

func TestParseRetryAfterAcceptsBothRFC7231Forms(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, value string
		want        int
		ok          bool
	}{
		{name: "delay seconds", value: "7", want: 7, ok: true},
		{name: "http date", value: now.Add(30 * time.Second).Format(http.TimeFormat), want: 30, ok: true},
		{name: "past date is spent", value: now.Add(-time.Minute).Format(http.TimeFormat)},
		{name: "zero is not a wait", value: "0"},
		{name: "negative", value: "-5"},
		{name: "absurd value is ignored", value: "86400"},
		{name: "garbage", value: "soon"},
		{name: "absent", value: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseRetryAfter(tc.value, now)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("parseRetryAfter(%q) = (%d, %v), want (%d, %v)", tc.value, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestCaptureUpstreamRetryAfterStoresHintForTheRetryLoop(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	header := make(http.Header)
	header.Set("Retry-After", "4")
	CaptureUpstreamRetryAfter(c, &http.Response{StatusCode: http.StatusTooManyRequests, Header: header})
	if got := common.GetContextKeyInt(c, constant.ContextKeyUpstreamRetryAfter); got != 4 {
		t.Fatalf("captured Retry-After = %d, want 4", got)
	}

	// A response without the header must not overwrite nothing with garbage.
	fresh, _ := gin.CreateTestContext(httptest.NewRecorder())
	CaptureUpstreamRetryAfter(fresh, &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header)})
	if got := common.GetContextKeyInt(fresh, constant.ContextKeyUpstreamRetryAfter); got != 0 {
		t.Fatalf("invented a Retry-After: %d", got)
	}
}
