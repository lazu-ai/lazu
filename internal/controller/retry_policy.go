package controller

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

const (
	// relayMaxRetries is the number of extra upstream attempts after the first
	// one, so a request reaches its provider at most relayMaxRetries+1 times.
	// Fixed on purpose: an operator-tunable count in the admin options was the
	// only reason production ran with a single cross-channel retry and no
	// same-channel retry at all. The SDKs clients already speak to us with
	// (OpenAI, Anthropic) default to 2; LiteLLM to 2; Portkey and Cloudflare
	// cap at 5. Three sits inside that band and the first-byte budget below is
	// what actually bounds the user-visible wait.
	relayMaxRetries = 3

	// relayMaxChannelSwitches is how many of those retries may land on a
	// different channel. Only a channel-level failure (rejected key, disabled
	// channel, model withdrawn) earns a switch; a busy or flaky provider is
	// retried in place. The agent lane gets none of these, because another
	// agent-lane channel can carry a different price than the one the customer chose.
	relayMaxChannelSwitches = 1

	// relayFirstByteBudget caps the total time a request may spend retrying
	// before the first byte reaches the client. A slow-failing upstream (one
	// that sits on a request for half a minute and then reports overload) would
	// otherwise multiply its own latency by the retry count. Past this point the
	// error belongs to the client, whose own reconnect logic is better placed to
	// decide what to do with it. No retry starts past it, and a streamed
	// attempt still waiting for its upstream's headers or first data event is
	// abandoned at it (RelayInfo.StreamStartDeadline).
	relayFirstByteBudget = 90 * time.Second

	// relayMaxRetryAfter bounds how long an upstream may push us out via
	// Retry-After. A longer hint is the provider saying it has no capacity for
	// this request, so retrying is worse than failing fast.
	relayMaxRetryAfter = 10 * time.Second

	// relayRetryBaseDelay is the wait before the first retry; each later retry
	// doubles it, giving 1s / 2s / 4s.
	relayRetryBaseDelay = time.Second

	// relayRetryMaxDelay caps a single computed backoff (before Retry-After is
	// considered), keeping the 3-retry schedule at ~7s of total waiting.
	relayRetryMaxDelay = 4 * time.Second

	// relayRetryJitterFraction spreads concurrent retries against the same
	// upstream by ±20%, so a provider blip does not produce a synchronized
	// second wave from every in-flight request.
	relayRetryJitterFraction = 0.2
)

// relayRetryBackoff returns the base wait after the attempt with the given
// 0-based index has failed, before jitter and before any Retry-After hint.
func relayRetryBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := relayRetryBaseDelay
	for i := 0; i < attempt && delay < relayRetryMaxDelay; i++ {
		delay *= 2
	}
	if delay > relayRetryMaxDelay {
		delay = relayRetryMaxDelay
	}
	return delay
}

// applyRelayRetryJitter spreads a backoff across ±relayRetryJitterFraction.
func applyRelayRetryJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	spread := 1 - relayRetryJitterFraction + rand.Float64()*(2*relayRetryJitterFraction)
	jittered := time.Duration(float64(delay) * spread)
	if jittered <= 0 {
		return delay
	}
	return jittered
}

// relayRetryDelay resolves the wait before the next attempt. A Retry-After hint
// from the upstream wins over the local schedule because the provider knows its
// own recovery window; ok is false when that hint asks for longer than a retry
// is worth, which ends the retry sequence.
func relayRetryDelay(attempt int, retryAfter time.Duration) (delay time.Duration, ok bool) {
	if retryAfter > 0 {
		if retryAfter > relayMaxRetryAfter {
			return 0, false
		}
		// An explicit provider hint is already a deliberate spacing; jitter it
		// too so a shared limit does not release every waiter at once.
		return applyRelayRetryJitter(retryAfter), true
	}
	return applyRelayRetryJitter(relayRetryBackoff(attempt)), true
}

