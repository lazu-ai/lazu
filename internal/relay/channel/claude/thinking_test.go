package claude

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
)

// bridgeChat is a Chat request through the hub: decoded, encoded for
// Messages, thinking fitted to the policy (encodeBridged).
func bridgeChat(req dto.GeneralOpenAIRequest, resolve PolicyResolver) (*dto.ClaudeRequest, error) {
	if len(req.Messages) == 0 {
		req.Messages = []dto.Message{{Role: "user", Content: "hi"}}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	semantic, _, err := openai_chat.Decode(body)
	if err != nil {
		return nil, err
	}
	return encodeBridged(semantic, nil, resolve)
}

// adaptivePolicy is a catalog row for a Claude from 4.6 on: adaptive
// thinking, disabled taken at any effort.
var adaptivePolicy = StaticPolicy(ModelPolicy{
	Reasoning: model.ModelReasoningOptions{Mode: model.ReasoningModeAdaptive, Off: model.ReasoningOffDisabled},
	Known:     true,
})

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

func TestBridgedThinking_ReasoningPriorityAndBounds(t *testing.T) {
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

		claudeReq, err := bridgeChat(req, nil)
		if err != nil {
			t.Fatal(err)
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

func TestBridgedThinking_ReasoningMaxTokensCanRaiseMaxTokens(t *testing.T) {
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

		claudeReq, err := bridgeChat(req, nil)
		if err != nil {
			t.Fatal(err)
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

func TestBridgedThinking_Opus47EffortSuffixUsesAdaptiveThinking(t *testing.T) {
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

		claudeReq, err := bridgeChat(req, adaptivePolicy)
		if err != nil {
			t.Fatal(err)
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

func TestBridgedThinking_Opus47ThinkingSuffixUsesAdaptiveHigh(t *testing.T) {
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

		claudeReq, err := bridgeChat(req, adaptivePolicy)
		if err != nil {
			t.Fatal(err)
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

// The Hermes case (2026-10-20): Haiku 5.5 with reasoning_effort was sent a
// legacy budget and refused. With the catalog saying adaptive it gets the
// effort, no budget and no sampling parameters.
func TestBridgedThinking_AdaptivePolicyTakesEffortAsIs(t *testing.T) {
	withClaudeSettings(t, func() {
		settings := model_setting.GetClaudeSettings()
		settings.ThinkingAdapterEnabled = true
		settings.DefaultMaxTokens = map[string]int{"default": 8192}
		temperature := 0.3
		req := dto.GeneralOpenAIRequest{Model: "claude-haiku-5-5", ReasoningEffort: "medium", Temperature: &temperature}
		claudeReq, err := bridgeChat(req, adaptivePolicy)
		if err != nil {
			t.Fatal(err)
		}
		if claudeReq.Thinking == nil || claudeReq.Thinking.Type != "adaptive" || claudeReq.Thinking.BudgetTokens != nil {
			t.Fatalf("expected adaptive thinking without a budget, got %+v", claudeReq.Thinking)
		}
		if string(claudeReq.OutputConfig) != `{"effort":"medium"}` {
			t.Fatalf("unexpected output_config: %s", claudeReq.OutputConfig)
		}
		if claudeReq.Temperature != nil || claudeReq.TopP != 0 {
			t.Fatalf("sampling parameters must be left out beside adaptive thinking: temp=%v top_p=%v", claudeReq.Temperature, claudeReq.TopP)
		}
	})
}

// A model the catalog does not describe is served exactly as before Mode
// existed: the effort becomes a budget.
func TestBridgedThinking_UnknownPolicyKeepsBudgetShape(t *testing.T) {
	withClaudeSettings(t, func() {
		settings := model_setting.GetClaudeSettings()
		settings.ThinkingAdapterEnabled = true
		settings.DefaultMaxTokens = map[string]int{"default": 8192}
		req := dto.GeneralOpenAIRequest{Model: "claude-haiku-5-5", ReasoningEffort: "medium"}
		claudeReq, err := bridgeChat(req, nil)
		if err != nil {
			t.Fatal(err)
		}
		if claudeReq.Thinking == nil || claudeReq.Thinking.Type != "enabled" || claudeReq.Thinking.GetBudgetTokens() != 4096 {
			t.Fatalf("expected enabled thinking with a 50%% budget, got %+v", claudeReq.Thinking)
		}
		if claudeReq.Model != "claude-haiku-5-5" {
			t.Fatalf("model changed: %q", claudeReq.Model)
		}
	})
}

// An effort suffix is taken off only when the catalog knows the base model.
func TestBridgedThinking_EffortSuffixNeedsAKnownBase(t *testing.T) {
	withClaudeSettings(t, func() {
		model_setting.GetClaudeSettings().DefaultMaxTokens = map[string]int{"default": 8192}
		req := dto.GeneralOpenAIRequest{Model: "some-vendor-model-low"}
		claudeReq, err := bridgeChat(req, nil)
		if err != nil {
			t.Fatal(err)
		}
		if claudeReq.Model != "some-vendor-model-low" || claudeReq.Thinking != nil {
			t.Fatalf("unknown base must keep its name: %q %+v", claudeReq.Model, claudeReq.Thinking)
		}
	})
}

func TestBridgedThinking_EffortNoneTurnsThinkingOffAsTheModelTakesIt(t *testing.T) {
	withClaudeSettings(t, func() {
		model_setting.GetClaudeSettings().DefaultMaxTokens = map[string]int{"default": 8192}
		betweenTools := StaticPolicy(ModelPolicy{Reasoning: model.ModelReasoningOptions{Mode: model.ReasoningModeAdaptive, Off: model.ReasoningOffBetweenTools}, Known: true})
		claudeReq, err := bridgeChat(dto.GeneralOpenAIRequest{Model: "claude-sonnet-5-5", ReasoningEffort: "none"}, betweenTools)
		if err != nil {
			t.Fatal(err)
		}
		if claudeReq.Thinking == nil || claudeReq.Thinking.Type != "between_tools" {
			t.Fatalf("Sonnet 5.5 takes between_tools, got %+v", claudeReq.Thinking)
		}
		never := StaticPolicy(ModelPolicy{Reasoning: model.ModelReasoningOptions{Mode: model.ReasoningModeAdaptive, Off: model.ReasoningOffNever}, Known: true})
		claudeReq, err = bridgeChat(dto.GeneralOpenAIRequest{Model: "claude-opus-5-5", ReasoningEffort: "none"}, never)
		if err != nil {
			t.Fatal(err)
		}
		if claudeReq.Thinking != nil || string(claudeReq.OutputConfig) != `{"effort":"low"}` {
			t.Fatalf("Opus 5.5 takes no thinking field at effort low, got %+v %s", claudeReq.Thinking, claudeReq.OutputConfig)
		}
	})
}

// A Claude client's own request (the native path) to an adaptive model.
func TestPrepareNativeThinking_BudgetBecomesAdaptiveEffort(t *testing.T) {
	withClaudeSettings(t, func() {
		model_setting.GetClaudeSettings().DefaultMaxTokens = map[string]int{"default": 8192}
		budget := 10000
		req := &dto.ClaudeRequest{Model: "claude-haiku-5-5", MaxTokens: 16000, Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: &budget}}
		PrepareNativeThinking(req, adaptivePolicy)
		if req.Thinking.Type != "adaptive" || req.Thinking.BudgetTokens != nil {
			t.Fatalf("expected adaptive, got %+v", req.Thinking)
		}
		if string(req.OutputConfig) != `{"effort":"high"}` {
			t.Fatalf("10000 tokens is nearest high, got %s", req.OutputConfig)
		}
		if req.Thinking.Display != "summarized" {
			t.Fatalf("thinking text must still be shown: %+v", req.Thinking)
		}
	})
}

func TestPrepareNativeThinking_OffShapeFollowsPolicy(t *testing.T) {
	withClaudeSettings(t, func() {
		model_setting.GetClaudeSettings().DefaultMaxTokens = map[string]int{"default": 8192}
		never := StaticPolicy(ModelPolicy{Reasoning: model.ModelReasoningOptions{Mode: model.ReasoningModeAdaptive, Off: model.ReasoningOffNever}, Known: true})
		req := &dto.ClaudeRequest{Model: "claude-opus-5-5", MaxTokens: 1000, Thinking: &dto.Thinking{Type: "disabled"}}
		PrepareNativeThinking(req, never)
		if req.Thinking != nil || string(req.OutputConfig) != `{"effort":"low"}` {
			t.Fatalf("Opus 5.5: thinking left out at effort low, got %+v %s", req.Thinking, req.OutputConfig)
		}
		upToHigh := StaticPolicy(ModelPolicy{Reasoning: model.ModelReasoningOptions{Mode: model.ReasoningModeAdaptive, Off: model.ReasoningOffUpToHigh}, Known: true})
		req = &dto.ClaudeRequest{Model: "claude-haiku-5-5", MaxTokens: 1000, Thinking: &dto.Thinking{Type: "disabled"}, OutputConfig: []byte(`{"effort":"max"}`)}
		PrepareNativeThinking(req, upToHigh)
		if req.Thinking == nil || req.Thinking.Type != "disabled" || string(req.OutputConfig) != `{"effort":"high"}` {
			t.Fatalf("Haiku 5.5: disabled at high, got %+v %s", req.Thinking, req.OutputConfig)
		}
		// unknown policy: as sent
		req = &dto.ClaudeRequest{Model: "claude-x", MaxTokens: 1000, Thinking: &dto.Thinking{Type: "disabled"}}
		PrepareNativeThinking(req, nil)
		if req.Thinking == nil || req.Thinking.Type != "disabled" || len(req.OutputConfig) != 0 {
			t.Fatalf("unknown policy must pass through, got %+v %s", req.Thinking, req.OutputConfig)
		}
	})
}

func TestPrepareNativeThinking_SuffixOnAdaptiveModel(t *testing.T) {
	withClaudeSettings(t, func() {
		settings := model_setting.GetClaudeSettings()
		settings.ThinkingAdapterEnabled = true
		settings.DefaultMaxTokens = map[string]int{"default": 8192}
		req := &dto.ClaudeRequest{Model: "claude-opus-4-7-xhigh"}
		PrepareNativeThinking(req, adaptivePolicy)
		if req.Model != "claude-opus-4-7" || req.Thinking == nil || req.Thinking.Type != "adaptive" || string(req.OutputConfig) != `{"effort":"xhigh"}` {
			t.Fatalf("got model=%q thinking=%+v output_config=%s", req.Model, req.Thinking, req.OutputConfig)
		}
	})
}

func TestFitThinkingBudget_KeepsBudgetUnderTheCatalogLimit(t *testing.T) {
	budget := 30000
	req := &dto.ClaudeRequest{Model: "claude-sonnet-4-5", MaxTokens: 64000, Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: &budget}}
	FitThinkingBudget(req, ModelPolicy{MaxOutputTokens: 16000, Known: true})
	if req.MaxTokens != 64000 || req.Thinking.GetBudgetTokens() != 16000-thinkingBudgetHeadroom {
		t.Fatalf("max_tokens is the client's; got max_tokens=%d budget=%d", req.MaxTokens, req.Thinking.GetBudgetTokens())
	}
	budget = 7500
	req = &dto.ClaudeRequest{Model: "claude-sonnet-4-5", MaxTokens: 8000, Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: &budget}}
	FitThinkingBudget(req, ModelPolicy{})
	if req.Thinking.GetBudgetTokens() != 7500 {
		t.Fatal("a policy naming no limit changes nothing")
	}
	budget = 2000
	req = &dto.ClaudeRequest{Model: "claude-sonnet-4-5", MaxTokens: 8000, Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: &budget}}
	FitThinkingBudget(req, ModelPolicy{MaxOutputTokens: 4000})
	if req.Thinking.GetBudgetTokens() != 2000 {
		t.Fatal("a budget that fits is left alone")
	}
}

// The catalog row is on the name the client sent; the resolver falls back
// to it for a mapped or suffixed upstream name.
func TestPolicyResolverFallsBackToTheOriginName(t *testing.T) {
	rows := map[string]model.ModelReasoningOptions{"claude-opus-4-7": {Mode: model.ReasoningModeAdaptive}}
	reasoning := func(names ...string) model.ModelReasoningOptions {
		for _, name := range names {
			if options, ok := rows[name]; ok {
				return options
			}
		}
		return model.ModelReasoningOptions{}
	}
	describe := func(name string) textgenruntime.ModelDescriptor {
		if name == "claude-opus-4-7" {
			return textgenruntime.ModelDescriptor{Found: true, MaxOutputTokens: 32000}
		}
		return textgenruntime.ModelDescriptor{}
	}
	resolve := policyResolver("claude-opus-4-7", reasoning, describe)
	if policy := resolve("vendor-opus-47-mapped"); !policy.Known || !policy.Adaptive() || policy.MaxOutputTokens != 32000 {
		t.Fatalf("origin fallback failed: %+v", policy)
	}
	if policy := policyResolver("", reasoning, describe)("unlisted"); policy.Known {
		t.Fatalf("unlisted model read as known: %+v", policy)
	}
	// a sparse row is still a listed model
	sparse := func(string) textgenruntime.ModelDescriptor { return textgenruntime.ModelDescriptor{Found: true} }
	if !policyResolver("", reasoning, sparse)("new-model").Known {
		t.Fatal("a row with no limits yet must still count as listed")
	}
}

// Behaviour change from main, on purpose: an effort suffix on a Claude name
// the catalog does not list is no longer trimmed by prefix.
func TestBridgedThinking_UnlistedClaudeKeepsItsSuffix(t *testing.T) {
	withClaudeSettings(t, func() {
		model_setting.GetClaudeSettings().DefaultMaxTokens = map[string]int{"default": 8192}
		claudeReq, err := bridgeChat(dto.GeneralOpenAIRequest{Model: "claude-opus-4-7-xhigh"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if claudeReq.Model != "claude-opus-4-7-xhigh" || claudeReq.Thinking != nil {
			t.Fatalf("got %q %+v", claudeReq.Model, claudeReq.Thinking)
		}
	})
}

// A Claude client's own adaptive request goes as written: no display
// injected, sampling parameters kept.
func TestPrepareNativeThinking_ClientAdaptiveRequestGoesAsWritten(t *testing.T) {
	withClaudeSettings(t, func() {
		model_setting.GetClaudeSettings().DefaultMaxTokens = map[string]int{"default": 8192}
		temperature := 0.7
		req := &dto.ClaudeRequest{Model: "claude-opus-4-7", MaxTokens: 4096, Temperature: &temperature, Thinking: &dto.Thinking{Type: "adaptive"}}
		PrepareNativeThinking(req, adaptivePolicy)
		if req.Thinking.Display != "" || req.Temperature == nil {
			t.Fatalf("client's adaptive request rewritten: %+v temp=%v", req.Thinking, req.Temperature)
		}
	})
}

// The suffix names the effort even when the client also sent a budget.
func TestPrepareNativeThinking_SuffixEffortWinsOverClientBudget(t *testing.T) {
	withClaudeSettings(t, func() {
		model_setting.GetClaudeSettings().DefaultMaxTokens = map[string]int{"default": 8192}
		budget := 2000
		req := &dto.ClaudeRequest{Model: "claude-opus-4-7-xhigh", MaxTokens: 16000, Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: &budget}}
		PrepareNativeThinking(req, adaptivePolicy)
		if req.Model != "claude-opus-4-7" || req.Thinking.Type != "adaptive" || string(req.OutputConfig) != `{"effort":"xhigh"}` {
			t.Fatalf("got model=%q thinking=%+v output_config=%s", req.Model, req.Thinking, req.OutputConfig)
		}
	})
}
