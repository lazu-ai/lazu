package model

import (
	"strings"
	"sync"
)

// The reasoning-effort ladder every vendor is mapped onto. A cap is one rung:
// that rung and everything below it are allowed. "none" (no reasoning) sits
// below the ladder and is never capped; anything above "max" — Codex's
// "ultra", for one — ranks as max+1 so a cap always lowers it.
var EffortLevels = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

const effortAboveLadder = 6

// EffortRank returns the rung of a level, -1 for an empty or unknown value
// that is not an effort word, and effortAboveLadder for words above max.
func EffortRank(level string) int {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "":
		return -1
	case "none", "off", "disabled":
		return -2
	case "ultra":
		return effortAboveLadder
	}
	for i, l := range EffortLevels {
		if l == level {
			return i
		}
	}
	return -1
}

// ValidEffortCap accepts an empty string (no cap) or one ladder rung.
func ValidEffortCap(cap string) bool {
	if cap == "" {
		return true
	}
	r := EffortRank(cap)
	return r >= 0 && r < len(EffortLevels)
}

// NormalizeEffortCap lowercases a cap and turns "max" into "" — a cap at the
// top of the ladder restricts nothing, so it is stored as no cap.
func NormalizeEffortCap(cap string) string {
	cap = strings.ToLower(strings.TrimSpace(cap))
	if cap == "max" {
		return ""
	}
	return cap
}

// EffortCapSource names which layer set the effective cap.
const (
	EffortCapSourceKey    = "key"
	EffortCapSourceMember = "member"
)

// EffectiveEffortCap is the lower of the Key's and the member's caps. The
// project default only seeds new members, so it is not a runtime layer.
func EffectiveEffortCap(keyCap, memberCap string) (string, string) {
	kr, mr := EffortRank(keyCap), EffortRank(memberCap)
	switch {
	case kr < 0 && mr < 0:
		return "", ""
	case kr < 0:
		return strings.ToLower(memberCap), EffortCapSourceMember
	case mr < 0:
		return strings.ToLower(keyCap), EffortCapSourceKey
	case kr <= mr:
		return strings.ToLower(keyCap), EffortCapSourceKey
	default:
		return strings.ToLower(memberCap), EffortCapSourceMember
	}
}

// effortFamily picks the vendor ladder a model name speaks. Every vendor uses
// a subset of the same words in the same order; only the subset differs.
func effortFamily(modelName string) []string {
	if levels, ok := catalogEffortLevels(modelName); ok {
		return levels
	}
	m := effortModelBase(modelName)
	switch {
	case strings.HasPrefix(m, "gpt-5.2-pro"):
		return []string{"medium", "high", "xhigh"}
	case strings.HasPrefix(m, "gpt-5."):
		return []string{"low", "medium", "high", "xhigh"}
	case strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return []string{"low", "medium", "high"}
	case strings.HasPrefix(m, "claude") && strings.Contains(m, "4-6"):
		return []string{"low", "medium", "high", "max"}
	case strings.HasPrefix(m, "claude"):
		return []string{"low", "medium", "high", "xhigh", "max"}
	case strings.HasPrefix(m, "gemini-3-pro"):
		return []string{"low", "high"}
	case strings.HasPrefix(m, "gemini-3.1-pro"):
		return []string{"low", "medium", "high"}
	case strings.HasPrefix(m, "gemini"):
		return []string{"minimal", "low", "medium", "high"}
	case strings.HasPrefix(m, "grok"):
		return []string{"low", "medium", "high", "xhigh"}
	case strings.HasPrefix(m, "deepseek"), strings.HasPrefix(m, "kimi"), strings.HasPrefix(m, "moonshot"), strings.HasPrefix(m, "glm"):
		return []string{"low", "high", "max"}
	default:
		return EffortLevels
	}
}

// SnapEffort returns the highest level the model's vendor supports at or
// below level; none when the vendor has nothing that low.
func SnapEffort(modelName, level string) string {
	want := EffortRank(level)
	family := effortFamily(modelName)
	best := ""
	for _, l := range family {
		if EffortRank(l) <= want {
			best = l
		}
	}
	if best == "" {
		return "none"
	}
	return best
}

