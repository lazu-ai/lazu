package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
)

func healthGridCell(modelName, lane string, channelID int, name string, role string) model.ChannelHealthLaneCell {
	return model.ChannelHealthLaneCell{
		ModelName: modelName, Lane: lane, ChannelId: channelID, ChannelName: name,
		ChannelStatus: common.ChannelStatusEnabled, Weight: 100, Role: role,
	}
}

func healthEvidence(modelName, lane string, channelID int, kind string, requests, errors int64) model.ChannelHealthEvidenceRow {
	return model.ChannelHealthEvidenceRow{
		ModelName: modelName, Lane: lane, ChannelId: channelID, Kind: kind,
		RequestCount: requests, ErrorCount: errors,
		LastEvidenceAt: healthTime(time.Now().UTC().Add(-10 * time.Minute)),
	}
}

func foldHealthForTest(input channelHealthFoldInput) []channelHealthLaneView {
	if input.now.IsZero() {
		input.now = time.Now().UTC()
	}
	if input.since.IsZero() {
		input.since = input.now.Add(-24 * time.Hour)
	}
	if input.bucketSeconds == 0 {
		input.bucketSeconds = 3600
	}
	return foldChannelHealth(input)
}

// An enabled route nobody called and nobody probed survives as "no data".
// Dropping it would let an idle model look identical to a healthy one.
func TestFoldChannelHealthKeepsIdleRoutesAsNoData(t *testing.T) {
	rows := foldHealthForTest(channelHealthFoldInput{
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("claude-sonnet-5", model.ChannelLaneDirect, 7, "anthropic", model.AbilityRolePrimary),
		},
	})
	if len(rows) != 1 {
		t.Fatalf("expected 1 lane, got %d", len(rows))
	}
	row := rows[0]
	if row.Status != publicStatusUnknown || row.Source != channelHealthSourceNone || row.SuccessRate != nil {
		t.Fatalf("idle lane = %+v, want unknown with no rate", row)
	}
	if len(row.Buckets) != channelHealthBucketCount {
		t.Errorf("bucket count = %d, want %d", len(row.Buckets), channelHealthBucketCount)
	}
}

// A lane nobody called still gets every behaviour figure a probe can measure:
// that is the point of streaming the probe.
func TestFoldChannelHealthProbeOnlyLaneReportsProbeFigures(t *testing.T) {
	probe := healthEvidence("gpt-5.5", model.ChannelLaneDirect, 34, model.ChannelHealthKindProbe, 24, 0)
	probe.OutputTokens, probe.DecodeMs = 24*64, 24*800
	probe.DurationSumMs, probe.DurationCount = 24*1500, 24
	rows := foldHealthForTest(channelHealthFoldInput{
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("gpt-5.5", model.ChannelLaneDirect, 34, "reseller", model.AbilityRolePrimary),
		},
		evidence: []model.ChannelHealthEvidenceRow{probe},
		percentiles: []model.ChannelHealthPercentileRow{{
			ModelName: "gpt-5.5", Lane: model.ChannelLaneDirect,
			ProbeP95LatencyMs: 1800, TTFTP50Ms: 700, StallP50Ms: 120,
		}},
	})
	row := findLane(t, rows, "gpt-5.5", "stable")
	if row.Status != publicStatusOperational || row.Source != channelHealthSourceProbe {
		t.Fatalf("status/source = %s/%s, want operational/probe", row.Status, row.Source)
	}
	if row.SuccessRate == nil || *row.SuccessRate != 1 {
		t.Fatalf("success rate = %v, want 1", row.SuccessRate)
	}
	if row.RequestCount != 0 || row.ProbeCount != 24 {
		t.Fatalf("counts = %d traffic / %d probe, want 0/24", row.RequestCount, row.ProbeCount)
	}
	if row.TTFTP50Ms == nil || *row.TTFTP50Ms != 700 || row.StallP50Ms == nil || *row.StallP50Ms != 120 {
		t.Fatalf("ttft/stall = %v/%v, want 700/120", row.TTFTP50Ms, row.StallP50Ms)
	}
	if row.OutputTPS == nil || *row.OutputTPS != 80 {
		t.Fatalf("tps = %v, want 80 from 64 tokens per 800ms", row.OutputTPS)
	}
	if row.P95LatencyMs == nil || *row.P95LatencyMs != 1800 {
		t.Fatalf("p95 = %v, want the probe figure on a lane with no traffic", row.P95LatencyMs)
	}
	if row.InterruptionRate != nil {
		t.Fatalf("interruption = %v, want nil when nothing streamed", *row.InterruptionRate)
	}
}

// Traffic and probes are counted together for availability and speed, but the
// lane's P95 is the customers' own whenever there are any: a short probe reply
// would otherwise pull a long-answer lane's tail latency down.
func TestFoldChannelHealthMixesSamplesButKeepsTrafficP95(t *testing.T) {
	traffic := healthEvidence("gpt-6-astra", model.ChannelLaneAgent, 83, model.ChannelHealthKindTraffic, 96, 1)
	traffic.DurationCount = 95
	traffic.InterruptedCount, traffic.StreamedCount = 2, 40
	probe := healthEvidence("gpt-6-astra", model.ChannelLaneAgent, 83, model.ChannelHealthKindProbe, 4, 1)
	probe.DurationCount = 3
	rows := foldHealthForTest(channelHealthFoldInput{
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("gpt-6-astra", model.ChannelLaneAgent, 83, "poolhost", model.AbilityRolePrimary),
		},
		evidence: []model.ChannelHealthEvidenceRow{traffic, probe},
		percentiles: []model.ChannelHealthPercentileRow{{
			ModelName: "gpt-6-astra", Lane: model.ChannelLaneAgent,
			P95LatencyMs: 42000, ProbeP95LatencyMs: 1500,
		}},
	})
	row := findLane(t, rows, "gpt-6-astra", "eco")
	if row.SuccessRate == nil || *row.SuccessRate != 0.98 {
		t.Fatalf("success rate = %v, want 98/100 over both kinds", row.SuccessRate)
	}
	if row.RequestCount != 96 || row.ErrorCount != 1 || row.ProbeCount != 4 || row.ProbeErrorCount != 1 {
		t.Fatalf("counts = %+v", row)
	}
	if row.P95LatencyMs == nil || *row.P95LatencyMs != 42000 {
		t.Fatalf("p95 = %v, want the traffic figure", row.P95LatencyMs)
	}
	if row.InterruptionRate == nil || *row.InterruptionRate != 0.05 {
		t.Fatalf("interruption = %v, want 0.05", row.InterruptionRate)
	}
	if row.AggregateTotals == nil || row.AggregateTotals.Streamed != 40 || row.AggregateTotals.Interrupted != 2 || row.AggregateTotals.DurationCount != 98 {
		t.Fatalf("snapshot lost public aggregation denominators: %+v", row.AggregateTotals)
	}
}

