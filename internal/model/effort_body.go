package model

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var effortWordPaths = []string{
	"reasoning_effort", "reasoning.effort", "output_config.effort",
	"generationConfig.thinkingConfig.thinkingLevel",
	"generationConfig.thinkingConfig.thinking_level",
	"extra_body.google.thinking_config.thinking_level",
}
var effortBudgetPaths = []string{
	"thinking.budget_tokens", "generationConfig.thinkingConfig.thinkingBudget",
	"generationConfig.thinkingConfig.thinking_budget",
	"extra_body.google.thinking_config.thinking_budget", "thinking_budget", "extra_body.thinking_budget",
}

func effortModelBase(name string) string {
	name = strings.ToLower(name)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if strings.HasPrefix(name, "claude") {
		name = strings.ReplaceAll(name, ".", "-")
	}
	return name
}
func effortOnOffOnly(name string) bool {
	m := effortModelBase(name)
	return m == "glm-5" || strings.HasPrefix(m, "glm-5.1") || strings.HasPrefix(m, "glm-4") || strings.HasPrefix(m, "kimi-k2.6") || strings.HasPrefix(m, "kimi-k2-")
}

// ClampEffortBody constrains the final wire representation. format is the
// outgoing protocol (chat, responses, claude, gemini), not the inbound route.
// Empty format only clamps explicit fields; non-generation requests are skipped
// by the caller. Provider defaults are bounded only for recognized model families.
func ClampEffortBody(body []byte, modelName, cap, format string) ([]byte, string, string) {
	uncapped := EffortRank(cap) < 0
	if uncapped {
		// The public ladder still ends at max. Normalize above-ladder input
		// without introducing defaults or budgets into unrestricted requests.
		cap = "max"
	}
	requested, applied := "", ""
	note := func(r, a string) {
		if requested == "" {
			requested = r
		}
		applied = a
	}
	set := func(path string, value interface{}) { body, _ = sjson.SetBytes(body, path, value) }
	del := func(path string) { body, _ = sjson.DeleteBytes(body, path) }
	m := effortModelBase(modelName)
	// Per-message Claude effort overrides the top-level value. Clamp those too,
	// and let the last override determine the observable execution state.
	if format == "claude" {
		messages := gjson.GetBytes(body, "messages").Array()
		for i, message := range messages {
			value := message.Get("output_config.effort")
			if value.Type != gjson.String || uncapped && EffortRank(value.String()) <= EffortRank("max") {
				continue
			}
			next, changed := ClampEffort(modelName, value.String(), cap)
			if next == "none" {
				del(fmt.Sprintf("messages.%d.output_config.effort", i))
				note(value.String(), "disabled")
				continue
			}
			if changed {
				set(fmt.Sprintf("messages.%d.output_config.effort", i), next)
				note(value.String(), next)
			}
		}
	}
	level := SnapEffort(modelName, cap)
	// Honour explicit opt-outs before adding an omitted effort. On/off providers
	// and below-minimum caps use the provider's disabling representation.
	disabled := gjson.GetBytes(body, "thinking.type").String() == "disabled" ||
		gjson.GetBytes(body, "enable_thinking").Type == gjson.False || gjson.GetBytes(body, "extra_body.enable_thinking").Type == gjson.False
	forceOff := effortOnOffOnly(m) && EffortRank(cap) < EffortRank("high") || level == "none"
	if forceOff && (strings.HasPrefix(m, "claude") || strings.HasPrefix(m, "deepseek") || strings.HasPrefix(m, "kimi") || strings.HasPrefix(m, "moonshot") || strings.HasPrefix(m, "glm")) {
		before := gjson.GetBytes(body, "thinking.type").String()
		if !disabled {
			if before == "" {
				before = "default"
			}
			note(before, "disabled")
		}
		set("thinking.type", "disabled")
		del("thinking.budget_tokens")
		if strings.HasPrefix(m, "kimi") || strings.HasPrefix(m, "moonshot") || gjson.GetBytes(body, "enable_thinking").Exists() {
			set("enable_thinking", false)
		}
		if gjson.GetBytes(body, "extra_body.enable_thinking").Exists() {
			set("extra_body.enable_thinking", false)
		}
		for _, path := range effortWordPaths {
			del(path)
		}
		// Claude effort also controls non-thinking output. Low is valid with disabled
		// thinking, whereas a provider default of xhigh/max may reject that combination.
		// The off shape itself is the model's (ClaudeThinkingOff): Sonnet 5.5 takes
		// between_tools, Opus 5.5 takes no thinking field at all.
		if format == "claude" && strings.HasPrefix(m, "claude") {
			policy, _ := catalogReasoningOptions(modelName)
			thinkingType, effort := ClaudeThinkingOff(policy, "disabled", "low")
			if thinkingType == "" {
				del("thinking")
			} else {
				set("thinking.type", thinkingType)
			}
			set("output_config.effort", effort)
		}
		if applied != "" {
			applied = "disabled"
		}
		return body, requested, applied
	}
	hasWord, hasBudget := false, false
	for _, path := range effortWordPaths {
		value := gjson.GetBytes(body, path)
		if value.Type != gjson.String || uncapped && EffortRank(value.String()) <= EffortRank("max") {
			continue
		}
		hasWord = true
		next, changed := ClampEffort(modelName, value.String(), cap)
		if changed {
			set(path, next)
			note(value.String(), next)
		}
	}
	if uncapped {
		return body, requested, applied
	}
	if limit := EffortBudgetTokens(level); limit > 0 {
		for _, path := range effortBudgetPaths {
			value := gjson.GetBytes(body, path)
			if value.Type != gjson.Number {
				continue
			}
			hasBudget = true
			if value.Int() >= 0 && value.Int() <= int64(limit) {
				continue
			}
			set(path, limit)
			note(value.Raw+" tokens", level)
		}
	}
	// An explicit budget or opt-out must not accidentally turn reasoning on.
	if !hasWord && !hasBudget && !disabled {
		path := ""
		switch format {
		case "claude":
			if strings.HasPrefix(m, "claude") {
				if strings.Contains(m, "-4-6") || strings.Contains(m, "-4-7") || strings.Contains(m, "-5") {
					path = "output_config.effort"
				} else if gjson.GetBytes(body, "thinking.type").String() == "enabled" {
					set("thinking.budget_tokens", EffortBudgetTokens(level))
					note("default", level)
				}
			}
		case "gemini":
			if strings.HasPrefix(m, "gemini-2.5") {
				set("generationConfig.thinkingConfig.thinkingBudget", EffortBudgetTokens(level))
				note("default", level)
			} else if strings.HasPrefix(m, "gemini-3") {
				path = "generationConfig.thinkingConfig.thinkingLevel"
			}
		case "chat", "responses":
			if strings.HasPrefix(m, "qwen") {
				set("thinking_budget", EffortBudgetTokens(level))
				note("default", level)
			} else if !effortOnOffOnly(m) && (level == "none" && CanDisableReasoning(m) || strings.HasPrefix(m, "gpt-5") || strings.HasPrefix(m, "gpt-6") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") || strings.HasPrefix(m, "deepseek") || strings.HasPrefix(m, "kimi-k3") || strings.HasPrefix(m, "glm-5.3") || strings.HasPrefix(m, "grok") || strings.HasPrefix(m, "claude") || strings.HasPrefix(m, "gemini-3")) {
				path = "reasoning_effort"
				if format == "responses" {
					path = "reasoning.effort"
				}
			}
		}
		if path != "" {
			// Dotted GPT-5.x non-Codex/non-Pro requests default to no reasoning;
			// an upper bound must never turn it on. Other known defaults are
			// bounded rather than raised when the cap sits above them.
			defaultLevel := ""
			switch {
			case strings.HasPrefix(m, "gpt-5.") && !strings.Contains(m, "codex") && !strings.Contains(m, "-pro"):
				defaultLevel = "none"
			case m == "gpt-5" || strings.HasPrefix(m, "gpt-5-20"):
				defaultLevel = "medium"
			case strings.HasPrefix(m, "gemini-3.5-flash"):
				defaultLevel = "medium"
			case strings.HasPrefix(m, "gemini-3") || strings.HasPrefix(m, "grok"):
				defaultLevel = "high"
			}
			if defaultLevel != "" {
				next, changed := ClampEffort(modelName, defaultLevel, cap)
				if changed {
					set(path, next)
					note("default", next)
				}
			} else {
				set(path, level)
				note("default", level)
			}
		}
	}
	if applied != "" && format == "claude" {
		for _, message := range gjson.GetBytes(body, "messages").Array() {
			if value := message.Get("output_config.effort"); value.Type == gjson.String {
				applied = value.String()
			}
		}
	}
	return body, requested, applied
}
