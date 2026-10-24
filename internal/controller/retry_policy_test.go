package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestClassifyRelayRetryDecision(t *testing.T) {
	tests := []struct {
		name      string
		err       *types.LazuError
		wantRetry bool
		wantClass relayRetryClass
	}{
		{
			name:      "nil error",
			err:       nil,
			wantRetry: false,
			wantClass: retryClassNone,
		},
		{
			name:      "channel error",
			err:       types.NewErrorWithStatusCode(errors.New("channel key invalid"), types.ErrorCodeChannelInvalidKey, http.StatusUnauthorized),
			wantRetry: true,
			wantClass: retryClassChannelError,
		},
		{
			name:      "skip retry",
			err:       types.NewErrorWithStatusCode(errors.New("no retry"), types.ErrorCodeInvalidRequest, http.StatusInternalServerError, types.ErrOptionWithSkipRetry()),
			wantRetry: false,
			wantClass: retryClassSkipRetry,
		},
		{
			name:      "429 retry",
			err:       types.NewErrorWithStatusCode(errors.New("too many"), types.ErrorCodeUpstreamError, http.StatusTooManyRequests),
			wantRetry: true,
			wantClass: retryClassRateLimited429,
		},
		{
			name:      "307 retry",
			err:       types.NewErrorWithStatusCode(errors.New("temporary"), types.ErrorCodeUpstreamError, http.StatusTemporaryRedirect),
			wantRetry: true,
			wantClass: retryClassTemporary307,
		},
		{
			name:      "timeout 408 no retry",
			err:       types.NewErrorWithStatusCode(errors.New("timeout"), types.ErrorCodeUpstreamError, http.StatusRequestTimeout),
			wantRetry: false,
			wantClass: retryClassTimeout,
		},
		{
			name:      "timeout 504 no retry",
			err:       types.NewErrorWithStatusCode(errors.New("gateway timeout"), types.ErrorCodeUpstreamError, http.StatusGatewayTimeout),
			wantRetry: false,
			wantClass: retryClassTimeout,
		},
		{
			name:      "server 500 retry",
			err:       types.NewErrorWithStatusCode(errors.New("server"), types.ErrorCodeUpstreamError, http.StatusInternalServerError),
			wantRetry: true,
			wantClass: retryClassServer5xx,
		},
		{
			name:      "400 no retry",
			err:       types.NewErrorWithStatusCode(errors.New("bad request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest),
			wantRetry: false,
			wantClass: retryClassBadRequest400,
		},
		{
			name:      "401 fallback retry",
			err:       types.NewErrorWithStatusCode(errors.New("unauthorized"), types.ErrorCodeUpstreamError, http.StatusUnauthorized),
			wantRetry: true,
			wantClass: retryClassClient4xxFallback,
		},
		{
			name:      "2xx no retry",
			err:       types.NewErrorWithStatusCode(errors.New("ok"), types.ErrorCodeUpstreamError, http.StatusOK),
			wantRetry: false,
			wantClass: retryClassSuccess2xx,
		},
		{
			name:      "unknown fallback retry",
			err:       types.NewErrorWithStatusCode(errors.New("unknown"), types.ErrorCodeUpstreamError, 0),
			wantRetry: true,
			wantClass: retryClassCatalog,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyRelayRetryDecision(tc.err)
			if got.AllowRetry != tc.wantRetry {
				t.Fatalf("AllowRetry mismatch: got=%v want=%v (class=%s)", got.AllowRetry, tc.wantRetry, got.Class)
			}
			if got.Class != tc.wantClass {
				t.Fatalf("Class mismatch: got=%s want=%s", got.Class, tc.wantClass)
			}
		})
	}
}

func TestShouldRetry_RespectsContextGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)

	baseErr := types.NewErrorWithStatusCode(errors.New("server error"), types.ErrorCodeUpstreamError, http.StatusInternalServerError)

	t.Run("retry_times_exhausted", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		if shouldRetry(c, baseErr, 0) {
			t.Fatal("expected false when retry times is 0")
		}
	})

	t.Run("specific_channel_no_retry", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("specific_channel_id", 123)
		if shouldRetry(c, baseErr, 2) {
			t.Fatal("expected false for specific channel request")
		}
	})

	t.Run("response_already_started_no_retry", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		info := &relaycommon.RelayInfo{
			StartTime:         time.Now(),
			FirstResponseTime: time.Now().Add(10 * time.Millisecond),
		}
		common.SetContextKey(c, constant.ContextKeyRelayInfo, info)
		if shouldRetry(c, baseErr, 2) {
			t.Fatal("expected false when response already started")
		}
	})

	t.Run("normal_retry_true", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		if !shouldRetry(c, baseErr, 2) {
			t.Fatal("expected retry for retryable 500 error")
		}
	})
}