// Status is the breaker's verdict, not the window's percentage. A route that
// failed for an hour this morning and has answered everything since is
// working now; a route whose newest sample failed is not, whatever the day
// looked like before.
func TestFoldChannelHealthStatusComesFromTheBreaker(t *testing.T) {
	now := time.Now().UTC()
	grid := []model.ChannelHealthLaneCell{
		healthGridCell("recovered", model.ChannelLaneDirect, 1, "a", model.AbilityRolePrimary),
		healthGridCell("failing", model.ChannelLaneDirect, 2, "b", model.AbilityRolePrimary),
		healthGridCell("pulled", model.ChannelLaneDirect, 3, "c", model.AbilityRolePrimary),
	}
	evidence := []model.ChannelHealthEvidenceRow{
		healthEvidence("recovered", model.ChannelLaneDirect, 1, model.ChannelHealthKindProbe, 24, 12),
		healthEvidence("failing", model.ChannelLaneDirect, 2, model.ChannelHealthKindTraffic, 1000, 1),
		healthEvidence("pulled", model.ChannelLaneDirect, 3, model.ChannelHealthKindProbe, 24, 3),
	}
	until := now.Add(3 * time.Minute)
	breaker := map[string]model.RouteHealthState{
		model.RouteHealthCacheKey("failing", model.ChannelLaneDirect, 2): {
			ModelName: "failing", Lane: model.ChannelLaneDirect, ChannelId: 2,
			State: model.RouteHealthStateHealthy, ConsecutiveFailures: 1,
		},
		model.RouteHealthCacheKey("pulled", model.ChannelLaneDirect, 3): {
			ModelName: "pulled", Lane: model.ChannelLaneDirect, ChannelId: 3,
			State: model.RouteHealthStateRetreat, ConsecutiveFailures: 3, RetreatUntil: &until,
		},
	}
	rows := foldHealthForTest(channelHealthFoldInput{grid: grid, evidence: evidence, breaker: breaker, now: now})

	if row := findLane(t, rows, "recovered", "stable"); row.Status != publicStatusOperational {
		t.Errorf("recovered status = %s, want operational despite a 50%% window", row.Status)
	}
	if row := findLane(t, rows, "failing", "stable"); row.Status != publicStatusDegraded {
		t.Errorf("failing status = %s, want degraded despite a 99.9%% window", row.Status)
	}
	pulled := findLane(t, rows, "pulled", "stable")
	if pulled.Status != publicStatusOutage {
		t.Errorf("pulled status = %s, want outage", pulled.Status)
	}
	if len(pulled.Sources) != 1 || !pulled.Sources[0].Retreating || pulled.Sources[0].ConsecutiveFailures != 3 {
		t.Errorf("pulled source = %+v, want the retreat visible to the operator", pulled.Sources)
	}
}

// A route that tripped the breaker is down even after its retreat lapses: the
// lapse only means the next sample decides. A lane served by one such route
// and one working route is degraded, not down and not fine.
func TestFoldChannelHealthTrippedRouteStaysDownAfterRetreatLapses(t *testing.T) {
	lapsed := time.Now().UTC().Add(-time.Minute)
	rows := foldHealthForTest(channelHealthFoldInput{
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("gpt-4o-mini", model.ChannelLaneDirect, 34, "dead-key", model.AbilityRolePrimary),
			healthGridCell("gpt-4o-mini", model.ChannelLaneDirect, 63, "working", model.AbilityRolePrimary),
			healthGridCell("solo", model.ChannelLaneDirect, 34, "dead-key", model.AbilityRolePrimary),
		},
		evidence: []model.ChannelHealthEvidenceRow{
			healthEvidence("gpt-4o-mini", model.ChannelLaneDirect, 34, model.ChannelHealthKindProbe, 21, 21),
			healthEvidence("gpt-4o-mini", model.ChannelLaneDirect, 63, model.ChannelHealthKindProbe, 3, 0),
			healthEvidence("solo", model.ChannelLaneDirect, 34, model.ChannelHealthKindProbe, 21, 21),
		},
		breaker: map[string]model.RouteHealthState{
			model.RouteHealthCacheKey("gpt-4o-mini", model.ChannelLaneDirect, 34): {
				State: model.RouteHealthStateRetreat, ConsecutiveFailures: 109, RetreatUntil: &lapsed,
			},
			model.RouteHealthCacheKey("solo", model.ChannelLaneDirect, 34): {
				State: model.RouteHealthStateRetreat, ConsecutiveFailures: 109, RetreatUntil: &lapsed,
			},
		},
	})
	if row := findLane(t, rows, "gpt-4o-mini", "stable"); row.Status != publicStatusDegraded {
		t.Errorf("mixed lane = %s, want degraded", row.Status)
	}
	if row := findLane(t, rows, "solo", "stable"); row.Status != publicStatusOutage {
		t.Errorf("solo lane = %s, want outage with its only route tripped", row.Status)
	}
}

