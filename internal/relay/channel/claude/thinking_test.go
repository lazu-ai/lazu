package claude

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
)

func withClaudeSettings(t *testing.T, fn func()) {
	t.Helper()
	settings := model_setting.GetClaudeSettings()
	oldEnabled := settings.ThinkingAdapterEnabled
	oldPercentage := settings.ThinkingAdapterBudgetTokensPercentage
	oldDefaults := make(map[string]int, len(settings.DefaultMaxTokens))
	for k, v := range settings.DefaultMaxTokens {
		oldDefaults[k] = v
	}
	defer func() {
		settings.ThinkingAdapterEnabled = oldEnabled
		settings.ThinkingAdapterBudgetTokensPercentage = oldPercentage
		settings.DefaultMaxTokens = oldDefaults
	}()
	fn()
}

func TestRequestOpenAI2ClaudeMessage_ReasoningPriorityAndBounds(t *testing.T) {
	withClaudeSettings(t, func() {
		settings := model_setting.GetClaudeSettings()
		settings.ThinkingAdapterEnabled = true
		settings.ThinkingAdapterBudgetTokensPercentage = 0.8
		settings.DefaultMaxTokens = map[string]int{
			"default": 8192,
		}

		reasoningRaw, _ := json.Marshal(map[string]any{
			"effort":     "low",
			"max_tokens": 900,
		})
		req := dto.GeneralOpenAIRequest{
			Model:           "claude-sonnet-4-20250514#thinking",
			MaxTokens:       1000,
			ReasoningEffort: "high",
			Reasoning:       reasoningRaw,
		}

		claudeReq, err := RequestOpenAI2ClaudeMessage(nil, req)
		if err != nil {
			t.Fatalf("RequestOpenAI2ClaudeMessage failed: %v", err)
		}
		if claudeReq.Model != "claude-sonnet-4-20250514" {
			t.Fatalf("expected trimmed model, got %q", claudeReq.Model)
		}
		if claudeReq.Thinking == nil || claudeReq.Thinking.BudgetTokens == nil {
			t.Fatalf("expected thinking enabled with budget tokens")
		}
		if *claudeReq.Thinking.BudgetTokens != 1024 {
			t.Fatalf("expected budget_tokens=1024, got %d", *claudeReq.Thinking.BudgetTokens)
		}
		if claudeReq.MaxTokens != 1280 {
			t.Fatalf("expected max_tokens=1280, got %d", claudeReq.MaxTokens)
		}
		if claudeReq.Temperature == nil || *claudeReq.Temperature != 1.0 {
			t.Fatalf("expected temperature=1.0, got %+v", claudeReq.Temperature)
		}
		if claudeReq.TopP != 0 {
			t.Fatalf("expected top_p=0, got %v", claudeReq.TopP)
		}
	})
}

func TestRequestOpenAI2ClaudeMessage_ReasoningMaxTokensCanRaiseMaxTokens(t *testing.T) {
	withClaudeSettings(t, func() {
		settings := model_setting.GetClaudeSettings()
		settings.ThinkingAdapterEnabled = true
		settings.ThinkingAdapterBudgetTokensPercentage = 0.8
		settings.DefaultMaxTokens = map[string]int{
			"default": 8192,
		}

		reasoningRaw, _ := json.Marshal(map[string]any{
			"max_tokens": 2000,
		})
		req := dto.GeneralOpenAIRequest{
			Model:     "claude-sonnet-4-20250514-thinking",
			MaxTokens: 1500,
			Reasoning: reasoningRaw,
		}

		claudeReq, err := RequestOpenAI2ClaudeMessage(nil, req)
		if err != nil {
			t.Fatalf("RequestOpenAI2ClaudeMessage failed: %v", err)
		}
		if claudeReq.Model != "claude-sonnet-4-20250514" {
			t.Fatalf("expected trimmed model, got %q", claudeReq.Model)
		}
		if claudeReq.Thinking == nil || claudeReq.Thinking.BudgetTokens == nil {
			t.Fatalf("expected thinking enabled with budget tokens")
		}
		if *claudeReq.Thinking.BudgetTokens != 2000 {
			t.Fatalf("expected budget_tokens=2000, got %d", *claudeReq.Thinking.BudgetTokens)
		}
		if claudeReq.MaxTokens != 2000 {
			t.Fatalf("expected max_tokens raised to 2000, got %d", claudeReq.MaxTokens)
		}
	})
}

