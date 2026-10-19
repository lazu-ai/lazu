package model

import "strings"

const (
	ModelLifecycleCandidate = "candidate"
	ModelLifecycleActive    = "active"
	ModelLifecycleRetired   = "retired"

	ModelSourceManual     = "manual"
	ModelSourceBasellm    = "basellm"
	ModelSourceLiteLLM    = "litellm"
	ModelSourceOpenRouter = "openrouter"
)

type RetiredModelRule struct {
	Kind    int
	Pattern string
}

var retiredModelRules = []RetiredModelRule{
	{Kind: NameRuleExact, Pattern: "gpt-3.5"},
	{Kind: NameRuleExact, Pattern: "gpt-4"},
	{Kind: NameRulePrefix, Pattern: "gpt-3.5-"},
	{Kind: NameRulePrefix, Pattern: "gpt-4-"},
	{Kind: NameRulePrefix, Pattern: "gpt-4.5-preview"},
	{Kind: NameRuleExact, Pattern: "gpt-4.1-nano"},
	{Kind: NameRulePrefix, Pattern: "gpt-4.1-nano-"},
	{Kind: NameRuleExact, Pattern: "gpt-4o-2024-05-13"},
	{Kind: NameRuleExact, Pattern: "gpt-5-2025-08-07"},
	{Kind: NameRuleExact, Pattern: "gpt-5-mini-2025-08-07"},
	{Kind: NameRuleExact, Pattern: "gpt-5-nano-2025-08-07"},
	{Kind: NameRuleExact, Pattern: "gpt-5-pro-2025-10-06"},
	{Kind: NameRuleExact, Pattern: "gpt-5.2"},
	{Kind: NameRulePrefix, Pattern: "gpt-5.2-"},
	{Kind: NameRuleExact, Pattern: "gpt-5.3"},
	{Kind: NameRulePrefix, Pattern: "gpt-5.3-"},
	{Kind: NameRulePrefix, Pattern: "claude-3.5-"},
	{Kind: NameRulePrefix, Pattern: "claude-3.7-"},
	{Kind: NameRulePrefix, Pattern: "claude-3-"},
	{Kind: NameRulePrefix, Pattern: "claude-sonnet-4-5-20250929"},
	{Kind: NameRulePrefix, Pattern: "claude-opus-4-5-20251101"},
	{Kind: NameRulePrefix, Pattern: "gemini-1.5-"},
	{Kind: NameRulePrefix, Pattern: "gemini-2.0-"},
	{Kind: NameRuleExact, Pattern: "gemini-3-pro-preview"},
	{Kind: NameRuleExact, Pattern: "gemini-3.1-flash-lite-preview"},
	{Kind: NameRuleExact, Pattern: "gemini-exp-1206"},
	{Kind: NameRulePrefix, Pattern: "gpt-audio"},
	{Kind: NameRulePrefix, Pattern: "gpt-realtime"},
	{Kind: NameRulePrefix, Pattern: "computer-use-preview"},
	{Kind: NameRulePrefix, Pattern: "grok-2"},
	{Kind: NameRulePrefix, Pattern: "grok-beta"},
	{Kind: NameRulePrefix, Pattern: "grok-vision-beta"},
}

func NormalizeModelLifecycleStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch normalized {
	case ModelLifecycleCandidate:
		return ModelLifecycleCandidate
	case ModelLifecycleActive:
		return ModelLifecycleActive
	case ModelLifecycleRetired:
		return ModelLifecycleRetired
	case "":
		return ModelLifecycleActive
	default:
		return ModelLifecycleCandidate
	}
}

func IsPublicModelLifecycle(status string) bool {
	return NormalizeModelLifecycleStatus(status) == ModelLifecycleActive
}

func IsRetiredModelName(modelName string) bool {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if name == "" {
		return false
	}
	for _, rule := range retiredModelRules {
		pattern := strings.ToLower(rule.Pattern)
		switch rule.Kind {
		case NameRuleExact:
			if name == pattern {
				return true
			}
		case NameRulePrefix:
			if strings.HasPrefix(name, pattern) {
				return true
			}
		case NameRuleContains:
			if strings.Contains(name, pattern) {
				return true
			}
		case NameRuleSuffix:
			if strings.HasSuffix(name, pattern) {
				return true
			}
		}
	}
	return false
}
