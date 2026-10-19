package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	_ "golang.org/x/image/webp"
)

type channelHealthProbeRequest struct {
	ModelName string `json:"model_name"`
	Lane      string `json:"lane"`
}

// ProbeChannelForHealth records an operator-triggered test in the same probe
// ledger as scheduled health checks, so the refreshed panel has new evidence.
func ProbeChannelForHealth(c *gin.Context) {
	id, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var req channelHealthProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	req.ModelName = strings.TrimSpace(req.ModelName)
	requestedLane := req.Lane
	req.Lane = model.NormalizeChannelLane(req.Lane)
	if req.ModelName == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "model_name"})
		return
	}
	if !model.IsValidChannelLane(req.Lane) {
		httperr.Code(c, types.ErrorCodeChannelLaneInvalid, types.D{"lane": requestedLane})
		return
	}
	grid, err := model.LoadChannelHealthGrid()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if cell, ok := findChannelProbeCell(grid, id, req.ModelName, req.Lane); ok {
		// A probe is one more sample of the route, never a replacement for its
		// traffic, so a route that is busy can still be tested on demand.
		outcome := probeChannelCell(c.Request.Context(), ChannelProbeCandidate{
			Lane: cell.Lane, ModelName: cell.ModelName, ChannelId: cell.ChannelId, ChannelName: cell.ChannelName,
		}, 0)
		if err := model.RecordChannelProbe(outcome); err != nil {
			httperr.Write(c, err)
			return
		}
		recordChannelProbeBreakerEvidence(cell.ModelName, cell.Lane, cell.ChannelId, outcome)
		InvalidateChannelHealthCache()
		refreshChannelHealthInBackground("manual probe")
		// The probe is recorded either way. A failed one is answered as a
		// failed test; its message stays in the probe ledger and the log.
		if !outcome.Success {
			httperr.Write(c, channelProbeFailure(outcome))
			return
		}
		common.ApiSuccess(c, gin.H{"success": true, "skipped": outcome.Skipped})
		return
	}
	httperr.Code(c, types.ErrorCodeChannelRouteNotFound, types.D{
		"channel_id": c.Param("id"), "model": req.ModelName, "lane": req.Lane,
	})
}

// channelProbeFailure is the answer to an operator probe that did not pass.
// A skipped probe never reached the provider; any other failure is the
// provider's.
func channelProbeFailure(outcome model.ChannelProbeResult) error {
	cause := fmt.Errorf("probe of channel %d model %s lane %s: %s",
		outcome.ChannelId, outcome.ModelName, outcome.Lane, outcome.ErrorMessage)
	if outcome.Skipped {
		return types.WrapErr(cause, types.ErrorCodeChannelTestNotSent)
	}
	return types.WrapErr(cause, types.ErrorCodeChannelTestFailed)
}

// findChannelProbeCell resolves the route an operator asked to test. The
// health page names a route by its catalog model, while the grid carries the
// routed ability name, and an aliased variant differs; an exact match wins,
// then any variant that resolves to the requested model.
func findChannelProbeCell(
	grid []model.ChannelHealthLaneCell, channelID int, modelName string, lane string,
) (model.ChannelHealthLaneCell, bool) {
	requested := model.NormalizePricingModelName(modelName)
	matched := -1
	for index, cell := range grid {
		if cell.ChannelId != channelID || cell.Lane != lane {
			continue
		}
		if cell.ModelName == modelName {
			return cell, true
		}
		if matched < 0 && model.NormalizePricingModelName(cell.ModelName) == requested {
			matched = index
		}
	}
	if matched < 0 {
		return model.ChannelHealthLaneCell{}, false
	}
	return grid[matched], true
}

// Synthetic probing exists because a (model, lane, channel) route that nobody
// called has no evidence behind it. A probe is a small streamed request, and
// the health page counts it as one more sample of the route alongside customer
// traffic — so every lane has a first-token time, a decode speed and a longest
// pause to show, however quiet it has been.
//
// Cost control is structural rather than hopeful:
//   - a route with real traffic in the last probe interval is not probed —
//     its customers are already sampling it;
//   - every text probe asks for ChannelProbeMaxOutputTokens and no more, and
//     an image probe for the smallest, lowest-quality image the model makes;
//   - image routes are generated against only after hours of idleness;
//   - a daily spend ceiling stops the sweep outright, so a pricing mistake or
//     a runaway loop cannot produce a surprise invoice.

