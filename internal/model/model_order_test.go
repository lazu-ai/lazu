package model

import (
	"reflect"
	"testing"

	"github.com/tidwall/gjson"
)

func TestResolveModelMakerReadsTheModelNameBeforeTheVendor(t *testing.T) {
	cases := []struct {
		explicit, name, vendor, want string
	}{
		{"", "claude-opus-5-5", "", "anthropic"},
		{"", "gpt-6-sol", "", "openai"},
		{"", "gemini-3.1-pro-preview", "Vivgrid", "google"},
		{"", "MiniMax-M2.5", "Cloudflare Workers AI", "minimax"},
		{"", "qwen3.8-max", "aliyun-bailian", "alibaba"},
		{"", "kimi-k3", "Moonshot AI (China)", "moonshot"},
		{"", "glm-5.3", "", "zhipu"},
		{"", "text-embedding-3-large", "", "openai"},
		// "gemini" ends in "mini" but is Google's; "o3" is OpenAI's only as a word.
		{"", "omni-embed", "", ""},
		{"", "some-model", "DeepSeek", "deepseek"},
		{"xai", "whatever", "", "xai"},
	}
	for _, tc := range cases {
		if got := ResolveModelMaker(tc.explicit, tc.name, tc.vendor); got != tc.want {
			t.Errorf("ResolveModelMaker(%q, %q, %q) = %q, want %q", tc.explicit, tc.name, tc.vendor, got, tc.want)
		}
	}
}