func TestRelayErrorAfterRetrySelectionFailurePreservesUpstreamError(t *testing.T) {
	upstream := types.NewErrorWithStatusCode(errors.New("upstream overloaded"), types.ErrorCodeUpstreamError, http.StatusTooManyRequests)
	selection := types.NewErrorWithStatusCode(errors.New("no retry channel"), types.ErrorCodeGetChannelFailed, http.StatusInternalServerError)

	if got := relayErrorAfterRetrySelectionFailure(upstream, selection, 1); got != upstream {
		t.Fatalf("retry selection failure replaced the upstream error: got=%v want=%v", got, upstream)
	}
	if got := relayErrorAfterRetrySelectionFailure(nil, selection, 1); got != selection {
		t.Fatalf("selection error should be returned without a previous attempt: got=%v want=%v", got, selection)
	}
	if got := relayErrorAfterRetrySelectionFailure(upstream, selection, 0); got != selection {
		t.Fatalf("initial selection failure should be returned directly: got=%v want=%v", got, selection)
	}
}

func TestShouldReturnPreConsumedQuota(t *testing.T) {
	err := types.NewErrorWithStatusCode(errors.New("boom"), types.ErrorCodeUpstreamError, http.StatusInternalServerError)
	now := time.Now()

	tests := []struct {
		name string
		info *relaycommon.RelayInfo
		err  *types.LazuError
		want bool
	}{
		{
			name: "nil error",
			info: &relaycommon.RelayInfo{
				FinalPreConsumedQuota: 100,
				StartTime:             now,
			},
			err:  nil,
			want: false,
		},
		{
			name: "zero pre consumed quota",
			info: &relaycommon.RelayInfo{
				FinalPreConsumedQuota: 0,
				StartTime:             now,
			},
			err:  err,
			want: false,
		},
		{
			name: "response already started",
			info: &relaycommon.RelayInfo{
				FinalPreConsumedQuota: 100,
				StartTime:             now,
				FirstResponseTime:     now.Add(50 * time.Millisecond),
			},
			err:  err,
			want: false,
		},
		{
			name: "metadata arrived but no billable usage observed",
			info: &relaycommon.RelayInfo{FinalPreConsumedQuota: 100, StartTime: now, FirstResponseTime: now.Add(time.Millisecond), StreamFailedWithoutUsage: true},
			err:  err,
			want: true,
		},
		{
			name: "client canceled after response started",
			info: &relaycommon.RelayInfo{
				FinalPreConsumedQuota: 100,
				StartTime:             now,
				FirstResponseTime:     now.Add(50 * time.Millisecond),
			},
			err: types.NewError(
				context.Canceled,
				types.ErrorCodeClientCanceled,
				types.ErrOptionWithSkipRetry(),
			),
			want: true,
		},
		{
			name: "failed before response started",
			info: &relaycommon.RelayInfo{
				FinalPreConsumedQuota: 100,
				StartTime:             now,
				FirstResponseTime:     now.Add(-time.Second),
			},
			err:  err,
			want: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := shouldReturnPreConsumedQuota(tc.info, tc.err)
			if got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestShouldRetryStopsCanceledRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	err := types.NewError(io.ErrUnexpectedEOF, types.ErrorCodeReadResponseBodyFailed)
	if shouldRetry(c, err, 3) {
		t.Fatal("canceled request must not retry even if the read error is retryable")
	}
}

func TestRetryBackoffStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(10*time.Millisecond, cancel)
	defer timer.Stop()
	start := time.Now()
	err := waitRelayRetry(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatalf("backoff ignored cancellation: %v", err)
	}
}

// The agent lane may retry, but only against the channel the customer's price
// was quoted from: switching would bill them for a channel they did not pick.
func TestCheapLaneRetriesWithoutSwitchingChannels(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusUnauthorized} {
		for _, lane := range []string{"cheap", "direct"} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			common.SetContextKey(c, constant.ContextKeyUsingGroup, lane)
			err := types.NewErrorWithStatusCode(errors.New("upstream failed"), types.ErrorCodeUpstreamError, status)
			if !shouldRetry(c, err, 3) {
				t.Fatalf("lane=%s status=%d refused a retry", lane, status)
			}
			plan := planRelayRetry(c, err, 0, 0, 0)
			if !plan.Retry {
				t.Fatalf("lane=%s status=%d planned no retry", lane, status)
			}
			if wantSame := lane == "cheap"; (plan.Pick == pickSameChannel) != wantSame {
				t.Fatalf("lane=%s status=%d pick=%v", lane, status, plan.Pick)
			}
		}
	}
}

// A channel-level failure on the agent lane has nowhere to go: the same channel
// would reject the retry identically and no other channel may serve the lane.
func TestCheapLaneStopsOnChannelError(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "cheap")
	err := types.NewErrorWithStatusCode(errors.New("bad key"), types.ErrorCodeChannelInvalidKey, http.StatusUnauthorized)
	if plan := planRelayRetry(c, err, 0, 0, 0); plan.Retry {
		t.Fatalf("agent lane retried a channel-level failure: %+v", plan)
	}
}

// Once the switch budget is spent, a direct-lane request keeps retrying in
// place rather than giving up while attempts remain.
func TestDirectLaneFallsBackToSameChannelAfterSwitch(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "direct")
	err := types.NewErrorWithStatusCode(errors.New("upstream failed"), types.ErrorCodeUpstreamError, http.StatusServiceUnavailable)
	plan := planRelayRetry(c, err, 1, 0, relayMaxChannelSwitches)
	if !plan.Retry || plan.Pick != pickSameChannel {
		t.Fatalf("exhausted switch budget ended the request: %+v", plan)
	}
}

