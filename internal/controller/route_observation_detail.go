package controller

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// The detail view draws a watched route's observation as two-hour slots, the
// same grain as the public status page, starting from the slot its watch
// began in.
const observationSlotSeconds = 2 * 60 * 60

// observationSlots is how many slots cover the whole observation.
func observationSlots() int {
	return model.RouteObservationDays() * 24 * 60 * 60 / observationSlotSeconds
}

// A route's verdict for the operator: ready can go live now, problem has
// something failing right now, watching is everything in between.
const (
	observationVerdictReady    = "ready"
	observationVerdictWatching = "watching"
	observationVerdictProblem  = "problem"
)

// observationProblemOrder ranks what holds a route back, most serious first:
// a route that rarely answers says more than a check it failed once.
var observationProblemOrder = []string{"probe", model.ObservationCheckTools, model.ObservationCheckBilling, model.ObservationCheckLongContext}

type observationReason struct {
	Kind   string   `json:"kind"`
	Detail string   `json:"detail,omitempty"`
	Value  *float64 `json:"value,omitempty"`
}

type observationProblem struct {
	Model string `json:"model"`
	observationReason
}

// observationSummary is a source's watched routes counted the way the ledger
// line reads them: how many passed every check, which ones have a problem
// and why, and how many are still waiting for a check to run.
type observationSummary struct {
	AllPassed    int                  `json:"all_passed"`
	Ready        int                  `json:"ready"`
	ProblemCount int                  `json:"problem_count"`
	Pending      int                  `json:"pending"`
	Problems     []observationProblem `json:"problems"`
	// WorstProbe is the watched model whose probes did worst in the window,
	// when it fell below the bar; the ledger names it beside the source's
	// pooled figure so one dead model does not read as a flaky source.
	WorstProbe *observationWindowProbe `json:"worst_probe,omitempty"`
}

type observationWindowProbe struct {
	Model    string `json:"model"`
	Samples  int64  `json:"samples"`
	Failures int64  `json:"failures"`
}

type observationSlot struct {
	Samples  int64 `json:"samples"`
	Failures int64 `json:"failures"`
}

type observationRoute struct {
	Model       string             `json:"model"`
	Lane        string             `json:"lane"`
	Observation observationView    `json:"observation"`
	Verdict     string             `json:"verdict"`
	Reason      *observationReason `json:"reason,omitempty"`
	// SlotOrigin is where slot 0 starts. Slots covers origin to now; the
	// console pads the rest of the observation as not yet reached.
	SlotOrigin time.Time         `json:"slot_origin"`
	Slots      []observationSlot `json:"slots"`
	// Days holds, for each observation day so far, the last result of each
	// check that day: pass, fail or na. A check that has not run that day is
	// absent.
	Days []map[string]string `json:"days"`
}

// observationChecksPassed reports whether every check has passed (or does
// not apply). Waiting checks do not count as passed.
func observationChecksPassed(view observationView) bool {
	if len(view.Checks) == 0 {
		return false
	}
	for _, check := range view.Checks {
		if check.State != observationStatePass && check.State != observationStateNA {
			return false
		}
	}
	return true
}

// observationProbeHopeless reports whether a route's probe record can no
// longer reach the success rate the observation requires. Midway, one early
// failure on an otherwise clean record is not a problem — the observation
// still has room to absorb it — but a route that has failed so often that
// even a clean remainder of the observation cannot lift it to the bar is.
func observationProbeHopeless(view observationView, now time.Time) bool {
	if view.SuccessState != observationStateFail {
		return false
	}
	if view.Complete {
		return true
	}
	window := common.AppConfig.SourceChecks.Observation()
	elapsed := now.Sub(view.StartedAt)
	if elapsed <= 0 || view.Samples <= 0 {
		return false
	}
	// Probes keep their pace for the rest of the observation.
	projected := float64(view.Samples) * float64(window) / float64(elapsed)
	return 1-float64(view.Failures)/projected < common.AppConfig.SourceChecks.MinSuccessRate
}

// observationRouteVerdict says where one route stands and, for a problem,
// which part of its record is failing.
func observationRouteVerdict(view observationView, probeError string, now time.Time) (string, *observationReason) {
	if view.Ready {
		return observationVerdictReady, nil
	}
	reasons := map[string]*observationReason{}
	if observationProbeHopeless(view, now) {
		reasons["probe"] = &observationReason{Kind: "probe", Detail: probeError}
	}
	for _, check := range view.Checks {
		if check.State == observationStateFail {
			// A failing billing check reads by its latest run: the worst ratio
			// may be an old pass, and a run that billed nothing has no ratio.
			reasons[check.Kind] = &observationReason{Kind: check.Kind, Detail: check.Detail, Value: check.Latest}
		}
	}
	for _, kind := range observationProblemOrder {
		if reason, ok := reasons[kind]; ok {
			return observationVerdictProblem, reason
		}
	}
	return observationVerdictWatching, nil
}

