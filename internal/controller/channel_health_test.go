package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
)

func healthTime(t time.Time) model.ChannelHealthTime {
	return model.ChannelHealthTime{Time: t, Valid: true}
}

func findLane(t *testing.T, rows []channelHealthLaneView, modelName string, lane string) channelHealthLaneView {
	t.Helper()
	for _, row := range rows {
		if row.ModelName == modelName && row.Lane == lane {
			return row
		}
	}
	t.Fatalf("lane %s/%s not found in %d rows", modelName, lane, len(rows))
	return channelHealthLaneView{}
}

func TestEstimateProbeCostUsesSelectedProviderContract(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	input := 2.0
	output := 8.0
	request := 0.001
	if err := db.Create(&model.ProviderCostPrice{
		ModelName: "priced-model", Provider: "upstream", ChannelID: 17,
		InputPerMtok: &input, OutputPerMtok: &output, RequestUSD: &request,
		PricingVersion: 1,
	}).Error; err != nil {
		t.Fatalf("seed provider price: %v", err)
	}

	cost := estimateProbeCostUSD("priced-model", model.ChannelLaneDirect, 17)
	want := 0.001576 // request + 32 input tokens + 64 capped output tokens
	if cost < want-0.000000001 || cost > want+0.000000001 {
		t.Errorf("probe cost = %.9f, want %.9f", cost, want)
	}
	// A reasoning probe may spend its larger cap, and the reservation covers it.
	if err := db.Create(&model.ProviderCostPrice{
		ModelName: "gpt-5", Provider: "upstream", ChannelID: 17,
		InputPerMtok: &input, OutputPerMtok: &output, RequestUSD: &request,
		PricingVersion: 1,
	}).Error; err != nil {
		t.Fatalf("seed reasoning price: %v", err)
	}
	reasoning := estimateProbeCostUSD("gpt-5", model.ChannelLaneDirect, 17)
	wantReasoning := 0.001 + 32*2.0/1e6 + float64(probeReasoningCap)*8.0/1e6
	if reasoning < wantReasoning-0.000000001 || reasoning > wantReasoning+0.000000001 {
		t.Errorf("reasoning probe cost = %.9f, want %.9f", reasoning, wantReasoning)
	}
	// An upstream with no cost row and no sell price is still worth checking.
	// Refusing to price it meant refusing to look at it, and a route we never
	// look at is a permanent hole in the health page — so the unknown is
	// charged at a deliberately pessimistic flat rate instead, and the daily
	// budget stays the thing that bounds spend.
	fallback := estimateProbeCostUSD("priced-model", model.ChannelLaneDirect, 999)
	if fallback != channelProbeFallbackCostUSD {
		t.Errorf("fallback cost = %.9f, want the flat pessimistic rate %.9f",
			fallback, channelProbeFallbackCostUSD)
	}
}

func TestRunChannelProbeSweepZeroBudgetDisablesAllProbes(t *testing.T) {
	t.Setenv("CHANNEL_PROBE_DAILY_BUDGET_USD", "0")
	result, err := RunChannelProbeSweep(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("zero-budget sweep: %v", err)
	}
	if !result.BudgetHit || result.Probed != 0 {
		t.Fatalf("zero-budget sweep = %+v, want disabled without probes", result)
	}
}

func TestProbeSearchChannelExitIsSkipped(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	channel := model.Channel{
		Id: 91, PublicID: "jina-probe", Name: "jina", Key: "test",
		Type: constant.ChannelTypeJina, Status: common.ChannelStatusEnabled,
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed Jina channel: %v", err)
	}
	outcome := probeChannelCell(context.Background(), ChannelProbeCandidate{
		Lane: model.ChannelLaneDirect, ModelName: "bge-reranker-v2-m3", ChannelId: channel.Id,
	}, 0.25)
	if !outcome.Skipped || outcome.Success || outcome.CostUSD != 0 {
		t.Fatalf("search-channel probe = %+v, want a zero-cost skipped result", outcome)
	}
}