const (
	// ChannelProbeMaxOutputTokens caps what a probe may generate. It is large
	// enough for decode speed and the longest pause to mean something — ten
	// tokens arrive in one or two chunks — and small enough that a probe on
	// the most expensive model costs well under a cent.
	ChannelProbeMaxOutputTokens = 64

	// channelProbeInputTokens is what the fixed probe prompt costs to send,
	// rounded up.
	channelProbeInputTokens = 32

	// A route seen in real traffic this recently needs no probe at all. It
	// matches the probe cadence, so a route that was called once and then
	// went quiet is back on the hourly schedule within one cell.
	channelProbeTrafficFreshness = channelProbeInterval

	// channelProbeInterval is the cadence for every idle route, busy or
	// long-forgotten alike.
	//
	// It used to be tiered: hourly for recently-used routes, six-hourly for
	// anything quiet for a week, and two-hourly for expensive models. Every
	// tier cost the page more than it saved. A 24-hour wall drawn in one-hour
	// cells shows a gap wherever a route was not sampled, and a reader cannot
	// tell "we did not look" from "it was not working".
	//
	// Forty-five minutes, not sixty: the scheduler wakes every ten and a
	// sweep takes a minute or two to reach a route, so a route due at sixty
	// was probed at sixty to seventy-odd and one hour in six went unsampled.
	// A cell now shows only what was measured inside it, so the cadence has
	// to leave margin: forty-five plus the heartbeat plus the sweep stays
	// inside an hour.
	channelProbeInterval = 45 * time.Minute

	// channelProbeMediaInterval is the cadence for image generation. One
	// image costs cents rather than fractions of a cent and takes seconds to
	// minutes, so an image route is only generated against after it has been
	// idle this long. Its wall is drawn in three-hour cells and a cell shows
	// only its own samples, so the interval leaves the same margin inside a
	// cell that the text cadence leaves inside an hour: nine images a day per
	// idle model, well under a dollar across the catalog.
	channelProbeMediaInterval = 2*time.Hour + 40*time.Minute

	// channelProbeImageCostUSD is what one probe image is budgeted at when no
	// per-call price is on file. It is deliberately pessimistic: the cheapest
	// image any upstream sells is about a cent, the dearest about a nickel.
	channelProbeImageCostUSD = 0.05

	// channelProbeSearchCostUSD budgets one search probe. The dearest backend
	// charges under a cent per basic query.
	channelProbeSearchCostUSD = 0.01

	// channelProbeRetreatInterval is how often a route the breaker has pulled
	// out of rotation is re-tested. It is the half-open probe of a circuit
	// breaker: much more frequent than the idle cadence, because nothing else
	// will bring this route back — real traffic is no longer being sent to it,
	// so a probe is the only thing that can prove it recovered.
	channelProbeRetreatInterval = time.Minute

	// channelProbeStaleAfter bounds how far back the scheduler looks for prior
	// evidence. Beyond it a route is simply "never seen recently", and the
	// exact age stops mattering.
	channelProbeStaleAfter = 7 * 24 * time.Hour

	// channelProbeFallbackCostUSD prices a probe on a route that has neither an
	// upstream cost row nor a sell price.
	//
	// The previous behaviour was to skip such routes outright, which quietly
	// turned a missing price row into a permanent hole in the health page.
	// A probe is ~100 tokens, and this is what that costs on the most
	// expensive model we route, so the budget stays honest.
	channelProbeFallbackCostUSD = 0.005

	// channelProbeDefaultDailyBudgetUSD is the ceiling for one UTC day across
	// every route.
	//
	// Roughly 130 live routes, sampled hourly, mostly on models whose 64
	// output tokens cost a fraction of a tenth of a cent, with the expensive
	// ones sampled every two hours: about $3–5/day, leaving headroom. Hitting
	// it stops probing until UTC midnight and every quiet wall goes blank,
	// which is precisely the failure probing exists to remove.
	channelProbeDefaultDailyBudgetUSD = 8.0

	// channelProbeMaxPerSweep bounds one pass so a cold start cannot fire
	// thousands of upstream calls at once.
	channelProbeMaxPerSweep = 40

	// channelProbeMaxRetreatsPerSweep reserves most of a sweep for ordinary
	// freshness work.
	//
	// A retreating route is due every minute while the sweep runs every ten,
	// and it sorts ahead of everything else. A provider outage that retreats
	// forty routes would therefore take every slot of every sweep for as long
	// as it lasted, and no healthy route would be probed at all — so after the
	// carry window every other lane's wall would go blank because of an
	// incident somewhere else entirely.
	channelProbeMaxRetreatsPerSweep = 12

	// channelProbeInterRequestDelay spaces probes so a sweep does not look
	// like a burst to an upstream rate limiter.
	channelProbeInterRequestDelay = 200 * time.Millisecond
)

func channelProbeClaimLease() time.Duration {
	// Non-stream probes use the relay HTTP timeout. Keep the lease slightly
	// longer so a slow but still in-flight request cannot be duplicated by a
	// second replica; a crashed owner becomes reclaimable shortly afterward.
	return time.Duration(common.NormalizeRelayTimeoutSeconds(common.RelayTimeout))*time.Second + time.Minute
}

// ChannelProbeCandidate is one route the scheduler decided to check.
type ChannelProbeCandidate struct {
	Lane        string
	ModelName   string
	ChannelId   int
	ChannelName string
	IdleFor     time.Duration
	// LastProbedAt is zero for a route that has never been probed.
	LastProbedAt time.Time
	// InRetreat marks a route the breaker has taken out of rotation, whose
	// probe is therefore a recovery check rather than a freshness top-up.
	InRetreat bool
	// Kind is what the route produces (see channelHealthKindOf). It sets the
	// cadence and what a successful reply must contain.
	Kind string
}

// channelProbeIntervalFor is the idle cadence of one kind of route.
func channelProbeIntervalFor(kind string) time.Duration {
	if kind == channelHealthKindImage {
		return channelProbeMediaInterval
	}
	return channelProbeInterval
}