// summarizeObservationRoutes counts a source's watched routes for its ledger
// line. Problems are ordered by observationProblemOrder, then by model name.
func summarizeObservationRoutes(rows []model.RouteObservation, views map[string]observationView, probeErrors map[string]string, window map[string]modelOpsHealth, now time.Time) *observationSummary {
	summary := &observationSummary{Problems: []observationProblem{}}
	worstRate := common.AppConfig.SourceChecks.MinSuccessRate
	for _, row := range rows {
		key := model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)
		if probe, ok := window[key]; ok && probe.RequestCount > 0 {
			rate := 1 - float64(probe.ErrorCount)/float64(probe.RequestCount)
			if rate < worstRate {
				worstRate = rate
				summary.WorstProbe = &observationWindowProbe{Model: row.ModelName, Samples: probe.RequestCount, Failures: probe.ErrorCount}
			}
		}
		view := views[key]
		verdict, reason := observationRouteVerdict(view, probeErrors[key], now)
		switch {
		case verdict == observationVerdictProblem:
			summary.ProblemCount++
			summary.Problems = append(summary.Problems, observationProblem{Model: row.ModelName, observationReason: *reason})
		case observationChecksPassed(view):
			summary.AllPassed++
		default:
			summary.Pending++
		}
		if verdict == observationVerdictReady {
			summary.Ready++
		}
	}
	rank := map[string]int{}
	for index, kind := range observationProblemOrder {
		rank[kind] = index
	}
	sort.SliceStable(summary.Problems, func(i, j int) bool {
		a, b := summary.Problems[i], summary.Problems[j]
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		return a.Model < b.Model
	})
	return summary
}

func observationSlotOrigin(startedAt time.Time) time.Time {
	return startedAt.UTC().Truncate(observationSlotSeconds * time.Second)
}

// buildObservationRoutes assembles the detail view of every watched route of
// one source.
func buildObservationRoutes(channelID int, now time.Time) ([]observationRoute, error) {
	views, watched, err := buildObservationViews([]int{channelID}, now)
	if err != nil || len(watched) == 0 {
		return nil, err
	}
	earliest := now
	origins := map[time.Time]bool{}
	for _, row := range watched {
		if row.StartedAt.Before(earliest) {
			earliest = row.StartedAt
		}
		origins[observationSlotOrigin(row.StartedAt)] = true
	}
	probeErrors, err := model.LoadRouteLastProbeErrors([]int{channelID}, earliest)
	if err != nil {
		return nil, err
	}
	checks, err := model.LoadRouteObservationChecks([]int{channelID}, earliest)
	if err != nil {
		return nil, err
	}
	// Routes of one source usually share a start; slice once per origin.
	slotsByOrigin := map[time.Time]map[string][]observationSlot{}
	for origin := range origins {
		buckets, err := model.LoadRouteProbeBuckets([]int{channelID}, origin, observationSlotSeconds)
		if err != nil {
			return nil, err
		}
		elapsed := int(now.Sub(origin)/(observationSlotSeconds*time.Second)) + 1
		if slots := observationSlots(); elapsed > slots {
			elapsed = slots
		}
		perRoute := map[string][]observationSlot{}
		for _, bucket := range buckets {
			if bucket.BucketIndex < 0 || bucket.BucketIndex >= elapsed {
				continue
			}
			key := model.ChannelHealthCellKey(bucket.ModelName, bucket.Lane, bucket.ChannelId)
			if perRoute[key] == nil {
				perRoute[key] = make([]observationSlot, elapsed)
			}
			perRoute[key][bucket.BucketIndex] = observationSlot{Samples: bucket.Samples, Failures: bucket.Failures}
		}
		perRoute[""] = make([]observationSlot, elapsed)
		slotsByOrigin[origin] = perRoute
	}

	routes := make([]observationRoute, 0, len(watched))
	for _, row := range watched {
		key := model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)
		view := views[key]
		verdict, reason := observationRouteVerdict(view, probeErrors[key], now)
		origin := observationSlotOrigin(row.StartedAt)
		slots := slotsByOrigin[origin][key]
		if slots == nil {
			slots = slotsByOrigin[origin][""]
		}
		days := make([]map[string]string, view.Day)
		for index := range days {
			days[index] = map[string]string{}
		}
		for _, check := range checks {
			if check.Skipped || check.ModelName != row.ModelName || model.NormalizeChannelLane(check.Lane) != row.Lane ||
				check.CheckedAt.Before(row.StartedAt) {
				continue
			}
			day := int(check.CheckedAt.Sub(row.StartedAt) / (24 * time.Hour))
			if day < 0 || day >= len(days) {
				continue
			}
			state := observationStateFail
			switch {
			case check.NotApplicable:
				state = observationStateNA
			case check.Passed:
				state = observationStatePass
			}
			days[day][check.Kind] = state
		}
		routes = append(routes, observationRoute{
			Model: row.ModelName, Lane: row.Lane, Observation: view,
			Verdict: verdict, Reason: reason, SlotOrigin: origin, Slots: slots, Days: days,
		})
	}
	// Ready first, then routes that have passed every check so far, then the
	// rest still waiting, then problems; each group by model name.
	group := func(route observationRoute) int {
		switch {
		case route.Verdict == observationVerdictReady:
			return 0
		case route.Verdict == observationVerdictProblem:
			return 3
		case observationChecksPassed(route.Observation):
			return 1
		}
		return 2
	}
	sort.SliceStable(routes, func(i, j int) bool {
		if group(routes[i]) != group(routes[j]) {
			return group(routes[i]) < group(routes[j])
		}
		if routes[i].Model != routes[j].Model {
			return routes[i].Model < routes[j].Model
		}
		return routes[i].Lane < routes[j].Lane
	})
	return routes, nil
}

