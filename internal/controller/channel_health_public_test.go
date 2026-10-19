package controller

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
)

func TestChannelHealthPublicPercentilesSurviveSnapshotAndMemberProjection(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	if err := db.AutoMigrate(&model.Model{}, &model.ModelMetadata{}, &model.ModelSellPrice{}, &model.Vendor{},
		&model.RouteHealthState{}, &model.RoutingDecision{}, &model.RoutingOutcome{}, &model.RoutingAttemptOutcome{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	names := []string{"gpt-pooled", "text-embedding-pooled"}
	for i, lane := range []string{model.ChannelLaneDirect, "turbo"} {
		channel := model.Channel{Id: i + 1, Type: constant.ChannelTypeOpenAI, Key: "test", Status: common.ChannelStatusEnabled, Group: lane, Models: strings.Join(names, ",")}
		if err := db.Create(&channel).Error; err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if err := db.Create(&model.Ability{Group: lane, Model: name, ChannelId: channel.Id, Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
			count, ttft := 9, 100
			if lane == "turbo" {
				count, ttft = 1, 1000
			}
			for j := 0; j < count; j++ {
				if err := db.Create(&model.ChannelProbeResult{
					ModelName: name, Lane: lane, ChannelId: channel.Id, ProbedAt: now.Add(-time.Minute),
					Success: true, TTFTMs: ttft, LatencyMs: ttft * 10, OutputTokens: 64,
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
		// Each route has too little real traffic on its own (3), but the
		// public lane has 6 measured durations and must select traffic latency.
		for j := 0; j < 3; j++ {
			id := int64(i*3 + j + 1)
			request := fmt.Sprintf("pooled-%d", id)
			decision := model.RoutingDecision{ID: id, RequestID: request, DecisionAt: now.Add(-time.Minute), RequestedModel: names[1], ResolvedModel: names[1], StrategyName: "test", IsPrimary: true}
			if err := db.Create(&decision).Error; err != nil {
				t.Fatal(err)
			}
			status, duration := 200, (int(id)+1)*1000
			outcome := model.RoutingOutcome{RequestID: request, DecisionID: id, ChannelID: &channel.Id, FinalGroup: lane,
				OutcomeAt: now.Add(-time.Minute), StatusCode: &status, TotalDurationMs: &duration}
			if err := db.Create(&outcome).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, name := range names {
		if err := db.Create(&model.Model{ModelName: name, Status: 1, NameRule: model.NameRuleExact}).Error; err != nil {
			t.Fatal(err)
		}
	}
	model.RefreshPricing()
	snapshot, err := buildChannelHealthSnapshot(7 * 24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.rows) != 4 {
		t.Fatalf("want four real routes: %+v", snapshot.rows)
	}
	for _, row := range snapshot.rows {
		if row.AggregateTotals == nil || row.AggregateTotals.PublicPercentiles == nil {
			t.Fatalf("missing pooled snapshot figures: %+v", row)
		}
	}
	// The view survives the JSON round trip a cached copy would take.
	encoded, err := json.Marshal(snapshot.rows)
	if err != nil {
		t.Fatal(err)
	}
	stored := channelHealthSnapshot{}
	if err := json.Unmarshal(encoded, &stored.rows); err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	members, err := projectChannelHealthRows(c, stored.rows, false)
	if err != nil || len(members) != 2 {
		t.Fatalf("public rows = %+v / %v", members, err)
	}
	llm := findLane(t, members, names[0], "stable")
	if llm.TTFTP50Ms == nil || !channelHealthFigureNear(*llm.TTFTP50Ms, 100) || llm.TTFTP95Ms == nil || !channelHealthFigureNear(*llm.TTFTP95Ms, 1000) {
		t.Fatalf("pooled first token lost after member projection: %+v", llm)
	}
	embedding := findLane(t, members, names[1], "stable")
	if embedding.LatencyP50Ms == nil || !channelHealthFigureNear(*embedding.LatencyP50Ms, 4000) || embedding.LatencyP95Ms == nil || !channelHealthFigureNear(*embedding.LatencyP95Ms, 7000) {
		t.Fatalf("pooled traffic latency lost: %+v", embedding)
	}
	for i := range members {
		members[i] = neutralizeChannelHealthProvenance(members[i])
	}
	wire, _ := json.Marshal(members)
	if strings.Contains(string(wire), "public_percentiles") || strings.Contains(string(wire), "aggregate_totals") {
		t.Fatalf("internal statistics leaked: %s", wire)
	}
	admin, err := projectChannelHealthRows(c, stored.rows, true)
	if err != nil || len(admin) != 4 {
		t.Fatalf("admin lost real routes: %+v / %v", admin, err)
	}
	for _, row := range admin {
		if row.ModelName == names[0] && row.LaneSlug == "turbo" && (row.TTFTP50Ms == nil || !channelHealthFigureNear(*row.TTFTP50Ms, 1000)) {
			t.Fatalf("pooled statistics overwrote real-route statistics: %+v", row)
		}
	}
}

func TestMergePublicChannelHealthRowsUsesTotalsAndWorstSlice(t *testing.T) {
	start := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	rows := []channelHealthLaneView{
		{
			ModelName: "gpt-test", Kind: "llm", Lane: "stable", LaneSlug: "direct",
			Status: publicStatusOperational, Source: channelHealthSourceTraffic,
			RequestCount: 100, ErrorCount: 1, OutputTokens: 100, DecodeMs: 2000,
			TTFTP50Ms: positiveChannelHealthFigure(100), TTFTP95Ms: positiveChannelHealthFigure(200),
			InputPerMtok:    positiveChannelHealthFigure(3),
			AggregateTotals: &channelHealthAggregateTotals{DurationSumMs: 50000, DurationCount: 50, Interrupted: 2, Streamed: 80},
			Buckets: []channelHealthBucketView{
				{Index: 0, StartAt: start, Source: channelHealthSourceTraffic, Tone: "ok", RequestCount: 100, ErrorCount: 1, SuccessRate: successRate(100, 1)},
				{Index: 1, StartAt: start.Add(2 * time.Hour), Source: channelHealthSourceNone, Tone: "none"},
			},
		},
		{
			ModelName: "gpt-test", Kind: "llm", Lane: "stable", LaneSlug: "turbo",
			Status: publicStatusOutage, Source: channelHealthSourceProbe,
			ProbeCount: 9, ProbeErrorCount: 9, OutputTokens: 300, DecodeMs: 1000,
			TTFTP50Ms: positiveChannelHealthFigure(900), TTFTP95Ms: positiveChannelHealthFigure(1000),
			AggregateTotals: &channelHealthAggregateTotals{DurationSumMs: 16000, DurationCount: 4, Interrupted: 8, Streamed: 10},
			Buckets: []channelHealthBucketView{
				{Index: 0, StartAt: start, Source: channelHealthSourceProbe, Tone: "bad", RequestCount: 9, ErrorCount: 9, SuccessRate: successRate(9, 9)},
				{Index: 1, StartAt: start.Add(2 * time.Hour), Source: channelHealthSourceBlocked, Tone: "blocked"},
			},
		},
	}
	// Exercise the materialized representation, not just fresh in-memory data.
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var stored []channelHealthLaneView
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	got := mergePublicChannelHealthRows(stored)
	if len(got) != 1 {
		t.Fatalf("got %d public rows, want 1", len(got))
	}
	row := got[0]
	if row.Status != publicStatusDegraded {
		t.Fatalf("mixed current status = %s", row.Status)
	}
	assertFigure := func(name string, got *float64, want float64) {
		t.Helper()
		if got == nil || math.Abs(*got-want) > 1e-9 {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
	assertFigure("availability", row.SuccessRate, 99.0/109)
	assertFigure("throughput", row.OutputTPS, 400000.0/3000)
	assertFigure("mean", row.AvgLatencyMs, 66000.0/54)
	assertFigure("interruption", row.InterruptionRate, 10.0/90)
	if row.TTFTP50Ms != nil || row.TTFTP95Ms != nil || row.InputPerMtok != nil {
		t.Fatal("pooled lane must not inherit a route's percentile or price")
	}
	if len(row.Buckets) != 2 || row.Buckets[0].Tone != "bad" || row.Buckets[0].Source != channelHealthSourceProbe {
		t.Fatalf("history lost the worse observed route: %+v", row.Buckets)
	}
	if summary := summarizeChannelHealth(got); summary.LaneCount != 1 || summary.DegradedCount != 1 {
		t.Fatalf("summary exposes hidden routes: %+v", summary)
	}
	// Neither route order nor the read may change the answer or cached input.
	stored[0], stored[1] = stored[1], stored[0]
	if !reflect.DeepEqual(got, mergePublicChannelHealthRows(stored)) {
		t.Fatal("public aggregation depends on route order")
	}
	if rows[0].Buckets[0].Tone != "ok" {
		t.Fatal("aggregation mutated input buckets")
	}
}

func TestMergePublicChannelHealthRowsHandlesUnknownAndLegacyMetrics(t *testing.T) {
	for _, tc := range []struct{ left, right, want string }{
		{"operational", "unknown", "operational"},
		{"outage", "unknown", "outage"},
		{"outage", "outage", "outage"},
		{"degraded", "operational", "degraded"},
		{"unknown", "unknown", "unknown"},
	} {
		t.Run(tc.left+"/"+tc.right, func(t *testing.T) {
			row := mergePublicChannelHealthLane([]channelHealthLaneView{
				{Status: tc.left, Source: channelHealthSourceNone},
				{Status: tc.right, Source: channelHealthSourceNone},
			})
			if row.Status != tc.want {
				t.Fatalf("status = %s, want %s", row.Status, tc.want)
			}
		})
	}
	p50, interruption := 123.0, 0.03
	measured := channelHealthLaneView{Source: channelHealthSourceTraffic, RequestCount: 10, TTFTP50Ms: &p50, InterruptionRate: &interruption}
	idle := channelHealthLaneView{Source: channelHealthSourceNone}
	row := mergePublicChannelHealthLane([]channelHealthLaneView{idle, measured})
	if row.TTFTP50Ms == nil || *row.TTFTP50Ms != p50 || row.InterruptionRate == nil || *row.InterruptionRate != interruption {
		t.Fatal("single measured population should keep its metrics")
	}
	row = mergePublicChannelHealthLane([]channelHealthLaneView{measured, measured})
	if row.TTFTP50Ms != nil || row.InterruptionRate != nil || row.AvgLatencyMs != nil {
		t.Fatal("old snapshots cannot supply missing pooled distributions or denominators")
	}
}

func TestProjectChannelHealthMergesMemberLanesWithoutMutatingAdminSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()
	if err := model.CoreDB().Create(&model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Key: "test", Status: common.ChannelStatusEnabled, Models: "gpt-member", Group: model.ChannelLaneDirect}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Ability{Group: model.ChannelLaneDirect, Model: "gpt-member", ChannelId: 1, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Model{ModelName: "gpt-member", Status: 1, NameRule: model.NameRuleExact}).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	rows := []channelHealthLaneView{
		{ModelName: "gpt-member", Kind: "llm", Lane: "stable", LaneSlug: "direct", Status: "operational", Source: "traffic", RequestCount: 100,
			Buckets:         []channelHealthBucketView{{Index: 0, Source: "blocked", Tone: "blocked"}},
			AggregateTotals: &channelHealthAggregateTotals{Streamed: 80, Interrupted: 2}},
		{ModelName: "gpt-member", Kind: "llm", Lane: "stable", LaneSlug: "turbo", Status: "outage", Source: "probe", ProbeCount: 5, ProbeErrorCount: 5},
		{ModelName: "gpt-member", Kind: "llm", Lane: "eco", LaneSlug: "cheap", Status: "operational", Source: "probe", ProbeCount: 2,
			Buckets: []channelHealthBucketView{{Index: 0, Source: "blocked", Tone: "blocked"}}},
		{ModelName: "not-accessible", Kind: "llm", Lane: "stable", Source: "traffic", RequestCount: 100},
	}
	before, _ := json.Marshal(rows)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	for attempt := 0; attempt < 2; attempt++ {
		member, err := projectChannelHealthRows(c, rows, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(member) != 2 || member[0].Lane != "stable" || member[1].Lane != "eco" {
			t.Fatalf("member rows = %+v", member)
		}
		if member[0].Status != "degraded" || len(member[0].Buckets) != 1 || member[0].Buckets[0].Tone != "none" {
			t.Fatalf("public status/history = %+v", member[0])
		}
		for i := range member {
			member[i] = neutralizeChannelHealthProvenance(member[i])
		}
		wire, _ := json.Marshal(member)
		for _, hidden := range []string{"lane_slug", "turbo", "aggregate_totals", "sources", "channel_count", "probe_count"} {
			if strings.Contains(string(wire), hidden) {
				t.Fatalf("member response leaks %s: %s", hidden, wire)
			}
		}
	}
	after, _ := json.Marshal(rows)
	if string(before) != string(after) {
		t.Fatal("member read mutated cached admin data")
	}
	admin, err := projectChannelHealthRows(c, rows, true)
	if err != nil || len(admin) != 4 || admin[1].LaneSlug != "turbo" || admin[0].Buckets[0].Tone != "blocked" {
		t.Fatalf("admin lost real routes or instrumentation: %+v / %v", admin, err)
	}
}

// channelHealthFigureNear accepts a percentile read back from the hourly
// histograms: within 10% of the exact value.
func channelHealthFigureNear(got, want float64) bool {
	if want == 0 {
		return got == 0
	}
	return math.Abs(got-want) <= 0.1*want
}

// A customer request that failed on one upstream and succeeded on a retry is
// one success on its lane; the failed attempt stays on the upstream's record.
func TestChannelHealthLaneCountsTheAnswerAndUpstreamKeepsTheAttempt(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	if err := db.AutoMigrate(&model.Model{}, &model.ModelMetadata{}, &model.ModelSellPrice{}, &model.Vendor{},
		&model.RouteHealthState{}, &model.RoutingDecision{}, &model.RoutingOutcome{}, &model.RoutingAttemptOutcome{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	const name = "retried-model"
	for _, id := range []int{41, 42} {
		channel := model.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Key: "test", Status: common.ChannelStatusEnabled, Group: model.ChannelLaneDirect, Models: name}
		if err := db.Create(&channel).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.Ability{Group: model.ChannelLaneDirect, Model: name, ChannelId: id, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.Model{ModelName: name, Status: 1, NameRule: model.NameRuleExact}).Error; err != nil {
		t.Fatal(err)
	}
	decision := model.RoutingDecision{ID: 41, RequestID: "retried", DecisionAt: now.Add(-time.Minute), RequestedModel: name, ResolvedModel: name, StrategyName: "test", IsPrimary: true}
	if err := db.Create(&decision).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.RoutingAttemptOutcome{RequestID: "retried", AttemptIndex: 0, OutcomeAt: now.Add(-50 * time.Second),
		DecisionID: 41, ChannelID: 41, FinalGroup: model.ChannelLaneDirect, StatusCode: 502, ErrorCode: "upstream_error"}).Error; err != nil {
		t.Fatal(err)
	}
	status, channel, duration := 200, 42, 1500
	if err := db.Create(&model.RoutingOutcome{RequestID: "retried", OutcomeAt: now.Add(-40 * time.Second), DecisionID: 41,
		ChannelID: &channel, FinalGroup: model.ChannelLaneDirect, StatusCode: &status, TotalDurationMs: &duration}).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	snapshot, err := buildChannelHealthSnapshot(7 * 24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	lane := findLane(t, snapshot.rows, name, "stable")
	if lane.RequestCount != 1 || lane.ErrorCount != 0 || lane.SuccessRate == nil || *lane.SuccessRate != 1 {
		t.Fatalf("lane = %+v, want the request counted once, as the success the customer got", lane)
	}
	for _, source := range lane.Sources {
		if source.ChannelID == 41 && (source.RequestCount != 1 || source.SuccessRate == nil || *source.SuccessRate != 0) {
			t.Fatalf("upstream 41 = %+v, want its failed attempt on its own record", source)
		}
	}
	if lane.AggregateTotals == nil || lane.AggregateTotals.TrafficDurationCount != 1 {
		t.Fatalf("lane latency population = %+v, want only the answer's duration", lane.AggregateTotals)
	}
}