// ChannelProbeSweepResult reports what one pass did, for logging and tests.
type ChannelProbeSweepResult struct {
	Considered   int
	Probed       int
	Skipped      int
	Failures     int
	BudgetHit    bool
	EstimatedUSD float64
}

// channelProbeDailyBudgetUSD reads the ceiling, allowing an operator to lower
// or raise it without a rebuild. Zero means probe nothing — that is the most
// direct way to switch synthetic spend off, so it must be honoured rather than
// read as "no limit". A negative value lifts the cap entirely.
func channelProbeDailyBudgetUSD() float64 {
	if raw := strings.TrimSpace(common.GetEnvOrDefaultString("CHANNEL_PROBE_DAILY_BUDGET_USD", "")); raw != "" {
		var parsed float64
		if _, err := fmt.Sscanf(raw, "%f", &parsed); err == nil {
			return parsed
		}
	}
	return channelProbeDefaultDailyBudgetUSD
}

// SelectChannelProbeCandidates decides which routes are due. It is separated
// from execution so the scheduling rules can be tested without touching an
// upstream.
func SelectChannelProbeCandidates(now time.Time) ([]ChannelProbeCandidate, error) {
	return selectChannelProbeCandidatesContext(context.Background(), now)
}

func SelectChannelProbeCandidatesContext(ctx context.Context, now time.Time) ([]ChannelProbeCandidate, error) {
	return selectChannelProbeCandidatesContext(model.ChannelHealthAnalysisContext(ctx), now)
}

func selectChannelProbeCandidatesContext(ctx context.Context, now time.Time) ([]ChannelProbeCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	grid, err := model.LoadChannelHealthGridContext(ctx)
	if err != nil {
		return nil, err
	}
	if len(grid) == 0 {
		return nil, nil
	}

	// Probe selection needs only the newest real observation per route. The old
	// implementation ran the full success/latency/token aggregate twice (one
	// hour and seven days) on every worker tick, making the scheduler's database
	// cost grow with the entire request population rather than its 40-probe cap.
	lastOutcomes, err := model.QueryChannelHealthLastOutcomesContext(ctx, now.Add(-channelProbeStaleAfter))
	if err != nil {
		return nil, err
	}
	lastTraffic := make(map[string]time.Time, len(lastOutcomes))
	for _, row := range lastOutcomes {
		if last := row.LastOutcomeAt.Ptr(); last != nil {
			lastTraffic[model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)] = *last
		}
	}

	lastProbe, err := model.LoadChannelProbeLastAttemptsContext(ctx, now.Add(-channelProbeStaleAfter))
	if err != nil {
		return nil, err
	}

	// Routes the breaker has pulled are the ones a probe is worth the most on:
	// they receive no traffic at all now, so nothing else can observe their
	// recovery.
	retreating := map[string]bool{}
	states, err := model.LoadRouteHealthStates()
	if err != nil {
		return nil, err
	}
	for _, state := range states {
		if state.IsRetreating(now) {
			retreating[model.ChannelHealthCellKey(state.ModelName, state.Lane, state.ChannelId)] = true
		}
	}

	kinds := channelHealthCatalogKinds()
	candidates := make([]ChannelProbeCandidate, 0, len(grid))
	neverProbed := time.Time{}
	for _, cell := range grid {
		if cell.ChannelStatus != common.ChannelStatusEnabled {
			continue
		}
		if cell.AbilityEnabled != nil && !*cell.AbilityEnabled {
			// A route the router will never pick is not worth a provider call.
			// The grid already filters these out; this guard keeps hand-built
			// grids and future grid changes from re-introducing paid probes on
			// dead routes.
			continue
		}
		key := model.ChannelHealthCellKey(cell.ModelName, cell.Lane, cell.ChannelId)
		inRetreat := retreating[key]
		kind := channelHealthKindOf(kinds, cell.ModelName)
		freshness := channelProbeTrafficFreshness
		if kind == channelHealthKindImage {
			freshness = channelProbeMediaInterval
		}
		if !inRetreat {
			if last, ok := lastTraffic[key]; ok && now.Sub(last) < freshness {
				continue
			}
		}

		idleFor := channelProbeStaleAfter
		if last, ok := lastTraffic[key]; ok {
			idleFor = now.Sub(last)
		}
		interval := channelProbeIntervalFor(kind)
		if inRetreat {
			interval = channelProbeRetreatInterval
		}
		if probedAt, ok := lastProbe[key]; ok && now.Sub(probedAt) < interval {
			continue
		}
		lastProbedAt := neverProbed
		if probedAt, ok := lastProbe[key]; ok {
			lastProbedAt = probedAt
		}
		candidates = append(candidates, ChannelProbeCandidate{
			Lane:         cell.Lane,
			ModelName:    cell.ModelName,
			ChannelId:    cell.ChannelId,
			ChannelName:  cell.ChannelName,
			IdleFor:      idleFor,
			LastProbedAt: lastProbedAt,
			InRetreat:    inRetreat,
			Kind:         kind,
		})
	}

	// Stalest evidence first. Every route idle past the stale threshold shares
	// one IdleFor value, so probe recency has to break that tie — ordering
	// that tier by name instead would let the per-sweep cap starve everything
	// after the first sweep's worth of cells indefinitely.
	sort.Slice(candidates, func(i, j int) bool {
		// A route in retreat is carrying no traffic at all, so its probe is
		// the only thing that can end the outage. It outranks mere staleness.
		if candidates[i].InRetreat != candidates[j].InRetreat {
			return candidates[i].InRetreat
		}
		if candidates[i].IdleFor != candidates[j].IdleFor {
			return candidates[i].IdleFor > candidates[j].IdleFor
		}
		if !candidates[i].LastProbedAt.Equal(candidates[j].LastProbedAt) {
			return candidates[i].LastProbedAt.Before(candidates[j].LastProbedAt)
		}
		if candidates[i].ModelName != candidates[j].ModelName {
			return candidates[i].ModelName < candidates[j].ModelName
		}
		return candidates[i].ChannelId < candidates[j].ChannelId
	})
	return capChannelProbeRetreats(candidates), nil
}

