package model

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestClaudeReasoningSeedReadsEverySpelling(t *testing.T) {
	cases := []struct {
		name      string
		mode, off string
	}{
		{"claude-sonnet-4-20250514", ReasoningModeBudget, ReasoningOffDisabled},
		{"claude-3-7-sonnet-20250219", ReasoningModeBudget, ReasoningOffDisabled},
		{"claude-opus-4-5-20251101", ReasoningModeBudget, ReasoningOffDisabled},
		{"claude-opus-4-6", ReasoningModeAdaptive, ReasoningOffDisabled},
		{"claude-opus-4-7", ReasoningModeAdaptive, ReasoningOffDisabled},
		{"claude-opus-5", ReasoningModeAdaptive, ReasoningOffUpToHigh},
		{"claude-sonnet-5", ReasoningModeAdaptive, ReasoningOffDisabled},
		{"claude-haiku-5", ReasoningModeAdaptive, ReasoningOffDisabled},
		{"claude-haiku-5-5", ReasoningModeAdaptive, ReasoningOffUpToHigh},
		{"claude-sonnet-5-5", ReasoningModeAdaptive, ReasoningOffBetweenTools},
		{"claude-opus-5-5", ReasoningModeAdaptive, ReasoningOffNever},
		{"claude-fable-5-1", ReasoningModeAdaptive, ReasoningOffNever},
		{"anthropic.claude-sonnet-5-5-v1:0", ReasoningModeAdaptive, ReasoningOffBetweenTools},
		{"claude-sonnet-5.5@20261001", ReasoningModeAdaptive, ReasoningOffBetweenTools},
		{"claude-opus-6", ReasoningModeAdaptive, ""},
	}
	for _, tc := range cases {
		seed, ok := ClaudeReasoningSeed(tc.name)
		if !ok {
			t.Fatalf("%s: not read as a Claude", tc.name)
		}
		if seed.Mode != tc.mode || seed.Off != tc.off {
			t.Fatalf("%s: got mode=%q off=%q want mode=%q off=%q", tc.name, seed.Mode, seed.Off, tc.mode, tc.off)
		}
	}
	for _, name := range []string{"gpt-5.2", "deepseek-v4-pro", "gemini-3-pro", "claude"} {
		if _, ok := ClaudeReasoningSeed(name); ok {
			t.Fatalf("%s read as a Claude", name)
		}
	}
}

func TestSeedReasoningPolicyFillsOnlyBlanks(t *testing.T) {
	row := ModelReasoningOptions{Toggle: true, Efforts: []string{"low", "high"}, Off: ReasoningOffDisabled}
	got := SeedReasoningPolicy("claude-opus-5-5", row)
	if got.Mode != ReasoningModeAdaptive || got.Off != ReasoningOffDisabled || !got.Toggle || len(got.Efforts) != 2 {
		t.Fatalf("operator's Off replaced or toggle/efforts lost: %+v", got)
	}
	if got := SeedReasoningPolicy("gpt-5.2", ModelReasoningOptions{}); !got.IsZero() {
		t.Fatalf("non-Claude seeded: %+v", got)
	}
}

func TestClaudeThinkingOffFollowsPolicy(t *testing.T) {
	cases := []struct {
		off, requested, effort string
		wantType, wantEffort   string
	}{
		{"", "disabled", "max", "disabled", "max"},
		{ReasoningOffDisabled, "between_tools", "max", "disabled", "max"},
		{ReasoningOffUpToHigh, "disabled", "max", "disabled", "high"},
		{ReasoningOffUpToHigh, "disabled", "low", "disabled", "low"},
		{ReasoningOffBetweenTools, "disabled", "xhigh", "between_tools", "high"},
		{ReasoningOffNever, "disabled", "", "", "low"},
		{ReasoningOffNever, "between_tools", "high", "", "high"},
	}
	for _, tc := range cases {
		typ, effort := ClaudeThinkingOff(ModelReasoningOptions{Off: tc.off}, tc.requested, tc.effort)
		if typ != tc.wantType || effort != tc.wantEffort {
			t.Fatalf("off=%q %s/%s: got %q/%q want %q/%q", tc.off, tc.requested, tc.effort, typ, effort, tc.wantType, tc.wantEffort)
		}
	}
}

func withCatalogReasoning(t *testing.T, declared map[string]ModelReasoningOptions) {
	t.Helper()
	catalogEffortMu.RLock()
	before := catalogEffortByModel
	catalogEffortMu.RUnlock()
	setCatalogReasoningOptions(declared)
	t.Cleanup(func() {
		catalogEffortMu.Lock()
		catalogEffortByModel = before
		catalogEffortMu.Unlock()
	})
}