func TestProbeUnavailableChannelReleasesReservation(t *testing.T) {
	setupModelOpsControllerTestDB(t)
	outcome := probeChannelCell(context.Background(), ChannelProbeCandidate{
		Lane: model.ChannelLaneDirect, ModelName: "missing-channel-model", ChannelId: 999999,
	}, 0.25)
	if !outcome.Skipped || outcome.Success || outcome.CostUSD != 0 {
		t.Fatalf("unavailable-channel probe = %+v, want a zero-cost skipped result", outcome)
	}
}

func TestSelectProbeEndpointPrefersEmbeddingShapeForEmbeddingModel(t *testing.T) {
	endpoint, ok := selectProbeEndpointType("text-embedding-3-small", []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	})
	if !ok || endpoint != string(constant.EndpointTypeEmbeddings) {
		t.Fatalf("endpoint = %q ok=%t, want embeddings", endpoint, ok)
	}
}

func TestSelectProbeEndpointPrefersExplicitRerankOverBGEInference(t *testing.T) {
	endpoint, ok := selectProbeEndpointType("bge-reranker-v2-m3", []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeJinaRerank,
	})
	if !ok || endpoint != string(constant.EndpointTypeJinaRerank) {
		t.Fatalf("endpoint = %q ok=%t, want jina-rerank", endpoint, ok)
	}
}

func TestProbeEndpointUsesCandidateChannelProtocol(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	endpoint, ok := probeEndpointType(channel, "shared-protocol-model")
	if !ok || endpoint != string(constant.EndpointTypeOpenAI) {
		t.Fatalf("endpoint = %q ok=%t, want candidate channel's OpenAI protocol", endpoint, ok)
	}
	if _, ok := probeEndpointType(nil, "shared-protocol-model"); ok {
		t.Fatal("nil candidate channel must not borrow a model-wide endpoint")
	}
}

func TestProbeEndpointSkipsVideoBeforeChatFallback(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	if endpoint, ok := probeEndpointType(channel, "veo-3-fast"); ok {
		t.Fatalf("video route selected endpoint %q, want an unprobeable media route", endpoint)
	}
}

func TestProbeEndpointSkipsAudioOnlyModelsBeforeChatFallback(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	for _, modelName := range []string{
		"whisper-1",
		"tts-1",
		"gpt-4o-mini-tts",
		"gpt-4o-transcribe",
	} {
		if endpoint, ok := probeEndpointType(channel, modelName); ok {
			t.Errorf("audio-only model %q selected endpoint %q, want unprobeable", modelName, endpoint)
		}
	}
	if endpoint, ok := probeEndpointType(channel, "gpt-4o-audio-preview"); !ok || endpoint != string(constant.EndpointTypeOpenAI) {
		t.Fatalf("chat-capable audio model selected endpoint %q ok=%t, want OpenAI chat", endpoint, ok)
	}
}

func TestChannelProbeUTCDateUsesReservationInstant(t *testing.T) {
	before := channelProbeUTCDate(time.Date(2026, 9, 1, 16, 59, 59, 0, time.FixedZone("west", -7*60*60)))
	after := channelProbeUTCDate(time.Date(2026, 9, 2, 0, 0, 1, 0, time.UTC))
	if before != time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("before ledger day = %s, want 2026-09-01 UTC", before)
	}
	if after != time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("after ledger day = %s, want 2026-09-02 UTC", after)
	}
}

func TestSummarizeCatalogLaneHealthKeepsUnknownEvidenceUnknown(t *testing.T) {
	available := 1.0
	lanes := map[string]map[string]dto.ModelCatalogHealth{
		"mixed-model": {
			"stable": {Status: "available", Availability: &available, ObservedWindow: "sample"},
			"eco":    {Status: "unknown", ObservedWindow: "none"},
		},
	}

	summary := summarizeCatalogLaneHealth([]string{"mixed-model"}, lanes)["mixed-model"]
	if summary.Status != "unknown" || summary.Availability != nil || summary.ObservedWindow != "none" {
		t.Errorf("summary = %+v, want unknown without fabricated availability", summary)
	}
}

