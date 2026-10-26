package model

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/lazu-ai/lazu/internal/common"
)

// These tests exist because two SQL syntax errors reached review in a row.
// The folding logic had unit tests, but nothing ever executed the statements
// themselves, so a malformed CTE looked exactly like a healthy code path from
// the outside. Running each query against a real database is the only thing
// that catches that class of mistake.
func setupChannelHealthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(
		gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite db handle: %v", err)
	}
	previousDB := coreDB
	previousSQLite := common.UsingSQLite
	coreDB = db
	// The queries branch on dialect for epoch extraction, so the harness has
	// to say which dialect it is or the bucket query emits MySQL syntax.
	common.UsingSQLite = true
	t.Cleanup(func() {
		coreDB = previousDB
		common.UsingSQLite = previousSQLite
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(
		&RoutingDecision{}, &RoutingOutcome{}, &RoutingAttemptOutcome{}, &Channel{},
		&Ability{}, &ChannelProbeResult{}, &ChannelProbeBudget{}, &ChannelProbeClaim{},
		&RouteHealthHourly{}, &RouteHealthRollupState{}, &SearchHealthSample{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// queryChannelHealthTrafficForTest returns the real-traffic half of the
// evidence query, which is what most of these fixtures are about.
func queryChannelHealthTrafficForTest(t *testing.T, since time.Time) ([]ChannelHealthEvidenceRow, error) {
	t.Helper()
	rows, err := QueryChannelHealthEvidence(since)
	if err != nil {
		return nil, err
	}
	traffic := make([]ChannelHealthEvidenceRow, 0, len(rows))
	for _, row := range rows {
		if row.Kind == ChannelHealthKindTraffic {
			traffic = append(traffic, row)
		}
	}
	return traffic, nil
}

func queryChannelHealthProbesForTest(t *testing.T, since time.Time) []ChannelHealthEvidenceRow {
	t.Helper()
	rows, err := QueryChannelHealthEvidence(since)
	if err != nil {
		t.Fatalf("QueryChannelHealthEvidence: %v", err)
	}
	probes := make([]ChannelHealthEvidenceRow, 0, len(rows))
	for _, row := range rows {
		if row.Kind == ChannelHealthKindProbe {
			probes = append(probes, row)
		}
	}
	return probes
}

func TestChannelHealthPostgreSQLBucketIndexFloorsBeforeCast(t *testing.T) {
	previousPostgreSQL := common.UsingPostgreSQL
	previousSQLite := common.UsingSQLite
	common.UsingPostgreSQL = true
	common.UsingSQLite = false
	t.Cleanup(func() {
		common.UsingPostgreSQL = previousPostgreSQL
		common.UsingSQLite = previousSQLite
	})

	got := channelHealthBucketIndexExpr("o.outcome_at")
	want := "CAST(FLOOR((EXTRACT(EPOCH FROM o.outcome_at) - ?) / ?) AS BIGINT)"
	if got != want {
		t.Fatalf("PostgreSQL bucket expression = %q, want %q", got, want)
	}
}

func TestChannelHealthIncludesRetriedUpstreamFailureOnce(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	channels := []Channel{
		{Id: 81, Name: "failed-primary", Status: common.ChannelStatusEnabled},
		{Id: 82, Name: "successful-retry", Status: common.ChannelStatusEnabled},
	}
	if err := db.Create(&channels).Error; err != nil {
		t.Fatalf("seed channels: %v", err)
	}
	if err := db.Create(&RoutingDecision{
		ID: 81, RequestID: "retried-request", DecisionAt: now.Add(-time.Minute),
		RequestedModel: "retry-model", ResolvedModel: "retry-model",
		StrategyName: "test", IsPrimary: true,
	}).Error; err != nil {
		t.Fatalf("seed decision: %v", err)
	}
	if err := db.Create(&RoutingAttemptOutcome{
		RequestID: "retried-request", AttemptIndex: 0, OutcomeAt: now.Add(-2 * time.Second),
		DecisionID: 81, ChannelID: 81, FinalGroup: ChannelLaneDirect,
		StatusCode: 502, ErrorCode: "upstream_error", TotalDurationMs: 900,
	}).Error; err != nil {
		t.Fatalf("seed failed attempt: %v", err)
	}
	statusOK, finalChannel, duration := 200, 82, 1200
	if err := db.Create(&RoutingOutcome{
		RequestID: "retried-request", OutcomeAt: now.Add(-time.Second), DecisionID: 81,
		ChannelID: &finalChannel, FinalGroup: ChannelLaneDirect, StatusCode: &statusOK,
		TotalDurationMs: &duration,
	}).Error; err != nil {
		t.Fatalf("seed final outcome: %v", err)
	}

	rows, err := QueryChannelHealthEvidence(now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("query evidence: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("evidence rows = %+v, want one row for each real upstream attempt", rows)
	}
	byChannel := map[int]ChannelHealthEvidenceRow{}
	for _, row := range rows {
		byChannel[row.ChannelId] = row
	}
	// The superseded attempt stays on the upstream that failed, as its own
	// kind; the customer's request is counted once, by its answer.
	if row := byChannel[81]; row.Kind != RouteHealthKindAttempt || row.RequestCount != 1 || row.ErrorCount != 1 {
		t.Fatalf("primary attempt = %+v, want one failed superseded attempt", row)
	}
	if row := byChannel[82]; row.Kind != ChannelHealthKindTraffic || row.RequestCount != 1 || row.ErrorCount != 0 {
		t.Fatalf("answer = %+v, want one successful request", row)
	}
}

func TestChannelHealthPercentilesExcludeExplicitlyDisabledChannel(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	channels := []Channel{
		{Id: 91, Name: "enabled", Status: common.ChannelStatusEnabled},
		{Id: 92, Name: "disabled", Status: common.ChannelStatusManuallyDisabled},
	}
	if err := db.Create(&channels).Error; err != nil {
		t.Fatalf("seed channels: %v", err)
	}
	for _, channelID := range []int{91, 92} {
		if err := db.Create(&Ability{
			Group: ChannelLaneDirect, Model: "percentile-model", ChannelId: channelID, Enabled: true,
		}).Error; err != nil {
			t.Fatalf("seed ability: %v", err)
		}
	}
	for index, requestID := range []string{"enabled-request", "disabled-request"} {
		decisionID := int64(91 + index)
		if err := db.Create(&RoutingDecision{
			ID: decisionID, RequestID: requestID, DecisionAt: now.Add(-time.Minute),
			RequestedModel: "percentile-model", ResolvedModel: "percentile-model",
			StrategyName: "test", IsPrimary: true,
		}).Error; err != nil {
			t.Fatalf("seed decision: %v", err)
		}
		status, channelID := 200, 91+index
		duration, ttft := 100, 25
		if index == 1 {
			duration, ttft = 10_000, 5_000
		}
		if err := db.Create(&RoutingOutcome{
			RequestID: requestID, OutcomeAt: now.Add(-time.Second), DecisionID: decisionID,
			ChannelID: &channelID, FinalGroup: ChannelLaneDirect, StatusCode: &status,
			TotalDurationMs: &duration, TTFTMs: &ttft, AnswerTTFTMs: &ttft,
		}).Error; err != nil {
			t.Fatalf("seed outcome: %v", err)
		}
	}

	rows, err := QueryChannelHealthPercentiles(now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("query percentiles: %v", err)
	}
	if len(rows) != 1 || !healthFigureNear(rows[0].P95LatencyMs, 100) || !healthFigureNear(rows[0].TTFTP50Ms, 25) {
		t.Fatalf("percentiles = %+v, want enabled channel's 100/25 only", rows)
	}
}

func TestChannelHealthLatencyExcludesFailedTraffic(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&Channel{Id: 93, Name: "latency-source", Status: common.ChannelStatusEnabled}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := db.Create(&Ability{Group: ChannelLaneDirect, Model: "latency-model", ChannelId: 93, Enabled: true}).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
	for index, duration := range []int{1000, 2000, 5, 9000} {
		requestID := fmt.Sprintf("latency-%d", index)
		decisionID := int64(930 + index)
		if err := db.Create(&RoutingDecision{
			ID: decisionID, RequestID: requestID, DecisionAt: now.Add(-time.Minute),
			RequestedModel: "latency-model", ResolvedModel: "latency-model",
		}).Error; err != nil {
			t.Fatalf("seed decision: %v", err)
		}
		status := 200
		if index >= 2 {
			status = 502
		}
		channelID, ttft := 93, duration/2
		if err := db.Create(&RoutingOutcome{
			RequestID: requestID, OutcomeAt: now.Add(-time.Second), DecisionID: decisionID,
			ChannelID: &channelID, FinalGroup: ChannelLaneDirect, StatusCode: &status,
			TotalDurationMs: &duration, TTFTMs: &ttft, AnswerTTFTMs: &ttft,
		}).Error; err != nil {
			t.Fatalf("seed outcome: %v", err)
		}
	}
	rows, err := queryChannelHealthTrafficForTest(t, now.Add(-time.Hour))
	if err != nil || len(rows) != 1 {
		t.Fatalf("traffic rows = %+v, err=%v", rows, err)
	}
	if rows[0].RequestCount != 4 || rows[0].ErrorCount != 2 || rows[0].DurationCount != 2 || rows[0].DurationSumMs != 3000 {
		t.Fatalf("traffic evidence = %+v, want two successful durations", rows[0])
	}
	percentiles, err := QueryChannelHealthPercentiles(now.Add(-time.Hour))
	if err != nil || len(percentiles) != 1 {
		t.Fatalf("percentiles = %+v, err=%v", percentiles, err)
	}
	if !healthFigureNear(percentiles[0].P50LatencyMs, 1000) || !healthFigureNear(percentiles[0].P95LatencyMs, 2000) {
		t.Fatalf("percentiles = %+v, want 1000/2000", percentiles[0])
	}
}

func TestRecordChannelProbeTruncatesUTF8OnByteBoundary(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	message := strings.Repeat("错", 200)
	if err := RecordChannelProbe(ChannelProbeResult{
		Lane: ChannelLaneDirect, ModelName: "utf8-model", ChannelId: 9,
		ProbedAt: time.Now().UTC(), ErrorMessage: message,
	}); err != nil {
		t.Fatalf("record probe: %v", err)
	}
	var stored ChannelProbeResult
	if err := db.Where("model_name = ?", "utf8-model").First(&stored).Error; err != nil {
		t.Fatalf("load probe: %v", err)
	}
	if len(stored.ErrorMessage) > 512 || !utf8.ValidString(stored.ErrorMessage) {
		t.Fatalf("stored error must be valid UTF-8 within 512 bytes: len=%d valid=%t",
			len(stored.ErrorMessage), utf8.ValidString(stored.ErrorMessage))
	}
}

func TestReserveChannelProbeBudgetEnforcesCeilingAndRelease(t *testing.T) {
	setupChannelHealthTestDB(t)
	day := time.Now().UTC().Truncate(24 * time.Hour)
	reserved, err := ReserveChannelProbeBudget(day, 1, 0.6)
	if err != nil || !reserved {
		t.Fatalf("first reservation: reserved=%t err=%v", reserved, err)
	}
	reserved, err = ReserveChannelProbeBudget(day, 1, 0.5)
	if err != nil || reserved {
		t.Fatalf("over-budget reservation: reserved=%t err=%v", reserved, err)
	}
	if err := ReleaseChannelProbeBudget(day, 0.2); err != nil {
		t.Fatalf("release reservation: %v", err)
	}
	reserved, err = ReserveChannelProbeBudget(day, 1, 0.5)
	if err != nil || !reserved {
		t.Fatalf("reservation after release: reserved=%t err=%v", reserved, err)
	}
}

func TestChannelProbeClaimExcludesConcurrentOwner(t *testing.T) {
	setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	cellKey := ChannelHealthCellKey("claim-model", ChannelLaneDirect, 41)
	claimed, err := ClaimChannelProbe(cellKey, "owner-a", now, now.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("first claim: claimed=%t err=%v", claimed, err)
	}
	claimed, err = ClaimChannelProbe(cellKey, "owner-b", now, now.Add(time.Minute))
	if err != nil || claimed {
		t.Fatalf("competing claim: claimed=%t err=%v", claimed, err)
	}
	if err := ReleaseChannelProbeClaim(cellKey, "owner-a"); err != nil {
		t.Fatalf("release first claim: %v", err)
	}
	claimed, err = ClaimChannelProbe(cellKey, "owner-b", now, now.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim after release: claimed=%t err=%v", claimed, err)
	}
}

func TestChannelHealthUsesPersistedProviderVerdicts(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	channelID := 51
	if err := db.Create(&Channel{Id: channelID, Name: "verdict-channel", Status: common.ChannelStatusEnabled}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	decisions := []RoutingDecision{
		{ID: 51, RequestID: "stream-failure", DecisionAt: now, RequestedModel: "verdict-model", ResolvedModel: "verdict-model", StrategyName: "test", IsPrimary: true},
		{ID: 52, RequestID: "local-rate-limit", DecisionAt: now, RequestedModel: "verdict-model", ResolvedModel: "verdict-model", StrategyName: "test", IsPrimary: true},
	}
	if err := db.Create(&decisions).Error; err != nil {
		t.Fatalf("seed decisions: %v", err)
	}
	statusOK, statusLimited := 200, 429
	providerAttempted, eligible, failed := true, true, true
	localAttempted, localEligible, localFailed := false, false, false
	outcomes := []RoutingOutcome{
		{RequestID: "stream-failure", OutcomeAt: now, DecisionID: 51, ChannelID: &channelID,
			FinalGroup: ChannelLaneDirect, StatusCode: &statusOK, ErrorCode: "upstream_network_error",
			ProviderAttempted: &providerAttempted, HealthEligible: &eligible, HealthFailed: &failed},
		{RequestID: "local-rate-limit", OutcomeAt: now, DecisionID: 52, ChannelID: &channelID,
			FinalGroup: ChannelLaneDirect, StatusCode: &statusLimited, ErrorCode: "token_rate_limit_exceeded",
			ProviderAttempted: &localAttempted, HealthEligible: &localEligible, HealthFailed: &localFailed},
	}
	if err := db.Create(&outcomes).Error; err != nil {
		t.Fatalf("seed outcomes: %v", err)
	}

	rows, err := queryChannelHealthTrafficForTest(t, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("query traffic: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestCount != 1 || rows[0].ErrorCount != 1 {
		t.Fatalf("traffic = %+v, want only the failed post-header provider stream", rows)
	}
}

func TestChannelHealthUsesStampedFinalGroup(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&Channel{
		Id: 44, Name: "shared-channel", Type: 1,
		Status: common.ChannelStatusEnabled, Group: ChannelLaneDirect,
	}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := db.Create(&RoutingDecision{
		ID: 44, RequestID: "eco-request", DecisionAt: now.Add(-time.Minute),
		RequestedModel: "lane-model", ResolvedModel: "lane-model",
		StrategyName: "test", IsPrimary: true,
	}).Error; err != nil {
		t.Fatalf("seed decision: %v", err)
	}
	status := 200
	channelID := 44
	if err := db.Create(&RoutingOutcome{
		RequestID: "eco-request", OutcomeAt: now.Add(-time.Minute), DecisionID: 44,
		ChannelID: &channelID, FinalGroup: ChannelLaneAgent, StatusCode: &status,
	}).Error; err != nil {
		t.Fatalf("seed outcome: %v", err)
	}

	rows, err := queryChannelHealthTrafficForTest(t, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("query traffic: %v", err)
	}
	if len(rows) != 1 || rows[0].Lane != ChannelLaneAgent {
		t.Fatalf("traffic rows = %+v, want request-time agent lane despite direct channel group", rows)
	}
}

func TestChannelHealthExcludesLegacyOutcomesWithoutStampedLane(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&Channel{
		Id: 45, Name: "legacy-channel", Type: 1,
		Status: common.ChannelStatusEnabled, Group: ChannelLaneDirect,
	}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := db.Create(&RoutingDecision{
		ID: 45, RequestID: "legacy-request", DecisionAt: now.Add(-time.Minute),
		RequestedModel: "legacy-model", ResolvedModel: "legacy-model",
		StrategyName: "test", IsPrimary: true,
	}).Error; err != nil {
		t.Fatalf("seed decision: %v", err)
	}
	status := 200
	channelID := 45
	if err := db.Create(&RoutingOutcome{
		RequestID: "legacy-request", OutcomeAt: now.Add(-time.Minute), DecisionID: 45,
		ChannelID: &channelID, StatusCode: &status,
	}).Error; err != nil {
		t.Fatalf("seed legacy outcome: %v", err)
	}
	// RoutingOutcome's write hook stamps new rows as direct. Reset this one
	// without hooks to reproduce a row that predates the final_group column.
	if err := db.Model(&RoutingOutcome{}).
		Where("request_id = ?", "legacy-request").
		UpdateColumn("final_group", "").Error; err != nil {
		t.Fatalf("clear legacy final_group: %v", err)
	}

	since := now.Add(-time.Hour)
	traffic, err := queryChannelHealthTrafficForTest(t, since)
	if err != nil || len(traffic) != 0 {
		t.Fatalf("legacy traffic = %+v err=%v, want no unverifiable lane evidence", traffic, err)
	}
	percentiles, err := QueryChannelHealthPercentiles(since)
	if err != nil || len(percentiles) != 0 {
		t.Fatalf("legacy percentiles = %+v err=%v, want none", percentiles, err)
	}
	last, err := QueryChannelHealthLastOutcomesContext(context.Background(), since)
	if err != nil || len(last) != 0 {
		t.Fatalf("legacy last outcomes = %+v err=%v, want none", last, err)
	}
	buckets, err := QueryChannelHealthBuckets(since, 150, 24)
	if err != nil || len(buckets) != 0 {
		t.Fatalf("legacy buckets = %+v err=%v, want none", buckets, err)
	}
}

func seedChannelHealthFixture(t *testing.T, db *gorm.DB, now time.Time) {
	t.Helper()
	if err := db.Create(&Channel{
		Id: 1, Name: "openai-main", Type: 1, Status: 1, Group: ChannelLaneDirect,
	}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := db.Create(&Ability{
		Group: ChannelLaneDirect, Model: "gpt-5.2", ChannelId: 1, Enabled: true, Weight: 100,
	}).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
	if err := db.Create(&RoutingDecision{
		ID: 1, RequestID: "req-1", DecisionAt: now.Add(-time.Hour),
		RequestedModel: "gpt-5.2", ResolvedModel: "gpt-5.2",
	}).Error; err != nil {
		t.Fatalf("seed decision: %v", err)
	}

	channelID := 1
	statusOK := 200
	duration := 2000
	ttft := 400
	output := 800
	for index := 0; index < 3; index++ {
		if err := db.Create(&RoutingOutcome{
			RequestID:          fmt.Sprintf("req-%d", index+1),
			OutcomeAt:          now.Add(-time.Duration(index+1) * time.Hour),
			DecisionID:         1,
			ChannelID:          &channelID,
			FinalGroup:         ChannelLaneDirect,
			StatusCode:         &statusOK,
			TotalDurationMs:    &duration,
			TTFTMs:             &ttft,
			AnswerTTFTMs:       &ttft,
			ActualOutputTokens: &output,
		}).Error; err != nil {
			t.Fatalf("seed outcome: %v", err)
		}
	}
	if err := db.Create(&ChannelProbeResult{
		Lane: ChannelLaneDirect, ModelName: "gpt-5.2", ChannelId: 1,
		ProbedAt: now.Add(-10 * time.Minute), Success: true, LatencyMs: 500,
	}).Error; err != nil {
		t.Fatalf("seed probe: %v", err)
	}
}

func TestChannelHealthQueriesExecute(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	seedChannelHealthFixture(t, db, now)
	since := now.Add(-24 * time.Hour)

	traffic, err := queryChannelHealthTrafficForTest(t, since)
	if err != nil {
		t.Fatalf("QueryChannelHealthTraffic: %v", err)
	}
	if len(traffic) != 1 {
		t.Fatalf("expected 1 traffic row, got %d", len(traffic))
	}
	row := traffic[0]
	if row.ModelName != "gpt-5.2" || row.Lane != ChannelLaneDirect {
		t.Errorf("row identity = %s/%s, want gpt-5.2/direct", row.ModelName, row.Lane)
	}
	if row.RequestCount != 3 || row.ErrorCount != 0 {
		t.Errorf("counts = %d/%d, want 3/0", row.RequestCount, row.ErrorCount)
	}
	// 3 requests x 800 tokens, decoding for (2000-400)ms each.
	if row.OutputTokens != 2400 {
		t.Errorf("output tokens = %d, want 2400", row.OutputTokens)
	}
	if row.DecodeMs != 4800 {
		t.Errorf("decode ms = %d, want 4800", row.DecodeMs)
	}
	if !row.LastEvidenceAt.Valid {
		t.Error("expected a last outcome timestamp")
	}

	percentiles, err := QueryChannelHealthPercentiles(since)
	if err != nil {
		t.Fatalf("QueryChannelHealthPercentiles: %v", err)
	}
	if len(percentiles) != 1 {
		t.Fatalf("expected 1 percentile row, got %d", len(percentiles))
	}
	if !healthFigureNear(percentiles[0].P95LatencyMs, 2000) {
		t.Errorf("p95 = %v, want 2000", percentiles[0].P95LatencyMs)
	}
	if !healthFigureNear(percentiles[0].TTFTP50Ms, 400) {
		t.Errorf("ttft p50 = %v, want 400", percentiles[0].TTFTP50Ms)
	}

	if !healthFigureNear(percentiles[0].ProbeP95LatencyMs, 0) {
		t.Errorf("probe p95 = %v, want 0 because the legacy probe has no streamed output", percentiles[0].ProbeP95LatencyMs)
	}

	probes := queryChannelHealthProbesForTest(t, since)
	if len(probes) != 1 || probes[0].RequestCount != 1 || probes[0].ErrorCount != 0 {
		t.Errorf("probes = %+v, want one successful probe", probes)
	}

	buckets, err := QueryChannelHealthBuckets(since, 3600, 24)
	if err != nil {
		t.Fatalf("QueryChannelHealthBuckets: %v", err)
	}
	var trafficBuckets, probeBuckets int
	for _, bucket := range buckets {
		if bucket.Kind == ChannelHealthKindProbe {
			probeBuckets++
			continue
		}
		trafficBuckets++
	}
	if trafficBuckets == 0 || probeBuckets == 0 {
		t.Errorf("expected both traffic and probe buckets, got %d/%d",
			trafficBuckets, probeBuckets)
	}

	grid, err := LoadChannelHealthGrid()
	if err != nil {
		t.Fatalf("LoadChannelHealthGrid: %v", err)
	}
	if len(grid) != 1 || grid[0].ModelName != "gpt-5.2" || grid[0].ChannelName != "openai-main" {
		t.Errorf("grid = %+v, want one gpt-5.2 route on openai-main", grid)
	}

	lastOutcomes, err := QueryChannelHealthLastOutcomesContext(context.Background(), since)
	if err != nil {
		t.Fatalf("QueryChannelHealthLastOutcomes: %v", err)
	}
	if len(lastOutcomes) != 1 || !lastOutcomes[0].LastOutcomeAt.Valid {
		t.Errorf("last outcomes = %+v, want one timestamped route", lastOutcomes)
	}
}

// A non-streaming outcome carries no TTFT, so it must contribute to neither the
// throughput ratio nor the TTFT median.
func TestChannelHealthExcludesNonStreamingFromThroughput(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	seedChannelHealthFixture(t, db, now)

	channelID := 1
	statusOK := 200
	duration := 9000
	output := 5000
	if err := db.Create(&RoutingOutcome{
		RequestID: "req-nonstream", OutcomeAt: now.Add(-30 * time.Minute),
		DecisionID: 1, ChannelID: &channelID, FinalGroup: ChannelLaneDirect, StatusCode: &statusOK,
		TotalDurationMs: &duration, ActualOutputTokens: &output,
	}).Error; err != nil {
		t.Fatalf("seed non-streaming outcome: %v", err)
	}

	traffic, err := queryChannelHealthTrafficForTest(t, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("QueryChannelHealthTraffic: %v", err)
	}
	if len(traffic) != 1 {
		t.Fatalf("expected 1 row, got %d", len(traffic))
	}
	if traffic[0].RequestCount != 4 {
		t.Errorf("request count = %d, want 4", traffic[0].RequestCount)
	}
	// The non-streaming request must not add its 5000 tokens or its 9s.
	if traffic[0].OutputTokens != 2400 || traffic[0].DecodeMs != 4800 {
		t.Errorf("throughput inputs = %d tokens / %d ms, want 2400 / 4800 — a "+
			"non-streaming request has no measurable decode phase",
			traffic[0].OutputTokens, traffic[0].DecodeMs)
	}

	percentiles, err := QueryChannelHealthPercentiles(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("QueryChannelHealthPercentiles: %v", err)
	}
	// Only three outcomes have a TTFT, all 400ms, so the median stays 400
	// rather than being dragged toward zero by the fourth.
	if !healthFigureNear(percentiles[0].TTFTP50Ms, 400) {
		t.Errorf("ttft p50 = %v, want 400", percentiles[0].TTFTP50Ms)
	}
}

// Probes that never reached an upstream are recorded for scheduling but are not
// health evidence.
func TestChannelHealthProbesExcludeSkipped(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	seedChannelHealthFixture(t, db, now)

	if err := db.Create(&ChannelProbeResult{
		Lane: ChannelLaneDirect, ModelName: "gpt-5.2", ChannelId: 1,
		ProbedAt: now.Add(-5 * time.Minute), Success: false, Skipped: true,
		ErrorMessage: "relay context unavailable",
	}).Error; err != nil {
		t.Fatalf("seed skipped probe: %v", err)
	}

	probes := queryChannelHealthProbesForTest(t, now.Add(-24*time.Hour))
	if len(probes) != 1 {
		t.Fatalf("expected 1 probe row, got %d", len(probes))
	}
	if probes[0].RequestCount != 1 || probes[0].ErrorCount != 0 {
		t.Errorf("probe aggregate = %+v — a skipped attempt must not count as "+
			"a failed liveness check", probes[0])
	}
}

// Channel availability must describe the channel, not its callers. A malformed
// request or a content-policy refusal is the caller's fault; charging it to the
// channel would make a lane look broken because its users sent bad requests.
// This mirrors categorizeForMetrics in the distributor.
func TestChannelHealthCountsOnlyChannelFaults(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	seedChannelHealthFixture(t, db, now)

	channelID := 1
	add := func(requestID string, status int) {
		t.Helper()
		code := status
		if err := db.Create(&RoutingOutcome{
			RequestID: requestID, OutcomeAt: now.Add(-20 * time.Minute),
			DecisionID: 1, ChannelID: &channelID, FinalGroup: ChannelLaneDirect, StatusCode: &code,
			ErrorType: "upstream_error",
		}).Error; err != nil {
			t.Fatalf("seed outcome %s: %v", requestID, err)
		}
	}
	add("caller-400", 400) // malformed request — the caller's fault
	add("caller-403", 403) // content policy — the caller's fault
	add("channel-500", 500)
	add("channel-429", 429)

	// A non-generative call — an embedding, a moderation — legitimately returns
	// no output tokens. The zero-output failure rule is gated on evidence that a
	// generation was attempted precisely so these stay successes.
	statusOK := 200
	if err := db.Create(&RoutingOutcome{
		RequestID: "embedding-200", OutcomeAt: now.Add(-20 * time.Minute),
		DecisionID: 1, ChannelID: &channelID, FinalGroup: ChannelLaneDirect,
		StatusCode: &statusOK,
	}).Error; err != nil {
		t.Fatalf("seed embedding outcome: %v", err)
	}

	traffic, err := queryChannelHealthTrafficForTest(t, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("QueryChannelHealthTraffic: %v", err)
	}
	if len(traffic) != 1 {
		t.Fatalf("expected 1 row, got %d", len(traffic))
	}
	// The two caller-fault responses belong in neither side of the availability
	// ratio. Counting them as successes would let bad requests inflate a failing
	// route toward 100%%.
	if traffic[0].RequestCount != 6 {
		t.Errorf("request count = %d, want 6", traffic[0].RequestCount)
	}
	// Only the 500 and the 429 are the channel's.
	if traffic[0].ErrorCount != 2 {
		t.Errorf("error count = %d, want 2 — a 4xx from the caller is not a "+
			"channel failure, and a call that never asked for output is not a "+
			"failed generation", traffic[0].ErrorCount)
	}

	buckets, err := QueryChannelHealthBuckets(now.Add(-24*time.Hour), 3600, 24)
	if err != nil {
		t.Fatalf("QueryChannelHealthBuckets: %v", err)
	}
	var bucketRequests int64
	for _, bucket := range buckets {
		if bucket.Kind == ChannelHealthKindTraffic {
			bucketRequests += bucket.RequestCount
			if bucket.ChannelId != channelID {
				t.Errorf("traffic bucket channel_id = %d, want %d", bucket.ChannelId, channelID)
			}
		}
	}
	if bucketRequests != 6 {
		t.Errorf("bucket request population = %d, want 6 without caller faults", bucketRequests)
	}
}

// An upstream that forwards its own error inside an already-committed 200
// stream ends the request with a clean status, no error code, no tokens and a
// $0 billing row. We shipped that as "healthy" once. The absence of output is
// the only evidence of it, so the reporting query treats it as a failure even
// when the router's live verdict said success.
func TestChannelHealthCountsZeroOutputSuccessAsFailure(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	seedChannelHealthFixture(t, db, now)

	channelID := 1
	statusOK := 200
	streaming := true
	healthy := false
	input := 1200
	zero := 0
	seed := func(outcome *RoutingOutcome) {
		t.Helper()
		outcome.OutcomeAt = now.Add(-20 * time.Minute)
		outcome.DecisionID = 1
		outcome.ChannelID = &channelID
		outcome.FinalGroup = ChannelLaneDirect
		outcome.StatusCode = &statusOK
		if err := db.Create(outcome).Error; err != nil {
			t.Fatalf("seed outcome %s: %v", outcome.RequestID, err)
		}
	}
	// The incident itself: a committed stream that died without producing a
	// token. The Go side had already called it a success.
	seed(&RoutingOutcome{
		RequestID: "stream-died", WasStreaming: &streaming, HealthFailed: &healthy,
		ActualInputTokens: &input, ActualOutputTokens: &zero,
	})
	// An embedding records its prompt and no output figure. Not streamed, it
	// is a correctly empty answer — and a non-streamed empty chat completion
	// is stored in the same shape, so neither can be read as a failure.
	seed(&RoutingOutcome{
		RequestID: "embedding", ActualInputTokens: &input,
	})
	seed(&RoutingOutcome{RequestID: "moderation"})

	traffic, err := queryChannelHealthTrafficForTest(t, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("QueryChannelHealthTraffic: %v", err)
	}
	if len(traffic) != 1 {
		t.Fatalf("expected 1 row, got %d", len(traffic))
	}
	if traffic[0].RequestCount != 6 {
		t.Fatalf("request count = %d, want 6", traffic[0].RequestCount)
	}
	if traffic[0].ErrorCount != 1 {
		t.Errorf("error count = %d, want 1 — the dead stream is a failure and "+
			"the embedding is not", traffic[0].ErrorCount)
	}

	// The heatmap reads the same expression, so a cell must not disagree with
	// the row above it.
	buckets, err := QueryChannelHealthBuckets(now.Add(-24*time.Hour), 3600, 24)
	if err != nil {
		t.Fatalf("QueryChannelHealthBuckets: %v", err)
	}
	var bucketErrors int64
	for _, bucket := range buckets {
		if bucket.Kind == ChannelHealthKindTraffic {
			bucketErrors += bucket.ErrorCount
		}
	}
	if bucketErrors != 1 {
		t.Errorf("bucket error population = %d, want 1", bucketErrors)
	}
}

// Interruptions and stalls are reported as totals so the ratio can be divided
// once. Averaged per request, a handful of tiny calls would outvote the long
// ones the numbers exist to describe.
func TestChannelHealthAggregatesInterruptionAndStall(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	seedChannelHealthFixture(t, db, now)

	channelID := 1
	statusOK := 200
	duration := 1000
	ttft := 100
	streamed := true
	seed := func(requestID string, output, stall int, aborted bool) {
		t.Helper()
		outputTokens, stallMs, abortedFlag := output, stall, aborted
		prompt := 1000
		if err := db.Create(&RoutingOutcome{
			RequestID: requestID, OutcomeAt: now.Add(-20 * time.Minute),
			DecisionID: 1, ChannelID: &channelID, FinalGroup: ChannelLaneDirect,
			StatusCode: &statusOK, TotalDurationMs: &duration, TTFTMs: &ttft, AnswerTTFTMs: &ttft,
			ActualInputTokens: &prompt, ActualOutputTokens: &outputTokens,
			MaxStallMs: &stallMs, WasStreaming: &streamed, StreamAborted: &abortedFlag,
		}).Error; err != nil {
			t.Fatalf("seed outcome %s: %v", requestID, err)
		}
	}
	seed("finished", 50, 300, false)
	seed("broke-midway", 100, 900, true)

	traffic, err := queryChannelHealthTrafficForTest(t, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("QueryChannelHealthTraffic: %v", err)
	}
	if len(traffic) != 1 {
		t.Fatalf("expected 1 row, got %d", len(traffic))
	}
	row := traffic[0]
	// Only the two seeded here streamed output. The fixture's rows did not
	// stream, and a reply that was never a stream cannot be interrupted.
	if row.InterruptedCount != 1 || row.StreamedCount != 2 {
		t.Errorf("interruption totals = %d/%d, want 1/2", row.InterruptedCount, row.StreamedCount)
	}

	percentiles, err := QueryChannelHealthPercentiles(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("QueryChannelHealthPercentiles: %v", err)
	}
	if len(percentiles) != 1 {
		t.Fatalf("expected 1 percentile row, got %d", len(percentiles))
	}
	// Ranked over the two rows that actually streamed. The three fixture rows
	// have no stall at all and must not drag the median to zero.
	if !healthFigureNear(percentiles[0].StallP50Ms, 300) {
		t.Errorf("stall p50 = %v, want 300 over observed stalls only",
			percentiles[0].StallP50Ms)
	}
}

// A reply delivered in one frame has no decode span to measure. Counting it
// divides its whole output by a millisecond and reports tens of thousands of
// tokens a second, which then swamps every honest sample it is summed with.
func TestChannelHealthDropsUnmeasurableDecodeSpans(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	probes := []ChannelProbeResult{
		{Lane: ChannelLaneDirect, ModelName: "buffered", ChannelId: 1,
			ProbedAt: now.Add(-time.Minute), Success: true, LatencyMs: 1101,
			TTFTMs: 1100, AnswerTTFTMs: 1100, OutputTokens: 60, Modality: "text"},
		{Lane: ChannelLaneDirect, ModelName: "buffered", ChannelId: 1,
			ProbedAt: now.Add(-2 * time.Minute), Success: true, LatencyMs: 1600,
			TTFTMs: 800, AnswerTTFTMs: 800, OutputTokens: 64, Modality: "text"},
	}
	if err := db.Create(&probes).Error; err != nil {
		t.Fatalf("seed probes: %v", err)
	}
	rows := queryChannelHealthProbesForTest(t, now.Add(-24*time.Hour))
	if len(rows) != 1 {
		t.Fatalf("probe rows = %+v", rows)
	}
	if rows[0].OutputTokens != 64 || rows[0].DecodeMs != 800 {
		t.Errorf("throughput inputs = %d/%d, want only the measurable 64/800",
			rows[0].OutputTokens, rows[0].DecodeMs)
	}
}

// Decode speed counts only what streamed during its span. With thinking
// omitted, an upstream reasons for 7.2s and then streams the answer, while
// output_tokens still counts the hidden reasoning: the reasoning comes out.
// When reasoning streams — before the answer, or as the whole reply of a
// probe that never reached one — it was generated inside the span and stays.
// A row from before the answer time was recorded, or a reply with almost
// nothing streamed, carries no sample.
func TestChannelHealthDecodeSpeedCountsOnlyStreamedTokens(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	probe := func(minutes, latency, ttft, answer, output, reasoning int) ChannelProbeResult {
		return ChannelProbeResult{Lane: ChannelLaneDirect, ModelName: "reasoner", ChannelId: 1,
			ProbedAt: now.Add(-time.Duration(minutes) * time.Minute), Success: true, LatencyMs: latency,
			TTFTMs: ttft, AnswerTTFTMs: answer, OutputTokens: output, ReasoningTokens: reasoning, Modality: "text"}
	}
	probes := []ChannelProbeResult{
		probe(1, 13100, 7200, 7200, 1850, 855),  // hidden thinking: 995 over 5900ms
		probe(2, 14200, 2700, 10100, 2041, 953), // streamed thinking: 2041 over 11500ms
		probe(3, 2600, 1400, 0, 64, 0),          // only reasoned, streamed: 64 over 1200ms
		probe(4, 5000, 4900, 4900, 512, 504),    // 8 answer tokens: too few
		probe(5, 9000, 1000, 1000, 900, 0),      // pre-answer-time row, nulled below
	}
	if err := db.Create(&probes).Error; err != nil {
		t.Fatalf("seed probes: %v", err)
	}
	if err := db.Model(&ChannelProbeResult{}).Where("id = ?", probes[4].Id).
		Update("answer_ttft_ms", gorm.Expr("NULL")).Error; err != nil {
		t.Fatalf("null legacy answer time: %v", err)
	}
	rows := queryChannelHealthProbesForTest(t, now.Add(-24*time.Hour))
	if len(rows) != 1 {
		t.Fatalf("probe rows = %+v", rows)
	}
	if want := int64(995 + 2041 + 64); rows[0].OutputTokens != want {
		t.Errorf("streamed tokens = %d, want %d", rows[0].OutputTokens, want)
	}
	if want := int64(5900 + 11500 + 1200); rows[0].DecodeMs != want {
		t.Errorf("decode span = %d ms, want %d", rows[0].DecodeMs, want)
	}
}

// An embedding or image probe is the same request a customer sends, so its
// duration is a latency sample. A text probe's is not: its reply is capped.
func TestChannelHealthCountsNonTextProbeDurations(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	probes := []ChannelProbeResult{
		{Lane: ChannelLaneDirect, ModelName: "text-embedding-3-small", ChannelId: 1,
			ProbedAt: now.Add(-time.Minute), Success: true, LatencyMs: 320, Modality: "embedding"},
		{Lane: ChannelLaneDirect, ModelName: "legacy-text", ChannelId: 1,
			ProbedAt: now.Add(-time.Minute), Success: true, LatencyMs: 900},
	}
	if err := db.Create(&probes).Error; err != nil {
		t.Fatalf("seed probes: %v", err)
	}
	rows := queryChannelHealthProbesForTest(t, now.Add(-24*time.Hour))
	durations := map[string]int64{}
	for _, row := range rows {
		durations[row.ModelName] = row.DurationSumMs
	}
	if durations["text-embedding-3-small"] != 320 {
		t.Errorf("embedding duration = %d, want 320", durations["text-embedding-3-small"])
	}
	if durations["legacy-text"] != 0 {
		t.Errorf("unstreamed text probe duration = %d, want 0", durations["legacy-text"])
	}
}

// A probe is one more sample of its route: every probe in the window counts,
// not only the newest, and a streamed probe's timings join the lane's
// first-token, stall and throughput figures.
func TestChannelHealthCountsEveryProbeAsASample(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	seedChannelHealthFixture(t, db, now)
	probes := []ChannelProbeResult{
		{Lane: ChannelLaneDirect, ModelName: "gpt-5.2", ChannelId: 1,
			ProbedAt: now.Add(-time.Minute), Success: false, LatencyMs: 900},
		{Lane: ChannelLaneDirect, ModelName: "gpt-5.2", ChannelId: 1,
			ProbedAt: now.Add(-2 * time.Minute), Success: true, LatencyMs: 1400,
			TTFTMs: 600, AnswerTTFTMs: 600, MaxStallMs: 250, OutputTokens: 64},
	}
	if err := db.Create(&probes).Error; err != nil {
		t.Fatalf("seed probes: %v", err)
	}

	since := now.Add(-24 * time.Hour)
	rows := queryChannelHealthProbesForTest(t, since)
	if len(rows) != 1 || rows[0].RequestCount != 3 || rows[0].ErrorCount != 1 {
		t.Fatalf("probe evidence = %+v, want all three probes with one failure", rows)
	}
	// Only the streamed success decoded anything: 64 tokens over 800ms.
	if rows[0].OutputTokens != 64 || rows[0].DecodeMs != 800 {
		t.Errorf("probe throughput inputs = %d/%d, want 64/800", rows[0].OutputTokens, rows[0].DecodeMs)
	}
	// A failed probe's latency is how fast the upstream said no.
	if rows[0].DurationCount != 1 || rows[0].DurationSumMs != 1400 {
		t.Errorf("probe durations = %d over %d, want only the new streamed probe's 1400",
			rows[0].DurationSumMs, rows[0].DurationCount)
	}

	percentiles, err := QueryChannelHealthPercentiles(since)
	if err != nil {
		t.Fatalf("QueryChannelHealthPercentiles: %v", err)
	}
	if len(percentiles) != 1 {
		t.Fatalf("expected 1 percentile row, got %d", len(percentiles))
	}
	// Traffic TTFTs are 400, 400, 400 and the probe adds 600: the median is
	// still 400, but the probe is in the population.
	if !healthFigureNear(percentiles[0].TTFTP50Ms, 400) {
		t.Errorf("ttft p50 = %v, want 400", percentiles[0].TTFTP50Ms)
	}
	if !healthFigureNear(percentiles[0].StallP50Ms, 250) {
		t.Errorf("stall p50 = %v, want the probe's 250 as the only observed stall", percentiles[0].StallP50Ms)
	}
	if !healthFigureNear(percentiles[0].P95LatencyMs, 2000) {
		t.Errorf("traffic p95 = %v, want 2000 untouched by probes", percentiles[0].P95LatencyMs)
	}

	buckets, err := QueryChannelHealthBuckets(since, 3600, 24)
	if err != nil {
		t.Fatalf("QueryChannelHealthBuckets: %v", err)
	}
	var probeCount, probeErrors int64
	for _, bucket := range buckets {
		if bucket.Kind == ChannelHealthKindProbe {
			probeCount += bucket.RequestCount
			probeErrors += bucket.ErrorCount
		}
	}
	if probeCount != 3 || probeErrors != 1 {
		t.Errorf("probe buckets = %d/%d, want 3 samples with 1 failure", probeCount, probeErrors)
	}
}

// A route the lane does not count — an idle standby — is left out of its
// percentiles, so its probes cannot move the lane's first-token time.
func TestChannelHealthPercentilesLeaveOutExcludedRoutes(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	seedChannelHealthFixture(t, db, now)
	if err := db.Create(&Channel{Id: 2, Name: "standby", Type: 1, Status: 1, Group: ChannelLaneDirect}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := db.Create(&Ability{
		Group: ChannelLaneDirect, Model: "gpt-5.2", ChannelId: 2, Enabled: true, Role: AbilityRoleBackup,
	}).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := db.Create(&ChannelProbeResult{
			Lane: ChannelLaneDirect, ModelName: "gpt-5.2", ChannelId: 2,
			ProbedAt: now.Add(-time.Duration(i+1) * time.Minute), Success: true,
			LatencyMs: 9000, TTFTMs: 8000, AnswerTTFTMs: 8000, MaxStallMs: 3000, OutputTokens: 64,
		}).Error; err != nil {
			t.Fatalf("seed probe: %v", err)
		}
	}
	since := now.Add(-24 * time.Hour)

	with, err := QueryChannelHealthPercentiles(since)
	if err != nil || len(with) != 1 || !healthFigureNear(with[0].TTFTP50Ms, 8000) {
		t.Fatalf("unfiltered percentiles = %+v err=%v, want the standby's 8000 to dominate", with, err)
	}
	without, err := QueryChannelHealthPercentiles(since, ChannelHealthRouteRef{
		ModelName: "gpt-5.2", Lane: ChannelLaneDirect, ChannelId: 2,
	})
	if err != nil || len(without) != 1 || !healthFigureNear(without[0].TTFTP50Ms, 400) || !healthFigureNear(without[0].StallP50Ms, 0) {
		t.Fatalf("filtered percentiles = %+v err=%v, want the primary's 400 and no stall", without, err)
	}
}

func TestChannelHealthPercentilesRankFoldedAliasesTogether(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&Channel{Id: 7, Name: "alias provider", Type: 1, Status: common.ChannelStatusEnabled}).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gpt-4o-gizmo-*", "gpt-4o-gizmo-a", "gpt-4o-gizmo-b"} {
		if err := db.Create(&Ability{Group: ChannelLaneDirect, Model: name, ChannelId: 7, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, sample := range []struct {
		name string
		ms   int
	}{
		{"gpt-4o-gizmo-*", 100},
		{"gpt-4o-gizmo-a", 900},
		{"gpt-4o-gizmo-b", 1100},
	} {
		if err := db.Create(&ChannelProbeResult{
			Lane: ChannelLaneDirect, ModelName: sample.name, ChannelId: 7,
			ProbedAt: now.Add(-time.Duration(i+1) * time.Minute), Success: true,
			LatencyMs: sample.ms + 100, TTFTMs: sample.ms, AnswerTTFTMs: sample.ms, OutputTokens: 64,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows, err := QueryChannelHealthPercentilesWithAliases(now.Add(-time.Hour), map[string]string{
		"gpt-4o-gizmo-a": "gpt-4o-gizmo-*",
		"gpt-4o-gizmo-b": "gpt-4o-gizmo-*",
	})
	if err != nil || len(rows) != 1 {
		t.Fatalf("folded percentile rows = %+v, err = %v", rows, err)
	}
	if rows[0].ModelName != "gpt-4o-gizmo-*" || !healthFigureNear(rows[0].TTFTP50Ms, 900) || !healthFigureNear(rows[0].ProbeP95LatencyMs, 1200) {
		t.Fatalf("folded percentile = %+v, want median 900 and probe p95 1200", rows[0])
	}
}

func TestChannelHealthPublicPercentilesPoolSamplesBeforeRanking(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	for _, channel := range []Channel{
		{Id: 1, Status: common.ChannelStatusEnabled},
		{Id: 2, Status: common.ChannelStatusEnabled},
		{Id: 3, Status: common.ChannelStatusManuallyDisabled},
	} {
		if err := db.Create(&channel).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, ability := range []Ability{
		{Group: ChannelLaneDirect, Model: "pooled", ChannelId: 1, Enabled: true},
		{Group: "turbo", Model: "pooled-variant", ChannelId: 2, Enabled: true},
		{Group: ChannelLaneAgent, Model: "pooled", ChannelId: 1, Enabled: true},
		{Group: "standby", Model: "pooled", ChannelId: 1, Enabled: true, Role: AbilityRoleBackup},
		{Group: ChannelLaneDirect, Model: "pooled", ChannelId: 3, Enabled: true},
		{Group: ChannelLaneDirect, Model: "other", ChannelId: 1, Enabled: true},
	} {
		if err := db.Create(&ability).Error; err != nil {
			t.Fatal(err)
		}
	}
	probes := []ChannelProbeResult{}
	add := func(name, lane string, channel, count, ttft int) {
		for i := 0; i < count; i++ {
			probes = append(probes, ChannelProbeResult{
				ModelName: name, Lane: lane, ChannelId: channel, ProbedAt: now.Add(-time.Minute),
				Success: true, TTFTMs: ttft, AnswerTTFTMs: ttft, LatencyMs: ttft * 10, MaxStallMs: ttft / 10, OutputTokens: 64,
			})
		}
	}
	add("pooled", ChannelLaneDirect, 1, 9, 100)
	add("pooled-variant", "turbo", 2, 1, 1000)
	add("pooled", ChannelLaneAgent, 1, 1, 50)
	add("pooled", "standby", 1, 20, 9000)         // explicitly excluded
	add("pooled", ChannelLaneDirect, 3, 20, 9000) // disabled channel
	add("pooled", "removed", 1, 20, 9000)         // no current ability
	add("other", ChannelLaneDirect, 1, 1, 30)     // outside selected models
	if err := db.Create(&probes).Error; err != nil {
		t.Fatal(err)
	}
	rows, err := QueryChannelHealthPublicPercentilesWithAliases(now.Add(-time.Hour),
		map[string]string{"pooled-variant": "pooled"}, []string{"pooled"},
		ChannelHealthRouteRef{ModelName: "pooled", Lane: "standby", ChannelId: 1})
	if err != nil || len(rows) != 2 {
		t.Fatalf("public quantiles = %+v, err=%v", rows, err)
	}
	for _, row := range rows {
		if row.ModelName != "pooled" {
			t.Fatalf("unexpected model: %+v", row)
		}
		switch row.Lane {
		case ChannelLaneDirect:
			if !healthFigureNear(row.TTFTP50Ms, 100) || !healthFigureNear(row.TTFTP95Ms, 1000) || !healthFigureNear(row.ProbeP50LatencyMs, 1000) || !healthFigureNear(row.ProbeP95LatencyMs, 10000) || !healthFigureNear(row.StallP50Ms, 10) {
				t.Fatalf("pooled 9:1 population must retain median 100 and tail 1000: %+v", row)
			}
		case ChannelLaneAgent:
			if !healthFigureNear(row.TTFTP50Ms, 50) || !healthFigureNear(row.ProbeP50LatencyMs, 500) {
				t.Fatalf("cheap mixed into stable: %+v", row)
			}
		default:
			t.Fatalf("hidden lane survived public grouping: %+v", row)
		}
	}
	if empty, err := QueryChannelHealthPublicPercentilesWithAliases(now.Add(-time.Hour), nil, nil); err != nil || len(empty) != 0 {
		t.Fatalf("no collisions must skip pooled query: %+v / %v", empty, err)
	}
}

func TestChannelHealthLegacyProbeDoesNotSetQuietLaneLatency(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&Channel{Id: 8, Name: "quiet", Type: 1, Status: common.ChannelStatusEnabled}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Ability{Group: ChannelLaneDirect, Model: "quiet-model", ChannelId: 8, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, probe := range []ChannelProbeResult{
		{Lane: ChannelLaneDirect, ModelName: "quiet-model", ChannelId: 8,
			ProbedAt: now.Add(-2 * time.Hour), Success: true, LatencyMs: 9000},
		{Lane: ChannelLaneDirect, ModelName: "quiet-model", ChannelId: 8,
			ProbedAt: now.Add(-time.Hour), Success: true, LatencyMs: 1200, TTFTMs: 400, AnswerTTFTMs: 400, OutputTokens: 64},
	} {
		if err := db.Create(&probe).Error; err != nil {
			t.Fatal(err)
		}
	}
	evidence := queryChannelHealthProbesForTest(t, now.Add(-24*time.Hour))
	if len(evidence) != 1 || evidence[0].RequestCount != 2 || evidence[0].DurationCount != 1 {
		t.Fatalf("probe evidence = %+v, want both availability samples but one duration", evidence)
	}
	rows, err := QueryChannelHealthPercentiles(now.Add(-24 * time.Hour))
	if err != nil || len(rows) != 1 || !healthFigureNear(rows[0].ProbeP95LatencyMs, 1200) {
		t.Fatalf("probe p95 = %+v, err = %v; want only the new 1200ms sample", rows, err)
	}
}

// When the sample cap binds, the page must keep the newest samples: status,
// streak and the right-hand cells all read from the end of the window.
func TestQuerySearchHealthSamplesKeepsTheNewestWhenCapped(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	rows := make([]SearchHealthSample, 0, searchHealthSampleLimit+2)
	for i := 0; i < searchHealthSampleLimit+2; i++ {
		rows = append(rows, SearchHealthSample{
			ChannelId: 55, Provider: "tavily", Kind: SearchHealthKindProbe, Success: true,
			SampledAt: now.Add(-time.Duration(searchHealthSampleLimit+2-i) * time.Second),
		})
	}
	if err := db.CreateInBatches(&rows, 1000).Error; err != nil {
		t.Fatalf("seed samples: %v", err)
	}
	got, err := QuerySearchHealthSamples(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != searchHealthSampleLimit {
		t.Fatalf("rows = %d, want the cap %d", len(got), searchHealthSampleLimit)
	}
	newest := rows[len(rows)-1].SampledAt
	if !got[len(got)-1].SampledAt.Equal(newest) || !got[0].SampledAt.Before(got[len(got)-1].SampledAt) {
		t.Fatalf("window = %s .. %s, want oldest-first ending at the newest sample %s",
			got[0].SampledAt, got[len(got)-1].SampledAt, newest)
	}
}

// healthFigureNear accepts a percentile read back from the hourly histograms:
// within 10% of the exact value, and exactly zero when nothing was observed.
func healthFigureNear(got, want float64) bool {
	if want == 0 {
		return got == 0
	}
	return math.Abs(got-want) <= 0.1*want
}

// A video liveness check reads a task's status. It must not enter the
// route's success rate: a lane whose API answers but whose every generation
// fails would otherwise read as healthy. A canary generation does count.
func TestChannelHealthEvidenceExcludesVideoLivenessChecks(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	now := time.Now().UTC()
	for index, row := range []ChannelProbeResult{
		{Success: true, Modality: ChannelProbeModalityVideoLiveness},
		{Success: false, Modality: ChannelProbeModalityVideoLiveness},
		{Success: false, Modality: "video", LatencyMs: 90000},
	} {
		row.Lane, row.ModelName, row.ChannelId = ChannelLaneDirect, "seedance-health", 7
		row.ProbedAt = now.Add(-time.Duration(10+index) * time.Minute)
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed probe: %v", err)
		}
	}
	rows, err := QueryChannelHealthEvidence(now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("query evidence: %v", err)
	}
	var probes []ChannelHealthEvidenceRow
	for _, row := range rows {
		if row.Kind == ChannelHealthKindProbe && row.ModelName == "seedance-health" {
			probes = append(probes, row)
		}
	}
	if len(probes) != 1 || probes[0].RequestCount != 1 || probes[0].ErrorCount != 1 {
		t.Fatalf("probe evidence = %+v, want only the canary generation", probes)
	}
}
