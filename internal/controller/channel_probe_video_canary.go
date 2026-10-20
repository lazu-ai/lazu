package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay/channel/video"
	"github.com/lazu-ai/lazu/internal/service"
)

// A canary is the only synthetic evidence that a video route can make a
// video. The liveness check (probeVideoLiveness) says the provider answers;
// a canary asks it for the smallest clip the model makes and waits for it.
//
// It costs real money, so it runs only where nothing else speaks for the
// route: a model on the allowlist, idle for a day, or tripped by failed
// generations that no customer is around to retry. One canary runs at a
// time across every replica, against its own daily budget.
//
// It does not go through media_jobs. A media job carries billing admission,
// ledger continuity and the usage log, none of which a probe may touch, so
// the canary submits through the same preparation and sender a customer's
// job uses and then polls in place until the provider decides.
const (
	channelProbeVideoCanaryInterval         = 24 * time.Hour
	channelProbeVideoCanaryRecoveryInterval = time.Hour
	// channelProbeVideoCanaryCostUSD is what one canary is budgeted at:
	// Seedance 2.5 sells 4 seconds of 480p at $0.41, and a provider costs
	// less than its sell price.
	channelProbeVideoCanaryCostUSD          = 0.5
	channelProbeVideoCanaryDefaultBudgetUSD = 1.0
	// channelProbeVideoCanaryDefaultModels are the models canaries run for
	// unless CHANNEL_PROBE_VIDEO_CANARY_MODELS says otherwise ("off" for none).
	channelProbeVideoCanaryDefaultModels = "seedance-2.5"
	channelProbeVideoCanaryDeadline      = 20 * time.Minute
	channelProbeVideoCanaryClaimKey      = "video-canary"
	videoCanaryPrompt                    = "A single red ball resting on a plain white floor, static camera."
	videoCanarySeed                      = int64(20261020)
)

// videoCanaryPollInterval is how often a running canary's task is read.
// A variable so tests do not wait a provider's real cadence.
var videoCanaryPollInterval = 15 * time.Second

// VideoCanaryCandidate is one route the canary scheduler decided is owed a
// generation.
type VideoCanaryCandidate struct {
	Lane      string
	ModelName string
	ChannelId int
	// Recovery marks a route whose breaker was tripped by failed generations.
	// Its canary is the only thing that can end the streak while it is idle.
	Recovery bool
	LastRun  time.Time
}

func videoCanaryModels() map[string]bool {
	raw := strings.TrimSpace(common.GetEnvOrDefaultString("CHANNEL_PROBE_VIDEO_CANARY_MODELS", channelProbeVideoCanaryDefaultModels))
	models := map[string]bool{}
	if strings.EqualFold(raw, "off") || strings.EqualFold(raw, "none") {
		return models
	}
	for _, name := range strings.Split(raw, ",") {
		if name = model.NormalizePricingModelName(strings.TrimSpace(name)); name != "" {
			models[name] = true
		}
	}
	return models
}

// videoCanaryDailyBudgetUSD reads the canary ceiling. Zero switches canaries
// off; a negative value lifts the cap.
func videoCanaryDailyBudgetUSD() float64 {
	if raw := strings.TrimSpace(common.GetEnvOrDefaultString("CHANNEL_PROBE_VIDEO_DAILY_BUDGET_USD", "")); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
			return parsed
		}
	}
	return channelProbeVideoCanaryDefaultBudgetUSD
}