// A lane's latency is a measurement whether a customer or a probe took it,
// so the model-level summary keeps the slowest lane's figure.
func TestSummarizeCatalogLaneHealthKeepsSampledLatency(t *testing.T) {
	fullAvailability := 1.0
	noAvailability := 0.0
	stableLatency, ecoLatency := 900, 1800
	lanes := map[string]map[string]dto.ModelCatalogHealth{
		"mixed-model": {
			"stable": {
				Status: "available", Availability: &fullAvailability,
				LatencyP95Ms: &stableLatency, ObservedWindow: "sample",
			},
			"eco": {
				Status: "unavailable", Availability: &noAvailability,
				LatencyP95Ms: &ecoLatency, ObservedWindow: "sample",
			},
		},
	}

	summary := summarizeCatalogLaneHealth([]string{"mixed-model"}, lanes)["mixed-model"]
	if summary.Status != "unavailable" || summary.ObservedWindow != "sample" {
		t.Fatalf("summary = %+v, want sampled unavailable", summary)
	}
	if summary.LatencyP95Ms == nil || *summary.LatencyP95Ms != 1800 {
		t.Errorf("summary latency = %v, want the slowest lane's 1800", summary.LatencyP95Ms)
	}
}

// The catalog reaches every API consumer, so it never says which samples were
// probes: a lane is measured, or it is not.
func TestCatalogHealthFromLaneNeutralizesProvenance(t *testing.T) {
	for source, want := range map[string]string{
		channelHealthSourceTraffic: "sample",
		channelHealthSourceProbe:   "sample",
		channelHealthSourceBlocked: "none",
		channelHealthSourceNone:    "none",
	} {
		health := catalogHealthFromLane(channelHealthLaneView{Source: source, Status: publicStatusOperational})
		if health.ObservedWindow != want {
			t.Errorf("source %s observed = %q, want %q", source, health.ObservedWindow, want)
		}
	}
}

func TestSummarizeChannelHealthSplitsLanes(t *testing.T) {
	rate := func(value float64) *float64 { return &value }
	rows := []channelHealthLaneView{
		{ModelName: "a", Lane: "stable", Status: publicStatusOperational,
			RequestCount: 1000, ErrorCount: 1, SuccessRate: rate(0.999)},
		{ModelName: "a", Lane: "eco", Status: publicStatusDegraded,
			RequestCount: 1000, ErrorCount: 30, SuccessRate: rate(0.97)},
		{ModelName: "b", Lane: "eco", Status: publicStatusUnknown},
	}

	summary := summarizeChannelHealth(rows)

	if summary.ModelCount != 2 {
		t.Errorf("model count = %d, want 2", summary.ModelCount)
	}
	if summary.ActiveIssues != 1 {
		t.Errorf("active issues = %d, want 1", summary.ActiveIssues)
	}
	if summary.NoDataCount != 1 {
		t.Errorf("no-data count = %d, want 1", summary.NoDataCount)
	}
	if summary.StableSuccessRate == nil || *summary.StableSuccessRate < 0.998 {
		t.Errorf("stable rate = %v, want ~0.999", summary.StableSuccessRate)
	}
	if summary.EcoSuccessRate == nil || *summary.EcoSuccessRate > 0.98 {
		t.Errorf("eco rate = %v, want ~0.97", summary.EcoSuccessRate)
	}
}