// capChannelProbeRetreats keeps a widespread outage from consuming the whole
// sweep. Recovery checks still come first; they just stop being the only thing
// that happens.
func capChannelProbeRetreats(candidates []ChannelProbeCandidate) []ChannelProbeCandidate {
	retreats := 0
	for _, candidate := range candidates {
		if candidate.InRetreat {
			retreats++
		}
	}
	if retreats <= channelProbeMaxRetreatsPerSweep {
		return candidates
	}
	kept := make([]ChannelProbeCandidate, 0, len(candidates))
	taken := 0
	for _, candidate := range candidates {
		if candidate.InRetreat {
			if taken >= channelProbeMaxRetreatsPerSweep {
				continue
			}
			taken++
		}
		kept = append(kept, candidate)
	}
	return kept
}

// RunChannelProbeSweep probes every due route, stopping at the per-sweep cap or
// the daily budget, whichever binds first.
func RunChannelProbeSweep(ctx context.Context, now time.Time) (ChannelProbeSweepResult, error) {
	result := ChannelProbeSweepResult{}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	budget := channelProbeDailyBudgetUSD()
	if budget == 0 {
		// Zero is the explicit off switch even when a provider contract is free.
		// Operators must be able to disable synthetic upstream calls without
		// relying on every cost row being positive.
		result.BudgetHit = true
		return result, nil
	}
	candidates, err := SelectChannelProbeCandidatesContext(ctx, now)
	if err != nil {
		return result, err
	}
	result.Considered = len(candidates)
	// Search backends are not in the model grid, so an empty model list must
	// not end the sweep before they are checked.
	defer func() {
		if result.Probed > 0 {
			// The dashboard caches aggregates for 30s; drop it so a freshly
			// probed cell is visible immediately after a sweep.
			InvalidateChannelHealthCache()
		}
	}()
	if len(candidates) == 0 {
		return result, runSearchProbeSweep(ctx, now, budget, &result)
	}

	for index, candidate := range candidates {
		// Shutdown must not wait out the remaining probes and their network
		// timeouts, which on an unreachable provider is minutes.
		select {
		case <-ctx.Done():
			result.Skipped += len(candidates) - index
			return result, nil
		default:
		}
		if index >= channelProbeMaxPerSweep {
			result.Skipped += len(candidates) - index
			break
		}
		claimKey := model.ChannelHealthCellKey(candidate.ModelName, candidate.Lane, candidate.ChannelId)
		claimToken := uuid.NewString()
		claimedAt := time.Now().UTC()
		claimed, err := model.ClaimChannelProbe(
			claimKey, claimToken, claimedAt, claimedAt.Add(channelProbeClaimLease()),
		)
		if err != nil {
			return result, err
		}
		if !claimed {
			// Another replica already owns this cell. Do not consume its budget or
			// contact the same provider while that request is in flight.
			result.Skipped++
			continue
		}
		releaseClaim := func() {
			if err := model.ReleaseChannelProbeClaim(claimKey, claimToken); err != nil {
				common.SysLog("channel probe: failed to release cell claim: " + err.Error())
			}
		}
		if channelProbeAlreadyFresh(candidate, claimedAt) {
			// Another replica probed this cell after our candidate list was
			// read and has already released it. Our list is stale; theirs
			// is the sample.
			releaseClaim()
			result.Skipped++
			continue
		}
		estimated := estimateProbeCostUSD(candidate.ModelName, candidate.Lane, candidate.ChannelId)
		if candidate.Kind == channelHealthKindImage && estimated < channelProbeImageCostUSD {
			// Token prices say nothing useful about one generated image.
			estimated = channelProbeImageCostUSD
		}
		// A sweep can cross UTC midnight while upstream requests are in flight.
		// Charge each reservation to the day on which it is actually made, not
		// to the sweep's scheduling timestamp, and retain that exact ledger key
		// if a pre-provider exit releases the reservation below.
		reservationDay := channelProbeUTCDate(time.Now())
		reserved, err := model.ReserveChannelProbeBudget(reservationDay, budget, estimated)
		if err != nil {
			releaseClaim()
			return result, err
		}
		if !reserved {
			releaseClaim()
			result.BudgetHit = true
			result.Skipped += len(candidates) - index
			common.SysLog(fmt.Sprintf(
				"channel probe: daily budget $%.4f reached, skipping %d remaining routes",
				budget, len(candidates)-index))
			break
		}

		outcome := probeChannelCell(ctx, candidate, estimated)
		if outcome.Skipped && outcome.CostUSD == 0 {
			// Only known zero-cost exits return their reservation. A canceled
			// in-flight provider request is skipped as health evidence, but may
			// still have incurred upstream spend and keeps its reservation.
			if err := model.ReleaseChannelProbeBudget(reservationDay, estimated); err != nil {
				common.SysLog("channel probe: failed to release unused budget: " + err.Error())
			}
		}
		if err := model.RecordChannelProbe(outcome); err != nil {
			common.SysLog("channel probe: failed to record result: " + err.Error())
		}
		recordChannelProbeBreakerEvidence(candidate.ModelName, candidate.Lane, candidate.ChannelId, outcome)
		releaseClaim()
		result.EstimatedUSD += outcome.CostUSD
		result.Probed++
		if outcome.Skipped {
			result.Skipped++
		} else if !outcome.Success {
			result.Failures++
		}
		select {
		case <-ctx.Done():
			result.Skipped += len(candidates) - index - 1
			return result, nil
		case <-time.After(channelProbeInterRequestDelay):
		}
	}

	return result, runSearchProbeSweep(ctx, now, budget, &result)
}

