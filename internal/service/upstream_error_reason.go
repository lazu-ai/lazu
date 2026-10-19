package service

import (
	"encoding/json"
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
