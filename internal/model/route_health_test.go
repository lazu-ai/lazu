package model

import (
	"context"
	"math"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestRouteHealthHistogramStaysWithinTenPercent(t *testing.T) {
	for _, value := range []int64{10, 11, 37, 100, 250, 999, 1000, 4321, 60_000, 400_000} {
		var h routeHealthHistogram
		h.add(value)
		got := h.quantile(0.5)
		if math.Abs(got-float64(value)) > 0.1*float64(value) {
			t.Errorf("value %d read back as %.1f", value, got)
		}
		round := decodeRouteHealthHistogram(h.encode())
		if round != h {
			t.Errorf("value %d did not survive encoding: %q", value, h.encode())
		}
	}
	var empty routeHealthHistogram
	if empty.quantile(0.95) != 0 || empty.encode() != "" {
		t.Fatal("an empty histogram reads as zero and encodes as nothing")
	}
	// Rank semantics match the SQL: the first rank r with r >= n*fraction.
	var h routeHealthHistogram
	for i := 0; i < 19; i++ {
		h.add(100)
	}
	h.add(10_000)
	if got := h.quantile(0.95); !healthFigureNear(got, 100) {
		t.Fatalf("p95 of 19×100 and 1×10000 = %.1f, want the 19th value", got)
	}
	h.add(10_000)
	if got := h.quantile(0.95); !healthFigureNear(got, 10_000) {
		t.Fatalf("p95 of 19×100 and 2×10000 = %.1f, want the tail", got)
	}
}

func seedRouteHealthOutcome(t *testing.T, requestID string, at time.Time, channel int, status int, duration int) {
	t.Helper()
	db := CoreDB()
	decision := RoutingDecision{ID: stableRoutingDecisionID(requestID), RequestID: requestID, DecisionAt: at, IsPrimary: true,
		RequestedModel: "rollup-model", ResolvedModel: "rollup-model", StrategyName: "test"}
	if err := db.Create(&decision).Error; err != nil {
		t.Fatal(err)
	}
	outcome := RoutingOutcome{RequestID: requestID, OutcomeAt: at, DecisionID: decision.ID, ChannelID: &channel,
		FinalGroup: ChannelLaneDirect, StatusCode: &status, TotalDurationMs: &duration}
	if err := db.Create(&outcome).Error; err != nil {
		t.Fatal(err)
	}
}

func routeHealthSnapshotOf(w RouteHealthWindow, since time.Time) ([]ChannelHealthEvidenceRow, []ChannelHealthBucketRow, []ChannelHealthPercentileRow) {
	evidence := w.Evidence()
	for i := range evidence {
		evidence[i].LastEvidenceAt = ChannelHealthTime{}
	}
	buckets := w.Buckets(since, 7200, 84, true)
	for i := range buckets {
		buckets[i].LastEvidenceAt = ChannelHealthTime{}
	}
	return evidence, buckets, w.Percentiles(RouteHealthPopulation{})
}

// Reading stored hours must give exactly what computing them from raw facts
// gives, and a page must keep working when the rollup task is not running.
func TestRouteHealthRollupMatchesLiveComputation(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	if err := db.Create(&Channel{Id: 31, Name: "rollup-upstream", Status: common.ChannelStatusEnabled}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	since := now.Truncate(2 * time.Hour).Add(2 * time.Hour).Add(-7 * 24 * time.Hour)
	for i := 0; i < 40; i++ {
		status := 200
		if i%7 == 0 {
			status = 502
		}
		seedRouteHealthOutcome(t, "rollup-"+time.Duration(i).String(), now.Add(-time.Duration(i)*97*time.Minute), 31, status, 300+i*40)
	}
	ctx := context.Background()

	live, err := LoadRouteHealthWindow(ctx, since, now)
	if err != nil || len(live.Rows) == 0 {
		t.Fatalf("live window: %+v %v", live.Rows, err)
	}
	if err := RefreshRouteHealthRollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	var stored int64
	db.Model(&RouteHealthHourly{}).Count(&stored)
	if stored == 0 {
		t.Fatal("refresh stored no hours")
	}
	read, err := LoadRouteHealthWindow(ctx, since, now)
	if err != nil {
		t.Fatal(err)
	}
	liveEvidence, liveBuckets, livePercentiles := routeHealthSnapshotOf(live, since)
	readEvidence, readBuckets, readPercentiles := routeHealthSnapshotOf(read, since)
	if !reflect.DeepEqual(liveEvidence, readEvidence) || !reflect.DeepEqual(liveBuckets, readBuckets) || !reflect.DeepEqual(livePercentiles, readPercentiles) {
		t.Fatalf("stored hours differ from live computation\nlive=%+v\nread=%+v", liveEvidence, readEvidence)
	}
	if liveEvidence[0].RequestCount != 40 || liveEvidence[0].ErrorCount != 6 {
		t.Fatalf("evidence = %+v, want 40 requests with 6 failures", liveEvidence[0])
	}

	// A request answered after the last refresh is invisible only while the
	// rollup is fresh; once it is stale the reader computes that hour itself.
	seedRouteHealthOutcome(t, "rollup-late", now.Add(-time.Second), 31, 200, 999)
	fresh, err := LoadRouteHealthWindow(ctx, since, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := fresh.Evidence()[0].RequestCount; got != 40 {
		t.Fatalf("fresh rollup served %d requests, want the 40 it computed", got)
	}
	if err := db.Model(&RouteHealthRollupState{}).Where("name = ?", routeHealthStateName).
		Update("refreshed_at_ms", now.Add(-10*time.Minute).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	stale, err := LoadRouteHealthWindow(ctx, since, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := stale.Evidence()[0].RequestCount; got != 41 {
		t.Fatalf("stale rollup served %d requests, want the late one computed live", got)
	}
	// With no rollup state at all every hour is computed live.
	if err := db.Where("1 = 1").Delete(&RouteHealthRollupState{}).Error; err != nil {
		t.Fatal(err)
	}
	none, err := LoadRouteHealthWindow(ctx, since, now)
	if err != nil || none.Evidence()[0].RequestCount != 41 {
		t.Fatalf("no rollup state: %+v %v", none.Evidence(), err)
	}
}

// A verdict rewritten after the fact (a media job that failed after its 202)
// is corrected by recomputing its hour, not by a compensating write.
func TestRouteHealthRollupFollowsRewrittenVerdicts(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	if err := db.Create(&Channel{Id: 32, Name: "media-upstream", Status: common.ChannelStatusEnabled}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := now.Add(-20 * time.Minute)
	seedRouteHealthOutcome(t, "media-job", at, 32, 202, 1500)
	ctx := context.Background()
	if err := RefreshRouteHealthRollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	failed, failedTrue := 502, true
	if err := db.Model(&RoutingOutcome{}).Where("request_id = ?", "media-job").
		Updates(map[string]interface{}{"status_code": failed, "health_failed": failedTrue, "error_code": "upstream_error"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RefreshRouteHealthRollup(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var rows []RouteHealthHourly
	if err := db.Where("channel_id = ?", 32).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Samples != 1 || rows[0].Failures != 1 {
		t.Fatalf("rows = %+v, want the one request counted once, as failed", rows)
	}
}

func TestRouteHealthRefreshSchedulesDeepPassesAndRetention(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	old := RouteHealthHourly{Hour: routeHealthHourOf(now.Add(-40 * 24 * time.Hour)), ModelName: "old", Lane: ChannelLaneDirect, ChannelID: 1, Kind: ChannelHealthKindTraffic, Samples: 1}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RefreshRouteHealthRollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	state, found, err := RouteHealthRollupStatus(ctx)
	if err != nil || !found || state.ThroughHour != routeHealthHourOf(now) || state.DeepRefreshedAtMs != now.UnixMilli() {
		t.Fatalf("state = %+v found=%v err=%v", state, found, err)
	}
	var kept int64
	db.Model(&RouteHealthHourly{}).Where("model_name = ?", "old").Count(&kept)
	if kept != 0 {
		t.Fatal("hours past retention survived a deep pass")
	}
	later := now.Add(5 * time.Minute)
	if err := RefreshRouteHealthRollup(ctx, later); err != nil {
		t.Fatal(err)
	}
	state, _, _ = RouteHealthRollupStatus(ctx)
	if state.DeepRefreshedAtMs != now.UnixMilli() || state.RefreshedAtMs != later.UnixMilli() {
		t.Fatalf("a refresh five minutes later must stay shallow: %+v", state)
	}
}

func TestRouteHealthPercentilesLeaveOutAttemptsAndParkedRoutes(t *testing.T) {
	encode := func(values ...int64) string {
		var h routeHealthHistogram
		for _, v := range values {
			h.add(v)
		}
		return h.encode()
	}
	window := RouteHealthWindow{Rows: []RouteHealthHourly{
		{Hour: 3600, ModelName: "m", Lane: "direct", ChannelID: 1, Kind: ChannelHealthKindTraffic, Samples: 2, DurationHist: encode(100, 100)},
		{Hour: 3600, ModelName: "m", Lane: "direct", ChannelID: 1, Kind: RouteHealthKindAttempt, Samples: 5, DurationHist: encode(9000, 9000, 9000, 9000, 9000)},
		{Hour: 3600, ModelName: "m", Lane: "direct", ChannelID: 2, Kind: ChannelHealthKindTraffic, Samples: 5, DurationHist: encode(9000, 9000, 9000, 9000, 9000)},
	}}
	rows := window.Percentiles(RouteHealthPopulation{Current: map[string]bool{ChannelHealthCellKey("m", "direct", 1): true}})
	if len(rows) != 1 || !healthFigureNear(rows[0].P95LatencyMs, 100) {
		t.Fatalf("percentiles = %+v, want only channel 1's answers", rows)
	}
	channels := window.ChannelPercentiles(nil)
	sort.Slice(channels, func(i, j int) bool { return channels[i].ChannelId < channels[j].ChannelId })
	if len(channels) != 2 || !healthFigureNear(channels[0].P95LatencyMs, 9000) {
		t.Fatalf("upstream percentiles = %+v, want channel 1's attempts counted", channels)
	}
}