// channelProbeAlreadyFresh re-reads a claimed cell's newest attempt. The
// sweep's candidate list is read once, before any probe runs, and every
// replica sweeps; without this check the second replica to reach a cell
// probes it again the moment the first releases its claim.
func channelProbeAlreadyFresh(candidate ChannelProbeCandidate, now time.Time) bool {
	interval := channelProbeIntervalFor(candidate.Kind)
	if candidate.InRetreat {
		interval = channelProbeRetreatInterval
	}
	last, ok, err := model.LoadChannelProbeLastAttempt(
		candidate.ModelName, candidate.Lane, candidate.ChannelId, now.Add(-interval),
	)
	if err != nil {
		// Unknown is not fresh: a failed read must not silence the route.
		return false
	}
	return ok && now.Sub(last) < interval
}

// runSearchProbeSweep asks every enabled search backend the fixed probe
// question once per probe interval, unless customers have already sampled it.
// Search backends have no abilities, so they never appear in the model grid
// the main sweep walks.
func runSearchProbeSweep(ctx context.Context, now time.Time, budget float64, result *ChannelProbeSweepResult) error {
	var channels []*model.Channel
	if err := model.CoreDB().
		Where("type IN ? AND status = ?", constant.SearchChannelTypes(), common.ChannelStatusEnabled).
		Order("id asc").
		Find(&channels).Error; err != nil {
		return fmt.Errorf("search probe: list backends: %w", err)
	}
	if len(channels) == 0 {
		return nil
	}
	last, err := model.LoadSearchHealthLastSamples(now.Add(-channelProbeStaleAfter))
	if err != nil {
		return fmt.Errorf("search probe: read last samples: %w", err)
	}
	for _, channel := range channels {
		if ctx.Err() != nil {
			return nil
		}
		// A channel configured for fetch only never serves /v1/search; a
		// search probe would bill for an operation nobody routes to it.
		if !service.SearchChannelServesWebSearch(channel) {
			continue
		}
		if at, ok := last[channel.Id]; ok && now.Sub(at) < channelProbeInterval {
			continue
		}
		claimKey := fmt.Sprintf("search\x1f%d", channel.Id)
		claimToken := uuid.NewString()
		claimedAt := time.Now().UTC()
		claimed, err := model.ClaimChannelProbe(claimKey, claimToken, claimedAt, claimedAt.Add(channelProbeClaimLease()))
		if err != nil {
			return fmt.Errorf("search probe: claim backend %d: %w", channel.Id, err)
		}
		if !claimed {
			result.Skipped++
			continue
		}
		release := func() {
			if err := model.ReleaseChannelProbeClaim(claimKey, claimToken); err != nil {
				common.SysLog("search probe: failed to release claim: " + err.Error())
			}
		}
		// Re-read under the claim for the same reason the model sweep does.
		fresh, err := model.LoadSearchHealthLastSamples(claimedAt.Add(-channelProbeInterval))
		if err != nil {
			release()
			return fmt.Errorf("search probe: recheck backend %d: %w", channel.Id, err)
		}
		if at, ok := fresh[channel.Id]; ok && claimedAt.Sub(at) < channelProbeInterval {
			release()
			result.Skipped++
			continue
		}
		day := channelProbeUTCDate(time.Now())
		reserved, err := model.ReserveChannelProbeBudget(day, budget, channelProbeSearchCostUSD)
		if err != nil {
			release()
			return fmt.Errorf("search probe: reserve backend %d: %w", channel.Id, err)
		}
		if !reserved {
			release()
			result.BudgetHit = true
			return nil
		}
		sample, attempted := service.ProbeSearchBackend(ctx, channel)
		if !attempted {
			if err := model.ReleaseChannelProbeBudget(day, channelProbeSearchCostUSD); err != nil {
				release()
				return fmt.Errorf("search probe: release unused budget for backend %d: %w", channel.Id, err)
			}
			release()
			result.Skipped++
			continue
		}
		if ctx.Err() != nil {
			// Shutdown, not evidence.
			release()
			return nil
		}
		if err := model.RecordSearchHealthSample(sample); err != nil {
			release()
			return fmt.Errorf("search probe: record backend %d: %w", channel.Id, err)
		}
		release()
		result.Probed++
		result.EstimatedUSD += channelProbeSearchCostUSD
		if !sample.Success {
			result.Failures++
		}
	}
	return nil
}

