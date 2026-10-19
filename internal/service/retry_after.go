package service

import (
	"net/http"
	"strconv"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"

	"github.com/gin-gonic/gin"
)

// maxCapturedRetryAfterSeconds bounds what we are willing to read out of an
// upstream header. The retry loop applies its own, tighter ceiling; this one
// only keeps an absurd or hostile value from reaching it.
const maxCapturedRetryAfterSeconds = 3600

// CaptureUpstreamRetryAfter records the failed attempt's Retry-After hint so
// the relay retry loop can pace the next attempt the way the provider asked,
// instead of guessing with its own backoff. Both RFC 7231 forms are accepted:
// delay-seconds and an HTTP-date. Anything unparseable, negative, or beyond
// maxCapturedRetryAfterSeconds is ignored, leaving the local schedule in charge.
func CaptureUpstreamRetryAfter(c *gin.Context, resp *http.Response) {
	if c == nil || resp == nil {
		return
	}
	seconds, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	if !ok {
		return
	}
	common.SetContextKey(c, constant.ContextKeyUpstreamRetryAfter, seconds)
}

func parseRetryAfter(value string, now time.Time) (int, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 || seconds > maxCapturedRetryAfterSeconds {
			return 0, false
		}
		return seconds, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	// Round up so a sub-second remainder still waits out the full window.
	seconds := int((when.Sub(now) + time.Second - 1) / time.Second)
	if seconds <= 0 || seconds > maxCapturedRetryAfterSeconds {
		return 0, false
	}
	return seconds, true
}
