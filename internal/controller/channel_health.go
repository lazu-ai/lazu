package controller

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
)

// Channel health is the one place that turns request facts into the stability
// numbers both the model plaza and the console health page show. Both surfaces
// read the same aggregate so they cannot disagree.
//
// A row answers two questions from two places. Whether a lane works now is
// the router's breaker state (route_health_state) over the upstreams it would
// choose between. How a lane behaves is every sample in the window — customer
// requests and the streamed probes sent to idle routes, counted alike — over
// the routes serving it today. Rows are the catalog's models × the lanes some
// enabled route serves, whatever the window contains.

const (
	channelHealthSourceTraffic = "traffic"
	channelHealthSourceProbe   = "probe"
	channelHealthSourceNone    = "none"
	// channelHealthSourceBlocked marks a route we are structurally unable to
	// check: no upstream price, no probeable endpoint, a client-only source.
	// It is a distinct source because the honest answer for these cells is
	// "we cannot see", which is a fact about our own setup and actionable,
	// while "no data" is a fact about the route and usually is not.
	channelHealthSourceBlocked = "blocked"

	// channelHealthToneBlocked colours a cell we were structurally unable to
	// check. It is not a severity — it says nothing about the upstream — so it
	// loses to every real measurement when sources are compared.
	channelHealthToneBlocked = "blocked"

	// channelHealthBucketCount is the day window's density: hourly slices,
	// which the catalog compacts into its own three-hour marks. The console
	// reads the week, drawn status-page style in two-hour slices — close to
	// the ninety bars people know from public status pages, and wide enough
	// that the 45-minute probe cadence lands at least twice in each.
	channelHealthBucketCount     = 24
	channelHealthWideBucketCount = 84
	// The model plaza draws the same week as the health page: 84 two-hour
	// slices, so a lane's bars and uptime read identically in both places.
	catalogHealthBucketCount = channelHealthWideBucketCount
	catalogHealthWindow      = 7 * 24 * time.Hour
	// channelHealthCacheTTL is how long one process reuses a health view it
	// built from the hourly route health rows. Lane status is overlaid from
	// the breaker on every read, so a cached view never hides a route that
	// went down a moment ago.
	channelHealthCacheTTL = 60 * time.Second

	// A slice is coloured warn below this success rate and bad below the
	// outage line when all repeated samples failed. These mirror
	// the admin health thresholds. Lane status does not use them: it is the
	// breaker's verdict, not a percentage.
	channelHealthDegradedRate = healthAvailabilityDegraded
	channelHealthOutageRate   = healthAvailabilityOutage
)

type channelHealthBucketView struct {
	Index        int       `json:"index"`
	StartAt      time.Time `json:"start_at"`
	Source       string    `json:"source"`
	RequestCount int64     `json:"request_count"`
	ErrorCount   int64     `json:"error_count"`
	SuccessRate  *float64  `json:"success_rate,omitempty"`
	Tone         string    `json:"tone"`
	// decidedBy is the upstream whose evidence currently owns this cell. It
	// only breaks ties between otherwise identical verdicts, so the wall is
	// stable across refreshes rather than flickering between equal sources.
	decidedBy int
}

type channelHealthSourceView struct {
	ChannelID int `json:"channel_id"`
	// LastEvidenceAt is when this route last produced a sample of either kind.
	LastEvidenceAt *time.Time `json:"last_evidence_at,omitempty"`
	ChannelName    string     `json:"channel_name"`
	Status         string     `json:"status"`
	Source         string     `json:"source"`
	Weight         uint       `json:"weight"`
	// RequestCount is real traffic and ProbeCount synthetic probes. They are
	// measured identically; the split exists so an operator can see how much
	// of a route's record is customers and how much is us.
	RequestCount int64    `json:"request_count"`
	ProbeCount   int64    `json:"probe_count"`
	SuccessRate  *float64 `json:"success_rate,omitempty"`
	AvgLatencyMs *float64 `json:"avg_latency_ms,omitempty"`
	Share        *float64 `json:"share,omitempty"`
	Disabled     bool     `json:"disabled"`
	// Role is what this upstream is for on the route: primary carries traffic
	// now, backup stands by, inventory is imported but parked.
	Role string `json:"role,omitempty"`
	// Retreating means the breaker has taken this route out of rotation, and
	// RetreatUntil is when it will be tried again. Together they are the
	// difference between "this source is broken" and "this source is broken
	// and we have already stopped sending anyone to it".
	Retreating   bool       `json:"retreating,omitempty"`
	RetreatUntil *time.Time `json:"retreat_until,omitempty"`
	// AbilityModel is the routed name of this route when it differs from the
	// row's catalog name — an aliased variant. Admin writes to the route must
	// carry it, since abilities are stored under it.
	AbilityModel string `json:"ability_model,omitempty"`
	// ConsecutiveFailures is the breaker's running count for this route: how
	// many samples in a row have failed since the last success.
	ConsecutiveFailures int `json:"consecutive_failures,omitempty"`
	// BlockedReason is why this route cannot be probed at all. It describes a
	// gap in our own configuration, so it is reported verbatim to the operator
	// who can close it.
	BlockedReason string `json:"blocked_reason,omitempty"`

	// The window's raw totals, kept private and summed by the lane and the
	// provider folds. Ratios are always divided once from totals.
	probeErrors   int64
	trafficErrors int64
	durationSumMs int64
	durationCount int64
	// Successful traffic samples with a measured duration decide whether
	// customer latency has enough observations to replace probe latency.
	trafficDurationCount int64
	outputTokens         int64
	decodeMs             int64
	interrupted          int64
	streamed             int64
	lastTrafficAt        *time.Time
	lastProbedAt         *time.Time
	excludedFromCurrent  bool
	// uncounted marks a current route the lane leaves out of its figures: a
	// parked or idle standby. See channelHealthCountsTowardLane.
	uncounted bool
	// Provider attempts a retry superseded on this upstream. The upstream's
	// own figures include them; its lane, which describes the answers
	// customers got, subtracts them again.
	attempts             int64
	attemptErrors        int64
	attemptDurationSumMs int64
	attemptDurationCount int64
}

func (s *channelHealthSourceView) samples() int64 {
	return s.RequestCount + s.ProbeCount
}

func (s *channelHealthSourceView) sampleErrors() int64 {
	return s.trafficErrors + s.probeErrors
}

type channelHealthLaneView struct {
	ModelName string `json:"model_name"`
	Lane      string `json:"lane"`
	// Maker and ReleaseDate place the row in the catalog's display order;
	// they are filled at read time so a maker reorder shows immediately.
	Maker       string `json:"maker,omitempty"`
	MakerRank   int    `json:"maker_rank"`
	ReleaseDate string `json:"release_date,omitempty"`
	// Kind is what the row's model produces — llm, embedding, image, search,
	// and later audio and video. Each kind is measured by its own figures and
	// drawn in its own section, so an image model is never judged by a
	// first-token time it cannot have.
	Kind string `json:"kind"`
	// Provider is a search row's backend name as /v1/search accepts it
	// (tavily, serper …); the row's model_name is its display label.
	Provider string `json:"provider,omitempty"`
	// LaneSlug is the lane's real registry name, which Lane is not: the
	// customer-facing key collapses every non-agent lane to "stable" so the
	// product only ever shows two. Any admin action that writes back to a
	// route has to carry the true slug, or an operator-created lane's source
	// would be edited as if it were 稳定. Admin-only, like the actions.
	LaneSlug     string   `json:"lane_slug,omitempty"`
	Status       string   `json:"status"`
	Source       string   `json:"source"`
	RequestCount int64    `json:"request_count"`
	ErrorCount   int64    `json:"error_count"`
	SuccessRate  *float64 `json:"success_rate,omitempty"`
	AvgLatencyMs *float64 `json:"avg_latency_ms,omitempty"`
	P95LatencyMs *float64 `json:"p95_latency_ms,omitempty"`
	TTFTP50Ms    *float64 `json:"ttft_p50_ms,omitempty"`
	// TTFTP95Ms is the slow tail of the first token, which unlike total
	// duration does not grow with how much the caller asked for.
	TTFTP95Ms *float64 `json:"ttft_p95_ms,omitempty"`
	// LatencyP50Ms and LatencyP95Ms rank whole-request duration over every
	// sample. They are the speed figures of kinds without a stream —
	// embeddings, images, search — where a request has one shape whoever
	// sends it.
	LatencyP50Ms *float64 `json:"latency_p50_ms,omitempty"`
	LatencyP95Ms *float64 `json:"latency_p95_ms,omitempty"`
	// StallP50Ms is the median longest gap between two streamed chunks. A lane
	// can hold a respectable TTFT and still read as broken when it goes quiet
	// for eight seconds in the middle of an answer, and no average catches that.
	StallP50Ms *float64 `json:"stall_p50_ms,omitempty"`
	OutputTPS  *float64 `json:"output_tps,omitempty"`
	// InterruptionRate is streamed replies that broke after output had
	// started, over streamed replies that started at all. A reply that never
	// started is already an availability failure; this is the other way a
	// stream fails a reader — halfway through a sentence.
	//
	// Cache-hit and truncation rates used to sit here. Both describe what the
	// callers sent — whether they reused a prefix, what output ceiling they
	// set — rather than the lane, so they left the health page.
	InterruptionRate *float64 `json:"interruption_rate,omitempty"`
	// InputPerMtok and OutputPerMtok are this lane's published list price, put
	// beside its behaviour so the two are read together: a lane's numbers only
	// mean something next to what it costs.
	InputPerMtok  *float64 `json:"input_per_mtok,omitempty"`
	OutputPerMtok *float64 `json:"output_per_mtok,omitempty"`
	// OutputTokens and DecodeMs are the raw inputs behind OutputTPS. They are
	// published so any consumer aggregating several lanes divides totals once
	// rather than averaging ratios, which would weight a short-response lane
	// the same as a long-response one.
	OutputTokens  int64      `json:"output_tokens"`
	DecodeMs      int64      `json:"decode_ms"`
	LastOutcomeAt *time.Time `json:"last_outcome_at,omitempty"`
	LastProbedAt  *time.Time `json:"last_probed_at,omitempty"`
	// ProbeCount and ProbeErrorCount are the synthetic half of the window's
	// samples; RequestCount and ErrorCount are the customer half. SuccessRate
	// and the latency figures are taken over both. Admin-only: how much of a
	// lane's record is our own probing is an operations fact.
	ProbeCount      int64                     `json:"probe_count,omitempty"`
	ProbeErrorCount int64                     `json:"probe_error_count,omitempty"`
	ChannelCount    int                       `json:"channel_count,omitempty"`
	Buckets         []channelHealthBucketView `json:"buckets"`
	Sources         []channelHealthSourceView `json:"sources,omitempty"`
	// FallbackActive means every primary on this route is out of rotation and
	// a standby is carrying the lane. Admin-only: to a customer this is an
	// implementation detail of a lane that is, correctly, still working.
	FallbackActive bool `json:"fallback_active,omitempty"`
	// ServingCount is how many upstreams the router would currently choose
	// between. Also admin-only — telling customers how thin our supply is
	// invites exactly the question the lane abstraction exists to avoid.
	ServingCount int `json:"serving_count,omitempty"`
	// BlockedCount is how many of this route's upstreams cannot be checked at
	// all, which is a gap in our instrumentation rather than an outage.
	BlockedCount int `json:"blocked_count,omitempty"`
	// Stored in materialized snapshots for exact public-lane aggregation.
	// Removed before either member or admin HTTP responses are serialized.
	AggregateTotals *channelHealthAggregateTotals `json:"aggregate_totals,omitempty"`
}

