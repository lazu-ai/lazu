package model

import (
	"testing"

	"github.com/tidwall/gjson"
)

func clampBody(body []byte, name, cap string) ([]byte, string, string) {
	return ClampEffortBody(body, name, cap, "")
}
func TestClampEffortBodyWireFormats(t *testing.T) {
	cases := []struct {
		name, model, body, path, want, requested, applied string
	}{
		{"chat", "gpt-5.2", `{"model":"gpt-5.2","reasoning_effort":"high"}`, "reasoning_effort", "medium", "high", "medium"},
		{"responses", "gpt-5.2", `{"model":"gpt-5.2","reasoning":{"effort":"xhigh","summary":"auto"}}`, "reasoning.effort", "medium", "xhigh", "medium"},
		{"claude", "claude-opus-5", `{"model":"claude-opus-5","output_config":{"effort":"max"}}`, "output_config.effort", "medium", "max", "medium"},
		{"gemini", "gemini-3-pro", `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, "generationConfig.thinkingConfig.thinkingLevel", "low", "high", "low"},
		{"deepseek has no medium", "deepseek-v4-pro", `{"model":"deepseek-v4-pro","reasoning_effort":"max"}`, "reasoning_effort", "low", "max", "low"},
		{"below cap untouched", "gpt-5.2", `{"model":"gpt-5.2","reasoning_effort":"low"}`, "reasoning_effort", "low", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, requested, applied := clampBody([]byte(tc.body), tc.model, "medium")
			if got := gjson.GetBytes(out, tc.path).String(); got != tc.want {
				t.Fatalf("%s = %q want %q (%s)", tc.path, got, tc.want, out)
			}
			if requested != tc.requested || applied != tc.applied {
				t.Fatalf("recorded %q→%q want %q→%q", requested, applied, tc.requested, tc.applied)
			}
		})
	}
}

func TestClampEffortBodyBudgets(t *testing.T) {
	out, _, applied := clampBody([]byte(`{"model":"claude-opus-4-5","max_tokens":64000,"thinking":{"type":"enabled","budget_tokens":32000}}`), "claude-opus-4-5", "low")
	if gjson.GetBytes(out, "thinking.budget_tokens").Int() != 4096 || applied != "low" {
		t.Fatalf("claude budget not capped: %s", out)
	}
	if gjson.GetBytes(out, "max_tokens").Int() != 64000 {
		t.Fatalf("unrelated field changed: %s", out)
	}
	out, _, _ = clampBody([]byte(`{"generationConfig":{"thinkingConfig":{"thinkingBudget":-1}}}`), "gemini-2.5-pro", "medium")
	if gjson.GetBytes(out, "generationConfig.thinkingConfig.thinkingBudget").Int() != 8192 {
		t.Fatalf("dynamic gemini budget not capped: %s", out)
	}
	out, _, applied = clampBody([]byte(`{"model":"glm-5","thinking":{"type":"enabled"}}`), "glm-5", "medium")
	if gjson.GetBytes(out, "thinking.type").String() != "disabled" || applied != "disabled" {
		t.Fatalf("on/off model not turned off: %s", out)
	}
	out, _, applied = clampBody([]byte(`{"model":"glm-5","thinking":{"type":"enabled"}}`), "glm-5", "high")
	if gjson.GetBytes(out, "thinking.type").String() != "enabled" || applied != "" {
		t.Fatalf("on/off model changed at high: %s", out)
	}
}

func TestPerMessageEffortCannotOverrideCap(t *testing.T) {
	body, _, applied := ClampEffortBody([]byte(`{"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hi"},{"role":"system","output_config":{"effort":"max"}},{"role":"system","output_config":{"effort":"low"}}]}`), "claude-opus-5.5", "medium", "claude")
	if gjson.GetBytes(body, "messages.1.output_config.effort").String() != "medium" || applied != "low" {
		t.Fatalf("per-message precedence: %s / %s", body, applied)
	}
}

func TestClaudeVersionSpellingsShareDefaultEffortPolicy(t *testing.T) {
	for _, name := range []string{"claude-opus-4.6", "claude-opus-4-6", "anthropic/claude-sonnet-4.6", "claude-opus-4.7", "claude-opus-4-7", "claude-opus-5.5"} {
		body, requested, applied := ClampEffortBody([]byte(`{"messages":[]}`), name, "low", "claude")
		if gjson.GetBytes(body, "output_config.effort").String() != "low" || requested != "default" || applied != "low" {
			t.Fatalf("%s: %s / %s -> %s", name, body, requested, applied)
		}
	}
	if got := SnapEffort("claude-opus-4.6", "xhigh"); got != "high" {
		t.Fatalf("dotted ladder differs: %s", got)
	}
}

func TestKnownGPTDefaultNeverIncreasesEffort(t *testing.T) {
	for _, format := range []string{"chat", "responses"} {
		for _, cap := range []string{"minimal", "low", "high", "xhigh"} {
			for _, name := range []string{"gpt-5.1", "gpt-5.2", "gpt-5.5", "openai/gpt-5.5"} {
				body, requested, applied := ClampEffortBody([]byte(`{"input":"hi"}`), name, cap, format)
				if string(body) != `{"input":"hi"}` || requested != "" || applied != "" {
					t.Fatalf("%s/%s/%s: %s (%s/%s)", name, format, cap, body, requested, applied)
				}
			}
		}
	}
	body, _, applied := ClampEffortBody([]byte(`{"reasoning_effort":"xhigh"}`), "gpt-5.5", "low", "chat")
	if gjson.GetBytes(body, "reasoning_effort").String() != "low" || applied != "low" {
		t.Fatalf("explicit effort escaped: %s", body)
	}
}

func TestEffortBudgetsUseSnappedVendorLevel(t *testing.T) {
	for _, tc := range []struct{ name, body, format, path, cap, level string }{
		{"gemini-2.5-pro", `{"generationConfig":{"thinkingConfig":{"thinkingBudget":-1}}}`, "gemini", "generationConfig.thinkingConfig.thinkingBudget", "xhigh", "high"},
		{"gemini-2.5-pro", `{}`, "gemini", "generationConfig.thinkingConfig.thinkingBudget", "xhigh", "high"},
		{"claude-opus-4.6", `{"thinking":{"type":"enabled","budget_tokens":32000}}`, "claude", "thinking.budget_tokens", "xhigh", "high"},
		{"deepseek-v4", `{"thinking_budget":10000}`, "chat", "thinking_budget", "medium", "low"},
	} {
		body, _, applied := ClampEffortBody([]byte(tc.body), tc.name, tc.cap, tc.format)
		if gjson.GetBytes(body, tc.path).Int() != int64(EffortBudgetTokens(tc.level)) || applied != tc.level {
			t.Fatalf("%s: %s (%s)", tc.name, body, applied)
		}
	}
}

func TestClaudeForceOffRemovesMessageEffort(t *testing.T) {
	body, _, applied := ClampEffortBody([]byte(`{"messages":[{"role":"user","content":"hi","output_config":{"effort":"max"}},{"role":"user","output_config":{"effort":"low"}}]}`), "claude-opus-4.6", "minimal", "claude")
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		if message.Get("output_config.effort").Exists() {
			t.Fatalf("override survived: %s", body)
		}
	}
	if gjson.GetBytes(body, "thinking.type").String() != "disabled" || applied != "disabled" {
		t.Fatalf("not disabled: %s (%s)", body, applied)
	}
}

func TestUncappedBodiesOnlyNormalizeAboveLadderEffort(t *testing.T) {
	for _, raw := range []string{`{}`, `{"reasoning_effort":"high"}`, `{"thinking_budget":100000}`, `{"thinking":{"type":"disabled"}}`} {
		body, requested, applied := ClampEffortBody([]byte(raw), "gpt-6-sol", "", "chat")
		if string(body) != raw || requested != "" || applied != "" {
			t.Fatalf("uncapped request changed: %s", body)
		}
	}
	body, requested, applied := ClampEffortBody([]byte(`{"reasoning":{"effort":"ultra"}}`), "gpt-6-sol", "", "responses")
	if gjson.GetBytes(body, "reasoning.effort").String() != "max" || requested != "ultra" || applied != "max" {
		t.Fatalf("ultra escaped: %s", body)
	}
}