// probeChannelCell runs one synthetic request against a specific model on a
// specific channel. Unlike the channel-level auto test, it never disables a
// channel: a probe failure is evidence for the dashboard, not grounds to take
// a route out of service on a single sample.
func probeChannelCell(ctx context.Context, candidate ChannelProbeCandidate, estimatedUSD float64) model.ChannelProbeResult {
	outcome := model.ChannelProbeResult{
		Lane:      candidate.Lane,
		ModelName: candidate.ModelName,
		ChannelId: candidate.ChannelId,
		ProbedAt:  time.Now().UTC(),
	}

	channel, err := model.GetChannelById(candidate.ChannelId, true)
	if err != nil || channel == nil {
		outcome.ErrorMessage = "channel unavailable"
		outcome.Skipped = true
		return outcome
	}
	if constant.IsSearchChannelType(channel.Type) {
		outcome.ErrorMessage = "search channel is not probeable"
		outcome.Skipped = true
		return outcome
	}

	if channel.GetOtherSettings().CodexClientOnly {
		outcome.ErrorMessage = "Codex-only source requires a real Codex session"
		outcome.Skipped = true
		return outcome
	}
	endpointType, ok := probeEndpointType(channel, candidate.ModelName)
	if !ok {
		outcome.ErrorMessage = "model has no probeable endpoint"
		outcome.Skipped = true
		return outcome
	}

	outcome.Modality = channelProbeModality(endpointType)

	started := time.Now()
	// recordConsumption is false: a probe must never appear in the consume log,
	// or hourly health checks would be attributed to user 1 as customer spend
	// and would skew usage exports and model popularity counters.
	testResult := testChannelForLane(ctx, channel, candidate.ModelName, endpointType, candidate.Lane, false)
	outcome.LatencyMs = int(time.Since(started).Milliseconds())
	outcome.CostUSD = estimatedUSD
	return finalizeChannelProbeResult(ctx, outcome, testResult)
}

// channelProbeModality names what a probe's endpoint produces, which is what
// decides how its reply is checked and whether its duration is a latency
// sample of the lane.
func channelProbeModality(endpointType string) string {
	switch constant.EndpointType(endpointType) {
	case constant.EndpointTypeDecisions:
		return channelHealthKindDecision
	case constant.EndpointTypeEmbeddings:
		return channelHealthKindEmbedding
	case constant.EndpointTypeImageGeneration:
		return channelHealthKindImage
	case constant.EndpointTypeJinaRerank:
		return channelHealthKindRerank
	default:
		return "text"
	}
}

// recordChannelProbeBreakerEvidence feeds a probe's verdict to the route's
// breaker. A skip says nothing about the upstream, so only real verdicts move
// it; a success is what ends a retreat.
func recordChannelProbeBreakerEvidence(modelName, lane string, channelID int, outcome model.ChannelProbeResult) {
	if outcome.Skipped {
		return
	}
	if err := model.RecordRouteHealthEvidence(
		modelName, lane, channelID, outcome.Success, outcome.ErrorMessage, outcome.ProbedAt,
	); err != nil {
		common.SysLog("channel probe: failed to record route health: " + err.Error())
	}
}