// A standby carrying a lane whose primary the breaker pulled is the lane
// working, and the page says so. The fallback is an operator fact.
func TestFoldChannelHealthFallbackKeepsLaneOperational(t *testing.T) {
	now := time.Now().UTC()
	until := now.Add(time.Minute)
	rows := foldHealthForTest(channelHealthFoldInput{
		now: now,
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("claude-opus-5", model.ChannelLaneDirect, 39, "primary", model.AbilityRolePrimary),
			healthGridCell("claude-opus-5", model.ChannelLaneDirect, 48, "backup", model.AbilityRoleBackup),
		},
		evidence: []model.ChannelHealthEvidenceRow{
			healthEvidence("claude-opus-5", model.ChannelLaneDirect, 39, model.ChannelHealthKindProbe, 10, 5),
			healthEvidence("claude-opus-5", model.ChannelLaneDirect, 48, model.ChannelHealthKindTraffic, 30, 0),
		},
		breaker: map[string]model.RouteHealthState{
			model.RouteHealthCacheKey("claude-opus-5", model.ChannelLaneDirect, 39): {
				State: model.RouteHealthStateRetreat, ConsecutiveFailures: 5, RetreatUntil: &until,
			},
		},
	})
	row := findLane(t, rows, "claude-opus-5", "stable")
	if row.Status != publicStatusOperational || !row.FallbackActive {
		t.Fatalf("status = %s fallback = %t, want operational on the standby", row.Status, row.FallbackActive)
	}
}

// Rows are what the catalog sells today. A model that was retired keeps its
// abilities for a while and its traffic for the whole window; neither may
// bring it back onto the page.
func TestFoldChannelHealthRowsFollowTheCatalog(t *testing.T) {
	rows := foldHealthForTest(channelHealthFoldInput{
		catalog: map[string]bool{"kept": true},
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("kept", model.ChannelLaneDirect, 1, "a", model.AbilityRolePrimary),
			healthGridCell("retired", model.ChannelLaneDirect, 1, "a", model.AbilityRolePrimary),
		},
		evidence: []model.ChannelHealthEvidenceRow{
			healthEvidence("retired", model.ChannelLaneDirect, 1, model.ChannelHealthKindTraffic, 50, 0),
			// A lane no enabled route serves any more.
			healthEvidence("kept", model.ChannelLaneAgent, 37, model.ChannelHealthKindTraffic, 8, 8),
		},
	})
	if len(rows) != 1 || rows[0].ModelName != "kept" || rows[0].Lane != "stable" {
		t.Fatalf("rows = %+v, want only kept/stable", rows)
	}
}

// A channel that served a lane earlier in the window and has since been
// deleted is history: shown to the operator, counted nowhere.
func TestFoldChannelHealthIgnoresRemovedSourcesInMetrics(t *testing.T) {
	rows := foldHealthForTest(channelHealthFoldInput{
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("gpt-6-astra", model.ChannelLaneAgent, 83, "poolhost", model.AbilityRolePrimary),
		},
		evidence: []model.ChannelHealthEvidenceRow{
			healthEvidence("gpt-6-astra", model.ChannelLaneAgent, 83, model.ChannelHealthKindTraffic, 100, 0),
			healthEvidence("gpt-6-astra", model.ChannelLaneAgent, 37, model.ChannelHealthKindTraffic, 8, 8),
		},
		buckets: []model.ChannelHealthBucketRow{
			{ModelName: "gpt-6-astra", Lane: model.ChannelLaneAgent, ChannelId: 37, BucketIndex: 23,
				Kind: model.ChannelHealthKindTraffic, RequestCount: 8, ErrorCount: 8},
		},
	})
	row := findLane(t, rows, "gpt-6-astra", "eco")
	if row.SuccessRate == nil || *row.SuccessRate != 1 || row.RequestCount != 100 {
		t.Fatalf("lane = rate %v / %d requests, want 100%% over the live source only", row.SuccessRate, row.RequestCount)
	}
	if row.Status != publicStatusOperational {
		t.Fatalf("status = %s, want operational", row.Status)
	}
	if len(row.Sources) != 2 {
		t.Fatalf("sources = %+v, want the removed channel kept as history", row.Sources)
	}
	for _, source := range row.Sources {
		if source.ChannelID == 37 && !source.Disabled {
			t.Fatalf("removed source = %+v, want disabled", source)
		}
	}
	if row.Buckets[23].Tone != "none" {
		t.Fatalf("bucket = %+v, want the removed channel's failures off the wall", row.Buckets[23])
	}
}

// A route we cannot probe and nobody called is a blind spot in our own
// instrumentation. It is flagged for the operator and does not vote on status.
func TestFoldChannelHealthMarksUnprobeableRoutes(t *testing.T) {
	rows := foldHealthForTest(channelHealthFoldInput{
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("gpt-image-2", model.ChannelLaneDirect, 34, "reseller", model.AbilityRolePrimary),
		},
		blockedCells: map[string]model.ChannelHealthUnprobeableRow{
			model.ChannelHealthCellKey("gpt-image-2", model.ChannelLaneDirect, 34): {
				ModelName: "gpt-image-2", Lane: model.ChannelLaneDirect, ChannelId: 34,
				Reason: "model has no probeable endpoint",
			},
		},
	})
	row := findLane(t, rows, "gpt-image-2", "stable")
	if row.BlockedCount != 1 || row.Source != channelHealthSourceBlocked || row.Status != publicStatusUnknown {
		t.Fatalf("row = blocked %d source %s status %s", row.BlockedCount, row.Source, row.Status)
	}
}