func TestCompareModelOrderFollowsMakerThenNewestRelease(t *testing.T) {
	applyModelMakerOrderOption("")
	defer applyModelMakerOrderOption("")
	rows := []ModelOrderKey{
		{Name: "MiniMax-M2.7", Maker: "minimax", ReleaseDate: "2026-03-18"},
		{Name: "claude-fable-5", Maker: "anthropic", ReleaseDate: "2026-06-07"},
		{Name: "gpt-6-luna", Maker: "openai", ReleaseDate: "2026-09-22"},
		{Name: "claude-opus-5-5", Maker: "anthropic", ReleaseDate: "2026-09-22"},
		{Name: "gpt-6-sol", Maker: "openai", ReleaseDate: "2026-09-22"},
		{Name: "unknown-model", Maker: "", ReleaseDate: ""},
		{Name: "claude-undated", Maker: "anthropic", ReleaseDate: ""},
		{Name: "gemini-3.5-flash-lite", Maker: "google", ReleaseDate: "2026-07-21"},
		{Name: "gemini-3.6-flash", Maker: "google", ReleaseDate: "2026-07-21"},
		{Name: "gpt-5.4-nano", Maker: "openai", ReleaseDate: "2026-03-17"},
		{Name: "gpt-5.4-mini-2026-03-17", Maker: "openai", ReleaseDate: "2026-03-17"},
		{Name: "gpt-5.4-mini", Maker: "openai", ReleaseDate: "2026-03-17"},
	}
	SortByModelOrder(rows, func(k ModelOrderKey) ModelOrderKey { return k })
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.Name)
	}
	want := []string{
		"claude-opus-5-5", "claude-fable-5", "claude-undated",
		// Same day: flagship (sol) before light (luna); mini before nano; a
		// dated snapshot follows its alias.
		"gpt-6-sol", "gpt-6-luna", "gpt-5.4-mini", "gpt-5.4-mini-2026-03-17", "gpt-5.4-nano",
		"gemini-3.6-flash", "gemini-3.5-flash-lite",
		"MiniMax-M2.7",
		"unknown-model",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestModelMakerOrderOptionReranksAndKeepsUnrankedLast(t *testing.T) {
	applyModelMakerOrderOption(`["deepseek","anthropic","not-a-maker","deepseek"]`)
	defer applyModelMakerOrderOption("")
	if got := ModelMakerOrder(); !reflect.DeepEqual(got, []string{"deepseek", "anthropic"}) {
		t.Fatalf("unexpected order %v", got)
	}
	if ModelMakerRank("deepseek") != 0 || ModelMakerRank("anthropic") != 1 {
		t.Fatalf("ranked makers out of place")
	}
	if ModelMakerRank("openai") != 2 || ModelMakerRank("") != 2 {
		t.Fatalf("unranked makers must share the rank past the end")
	}
	applyModelMakerOrderOption("{broken")
	if got := ModelMakerOrder(); got[0] != "anthropic" {
		t.Fatalf("an unreadable option must fall back to the shipped order, got %v", got)
	}
}

func TestModelIsNewReleaseUsesTwoWeeks(t *testing.T) {
	if !ModelIsNewRelease("2026-09-22", "2026-09-28") {
		t.Fatal("six days old should be new")
	}
	if ModelIsNewRelease("2026-09-01", "2026-09-28") {
		t.Fatal("four weeks old should not be new")
	}
	if ModelIsNewRelease("", "2026-09-28") || ModelIsNewRelease("2026-10-01", "2026-09-28") {
		t.Fatal("blank and future dates are not new")
	}
}

func TestModelSellPriceForInputTokensSwitchesWholeRequest(t *testing.T) {
	in, out, cache := 2.0, 10.0, 0.2
	tierIn, tierOut := 4.0, 15.0
	price := ModelSellPrice{
		InputPerMtok: &in, OutputPerMtok: &out, CacheReadPerMtok: &cache,
		ContextTierThreshold: 272_000, ContextTierInputPerMtok: &tierIn, ContextTierOutputPerMtok: &tierOut,
	}
	base, tiered := price.ForInputTokens(272_000)
	if tiered || *base.InputPerMtok != 2 {
		t.Fatal("at the threshold the base price applies")
	}
	long, tiered := price.ForInputTokens(272_001)
	if !tiered || *long.InputPerMtok != 4 || *long.OutputPerMtok != 15 {
		t.Fatalf("past the threshold the long-context price applies, got %+v", long)
	}
	if *long.CacheReadPerMtok != 0.2 {
		t.Fatal("a tier price left unset keeps the base price")
	}
	if _, tiered := (ModelSellPrice{InputPerMtok: &in}).ForInputTokens(1_000_000); tiered {
		t.Fatal("no threshold means a single price")
	}
}

func TestValidateModelSellPriceRequiresATierPrice(t *testing.T) {
	in := 2.0
	if err := validateModelSellPrice(ModelSellPrice{InputPerMtok: &in, ContextTierThreshold: 200_000}); err == nil {
		t.Fatal("a threshold without any long-context price must be rejected")
	}
	negative := -1.0
	if err := validateModelSellPrice(ModelSellPrice{InputPerMtok: &in, ContextTierThreshold: 200_000, ContextTierInputPerMtok: &negative}); err == nil {
		t.Fatal("negative long-context prices must be rejected")
	}
	tier := 4.0
	if err := validateModelSellPrice(ModelSellPrice{InputPerMtok: &in, ContextTierThreshold: 200_000, ContextTierInputPerMtok: &tier}); err != nil {
		t.Fatalf("valid tier rejected: %v", err)
	}
}

func TestCatalogEffortLevelsOverrideThePrefixTable(t *testing.T) {
	setCatalogReasoningOptions(map[string]ModelReasoningOptions{"gpt-6-sol": {Efforts: []string{"none", "low", "medium", "high", "xhigh", "max"}, Toggle: true}})
	defer setCatalogReasoningOptions(nil)
	if got := effortFamily("gpt-6-sol"); !reflect.DeepEqual(got, []string{"low", "medium", "high", "xhigh", "max"}) {
		t.Fatalf("declared levels should win and drop \"none\", got %v", got)
	}
	if got := SnapEffort("gpt-6-sol", "minimal"); got != "none" {
		t.Fatalf("gpt-6-sol has no minimal rung, expected none, got %q", got)
	}
}

func TestCatalogReasoningToggleControlsBelowMinimumCaps(t *testing.T) {
	defer setCatalogReasoningOptions(nil)
	for _, toggle := range []bool{true, false} {
		setCatalogReasoningOptions(map[string]ModelReasoningOptions{"new-lab-model": {Efforts: []string{"high", "max"}, Toggle: toggle}})
		if got := SnapEffort("vendor/new-lab-model", "low"); got != "none" {
			t.Fatalf("below-minimum cap snapped to %q", got)
		}
		if got := CanDisableReasoning("vendor/new-lab-model"); got != toggle {
			t.Fatalf("toggle capability ignored: %v != %v", got, toggle)
		}
		if toggle {
			for _, body := range []string{`{"model":"new-lab-model"}`, `{"model":"new-lab-model","reasoning_effort":"high"}`} {
				next, _, _ := ClampEffortBody([]byte(body), "new-lab-model", "low", "chat")
				if gjson.GetBytes(next, "reasoning_effort").String() != "none" {
					t.Fatalf("cap did not disable reasoning: %s", next)
				}
			}
		}
	}
	setCatalogReasoningOptions(map[string]ModelReasoningOptions{"none-option": {Efforts: []string{"none", "high"}}})
	if !CanDisableReasoning("none-option") {
		t.Fatal("explicit none effort must permit disabling")
	}
	setCatalogReasoningOptions(map[string]ModelReasoningOptions{"toggle-only": {Toggle: true}})
	if !CanDisableReasoning("toggle-only") {
		t.Fatal("toggle-only capability lost")
	}
	setCatalogReasoningOptions(nil)
	if CanDisableReasoning("toggle-only") || !CanDisableReasoning("claude-opus-5") {
		t.Fatal("cache replacement did not restore name fallback")
	}
}

func TestPricingRefreshLoadsReasoningToggle(t *testing.T) {
	db := withPricingTestDB(t, "pricing_reasoning_toggle")
	if err := db.AutoMigrate(&Channel{}, &Vendor{}, &ModelMetadata{}); err != nil {
		t.Fatal(err)
	}
	defer setCatalogReasoningOptions(nil)
	m := Model{ModelName: "new-lab-model", ReasoningOptions: ModelReasoningOptions{Efforts: []string{"high"}, Toggle: true}}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	RefreshPricing()
	if !CanDisableReasoning(m.ModelName) || SnapEffort(m.ModelName, "low") != "none" {
		t.Fatal("catalog options not loaded with pricing")
	}
	if err := db.Delete(&m).Error; err != nil {
		t.Fatal(err)
	}
	RefreshPricing()
	if CanDisableReasoning(m.ModelName) {
		t.Fatal("deleted catalog toggle still cached")
	}
}