// channelHealthProviderLaneView is one route an upstream serves, seen from the
// upstream's side. Share is the portion of that lane's traffic this channel
// carried, which is the number that answers "if I take this source out, what
// moves?".
type channelHealthProviderLaneView struct {
	ModelName    string   `json:"model_name"`
	Lane         string   `json:"lane"`
	Status       string   `json:"status"`
	Source       string   `json:"source"`
	SuccessRate  *float64 `json:"success_rate,omitempty"`
	RequestCount int64    `json:"request_count"`
	Share        *float64 `json:"share,omitempty"`
	Weight       uint     `json:"weight"`
	Disabled     bool     `json:"disabled"`
}

// channelHealthLastProbeView reports the newest synthetic check of an upstream
// verbatim. An idle source has no traffic to speak for it, so the probe's own
// timestamp, verdict, latency and error are the whole of the evidence.
type channelHealthLastProbeView struct {
	ProbedAt  time.Time `json:"probed_at"`
	ModelName string    `json:"model_name,omitempty"`
	Lane      string    `json:"lane,omitempty"`
	Success   bool      `json:"success"`
	LatencyMs int       `json:"latency_ms,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// channelHealthProviderView is the same window aggregated by upstream instead
// of by lane. Admin-only, for the same reason the per-lane source breakdown is:
// which vendor served what is an operations fact, not a product one.
//
// It is a projection of the identical evidence, never a second measurement, so
// a provider and the lanes it serves can never disagree.
type channelHealthProviderView struct {
	ChannelID   int    `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	SourceType  string `json:"source_type"`
	ChannelType int    `json:"channel_type"`
	Status      string `json:"status"`
	Source      string `json:"source"`
	Disabled    bool   `json:"disabled"`
	// Weight is the highest weight this channel holds on any route it serves.
	// Weights are per (model, lane, channel), so one number can only be a
	// headline; the per-route weights stay on Lanes below.
	Weight       uint     `json:"weight"`
	RequestCount int64    `json:"request_count"`
	ErrorCount   int64    `json:"error_count"`
	SuccessRate  *float64 `json:"success_rate,omitempty"`
	AvgLatencyMs *float64 `json:"avg_latency_ms,omitempty"`
	P95LatencyMs *float64 `json:"p95_latency_ms,omitempty"`
	OutputTPS    *float64 `json:"output_tps,omitempty"`
	// Share is this channel's portion of all traffic in the window, so the
	// cards can be read against each other at a glance.
	Share         *float64                        `json:"share,omitempty"`
	ModelCount    int                             `json:"model_count"`
	LaneCount     int                             `json:"lane_count"`
	ProbeCount    int64                           `json:"probe_count"`
	LastOutcomeAt *time.Time                      `json:"last_outcome_at,omitempty"`
	LastProbedAt  *time.Time                      `json:"last_probed_at,omitempty"`
	LastProbe     *channelHealthLastProbeView     `json:"last_probe,omitempty"`
	Buckets       []channelHealthBucketView       `json:"buckets"`
	Lanes         []channelHealthProviderLaneView `json:"lanes"`
}

type channelHealthSummaryView struct {
	StableSuccessRate *float64 `json:"stable_success_rate,omitempty"`
	EcoSuccessRate    *float64 `json:"eco_success_rate,omitempty"`
	OutputTPS         *float64 `json:"output_tps,omitempty"`
	ActiveIssues      int      `json:"active_issues"`
	DegradedCount     int      `json:"degraded_count"`
	OutageCount       int      `json:"outage_count"`
	NoDataCount       int      `json:"no_data_count"`
	LaneCount         int      `json:"lane_count"`
	ModelCount        int      `json:"model_count"`
	RequestCount      int64    `json:"request_count"`
	// Provider counts are admin-only and stay zero for members, who never
	// receive the provider projection in the first place.
	ProviderCount         int `json:"provider_count,omitempty"`
	ProviderDegradedCount int `json:"provider_degraded_count,omitempty"`
	ProviderOutageCount   int `json:"provider_outage_count,omitempty"`
	// FallbackCount is how many routes are currently being carried by a
	// standby, and BlockedCount how many upstreams we cannot check at all.
	// Both are work queues for the operator rather than service status.
	FallbackCount int `json:"fallback_count,omitempty"`
	BlockedCount  int `json:"blocked_count,omitempty"`
}

type channelHealthResponse struct {
	Window        string                   `json:"window"`
	WindowSeconds int64                    `json:"window_seconds"`
	BucketCount   int                      `json:"bucket_count"`
	BucketSeconds int64                    `json:"bucket_seconds"`
	IsAdmin       bool                     `json:"is_admin"`
	Summary       channelHealthSummaryView `json:"summary"`
	// Kinds lists the sections present, in display order, each with the
	// slice width its wall is drawn in. Image generation is sampled hours
	// apart, so its wall uses slices three times as wide as everything else.
	Kinds []channelHealthKindView `json:"kinds"`
	Rows  []channelHealthLaneView `json:"rows"`
	// Providers is the by-upstream projection of the same window. It is
	// omitted entirely for non-admins rather than emptied, so a member's
	// payload carries no trace of the upstream dimension.
	Providers []channelHealthProviderView `json:"providers,omitempty"`
	UpdatedAt time.Time                   `json:"updated_at"`
}

// channelHealthSnapshot is the un-projected aggregate. It is cached per window
// and then filtered per caller, so a user request never triggers its own scan.
type channelHealthSnapshot struct {
	windowSeconds int64
	bucketSeconds int64
	bucketCount   int
	since         time.Time
	rows          []channelHealthLaneView
	providers     []channelHealthProviderView
	updatedAt     time.Time
}

var channelHealthCache = struct {
	sync.Mutex
	items map[string]channelHealthSnapshot
	// building serialises rebuilds per window so a burst of requests arriving
	// on a cold cache runs one scan between them rather than one each.
	building map[string]*sync.Mutex
}{
	items:    map[string]channelHealthSnapshot{},
	building: map[string]*sync.Mutex{},
}

// parseChannelHealthWindow maps the UI's window chips onto a duration. There
// are two: a day, aggregated on demand, and a week, served from a
// worker-maintained snapshot. An hour holds a single probe per idle route and
// a month answers a reporting question this page is not for, so anything else
// reads as a day.
func parseChannelHealthWindow(raw string) (string, time.Duration) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "7d":
		return "7d", 7 * 24 * time.Hour
	default:
		return "24h", 24 * time.Hour
	}
}

func GetChannelHealth(c *gin.Context) {
	windowLabel, window := parseChannelHealthWindow(c.Query("window"))
	snapshot, err := loadChannelHealthSnapshot(windowLabel, window)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	// Status is the router's verdict now, and a snapshot can be most of an
	// hour old; the breaker table is small and always current.
	if states, err := model.LoadRouteHealthStates(); err == nil {
		snapshot = overlayChannelHealthBreaker(snapshot, states, time.Now().UTC())
	} else {
		common.SysLog("channel health: breaker overlay skipped: " + err.Error())
	}

	snapshot, err = overlayChannelHealthSearch(snapshot, time.Now().UTC())
	if err != nil {
		common.SysLog("channel health: search overlay skipped: " + err.Error())
		// Only search lost its current evidence. Keep the usable historical
		// snapshot and model lanes, without claiming stale search is healthy.
		rows := append([]channelHealthLaneView(nil), snapshot.rows...)
		for i := range rows {
			if rows[i].Kind != channelHealthKindSearch {
				continue
			}
			rows[i].Status = publicStatusUnknown
			rows[i].Sources = append([]channelHealthSourceView(nil), rows[i].Sources...)
			for j := range rows[i].Sources {
				rows[i].Sources[j].Status = publicStatusUnknown
			}
		}
		snapshot.rows = rows
	}

	isAdmin := c.GetInt("role") >= common.RoleRootUser
	rows, err := projectChannelHealthRows(c, snapshot.rows, isAdmin)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	summary := summarizeChannelHealth(rows)
	// Admin projection may share the snapshot's row slice. Copy before removing
	// snapshot-only aggregation inputs so a later member read still has them.
	responseRows := make([]channelHealthLaneView, len(rows))
	copy(responseRows, rows)
	rows = responseRows
	for i := range rows {
		rows[i].AggregateTotals = nil
	}
	orderChannelHealthRows(rows)
	if !isAdmin {
		// The summary above counts probes as samples, as the rows do; the
		// member payload then drops every trace of which samples were ours
		// and how many there were. projectChannelHealthRows returned copies,
		// so this cannot reach the cached snapshot.
		for i := range rows {
			rows[i] = neutralizeChannelHealthProvenance(rows[i])
		}
		summary.RequestCount = 0
	}
	// Members never see the upstream dimension, so both the projection and its
	// counters stay off their payload rather than being sent as zeroes.
	var providers []channelHealthProviderView
	if isAdmin {
		providers = snapshot.providers
		summary.ProviderCount = len(providers)
		for _, provider := range providers {
			switch provider.Status {
			case publicStatusDegraded:
				summary.ProviderDegradedCount++
			case publicStatusOutage:
				summary.ProviderOutageCount++
			}
		}
	}

	common.ApiSuccess(c, channelHealthResponse{
		Window:        windowLabel,
		WindowSeconds: snapshot.windowSeconds,
		BucketCount:   snapshot.slices(),
		BucketSeconds: snapshot.bucketSeconds,
		IsAdmin:       isAdmin,
		Summary:       summary,
		Kinds:         channelHealthKindViews(rows, snapshot.bucketSeconds, snapshot.slices()),
		Rows:          rows,
		Providers:     providers,
		UpdatedAt:     snapshot.updatedAt,
	})
}

// projectChannelHealthRows strips what the caller is not entitled to see. A
// non-admin sees only models their token can reach, and never learns which
// upstream vendor served them — the lane abstraction is the product.
func projectChannelHealthRows(c *gin.Context, rows []channelHealthLaneView, isAdmin bool) ([]channelHealthLaneView, error) {
	if isAdmin {
		return rows, nil
	}
	var accessibleNames []string
	if c.GetInt("token_id") > 0 {
		var err error
		accessibleNames, err = getAccessibleModelNames(c)
		if err != nil {
			return nil, err
		}
	} else {
		// Session users carry their billing tier in the group context key; that
		// is not an ability lane. Project them against the two published lanes,
		// while API keys retain token-specific group/model/vendor restrictions.
		accessibleNames = getUserCatalogModelNames()
	}
	accessible := stringSet(accessibleNames)
	allowedSearch, searchRestricted := service.SearchProvidersAllowedForToken(c)
	result := make([]channelHealthLaneView, 0, len(rows))
	for _, row := range rows {
		// A search backend is selected by name in /v1/search rather than
		// granted like a model, so its row follows the key's search policy:
		// every backend for an unrestricted key, only the allowed ones — or
		// none — for a restricted one.
		if row.Kind == channelHealthKindSearch {
			if searchRestricted {
				if _, ok := allowedSearch[row.Provider]; !ok {
					continue
				}
			}
		} else if !accessible[row.ModelName] {
			continue
		}
		result = append(result, row)
	}
	// Fold before redaction, while sample totals and real-route statuses are
	// still available. A member receives one row per public lane, not one row
	// per hidden route with its name erased.
	result = mergePublicChannelHealthRows(result)
	projected := make([]channelHealthLaneView, 0, len(result))
	for _, row := range result {
		if !channelHealthRowHasSamples(row) {
			continue
		}
		// Lanes are the user-facing unit. Everything about the pool behind one
		// is admin-only: not just which vendors are in it, but how many there
		// are, how many are still usable, and whether a standby is currently
		// covering for a failure. A customer who learns that a lane is running
		// on its last source knows something true, actionable only by us, and
		// alarming out of proportion to their actual experience — which is
		// that the lane works.
		row.Sources = nil
		row.ChannelCount = 0
		row.LaneSlug = ""
		row.FallbackActive = false
		row.ServingCount = 0
		row.BlockedCount = 0
		row.AggregateTotals = nil
		row.Buckets = append([]channelHealthBucketView(nil), row.Buckets...)
		for i := range row.Buckets {
			// "We could not check this" is our instrumentation gap, not a fact
			// about their service. To them it reads the same as quiet.
			if row.Buckets[i].Source == channelHealthSourceBlocked {
				row.Buckets[i].Source = channelHealthSourceNone
				row.Buckets[i].Tone = "none"
			}
		}
		projected = append(projected, row)
	}
	return projected, nil
}