// A slice that holds both a customer request and a probe counts both.
func TestFoldChannelHealthBucketsSumBothKinds(t *testing.T) {
	rows := foldHealthForTest(channelHealthFoldInput{
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("m", model.ChannelLaneDirect, 1, "a", model.AbilityRolePrimary),
		},
		buckets: []model.ChannelHealthBucketRow{
			{ModelName: "m", Lane: model.ChannelLaneDirect, ChannelId: 1, BucketIndex: 5,
				Kind: model.ChannelHealthKindTraffic, RequestCount: 1, ErrorCount: 0},
			{ModelName: "m", Lane: model.ChannelLaneDirect, ChannelId: 1, BucketIndex: 5,
				Kind: model.ChannelHealthKindProbe, RequestCount: 1, ErrorCount: 1},
			{ModelName: "m", Lane: model.ChannelLaneDirect, ChannelId: 1, BucketIndex: 9,
				Kind: model.ChannelHealthKindProbe, RequestCount: 1, ErrorCount: 0},
		},
	})
	row := findLane(t, rows, "m", "stable")
	mixed := row.Buckets[5]
	if mixed.Source != channelHealthSourceTraffic || mixed.RequestCount != 2 || mixed.ErrorCount != 1 {
		t.Fatalf("mixed bucket = %+v, want both samples", mixed)
	}
	if probeOnly := row.Buckets[9]; probeOnly.Source != channelHealthSourceProbe || probeOnly.Tone != "ok" {
		t.Fatalf("probe bucket = %+v, want an ok probe cell", probeOnly)
	}
	// A slice with no sample of its own says so; it does not borrow the
	// previous slice's verdict.
	if empty := row.Buckets[10]; empty.Source != channelHealthSourceNone || empty.Tone != "none" {
		t.Fatalf("next bucket = %+v, want no sample", empty)
	}
}

func TestFoldChannelHealthProvidersSumRoutesPerUpstream(t *testing.T) {
	now := time.Now().UTC()
	until := now.Add(time.Minute)
	grid := []model.ChannelHealthLaneCell{
		healthGridCell("a", model.ChannelLaneDirect, 1, "reseller", model.AbilityRolePrimary),
		healthGridCell("b", model.ChannelLaneDirect, 1, "reseller", model.AbilityRolePrimary),
		healthGridCell("a", model.ChannelLaneAgent, 2, "reverse", model.AbilityRolePrimary),
	}
	rows := foldHealthForTest(channelHealthFoldInput{
		now:  now,
		grid: grid,
		evidence: []model.ChannelHealthEvidenceRow{
			healthEvidence("a", model.ChannelLaneDirect, 1, model.ChannelHealthKindTraffic, 90, 0),
			healthEvidence("b", model.ChannelLaneDirect, 1, model.ChannelHealthKindProbe, 10, 0),
			healthEvidence("a", model.ChannelLaneAgent, 2, model.ChannelHealthKindProbe, 10, 10),
		},
		breaker: map[string]model.RouteHealthState{
			model.RouteHealthCacheKey("a", model.ChannelLaneAgent, 2): {
				State: model.RouteHealthStateRetreat, ConsecutiveFailures: 10, RetreatUntil: &until,
			},
		},
	})
	providers := foldChannelHealthProviders(
		rows, grid, nil, nil, nil, channelHealthCurrentCells(grid), now.Add(-24*time.Hour), 3600, channelHealthBucketCount,
	)
	if len(providers) != 2 {
		t.Fatalf("providers = %+v, want two upstreams", providers)
	}
	if providers[0].ChannelName != "reverse" || providers[0].Status != publicStatusOutage {
		t.Fatalf("first provider = %+v, want the pulled upstream first", providers[0])
	}
	reseller := providerByName(t, providers, "reseller")
	if reseller.RequestCount != 90 || reseller.ProbeCount != 10 || reseller.LaneCount != 2 ||
		reseller.SuccessRate == nil || *reseller.SuccessRate != 1 || reseller.Status != publicStatusOperational {
		t.Fatalf("reseller = %+v", reseller)
	}
}

func TestParseChannelHealthWindowOffersDayAndWeek(t *testing.T) {
	for raw, want := range map[string]string{"": "24h", "24h": "24h", "7d": "7d", "1h": "24h", "30d": "24h"} {
		if got, _ := parseChannelHealthWindow(raw); got != want {
			t.Errorf("window %q = %q, want %q", raw, got, want)
		}
	}
}