func TestCatalogHealthPublishesTheHealthPageWeek(t *testing.T) {
	since := time.Date(2026, time.September, 2, 0, 0, 0, 0, time.UTC)
	buckets := emptyChannelHealthBuckets(since, 7200, channelHealthWideBucketCount)
	buckets[0] = channelHealthBucketView{
		Index: 0, StartAt: since, Source: channelHealthSourceTraffic,
		RequestCount: 1000, ErrorCount: 1, Tone: "ok",
	}
	buckets[1] = channelHealthBucketView{
		Index: 1, StartAt: since.Add(2 * time.Hour), Source: channelHealthSourceProbe,
		RequestCount: 2, ErrorCount: 2, Tone: "bad",
	}
	// A blocked mark holds no samples of its own and must not read as data.
	buckets[2] = channelHealthBucketView{
		Index: 2, StartAt: since.Add(4 * time.Hour), Source: channelHealthSourceBlocked, Tone: "blocked",
	}

	public := catalogHealthFromLane(channelHealthLaneView{
		Status: publicStatusDegraded, Source: channelHealthSourceTraffic, Buckets: buckets,
	})
	if public.BucketSeconds != 2*60*60 {
		t.Fatalf("bucket seconds = %d, want 7200", public.BucketSeconds)
	}
	if len(public.Buckets) != channelHealthWideBucketCount {
		t.Fatalf("bucket count = %d, want %d", len(public.Buckets), channelHealthWideBucketCount)
	}
	if got := public.Buckets[0]; got.Source != channelHealthSourceSample || got.Tone != "ok" ||
		got.SuccessRate == nil || *got.SuccessRate != 0.999 || !got.StartAt.Equal(since) {
		t.Fatalf("first bucket = %+v, want the slice's samples one for one", got)
	}
	if got := public.Buckets[1]; got.Source != channelHealthSourceSample || got.Tone != "bad" {
		t.Fatalf("second bucket = %+v, want the failed probes kept", got)
	}
	if got := public.Buckets[2]; got.Source != channelHealthSourceNone || got.Tone != "none" {
		t.Fatalf("blocked bucket = %+v, want neutral no-data evidence", got)
	}
}

func TestSummarizeChannelHealthDividesThroughputTotals(t *testing.T) {
	rows := []channelHealthLaneView{
		// 100 tokens over 10s = 10 tps, on a single request.
		{ModelName: "a", Lane: "stable", Status: publicStatusOperational,
			RequestCount: 1, OutputTokens: 100, DecodeMs: 10_000},
		// 90,000 tokens over 900s = 100 tps, on many requests.
		{ModelName: "b", Lane: "stable", Status: publicStatusOperational,
			RequestCount: 900, OutputTokens: 90_000, DecodeMs: 900_000},
	}

	summary := summarizeChannelHealth(rows)

	// 90,100 tokens over 910s ≈ 99.0 tps. Averaging the two ratios would give
	// something near 55 instead.
	if summary.OutputTPS == nil {
		t.Fatal("expected a platform throughput figure")
	}
	if *summary.OutputTPS < 98 || *summary.OutputTPS > 100 {
		t.Errorf("output tps = %.2f, want ~99 from summed totals", *summary.OutputTPS)
	}
}

func TestFinalizeChannelProbeResultTreatsCancellationAsSkipped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome := finalizeChannelProbeResult(ctx, model.ChannelProbeResult{
		CostUSD: 0.25,
	}, testResult{
		providerAttempted: true,
		localErr:          context.Canceled,
	})

	if !outcome.Skipped || outcome.Success {
		t.Fatalf("canceled probe = %+v, want a skipped non-success", outcome)
	}
	if outcome.CostUSD != 0.25 {
		t.Errorf("canceled in-flight probe cost = %v, want reservation retained", outcome.CostUSD)
	}
	if outcome.ErrorMessage != context.Canceled.Error() {
		t.Errorf("canceled probe error = %q, want %q", outcome.ErrorMessage, context.Canceled)
	}
}

// providerByName keeps the provider assertions readable while the fold is free
// to order the ledger worst-first.
func providerByName(t *testing.T, providers []channelHealthProviderView, name string) channelHealthProviderView {
	t.Helper()
	for _, provider := range providers {
		if provider.ChannelName == name {
			return provider
		}
	}
	t.Fatalf("provider %q not found in %d entries", name, len(providers))
	return channelHealthProviderView{}
}