// overlayChannelHealthBreaker re-reads every route's breaker state into a
// snapshot and recomputes the statuses that depend on it.
//
// A snapshot's figures describe its window and age gracefully; its statuses
// do not. The 7d view is rebuilt every half hour, so without this a lane the
// router pulled ten minutes ago would still read operational, or a recovered
// one still read down. Only status moves here — sources, lanes and providers
// — and the snapshot's own slices are never written, since the caller's copy
// may be cached.
func overlayChannelHealthBreaker(
	snapshot channelHealthSnapshot,
	states []model.RouteHealthState,
	now time.Time,
) channelHealthSnapshot {
	breaker := make(map[string]model.RouteHealthState, len(states))
	for _, state := range states {
		breaker[model.RouteHealthCacheKey(state.ModelName, state.Lane, state.ChannelId)] = state
	}

	type routeKey struct {
		model string
		lane  string
		id    int
	}
	sourceStatus := map[routeKey]string{}
	providerStatus := map[int]string{}
	rows := make([]channelHealthLaneView, len(snapshot.rows))
	for i, row := range snapshot.rows {
		if row.Kind == channelHealthKindSearch {
			// Search backends have no breaker; their streak was read from
			// their own samples when the row was folded.
			rows[i] = row
			continue
		}
		sources := make([]channelHealthSourceView, len(row.Sources))
		copy(sources, row.Sources)
		for j := range sources {
			source := &sources[j]
			if source.Disabled {
				continue
			}
			routed := source.AbilityModel
			if routed == "" {
				routed = row.ModelName
			}
			lane := row.LaneSlug
			if lane == "" {
				lane = model.ChannelLaneDirect
				if row.Lane == catalogLaneKey(model.ChannelLaneAgent) {
					lane = model.ChannelLaneAgent
				}
			}
			state := breaker[model.RouteHealthCacheKey(routed, lane, source.ChannelID)]
			source.ConsecutiveFailures = state.ConsecutiveFailures
			source.Retreating = state.IsRetreating(now)
			source.RetreatUntil = nil
			if source.Retreating {
				source.RetreatUntil = state.RetreatUntil
			}
			source.Status = channelHealthSourceStatus(source)
			sourceStatus[routeKey{row.ModelName, row.Lane, source.ChannelID}] = source.Status
			providerStatus[source.ChannelID] = worstChannelHealthStatus(
				providerStatus[source.ChannelID], source.Status,
			)
		}
		row.Sources = sources
		if len(sources) > 0 {
			serving, fallbackActive := electChannelHealthServingSources(sources)
			row.FallbackActive = fallbackActive
			row.ServingCount = len(serving)
			row.Status = channelHealthLaneStatus(serving)
		}
		rows[i] = row
	}

	providers := make([]channelHealthProviderView, len(snapshot.providers))
	for i, provider := range snapshot.providers {
		lanes := make([]channelHealthProviderLaneView, len(provider.Lanes))
		copy(lanes, provider.Lanes)
		for j := range lanes {
			if status, ok := sourceStatus[routeKey{lanes[j].ModelName, lanes[j].Lane, provider.ChannelID}]; ok {
				lanes[j].Status = status
			}
		}
		provider.Lanes = lanes
		if status, ok := providerStatus[provider.ChannelID]; ok && status != "" {
			provider.Status = status
		}
		providers[i] = provider
	}
	sort.SliceStable(providers, func(i, j int) bool {
		return channelHealthStatusSeverity(providers[i].Status) > channelHealthStatusSeverity(providers[j].Status)
	})

	snapshot.rows = rows
	snapshot.providers = providers
	return snapshot
}

// channelHealthSourceSample is the member-facing source of any measured
// evidence. Whether a sample was a customer's request or our probe, and how
// many there were, is an operations fact.
const channelHealthSourceSample = "sample"

// neutralizeChannelHealthProvenance rewrites a member's row so it states what
// was measured and how fresh it is, never whose request measured it or how
// many requests there were. Rates, latencies and tones are untouched.
func neutralizeChannelHealthProvenance(row channelHealthLaneView) channelHealthLaneView {
	neutral := func(source string) string {
		if source == channelHealthSourceTraffic || source == channelHealthSourceProbe {
			return channelHealthSourceSample
		}
		return source
	}
	row.Source = neutral(row.Source)
	row.RequestCount, row.ErrorCount = 0, 0
	row.ProbeCount, row.ProbeErrorCount = 0, 0
	row.OutputTokens, row.DecodeMs = 0, 0
	row.LastOutcomeAt = laterChannelHealthTime(row.LastOutcomeAt, row.LastProbedAt)
	row.LastProbedAt = nil
	buckets := make([]channelHealthBucketView, len(row.Buckets))
	for i, bucket := range row.Buckets {
		bucket.Source = neutral(bucket.Source)
		bucket.RequestCount, bucket.ErrorCount = 0, 0
		buckets[i] = bucket
	}
	row.Buckets = buckets
	return row
}

// channelHealthRowHasSamples reports whether a lane has anything in the window
// to show. Every lane we can reach is probed hourly, so one with no sample at
// all is a lane we are unable to check — an image model, a client-only
// upstream. That gap is ours to close; a customer would read a row of blanks
// as a model that is quietly broken, so members do not receive it.
func channelHealthRowHasSamples(row channelHealthLaneView) bool {
	return row.Source != channelHealthSourceNone && row.Source != channelHealthSourceBlocked
}

// loadChannelHealthSnapshot returns this process's view of a window, built
// from the hourly route health rows and reused for channelHealthCacheTTL.
// Building reads hours, not raw requests, so it is cheap enough to do on the
// first request after the cache lapses; there is no background snapshot to go
// stale.
func loadChannelHealthSnapshot(windowLabel string, window time.Duration) (channelHealthSnapshot, error) {
	if entry, ok := readChannelHealthCache(windowLabel); ok &&
		time.Since(entry.updatedAt) < channelHealthCacheTTL {
		return entry, nil
	}

	build := channelHealthBuildLock(windowLabel)
	build.Lock()
	defer build.Unlock()

	// Re-check under the build lock: whoever held it may have just refreshed
	// this window, in which case there is nothing left to do.
	entry, cached := readChannelHealthCache(windowLabel)
	if cached && time.Since(entry.updatedAt) < channelHealthCacheTTL {
		return entry, nil
	}

	snapshot, err := buildChannelHealthSnapshot(window)
	if err != nil {
		// A stale view beats an error page on a monitoring surface.
		if cached {
			return entry, nil
		}
		return channelHealthSnapshot{}, err
	}
	channelHealthCache.Lock()
	channelHealthCache.items[windowLabel] = snapshot
	channelHealthCache.Unlock()
	return snapshot, nil
}

func readChannelHealthCache(windowLabel string) (channelHealthSnapshot, bool) {
	channelHealthCache.Lock()
	defer channelHealthCache.Unlock()
	entry, ok := channelHealthCache.items[windowLabel]
	return entry, ok
}

func channelHealthBuildLock(windowLabel string) *sync.Mutex {
	channelHealthCache.Lock()
	defer channelHealthCache.Unlock()
	lock, ok := channelHealthCache.building[windowLabel]
	if !ok {
		lock = &sync.Mutex{}
		channelHealthCache.building[windowLabel] = lock
	}
	return lock
}

// InvalidateChannelHealthCache drops cached aggregates. The probe worker calls
// it after a sweep so a freshly probed cell shows up without waiting out the
// cache TTL.
func InvalidateChannelHealthCache() {
	channelHealthCache.Lock()
	channelHealthCache.items = map[string]channelHealthSnapshot{}
	channelHealthCache.Unlock()
	// The public model discovery payload embeds lane health, so dropping only
	// the aggregate would leave /models.json serving the previous snapshot.
	InvalidateModelDiscoveryCache()
}

// RefreshChannelHealthAfterRoutingMutation drops this process's cached health
// views so a pause or reweight shows on the next read. Other API processes
// pick it up when their one-minute cache lapses.
func RefreshChannelHealthAfterRoutingMutation() error {
	InvalidateChannelHealthCache()
	return nil
}

func buildChannelHealthSnapshot(window time.Duration) (channelHealthSnapshot, error) {
	return buildChannelHealthSnapshotContext(context.Background(), window)
}

func buildChannelHealthSnapshotContext(ctx context.Context, window time.Duration) (channelHealthSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return channelHealthSnapshot{}, err
	}

	now := time.Now().UTC()
	bucketCount := channelHealthBucketCountFor(window)
	bucketSeconds := int64(window.Seconds()) / int64(bucketCount)
	if bucketSeconds <= 0 {
		bucketSeconds = 1
	}
	// Slices sit on the clock — 20:00 to 22:00, not 20:48 to 22:48 — and the
	// newest one is the slot now is in, so a bar reads as a period of the
	// day rather than as an offset from whenever the aggregate was built.
	slice := time.Duration(bucketSeconds) * time.Second
	since := now.Truncate(slice).Add(slice).Add(-window)

	grid, err := model.LoadChannelHealthGridContext(ctx)
	if err != nil {
		return channelHealthSnapshot{}, err
	}
	// One read of the hourly route health rows feeds every aggregate below.
	health, err := model.LoadRouteHealthWindow(ctx, since, now)
	if err != nil {
		return channelHealthSnapshot{}, err
	}
	evidence := health.Evidence()
	buckets := health.Buckets(since, bucketSeconds, bucketCount, false)
	coarseBuckets := health.Buckets(since, bucketSeconds*channelHealthCoarseFactor, bucketCount/channelHealthCoarseFactor, false)
	// The upstream view counts every provider attempt, superseded ones too.
	upstreamBuckets := health.Buckets(since, bucketSeconds, bucketCount, true)
	channelPercentiles := health.ChannelPercentiles(model.RouteHealthEnabledChannels(grid))
	lastProbes, err := model.QueryChannelHealthLastProbesContext(ctx, since)
	if err != nil {
		return channelHealthSnapshot{}, err
	}
	unprobeable, err := model.QueryChannelHealthUnprobeableContext(ctx, since)
	if err != nil {
		return channelHealthSnapshot{}, err
	}
	blockedCells := make(map[string]model.ChannelHealthUnprobeableRow, len(unprobeable))
	for _, row := range unprobeable {
		blockedCells[model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)] = row
	}
	states, err := model.LoadRouteHealthStates()
	if err != nil {
		return channelHealthSnapshot{}, err
	}
	breaker := make(map[string]model.RouteHealthState, len(states))
	for _, state := range states {
		breaker[model.RouteHealthCacheKey(state.ModelName, state.Lane, state.ChannelId)] = state
	}

	input := channelHealthFoldInput{
		grid:          grid,
		catalog:       channelHealthCatalogModels(),
		evidence:      evidence,
		buckets:       buckets,
		blockedCells:  blockedCells,
		breaker:       breaker,
		now:           now,
		since:         since,
		bucketSeconds: bucketSeconds,
		bucketCount:   bucketCount,
		kinds:         channelHealthCatalogKinds(),
		coarseBuckets: coarseBuckets,
	}
	// Which routes a lane counts depends on the router's election, which only
	// the fold knows. Percentiles cannot be re-derived per route afterwards,
	// so the fold runs once to name the routes it leaves out, and their
	// samples are kept out of the percentile population at the source.
	excluded := channelHealthUncountedRoutes(foldChannelHealth(input), grid, input.catalog)
	aliases := make(map[string]string)
	for _, cell := range grid {
		if name, published := channelHealthCatalogName(input.catalog, cell.ModelName); published && name != cell.ModelName {
			aliases[cell.ModelName] = name
		}
	}
	population := model.RouteHealthPopulation{Current: model.RouteHealthCurrentCells(grid), Aliases: aliases, Excluded: excluded}
	input.percentiles = health.Percentiles(population)
	rows := foldChannelHealth(input)
	attachPublicChannelHealthPercentiles(rows, health, population)
	// The provider projection is folded from the lane result rather than from
	// the raw queries, which is what guarantees the two views of this window
	// agree on every decision.
	providers := foldChannelHealthProviders(
		rows, grid, channelPercentiles, lastProbes, upstreamBuckets,
		channelHealthCurrentCells(grid), since, bucketSeconds, bucketCount,
	)
	// Search backends are not routes in the grid; their rows come from their
	// own samples and join the same list as one more section.
	searchBackends, err := loadSearchHealthBackends()
	if err != nil {
		return channelHealthSnapshot{}, err
	}
	if len(searchBackends) > 0 {
		searchSamples, err := model.QuerySearchHealthSamples(since)
		if err != nil {
			return channelHealthSnapshot{}, err
		}
		rows = append(rows, foldSearchHealth(searchBackends, searchSamples, since, bucketSeconds, bucketCount)...)
	}
	return channelHealthSnapshot{
		windowSeconds: int64(window.Seconds()),
		bucketSeconds: bucketSeconds,
		bucketCount:   bucketCount,
		since:         since,
		rows:          rows,
		providers:     providers,
		updatedAt:     now,
	}, nil
}