func TestPrepareProbeRequestStreamsAFixedCappedPrompt(t *testing.T) {
	chat := &dto.GeneralOpenAIRequest{Model: "gpt-5.5", MaxTokens: 10,
		Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	prepareProbeRequest(chat)
	if !chat.Stream || chat.StreamOptions == nil || !chat.StreamOptions.IncludeUsage {
		t.Fatalf("chat probe = %+v, want a streamed request that reports usage", chat)
	}
	if chat.MaxTokens != ChannelProbeMaxOutputTokens || chat.MaxCompletionTokens != 0 {
		t.Fatalf("chat caps = %d/%d, want only max_tokens at the probe cap", chat.MaxTokens, chat.MaxCompletionTokens)
	}
	if len(chat.Messages) != 1 || chat.Messages[0].Content != probePrompt {
		t.Fatalf("chat messages = %+v, want the fixed probe prompt", chat.Messages)
	}

	// A reasoning model that cannot stop thinking gets room to reach a visible
	// token, at the lowest effort its family accepts.
	reasoning := &dto.GeneralOpenAIRequest{Model: "o4", MaxCompletionTokens: 10}
	prepareProbeRequest(reasoning)
	if reasoning.MaxCompletionTokens != probeReasoningCap || reasoning.MaxTokens != 0 ||
		reasoning.ReasoningEffort != "low" {
		t.Fatalf("reasoning probe = %d/%d effort %q, want only max_completion_tokens at the reasoning cap, low effort",
			reasoning.MaxTokens, reasoning.MaxCompletionTokens, reasoning.ReasoningEffort)
	}
	gpt5 := &dto.GeneralOpenAIRequest{Model: "gpt-5-mini", MaxCompletionTokens: 10}
	prepareProbeRequest(gpt5)
	if gpt5.ReasoningEffort != "minimal" {
		t.Fatalf("gpt-5 effort = %q, want minimal", gpt5.ReasoningEffort)
	}
	// Later gpt-5.x releases reason little by default and reject "minimal".
	if effort := probeReasoningEffort("gpt-5.5"); effort != "" {
		t.Fatalf("gpt-5.5 effort = %q, want none sent", effort)
	}
	// Other adaptors translate the parameter into a thinking budget, which
	// would switch thinking on for a probe.
	if effort := probeReasoningEffort("claude-opus-5"); effort != "" {
		t.Fatalf("claude effort = %q, want none sent", effort)
	}

	image := &dto.ImageRequest{Model: "gpt-image-2", Prompt: "a cute cat", Size: "1536x1024"}
	prepareProbeRequest(image)
	if image.N != 1 || image.Size != "1024x1024" || image.Quality != "low" || image.Prompt != probeImagePrompt {
		t.Fatalf("image probe = %+v, want one low-quality 1024 square of the fixed prompt", image)
	}

	responses := &dto.OpenAIResponsesRequest{Model: "gpt-6-astra", Input: json.RawMessage(`"hi"`)}
	prepareProbeRequest(responses)
	var input string
	if err := json.Unmarshal(responses.Input, &input); err != nil || input != probePrompt {
		t.Fatalf("responses input = %s, want the fixed prompt", responses.Input)
	}
	if !responses.Stream || responses.MaxOutputTokens != ChannelProbeMaxOutputTokens {
		t.Fatalf("responses probe = %+v, want streamed and capped", responses)
	}
}

func TestFinalizeChannelProbeResultKeepsStreamTimings(t *testing.T) {
	outcome := finalizeChannelProbeResult(context.Background(), model.ChannelProbeResult{LatencyMs: 2500}, testResult{
		providerAttempted: true, durationMs: 1400, streamed: true,
		ttftMs: 600, maxStallMs: 180, outputTokens: 64,
	})
	if !outcome.Success || outcome.LatencyMs != 1400 || outcome.TTFTMs != 600 ||
		outcome.MaxStallMs != 180 || outcome.OutputTokens != 64 {
		t.Fatalf("outcome = %+v, want the upstream exchange's own timings", outcome)
	}
}

// Every text route shares one cadence, whatever its price — a two-hour tier
// for expensive models left every other cell of their wall without a sample.
// Image routes wait hours, because one probe image costs cents.
func TestSelectChannelProbeCandidatesUsesOneCadencePerKind(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	channels := []model.Channel{
		{Id: 1, Name: "cheap", Key: "k", Status: common.ChannelStatusEnabled, Group: model.ChannelLaneDirect},
		{Id: 2, Name: "opus", Key: "k", Status: common.ChannelStatusEnabled, Group: model.ChannelLaneDirect},
		{Id: 3, Name: "images", Key: "k", Status: common.ChannelStatusEnabled, Group: model.ChannelLaneDirect},
	}
	if err := db.Create(&channels).Error; err != nil {
		t.Fatalf("seed channels: %v", err)
	}
	abilities := []model.Ability{
		{Group: model.ChannelLaneDirect, Model: "cheap-model", ChannelId: 1, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "opus-model", ChannelId: 2, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "gpt-image-2", ChannelId: 3, Enabled: true},
	}
	if err := db.Create(&abilities).Error; err != nil {
		t.Fatalf("seed abilities: %v", err)
	}
	cheapOut, opusOut := 2.0, 75.0
	prices := []model.ProviderCostPrice{
		{ModelName: "cheap-model", Provider: "p", ChannelID: 1, OutputPerMtok: &cheapOut, PricingVersion: 1},
		{ModelName: "opus-model", Provider: "p", ChannelID: 2, OutputPerMtok: &opusOut, PricingVersion: 1},
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatalf("seed prices: %v", err)
	}
	now := time.Now().UTC()
	for _, probe := range []model.ChannelProbeResult{
		{Lane: model.ChannelLaneDirect, ModelName: "cheap-model", ChannelId: 1, ProbedAt: now.Add(-55 * time.Minute), Success: true},
		{Lane: model.ChannelLaneDirect, ModelName: "opus-model", ChannelId: 2, ProbedAt: now.Add(-55 * time.Minute), Success: true},
		{Lane: model.ChannelLaneDirect, ModelName: "gpt-image-2", ChannelId: 3, ProbedAt: now.Add(-2 * time.Hour), Success: true},
	} {
		if err := db.Create(&probe).Error; err != nil {
			t.Fatalf("seed probe: %v", err)
		}
	}

	candidates, err := SelectChannelProbeCandidates(now)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	due := map[string]string{}
	for _, candidate := range candidates {
		due[candidate.ModelName] = candidate.Kind
	}
	if len(due) != 2 || due["cheap-model"] != channelHealthKindLLM || due["opus-model"] != channelHealthKindLLM {
		t.Fatalf("candidates = %+v, want both text routes due after 55 minutes and the image route not yet", candidates)
	}

	candidates, err = SelectChannelProbeCandidates(now.Add(4 * time.Hour))
	if err != nil {
		t.Fatalf("select later: %v", err)
	}
	for _, candidate := range candidates {
		if candidate.ModelName == "gpt-image-2" {
			if candidate.Kind != channelHealthKindImage {
				t.Fatalf("image candidate kind = %q", candidate.Kind)
			}
			return
		}
	}
	t.Fatalf("candidates = %+v, want the image route due after six hours", candidates)
}

// A customer never receives a lane with no samples in the window. Every lane
// we can reach is probed hourly, so what remains is one we cannot check, and a
// row of blanks would read as a model that is quietly broken.
func TestChannelHealthMembersOnlyReceiveLanesWithSamples(t *testing.T) {
	for source, want := range map[string]bool{
		channelHealthSourceTraffic: true,
		channelHealthSourceProbe:   true,
		channelHealthSourceBlocked: false,
		channelHealthSourceNone:    false,
	} {
		if got := channelHealthRowHasSamples(channelHealthLaneView{Source: source}); got != want {
			t.Errorf("source %s visible = %t, want %t", source, got, want)
		}
	}
}

// An aliased variant is shown under the catalog's name, so the plaza and a
// member's accessible-model list can find it, while each route keeps the name
// its ability is stored under for admin writes.
func TestFoldChannelHealthKeysRowsByCatalogName(t *testing.T) {
	rows := foldHealthForTest(channelHealthFoldInput{
		catalog: map[string]bool{"gpt-4o-gizmo-*": true},
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("gpt-4o-gizmo-abc", model.ChannelLaneDirect, 1, "a", model.AbilityRolePrimary),
			healthGridCell("gpt-4o-gizmo-xyz", model.ChannelLaneDirect, 2, "b", model.AbilityRolePrimary),
		},
		evidence: []model.ChannelHealthEvidenceRow{
			healthEvidence("gpt-4o-gizmo-abc", model.ChannelLaneDirect, 1, model.ChannelHealthKindTraffic, 10, 0),
			healthEvidence("gpt-4o-gizmo-xyz", model.ChannelLaneDirect, 2, model.ChannelHealthKindProbe, 5, 0),
		},
		buckets: []model.ChannelHealthBucketRow{
			{ModelName: "gpt-4o-gizmo-xyz", Lane: model.ChannelLaneDirect, ChannelId: 2, BucketIndex: 3,
				Kind: model.ChannelHealthKindProbe, RequestCount: 1},
		},
	})
	if len(rows) != 1 || rows[0].ModelName != "gpt-4o-gizmo-*" {
		t.Fatalf("rows = %+v, want one row under the catalog name", rows)
	}
	row := rows[0]
	if row.RequestCount != 10 || row.ProbeCount != 5 || row.ChannelCount != 2 {
		t.Fatalf("row = %+v, want both variants folded in", row)
	}
	if row.Buckets[3].Source != channelHealthSourceProbe {
		t.Fatalf("bucket = %+v, want the variant's probe on the wall", row.Buckets[3])
	}
	for _, source := range row.Sources {
		if source.AbilityModel == "" {
			t.Fatalf("source %+v lost its routed name", source)
		}
	}
}