func TestCanDisableReasoningHonoursOff(t *testing.T) {
	withCatalogReasoning(t, map[string]ModelReasoningOptions{
		"claude-opus-5-5":   {Toggle: true, Efforts: []string{"low", "high"}, Mode: ReasoningModeAdaptive, Off: ReasoningOffNever},
		"claude-sonnet-5-5": {Efforts: []string{"low", "high"}, Mode: ReasoningModeAdaptive, Off: ReasoningOffBetweenTools},
		"claude-haiku-4-5":  {Toggle: true, Efforts: []string{"low", "high"}},
	})
	if CanDisableReasoning("claude-opus-5-5") {
		t.Fatal("Opus 5.5 cannot turn thinking off whatever models.dev's toggle says")
	}
	if !CanDisableReasoning("claude-sonnet-5-5") {
		t.Fatal("Sonnet 5.5 turns thinking off with between_tools")
	}
	if !CanDisableReasoning("claude-haiku-4-5") {
		t.Fatal("a row with no Off keeps its toggle")
	}
	if ReasoningPolicy("", "claude-opus-5-5").Off != ReasoningOffNever {
		t.Fatal("ReasoningPolicy skips empty names and reads the catalog")
	}
	if !ReasoningPolicy("not-listed", "also-not").IsZero() {
		t.Fatal("ReasoningPolicy is zero for models the catalog does not describe")
	}
}

func TestClampEffortBodyOffShapeFollowsPolicy(t *testing.T) {
	withCatalogReasoning(t, map[string]ModelReasoningOptions{
		"claude-sonnet-5-5": {Efforts: []string{"low", "medium", "high", "xhigh", "max"}, Mode: ReasoningModeAdaptive, Off: ReasoningOffBetweenTools},
		"claude-opus-5-5":   {Efforts: []string{"low", "medium", "high", "xhigh", "max"}, Mode: ReasoningModeAdaptive, Off: ReasoningOffNever},
		"claude-opus-4-5":   {Efforts: []string{"low", "medium", "high", "xhigh", "max"}, Mode: ReasoningModeBudget, Off: ReasoningOffDisabled},
	})
	body := []byte(`{"model":"claude-sonnet-5-5","thinking":{"type":"enabled","budget_tokens":8000}}`)
	out, _, applied := ClampEffortBody(body, "claude-sonnet-5-5", "minimal", "claude")
	if gjson.GetBytes(out, "thinking.type").String() != "between_tools" || gjson.GetBytes(out, "thinking.budget_tokens").Exists() || applied != "disabled" {
		t.Fatalf("Sonnet 5.5 off shape: %s applied=%q", out, applied)
	}
	body = []byte(`{"model":"claude-opus-5-5","thinking":{"type":"enabled","budget_tokens":8000}}`)
	out, _, _ = ClampEffortBody(body, "claude-opus-5-5", "minimal", "claude")
	if gjson.GetBytes(out, "thinking").Exists() || gjson.GetBytes(out, "output_config.effort").String() != "low" {
		t.Fatalf("Opus 5.5 off shape: %s", out)
	}
	body = []byte(`{"model":"claude-opus-4-5","thinking":{"type":"enabled","budget_tokens":8000}}`)
	out, _, _ = ClampEffortBody(body, "claude-opus-4-5", "minimal", "claude")
	if gjson.GetBytes(out, "thinking.type").String() != "disabled" {
		t.Fatalf("budget model off shape: %s", out)
	}
}

func TestBackfillClaudeReasoningPolicyFillsBlankRowsOnce(t *testing.T) {
	db := withPricingTestDB(t, "claude_reasoning_backfill")
	if err := db.AutoMigrate(&Model{}, &SchemaVersion{}); err != nil {
		t.Fatal(err)
	}
	rows := []Model{
		{ModelName: "claude-haiku-5-5"},
		{ModelName: "claude-opus-5-5", ReasoningOptions: ModelReasoningOptions{Toggle: true, Efforts: []string{"low", "high"}}},
		{ModelName: "claude-sonnet-5-5", ReasoningOptions: ModelReasoningOptions{Off: ReasoningOffDisabled}},
		{ModelName: "gpt-5.2", ReasoningOptions: ModelReasoningOptions{Efforts: []string{"low"}}},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := backfillClaudeReasoningPolicyOnce(db); err != nil {
		t.Fatal(err)
	}
	want := map[string]ModelReasoningOptions{
		"claude-haiku-5-5":  {Mode: ReasoningModeAdaptive, Off: ReasoningOffUpToHigh},
		"claude-opus-5-5":   {Toggle: true, Efforts: []string{"low", "high"}, Mode: ReasoningModeAdaptive, Off: ReasoningOffNever},
		"claude-sonnet-5-5": {Mode: ReasoningModeAdaptive, Off: ReasoningOffDisabled},
		"gpt-5.2":           {Efforts: []string{"low"}},
	}
	for name, expected := range want {
		var got Model
		if err := db.Where("model_name = ?", name).First(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got.ReasoningOptions.Mode != expected.Mode || got.ReasoningOptions.Off != expected.Off || got.ReasoningOptions.Toggle != expected.Toggle || len(got.ReasoningOptions.Efforts) != len(expected.Efforts) {
			t.Fatalf("%s: got %+v want %+v", name, got.ReasoningOptions, expected)
		}
	}
	// the marker keeps a later run from touching rows an operator blanked
	if err := db.Model(&Model{}).Where("model_name = ?", "claude-haiku-5-5").Update("reasoning_options", ModelReasoningOptions{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillClaudeReasoningPolicyOnce(db); err != nil {
		t.Fatal(err)
	}
	var again Model
	if err := db.Where("model_name = ?", "claude-haiku-5-5").First(&again).Error; err != nil {
		t.Fatal(err)
	}
	if again.ReasoningOptions.Mode != "" {
		t.Fatalf("second run must be a no-op, got %+v", again.ReasoningOptions)
	}
}