// channelHealthCatalogModels is the set of models the catalog currently
// publishes. The page describes what we sell, so a model that was retired
// stays off it even while its old traffic is still inside the window. A nil
// set means the catalog could not be read, and the fold then shows every
// routed model rather than none.
func channelHealthCatalogModels() map[string]bool {
	names := getPublicModelDiscoveryNames()
	if len(names) == 0 {
		return nil
	}
	return stringSet(names)
}

// channelHealthUncountedRoutes lists, by routed name, every current route a
// folded lane leaves out of its figures.
func channelHealthUncountedRoutes(
	rows []channelHealthLaneView,
	grid []model.ChannelHealthLaneCell,
	catalog map[string]bool,
) []model.ChannelHealthRouteRef {
	uncounted := map[string]bool{}
	for _, row := range rows {
		for _, source := range row.Sources {
			if source.uncounted {
				uncounted[model.ChannelHealthCellKey(row.ModelName, row.LaneSlug, source.ChannelID)] = true
			}
		}
	}
	if len(uncounted) == 0 {
		return nil
	}
	var routes []model.ChannelHealthRouteRef
	for _, cell := range grid {
		name, published := channelHealthCatalogName(catalog, cell.ModelName)
		if !published || !uncounted[model.ChannelHealthCellKey(name, cell.Lane, cell.ChannelId)] {
			continue
		}
		routes = append(routes, model.ChannelHealthRouteRef{
			ModelName: cell.ModelName, Lane: cell.Lane, ChannelId: cell.ChannelId,
		})
	}
	return routes
}

// channelHealthCurrentCells names every route serving now. Evidence from any
// other route — a channel since deleted, paused, or moved off the model — is
// history, and stays out of every number on the page.
func channelHealthCurrentCells(grid []model.ChannelHealthLaneCell) map[string]bool {
	current := make(map[string]bool, len(grid))
	for _, cell := range grid {
		if cell.ChannelStatus != common.ChannelStatusEnabled {
			continue
		}
		if cell.AbilityEnabled != nil && !*cell.AbilityEnabled {
			continue
		}
		current[model.ChannelHealthCellKey(cell.ModelName, cell.Lane, cell.ChannelId)] = true
	}
	return current
}

type channelHealthAccumulator struct {
	view     channelHealthLaneView
	p50      float64
	probeP50 float64
	p95      float64
	probeP95 float64
	ttftP50  float64
	ttftP95  float64
	stallP50 float64
	sources  map[int]*channelHealthSourceView
	// counted names the sources whose samples make up the lane's figures.
	counted map[int]bool
}

// channelHealthCountsTowardLane decides whether a source's samples describe
// the lane. A primary always does, including one the breaker has pulled: its
// failures happened to customers. Parked inventory never does, and a standby
// only once it has carried customer traffic or is carrying the lane now —
// until then its hourly probes measure capacity nobody is using, and a failed
// one must not lower the availability of a lane whose primary served everyone.
func channelHealthCountsTowardLane(source *channelHealthSourceView, serving map[int]bool) bool {
	if source.excludedFromCurrent {
		return false
	}
	switch model.NormalizeAbilityRole(source.Role) {
	case model.AbilityRoleInventory:
		return false
	case model.AbilityRoleBackup:
		return source.RequestCount > 0 || serving[source.ChannelID]
	default:
		return true
	}
}

// channelHealthLanePrices flattens the active sell-price table into one
// (model, lane) lookup, loaded once per fold. Price is per lane, so a model's
// eco row and its stable row are different entries and must not substitute for
// each other.
func channelHealthLanePrices() map[string]model.ModelSellPrice {
	lanes := model.GetActiveModelSellPriceLaneMap()
	prices := make(map[string]model.ModelSellPrice, len(lanes))
	for modelName, rows := range lanes {
		for _, row := range rows {
			prices[model.ChannelHealthLaneKey(modelName, row.ChannelGroup)] = row
		}
	}
	return prices
}

// channelHealthLanePrice resolves one route's list price, falling back to the
// catalog's canonical model name exactly as the pricing surfaces do. A route
// with no price row simply has none: leaving the pointers nil says "unpriced",
// where a zero would claim this lane is free.
func channelHealthLanePrice(
	prices map[string]model.ModelSellPrice,
	modelName string,
	lane string,
) (model.ModelSellPrice, bool) {
	if lane == "" {
		return model.ModelSellPrice{}, false
	}
	if row, ok := prices[model.ChannelHealthLaneKey(modelName, lane)]; ok {
		return row, true
	}
	if matched := model.NormalizePricingModelName(modelName); matched != "" && matched != modelName {
		if row, ok := prices[model.ChannelHealthLaneKey(matched, lane)]; ok {
			return row, true
		}
	}
	return model.ModelSellPrice{}, false
}

// channelHealthFoldInput is everything one window's fold reads.
type channelHealthFoldInput struct {
	grid        []model.ChannelHealthLaneCell
	catalog     map[string]bool
	evidence    []model.ChannelHealthEvidenceRow
	percentiles []model.ChannelHealthPercentileRow
	buckets     []model.ChannelHealthBucketRow
	// blockedCells are routes whose newest probe attempt was skipped.
	blockedCells map[string]model.ChannelHealthUnprobeableRow
	// breaker is route_health_state keyed by RouteHealthCacheKey.
	breaker       map[string]model.RouteHealthState
	now           time.Time
	since         time.Time
	bucketSeconds int64
	// bucketCount is how many slices the window holds; zero reads as the
	// day's density.
	bucketCount int
	// kinds maps a catalog model name to what it produces; see
	// channelHealthCatalogKinds. Nil reads every model as a text model.
	kinds map[string]string
	// coarseBuckets are the same window sliced channelHealthCoarseFactor
	// times wider, for the kinds whose wall is drawn in wide slices.
	coarseBuckets []model.ChannelHealthBucketRow
}

// channelHealthCatalogName maps a routed model name onto the name the catalog
// publishes it under, and reports whether the catalog publishes it at all.
//
// Abilities and traffic carry the routed name, which for aliased families (a
// thinking-budget form, a gizmo variant) is not the catalog's. A row keyed by
// the routed name would be dropped by every consumer that looks up the
// catalog's name — the plaza, a member's accessible models — so rows are keyed
// by the catalog name and every variant that resolves to it folds into one.
// With no catalog to consult, every name stands as itself.
func channelHealthCatalogName(catalog map[string]bool, modelName string) (string, bool) {
	if catalog == nil || catalog[modelName] {
		return modelName, true
	}
	normalized := model.NormalizePricingModelName(modelName)
	if normalized != "" && catalog[normalized] {
		return normalized, true
	}
	return modelName, false
}