// SelectVideoCanaryCandidatesContext decides which video routes are owed a
// canary, recovery checks first, then the longest without one.
func SelectVideoCanaryCandidatesContext(ctx context.Context, now time.Time) ([]VideoCanaryCandidate, error) {
	allowed := videoCanaryModels()
	if len(allowed) == 0 {
		return nil, nil
	}
	ctx = model.ChannelHealthAnalysisContext(ctx)
	grid, err := model.LoadChannelHealthGridContext(ctx)
	if err != nil {
		return nil, err
	}
	lastTraffic := map[string]time.Time{}
	outcomes, err := model.QueryChannelHealthLastOutcomesContext(ctx, now.Add(-channelProbeVideoCanaryInterval))
	if err != nil {
		return nil, err
	}
	for _, row := range outcomes {
		if last := row.LastOutcomeAt.Ptr(); last != nil {
			lastTraffic[model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)] = *last
		}
	}
	lastRun, err := model.LoadVideoCanaryLastRunsContext(ctx, now.Add(-channelProbeVideoCanaryInterval))
	if err != nil {
		return nil, err
	}
	states, err := model.LoadRouteHealthStates()
	if err != nil {
		return nil, err
	}
	tripped := map[string]bool{}
	for _, state := range states {
		if state.TrippedByGeneration() {
			tripped[model.RouteHealthCacheKey(state.ModelName, state.Lane, state.ChannelId)] = true
		}
	}

	var candidates []VideoCanaryCandidate
	for _, cell := range grid {
		if cell.ChannelStatus != common.ChannelStatusEnabled ||
			(cell.AbilityEnabled != nil && !*cell.AbilityEnabled) ||
			!allowed[model.NormalizePricingModelName(cell.ModelName)] {
			continue
		}
		key := model.ChannelHealthCellKey(cell.ModelName, cell.Lane, cell.ChannelId)
		recovery := tripped[model.RouteHealthCacheKey(cell.ModelName, cell.Lane, cell.ChannelId)]
		interval := channelProbeVideoCanaryInterval
		if recovery {
			interval = channelProbeVideoCanaryRecoveryInterval
		}
		if last, ok := lastRun[key]; ok && now.Sub(last) < interval {
			continue
		}
		if !recovery {
			// A customer's generation in the last day already speaks for
			// the route, and better than a canary would.
			if last, ok := lastTraffic[key]; ok && now.Sub(last) < channelProbeVideoCanaryInterval {
				continue
			}
		}
		candidates = append(candidates, VideoCanaryCandidate{
			Lane: cell.Lane, ModelName: cell.ModelName, ChannelId: cell.ChannelId,
			Recovery: recovery, LastRun: lastRun[key],
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Recovery != candidates[j].Recovery {
			return candidates[i].Recovery
		}
		if !candidates[i].LastRun.Equal(candidates[j].LastRun) {
			return candidates[i].LastRun.Before(candidates[j].LastRun)
		}
		if candidates[i].ModelName != candidates[j].ModelName {
			return candidates[i].ModelName < candidates[j].ModelName
		}
		return candidates[i].ChannelId < candidates[j].ChannelId
	})
	return candidates, nil
}

// RunVideoCanarySweep runs at most one canary: the most owed route's, if the
// day's budget covers it and no other replica is running one.
func RunVideoCanarySweep(ctx context.Context, now time.Time) (ChannelProbeSweepResult, error) {
	result := ChannelProbeSweepResult{}
	budget := videoCanaryDailyBudgetUSD()
	if budget == 0 {
		result.BudgetHit = true
		return result, nil
	}
	candidates, err := SelectVideoCanaryCandidatesContext(ctx, now)
	if err != nil || len(candidates) == 0 {
		return result, err
	}
	result.Considered = len(candidates)

	token := uuid.NewString()
	claimedAt := time.Now().UTC()
	claimed, err := model.ClaimChannelProbe(channelProbeVideoCanaryClaimKey, token, claimedAt,
		claimedAt.Add(channelProbeVideoCanaryDeadline+5*time.Minute))
	if err != nil || !claimed {
		return result, err
	}
	defer func() {
		if err := model.ReleaseChannelProbeClaim(channelProbeVideoCanaryClaimKey, token); err != nil {
			common.SysLog("video canary: failed to release claim: " + err.Error())
		}
	}()
	if budget > 0 {
		spent, err := model.VideoCanarySpendSince(channelProbeUTCDate(claimedAt))
		if err != nil {
			return result, err
		}
		if spent+channelProbeVideoCanaryCostUSD > budget {
			result.BudgetHit = true
			return result, nil
		}
	}

	candidate := candidates[0]
	verdict := runVideoCanary(ctx, candidate)
	if err := model.RecordChannelProbe(verdict.outcome); err != nil {
		common.SysLog("video canary: failed to record result: " + err.Error())
	}
	recordVideoCanaryBreakerEvidence(candidate, verdict)
	InvalidateChannelHealthCache()
	result.Probed = 1
	result.EstimatedUSD = verdict.outcome.CostUSD
	if verdict.outcome.Skipped {
		result.Skipped = 1
	} else if !verdict.outcome.Success {
		result.Failures = 1
	}
	return result, nil
}

// videoCanaryVerdict is one canary's ledger row and what its failure, if
// any, says about the route.
type videoCanaryVerdict struct {
	outcome model.ChannelProbeResult
	// generation is true when the provider accepted the job and then failed
	// to make it. A refusal at submission is reachability evidence instead.
	generation bool
	code       string
}