// relayRetryWithinBudget reports whether waiting delay and making another
// attempt still fits inside the first-byte budget. elapsed is the time already
// spent on this request.
func relayRetryWithinBudget(elapsed, delay time.Duration) bool {
	return elapsed+delay < relayFirstByteBudget
}

// relayStreamStartDeadline is when a streamed request, started at started,
// stops waiting for any attempt's upstream to begin answering. Zero for a
// non-streamed request, whose headers arrive only with the whole answer.
func relayStreamStartDeadline(stream bool, started time.Time) time.Time {
	if !stream {
		return time.Time{}
	}
	return started.Add(relayFirstByteBudget)
}

// waitRelayRetry paces the next attempt against an unhealthy upstream without
// delaying local validation errors. Cancellation interrupts the wait rather
// than scheduling more work.
func waitRelayRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

// channelPickMode says which channel the next attempt should go to.
type channelPickMode int

const (
	// pickSameChannel re-sends to the channel that just failed. On a multi-key
	// channel the re-setup advances to the next key.
	pickSameChannel channelPickMode = iota
	// pickDifferentChannel prefers another compatible channel but falls back to
	// the current one when none is free, because a same-channel retry still
	// beats giving up while the budget allows another attempt.
	pickDifferentChannel
	// pickDifferentChannelOnly refuses to fall back. A channel that rejected the
	// key or withdrew the model will reject the retry the same way, and repeating
	// a rejected credential is how accounts get locked.
	pickDifferentChannelOnly
)

// relayRetryPlan is the decision for one failed attempt: whether to send again,
// against which channel, and after how long. Reason explains a refusal, for the
// operator log.
type relayRetryPlan struct {
	Retry  bool
	Pick   channelPickMode
	Delay  time.Duration
	Reason string
}

// planRelayRetry resolves everything about the next attempt in one place, so the
// relay loop can record the failed attempt knowing whether the request is over.
// attempt is the 0-based index of the attempt that just failed and elapsed is
// the time spent on the request so far.
func planRelayRetry(c *gin.Context, err *types.LazuError, attempt int, elapsed time.Duration, channelSwitchesUsed int) relayRetryPlan {
	if c != nil && c.Request != nil && c.Request.URL.Path == "/v1/decisions" {
		if attempt >= 1 || elapsed >= 10*time.Second || c.Request.Context().Err() != nil || err == nil || types.IsSkipRetryError(err) {
			return relayRetryPlan{}
		}
		if err.GetErrorCode() == types.ErrorCodeUpstreamCapacity || err.GetErrorCode() == types.ErrorCodeUpstreamDecisionInvalid || err.StatusCode >= 500 {
			if common.GetContextKeyString(c, constant.ContextKeyUsingGroup) == model.ChannelLaneAgent {
				return relayRetryPlan{}
			}
			return relayRetryPlan{Retry: true, Pick: pickDifferentChannelOnly}
		}
		return relayRetryPlan{}
	}
	if !shouldRetry(c, err, relayMaxRetries-attempt) {
		return relayRetryPlan{}
	}

	plan := relayRetryPlan{Retry: true, Pick: pickSameChannel}

	// A different channel fails independently of this one, so it is the better
	// next attempt whenever the lane allows it. The agent lane allows none:
	// another agent-lane channel may carry a different price, and switching silently
	// would bill the customer for something they did not choose. Once the switch
	// budget is spent, the remaining retries stay in place.
	cheapLane := common.GetContextKeyString(c, constant.ContextKeyUsingGroup) == model.ChannelLaneAgent
	switch {
	case cheapLane:
		if types.IsChannelError(err) {
			// Nothing else may serve this lane, and the channel itself is the
			// problem, so another send cannot help.
			return relayRetryPlan{Reason: "the agent lane does not switch channels"}
		}
	case channelSwitchesUsed < relayMaxChannelSwitches:
		plan.Pick = pickDifferentChannel
		if types.IsChannelError(err) {
			plan.Pick = pickDifferentChannelOnly
		}
	case types.IsChannelError(err):
		return relayRetryPlan{Reason: "no channel switches left for a channel-level failure"}
	}

	if relayRetryBackoffApplies(err) {
		delay, ok := relayRetryDelay(attempt, upstreamRetryAfter(c))
		if !ok {
			return relayRetryPlan{Reason: "upstream Retry-After exceeds the retry window"}
		}
		plan.Delay = delay
	}

	if !relayRetryWithinBudget(elapsed, plan.Delay) {
		return relayRetryPlan{Reason: fmt.Sprintf("first-byte budget %s exhausted after %s", relayFirstByteBudget, elapsed.Round(time.Millisecond))}
	}
	return plan
}