// foldChannelHealth builds one row per (model, lane) the catalog sells and
// some enabled route serves.
//
// Each row answers two questions, from two different places:
//
//   - whether the lane works NOW is the router's own breaker state, read over
//     the upstreams the router would actually choose between. The page never
//     recomputes that verdict from a percentage, so it cannot disagree with
//     where the next request is sent;
//   - how the lane BEHAVES is every sample in the window — customer requests
//     and probes alike — summed over the routes serving now and divided once.
func foldChannelHealth(input channelHealthFoldInput) []channelHealthLaneView {
	lanes := map[string]*channelHealthAccumulator{}
	currentCells := channelHealthCurrentCells(input.grid)

	// displayCells is currentCells re-keyed by catalog name, for the wall.
	displayCells := map[string]bool{}
	for _, cell := range input.grid {
		key := model.ChannelHealthCellKey(cell.ModelName, cell.Lane, cell.ChannelId)
		if !currentCells[key] {
			continue
		}
		name, published := channelHealthCatalogName(input.catalog, cell.ModelName)
		if !published {
			continue
		}
		displayCells[model.ChannelHealthCellKey(name, cell.Lane, cell.ChannelId)] = true
		laneKey := model.ChannelHealthLaneKey(name, cell.Lane)
		acc, ok := lanes[laneKey]
		if !ok {
			acc = &channelHealthAccumulator{
				view: channelHealthLaneView{
					ModelName: name,
					Kind:      channelHealthKindOf(input.kinds, name),
					Lane:      catalogLaneKey(model.NormalizeChannelLane(cell.Lane)),
					LaneSlug:  model.NormalizeChannelLane(cell.Lane),
					Status:    publicStatusUnknown,
					Source:    channelHealthSourceNone,
				},
				sources: map[int]*channelHealthSourceView{},
			}
			lanes[laneKey] = acc
		}
		blocked, isBlocked := input.blockedCells[key]
		source, seen := acc.sources[cell.ChannelId]
		if !seen {
			acc.view.ChannelCount++
			source = &channelHealthSourceView{
				ChannelID:   cell.ChannelId,
				ChannelName: cell.ChannelName,
				Status:      publicStatusUnknown,
				Source:      channelHealthSourceNone,
				Weight:      cell.Weight,
				Role:        cell.Role,
			}
			if cell.ModelName != name {
				source.AbilityModel = cell.ModelName
			}
			if isBlocked {
				source.BlockedReason = blocked.Reason
			}
			acc.sources[cell.ChannelId] = source
		} else if !isBlocked {
			// Two variants of one model on one upstream: if either can be
			// checked, the upstream is not a blind spot for this lane.
			source.BlockedReason = ""
		}
		if state, ok := input.breaker[model.RouteHealthCacheKey(cell.ModelName, cell.Lane, cell.ChannelId)]; ok {
			// Variants merge toward the worse verdict: the lane is served by
			// both, so a tripped variant is a tripped route.
			if state.ConsecutiveFailures > source.ConsecutiveFailures {
				source.ConsecutiveFailures = state.ConsecutiveFailures
			}
			if state.IsRetreating(input.now) {
				source.Retreating = true
				source.RetreatUntil = state.RetreatUntil
			}
		}
	}

	for _, row := range input.evidence {
		if row.RequestCount == 0 {
			continue
		}
		name, _ := channelHealthCatalogName(input.catalog, row.ModelName)
		acc, ok := lanes[model.ChannelHealthLaneKey(name, row.Lane)]
		if !ok {
			continue
		}
		source, ok := acc.sources[row.ChannelId]
		if !ok {
			// A route that served this lane earlier in the window and does not
			// now. Its record is shown to the operator as history and counted
			// nowhere: the lane's numbers describe the pool it has today.
			source = &channelHealthSourceView{
				ChannelID:           row.ChannelId,
				ChannelName:         row.ChannelName,
				Status:              publicStatusUnknown,
				Source:              channelHealthSourceNone,
				Disabled:            true,
				excludedFromCurrent: true,
			}
			acc.sources[row.ChannelId] = source
		}
		last := row.LastEvidenceAt.Ptr()
		if row.Kind == model.RouteHealthKindAttempt {
			source.attempts += row.RequestCount
			source.attemptErrors += row.ErrorCount
			source.attemptDurationSumMs += row.DurationSumMs
			source.attemptDurationCount += row.DurationCount
		}
		if row.Kind == model.ChannelHealthKindProbe {
			source.ProbeCount += row.RequestCount
			source.probeErrors += row.ErrorCount
			source.lastProbedAt = laterChannelHealthTime(source.lastProbedAt, last)
		} else {
			source.RequestCount += row.RequestCount
			source.trafficErrors += row.ErrorCount
			source.trafficDurationCount += row.DurationCount
			source.lastTrafficAt = laterChannelHealthTime(source.lastTrafficAt, last)
			if row.ChannelName != "" && row.ChannelName != "unknown" {
				source.ChannelName = row.ChannelName
			}
		}
		source.durationSumMs += row.DurationSumMs
		source.durationCount += row.DurationCount
		source.outputTokens += row.OutputTokens
		source.decodeMs += row.DecodeMs
		source.interrupted += row.InterruptedCount
		source.streamed += row.StreamedCount
		source.LastEvidenceAt = laterChannelHealthTime(source.LastEvidenceAt, last)
	}

	// SQL ranks every routed alias under its catalog name before returning a
	// percentile, so each published lane has one complete population.
	for _, row := range input.percentiles {
		name, _ := channelHealthCatalogName(input.catalog, row.ModelName)
		laneKey := model.ChannelHealthLaneKey(name, row.Lane)
		acc, ok := lanes[laneKey]
		if !ok {
			continue
		}
		acc.p95 = row.P95LatencyMs
		acc.probeP95 = row.ProbeP95LatencyMs
		acc.ttftP50 = row.TTFTP50Ms
		acc.ttftP95 = row.TTFTP95Ms
		acc.p50 = row.P50LatencyMs
		acc.probeP50 = row.ProbeP50LatencyMs
		acc.stallP50 = row.StallP50Ms
	}

	// The wall is drawn per catalog name too, from current routes only.
	wallBuckets := make([]model.ChannelHealthBucketRow, 0, len(input.buckets))
	for _, row := range input.buckets {
		if !currentCells[model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)] {
			continue
		}
		row.ModelName, _ = channelHealthCatalogName(input.catalog, row.ModelName)
		wallBuckets = append(wallBuckets, row)
	}
	coarseWallBuckets := make([]model.ChannelHealthBucketRow, 0, len(input.coarseBuckets))
	for _, row := range input.coarseBuckets {
		if !currentCells[model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)] {
			continue
		}
		row.ModelName, _ = channelHealthCatalogName(input.catalog, row.ModelName)
		coarseWallBuckets = append(coarseWallBuckets, row)
	}
	wallBlocked := make(map[string]model.ChannelHealthUnprobeableRow, len(input.blockedCells))
	for key, row := range input.blockedCells {
		if !currentCells[key] {
			continue
		}
		row.ModelName, _ = channelHealthCatalogName(input.catalog, row.ModelName)
		wallBlocked[model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)] = row
	}
	// Resolve each source's state and the router's election first: which
	// sources count toward a lane depends on which of them are serving it.
	for _, acc := range lanes {
		candidates := make([]channelHealthSourceView, 0, len(acc.sources))
		for _, source := range acc.sources {
			source.Source = channelHealthSourceOf(source)
			source.Status = channelHealthSourceStatus(source)
			if source.samples() > 0 {
				source.SuccessRate = successRate(source.samples(), source.sampleErrors())
				source.BlockedReason = ""
			}
			if source.durationCount > 0 {
				avg := float64(source.durationSumMs) / float64(source.durationCount)
				source.AvgLatencyMs = &avg
			}
			candidates = append(candidates, *source)
		}
		serving, _ := electChannelHealthServingSources(candidates)
		servingIDs := make(map[int]bool, len(serving))
		for _, source := range serving {
			servingIDs[source.ChannelID] = true
		}
		acc.counted = map[int]bool{}
		for id, source := range acc.sources {
			if channelHealthCountsTowardLane(source, servingIDs) {
				acc.counted[id] = true
				continue
			}
			source.uncounted = !source.excludedFromCurrent
			if !source.excludedFromCurrent {
				// A parked route keeps its record in the source list and the
				// provider view, and stays off the lane's wall.
				delete(displayCells, model.ChannelHealthCellKey(acc.view.ModelName, acc.view.LaneSlug, id))
			}
		}
	}
	bucketsByLane := foldChannelHealthBuckets(
		wallBuckets, displayCells, wallBlocked, input.since, input.bucketSeconds, input.slices(),
	)
	coarseBucketSeconds := input.bucketSeconds * channelHealthCoarseFactor
	coarseBucketCount := input.slices() / channelHealthCoarseFactor
	coarseBucketsByLane := foldChannelHealthBuckets(
		coarseWallBuckets, displayCells, wallBlocked, input.since, coarseBucketSeconds, coarseBucketCount,
	)
	// One load for the whole fold. A price lookup per lane row would put a
	// query behind every cell of a page that already scans a week of outcomes.
	lanePrices := channelHealthLanePrices()

	result := make([]channelHealthLaneView, 0, len(lanes))
	for key, acc := range lanes {
		view := acc.view
		if price, ok := channelHealthLanePrice(lanePrices, view.ModelName, view.LaneSlug); ok {
			view.InputPerMtok = price.InputPerMtok
			view.OutputPerMtok = price.OutputPerMtok
		}

		var durationSum, durationCount, trafficDurationCount, interrupted, streamed int64
		for id, source := range acc.sources {
			if !acc.counted[id] {
				continue
			}
			if source.BlockedReason != "" {
				// A route we cannot probe and nobody called: a gap in our own
				// instrumentation, surfaced to the operator.
				source.Source = channelHealthSourceBlocked
				view.BlockedCount++
			}
			// A lane counts each customer request once, by its answer: the
			// attempts a retry superseded stay on their upstream's record.
			view.RequestCount += source.RequestCount - source.attempts
			view.ErrorCount += source.trafficErrors - source.attemptErrors
			view.ProbeCount += source.ProbeCount
			view.ProbeErrorCount += source.probeErrors
			view.OutputTokens += source.outputTokens
			view.DecodeMs += source.decodeMs
			durationSum += source.durationSumMs - source.attemptDurationSumMs
			durationCount += source.durationCount - source.attemptDurationCount
			trafficDurationCount += source.trafficDurationCount - source.attemptDurationCount
			interrupted += source.interrupted
			streamed += source.streamed
			view.LastOutcomeAt = laterChannelHealthTime(view.LastOutcomeAt, source.lastTrafficAt)
			view.LastProbedAt = laterChannelHealthTime(view.LastProbedAt, source.lastProbedAt)
		}

		samples := view.RequestCount + view.ProbeCount
		switch {
		case view.RequestCount > 0:
			view.Source = channelHealthSourceTraffic
		case view.ProbeCount > 0:
			view.Source = channelHealthSourceProbe
		case view.BlockedCount > 0:
			view.Source = channelHealthSourceBlocked
		default:
			view.Source = channelHealthSourceNone
		}
		view.SuccessRate = successRate(samples, view.ErrorCount+view.ProbeErrorCount)
		if durationCount > 0 {
			avg := float64(durationSum) / float64(durationCount)
			view.AvgLatencyMs = &avg
		}
		// Total duration grows with the length of the reply, and a probe's
		// reply is short by design. Fall back only when no successful customer
		// request measured a duration; failed attempts are availability evidence.
		p95 := acc.p95
		if trafficDurationCount == 0 {
			p95 = acc.probeP95
		}
		view.P95LatencyMs = positiveChannelHealthFigure(p95)
		view.TTFTP50Ms = positiveChannelHealthFigure(acc.ttftP50)
		view.TTFTP95Ms = positiveChannelHealthFigure(acc.ttftP95)
		latencyP50, latencyP95 := channelHealthLatencyPair(
			trafficDurationCount, acc.p50, acc.p95, acc.probeP50, acc.probeP95,
		)
		view.LatencyP50Ms = positiveChannelHealthFigure(latencyP50)
		view.LatencyP95Ms = positiveChannelHealthFigure(latencyP95)
		view.StallP50Ms = positiveChannelHealthFigure(acc.stallP50)
		if view.DecodeMs > 0 && view.OutputTokens > 0 {
			tps := float64(view.OutputTokens) * 1000 / float64(view.DecodeMs)
			view.OutputTPS = &tps
		}
		if streamed > 0 {
			ratio := float64(interrupted) / float64(streamed)
			view.InterruptionRate = &ratio
		}
		view.AggregateTotals = &channelHealthAggregateTotals{
			DurationSumMs: durationSum, DurationCount: durationCount,
			TrafficDurationCount: trafficDurationCount,
			Interrupted:          interrupted, Streamed: streamed,
		}

		view.Sources = sortedChannelHealthSources(acc.sources, view.RequestCount)
		serving, fallbackActive := electChannelHealthServingSources(view.Sources)
		view.FallbackActive = fallbackActive
		view.ServingCount = len(serving)
		view.Status = channelHealthLaneStatus(serving)

		if channelHealthKindIsCoarse(view.Kind) {
			view.Buckets = coarseBucketsByLane[key]
			if view.Buckets == nil {
				view.Buckets = emptyChannelHealthBuckets(input.since, coarseBucketSeconds, coarseBucketCount)
			}
		} else {
			view.Buckets = bucketsByLane[key]
			if view.Buckets == nil {
				view.Buckets = emptyChannelHealthBuckets(input.since, input.bucketSeconds, input.slices())
			}
		}
		result = append(result, view)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].ModelName != result[j].ModelName {
			return result[i].ModelName < result[j].ModelName
		}
		// Stable before eco so a model's two lanes always read in the same
		// order down the page.
		return result[i].Lane > result[j].Lane
	})
	return result
}

// channelHealthSourceOf names the kind of evidence a route has in the window.
func channelHealthSourceOf(source *channelHealthSourceView) string {
	switch {
	case source.RequestCount > 0:
		return channelHealthSourceTraffic
	case source.ProbeCount > 0:
		return channelHealthSourceProbe
	default:
		return channelHealthSourceNone
	}
}