func finalizeChannelProbeResult(ctx context.Context, outcome model.ChannelProbeResult, testResult testResult) model.ChannelProbeResult {
	if err := ctx.Err(); err != nil {
		// Scheduler shutdown is not provider evidence. An in-flight request can
		// still have reached the adaptor before cancellation, so this check must
		// precede the normal provider-attempt classification.
		outcome.ErrorMessage = err.Error()
		outcome.Skipped = true
		return outcome
	}

	switch {
	case !testResult.providerAttempted && (testResult.localErr != nil || testResult.lazuErr != nil):
		// Request construction, mapping, pricing and conversion all happen
		// before adaptor.DoRequest. A failure there is local deployment evidence,
		// not a provider outage, even when the helper wraps it as a LazuError.
		if testResult.localErr != nil {
			outcome.ErrorMessage = testResult.localErr.Error()
		} else {
			outcome.ErrorMessage = testResult.lazuErr.Error()
		}
		outcome.CostUSD = 0
		outcome.Skipped = true
	case testResult.lazuErr != nil:
		// A relay error is the upstream's verdict on this route, and it is
		// real health evidence. This case has to come first: the relay
		// populates localErr as well as lazuErr on upstream failures, so
		// testing localErr first would file every provider outage as "never
		// reached the upstream" and quietly drop it.
		outcome.ErrorMessage = testResult.lazuErr.Error()
		if testResult.streamed && testResult.ttftMs > 0 {
			// The reply had started when it broke. The first-token time is
			// kept so the lane can count this as an interruption; the health
			// queries never read a failed probe's timings as latency.
			outcome.TTFTMs = testResult.ttftMs
		}
	case testResult.localErr != nil:
		// Only reachable when lazuErr is nil: the probe genuinely never left
		// this process — a bad relay context, missing metadata. That is a fact
		// about this deployment, not about the route, so it must not count as
		// a failed liveness check.
		outcome.ErrorMessage = testResult.localErr.Error()
		outcome.CostUSD = 0
		outcome.Skipped = true
	case outcome.Modality == channelHealthKindImage && !probeReplyHasImage(testResult.body):
		// A 200 that carries no decodable image is the failure image routes
		// actually have: a proxy that returns an empty data array, a URL to
		// nothing, a truncated base64 blob.
		outcome.ErrorMessage = "image response carried no decodable image"
	case outcome.Modality == channelHealthKindEmbedding && !probeReplyHasEmbedding(testResult.body):
		outcome.ErrorMessage = "embedding response carried no usable vector"
	case outcome.Modality == channelHealthKindRerank && !probeReplyHasRanking(testResult.body):
		outcome.ErrorMessage = "rerank response carried no ranked results"
	case outcome.Modality == channelHealthKindDecision && !probeReplyHasDecision(testResult.body):
		outcome.ErrorMessage = "decision response failed the known red-apple judgment"
	case testResult.streamed && testResult.ttftMs == 0 && testResult.outputTokens == 0:
		// A 200 stream that delivered nothing — metadata frames and a close —
		// is the same failure the traffic query counts for customers. A reply
		// whose hidden reasoning used the whole cap still generated tokens and
		// is not this case.
		outcome.ErrorMessage = "stream produced no output"
		if testResult.durationMs > 0 {
			outcome.LatencyMs = testResult.durationMs
		}
	default:
		outcome.Success = true
		// Prefer the upstream exchange's own duration: the outer clock also
		// covers local setup, which is not the route's latency.
		if testResult.durationMs > 0 {
			outcome.LatencyMs = testResult.durationMs
		}
		if testResult.streamed {
			outcome.TTFTMs = testResult.ttftMs
			outcome.MaxStallMs = testResult.maxStallMs
			outcome.OutputTokens = testResult.outputTokens
			outcome.AnswerTTFTMs = testResult.answerTtftMs
			outcome.ReasoningTokens = testResult.reasoningTokens
		}
	}
	return outcome
}

// probeEndpointType picks the endpoint a model actually serves, and reports
// whether the model is probeable at all.
//
// Media models list a chat endpoint alongside their real one, so preferring
// chat would send a chat request to an image model and record the upstream's
// entirely correct rejection as a health failure. Image models are therefore
// probed on their image endpoint, on their own slower cadence; video models,
// which have no probe request, are not probed at all.
func probeEndpointType(channel *model.Channel, modelName string) (string, bool) {
	if channel == nil {
		return "", false
	}
	// Video execution and audio-only routes have no probe request builders.
	// Their channel type may still expose generic OpenAI chat; detect the model
	// before that fallback can manufacture a false liveness failure from a chat
	// payload that the provider is correct to reject.
	if common.IsVideoGenerationModel(modelName) || isAudioOnlyProbeModel(modelName) {
		return "", false
	}
	// Catalog support is a union across every channel serving a model, so it is
	// unsuitable for a request pinned to one candidate. Start from the concrete
	// channel protocol, then add only model-level endpoint overrides: those are
	// an explicit request-shape contract and are not polluted by other abilities.
	endpointTypes := common.GetEndpointTypesByChannelType(channel.Type, modelName)
	endpointTypes = append(endpointTypes, model.GetModelEndpointOverrideTypes(modelName)...)
	return selectProbeEndpointType(modelName, endpointTypes)
}

func isAudioOnlyProbeModel(modelName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	return strings.HasPrefix(normalized, "whisper-") ||
		strings.HasPrefix(normalized, "tts-") ||
		strings.Contains(normalized, "-transcribe") ||
		strings.Contains(normalized, "-tts") ||
		strings.Contains(normalized, "speech-to-text") ||
		strings.Contains(normalized, "text-to-speech")
}

func channelProbeUTCDate(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}