func TestEnsureThinkingConstraints_FillsMissingBudget(t *testing.T) {
	withClaudeSettings(t, func() {
		settings := model_setting.GetClaudeSettings()
		settings.ThinkingAdapterBudgetTokensPercentage = 0.8

		req := &dto.ClaudeRequest{
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 0,
			Thinking: &dto.Thinking{
				Type: "enabled",
			},
		}
		EnsureThinkingConstraints(req)

		if req.Thinking == nil || req.Thinking.BudgetTokens == nil {
			t.Fatalf("expected budget tokens to be filled")
		}
		if *req.Thinking.BudgetTokens != 1024 {
			t.Fatalf("expected budget_tokens=1024, got %d", *req.Thinking.BudgetTokens)
		}
		if req.MaxTokens != 1280 {
			t.Fatalf("expected max_tokens=1280, got %d", req.MaxTokens)
		}
		if req.Temperature == nil || *req.Temperature != 1.0 {
			t.Fatalf("expected temperature=1.0, got %+v", req.Temperature)
		}
		if req.TopP != 0 {
			t.Fatalf("expected top_p=0, got %v", req.TopP)
		}
	})
}

func TestRequestOpenAI2ClaudeMessage_Opus47EffortSuffixUsesAdaptiveThinking(t *testing.T) {
	withClaudeSettings(t, func() {
		settings := model_setting.GetClaudeSettings()
		settings.ThinkingAdapterEnabled = true
		settings.DefaultMaxTokens = map[string]int{
			"default": 8192,
		}

		req := dto.GeneralOpenAIRequest{
			Model:     "claude-opus-4-7-xhigh",
			MaxTokens: 4096,
		}

		claudeReq, err := RequestOpenAI2ClaudeMessage(nil, req)
		if err != nil {
			t.Fatalf("RequestOpenAI2ClaudeMessage failed: %v", err)
		}
		if claudeReq.Model != "claude-opus-4-7" {
			t.Fatalf("expected trimmed model, got %q", claudeReq.Model)
		}
		if claudeReq.Thinking == nil || claudeReq.Thinking.Type != "adaptive" {
			t.Fatalf("expected adaptive thinking, got %+v", claudeReq.Thinking)
		}
		if claudeReq.Thinking.Display != "summarized" {
			t.Fatalf("expected summarized thinking display, got %q", claudeReq.Thinking.Display)
		}
		if string(claudeReq.OutputConfig) != `{"effort":"xhigh"}` {
			t.Fatalf("unexpected output_config: %s", string(claudeReq.OutputConfig))
		}
		if claudeReq.Temperature != nil {
			t.Fatalf("expected temperature to be omitted for opus 4.7")
		}
		if claudeReq.TopP != 0 || claudeReq.TopK != 0 {
			t.Fatalf("expected top_p/top_k to be cleared, got top_p=%v top_k=%d", claudeReq.TopP, claudeReq.TopK)
		}
	})
}

func TestRequestOpenAI2ClaudeMessage_Opus47ThinkingSuffixUsesAdaptiveHigh(t *testing.T) {
	withClaudeSettings(t, func() {
		settings := model_setting.GetClaudeSettings()
		settings.ThinkingAdapterEnabled = true
		settings.ThinkingAdapterBudgetTokensPercentage = 0.8
		settings.DefaultMaxTokens = map[string]int{
			"default": 8192,
		}

		req := dto.GeneralOpenAIRequest{
			Model: "claude-opus-4-7-thinking",
		}

		claudeReq, err := RequestOpenAI2ClaudeMessage(nil, req)
		if err != nil {
			t.Fatalf("RequestOpenAI2ClaudeMessage failed: %v", err)
		}
		if claudeReq.Model != "claude-opus-4-7" {
			t.Fatalf("expected trimmed model, got %q", claudeReq.Model)
		}
		if claudeReq.Thinking == nil || claudeReq.Thinking.Type != "adaptive" {
			t.Fatalf("expected adaptive thinking, got %+v", claudeReq.Thinking)
		}
		if string(claudeReq.OutputConfig) != `{"effort":"high"}` {
			t.Fatalf("unexpected output_config: %s", string(claudeReq.OutputConfig))
		}
		if claudeReq.Temperature != nil {
			t.Fatalf("expected temperature to be omitted for opus 4.7")
		}
	})
}