// channelHealthSourceStatus is the breaker's view of one route. A route that
// has failed often enough to trip the breaker is down — whether or not its
// current retreat has lapsed, since a lapsed retreat only means the next
// sample decides; one whose newest sample failed is degraded; one whose newest
// sample succeeded is operational. The window's success rate does not enter
// into it: a route that failed for an hour this morning and has answered every
// request since is working now.
func channelHealthSourceStatus(source *channelHealthSourceView) string {
	switch {
	case source.Retreating || source.ConsecutiveFailures >= model.RouteHealthFailureThreshold:
		return publicStatusOutage
	case source.ConsecutiveFailures > 0:
		return publicStatusDegraded
	case source.samples() > 0:
		return publicStatusOperational
	default:
		return publicStatusUnknown
	}
}

// channelHealthLaneStatus reads a lane's state off the routes the router would
// choose between right now. Every one of them down means the lane is down;
// some down and some working means the lane is degraded, because a share of
// requests lands on a failing route before a retry moves it; all working means
// operational. A route with no evidence does not vote, because "unknown" would
// outrank a pool that is visibly serving.
func channelHealthLaneStatus(serving []channelHealthSourceView) string {
	if len(serving) == 0 {
		return publicStatusUnknown
	}
	allRetreating := true
	var working, troubled, down int
	for _, source := range serving {
		if !source.Retreating {
			allRetreating = false
		}
		switch source.Status {
		case publicStatusOperational:
			working++
		case publicStatusDegraded:
			troubled++
		case publicStatusOutage:
			down++
		}
	}
	switch {
	case allRetreating:
		return publicStatusOutage
	case working+troubled+down == 0:
		return publicStatusUnknown
	case working == 0 && troubled == 0:
		return publicStatusOutage
	case troubled > 0 || down > 0:
		return publicStatusDegraded
	default:
		return publicStatusOperational
	}
}

func laterChannelHealthTime(current *time.Time, candidate *time.Time) *time.Time {
	if candidate == nil {
		return current
	}
	if current == nil || candidate.After(*current) {
		value := *candidate
		return &value
	}
	return current
}

func positiveChannelHealthFigure(value float64) *float64 {
	if value <= 0 {
		return nil
	}
	return &value
}

// electChannelHealthServingSources picks the upstreams the router is currently
// choosing between, mirroring the election the cache performs on every request:
// primaries first, standbys only when no primary is available, and parked
// inventory never.
//
// It reports separately whether a standby is carrying the lane, because that
// is the fact an operator has to act on. The lane itself is fine; the supply
// behind it is one failure from not being.
func electChannelHealthServingSources(
	sources []channelHealthSourceView,
) ([]channelHealthSourceView, bool) {
	var primaries, backups, availablePrimaries, availableBackups []channelHealthSourceView
	for _, source := range sources {
		// Disabled covers both an operator pause and a channel that left this
		// lane mid-window. Neither can receive the next request, so neither
		// may speak for the lane's current state.
		//
		// The distinction that matters elsewhere — a retired channel's traffic
		// still counts toward the window's totals, a paused one's does not —
		// belongs to the metrics, not to this election. Letting a retired
		// channel stand as a primary here would let its stale success rate
		// hide an outage on the standby that is actually carrying the lane.
		if source.Disabled {
			continue
		}
		switch model.NormalizeAbilityRole(source.Role) {
		case model.AbilityRoleInventory:
			continue
		case model.AbilityRoleBackup:
			backups = append(backups, source)
			if !source.Retreating {
				availableBackups = append(availableBackups, source)
			}
		default:
			primaries = append(primaries, source)
			if !source.Retreating {
				availablePrimaries = append(availablePrimaries, source)
			}
		}
	}
	if len(availablePrimaries) > 0 {
		return availablePrimaries, false
	}
	if len(availableBackups) > 0 {
		return availableBackups, true
	}
	// Everything is in retreat, which is exactly when the router stops
	// honouring the breaker and tries anyway. The page says the same thing the
	// router does rather than inventing a state neither of them is in.
	if len(primaries) > 0 {
		return primaries, false
	}
	return backups, len(backups) > 0
}

// channelHealthProviderAccumulator collects one upstream's evidence across
// every route it serves.
type channelHealthProviderAccumulator struct {
	view          channelHealthProviderView
	models        map[string]bool
	trafficErrors int64
	probeErrors   int64
	outputTokens  int64
	decodeMs      int64
	durationSum   int64
	durationCount int64
	worstStatus   string
}

// foldChannelHealthProviders re-folds the very same lane sources by upstream.
// It reads only what foldChannelHealth already resolved per source, so the
// provider view can never contradict the lane view: same evidence, different
// grouping.
//
// A provider's status is the worst of its routes, because a key that is dead
// on one model is usually dead on all of them and is worth one look.
func foldChannelHealthProviders(
	lanes []channelHealthLaneView,
	grid []model.ChannelHealthLaneCell,
	percentiles []model.ChannelHealthChannelPercentileRow,
	lastProbes []model.ChannelHealthLastProbeRow,
	buckets []model.ChannelHealthBucketRow,
	currentCells map[string]bool,
	since time.Time,
	bucketSeconds int64,
	bucketCount int,
) []channelHealthProviderView {
	providers := map[int]*channelHealthProviderAccumulator{}
	ensure := func(channelID int, channelName string) *channelHealthProviderAccumulator {
		if existing, ok := providers[channelID]; ok {
			return existing
		}
		created := &channelHealthProviderAccumulator{
			view: channelHealthProviderView{
				ChannelID:   channelID,
				ChannelName: channelName,
				SourceType:  model.ChannelSourceUnclassified,
				Status:      publicStatusUnknown,
				Source:      channelHealthSourceNone,
			},
			models: map[string]bool{},
		}
		providers[channelID] = created
		return created
	}

	// The grid names every enabled upstream, including ones with no evidence
	// at all. An operator has to see an idle source as idle rather than have
	// it vanish from the ledger.
	for _, cell := range grid {
		acc := ensure(cell.ChannelId, cell.ChannelName)
		acc.view.SourceType = model.NormalizeChannelSourceType(cell.SourceType)
		acc.view.ChannelType = cell.ChannelType
		if cell.Weight > acc.view.Weight {
			acc.view.Weight = cell.Weight
		}
		if cell.ChannelStatus != common.ChannelStatusEnabled {
			acc.view.Disabled = true
		}
	}

	var platformTraffic int64
	for _, lane := range lanes {
		for _, source := range lane.Sources {
			if source.excludedFromCurrent {
				// History on a route this upstream no longer serves. It is
				// already visible on the lane; the upstream's card describes
				// what it serves now.
				continue
			}
			acc := ensure(source.ChannelID, source.ChannelName)
			acc.models[lane.ModelName] = true
			acc.view.LaneCount++
			acc.view.Lanes = append(acc.view.Lanes, channelHealthProviderLaneView{
				ModelName:    lane.ModelName,
				Lane:         lane.Lane,
				Status:       source.Status,
				Source:       source.Source,
				SuccessRate:  source.SuccessRate,
				RequestCount: source.RequestCount,
				Share:        source.Share,
				Weight:       source.Weight,
				Disabled:     source.Disabled,
			})
			acc.worstStatus = worstChannelHealthStatus(acc.worstStatus, source.Status)
			acc.view.RequestCount += source.RequestCount
			acc.view.ProbeCount += source.ProbeCount
			acc.trafficErrors += source.trafficErrors
			acc.probeErrors += source.probeErrors
			acc.outputTokens += source.outputTokens
			acc.decodeMs += source.decodeMs
			acc.durationSum += source.durationSumMs
			acc.durationCount += source.durationCount
			acc.view.LastOutcomeAt = laterChannelHealthTime(acc.view.LastOutcomeAt, source.lastTrafficAt)
			acc.view.LastProbedAt = laterChannelHealthTime(acc.view.LastProbedAt, source.lastProbedAt)
			platformTraffic += source.RequestCount
		}
	}

	p95ByChannel := map[int]float64{}
	for _, row := range percentiles {
		p95ByChannel[row.ChannelId] = row.P95LatencyMs
	}
	probeByChannel := map[int]model.ChannelHealthLastProbeRow{}
	for _, row := range lastProbes {
		probeByChannel[row.ChannelId] = row
	}
	bucketsByChannel := foldChannelHealthProviderBuckets(buckets, currentCells, since, bucketSeconds, bucketCount)

	result := make([]channelHealthProviderView, 0, len(providers))
	for channelID, acc := range providers {
		view := acc.view
		view.ErrorCount = acc.trafficErrors
		view.ModelCount = len(acc.models)
		samples := view.RequestCount + view.ProbeCount
		switch {
		case view.RequestCount > 0:
			view.Source = channelHealthSourceTraffic
		case view.ProbeCount > 0:
			view.Source = channelHealthSourceProbe
		}
		view.SuccessRate = successRate(samples, acc.trafficErrors+acc.probeErrors)
		if acc.durationCount > 0 {
			avg := float64(acc.durationSum) / float64(acc.durationCount)
			view.AvgLatencyMs = &avg
		}
		if view.RequestCount > 0 {
			view.P95LatencyMs = positiveChannelHealthFigure(p95ByChannel[channelID])
		}
		if acc.decodeMs > 0 && acc.outputTokens > 0 {
			tps := float64(acc.outputTokens) * 1000 / float64(acc.decodeMs)
			view.OutputTPS = &tps
		}
		if acc.worstStatus != "" {
			view.Status = acc.worstStatus
		}

		if probe, ok := probeByChannel[channelID]; ok {
			if probedAt := probe.ProbedAt.Ptr(); probedAt != nil {
				view.LastProbe = &channelHealthLastProbeView{
					ProbedAt:  *probedAt,
					ModelName: probe.ModelName,
					Lane:      catalogLaneKey(probe.Lane),
					Success:   probe.Success,
					LatencyMs: probe.LatencyMs,
					Error:     probe.ErrorMessage,
				}
			}
		}

		view.Buckets = bucketsByChannel[channelID]
		if view.Buckets == nil {
			view.Buckets = emptyChannelHealthBuckets(since, bucketSeconds, bucketCount)
		}
		sort.Slice(view.Lanes, func(i, j int) bool {
			if view.Lanes[i].ModelName != view.Lanes[j].ModelName {
				return view.Lanes[i].ModelName < view.Lanes[j].ModelName
			}
			return view.Lanes[i].Lane > view.Lanes[j].Lane
		})
		result = append(result, view)
	}

	if platformTraffic > 0 {
		for i := range result {
			if result[i].RequestCount == 0 {
				continue
			}
			share := float64(result[i].RequestCount) / float64(platformTraffic)
			result[i].Share = &share
		}
	}

	// Worst first: the ledger exists to be triaged, and an operator opening it
	// during an incident should not have to hunt for the red card.
	sort.Slice(result, func(i, j int) bool {
		left := channelHealthStatusSeverity(result[i].Status)
		right := channelHealthStatusSeverity(result[j].Status)
		if left != right {
			return left > right
		}
		if result[i].RequestCount != result[j].RequestCount {
			return result[i].RequestCount > result[j].RequestCount
		}
		return result[i].ChannelName < result[j].ChannelName
	})
	return result
}

func worstChannelHealthStatus(left, right string) string {
	if channelHealthStatusSeverity(left) > channelHealthStatusSeverity(right) {
		return left
	}
	return right
}

func channelHealthStatusSeverity(status string) int {
	switch status {
	case publicStatusOutage:
		return 3
	case publicStatusDegraded:
		return 2
	case publicStatusUnknown:
		return 1
	default:
		return 0
	}
}