func runVideoCanary(ctx context.Context, candidate VideoCanaryCandidate) videoCanaryVerdict {
	verdict := videoCanaryVerdict{outcome: model.ChannelProbeResult{
		Lane: candidate.Lane, ModelName: candidate.ModelName, ChannelId: candidate.ChannelId,
		Modality: model.ChannelProbeModalityVideo,
	}}
	finish := func() videoCanaryVerdict {
		verdict.outcome.ProbedAt = time.Now().UTC()
		return verdict
	}
	skip := func(message string) videoCanaryVerdict {
		verdict.outcome.Skipped = true
		verdict.outcome.ErrorMessage = message
		return finish()
	}

	channel, err := model.GetChannelById(candidate.ChannelId, true)
	if err != nil || channel == nil {
		return skip("channel unavailable")
	}
	prepared, err := prepareVideoCanaryRequest(ctx, channel, candidate)
	if err != nil {
		return skip("prepare canary: " + err.Error())
	}
	started := time.Now()
	submission, err := sendVideoProviderSubmission(ctx, prepared)
	if err != nil {
		var submitErr *videoProviderSubmitError
		switch {
		case ctx.Err() != nil:
			return skip("canary interrupted at submission: " + ctx.Err().Error())
		case errors.As(err, &submitErr) && submitErr.Accepted:
			// The provider may be making it and billing us for it.
			verdict.outcome.CostUSD = channelProbeVideoCanaryCostUSD
			return skip("canary submission indeterminate: " + err.Error())
		case errors.As(err, &submitErr) && videoCanarySubmitRefusalIsOurs(submitErr.StatusCode):
			// The provider understood the request and refused its shape. That
			// is the canary's fault, not the route's; the operator sees why.
			return skip("canary request refused: " + err.Error())
		default:
			verdict.outcome.ErrorMessage = err.Error()
			verdict.outcome.LatencyMs = int(time.Since(started).Milliseconds())
			return finish()
		}
	}
	verdict.outcome.CostUSD = channelProbeVideoCanaryCostUSD

	deadline := started.Add(channelProbeVideoCanaryDeadline)
	job := video.Job{UpstreamID: submission.UpstreamID, ArtifactURL: submission.ArtifactURL, Model: candidate.ModelName}
	for submission.ArtifactURL == "" {
		if time.Now().After(deadline) {
			verdict.generation, verdict.code = true, "video_generation_timeout"
			verdict.outcome.ErrorMessage = "canary did not finish within " + channelProbeVideoCanaryDeadline.String()
			verdict.outcome.LatencyMs = int(time.Since(started).Milliseconds())
			return finish()
		}
		select {
		case <-ctx.Done():
			return skip("canary interrupted while polling: " + ctx.Err().Error())
		case <-time.After(videoCanaryPollInterval):
		}
		state, code, message, done := pollVideoCanary(ctx, prepared, job)
		if !done {
			continue
		}
		verdict.outcome.LatencyMs = int(time.Since(started).Milliseconds())
		if state == video.StateSucceeded {
			verdict.outcome.Success = true
			return finish()
		}
		if video.ClassifyFailure(code) == video.FailureCaller {
			// The fixed prompt refused or the task withdrawn: not the route.
			return skip(fmt.Sprintf("canary refused by the provider (%s): %s", code, message))
		}
		verdict.generation, verdict.code = true, code
		verdict.outcome.ErrorMessage = strings.TrimSpace(code + ": " + message)
		return finish()
	}
	verdict.outcome.LatencyMs = int(time.Since(started).Milliseconds())
	verdict.outcome.Success = true
	return finish()
}

// pollVideoCanary reads the canary's task once. done is false while the
// provider is still working or the read failed in a way the next read may
// not.
func pollVideoCanary(ctx context.Context, prepared *preparedVideoProviderRequest, job video.Job) (state string, code string, message string, done bool) {
	request, err := prepared.Driver.BuildStatus(prepared.Channel, job)
	if err != nil {
		return video.StateFailed, "video_provider_configuration_invalid", err.Error(), true
	}
	response, body, err := service.DoVideoProviderRequest(ctx, http.MethodGet, request.URL, request.Headers, prepared.Proxy)
	if err != nil {
		return "", "", "", false
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		// The customer poller's rule: a refusal is final, anything else (a
		// task not yet visible right after submission, a 5xx) is read again.
		switch response.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
			return video.StateFailed, "video_provider_poll_rejected",
				fmt.Sprintf("status read returned %d", response.StatusCode), true
		default:
			return "", "", "", false
		}
	}
	parsed, err := prepared.Driver.ParseStatus(prepared.Channel, body)
	if err != nil {
		return "", "", "", false
	}
	switch parsed.State {
	case video.StateSucceeded, video.StateFailed:
		return parsed.State, parsed.ErrorCode, parsed.ErrorMessage, true
	case video.StateCancelled:
		return video.StateFailed, "video_provider_cancelled", parsed.ErrorMessage, true
	default:
		return "", "", "", false
	}
}

