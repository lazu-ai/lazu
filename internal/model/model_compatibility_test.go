package model

import (
	"reflect"
	"testing"
	"time"
)

func TestListedModelNamesKeepsOldOnlyScopes(t *testing.T) {
	for _, tt := range []struct{ names, want []string }{
		{[]string{"deepseek-v4-flash", "deepseek-flash", "deepseek-v4-pro", "deepseek-v4-flash-vision-exp"}, []string{"deepseek-flash", "deepseek-v4-pro"}},
		{[]string{"deepseek-v4-flash"}, []string{"deepseek-v4-flash"}},
		{[]string{"deepseek-v4-flash-vision-exp", "unrelated"}, []string{"deepseek-v4-flash-vision-exp", "unrelated"}},
	} {
		if got := ListedModelNames(tt.names); !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("got %v want %v", got, tt.want)
		}
	}
}
func TestHiddenAliasesKeepRoutingAndPrice(t *testing.T) {
	db := withPricingTestDB(t, "deepseek_alias_price")
	if err := db.AutoMigrate(&Model{}, &Channel{}, &Ability{}, &Vendor{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Channel{Id: 54, Name: "test", Type: 1, Status: 1, Models: "deepseek-flash,deepseek-v4-flash,deepseek-v4-flash-vision-exp", Group: ChannelLaneDirect}).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp"} {
		if err := db.Create(&Model{ModelName: name, Status: 1}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&Ability{Model: name, Group: ChannelLaneDirect, ChannelId: 54, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		input, output := 0.1875, 0.75
		if err := db.Create(&ModelSellPrice{ModelName: name, ChannelGroup: ChannelLaneDirect, Active: true, InputPerMtok: &input, OutputPerMtok: &output, PricingVersion: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	RefreshPricing()
	t.Cleanup(func() { lastGetPricingTime = time.Time{} })
	for _, name := range []string{"deepseek-v4-flash", "deepseek-v4-flash-vision-exp"} {
		if !IsModelRequestable(name) {
			t.Fatalf("hidden alias %s is no longer requestable", name)
		}
		if _, ok := GetActiveModelSellPrice(name, ChannelLaneDirect); !ok {
			t.Fatalf("hidden alias %s lost pricing", name)
		}
	}
	if got := ListedPricing(GetPricing()); len(got) != 1 || got[0].ModelName != "deepseek-flash" {
		t.Fatalf("unexpected listing: %+v", got)
	}
}

func withCatalogReplacements(t *testing.T, targets map[string]string) {
	t.Helper()
	// Without a database the reader does not refresh the catalog, so the
	// installed map is what it sees.
	previousDB := coreDB
	coreDB = nil
	catalogReplacementsMu.RLock()
	previous := catalogReplacements
	catalogReplacementsMu.RUnlock()
	setCatalogReplacements(targets)
	t.Cleanup(func() {
		setCatalogReplacements(previous)
		coreDB = previousDB
	})
}

func TestCompatibilityModelTargetReadsCatalogReplacements(t *testing.T) {
	withCatalogReplacements(t, map[string]string{"old-model": "new-model", "deepseek-v4-flash": "catalog-flash"})
	for name, want := range map[string]string{
		"old-model":                    "new-model",
		"new-model":                    "",
		"unrelated":                    "",
		"deepseek-v4-flash":            "catalog-flash", // the catalog wins over the fixed pair
		"deepseek-v4-flash-vision-exp": "deepseek-flash",
	} {
		if got := CompatibilityModelTarget(name); got != want {
			t.Fatalf("CompatibilityModelTarget(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestListedModelNamesHidesCatalogAliasOnlyBesideItsReplacement(t *testing.T) {
	withCatalogReplacements(t, map[string]string{"old-model": "new-model", "old-other": "new-other"})
	for _, tt := range []struct{ names, want []string }{
		{[]string{"old-model", "new-model", "old-other"}, []string{"new-model", "old-other"}},
		{[]string{"old-model"}, []string{"old-model"}},
		{[]string{"deepseek-v4-flash", "deepseek-flash", "old-model"}, []string{"deepseek-flash", "old-model"}},
	} {
		if got := ListedModelNames(tt.names); !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("ListedModelNames(%v) = %v want %v", tt.names, got, tt.want)
		}
	}
}

func TestCatalogReplacementTargetsTakeOnlyListableExactRows(t *testing.T) {
	exact := func(name, replacement string) *Model {
		return &Model{ModelName: name, Status: 1, LifecycleStatus: ModelLifecycleActive, ReplacementModel: replacement}
	}
	family := exact("fam-", "new-model")
	family.NameRule = NameRulePrefix
	got := catalogReplacementTargets(map[string]*Model{
		"old-model":   exact("old-model", " new-model "),
		"new-model":   exact("new-model", ""),
		"fam-1":       family,                     // a pattern row retires no family
		"orphan":      exact("orphan", "missing"), // replacement is not in the catalog
		"self":        exact("self", "self"),
		"loop-a":      exact("loop-a", "loop-b"),
		"loop-b":      exact("loop-b", "loop-a"),
		"chain-start": exact("chain-start", "old-model"),
	})
	want := map[string]string{"old-model": "new-model", "chain-start": "old-model"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalogReplacementTargets = %v want %v", got, want)
	}
}

func TestCatalogReplacementHidesAliasAndKeepsItRequestable(t *testing.T) {
	db := withPricingTestDB(t, "catalog_replacement_alias")
	if err := db.AutoMigrate(&Model{}, &Channel{}, &Ability{}, &Vendor{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { setCatalogReplacements(nil) })
	names := []string{"new-model", "old-model", "paused-model", "trial-model"}
	if err := db.Create(&Channel{Id: 61, Name: "test", Type: 1, Status: 1, Models: "new-model,old-model,paused-model,trial-model", Group: ChannelLaneDirect}).Error; err != nil {
		t.Fatal(err)
	}
	rows := []Model{
		{ModelName: "new-model", Status: 1},
		{ModelName: "old-model", Status: 1, ReplacementModel: "new-model"},
		{ModelName: "paused-model", Status: 0, ReplacementModel: "new-model"},
		{ModelName: "trial-model", Status: 1, LifecycleStatus: ModelLifecycleCandidate, ReplacementModel: "new-model"},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Status has a column default of 1, so a zero on Create is not stored.
	if err := db.Model(&Model{}).Where("model_name = ?", "paused-model").Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := db.Create(&Ability{Model: name, Group: ChannelLaneDirect, ChannelId: 61, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		input, output := 0.5, 1.0
		if err := db.Create(&ModelSellPrice{ModelName: name, ChannelGroup: ChannelLaneDirect, Active: true, InputPerMtok: &input, OutputPerMtok: &output, PricingVersion: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	RefreshPricing()
	t.Cleanup(func() { lastGetPricingTime = time.Time{} })
	if got := CompatibilityModelTarget("old-model"); got != "new-model" {
		t.Fatalf("old-model target = %q", got)
	}
	for _, name := range []string{"paused-model", "trial-model"} {
		if got := CompatibilityModelTarget(name); got != "" {
			t.Fatalf("%s is not active but has target %q", name, got)
		}
	}
	if !IsModelRequestable("old-model") {
		t.Fatal("alias is no longer requestable")
	}
	if _, ok := GetActiveModelSellPrice("old-model", ChannelLaneDirect); !ok {
		t.Fatal("alias lost pricing")
	}
	var listed []string
	for _, row := range ListedPricing(GetPricing()) {
		listed = append(listed, row.ModelName)
	}
	if !reflect.DeepEqual(listed, []string{"new-model"}) {
		t.Fatalf("unexpected listing: %v", listed)
	}
}