// foldChannelHealthProviderBuckets sums an upstream's slices across every route
// it serves now, traffic and probes alike.
func foldChannelHealthProviderBuckets(
	rows []model.ChannelHealthBucketRow,
	currentCells map[string]bool,
	since time.Time,
	bucketSeconds int64,
	bucketCount int,
) map[int][]channelHealthBucketView {
	type cell struct {
		trafficCount, trafficError, probeCount, probeError int64
	}
	grouped := map[int]map[int]*cell{}
	for _, row := range rows {
		if !currentCells[model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)] {
			continue
		}
		if grouped[row.ChannelId] == nil {
			grouped[row.ChannelId] = map[int]*cell{}
		}
		entry, ok := grouped[row.ChannelId][row.BucketIndex]
		if !ok {
			entry = &cell{}
			grouped[row.ChannelId][row.BucketIndex] = entry
		}
		if row.Kind == model.ChannelHealthKindProbe {
			entry.probeCount += row.RequestCount
			entry.probeError += row.ErrorCount
			continue
		}
		entry.trafficCount += row.RequestCount
		entry.trafficError += row.ErrorCount
	}

	result := make(map[int][]channelHealthBucketView, len(grouped))
	for channelID, slices := range grouped {
		views := emptyChannelHealthBuckets(since, bucketSeconds, bucketCount)
		for index, entry := range slices {
			if index < 0 || index >= len(views) {
				continue
			}
			verdict := channelHealthMixedVerdict(
				entry.trafficCount, entry.trafficError, entry.probeCount, entry.probeError,
			)
			if verdict == nil {
				continue
			}
			views[index].Source = verdict.source
			views[index].RequestCount = verdict.requestCount
			views[index].ErrorCount = verdict.errorCount
			views[index].SuccessRate = verdict.rate
			views[index].Tone = verdict.tone
		}
		result[channelID] = views
	}
	return result
}

// channelHealthCellVerdict is what one upstream's evidence says about one
// time slice.
type channelHealthCellVerdict struct {
	source       string
	requestCount int64
	errorCount   int64
	rate         *float64
	tone         string
}

// channelHealthSourceCell accumulates one upstream's counters inside one slice.
type channelHealthSourceCell struct {
	trafficCount int64
	trafficError int64
	probeCount   int64
	probeError   int64
}

// verdict reduces one slice of one upstream to a single judgement, or nil when
// that upstream produced no evidence in the slice at all.
func (c *channelHealthSourceCell) verdict() *channelHealthCellVerdict {
	if c == nil {
		return nil
	}
	return channelHealthMixedVerdict(c.trafficCount, c.trafficError, c.probeCount, c.probeError)
}

// channelHealthMixedVerdict sums traffic and probe samples into one slice
// verdict. The source label says what was present — traffic whenever any
// customer request landed in the slice — but both kinds are counted.
func channelHealthMixedVerdict(trafficCount, trafficError, probeCount, probeError int64) *channelHealthCellVerdict {
	count := trafficCount + probeCount
	if count <= 0 {
		return nil
	}
	source := channelHealthSourceProbe
	if trafficCount > 0 {
		source = channelHealthSourceTraffic
	}
	errors := trafficError + probeError
	rate := successRate(count, errors)
	return &channelHealthCellVerdict{
		source:       source,
		requestCount: count,
		errorCount:   errors,
		rate:         rate,
		tone:         channelHealthTone(rate, errors, source),
	}
}

// foldChannelHealthBuckets turns raw per-slice counters into the wall of cells
// the page draws.
//
// The important property is that a cell reports a STATE, not an event. A slice
// with no evidence of its own shows the verdict still standing from the
// previous one, marked as carried, until that verdict ages out. Only then does
// a cell go blank — and a blank cell then genuinely means nobody knows.
func foldChannelHealthBuckets(
	rows []model.ChannelHealthBucketRow,
	currentCells map[string]bool,
	blockedCells map[string]model.ChannelHealthUnprobeableRow,
	since time.Time,
	bucketSeconds int64,
	bucketCount int,
) map[string][]channelHealthBucketView {
	grouped := map[string]map[int]map[int]*channelHealthSourceCell{}
	laneChannels := map[string]map[int]bool{}
	register := func(modelName, lane string, channelID int) string {
		key := model.ChannelHealthLaneKey(modelName, lane)
		if grouped[key] == nil {
			grouped[key] = map[int]map[int]*channelHealthSourceCell{}
			laneChannels[key] = map[int]bool{}
		}
		laneChannels[key][channelID] = true
		return key
	}

	for _, row := range rows {
		if !currentCells[model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)] {
			continue
		}
		key := register(row.ModelName, row.Lane, row.ChannelId)
		if grouped[key][row.BucketIndex] == nil {
			grouped[key][row.BucketIndex] = map[int]*channelHealthSourceCell{}
		}
		entry, ok := grouped[key][row.BucketIndex][row.ChannelId]
		if !ok {
			entry = &channelHealthSourceCell{}
			grouped[key][row.BucketIndex][row.ChannelId] = entry
		}
		if row.Kind == model.ChannelHealthKindProbe {
			entry.probeCount += row.RequestCount
			entry.probeError += row.ErrorCount
			continue
		}
		entry.trafficCount += row.RequestCount
		entry.trafficError += row.ErrorCount
	}

	// A route we cannot probe at all produces no rows above, so a lane served
	// only by unprobeable sources would not appear here and would report an
	// empty wall. Register those upstreams so their blind spot is drawn.
	for cellKey, blocked := range blockedCells {
		if !currentCells[cellKey] {
			continue
		}
		register(blocked.ModelName, blocked.Lane, blocked.ChannelId)
	}

	result := make(map[string][]channelHealthBucketView, len(grouped))
	for key, cells := range grouped {
		views := emptyChannelHealthBuckets(since, bucketSeconds, bucketCount)
		for channelID := range laneChannels[key] {
			blocked := blockedCells[channelHealthLaneCellKey(key, channelID)]
			isBlocked := strings.TrimSpace(blocked.Reason) != "" || blocked.SkippedAt.Valid
			timeline := channelHealthChannelTimeline(cells, channelID, isBlocked, bucketCount)
			for index := range timeline {
				if timeline[index] == nil {
					continue
				}
				mergeChannelHealthBucketVerdict(&views[index], *timeline[index], channelID)
			}
		}
		result[key] = views
	}
	return result
}

// channelHealthLaneCellKey rebuilds a cell key from a lane key and a channel,
// so the blocked lookup shares the key space every other map here uses.
func channelHealthLaneCellKey(laneKey string, channelID int) string {
	parts := strings.SplitN(laneKey, "\x1f", 2)
	if len(parts) != 2 {
		return ""
	}
	return model.ChannelHealthCellKey(parts[0], parts[1], channelID)
}

// channelHealthChannelTimeline resolves one upstream's own row of cells
// before any cross-upstream comparison happens.
//
// A cell shows only what was measured inside its own slice. Slices with no
// sample used to carry the previous verdict forward, drawn at full strength
// once the wall stopped fading them — which painted an hour nobody looked at
// as a fresh success. The probe cadence is set so that every slice gets a
// sample; when one does not, the honest mark is "no sample".
func channelHealthChannelTimeline(
	cells map[int]map[int]*channelHealthSourceCell,
	channelID int,
	isBlocked bool,
	bucketCount int,
) []*channelHealthCellVerdict {
	timeline := make([]*channelHealthCellVerdict, bucketCount)
	for index := 0; index < bucketCount; index++ {
		verdict := cells[index][channelID].verdict()
		if verdict != nil {
			timeline[index] = verdict
			continue
		}
		if isBlocked {
			// Nothing was observed and nothing could have been: say so rather
			// than leaving a blank that reads as an idle route.
			timeline[index] = &channelHealthCellVerdict{
				source: channelHealthSourceBlocked,
				tone:   channelHealthToneBlocked,
			}
		}
	}
	return timeline
}

// mergeChannelHealthBucketVerdict folds one upstream's cell into the lane's.
//
// The lane shows its worst live source, because a lane is only as good as the
// pool behind it. Ties are broken toward the stronger evidence — measured
// traffic over a synthetic probe, a real check over a carried one — so a cell
// never claims more certainty than it has.
func mergeChannelHealthBucketVerdict(
	view *channelHealthBucketView, verdict channelHealthCellVerdict, channelID int,
) {
	severity := channelHealthToneSeverity(verdict.tone)
	current := channelHealthToneSeverity(view.Tone)
	strength := channelHealthEvidenceStrength(verdict.source)
	currentStrength := channelHealthEvidenceStrength(view.Source)
	rate := 2.0
	if verdict.rate != nil {
		rate = *verdict.rate
	}
	currentRate := 2.0
	if view.SuccessRate != nil {
		currentRate = *view.SuccessRate
	}
	better := severity > current ||
		(severity == current && strength > currentStrength) ||
		(severity == current && strength == currentStrength && rate < currentRate) ||
		(severity == current && strength == currentStrength && rate == currentRate &&
			channelID < view.decidedBy)
	if !better {
		return
	}
	view.Source = verdict.source
	view.RequestCount = verdict.requestCount
	view.ErrorCount = verdict.errorCount
	view.SuccessRate = verdict.rate
	view.Tone = verdict.tone
	view.decidedBy = channelID
}

// channelHealthEvidenceStrength ranks how much a cell's colour can be trusted.
func channelHealthEvidenceStrength(source string) int {
	switch source {
	case channelHealthSourceTraffic:
		return 3
	case channelHealthSourceProbe:
		return 2
	default:
		return 0
	}
}

func channelHealthToneSeverity(tone string) int {
	switch tone {
	case "bad":
		return 4
	case "warn":
		return 3
	case "ok":
		return 2
	case channelHealthToneBlocked:
		// Above "nothing known" and below every measurement: a blind spot is
		// worth surfacing, but one source we can actually see and that is
		// healthy still decides the slice.
		return 1
	default:
		return 0
	}
}

// channelHealthBucketCountFor is how many slices a window is drawn in: the
// week in two-hour slices, anything shorter hourly.
func channelHealthBucketCountFor(window time.Duration) int {
	if window >= 7*24*time.Hour {
		return channelHealthWideBucketCount
	}
	return channelHealthBucketCount
}

// slices is the fold's slice count, reading an unset one as the day's.
func (input channelHealthFoldInput) slices() int {
	if input.bucketCount > 0 {
		return input.bucketCount
	}
	return channelHealthBucketCount
}

// slices is the snapshot's slice count, reading one written before the
// count was stored as the day's.
func (snapshot channelHealthSnapshot) slices() int {
	if snapshot.bucketCount > 0 {
		return snapshot.bucketCount
	}
	return channelHealthBucketCount
}

func emptyChannelHealthBuckets(since time.Time, bucketSeconds int64, bucketCount int) []channelHealthBucketView {
	views := make([]channelHealthBucketView, bucketCount)
	for i := range views {
		views[i] = channelHealthBucketView{
			Index:   i,
			StartAt: since.Add(time.Duration(int64(i)*bucketSeconds) * time.Second),
			Source:  channelHealthSourceNone,
			Tone:    "none",
		}
	}
	return views
}

// channelHealthTone is computed server-side so the status bars, the plaza dot
// and any future surface colour a slice the same way.
//
// Aggregate counts do not reveal ordering or prove a breaker trip. Red is
// reserved for repeated failures with no successful sample in the slice;
// mixed evidence stays amber (or green above the availability threshold).
func channelHealthTone(rate *float64, errors int64, source string) string {
	if rate == nil || source == channelHealthSourceNone {
		return "none"
	}
	down := *rate == 0 && errors >= 2
	switch {
	case *rate >= channelHealthDegradedRate:
		return "ok"
	case down && *rate < channelHealthOutageRate:
		return "bad"
	default:
		return "warn"
	}
}