// GetModelOpsSourceObservation backs the observation dialog: every watched
// route of one source with its observation drawn slot by slot and day by day.
func GetModelOpsSourceObservation(c *gin.Context) {
	channelID, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	channel, err := model.GetChannelById(channelID, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	now := time.Now().UTC()
	routes, err := buildObservationRoutes(channelID, now)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if len(routes) == 0 {
		httperr.Code(c, types.ErrorCodeRouteObservationNone, types.D{"channel_id": c.Param("id")})
		return
	}
	common.ApiSuccess(c, gin.H{
		"source": gin.H{
			"id": channel.PublicID, "name": channel.Name,
			"lane": model.NormalizeChannelLane(channel.Group), "status": channel.Status,
		},
		"now":          now,
		"slot_seconds": observationSlotSeconds,
		"slots":        observationSlots(),
		"routes":       routes,
	})
}

type removeObservedModelsRequest struct {
	Models []string `json:"models"`
}

// RemoveModelOpsSourceModels drops models that are still under watch from a
// source's model list. Only watched models may go this way: a model that
// already carries traffic on any lane of the source is refused, so the
// dialog cannot take a serving route out by accident.
func RemoveModelOpsSourceModels(c *gin.Context) {
	channelID, err := resolveChannelID(c.Param("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var req removeObservedModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeBadRequestBody)
		return
	}
	remove := map[string]bool{}
	for _, name := range req.Models {
		if name = strings.TrimSpace(name); name != "" {
			remove[name] = true
		}
	}
	if len(remove) == 0 {
		httperr.Invalid(c, "models")
		return
	}
	channel, err := model.GetChannelById(channelID, false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	_, watched, err := buildObservationViews([]int{channelID}, time.Now().UTC())
	if err != nil {
		httperr.Write(c, err)
		return
	}
	isWatched := map[string]bool{}
	for _, row := range watched {
		isWatched[row.ModelName] = true
	}
	var serving int64
	names := make([]string, 0, len(remove))
	for name := range remove {
		if !isWatched[name] {
			httperr.Code(c, types.ErrorCodeRouteObservationNone, types.D{"channel_id": c.Param("id"), "model": name})
			return
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if err := model.CoreDB().Model(&model.Ability{}).
		Where("channel_id = ? AND model IN ? AND role <> ?", channelID, names, model.AbilityRoleInventory).
		Count(&serving).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	if serving > 0 {
		httperr.Invalid(c, "models")
		return
	}
	before := channel.Models
	kept := make([]string, 0, len(channel.GetModels()))
	for _, name := range channel.GetModels() {
		if !remove[strings.TrimSpace(name)] {
			kept = append(kept, name)
		}
	}
	channel.Models = strings.Join(kept, ",")
	err = model.CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Channel{}).Where("id = ?", channelID).Update("models", channel.Models).Error; err != nil {
			return err
		}
		return channel.UpdateAbilities(tx)
	})
	if err != nil {
		httperr.Write(c, err)
		return
	}
	model.WriteAdminAuditLog(c, "model_ops.source.remove_watched_models", "channel",
		fmt.Sprintf("%d", channelID), c.Param("id"),
		gin.H{"models": before}, gin.H{"models": channel.Models, "removed": names})
	model.InitChannelCache()
	InvalidateChannelHealthCache()
	common.ApiSuccess(c, gin.H{"removed": names})
}