// A served snapshot takes the breaker's current verdict, however old the
// snapshot is, and leaves the cached copy untouched.
func TestOverlayChannelHealthBreakerUpdatesStatusOnly(t *testing.T) {
	now := time.Now().UTC()
	rate := 1.0
	snapshot := channelHealthSnapshot{
		rows: []channelHealthLaneView{{
			ModelName: "claude-opus-5", Lane: "stable", LaneSlug: model.ChannelLaneDirect,
			Status: publicStatusOperational, SuccessRate: &rate, ProbeCount: 12,
			Sources: []channelHealthSourceView{{
				ChannelID: 39, ChannelName: "reseller", Status: publicStatusOperational,
				Source: channelHealthSourceProbe, ProbeCount: 12, Role: model.AbilityRolePrimary,
			}},
		}},
		providers: []channelHealthProviderView{
			{ChannelID: 7, ChannelName: "other", Status: publicStatusDegraded},
			{ChannelID: 39, ChannelName: "reseller", Status: publicStatusOperational,
				Lanes: []channelHealthProviderLaneView{{ModelName: "claude-opus-5", Lane: "stable", Status: publicStatusOperational}}},
		},
	}
	until := now.Add(4 * time.Minute)
	overlaid := overlayChannelHealthBreaker(snapshot, []model.RouteHealthState{{
		ModelName: "claude-opus-5", Lane: model.ChannelLaneDirect, ChannelId: 39,
		State: model.RouteHealthStateRetreat, ConsecutiveFailures: 3, RetreatUntil: &until,
	}}, now)

	row := overlaid.rows[0]
	if row.Status != publicStatusOutage || !row.Sources[0].Retreating || row.Sources[0].Status != publicStatusOutage {
		t.Fatalf("row = %+v, want the pulled route to read down", row)
	}
	if row.SuccessRate == nil || *row.SuccessRate != 1 || row.ProbeCount != 12 {
		t.Fatalf("figures changed: %+v", row)
	}
	if overlaid.providers[0].ChannelName != "reseller" || overlaid.providers[0].Status != publicStatusOutage ||
		overlaid.providers[0].Lanes[0].Status != publicStatusOutage {
		t.Fatalf("providers = %+v, want reseller down and first", overlaid.providers)
	}
	if snapshot.rows[0].Status != publicStatusOperational || snapshot.rows[0].Sources[0].Retreating ||
		snapshot.providers[1].Lanes[0].Status != publicStatusOperational {
		t.Fatal("overlay wrote through to the cached snapshot")
	}

	recovered := overlayChannelHealthBreaker(overlaid, nil, now)
	if recovered.rows[0].Status != publicStatusOperational {
		t.Fatalf("recovered status = %s, want operational once the breaker cleared", recovered.rows[0].Status)
	}
}