// ClampEffort lowers requested to cap for the given model. It reports whether
// anything changed; a request at or below the cap, "none", and words that are
// not effort levels are left alone.
func ClampEffort(modelName, requested, cap string) (string, bool) {
	cr := EffortRank(cap)
	rr := EffortRank(requested)
	if cr < 0 || rr < 0 {
		return requested, false
	}
	level := requested
	if rr > cr {
		level = cap
	}
	applied := SnapEffort(modelName, level)
	return applied, applied != requested
}

// EffortBudgetTokens is the thinking-token budget a cap allows on vendors
// that take a number instead of a word (Claude budget_tokens, Gemini 2.5
// thinkingBudget, Qwen thinking_budget). 0 means no budget limit.
func EffortBudgetTokens(cap string) int {
	switch EffortRank(cap) {
	case 0:
		return 1024
	case 1:
		return 4096
	case 2:
		return 8192
	case 3:
		return 16384
	case 4:
		return 24576
	default:
		return 0
	}
}

// CanDisableReasoning covers the families with an explicit off representation.
// Reasoning-only models must reject caps below their first supported rung.
func CanDisableReasoning(name string) bool {
	if options, ok := catalogReasoningOptions(name); ok {
		// Off is lazu's own fact about the Messages API and outranks the
		// toggle models.dev lists: Opus 5.5 lists one and refuses it.
		if options.Off != "" {
			return options.Off != ReasoningOffNever
		}
		return options.Toggle
	}
	m := effortModelBase(name)
	if strings.HasPrefix(m, "gpt-") && (strings.Contains(m, "-pro") || strings.Contains(m, "codex")) {
		return false
	}
	return strings.HasPrefix(m, "claude") || strings.HasPrefix(m, "deepseek") || strings.HasPrefix(m, "kimi") || strings.HasPrefix(m, "moonshot") || strings.HasPrefix(m, "glm") || strings.HasPrefix(m, "gpt-5.") || strings.HasPrefix(m, "gpt-6") || strings.HasPrefix(m, "grok-4.3")
}

// Empty Key caps inherit the membership; explicit caps may only narrow it.
func KeyEffortCapAllowed(keyCap, memberCap string) bool {
	keyCap, memberCap = NormalizeEffortCap(keyCap), NormalizeEffortCap(memberCap)
	return ValidEffortCap(keyCap) && (keyCap == "" || memberCap == "" || EffortRank(keyCap) <= EffortRank(memberCap))
}

// Effort levels a model's catalog entry declares (from models.dev), keyed by
// effortModelBase. They win over the name-prefix table above, which cannot
// know a model released after it was written. Refreshed with the pricing
// cache; read on the relay path, so it is an in-memory map only.
var (
	catalogEffortMu      sync.RWMutex
	catalogEffortByModel = map[string]ModelReasoningOptions{}
)

func catalogEffortLevels(modelName string) ([]string, bool) {
	catalogEffortMu.RLock()
	defer catalogEffortMu.RUnlock()
	options, ok := catalogEffortByModel[effortModelBase(modelName)]
	return options.Efforts, ok && len(options.Efforts) > 0
}

func catalogReasoningOptions(name string) (ModelReasoningOptions, bool) {
	catalogEffortMu.RLock()
	defer catalogEffortMu.RUnlock()
	options, ok := catalogEffortByModel[effortModelBase(name)]
	return options, ok
}

// setCatalogReasoningOptions replaces the declared options, keeping only rungs of
// the shared ladder ("none" is below it and never capped).
func setCatalogReasoningOptions(declared map[string]ModelReasoningOptions) {
	next := make(map[string]ModelReasoningOptions, len(declared))
	for name, options := range declared {
		efforts := options.Efforts
		levels := make([]string, 0, len(efforts))
		for _, effort := range efforts {
			// Some catalogs express the off switch as an effort value.
			if EffortRank(effort) == -2 {
				options.Toggle = true
			}
			if rank := EffortRank(effort); rank >= 0 && rank < len(EffortLevels) {
				levels = append(levels, strings.ToLower(effort))
			}
		}
		if len(levels) > 0 || options.Toggle || options.Mode != "" || options.Off != "" {
			options.Efforts = levels
			next[effortModelBase(name)] = options
		}
	}
	catalogEffortMu.Lock()
	catalogEffortByModel = next
	catalogEffortMu.Unlock()
}
