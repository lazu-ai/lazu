package middleware

import (
	"testing"
	"time"
)

func TestInMemoryRateLimiterResetStartsFresh(t *testing.T) {
	var limiter InMemoryRateLimiter
	limiter.Init(time.Millisecond)
	if !limiter.Request("client", 1, 60) {
		t.Fatal("expected the first request to pass")
	}
	if limiter.Request("client", 1, 60) {
		t.Fatal("expected the second request to be limited")
	}

	limiter.Reset()
	limiter.Init(time.Millisecond)
	if !limiter.Request("client", 1, 60) {
		t.Fatal("expected reset to discard the previous request window")
	}
	limiter.Reset()
}

func TestInMemoryRateLimiterCanBeReinitializedAfterCleanupTick(t *testing.T) {
	var limiter InMemoryRateLimiter
	for range 10 {
		limiter.Init(time.Nanosecond)
		if !limiter.Request("client", 1, 60) {
			t.Fatal("expected the first request for each state to pass")
		}
		limiter.Reset()
	}
}