// A failed hourly probe against parked capacity must not lower the figures of
// a lane whose primary served every customer. A standby counts once it is
// carrying traffic.
func TestFoldChannelHealthLeavesParkedRoutesOutOfLaneFigures(t *testing.T) {
	rows := foldHealthForTest(channelHealthFoldInput{
		grid: []model.ChannelHealthLaneCell{
			healthGridCell("m", model.ChannelLaneDirect, 1, "primary", model.AbilityRolePrimary),
			healthGridCell("m", model.ChannelLaneDirect, 2, "idle-backup", model.AbilityRoleBackup),
			healthGridCell("m", model.ChannelLaneDirect, 3, "inventory", model.AbilityRoleInventory),
			healthGridCell("m", model.ChannelLaneDirect, 4, "used-backup", model.AbilityRoleBackup),
		},
		evidence: []model.ChannelHealthEvidenceRow{
			healthEvidence("m", model.ChannelLaneDirect, 1, model.ChannelHealthKindTraffic, 100, 0),
			healthEvidence("m", model.ChannelLaneDirect, 2, model.ChannelHealthKindProbe, 24, 24),
			healthEvidence("m", model.ChannelLaneDirect, 3, model.ChannelHealthKindProbe, 24, 24),
			healthEvidence("m", model.ChannelLaneDirect, 4, model.ChannelHealthKindTraffic, 10, 1),
		},
		buckets: []model.ChannelHealthBucketRow{
			{ModelName: "m", Lane: model.ChannelLaneDirect, ChannelId: 3, BucketIndex: 20,
				Kind: model.ChannelHealthKindProbe, RequestCount: 1, ErrorCount: 1},
		},
	})
	row := findLane(t, rows, "m", "stable")
	if row.RequestCount != 110 || row.ProbeCount != 0 {
		t.Fatalf("counts = %d/%d, want the primary and the used standby only", row.RequestCount, row.ProbeCount)
	}
	if row.SuccessRate == nil || *row.SuccessRate < 0.99 {
		t.Fatalf("success rate = %v, want parked failures left out", row.SuccessRate)
	}
	if row.Buckets[20].Tone != "none" {
		t.Fatalf("bucket = %+v, want inventory probes off the wall", row.Buckets[20])
	}
	if len(row.Sources) != 4 {
		t.Fatalf("sources = %d, want every route still listed for the operator", len(row.Sources))
	}
}

// A stream that answered 200 and delivered nothing is a failed sample, as it
// is for customer traffic. One whose hidden reasoning used the cap is not.
func TestFinalizeChannelProbeResultFailsAnEmptyStream(t *testing.T) {
	empty := finalizeChannelProbeResult(context.Background(), model.ChannelProbeResult{}, testResult{
		providerAttempted: true, durationMs: 900, streamed: true,
	})
	if empty.Success || empty.Skipped || empty.ErrorMessage == "" {
		t.Fatalf("empty stream = %+v, want a recorded failure", empty)
	}
	reasoning := finalizeChannelProbeResult(context.Background(), model.ChannelProbeResult{}, testResult{
		providerAttempted: true, durationMs: 3000, streamed: true, outputTokens: 64,
	})
	if !reasoning.Success || reasoning.TTFTMs != 0 {
		t.Fatalf("reasoning-only stream = %+v, want success with no first-token sample", reasoning)
	}
}

// A member's row says what was measured and how fresh it is, never whose
// request measured it or how many there were.
func TestNeutralizeChannelHealthProvenanceForMembers(t *testing.T) {
	now := time.Now().UTC()
	older := now.Add(-3 * time.Hour)
	rate := 0.98
	row := neutralizeChannelHealthProvenance(channelHealthLaneView{
		Source: channelHealthSourceProbe, SuccessRate: &rate,
		RequestCount: 3, ErrorCount: 1, ProbeCount: 24, ProbeErrorCount: 1,
		LastOutcomeAt: &older, LastProbedAt: &now,
		Buckets: []channelHealthBucketView{
			{Source: channelHealthSourceProbe, RequestCount: 1, Tone: "ok"},
			{Source: channelHealthSourceNone, Tone: "none"},
		},
	})
	if row.Source != channelHealthSourceSample || row.RequestCount != 0 || row.ProbeCount != 0 ||
		row.ErrorCount != 0 || row.ProbeErrorCount != 0 {
		t.Fatalf("row = %+v, want neutral source and no counts", row)
	}
	if row.LastProbedAt != nil || row.LastOutcomeAt == nil || !row.LastOutcomeAt.Equal(now) {
		t.Fatalf("freshness = %v / %v, want only the newest sample time", row.LastOutcomeAt, row.LastProbedAt)
	}
	if row.SuccessRate == nil || *row.SuccessRate != rate {
		t.Fatalf("rate changed: %v", row.SuccessRate)
	}
	if row.Buckets[0].Source != channelHealthSourceSample || row.Buckets[0].RequestCount != 0 ||
		row.Buckets[1].Source != channelHealthSourceNone {
		t.Fatalf("buckets = %+v", row.Buckets)
	}
}