func successRate(total int64, errors int64) *float64 {
	if total <= 0 {
		return nil
	}
	rate := 1 - float64(errors)/float64(total)
	if rate < 0 {
		rate = 0
	}
	return &rate
}

func sortedChannelHealthSources(sources map[int]*channelHealthSourceView, laneTotal int64) []channelHealthSourceView {
	if len(sources) == 0 {
		return nil
	}
	result := make([]channelHealthSourceView, 0, len(sources))
	for _, source := range sources {
		if laneTotal > 0 && source.RequestCount > 0 {
			share := float64(source.RequestCount) / float64(laneTotal)
			source.Share = &share
		}
		result = append(result, *source)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].RequestCount != result[j].RequestCount {
			return result[i].RequestCount > result[j].RequestCount
		}
		return result[i].ChannelName < result[j].ChannelName
	})
	return result
}

func summarizeChannelHealth(rows []channelHealthLaneView) channelHealthSummaryView {
	summary := channelHealthSummaryView{}
	models := map[string]struct{}{}
	var stableTotal, stableErrors, ecoTotal, ecoErrors int64
	var outputTokens, decodeMs float64

	for _, row := range rows {
		if row.Kind == channelHealthKindSearch {
			// A search backend has no lane and no throughput; it is counted
			// only toward the issues an operator needs to see.
			switch row.Status {
			case publicStatusOutage:
				summary.OutageCount++
			case publicStatusDegraded:
				summary.DegradedCount++
			}
			continue
		}
		models[row.ModelName] = struct{}{}
		summary.RequestCount += row.RequestCount
		switch row.Status {
		case publicStatusOutage:
			summary.OutageCount++
		case publicStatusDegraded:
			summary.DegradedCount++
		case publicStatusUnknown:
			summary.NoDataCount++
		}
		summary.LaneCount++
		if row.FallbackActive {
			summary.FallbackCount++
		}
		summary.BlockedCount += row.BlockedCount
		samples := row.RequestCount + row.ProbeCount
		errors := row.ErrorCount + row.ProbeErrorCount
		if row.Lane == catalogLaneKey(model.ChannelLaneAgent) {
			ecoTotal += samples
			ecoErrors += errors
		} else {
			stableTotal += samples
			stableErrors += errors
		}
		// Sum the throughput inputs and divide once. Averaging each lane's
		// ratio would give a short-response lane the same weight as one
		// decoding far more tokens over far longer.
		outputTokens += float64(row.OutputTokens)
		decodeMs += float64(row.DecodeMs)
	}

	summary.ModelCount = len(models)
	summary.ActiveIssues = summary.DegradedCount + summary.OutageCount
	summary.StableSuccessRate = successRate(stableTotal, stableErrors)
	summary.EcoSuccessRate = successRate(ecoTotal, ecoErrors)
	if decodeMs > 0 {
		tps := outputTokens * 1000 / decodeMs
		summary.OutputTPS = &tps
	}
	return summary
}

// loadCatalogHealthByLane is the plaza's view of each lane: the health page's
// week, read from the same cached view, then brought up to the breaker's
// verdict. Search backends are not catalog models and stay out.
func loadCatalogHealthByLane(modelNames []string) map[string]map[string]dto.ModelCatalogHealth {
	result := map[string]map[string]dto.ModelCatalogHealth{}
	if len(modelNames) == 0 {
		return result
	}
	snapshot, err := loadChannelHealthSnapshot("7d", catalogHealthWindow)
	if err != nil {
		common.SysLog("catalog health: " + err.Error())
		return result
	}
	wanted := stringSet(modelNames)
	for _, row := range snapshot.rows {
		if row.Kind == channelHealthKindSearch || !wanted[row.ModelName] {
			continue
		}
		if result[row.ModelName] == nil {
			result[row.ModelName] = map[string]dto.ModelCatalogHealth{}
		}
		result[row.ModelName][row.Lane] = catalogHealthFromLane(row)
	}
	grid, err := model.LoadChannelHealthGrid()
	if err != nil {
		return result
	}
	states, err := model.LoadRouteHealthStates()
	if err != nil {
		return result
	}
	return overlayCatalogHealthBreaker(result, grid, states, channelHealthCatalogModels(), time.Now().UTC())
}

// overlayCatalogHealthBreaker brings the plaza's lane status up to the
// breaker's current verdict. The catalog snapshot is rebuilt every ten
// minutes, and the plaza trusts a published status over the window's
// availability, so without this a lane the router pulled a minute ago would
// stay green there while the router and the console already call it down.
//
// Only status moves, with the same rules as the console: a route that tripped
// is down, one whose newest sample failed is degraded, and a lane is down when
// every route the router would choose is down. A route in good standing keeps
// the snapshot's verdict of whether the lane had evidence at all.
func overlayCatalogHealthBreaker(
	health map[string]map[string]dto.ModelCatalogHealth,
	grid []model.ChannelHealthLaneCell,
	states []model.RouteHealthState,
	catalog map[string]bool,
	now time.Time,
) map[string]map[string]dto.ModelCatalogHealth {
	if len(health) == 0 {
		return health
	}
	breaker := make(map[string]model.RouteHealthState, len(states))
	for _, state := range states {
		breaker[model.RouteHealthCacheKey(state.ModelName, state.Lane, state.ChannelId)] = state
	}
	type laneRef struct{ model, lane string }
	pools := map[laneRef]map[int]*channelHealthSourceView{}
	for _, cell := range grid {
		if cell.ChannelStatus != common.ChannelStatusEnabled ||
			(cell.AbilityEnabled != nil && !*cell.AbilityEnabled) {
			continue
		}
		name, published := channelHealthCatalogName(catalog, cell.ModelName)
		if !published {
			continue
		}
		ref := laneRef{name, catalogLaneKey(model.NormalizeChannelLane(cell.Lane))}
		if _, ok := health[ref.model][ref.lane]; !ok {
			continue
		}
		if pools[ref] == nil {
			pools[ref] = map[int]*channelHealthSourceView{}
		}
		source, ok := pools[ref][cell.ChannelId]
		if !ok {
			source = &channelHealthSourceView{ChannelID: cell.ChannelId, Role: cell.Role}
			pools[ref][cell.ChannelId] = source
		}
		state := breaker[model.RouteHealthCacheKey(cell.ModelName, cell.Lane, cell.ChannelId)]
		if state.ConsecutiveFailures > source.ConsecutiveFailures {
			source.ConsecutiveFailures = state.ConsecutiveFailures
		}
		if state.IsRetreating(now) {
			source.Retreating = true
		}
	}
	for ref, pool := range pools {
		current := health[ref.model][ref.lane]
		sources := make([]channelHealthSourceView, 0, len(pool))
		for _, source := range pool {
			switch {
			case source.Retreating || source.ConsecutiveFailures >= model.RouteHealthFailureThreshold:
				source.Status = publicStatusOutage
			case source.ConsecutiveFailures > 0:
				source.Status = publicStatusDegraded
			case current.Status == "unknown":
				source.Status = publicStatusUnknown
			default:
				source.Status = publicStatusOperational
			}
			sources = append(sources, *source)
		}
		serving, _ := electChannelHealthServingSources(sources)
		current.Status = catalogHealthStatusFromPublic(channelHealthLaneStatus(serving))
		health[ref.model][ref.lane] = current
	}
	return health
}

func catalogHealthFromLane(row channelHealthLaneView) dto.ModelCatalogHealth {
	health := dto.ModelCatalogHealth{
		Status:         catalogHealthStatusFromPublic(row.Status),
		ObservedWindow: catalogHealthObservedWindow(row.Source),
		BucketSeconds:  int64((catalogHealthWindow / catalogHealthBucketCount) / time.Second),
		Buckets:        compactCatalogHealthBuckets(row.Buckets),
	}
	if row.SuccessRate != nil {
		availability := *row.SuccessRate
		health.Availability = &availability
	}
	if row.P95LatencyMs != nil {
		health.LatencyP95Ms = intValuePtr(int(*row.P95LatencyMs + 0.5))
	}
	// LatencyP50Ms is deliberately left unset: the lane aggregate carries a
	// mean, and publishing a mean under a percentile label would misstate it
	// for every consumer of the catalog.
	if row.OutputTPS != nil {
		tps := *row.OutputTPS
		health.OutputTPS = &tps
	}
	return health
}

func compactCatalogHealthBuckets(buckets []channelHealthBucketView) []dto.ModelCatalogHealthBucket {
	result := make([]dto.ModelCatalogHealthBucket, catalogHealthBucketCount)
	for index := range result {
		result[index] = dto.ModelCatalogHealthBucket{
			Index:  index,
			Source: channelHealthSourceNone,
			Tone:   "none",
		}
	}
	if len(buckets) == 0 {
		return result
	}

	for index := range result {
		start := index * len(buckets) / catalogHealthBucketCount
		end := (index + 1) * len(buckets) / catalogHealthBucketCount
		if end <= start {
			continue
		}
		group := buckets[start:end]
		result[index].StartAt = group[0].StartAt

		// Sum every sample in the slice so one sparse bad hour cannot
		// overwrite thousands of successful calls. Carried and blocked marks
		// hold no samples of their own and do not enter the sum.
		var requestCount, errorCount int64
		for _, bucket := range group {
			switch bucket.Source {
			case channelHealthSourceTraffic, channelHealthSourceProbe:
			default:
				continue
			}
			requestCount += bucket.RequestCount
			errorCount += bucket.ErrorCount
		}
		if requestCount == 0 {
			continue
		}
		rate := successRate(requestCount, errorCount)
		result[index].Source = channelHealthSourceSample
		result[index].SuccessRate = rate
		result[index].Tone = channelHealthTone(rate, errorCount, channelHealthSourceSample)
	}
	return result
}

// catalogHealthObservedWindow is the catalog's account of a lane's evidence.
// The catalog reaches every API consumer, so it says whether the lane was
// measured and never whether a customer or our probe did the measuring.
func catalogHealthObservedWindow(source string) string {
	switch source {
	case channelHealthSourceTraffic, channelHealthSourceProbe, channelHealthSourceSample:
		return channelHealthSourceSample
	default:
		return channelHealthSourceNone
	}
}

// catalogHealthStatusFromPublic maps the internal status vocabulary onto the
// three values the catalog DTO has always used.
func catalogHealthStatusFromPublic(status string) string {
	switch status {
	case publicStatusOperational:
		return "available"
	case publicStatusDegraded:
		return "degraded"
	case publicStatusOutage:
		return "unavailable"
	default:
		return "unknown"
	}
}

// orderChannelHealthRows sorts rows the way the catalog reads — maker order,
// newest release first — keeping each model's lanes in their existing order.
func orderChannelHealthRows(rows []channelHealthLaneView) {
	facts := make(map[string]model.Pricing)
	for _, pricing := range model.GetPricing() {
		facts[pricing.ModelName] = pricing
	}
	for i := range rows {
		if pricing, ok := facts[rows[i].ModelName]; ok {
			rows[i].Maker = pricing.Maker
			rows[i].ReleaseDate = pricing.ReleaseDate
		} else {
			rows[i].Maker = model.ResolveModelMaker("", rows[i].ModelName, "")
		}
		rows[i].MakerRank = model.ModelMakerRank(rows[i].Maker)
	}
	model.SortByModelOrder(rows, func(row channelHealthLaneView) model.ModelOrderKey {
		return model.ModelOrderKey{Name: row.ModelName, Maker: row.Maker, ReleaseDate: row.ReleaseDate}
	})
}