// relayRetryBackoffApplies reports whether an error warrants spacing out the
// next attempt. Transport-level failures and local errors retry immediately;
// an overloaded or rate-limited provider needs room to recover.
func relayRetryBackoffApplies(err *types.LazuError) bool {
	if err == nil {
		return false
	}
	return err.StatusCode == http.StatusTooManyRequests || err.StatusCode >= 500
}

// StatusCloudflareTimeout is Cloudflare's non-standard 524 "A Timeout Occurred" status code.
const StatusCloudflareTimeout = 524

type relayRetryClass string

const (
	retryClassNone              relayRetryClass = "none"
	retryClassChannelError      relayRetryClass = "channel_error"
	retryClassSkipRetry         relayRetryClass = "skip_retry"
	retryClassBadResponseBody   relayRetryClass = "bad_response_body"
	retryClassCatalog           relayRetryClass = "catalog_retry"
	retryClassRateLimited429    relayRetryClass = "rate_limited_429"
	retryClassTemporary307      relayRetryClass = "temporary_redirect_307"
	retryClassTimeout           relayRetryClass = "timeout"
	retryClassServer5xx         relayRetryClass = "server_5xx"
	retryClassBadRequest400     relayRetryClass = "bad_request_400"
	retryClassSuccess2xx        relayRetryClass = "success_2xx"
	retryClassClient4xxFallback relayRetryClass = "client_4xx_fallback"
	retryClassUnknownFallback   relayRetryClass = "unknown_fallback"
	// Fallback-category-driven classes: content-policy and context-window
	// errors should NOT retry against the same channel — retrying produces
	// the same content-filter block / overflow. Spec §J's "3-class fallback"
	// design says these need a different provider (content) or longer-context
	// model (window). The channel-swap part of that design lives in the
	// distributor candidate loop; here we just stop the retry storm.
	retryClassContentPolicy relayRetryClass = "content_policy_blocked"
	retryClassContextWindow relayRetryClass = "context_window_exceeded"
)

type relayRetryDecision struct {
	AllowRetry bool
	Class      relayRetryClass
}