// Parking a source parks every aliased variant the page folded into it.
func TestUpdateChannelHealthRouteRoleCoversFoldedAliases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupModelOpsControllerTestDB(t)
	if err := db.AutoMigrate(&model.AdminAuditLog{}); err != nil {
		t.Fatalf("migrate audit log: %v", err)
	}
	if err := db.Create(&model.Channel{
		Id: 5, PublicID: "ch-alias", Key: "k", Name: "gizmo", Status: common.ChannelStatusEnabled,
		Group: model.ChannelLaneDirect,
	}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	for _, name := range []string{"gpt-4o-gizmo-abc", "gpt-4o-gizmo-xyz", "gpt-4o"} {
		if err := db.Create(&model.Ability{
			Group: model.ChannelLaneDirect, Model: name, ChannelId: 5, Enabled: true,
			Role: model.AbilityRolePrimary,
		}).Error; err != nil {
			t.Fatalf("seed ability: %v", err)
		}
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/channel_health/route_role", strings.NewReader(
		`{"channel_id":"ch-alias","lane":"direct","model":"gpt-4o-gizmo-abc","role":"inventory"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	UpdateChannelHealthRouteRole(c)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"success":true`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}

	var abilities []model.Ability
	if err := db.Where("channel_id = ?", 5).Find(&abilities).Error; err != nil {
		t.Fatalf("load abilities: %v", err)
	}
	for _, ability := range abilities {
		want := model.AbilityRoleInventory
		if ability.Model == "gpt-4o" {
			want = model.AbilityRolePrimary
		}
		if ability.Role != want {
			t.Errorf("%s role = %q, want %q", ability.Model, ability.Role, want)
		}
	}
}

// The plaza takes the breaker's current verdict over a snapshot up to ten
// minutes old, in both directions.
func TestOverlayCatalogHealthBreakerFollowsTheRouter(t *testing.T) {
	now := time.Now().UTC()
	until := now.Add(3 * time.Minute)
	health := map[string]map[string]dto.ModelCatalogHealth{
		"claude-opus-5": {"stable": {Status: "available"}},
		"gpt-5.5":       {"stable": {Status: "unavailable"}},
	}
	grid := []model.ChannelHealthLaneCell{
		healthGridCell("claude-opus-5", model.ChannelLaneDirect, 39, "reseller", model.AbilityRolePrimary),
		healthGridCell("gpt-5.5", model.ChannelLaneDirect, 34, "reseller", model.AbilityRolePrimary),
	}
	states := []model.RouteHealthState{{
		ModelName: "claude-opus-5", Lane: model.ChannelLaneDirect, ChannelId: 39,
		State: model.RouteHealthStateRetreat, ConsecutiveFailures: 3, RetreatUntil: &until,
	}}
	got := overlayCatalogHealthBreaker(health, grid, states, nil, now)
	if status := got["claude-opus-5"]["stable"].Status; status != "unavailable" {
		t.Errorf("pulled lane = %q, want unavailable", status)
	}
	if status := got["gpt-5.5"]["stable"].Status; status != "available" {
		t.Errorf("recovered lane = %q, want available", status)
	}
}

// The routes a lane leaves out are named by the routed name their samples are
// stored under, so the percentile query can drop them.
func TestChannelHealthUncountedRoutesNamesIdleStandbys(t *testing.T) {
	grid := []model.ChannelHealthLaneCell{
		healthGridCell("gpt-4o-gizmo-abc", model.ChannelLaneDirect, 1, "primary", model.AbilityRolePrimary),
		healthGridCell("gpt-4o-gizmo-abc", model.ChannelLaneDirect, 2, "standby", model.AbilityRoleBackup),
	}
	catalog := map[string]bool{"gpt-4o-gizmo-*": true}
	rows := foldHealthForTest(channelHealthFoldInput{
		grid: grid, catalog: catalog,
		evidence: []model.ChannelHealthEvidenceRow{
			healthEvidence("gpt-4o-gizmo-abc", model.ChannelLaneDirect, 1, model.ChannelHealthKindTraffic, 10, 0),
			healthEvidence("gpt-4o-gizmo-abc", model.ChannelLaneDirect, 2, model.ChannelHealthKindProbe, 24, 0),
		},
	})
	routes := channelHealthUncountedRoutes(rows, grid, catalog)
	if len(routes) != 1 || routes[0].ChannelId != 2 || routes[0].ModelName != "gpt-4o-gizmo-abc" {
		t.Fatalf("routes = %+v, want the idle standby under its routed name", routes)
	}
}

// The health page asks to test a route by its catalog name; an aliased route
// must still resolve to the ability it is stored under.
func TestFindChannelProbeCellResolvesAliases(t *testing.T) {
	grid := []model.ChannelHealthLaneCell{
		healthGridCell("gpt-4o-gizmo-abc", model.ChannelLaneDirect, 5, "gizmo", model.AbilityRolePrimary),
		healthGridCell("gpt-4o", model.ChannelLaneDirect, 5, "gizmo", model.AbilityRolePrimary),
	}
	cell, ok := findChannelProbeCell(grid, 5, "gpt-4o-gizmo-*", model.ChannelLaneDirect)
	if !ok || cell.ModelName != "gpt-4o-gizmo-abc" {
		t.Fatalf("cell = %+v ok=%t, want the routed variant", cell, ok)
	}
	if cell, ok := findChannelProbeCell(grid, 5, "gpt-4o", model.ChannelLaneDirect); !ok || cell.ModelName != "gpt-4o" {
		t.Fatalf("exact = %+v ok=%t", cell, ok)
	}
	if _, ok := findChannelProbeCell(grid, 5, "gpt-4o", model.ChannelLaneAgent); ok {
		t.Fatal("matched a lane the route does not serve")
	}
}

func TestSeedreamManualTestsAndHealthProbesUseValidSizes(t *testing.T) {
	for _, name := range []string{"seedream-5.0-lite", "seedream-4.5", "seedream-5.0-pro", "seedream-5.0-flash"} {
		request := buildTestRequest(name, string(constant.EndpointTypeImageGeneration)).(*dto.ImageRequest)
		if err := request.PrepareSeedream(); err != nil {
			t.Fatalf("%s manual test: %v", name, err)
		}
		prepareProbeRequest(request)
		if err := request.PrepareSeedream(); err != nil {
			t.Fatalf("%s health probe: %v", name, err)
		}
		if request.N != 1 {
			t.Fatal("image health probes must stay bounded")
		}
	}
}