func selectProbeEndpointType(modelName string, endpointTypes []constant.EndpointType) (string, bool) {
	if len(endpointTypes) == 0 {
		return "", false
	}
	available := map[string]bool{}
	for _, endpointType := range endpointTypes {
		available[string(endpointType)] = true
	}
	if common.IsDecisionModel(modelName) || available[string(constant.EndpointTypeDecisions)] {
		return string(constant.EndpointTypeDecisions), true
	}
	// Video execution has no probe request builder. Image generation is
	// probed as an image — never as chat: media models list a chat endpoint
	// alongside their real one, and a chat request to an image model earns a
	// correct rejection that would be recorded as an outage.
	if available[string(constant.EndpointTypeVideoGeneration)] {
		return "", false
	}
	if available[string(constant.EndpointTypeImageGeneration)] {
		return string(constant.EndpointTypeImageGeneration), true
	}
	// An explicitly advertised rerank endpoint is narrower and more reliable
	// than name inference. In particular, bge-reranker-* also matches the broad
	// BGE embedding heuristic below, but must be probed with a rerank payload.
	if available[string(constant.EndpointTypeJinaRerank)] {
		return string(constant.EndpointTypeJinaRerank), true
	}
	// Generic OpenAI-compatible channels advertise only their broad chat and
	// Responses capabilities even for embedding-only abilities. Infer the
	// narrower request shape from the model identity before endpoint priority;
	// requiring embeddings to be advertised here would simply select chat again.
	openAICompatible := available[string(constant.EndpointTypeOpenAI)] ||
		available[string(constant.EndpointTypeOpenAIResponse)] ||
		available[string(constant.EndpointTypeEmbeddings)]
	if looksLikeEmbeddingModel(modelName) && openAICompatible {
		return string(constant.EndpointTypeEmbeddings), true
	}
	for _, candidate := range []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeAnthropic,
		constant.EndpointTypeGemini,
		constant.EndpointTypeEmbeddings,
	} {
		if available[string(candidate)] {
			return string(candidate), true
		}
	}
	// Anything left serves only endpoints this probe cannot construct a
	// meaningful request for. Falling back to autodetection would guess wrong
	// and report a false outage, so skip it.
	return "", false
}

func looksLikeEmbeddingModel(modelName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	return strings.Contains(normalized, "embedding") ||
		strings.Contains(normalized, "embed") ||
		strings.HasPrefix(normalized, "m3e") ||
		strings.Contains(normalized, "bge-")
}

// estimateProbeCostUSD prices one probe from the selected channel's provider
// cost sheet. Without one it falls back to the lane's sell price — which sits
// above cost — and only then to a flat pessimistic rate, so a bookkeeping gap
// never becomes a reason not to look at a route.
func estimateProbeCostUSD(modelName string, lane string, channelID int) float64 {
	// A reasoning probe may use its whole, larger cap, and the reservation
	// has to cover that or the daily ceiling stops meaning anything.
	outputTokens := ChannelProbeMaxOutputTokens
	if probeReasoningEffort(modelName) != "" {
		outputTokens = probeReasoningCap
	}
	if pricing, ok := model.GetActiveProviderCostPrice(modelName, channelID); ok {
		return probeCostFromPrices(pricing.RequestUSD, pricing.InputPerMtok, pricing.OutputPerMtok, outputTokens)
	}
	if sell, ok := model.GetActiveModelSellPrice(modelName, lane); ok {
		return probeCostFromPrices(sell.RequestUSD, sell.InputPerMtok, sell.OutputPerMtok, outputTokens)
	}
	return channelProbeFallbackCostUSD
}

func probeCostFromPrices(requestUSD, inputPerMtok, outputPerMtok *float64, outputTokens int) float64 {
	// A per-call charge applies whatever the token counts are, so a
	// request-priced model would otherwise estimate at zero and walk straight
	// past the daily ceiling.
	cost := derefPrice(requestUSD) +
		float64(channelProbeInputTokens)*derefPrice(inputPerMtok)/1_000_000 +
		float64(outputTokens)*derefPrice(outputPerMtok)/1_000_000
	if cost < 0 {
		return 0
	}
	return cost
}

func derefPrice(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

// probeReplyHasImage reports whether an image-generation reply carries at
// least one image: a base64 payload whose header decodes as an image, or a
// URL. The header is enough; the pixels are the upstream's business.
func probeReplyHasImage(body []byte) bool {
	var reply struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if err := common.Unmarshal(body, &reply); err != nil {
		return false
	}
	for _, item := range reply.Data {
		if strings.TrimSpace(item.URL) != "" {
			return true
		}
		if item.B64JSON == "" {
			continue
		}
		// JPEG metadata can precede the frame header by many kilobytes.
		// Decode the complete base64 payload so a valid image is not rejected
		// by an arbitrary prefix limit, and malformed suffixes are not hidden.
		decoded, err := base64.StdEncoding.DecodeString(item.B64JSON)
		if err != nil {
			continue
		}
		if _, _, err := image.DecodeConfig(bytes.NewReader(decoded)); err == nil {
			return true
		}
	}
	return false
}

// probeReplyHasEmbedding reports whether an embedding reply carries a
// non-empty vector with at least one non-zero component. An all-zero vector
// is what a broken proxy returns when it fills the shape and not the content.
func probeReplyHasEmbedding(body []byte) bool {
	var reply struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := common.Unmarshal(body, &reply); err != nil {
		return false
	}
	for _, item := range reply.Data {
		for _, component := range item.Embedding {
			if component != 0 {
				return true
			}
		}
	}
	return false
}

// probeReplyHasRanking reports whether a rerank reply ranked at least one of
// the documents it was sent.
func probeReplyHasRanking(body []byte) bool {
	var reply struct {
		Results []struct {
			Index          *int     `json:"index"`
			RelevanceScore *float64 `json:"relevance_score"`
		} `json:"results"`
	}
	if err := common.Unmarshal(body, &reply); err != nil {
		return false
	}
	for _, result := range reply.Results {
		if result.Index != nil && result.RelevanceScore != nil {
			return true
		}
	}
	return false
}
