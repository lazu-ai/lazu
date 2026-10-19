package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/pkg/billing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupModelInfoTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	previous := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMapRWMutex.Unlock()
	if err := db.AutoMigrate(&model.Model{}, &model.ModelInfoReview{}, &model.ModelSellPrice{},
		&model.Option{}, &model.Vendor{}, &model.Ability{}, &model.Channel{}, &model.ModelMetadata{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previous)
	})
	return db
}

func float(v float64) *float64 { return &v }

func TestQuoteModelUsageBillsLongInputAtTheContextTier(t *testing.T) {
	db := setupModelInfoTestDB(t, "quote_context_tier")
	if err := db.Create(&model.ModelSellPrice{
		ModelName: "gpt-6-sol", ChannelGroup: model.ChannelLaneDirect,
		InputPerMtok: float(2), OutputPerMtok: float(10), CacheReadPerMtok: float(0.2),
		ContextTierThreshold: 272_000, ContextTierInputPerMtok: float(4), ContextTierOutputPerMtok: float(15),
		ContextTierCacheReadPerMtok: float(0.4),
		Active:                      true, PricingVersion: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	info := &relaycommon.RelayInfo{UsingGroup: model.ChannelLaneDirect}

	short := QuoteModelUsage(info, "gpt-6-sol", billing.Usage{InputTokens: 12_480, OutputTokens: 1_020}, nil)
	if short.ContextTier != nil || QuoteToQuota(short) != 35_160 {
		t.Fatalf("short request should be billed at the base price, got %d %+v", QuoteToQuota(short), short.ContextTier)
	}

	// 318,442 input: 300,000 fresh + 18,442 cache reads — cache counts toward
	// the threshold and is itself billed at the long-context cache price.
	long := QuoteModelUsage(info, "gpt-6-sol", billing.Usage{InputTokens: 300_000, CacheReadTokens: 18_442, OutputTokens: 2_104}, nil)
	if long.ContextTier == nil || long.ContextTier.Threshold != 272_000 || long.ContextTier.InputTokens != 318_442 {
		t.Fatalf("expected the context tier to be recorded, got %+v", long.ContextTier)
	}
	// 300000*4 + 18442*0.4 + 2104*15 = 1,200,000 + 7,376.8 + 31,560 microusd
	if got := QuoteToQuota(long); got != 1_238_937 {
		t.Fatalf("unexpected long-context charge %d", got)
	}
	raw := QuoteLineItemsJSON(long)
	var decoded map[string]interface{}
	_ = json.Unmarshal([]byte(raw), &decoded)
	if _, ok := decoded["context_tier"]; !ok {
		t.Fatalf("the log line items must carry the context tier: %s", raw)
	}
}

func testCatalog() *ModelsDevIndex {
	yes, no := true, false
	upstream := ModelsDevModel{
		ID: "gpt-6-sol", Name: "GPT-6 Sol", ReleaseDate: "2026-09-22", Knowledge: "2026-04-20",
		Reasoning: true, ToolCall: true, StructuredOutput: &yes, Temperature: &no,
		ReasoningOptions: []ModelsDevReasoningOption{{Type: "effort", Values: []string{"none", "low", "high", "max"}}},
		Cost:             &ModelsDevCost{Input: float(2), Output: float(10), Tiers: []ModelsDevCostTier{{Input: float(4), Output: float(15)}}},
	}
	upstream.Limit.Context, upstream.Limit.Input, upstream.Limit.Output = 1_050_000, 922_000, 128_000
	upstream.Modalities.Input, upstream.Modalities.Output = []string{"text", "image", "pdf"}, []string{"text"}
	upstream.Cost.Tiers[0].Tier.Type, upstream.Cost.Tiers[0].Tier.Size = "context", 272_000

	fable := ModelsDevModel{ID: "claude-fable-5-1", ReleaseDate: "2026-09-01"}
	fable.Limit.Context, fable.Limit.Output = 1_000_000, 128_000
	fresh := ModelsDevModel{ID: "glm-5.3-flashx", ReleaseDate: "2026-09-18"}
	fresh.Limit.Context = 1_000_000
	old := ModelsDevModel{ID: "glm-4", ReleaseDate: "2025-01-01"}

	return NewModelsDevIndex(ModelsDevCatalog{
		"openai":    {ID: "openai", Models: map[string]ModelsDevModel{"gpt-6-sol": upstream}},
		"anthropic": {ID: "anthropic", Models: map[string]ModelsDevModel{"claude-fable-5-1": fable}},
		"zai":       {ID: "zai", Models: map[string]ModelsDevModel{"glm-5.3-flashx": fresh, "glm-4": old}},
		// A relay reselling the same id must never win over the lab.
		"relay": {ID: "relay", Models: map[string]ModelsDevModel{"gpt-6-sol": {ID: "gpt-6-sol", ReleaseDate: "2020-01-01"}}},
	})
}

func reviewsOf(t *testing.T, db *gorm.DB, kind string) []model.ModelInfoReview {
	t.Helper()
	var rows []model.ModelInfoReview
	if err := db.Where("kind = ?", kind).Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestReconcileModelInfoFillsProposesAndFlags(t *testing.T) {
	db := setupModelInfoTestDB(t, "reconcile_model_info")
	models := []model.Model{
		// Everything empty except a context equal to upstream's input limit
		// (the older reading) and a stale per-token official price.
		{ModelName: "gpt-6-sol", ContextLength: 922_000, MaxOutputTokens: 64_000, OfficialInputPrice: 0.000002, OfficialOutputPrice: 0.00001},
		// A legacy release date (the listing day) is proposed as first-run.
		{ModelName: "claude-fable-5-1", ReleaseDate: "2026-09-10", InfoSources: model.ModelInfoSources{"release_date": model.ModelInfoSourceLegacy}},
	}
	for i := range models {
		if err := db.Create(&models[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.ModelSellPrice{ModelName: "gpt-6-sol", ChannelGroup: model.ChannelLaneDirect,
		InputPerMtok: float(2), OutputPerMtok: float(10), Active: true, PricingVersion: 1}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := reconcileModelInfo(testCatalog(), time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 2 {
		t.Fatalf("expected both models matched, got %+v", result)
	}

	var sol model.Model
	db.Where("model_name = ?", "gpt-6-sol").First(&sol)
	if sol.ReleaseDate != "2026-09-22" || sol.KnowledgeCutoff != "2026-04" || sol.InputLimit != 922_000 {
		t.Fatalf("empty facts should be filled from the lab entry, got %+v", sol)
	}
	if sol.Maker != "openai" || sol.ModelsDevID != "openai/gpt-6-sol" {
		t.Fatalf("maker and upstream id should be recorded, got %q %q", sol.Maker, sol.ModelsDevID)
	}
	if sol.ContextLength != 922_000 {
		t.Fatal("context is guarded and must never be overwritten")
	}
	if len(sol.UnsupportedParams) != 1 || sol.UnsupportedParams[0] != "temperature" {
		t.Fatalf("temperature should be recorded as unsupported, got %v", sol.UnsupportedParams)
	}
	if got := sol.ReasoningOptions.Efforts; len(got) != 4 || got[0] != "none" {
		t.Fatalf("reasoning efforts should be recorded, got %v", got)
	}
	if !sol.Capabilities.SupportsFunctionCalling || !sol.Capabilities.SupportsVision {
		t.Fatal("capabilities the lab declares should be switched on")
	}

	diffs := reviewsOf(t, db, model.ModelInfoReviewDiff)
	fields := map[string]model.ModelInfoReview{}
	for _, row := range diffs {
		fields[row.ModelName+"/"+row.Field] = row
	}
	if _, ok := fields["gpt-6-sol/context_length"]; ok {
		t.Fatal("a context equal to the upstream input limit is the same fact, not a disagreement")
	}
	if row, ok := fields["gpt-6-sol/max_output_tokens"]; !ok || row.LocalValue != "64000" || row.UpstreamValue != "128000" {
		t.Fatalf("max output disagreement should be proposed, got %+v", diffs)
	}
	if _, ok := fields["gpt-6-sol/official_price"]; ok {
		t.Fatal("per-token and per-million official prices must compare equal")
	}

	firstRun := reviewsOf(t, db, model.ModelInfoReviewFirstRun)
	if len(firstRun) != 1 || firstRun[0].ModelName != "claude-fable-5-1" || firstRun[0].UpstreamValue != "2026-09-01" || firstRun[0].Note != "9" {
		t.Fatalf("legacy release date should be a first-run correction, got %+v", firstRun)
	}
	var fable model.Model
	db.Where("model_name = ?", "claude-fable-5-1").First(&fable)
	if fable.ReleaseDate != "2026-09-10" {
		t.Fatal("a first-run correction must wait for the operator")
	}

	tierGaps := reviewsOf(t, db, model.ModelInfoReviewTierGap)
	if len(tierGaps) != 1 || tierGaps[0].Note != "272000" || tierGaps[0].UpstreamValue != "4 / 15" || tierGaps[0].LocalValue != "2 / 10" {
		t.Fatalf("the missing long-context tier should be flagged, got %+v", tierGaps)
	}

	fresh := reviewsOf(t, db, model.ModelInfoReviewNewRelease)
	if len(fresh) != 1 || fresh[0].ModelName != "glm-5.3-flashx" || fresh[0].Maker != "zhipu" {
		t.Fatalf("only the recent unlisted lab release should be queued, got %+v", fresh)
	}

	// Accepting the first-run row writes the upstream date and marks its source.
	if err := ApplyModelInfoReview(&firstRun[0]); err != nil {
		t.Fatal(err)
	}
	db.Where("model_name = ?", "claude-fable-5-1").First(&fable)
	if fable.ReleaseDate != "2026-09-01" || fable.InfoSources.Get("release_date") != model.ModelInfoSourceModelsDev {
		t.Fatalf("accepted date not applied: %+v", fable)
	}

	// Undoing a fill clears it and keeps it clear on the next run.
	fills := reviewsOf(t, db, model.ModelInfoReviewFilled)
	var knowledgeFill *model.ModelInfoReview
	for i := range fills {
		if fills[i].ModelName == "gpt-6-sol" && fills[i].Field == "knowledge_cutoff" {
			knowledgeFill = &fills[i]
		}
	}
	if knowledgeFill == nil {
		t.Fatalf("knowledge fill should be logged, got %+v", fills)
	}
	if err := UndoModelInfoFill(knowledgeFill); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileModelInfo(testCatalog(), time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	db.Where("model_name = ?", "gpt-6-sol").First(&sol)
	if sol.KnowledgeCutoff != "" {
		t.Fatal("an undone fill must not come back")
	}
	// A second run does not duplicate pending rows.
	if again := reviewsOf(t, db, model.ModelInfoReviewTierGap); len(again) != 1 {
		t.Fatalf("pending rows must be refreshed, not duplicated, got %d", len(again))
	}
}

func TestLockedModelOnlyGetsProposals(t *testing.T) {
	db := setupModelInfoTestDB(t, "reconcile_locked_model")
	locked := model.Model{ModelName: "gpt-6-sol", InfoLocked: true}
	if err := db.Create(&locked).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileModelInfo(testCatalog(), time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var got model.Model
	db.First(&got, locked.Id)
	if got.Maker != "" || got.ReleaseDate != "" || got.KnowledgeCutoff != "" || got.InputLimit != 0 {
		t.Fatalf("a locked model must not be written, got %+v", got)
	}
	if len(reviewsOf(t, db, model.ModelInfoReviewDiff)) == 0 {
		t.Fatal("a locked model's differences should still be listed")
	}
}

func TestSyncPreservesManualMakerAndDisabledCapabilities(t *testing.T) {
	db := setupModelInfoTestDB(t, "manual_model_facts")
	m := model.Model{ModelName: "gpt-6-sol", InfoSources: model.ModelInfoSources{model.ModelInfoFieldMaker: model.ModelInfoSourceManual}}
	sources := model.RegistrationInfoSources(m, nil, []string{model.ModelInfoFieldCapabilities})
	m.InfoSources[model.ModelInfoFieldCapabilities] = sources[model.ModelInfoFieldCapabilities]
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileModelInfo(testCatalog(), time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var got model.Model
	if err := db.First(&got, m.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.Maker != "" || got.Capabilities.HasAny() {
		t.Fatalf("manual facts overwritten: %+v", got)
	}
}

func TestUndoRejectsOldRunsAndSubsequentManualEdits(t *testing.T) {
	db := setupModelInfoTestDB(t, "undo_model_fills")
	m := model.Model{ModelName: "gpt-6-sol"}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)
	if _, err := reconcileModelInfo(testCatalog(), now); err != nil {
		t.Fatal(err)
	}
	fills := reviewsOf(t, db, model.ModelInfoReviewFilled)
	var knowledge model.ModelInfoReview
	for _, f := range fills {
		if f.Field == model.ModelInfoFieldKnowledgeCutoff {
			knowledge = f
		}
	}
	if knowledge.Id == 0 {
		t.Fatal("missing fill")
	}
	if err := writeModelInfoField(m.ModelName, model.ModelInfoFieldKnowledgeCutoff, `"2026-08"`, model.ModelInfoSourceManual); err != nil {
		t.Fatal(err)
	}
	if err := UndoModelInfoFill(&knowledge); err == nil {
		t.Fatal("undo must not erase a later manual edit")
	}
	if _, err := reconcileModelInfo(testCatalog(), now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	rows, err := model.ListModelInfoReviews()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Kind == model.ModelInfoReviewFilled {
			t.Fatalf("old fill still visible: %+v", r)
		}
	}
	if err := UndoModelInfoFill(&fills[0]); err == nil {
		t.Fatal("old run still undoable")
	}
}

func TestMakerFillCanBeUndone(t *testing.T) {
	db := setupModelInfoTestDB(t, "undo_maker_fill")
	m := model.Model{ModelName: "gpt-6-sol"}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileModelInfo(testCatalog(), time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	for _, f := range reviewsOf(t, db, model.ModelInfoReviewFilled) {
		if f.Field == model.ModelInfoFieldMaker {
			if err := UndoModelInfoFill(&f); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("maker fill not recorded")
}

func TestAcceptReviewDoesNotOverwriteNewerManualFacts(t *testing.T) {
	db := setupModelInfoTestDB(t, "stale_review")
	m := model.Model{ModelName: "gpt-6-sol", MaxOutputTokens: 64000}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileModelInfo(testCatalog(), time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := writeModelInfoField(m.ModelName, model.ModelInfoFieldMaxOutputTokens, "96000", model.ModelInfoSourceManual); err != nil {
		t.Fatal(err)
	}
	for _, row := range reviewsOf(t, db, model.ModelInfoReviewDiff) {
		if row.Field == model.ModelInfoFieldMaxOutputTokens {
			if err := ApplyModelInfoReview(&row); err == nil {
				t.Fatal("stale review overwrote the newer value")
			}
			return
		}
	}
	t.Fatal("missing max output review")
}

func TestSyncPartialOfficialPrices(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		localInput, localOutput float64
		input, output           *float64
		wantInput, wantOutput   float64
		kind                    string
	}{
		{"embedding_fill", 0, 0, float(0.02), nil, 0.02, 0, model.ModelInfoReviewFilled},
		{"input_diff", 1, 5, float(2), nil, 2, 5, model.ModelInfoReviewPriceFollowed},
		{"output_diff", 1, 5, nil, float(10), 1, 10, model.ModelInfoReviewPriceFollowed},
		{"explicit_free", 1, 5, float(0), nil, 0, 5, model.ModelInfoReviewPriceFollowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupModelInfoTestDB(t, "partial_price_"+tc.name)
			m := model.Model{ModelName: "text-embedding-test", OfficialInputPrice: tc.localInput / 1e6, OfficialOutputPrice: tc.localOutput / 1e6}
			if err := db.Create(&m).Error; err != nil {
				t.Fatal(err)
			}
			index := NewModelsDevIndex(ModelsDevCatalog{"openai": {ID: "openai", Models: map[string]ModelsDevModel{
				m.ModelName: {ID: m.ModelName, Cost: &ModelsDevCost{Input: tc.input, Output: tc.output}},
			}}})
			if _, err := reconcileModelInfo(index, time.Now()); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range reviewsOf(t, db, tc.kind) {
				if row.Field != model.ModelInfoFieldOfficialPrice {
					continue
				}
				found = true
				if tc.kind == model.ModelInfoReviewDiff {
					if err := ApplyModelInfoReview(&row); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !found {
				t.Fatal("missing official-price review")
			}
			var got model.Model
			if err := db.First(&got, m.Id).Error; err != nil {
				t.Fatal(err)
			}
			if !floatsClose(got.OfficialInputPrice*1e6, tc.wantInput) || !floatsClose(got.OfficialOutputPrice*1e6, tc.wantOutput) {
				t.Fatalf("partial price lost or overwrote an omitted dimension: %+v", got)
			}
		})
	}
}

func TestSyncFillsOfficialCachePriceBesideAnExistingPrice(t *testing.T) {
	db := setupModelInfoTestDB(t, "official_cache_fill")
	m := model.Model{ModelName: "gpt-6-astra", OfficialInputPrice: 10 / 1e6, OfficialOutputPrice: 50 / 1e6}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	index := NewModelsDevIndex(ModelsDevCatalog{"openai": {ID: "openai", Models: map[string]ModelsDevModel{
		m.ModelName: {ID: m.ModelName, Cost: &ModelsDevCost{Input: float(10), Output: float(50), CacheRead: float(1), CacheWrite: float(12.5)}},
	}}})
	if _, err := reconcileModelInfo(index, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, row := range reviewsOf(t, db, model.ModelInfoReviewDiff) {
		t.Fatalf("an unset cache price must be filled, not proposed: %+v", row)
	}
	var got model.Model
	if err := db.First(&got, m.Id).Error; err != nil {
		t.Fatal(err)
	}
	if !floatsClose(got.OfficialCacheReadPerMtok, 1) || !floatsClose(got.OfficialCacheWritePerMtok, 12.5) {
		t.Fatalf("cache price not filled: read %v write %v", got.OfficialCacheReadPerMtok, got.OfficialCacheWritePerMtok)
	}
	if got.InfoSources.Get(model.ModelInfoFieldOfficialCache) != model.ModelInfoSourceModelsDev {
		t.Fatalf("cache price source = %q", got.InfoSources.Get(model.ModelInfoFieldOfficialCache))
	}

	// A later change upstream is taken, like the input/output price, and
	// filed as a notice.
	index = NewModelsDevIndex(ModelsDevCatalog{"openai": {ID: "openai", Models: map[string]ModelsDevModel{
		m.ModelName: {ID: m.ModelName, Cost: &ModelsDevCost{Input: float(10), Output: float(50), CacheRead: float(0.5), CacheWrite: float(12.5)}},
	}}})
	if _, err := reconcileModelInfo(index, time.Now()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range reviewsOf(t, db, model.ModelInfoReviewPriceFollowed) {
		if row.Field == model.ModelInfoFieldOfficialCache {
			found = true
		}
	}
	if !found {
		t.Fatal("missing followed cache price notice")
	}
	if err := db.First(&got, m.Id).Error; err != nil {
		t.Fatal(err)
	}
	if !floatsClose(got.OfficialCacheReadPerMtok, 0.5) || !floatsClose(got.OfficialCacheWritePerMtok, 12.5) {
		t.Fatalf("followed price wrote read %v write %v", got.OfficialCacheReadPerMtok, got.OfficialCacheWritePerMtok)
	}
}

func TestAcceptAllModelInfoReviewsIsAtomic(t *testing.T) {
	db := setupModelInfoTestDB(t, "atomic_review_batch")
	models := []model.Model{{ModelName: "first", ReleaseDate: "2026-08-01"}, {ModelName: "stale", ReleaseDate: "2026-08-02"}}
	if err := db.Create(&models).Error; err != nil {
		t.Fatal(err)
	}
	rows := []model.ModelInfoReview{}
	for _, m := range models {
		rows = append(rows, model.ModelInfoReview{ModelName: m.ModelName, Kind: model.ModelInfoReviewFirstRun,
			Field: model.ModelInfoFieldReleaseDate, LocalValue: "2026-08-01", UpstreamValue: "2026-09-01", Payload: `"2026-09-01"`})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := ApplyModelInfoReviews(rows); err == nil {
		t.Fatal("stale second review must fail the batch")
	}
	var first model.Model
	if err := db.First(&first, models[0].Id).Error; err != nil {
		t.Fatal(err)
	}
	if first.ReleaseDate != "2026-08-01" {
		t.Fatal("first model partially committed")
	}
	for _, row := range reviewsOf(t, db, model.ModelInfoReviewFirstRun) {
		if row.Status != model.ModelInfoReviewPending {
			t.Fatal("review partially committed")
		}
	}
	if err := db.Model(&rows[1]).Update("local_value", "2026-08-02").Error; err != nil {
		t.Fatal(err)
	}
	if err := ApplyModelInfoReviews(rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range reviewsOf(t, db, model.ModelInfoReviewFirstRun) {
		if row.Status != model.ModelInfoReviewAccepted {
			t.Fatal("review was not accepted")
		}
	}
	var count int64
	if err := db.Model(&model.Model{}).Where("release_date = ?", "2026-09-01").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal("batch did not update both models")
	}
}

func TestManualModelInfoEditsRetireOnlyChangedProposals(t *testing.T) {
	db := setupModelInfoTestDB(t, "manual_review_resolution")
	before := model.Model{ModelName: "gpt-6-sol", Maker: "openai", ReleaseDate: "2026-08-01", KnowledgeCutoff: "2026-01"}
	if err := db.Create(&before).Error; err != nil {
		t.Fatal(err)
	}
	rows := []model.ModelInfoReview{
		{ModelName: before.ModelName, Kind: model.ModelInfoReviewDiff, Field: model.ModelInfoFieldMaker},
		{ModelName: before.ModelName, Kind: model.ModelInfoReviewFirstRun, Field: model.ModelInfoFieldReleaseDate},
		{ModelName: before.ModelName, Kind: model.ModelInfoReviewDiff, Field: model.ModelInfoFieldKnowledgeCutoff},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	after := before
	after.Maker, after.ReleaseDate = "anthropic", "2026-09-01"
	if err := model.ApplyModelInfoEdits(before, after, map[string]json.RawMessage{"maker": json.RawMessage(`"anthropic"`), "release_date": json.RawMessage(`"2026-09-01"`)}); err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		if err := db.First(&rows[i], rows[i].Id).Error; err != nil {
			t.Fatal(err)
		}
		want := model.ModelInfoReviewResolved
		if i == 2 {
			want = model.ModelInfoReviewPending
		}
		if rows[i].Status != want {
			t.Fatalf("unexpected status for %s: %s", rows[i].Field, rows[i].Status)
		}
	}
	var got model.Model
	if err := db.First(&got, before.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.Maker != after.Maker || got.ReleaseDate != after.ReleaseDate || got.InfoSources[model.ModelInfoFieldMaker] != model.ModelInfoSourceManual {
		t.Fatal("manual facts were not saved")
	}
}

func TestModelInfoQueueRetiresDeletedAndRenamedModels(t *testing.T) {
	for _, countFirst := range []bool{false, true} {
		name := "list"
		if countFirst {
			name = "count"
		}
		t.Run(name, func(t *testing.T) {
			db := setupModelInfoTestDB(t, "orphan_review_"+name)
			models := []model.Model{{ModelName: "deleted"}, {ModelName: "renamed"}, {ModelName: "live"}}
			if err := db.Create(&models).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Delete(&models[0]).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&models[1]).Update("model_name", "new-name").Error; err != nil {
				t.Fatal(err)
			}
			rows := []model.ModelInfoReview{}
			for _, modelName := range []string{"deleted", "renamed"} {
				for _, kind := range []string{model.ModelInfoReviewDiff, model.ModelInfoReviewFirstRun, model.ModelInfoReviewTierGap} {
					rows = append(rows, model.ModelInfoReview{ModelName: modelName, Kind: kind})
				}
			}
			rows = append(rows, model.ModelInfoReview{ModelName: "live", Kind: model.ModelInfoReviewDiff}, model.ModelInfoReview{ModelName: "unregistered", Kind: model.ModelInfoReviewNewRelease})
			if err := db.Create(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if countFirst {
				if count, err := model.CountPendingModelInfoReviews(); err != nil || count != 2 {
					t.Fatalf("orphan count: %d, %v", count, err)
				}
			}
			queue, err := model.ListModelInfoReviews()
			if err != nil {
				t.Fatal(err)
			}
			if len(queue) != 2 || queue[0].ModelName != "live" || queue[1].ModelName != "unregistered" {
				t.Fatalf("wrong queue: %+v", queue)
			}
			if count, err := model.CountPendingModelInfoReviews(); err != nil || count != 2 {
				t.Fatalf("orphan count: %d, %v", count, err)
			}
		})
	}
}

func TestFailedModelInfoSyncRollsBackEveryFillAndKeepsRetryUndoable(t *testing.T) {
	for _, tc := range []struct{ name, trigger string }{
		{"later_model", `BEFORE UPDATE ON models WHEN OLD.model_name = 'claude-fable-5-1'`},
		{"review_queue", `BEFORE INSERT ON model_info_reviews WHEN NEW.kind = 'new_release'`},
		{"success_marker", `BEFORE INSERT ON options WHEN NEW.key = 'ModelsDevSyncLastRun'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupModelInfoTestDB(t, "atomic_sync_"+tc.name)
			models := []model.Model{{ModelName: "gpt-6-sol"}, {ModelName: "claude-fable-5-1"}}
			if err := db.Create(&models).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.Option{Key: model.ModelsDevSyncLastRunOptionKey, Value: "123"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("CREATE TRIGGER fail_sync " + tc.trigger + " BEGIN SELECT RAISE(ABORT, 'injected sync failure'); END").Error; err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)
			if result, err := reconcileModelInfo(testCatalog(), now); err == nil || result.Filled != 0 {
				t.Fatalf("failure was swallowed or reported committed fills: %+v, %v", result, err)
			}
			var got model.Model
			if err := db.First(&got, models[0].Id).Error; err != nil {
				t.Fatal(err)
			}
			if got.Maker != "" || got.ReleaseDate != "" || got.ContextLength != 0 {
				t.Fatal("early model changes leaked from failed sync")
			}
			var count int64
			if err := db.Model(&model.ModelInfoReview{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("review rows leaked from failed sync")
			}
			if last, err := model.ModelsDevSyncLastRun(); err != nil || last != 123 {
				t.Fatalf("success marker changed: %d, %v", last, err)
			}
			if err := db.Exec("DROP TRIGGER fail_sync").Error; err != nil {
				t.Fatal(err)
			}
			if _, err := reconcileModelInfo(testCatalog(), now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			queue, err := model.ListModelInfoReviews()
			if err != nil {
				t.Fatal(err)
			}
			undoable := false
			for _, row := range queue {
				if row.Kind == model.ModelInfoReviewFilled && row.ModelName == models[0].ModelName && row.Field == model.ModelInfoFieldReleaseDate {
					if err := UndoModelInfoFill(&row); err != nil {
						t.Fatal(err)
					}
					undoable = true
				}
			}
			if !undoable {
				t.Fatal("retry fills missing from undo queue")
			}
		})
	}
}

func TestReleaseDiscoveryIncludesUnrankedMakers(t *testing.T) {
	db := setupModelInfoTestDB(t, "unranked_releases")
	previous := model.ModelMakerOrder()
	t.Cleanup(func() { _ = model.UpdateOption(model.ModelMakerOrderOptionKey, model.EncodeModelMakerOrder(previous)) })
	if err := model.UpdateOption(model.ModelMakerOrderOptionKey, `["openai"]`); err != nil {
		t.Fatal(err)
	}
	catalog := ModelsDevCatalog{
		"openai":  {ID: "openai", Models: map[string]ModelsDevModel{"gpt-release-test": {ID: "gpt-release-test", ReleaseDate: "2026-09-22"}}},
		"meta":    {ID: "meta", Models: map[string]ModelsDevModel{"llama-release-test": {ID: "llama-release-test", ReleaseDate: "2026-09-22"}}},
		"mistral": {ID: "mistral", Models: map[string]ModelsDevModel{"mistral-release-test": {ID: "mistral-release-test", ReleaseDate: "2026-09-22"}}},
	}
	now := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)
	for _, order := range []string{`["openai"]`, `["meta"]`} {
		if err := model.UpdateOption(model.ModelMakerOrderOptionKey, order); err != nil {
			t.Fatal(err)
		}
		if _, err := reconcileModelInfo(NewModelsDevIndex(catalog), now); err != nil {
			t.Fatal(err)
		}
		rows := reviewsOf(t, db, model.ModelInfoReviewNewRelease)
		if len(rows) != 3 {
			t.Fatalf("unranked makers disappeared: %+v", rows)
		}
		for _, row := range rows {
			if row.Status != model.ModelInfoReviewPending {
				t.Fatalf("reordering retired a release: %+v", row)
			}
		}
	}
}

func TestKeptTierReviewOnlySuppressesUnchangedProposal(t *testing.T) {
	for _, tc := range []struct{ name, note, payload string }{
		{"unchanged", "200000", `{"threshold":200000,"cache_read_per_mtok":0.2}`},
		{"threshold", "300000", `{"threshold":300000,"cache_read_per_mtok":0.2}`},
		{"cache_price", "200000", `{"threshold":200000,"cache_read_per_mtok":0.4}`},
		{"note", "300000", `{"threshold":200000,"cache_read_per_mtok":0.2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupModelInfoTestDB(t, "kept_proposal_"+tc.name)
			row := model.ModelInfoReview{Kind: model.ModelInfoReviewTierGap, Status: model.ModelInfoReviewKept, ModelName: "gpt-6-sol", Field: "context_tier", Note: "200000", Payload: `{"threshold":200000,"cache_read_per_mtok":0.2}`}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			row.Id, row.Status, row.Note, row.Payload = 0, model.ModelInfoReviewPending, tc.note, tc.payload
			if err := model.RecordModelInfoReview(row); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := db.Model(&model.ModelInfoReview{}).Where("status = ?", model.ModelInfoReviewPending).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			want := int64(1)
			if tc.name == "unchanged" {
				want = 0
			}
			if count != want {
				t.Fatalf("pending=%d, want=%d", count, want)
			}
		})
	}
}

func TestKeptReleaseIsNotRecreatedByDuplicateChannelDiscovery(t *testing.T) {
	db := setupModelInfoTestDB(t, "kept_lab_channel_release")
	if err := db.Create(&model.Ability{Model: "glm-5.3-flashx", ChannelId: 1, Group: "direct", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)
	if _, err := reconcileModelInfo(testCatalog(), now); err != nil {
		t.Fatal(err)
	}
	var row model.ModelInfoReview
	if err := db.Where("model_name = ? AND kind = ?", "glm-5.3-flashx", model.ModelInfoReviewNewRelease).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.SetModelInfoReviewStatus(row.Id, model.ModelInfoReviewKept); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileModelInfo(testCatalog(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.ModelInfoReview{}).Where("model_name = ? AND status = ?", row.ModelName, model.ModelInfoReviewPending).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("channel discovery recreated a dismissed lab suggestion")
	}
}

func TestKeptProposalRetiresSupersededPendingRevision(t *testing.T) {
	db := setupModelInfoTestDB(t, "kept_reverted_proposal")
	kept := model.ModelInfoReview{Kind: model.ModelInfoReviewTierGap, ModelName: "gpt-6-sol", Field: "context_tier", Note: "200000", Payload: `{"threshold":200000}`, Status: model.ModelInfoReviewKept}
	if err := db.Create(&kept).Error; err != nil {
		t.Fatal(err)
	}
	revision := kept
	revision.Id, revision.Status, revision.Note, revision.Payload = 0, model.ModelInfoReviewPending, "300000", `{"threshold":300000}`
	if err := model.RecordModelInfoReview(revision); err != nil {
		t.Fatal(err)
	}
	if err := model.RecordModelInfoReview(kept); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.ModelInfoReview{}).Where("status = ?", model.ModelInfoReviewPending).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("superseded revision remained pending after upstream reverted to the dismissed value")
	}
}

func TestChangedReviewGetsNewIDAndRejectsStaleDecisions(t *testing.T) {
	db := setupModelInfoTestDB(t, "immutable_review_revision")
	m := model.Model{ModelName: "gpt-6-sol", MaxOutputTokens: 64000}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	proposal := model.ModelInfoReview{Kind: model.ModelInfoReviewDiff, ModelName: m.ModelName, Field: model.ModelInfoFieldMaxOutputTokens, LocalValue: "64000", UpstreamValue: "128000", Payload: "128000", SyncRun: 1}
	if err := model.RecordModelInfoReview(proposal); err != nil {
		t.Fatal(err)
	}
	old := reviewsOf(t, db, model.ModelInfoReviewDiff)[0]
	proposal.SyncRun = 2
	if err := model.RecordModelInfoReview(proposal); err != nil {
		t.Fatal(err)
	}
	if rows := reviewsOf(t, db, model.ModelInfoReviewDiff); len(rows) != 1 || rows[0].Id != old.Id {
		t.Fatal("unchanged proposal should retain its identity")
	}
	proposal.UpstreamValue, proposal.Payload = "256000", "256000"
	if err := model.RecordModelInfoReview(proposal); err != nil {
		t.Fatal(err)
	}
	rows := reviewsOf(t, db, model.ModelInfoReviewDiff)
	if len(rows) != 2 || rows[0].Status != model.ModelInfoReviewResolved || rows[1].Status != model.ModelInfoReviewPending {
		t.Fatalf("wrong revision lifecycle: %+v", rows)
	}
	if err := ApplyModelInfoReview(&old); err == nil {
		t.Fatal("stale accept succeeded")
	}
	if err := model.KeepModelInfoReview(old.Id); err == nil {
		t.Fatal("stale keep succeeded")
	}
	if err := ApplyModelInfoReview(&rows[1]); err != nil {
		t.Fatal(err)
	}
	var got model.Model
	if err := db.First(&got, m.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.MaxOutputTokens != 256000 {
		t.Fatal("current revision not applied")
	}
}

func TestDuplicateModelFillsHaveOneUndoDecision(t *testing.T) {
	db := setupModelInfoTestDB(t, "duplicate_model_fills")
	models := []model.Model{{ModelName: "gpt-6-sol"}, {ModelName: "gpt-6-sol"}}
	if err := db.Create(&models).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileModelInfo(testCatalog(), time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var fills []model.ModelInfoReview
	if err := db.Where("kind = ? AND field = ?", model.ModelInfoReviewFilled, model.ModelInfoFieldMaker).Find(&fills).Error; err != nil {
		t.Fatal(err)
	}
	if len(fills) != 1 {
		t.Fatalf("duplicate fills: %+v", fills)
	}
	// Also retire duplicates already written by an older server version.
	sibling := fills[0]
	sibling.Id = 0
	if err := db.Create(&sibling).Error; err != nil {
		t.Fatal(err)
	}
	if err := UndoModelInfoFill(&fills[0]); err != nil {
		t.Fatal(err)
	}
	if err := db.Find(&models).Error; err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.Maker != "" {
			t.Fatal("undo did not clear every duplicate model")
		}
	}
	queue, err := model.ListModelInfoReviews()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range queue {
		if row.Kind == model.ModelInfoReviewFilled && row.Field == model.ModelInfoFieldMaker {
			t.Fatal("sibling fill still appears undoable")
		}
	}
}

func TestModelsDevExplicitPinNeverFallsBack(t *testing.T) {
	index := testCatalog()
	for _, pin := range []string{"missing/gpt-6-sol", "openai/deleted", "malformed", "openai/"} {
		if _, ok := index.Lookup("gpt-6-sol", "openai", pin); ok {
			t.Fatalf("invalid pin %q fell back to another entry", pin)
		}
	}
	for _, pin := range []string{"", "openai/gpt-6-sol", "relay/gpt-6-sol"} {
		entry, ok := index.Lookup("gpt-6-sol", "openai", pin)
		if !ok || (pin != "" && entry.ID() != pin) {
			t.Fatalf("explicit valid pin must be honored: %q -> %+v", pin, entry)
		}
	}
}

func TestRegistrationUnknownBooleansCanSyncLater(t *testing.T) {
	db := setupModelInfoTestDB(t, "registration_unknown_booleans")
	m := model.Model{ModelName: "gpt-6-sol"}
	m.InfoSources = model.RegistrationInfoSources(m, nil, nil)
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	index := testCatalog()
	entry := index.byLab["openai"]["gpt-6-sol"]
	entry.Model.OpenWeights = true
	index.byLab["openai"]["gpt-6-sol"] = entry
	if _, err := reconcileModelInfo(index, time.Now()); err != nil {
		t.Fatal(err)
	}
	var got model.Model
	if err := db.First(&got, m.Id).Error; err != nil {
		t.Fatal(err)
	}
	if !got.Capabilities.HasAny() || !got.OpenWeights {
		t.Fatalf("unknown defaults blocked sync: %+v", got)
	}
}

func TestSyncWithdrawsProposalsWhenUpstreamFactsDisappear(t *testing.T) {
	for _, missing := range []string{"fields", "model"} {
		t.Run(missing, func(t *testing.T) {
			db := setupModelInfoTestDB(t, "withdraw_"+missing)
			m := model.Model{ModelName: "gpt-6-sol", ReleaseDate: "2020-01-01", ContextLength: 100, OfficialInputPrice: 0.0001, InfoLocked: true}
			if err := db.Create(&m).Error; err != nil {
				t.Fatal(err)
			}
			index := testCatalog()
			if _, err := reconcileModelInfo(index, time.Now()); err != nil {
				t.Fatal(err)
			}
			firstRun := model.ModelInfoReview{ModelName: m.ModelName, Kind: model.ModelInfoReviewFirstRun, Field: model.ModelInfoFieldReleaseDate, LocalValue: m.ReleaseDate, UpstreamValue: "2026-09-22", Payload: `"2026-09-22"`}
			if err := db.Create(&firstRun).Error; err != nil {
				t.Fatal(err)
			}
			var stale []model.ModelInfoReview
			if err := db.Where("model_name = ? AND status = ? AND field IN ?", m.ModelName, model.ModelInfoReviewPending, []string{model.ModelInfoFieldReleaseDate, model.ModelInfoFieldContextLength, model.ModelInfoFieldOfficialPrice, model.ModelInfoFieldUnsupported}).Find(&stale).Error; err != nil {
				t.Fatal(err)
			}
			if len(stale) != 5 {
				t.Fatalf("expected five proposals, got %d", len(stale))
			}
			entry := index.byLab["openai"][m.ModelName]
			entry.Model.ReleaseDate, entry.Model.Limit.Context, entry.Model.Cost, entry.Model.Temperature = "", 0, nil, nil
			index.byLab["openai"][m.ModelName] = entry
			if missing == "model" {
				delete(index.byLab["openai"], m.ModelName)
			}
			if _, err := reconcileModelInfo(index, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			for _, row := range stale {
				var got model.ModelInfoReview
				if err := db.First(&got, row.Id).Error; err != nil {
					t.Fatal(err)
				}
				if got.Status != model.ModelInfoReviewResolved {
					t.Fatalf("withdrawn %s remains %s", row.Field, got.Status)
				}
				if err := ApplyModelInfoReview(&row); err == nil {
					t.Fatalf("stale %s still accepted", row.Field)
				}
			}
		})
	}
}

func TestTierGapRequiresTheDirectLaneTier(t *testing.T) {
	db := setupModelInfoTestDB(t, "direct_tier_gap")
	entry, _ := testCatalog().Lookup("gpt-6-sol", "openai", "")
	direct := model.ModelSellPrice{ChannelGroup: model.ChannelLaneDirect, InputPerMtok: float(2), OutputPerMtok: float(10)}
	cheap := model.ModelSellPrice{ChannelGroup: model.ChannelLaneAgent, ContextTierThreshold: 100, ContextTierInputPerMtok: float(1), ContextTierOutputPerMtok: float(2)}
	if gap, err := recordTierGap(db, "gpt-6-sol", "openai", entry, []model.ModelSellPrice{cheap, direct}, 1); err != nil || !gap {
		t.Fatalf("agent tier hid direct gap: %v %v", gap, err)
	}
	direct.ContextTierThreshold, direct.ContextTierInputPerMtok, direct.ContextTierOutputPerMtok = 272000, float(4), float(15)
	if gap, err := recordTierGap(db, "gpt-6-sol", "openai", entry, []model.ModelSellPrice{cheap, direct}, 2); err != nil || gap {
		t.Fatalf("configured direct lane still flagged: %v %v", gap, err)
	}
	if gap, err := recordTierGap(db, "gpt-6-sol", "openai", entry, []model.ModelSellPrice{cheap}, 3); err != nil || gap {
		t.Fatalf("absent direct lane flagged: %v %v", gap, err)
	}
}

func TestWholeModelEditIsAtomicAndRenameClearsOldPin(t *testing.T) {
	db := setupModelInfoTestDB(t, "atomic_model_edit")
	before := model.Model{ModelName: "gpt-6-sol", ModelsDevID: "openai/gpt-6-sol", ContextLength: 100, InfoSources: model.ModelInfoSources{model.ModelInfoFieldContextLength: model.ModelInfoSourceModelsDev}}
	if err := db.Create(&before).Error; err != nil {
		t.Fatal(err)
	}
	row := model.ModelInfoReview{ModelName: before.ModelName, Kind: model.ModelInfoReviewDiff, Field: model.ModelInfoFieldContextLength}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER fail_review_cleanup BEFORE UPDATE ON model_info_reviews BEGIN SELECT RAISE(ABORT, 'cleanup failed'); END`).Error; err != nil {
		t.Fatal(err)
	}
	after := before
	after.ContextLength = 200
	if err := after.UpdateWithModelInfo(nil); err == nil {
		t.Fatal("expected cleanup failure")
	}
	var got model.Model
	if err := db.First(&got, before.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.ContextLength != 100 || got.InfoSources.Get(model.ModelInfoFieldContextLength) != model.ModelInfoSourceModelsDev {
		t.Fatalf("partial edit committed: %+v", got)
	}
	if err := db.Exec("DROP TRIGGER fail_review_cleanup").Error; err != nil {
		t.Fatal(err)
	}
	after.ModelName = "gpt-renamed"
	if err := after.UpdateWithModelInfo(map[string]json.RawMessage{"models_dev_id": json.RawMessage(`"openai/gpt-6-sol"`)}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&got, before.Id).Error; err != nil {
		t.Fatal(err)
	}
	if got.ModelsDevID != "" || got.ModelName != after.ModelName || got.ContextLength != 200 || got.InfoSources.Get(model.ModelInfoFieldContextLength) != model.ModelInfoSourceManual {
		t.Fatalf("rename/edit not saved correctly: %+v", got)
	}
	if err := db.First(&row, row.Id).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != model.ModelInfoReviewResolved {
		t.Fatal("old model proposal was not retired")
	}
}

func TestReleaseSuggestionsDeduplicateCaseAcrossLabAndChannels(t *testing.T) {
	db := setupModelInfoTestDB(t, "case_release_suggestions")
	old := model.ModelInfoReview{Kind: model.ModelInfoReviewNewRelease, ModelName: "GPT-6-SOL", Field: "release", Note: "channel"}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	for run := int64(1); run <= 2; run++ {
		_, err := recordNewReleases(db, testCatalog(), []string{"GPT-6-SOL", "gPt-6-sOl", "CUSTOM-MODEL", "custom-model"}, map[string]struct{}{}, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), run)
		if err != nil {
			t.Fatal(err)
		}
		var rows []model.ModelInfoReview
		if err := db.Where("kind = ? AND status = ?", model.ModelInfoReviewNewRelease, model.ModelInfoReviewPending).Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, row := range rows {
			key := strings.ToLower(row.ModelName)
			if seen[key] {
				t.Fatalf("duplicate release %q", key)
			}
			seen[key] = true
			if key == "gpt-6-sol" && row.Note != "lab" {
				t.Fatal("channel replaced lab proposal")
			}
		}
		if !seen["gpt-6-sol"] || !seen["custom-model"] {
			t.Fatal("missing discovered model")
		}
	}
}

func TestKeepReleaseDateConfirmsLegacyAndRejectsStaleReview(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			db := setupModelInfoTestDB(t, fmt.Sprint("keep_release_", stale))
			m := model.Model{ModelName: "deepseek-v4-pro", ReleaseDate: "2026-04-24", InfoSources: model.ModelInfoSources{model.ModelInfoFieldReleaseDate: model.ModelInfoSourceLegacy}}
			if err := db.Create(&m).Error; err != nil {
				t.Fatal(err)
			}
			local := m.ReleaseDate
			if stale {
				local = "2026-04-23"
			}
			row := model.ModelInfoReview{ModelName: m.ModelName, Field: "release_date", Kind: model.ModelInfoReviewFirstRun, Status: model.ModelInfoReviewPending, LocalValue: local, UpstreamValue: "2026-08-12"}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			err := model.KeepModelInfoReview(row.Id)
			if stale != (err != nil) {
				t.Fatalf("stale=%v error=%v", stale, err)
			}
			if err := db.First(&m, m.Id).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&row, row.Id).Error; err != nil {
				t.Fatal(err)
			}
			if stale {
				if m.ConfirmedReleaseDate() != "" || row.Status != model.ModelInfoReviewPending {
					t.Fatal("stale decision changed facts")
				}
			} else if m.ConfirmedReleaseDate() != "2026-04-24" || m.InfoSources.Get("release_date") != model.ModelInfoSourceManual || row.Status != model.ModelInfoReviewKept {
				t.Fatalf("kept date remains unconfirmed: %+v %+v", m, row)
			}
		})
	}
}

func TestKeptReleaseDateSurvivesProvenanceTransition(t *testing.T) {
	for _, change := range []string{"none", "local", "upstream"} {
		t.Run(change, func(t *testing.T) {
			db := setupModelInfoTestDB(t, "keep_date_transition_"+change)
			m := model.Model{ModelName: "gpt-6-sol", ReleaseDate: "2026-04-24", InfoSources: model.ModelInfoSources{model.ModelInfoFieldReleaseDate: model.ModelInfoSourceLegacy}}
			if err := db.Create(&m).Error; err != nil {
				t.Fatal(err)
			}
			sync := func(index *ModelsDevIndex) {
				t.Helper()
				if _, err := reconcileModelInfo(index, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)); err != nil {
					t.Fatal(err)
				}
			}
			sync(testCatalog())
			var proposal model.ModelInfoReview
			if err := db.Where("model_name = ? AND field = ? AND kind = ? AND status = ?", m.ModelName, "release_date", model.ModelInfoReviewFirstRun, model.ModelInfoReviewPending).First(&proposal).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.KeepModelInfoReview(proposal.Id); err != nil {
				t.Fatal(err)
			}
			index := testCatalog()
			if change == "local" {
				if err := db.Model(&m).Update("release_date", "2026-05-01").Error; err != nil {
					t.Fatal(err)
				}
			} else if change == "upstream" {
				index = NewModelsDevIndex(ModelsDevCatalog{"openai": {ID: "openai", Models: map[string]ModelsDevModel{"gpt-6-sol": {ID: "gpt-6-sol", ReleaseDate: "2026-09-10"}}}})
			}
			sync(index)
			sync(index)
			var pending []model.ModelInfoReview
			if err := db.Where("model_name = ? AND field = ? AND status = ?", m.ModelName, "release_date", model.ModelInfoReviewPending).Find(&pending).Error; err != nil {
				t.Fatal(err)
			}
			want := 1
			if change == "none" {
				want = 0
			}
			if len(pending) != want {
				t.Fatalf("change=%s pending=%+v", change, pending)
			}
			if want == 1 && pending[0].Kind != model.ModelInfoReviewDiff {
				t.Fatalf("expected manual date diff: %+v", pending[0])
			}
		})
	}
}

// An official price change is taken and the sell prices that follow it move
// with it; a typed official price is still only proposed.
func TestSyncFollowsOfficialPriceIntoFollowingSellPrices(t *testing.T) {
	db := setupModelInfoTestDB(t, "official_price_follow")
	catalog := []model.Model{
		{ModelName: "gpt-follow", OfficialInputPrice: 2 / 1e6, OfficialOutputPrice: 8 / 1e6},
		{ModelName: "gpt-typed", OfficialInputPrice: 2 / 1e6, OfficialOutputPrice: 8 / 1e6,
			InfoSources: model.ModelInfoSources{model.ModelInfoFieldOfficialPrice: model.ModelInfoSourceManual}},
	}
	if err := db.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	sell := model.ModelSellPrice{ModelName: "gpt-follow", ChannelGroup: "direct", Active: true, PricingVersion: 1,
		FollowOfficial: true, InputPerMtok: float(2), OutputPerMtok: float(8)}
	if err := db.Create(&sell).Error; err != nil {
		t.Fatal(err)
	}
	index := NewModelsDevIndex(ModelsDevCatalog{"openai": {ID: "openai", Models: map[string]ModelsDevModel{
		"gpt-follow": {ID: "gpt-follow", Cost: &ModelsDevCost{Input: float(1.5), Output: float(6)}},
		"gpt-typed":  {ID: "gpt-typed", Cost: &ModelsDevCost{Input: float(1.5), Output: float(6)}},
	}}})
	result, err := reconcileModelInfo(index, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Followed != 1 {
		t.Fatalf("followed = %d", result.Followed)
	}
	var active model.ModelSellPrice
	if err := db.Where("model_name = ? AND active = ?", "gpt-follow", true).First(&active).Error; err != nil {
		t.Fatal(err)
	}
	if !floatsClose(*active.InputPerMtok, 1.5) || !floatsClose(*active.OutputPerMtok, 6) {
		t.Fatalf("sell price did not follow: %+v", active)
	}
	var typed model.Model
	if err := db.Where("model_name = ?", "gpt-typed").First(&typed).Error; err != nil {
		t.Fatal(err)
	}
	if !floatsClose(typed.OfficialInputPrice*1e6, 2) {
		t.Fatalf("a typed official price was overwritten: %v", typed.OfficialInputPrice)
	}
	proposed := false
	for _, row := range reviewsOf(t, db, model.ModelInfoReviewDiff) {
		if row.ModelName == "gpt-typed" && row.Field == model.ModelInfoFieldOfficialPrice {
			proposed = true
		}
	}
	if !proposed {
		t.Fatal("a typed official price must still be proposed")
	}
}
