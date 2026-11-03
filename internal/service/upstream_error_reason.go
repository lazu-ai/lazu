package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const maxUpstreamErrorBytes = 64 << 10

type upstreamErrorReason struct {
	Message string
	Field   string
	Feature string
}

// Read only known message envelopes; never serialize arbitrary upstream
// objects, details, or echoed request values back to a customer. Even a valid
// OpenAI error type is not evidence that its message is safe to disclose.
func safeUpstreamErrorReason(body []byte) upstreamErrorReason {
	var envelope struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return upstreamErrorReason{}
	}
	messages := []string{envelope.Message}
	var nested struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(envelope.Error, &nested) == nil {
		messages = append(messages, nested.Message, nested.Error.Message)
	}
	// Claude Code acts on three of Anthropic's messages by their words: it
	// compacts on "prompt is too long", and resends without a field or beta
	// the API names. Each is re-rendered from the numbers, field path or beta
	// names it carries, never passed through, so nothing else in the upstream
	// text reaches the client.
	for _, message := range messages {
		if reason, ok := contextLengthReason(message); ok {
			return reason
		}
		if match := extraInputPattern.FindStringSubmatch(message); match != nil {
			return upstreamErrorReason{match[1] + ": Extra inputs are not permitted", match[1], ""}
		}
	}
	for _, message := range messages {
		lower := strings.ToLower(message)
		rejected := strings.Contains(lower, "unexpected") || strings.Contains(lower, "not support") ||
			strings.Contains(lower, "unsupported") || strings.Contains(lower, "invalid") ||
			strings.Contains(lower, "not allowed") || strings.Contains(lower, "extraneous")
		if !rejected {
			continue
		}
		if strings.Contains(lower, "mid-conversation-system-2026-04-07") {
			return upstreamErrorReason{"Upstream rejected the mid-conversation-system-2026-04-07 beta; this channel does not accept that marker", "anthropic-beta", "mid-conversation-system-2026-04-07"}
		}
		if strings.Contains(lower, "beta") {
			if names := betaNames(message); len(names) > 0 {
				return upstreamErrorReason{"Unexpected value(s) `" + strings.Join(names, "`, `") + "` for the `anthropic-beta` header. Try again without them.", "anthropic-beta", strings.Join(names, ",")}
			}
			return upstreamErrorReason{"Upstream rejected a beta feature; check the selected channel's supported features", "anthropic-beta", ""}
		}
		if strings.Contains(lower, "function tools with reasoning_effort") {
			return upstreamErrorReason{"This model takes function tools on /v1/chat/completions only with reasoning_effort \"none\"; use /v1/responses to call tools while it reasons", "reasoning_effort", ""}
		}
		if strings.Contains(lower, "thinking") && strings.Contains(lower, "adaptive") {
			return upstreamErrorReason{"Upstream rejected the thinking configuration; check this model's adaptive thinking requirements", "thinking", ""}
		}
		for _, field := range []string{"max_tokens", "temperature", "output_config", "tool_choice", "messages", "tools"} {
			if strings.Contains(lower, field) {
				return upstreamErrorReason{"Upstream rejected the " + field + " configuration; check the selected channel's requirements", field, ""}
			}
		}
	}
	return upstreamErrorReason{}
}

var (
	// Anthropic: "prompt is too long: 205000 tokens > 200000 maximum".
	anthropicTooLongPattern = regexp.MustCompile(`(?i)prompt is too long:\s*(\d{1,9})\s*tokens\s*>\s*(\d{1,9})\s*maximum`)
	// OpenAI: "This model's maximum context length is 128000 tokens. However,
	// your messages resulted in 130000 tokens."
	openAITooLongPattern = regexp.MustCompile(`(?i)maximum context length is\s*(\d{1,9})\s*tokens.{0,80}?resulted in\s*(\d{1,9})\s*tokens`)
	// Anthropic (pydantic): "context_management: Extra inputs are not
	// permitted". Only a plain field path is kept.
	extraInputPattern = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_.\[\]]{0,127}):\s*Extra inputs are not permitted\.?\s*$`)
	// Beta names: "context-1m-2025-08-07", "claude-code-20250219".
	betaNamePattern = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*[-_](?:\d{4}-\d{2}-\d{2}|\d{8})\b`)
)

// contextLengthReason renders an upstream context-window refusal the way
// Anthropic words it, which is what Claude Code recognises.
func contextLengthReason(message string) (upstreamErrorReason, bool) {
	if match := anthropicTooLongPattern.FindStringSubmatch(message); match != nil {
		return upstreamErrorReason{Message: fmt.Sprintf("prompt is too long: %s tokens > %s maximum", match[1], match[2]), Field: "messages"}, true
	}
	if match := openAITooLongPattern.FindStringSubmatch(message); match != nil {
		return upstreamErrorReason{Message: fmt.Sprintf("prompt is too long: %s tokens > %s maximum", match[2], match[1]), Field: "messages"}, true
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "prompt is too long") || strings.Contains(lower, "input is too long") {
		return upstreamErrorReason{Message: "prompt is too long", Field: "messages"}, true
	}
	return upstreamErrorReason{}, false
}

// betaNames are the beta names a refusal names, at most five, in order.
func betaNames(message string) []string {
	var names []string
	for _, name := range betaNamePattern.FindAllString(strings.ToLower(message), -1) {
		if len(name) > 64 || slices.Contains(names, name) {
			continue
		}
		names = append(names, name)
		if len(names) == 5 {
			break
		}
	}
	return names
}

// ContextLengthMessage is a context-window refusal worded as Anthropic words
// it ("prompt is too long: N tokens > M maximum" when the upstream gave the
// numbers), the form Claude Code compacts on.
func ContextLengthMessage(upstream string) string {
	if reason, ok := contextLengthReason(upstream); ok {
		return reason.Message
	}
	return "prompt is too long"
}
