package model

import "testing"

func TestEffectiveEffortCap(t *testing.T) {
	cases := []struct{ key, member, want, source string }{
		{"", "", "", ""},
		{"medium", "", "medium", EffortCapSourceKey},
		{"", "high", "high", EffortCapSourceMember},
		{"low", "high", "low", EffortCapSourceKey},
		{"xhigh", "high", "high", EffortCapSourceMember},
		{"high", "high", "high", EffortCapSourceKey},
		{"bogus", "medium", "medium", EffortCapSourceMember},
	}
	for _, tc := range cases {
		got, source := EffectiveEffortCap(tc.key, tc.member)
		if got != tc.want || source != tc.source {
			t.Fatalf("EffectiveEffortCap(%q,%q) = %q,%q want %q,%q", tc.key, tc.member, got, source, tc.want, tc.source)
		}
	}
}

func TestClampEffortPerVendor(t *testing.T) {
	cases := []struct {
		model, requested, cap, want string
		changed                     bool
	}{
		{"gpt-5.2", "high", "medium", "medium", true},
		{"gpt-5.2", "low", "medium", "low", false},
		{"gpt-5.2", "none", "minimal", "none", false},
		{"gpt-6-sol", "ultra", "max", "max", true},
		{"claude-opus-5", "max", "medium", "medium", true},
		{"claude-opus-5", "high", "minimal", "none", true},
		{"gemini-3-pro", "high", "medium", "low", true},
		{"deepseek-v4-pro", "high", "medium", "low", true},
		{"deepseek-v4-pro", "max", "xhigh", "high", true},
		{"grok-4.7", "xhigh", "high", "high", true},
		{"glm-5.3", "max", "high", "high", true},
		{"openrouter/deepseek-v4", "max", "medium", "low", true},
		{"gpt-5.2", "weird", "low", "weird", false},
	}
	for _, tc := range cases {
		got, changed := ClampEffort(tc.model, tc.requested, tc.cap)
		if got != tc.want || changed != tc.changed {
			t.Fatalf("ClampEffort(%q,%q,%q) = %q,%v want %q,%v", tc.model, tc.requested, tc.cap, got, changed, tc.want, tc.changed)
		}
	}
}

func TestNormalizeEffortCap(t *testing.T) {
	if NormalizeEffortCap("MAX") != "" || NormalizeEffortCap(" High ") != "high" {
		t.Fatal("normalize")
	}
	if !ValidEffortCap("") || !ValidEffortCap("minimal") || ValidEffortCap("ultra") || ValidEffortCap("none") {
		t.Fatal("valid")
	}
}

func TestChatToolsNeedReasoningOffHonoursCatalogAndFallback(t *testing.T) {
	defer setCatalogReasoningOptions(nil)
	setCatalogReasoningOptions(nil)
	if !ChatToolsNeedReasoningOff("gpt-6-sol") || !ChatToolsNeedReasoningOff("gpt-6.1-terra") {
		t.Fatal("expected none-capable gpt-6 models to disable reasoning for Chat tools")
	}
	for _, reasoningOnly := range []string{"gpt-6-astra", "gpt-6-cyber", "gpt-6.1-sol", "gpt-6.2-sol", "gpt-6.2-astra"} {
		if ChatToolsNeedReasoningOff(reasoningOnly) || CanDisableReasoning(reasoningOnly) {
			t.Fatalf("%s starts at low and must not inject effort none", reasoningOnly)
		}
	}
	setCatalogReasoningOptions(map[string]ModelReasoningOptions{
		"gpt-6.3-terra": {Efforts: []string{"low", "medium", "high"}},
		"gpt-6.3-luna":  {Efforts: []string{"none", "low", "medium", "high"}},
	})
	if ChatToolsNeedReasoningOff("gpt-6.3-terra") {
		t.Fatal("catalog entry without none must not inject effort none")
	}
	if !ChatToolsNeedReasoningOff("gpt-6.3-luna") {
		t.Fatal("catalog entry with none must disable reasoning for Chat tools")
	}
}