// Rule 4 of the locked channel-health surface: lanes are the user-facing unit,
// upstreams are not. A member must not learn which vendor served them, so the
// provider ledger has to be absent from their payload — not empty, absent —
// and the provider counters must not leak the shape of the pool either.
func TestGetChannelHealthWithholdsProviderLedgerFromMembers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The member path resolves the caller's accessible models against the
	// catalog, so it needs a database even though this test only asserts on
	// what is withheld.
	setupModelOpsControllerTestDB(t)
	snapshot := channelHealthSnapshot{
		windowSeconds: int64((24 * time.Hour).Seconds()),
		bucketSeconds: 3600,
		since:         time.Now().UTC().Add(-24 * time.Hour),
		rows: []channelHealthLaneView{{
			ModelName: "shared-model", Lane: "stable",
			Status: publicStatusOperational, Source: channelHealthSourceTraffic,
			AggregateTotals: &channelHealthAggregateTotals{Streamed: 10, Interrupted: 1},
		}},
		providers: []channelHealthProviderView{{
			ChannelID: 5, ChannelName: "reseller", Status: publicStatusOutage,
		}},
		updatedAt: time.Now().UTC(),
	}
	channelHealthCache.Lock()
	channelHealthCache.items = map[string]channelHealthSnapshot{"24h": snapshot}
	channelHealthCache.Unlock()
	t.Cleanup(func() {
		channelHealthCache.Lock()
		channelHealthCache.items = map[string]channelHealthSnapshot{}
		channelHealthCache.Unlock()
	})

	call := func(role int) channelHealthResponse {
		t.Helper()
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/channel_health?window=24h", nil)
		c.Set("role", role)
		c.Set("id", 1)
		GetChannelHealth(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var envelope struct {
			Data channelHealthResponse `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return envelope.Data
	}

	member := call(common.RoleCommonUser)
	if len(member.Providers) != 0 {
		t.Fatalf("member payload carries %d providers, want none", len(member.Providers))
	}
	if member.Summary.ProviderCount != 0 || member.Summary.ProviderOutageCount != 0 {
		t.Fatalf("member summary leaks provider counts: %+v", member.Summary)
	}
	admin := call(common.RoleRootUser)
	if len(admin.Rows) != 1 || admin.Rows[0].AggregateTotals != nil {
		t.Fatalf("admin response exposes snapshot-only inputs: %+v", admin.Rows)
	}
	if snapshot.rows[0].AggregateTotals == nil {
		t.Fatal("HTTP response redaction mutated the cached aggregation inputs")
	}
	if len(admin.Providers) != 1 || admin.Providers[0].ChannelName != "reseller" {
		t.Fatalf("admin providers = %+v, want the ledger", admin.Providers)
	}
	if admin.Summary.ProviderCount != 1 || admin.Summary.ProviderOutageCount != 1 {
		t.Fatalf("admin summary = %+v, want one upstream, one down", admin.Summary)
	}
}

// Weight cannot take an upstream out of rotation: the picker adds a base
// weight to every live route, so even weight 0 keeps a share. This test pins
// that fact, because the provider ledger's copy depends on it — an action
// labelled "stop sending traffic here" must not be wired to weight.
func TestAbilityWeightZeroStillReceivesTraffic(t *testing.T) {
	weights := []uint{0, 0, 90}
	sum := uint(0)
	for _, weight := range weights {
		sum += weight + 10
	}
	// A zero-weight route's share is its floor over the total, never zero.
	share := float64(weights[0]+10) / float64(sum)
	if share <= 0 {
		t.Fatalf("zero-weight share = %v, want a non-zero floor share", share)
	}
}

// Admin writes must carry the lane's real slug. The customer-facing key shows
// only two lanes by folding every other one into 稳定, so a write derived from
// it would edit a different route than the one on screen.
func TestChannelHealthRowsCarryTheRealLaneSlugForAdminsOnly(t *testing.T) {
	since := time.Now().UTC().Add(-time.Hour)
	grid := []model.ChannelHealthLaneCell{{
		ModelName: "gpt-5.2", Lane: "turbo", ChannelId: 1,
		ChannelName: "fast", ChannelStatus: common.ChannelStatusEnabled,
		Role: model.AbilityRolePrimary,
	}}
	rows := foldChannelHealth(channelHealthFoldInput{grid: grid, since: since, bucketSeconds: 3600})
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the custom lane to survive the fold", rows)
	}
	if rows[0].Lane != "stable" {
		t.Fatalf("display lane = %q, want the custom lane presented as 稳定", rows[0].Lane)
	}
	if rows[0].LaneSlug != "turbo" {
		t.Fatalf("lane slug = %q, want the registry name an admin write needs", rows[0].LaneSlug)
	}
}

// A source whose last probe was skipped but which is carrying real traffic is
// not a blind spot. Codex-only upstreams can never be probed and serve all
// day; reporting them as unreadable would be a permanent false alarm.
func TestFoldChannelHealthClearsBlockedOnSourcesWithEvidence(t *testing.T) {
	since := time.Now().UTC().Add(-time.Hour)
	grid := []model.ChannelHealthLaneCell{{
		ModelName: "gpt-5.6-terra", Lane: model.ChannelLaneAgent, ChannelId: 83,
		ChannelName: "codex", ChannelStatus: common.ChannelStatusEnabled,
		Role: model.AbilityRolePrimary,
	}}
	blocked := map[string]model.ChannelHealthUnprobeableRow{
		model.ChannelHealthCellKey("gpt-5.6-terra", model.ChannelLaneAgent, 83): {
			ModelName: "gpt-5.6-terra", Lane: model.ChannelLaneAgent, ChannelId: 83,
			Reason: "Codex-only source requires a real Codex session",
		},
	}
	evidence := []model.ChannelHealthEvidenceRow{{
		ModelName: "gpt-5.6-terra", Lane: model.ChannelLaneAgent, ChannelId: 83,
		Kind: model.ChannelHealthKindTraffic, ChannelName: "codex", RequestCount: 500, ErrorCount: 2,
	}}

	row := findLane(t, foldChannelHealth(channelHealthFoldInput{
		grid: grid, evidence: evidence, blockedCells: blocked, since: since, bucketSeconds: 3600,
	}), "gpt-5.6-terra", "eco")
	if row.BlockedCount != 0 {
		t.Fatalf("blocked count = %d, want none — the source is measured by its traffic", row.BlockedCount)
	}
	if len(row.Sources) != 1 || row.Sources[0].BlockedReason != "" {
		t.Fatalf("sources = %+v, want the stale skip reason dropped", row.Sources)
	}
	if row.Status != publicStatusOperational {
		t.Fatalf("status = %q, want operational", row.Status)
	}
}

func TestSearchOverlayFailureKeepsModelHealth(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	// Search backend discovery fails after a usable snapshot was cached.
	if err := db.Migrator().DropTable(&model.Channel{}); err != nil {
		t.Fatal(err)
	}
	snapshot := channelHealthSnapshot{
		windowSeconds: 86400, bucketSeconds: 3600, updatedAt: time.Now().UTC(),
		rows: []channelHealthLaneView{
			{ModelName: "model-ok", Kind: channelHealthKindLLM, Status: publicStatusOperational, Source: channelHealthSourceTraffic},
			{ModelName: "Serper", Kind: channelHealthKindSearch, Provider: "serper", Status: publicStatusOperational, Source: channelHealthSourceTraffic,
				Sources: []channelHealthSourceView{{ChannelID: 1, Status: publicStatusOperational}}},
		},
	}
	channelHealthCache.Lock()
	channelHealthCache.items = map[string]channelHealthSnapshot{"24h": snapshot}
	channelHealthCache.Unlock()
	t.Cleanup(func() {
		channelHealthCache.Lock()
		channelHealthCache.items = map[string]channelHealthSnapshot{}
		channelHealthCache.Unlock()
	})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/channel_health?window=24h", nil)
	c.Set("role", common.RoleRootUser)
	GetChannelHealth(c)
	var envelope struct {
		Success bool                  `json:"success"`
		Data    channelHealthResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Success || len(envelope.Data.Rows) != 2 {
		t.Fatalf("health unavailable: %s", recorder.Body.String())
	}
	if envelope.Data.Rows[0].Status != publicStatusOperational || envelope.Data.Rows[1].Status != publicStatusUnknown {
		t.Fatalf("wrong fallback statuses: %+v", envelope.Data.Rows)
	}
	if snapshot.rows[1].Status != publicStatusOperational || snapshot.rows[1].Sources[0].Status != publicStatusOperational {
		t.Fatal("cached snapshot was mutated")
	}
}