func classifyRelayRetryDecision(err *types.LazuError) relayRetryDecision {
	if err == nil {
		return relayRetryDecision{AllowRetry: false, Class: retryClassNone}
	}
	if err.GetErrorCode() == types.ErrorCodeClientCanceled || err.GetErrorCode() == types.ErrorCodeDownstreamWriteFailed {
		return relayRetryDecision{AllowRetry: false, Class: retryClassSkipRetry}
	}
	if types.IsChannelError(err) || err.GetErrorCode() == types.ErrorCodeUpstreamBillingFailed {
		return relayRetryDecision{AllowRetry: true, Class: retryClassChannelError}
	}
	if types.IsSkipRetryError(err) {
		return relayRetryDecision{AllowRetry: false, Class: retryClassSkipRetry}
	}
	if err.GetErrorCode() == types.ErrorCodeBadResponseBody {
		return relayRetryDecision{AllowRetry: false, Class: retryClassBadResponseBody}
	}
	// Spec §J: content-policy / context-window are "wrong-fit" errors. Same
	// channel will block again. Mark them non-retryable so the request fails
	// fast instead of cycling all channels for the same content rejection.
	switch types.GetFallbackCategory(err.GetErrorCode()) {
	case types.FallbackContentPolicy:
		return relayRetryDecision{AllowRetry: false, Class: retryClassContentPolicy}
	case types.FallbackContextWindow:
		return relayRetryDecision{AllowRetry: false, Class: retryClassContextWindow}
	}

	switch {
	case err.StatusCode == http.StatusTooManyRequests:
		return relayRetryDecision{AllowRetry: true, Class: retryClassRateLimited429}
	case err.StatusCode == http.StatusTemporaryRedirect:
		return relayRetryDecision{AllowRetry: true, Class: retryClassTemporary307}
	case err.StatusCode == http.StatusRequestTimeout || err.StatusCode == http.StatusGatewayTimeout || err.StatusCode == StatusCloudflareTimeout:
		return relayRetryDecision{AllowRetry: false, Class: retryClassTimeout}
	case err.StatusCode/100 == 5:
		return relayRetryDecision{AllowRetry: true, Class: retryClassServer5xx}
	case err.StatusCode == http.StatusBadRequest:
		return relayRetryDecision{AllowRetry: false, Class: retryClassBadRequest400}
	case err.StatusCode/100 == 2:
		return relayRetryDecision{AllowRetry: false, Class: retryClassSuccess2xx}
	case err.StatusCode/100 == 4:
		// Keep historical behavior: most client errors may still succeed on next channel.
		return relayRetryDecision{AllowRetry: true, Class: retryClassClient4xxFallback}
	}
	if types.ShouldRetry(err.GetErrorCode()) {
		return relayRetryDecision{AllowRetry: true, Class: retryClassCatalog}
	}
	return relayRetryDecision{AllowRetry: true, Class: retryClassUnknownFallback}
}

func shouldReturnPreConsumedQuota(info *relaycommon.RelayInfo, err *types.LazuError) bool {
	if err == nil || info == nil {
		return false
	}
	if info.FinalPreConsumedQuota == 0 {
		return false
	}
	if info.StreamFailedWithoutUsage {
		return true
	}
	// Stream handlers settle provider-reported or gateway-observed usage before
	// returning a cancellation and clear FinalPreConsumedQuota. A non-zero
	// reservation here therefore means no trustworthy billable output was
	// observed, so the remaining fallback is a full refund.
	if err.GetErrorCode() == types.ErrorCodeClientCanceled {
		return true
	}
	// If response has started, do not blindly refund to avoid accounting drift.
	if info.HasSendResponse() {
		return false
	}
	return true
}

// planSwitchOnly handles the local pre-flight failures — an unusable channel
// type, a relay format the channel cannot serve — where sending to the same
// channel again is pointless by construction. It reports whether another
// attempt may be made, and if so leaves pick set to a mandatory switch and the
// switch budget debited.
//
// These failures never reached a provider, so there is nothing to back off
// from; they still have to respect the lane rule and the switch budget, which
// is why they route through the same accounting as every other retry instead
// of setting pick themselves.
func planSwitchOnly(
	c *gin.Context,
	err *types.LazuError,
	attempt int,
	elapsed time.Duration,
	channelSwitchesUsed *int,
	pick *channelPickMode,
) bool {
	if !shouldRetry(c, err, relayMaxRetries-attempt) {
		return false
	}
	if common.GetContextKeyString(c, constant.ContextKeyUsingGroup) == model.ChannelLaneAgent {
		// Another agent-lane channel may carry a different price than the one the
		// customer chose, so there is nowhere to go.
		return false
	}
	if *channelSwitchesUsed >= relayMaxChannelSwitches {
		return false
	}
	if !relayRetryWithinBudget(elapsed, 0) {
		return false
	}
	*channelSwitchesUsed++
	*pick = pickDifferentChannelOnly
	return true
}