// The budget, not the attempt count, is what bounds a slow-failing upstream.
func TestRetryStopsWhenFirstByteBudgetIsSpent(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "direct")
	err := types.NewErrorWithStatusCode(errors.New("overloaded"), types.ErrorCodeUpstreamOverloaded, http.StatusServiceUnavailable)
	if plan := planRelayRetry(c, err, 1, relayFirstByteBudget-time.Millisecond, 0); plan.Retry {
		t.Fatalf("retried past the first-byte budget: %+v", plan)
	}
	if plan := planRelayRetry(c, err, 1, 0, 0); !plan.Retry {
		t.Fatal("refused a retry well inside the budget")
	}
}

// The budget between attempts and the cut-off inside a streamed attempt are
// the same clock; a non-streamed request is not cut off.
func TestStreamStartDeadlineIsTheFirstByteBudget(t *testing.T) {
	started := time.Now()
	if got := relayStreamStartDeadline(true, started); !got.Equal(started.Add(relayFirstByteBudget)) {
		t.Fatalf("stream deadline=%s want started+%s", got.Sub(started), relayFirstByteBudget)
	}
	if got := relayStreamStartDeadline(false, started); !got.IsZero() {
		t.Fatalf("non-streamed request got a deadline %s after start", got.Sub(started))
	}
}

// A provider asking for a longer pause than a retry is worth ends the sequence
// instead of parking the caller on a dead request.
func TestRetryAfterBeyondWindowStopsRetrying(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "direct")
	common.SetContextKey(c, constant.ContextKeyUpstreamRetryAfter, int(relayMaxRetryAfter/time.Second)+5)
	err := types.NewErrorWithStatusCode(errors.New("slow down"), types.ErrorCodeUpstreamRateLimited, http.StatusTooManyRequests)
	if plan := planRelayRetry(c, err, 0, 0, 0); plan.Retry {
		t.Fatalf("honored an out-of-range Retry-After: %+v", plan)
	}

	common.SetContextKey(c, constant.ContextKeyUpstreamRetryAfter, 3)
	plan := planRelayRetry(c, err, 0, 0, 0)
	if !plan.Retry || plan.Delay < 2*time.Second || plan.Delay > 4*time.Second {
		t.Fatalf("ignored an in-range Retry-After: %+v", plan)
	}
}

// 1s / 2s / 4s, jittered by ±20%, and never beyond the cap.
func TestRetryBackoffSchedule(t *testing.T) {
	for attempt, base := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second} {
		if got := relayRetryBackoff(attempt); got != base {
			t.Fatalf("attempt %d backoff = %s, want %s", attempt, got, base)
		}
		for i := 0; i < 50; i++ {
			jittered := applyRelayRetryJitter(base)
			if jittered < time.Duration(float64(base)*0.8) || jittered > time.Duration(float64(base)*1.2) {
				t.Fatalf("attempt %d jitter %s outside ±20%% of %s", attempt, jittered, base)
			}
		}
	}
}

// The pre-flight failures (unusable channel type, unsupported relay format)
// have no same-channel option by construction, so they ask for a switch. That
// request still has to go through the lane rule and the switch budget: deciding
// locally is how an agent-lane request ends up on a channel whose price the
// customer never agreed to.
func TestPreflightSwitchObeysLaneAndBudget(t *testing.T) {
	newCtx := func(lane string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, lane)
		return c
	}
	err := types.NewErrorWithStatusCode(errors.New("invalid channel type"), types.ErrorCodeInvalidApiType, http.StatusServiceUnavailable)

	// Agent lane: no channel it could legitimately move to.
	used, pick := 0, pickSameChannel
	if planSwitchOnly(newCtx(model.ChannelLaneAgent), err, 0, 0, &used, &pick) {
		t.Fatal("agent lane was allowed to switch channels on a pre-flight failure")
	}
	if used != 0 || pick != pickSameChannel {
		t.Fatalf("refused switch still mutated state: used=%d pick=%v", used, pick)
	}

	// Direct lane: one switch, debited, and not a second.
	used, pick = 0, pickSameChannel
	if !planSwitchOnly(newCtx(model.ChannelLaneDirect), err, 0, 0, &used, &pick) {
		t.Fatal("direct lane refused its first switch")
	}
	if used != 1 || pick != pickDifferentChannelOnly {
		t.Fatalf("switch not accounted: used=%d pick=%v", used, pick)
	}
	if planSwitchOnly(newCtx(model.ChannelLaneDirect), err, 1, 0, &used, &pick) {
		t.Fatal("switch budget exceeded")
	}

	// And the first-byte budget still applies.
	used, pick = 0, pickSameChannel
	if planSwitchOnly(newCtx(model.ChannelLaneDirect), err, 0, relayFirstByteBudget, &used, &pick) {
		t.Fatal("pre-flight switch ignored the first-byte budget")
	}
}