// videoCanarySubmitRefusalIsOurs reports a submission status that says the
// request was understood and refused, rather than the route failing: any
// 4xx but an auth refusal or a throttle.
func videoCanarySubmitRefusalIsOurs(statusCode int) bool {
	switch {
	case statusCode == http.StatusUnauthorized, statusCode == http.StatusForbidden,
		statusCode == http.StatusTooManyRequests, statusCode == http.StatusPaymentRequired:
		return false
	default:
		return statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError
	}
}

func recordVideoCanaryBreakerEvidence(candidate VideoCanaryCandidate, verdict videoCanaryVerdict) {
	if verdict.outcome.Skipped {
		return
	}
	reason := verdict.outcome.ErrorMessage
	if verdict.generation {
		reason = model.RouteHealthGenerationReason(verdict.code)
	}
	if !model.ShouldRecordRouteHealthEvidence(candidate.ModelName, candidate.Lane, candidate.ChannelId, verdict.outcome.Success) {
		return
	}
	if err := model.RecordRouteHealthEvidence(
		candidate.ModelName, candidate.Lane, candidate.ChannelId, verdict.outcome.Success, reason, verdict.outcome.ProbedAt,
	); err != nil {
		common.SysLog("video canary: failed to record route health: " + err.Error())
	}
}

// prepareVideoCanaryRequest builds the canary exactly as a customer's job on
// this route would be built: the same channel context, model mapping,
// parameter and header overrides, family driver and body.
func prepareVideoCanaryRequest(ctx context.Context, channel *model.Channel, candidate VideoCanaryCandidate) (*preparedVideoProviderRequest, error) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = (&http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Path: "/v1/videos"},
		Header: http.Header{"Content-Type": []string{"application/json"}},
	}).WithContext(ctx)
	cache, err := model.GetUserCache(1)
	if err != nil {
		return nil, err
	}
	cache.WriteContext(c)
	lane := model.NormalizeChannelLane(candidate.Lane)
	if !model.IsValidChannelLane(lane) {
		lane = model.ChannelLaneDirect
	}
	c.Set("group", lane)
	if lazuErr := middleware.SetupContextForSelectedChannel(c, channel, candidate.ModelName); lazuErr != nil {
		return nil, lazuErr
	}
	return prepareVideoProviderRequest(c, videoCanaryRequest(candidate.ModelName))
}

// videoCanaryRequest is the smallest, cheapest clip the model makes: the
// shortest duration, the lowest resolution, no sound, a fixed prompt and
// seed so its timing is comparable from day to day.
func videoCanaryRequest(modelName string) dto.VideoRequest {
	request := dto.VideoRequest{Model: modelName, Prompt: videoCanaryPrompt}
	seed := videoCanarySeed
	request.Seed = &seed
	caps := model.ModelVideoCapabilities(modelName)
	if caps == nil {
		return request
	}
	switch {
	case caps.MinSeconds > 0:
		request.Seconds = caps.MinSeconds
	case len(caps.Durations) > 0:
		shortest := caps.Durations[0]
		for _, seconds := range caps.Durations[1:] {
			if seconds < shortest {
				shortest = seconds
			}
		}
		request.Seconds = shortest
	default:
		request.Seconds = caps.DefaultSeconds
	}
	request.Resolution = lowestVideoResolution(caps.Resolutions)
	request.AspectRatio = caps.DefaultRatio
	if caps.Audio != nil && *caps.Audio {
		silent := false
		request.GenerateAudio = &silent
	}
	return request
}

// lowestVideoResolution picks the smallest "<n>p" resolution listed.
func lowestVideoResolution(resolutions []string) string {
	best, bestLines := "", 0
	for _, resolution := range resolutions {
		lines, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(resolution)), "p"))
		if err != nil || lines <= 0 {
			continue
		}
		if best == "" || lines < bestLines {
			best, bestLines = resolution, lines
		}
	}
	return best
}
