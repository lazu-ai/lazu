package model

import (
	"strconv"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestModelInfoNormalizationAndLegacyDates(t *testing.T) {
	for _, m := range []Model{{Maker: "unknown"}, {ReleaseDate: "2026/09/27"}, {ReleaseDate: "2026-02-30"}, {InputLimit: -1}} {
		if NormalizeModelInfo(&m) == nil {
			t.Fatalf("invalid facts accepted: %+v", m)
		}
	}
	m := Model{Maker: " OpenAI ", ReleaseDate: " 2026-09-27 "}
	if err := NormalizeModelInfo(&m); err != nil || m.Maker != "openai" || m.ReleaseDate != "2026-09-27" {
		t.Fatalf("normalize: %+v %v", m, err)
	}
	m.InfoSources = ModelInfoSources{ModelInfoFieldReleaseDate: ModelInfoSourceLegacy}
	ts := time.Now().Unix()
	p := Pricing{ReleasedAt: &ts}
	p.applyModelInfo(&m, "", "2026-09-28")
	if p.ReleaseDate != "" || p.ReleasedAt != nil || p.IsNewRelease {
		t.Fatalf("legacy listing date leaked: %+v", p)
	}
	m.InfoSources[ModelInfoFieldReleaseDate] = ModelInfoSourceManual
	p.applyModelInfo(&m, "", "2026-09-28")
	if !p.IsNewRelease || p.ReleaseDate != m.ReleaseDate {
		t.Fatal("confirmed release hidden")
	}
}

func TestModelsDevLeaseRetryExpiryAndSuccess(t *testing.T) {
	previous := coreDB
	coreDB = withOptionMigrationDB(t)
	t.Cleanup(func() { coreDB = previous })
	now := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)
	claim := func(at time.Time, scheduled, want bool) {
		t.Helper()
		got, err := ClaimModelsDevSync(at, scheduled)
		if err != nil || got != want {
			t.Fatalf("claim got %v, %v; want %v", got, err, want)
		}
	}
	claim(now, true, true)
	claim(now.Add(time.Minute), false, false)
	if last, err := ModelsDevSyncLastRun(); err != nil || last != 0 {
		t.Fatalf("claim is not a success: %d %v", last, err)
	}
	if err := ReleaseModelsDevSync(now); err != nil {
		t.Fatal(err)
	}
	claim(now.Add(time.Minute), true, true)
	// A crashed run expires; an old owner cannot release the replacement.
	claim(now.Add(12*time.Minute), true, true)
	if err := ReleaseModelsDevSync(now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	claim(now.Add(13*time.Minute), false, false)
	if err := ReleaseModelsDevSync(now.Add(12 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := coreDB.Create(&Option{Key: ModelsDevSyncLastRunOptionKey, Value: strconv.FormatInt(now.Unix(), 10)}).Error; err != nil {
		t.Fatal(err)
	}
	claim(now.Add(time.Hour), true, false)
	claim(now.Add(time.Hour), false, true)
}

func TestRegistrationSourcesPreserveExplicitClears(t *testing.T) {
	sources := RegistrationInfoSources(Model{}, nil, []string{ModelInfoFieldReleaseDate, ModelInfoFieldMaker, "unknown"})
	if sources.Get(ModelInfoFieldReleaseDate) != ModelInfoSourceManual || sources.Get(ModelInfoFieldMaker) != ModelInfoSourceManual {
		t.Fatalf("manual clears lost: %+v", sources)
	}
	if _, ok := sources["unknown"]; ok {
		t.Fatal("untracked field recorded")
	}
}

func TestOptionHotReloadPicksUpMakerOrderWrittenByAnotherReplica(t *testing.T) {
	previousDB, previousOrder := coreDB, ModelMakerOrder()
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	coreDB = withOptionMigrationDB(t)
	t.Cleanup(func() {
		coreDB = previousDB
		_ = updateOptionMap(ModelMakerOrderOptionKey, EncodeModelMakerOrder(previousOrder))
	})
	applyModelMakerOrderOption(`["anthropic","openai"]`)
	// Write only to the shared database, without updating this process's map.
	if err := coreDB.Create(&Option{Key: ModelMakerOrderOptionKey, Value: `["openai","anthropic"]`}).Error; err != nil {
		t.Fatal(err)
	}
	if ModelMakerOrder()[0] != "anthropic" {
		t.Fatal("fixture did not preserve the stale replica state")
	}
	// This is the same loader called by each API's SyncOptions loop.
	loadOptionsFromDatabase()
	if ModelMakerOrder()[0] != "openai" || ModelMakerRank("openai") != 0 || ModelMakerRank("anthropic") != 1 {
		t.Fatal("periodic option reload did not apply the remote order")
	}
}

func TestRegistrationSourcesDistinguishUnknownAndExplicitFalse(t *testing.T) {
	fields := []string{ModelInfoFieldCapabilities, ModelInfoFieldOpenWeights}
	for _, field := range fields {
		if source := RegistrationInfoSources(Model{}, nil, nil).Get(field); source != "" {
			t.Fatalf("untouched %s should be unknown, got %s", field, source)
		}
		if source := RegistrationInfoSources(Model{}, nil, fields).Get(field); source != ModelInfoSourceManual {
			t.Fatalf("explicit false %s should be manual, got %s", field, source)
		}
		if source := RegistrationInfoSources(Model{}, fields, nil).Get(field); source != ModelInfoSourceModelsDev {
			t.Fatalf("prefilled false %s lost upstream source: %s", field, source)
		}
	}
}

func TestCatalogModelIDsFollowPublicMetadataRules(t *testing.T) {
	previous := coreDB
	coreDB = withOptionMigrationDB(t)
	t.Cleanup(func() { coreDB = previous })
	if err := coreDB.AutoMigrate(&Model{}); err != nil {
		t.Fatal(err)
	}
	rows := []Model{
		{ModelName: "gpt-6-astra", Status: 2, NameRule: NameRuleExact},
		{ModelName: "gpt-", Status: 1, NameRule: NameRulePrefix},
		{ModelName: "gpt-6-astra", Status: 1, NameRule: NameRuleExact},
	}
	for i := range rows {
		if err := coreDB.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	ids, err := CatalogModelIDs([]string{"gpt-6-astra", "gpt-other", "missing"})
	if err != nil || ids["gpt-6-astra"] != rows[2].Id || ids["gpt-other"] != rows[1].Id || ids["missing"] != 0 {
		t.Fatalf("wrong metadata owner: %+v %v", ids, err)
	}
}
