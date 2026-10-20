// Package anthropic_messages is the codec for Anthropic's Messages API: its
// stream decoder (the vendor's SSE to content events) and emitter (content
// events to the SSE a Claude client reads).
package anthropic_messages

import (
	"encoding/json"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// SealedBy is the dialect name Anthropic's own reasoning is sealed under:
// a thinking block's signature and a redacted_thinking block go back to
// Anthropic alone.
const SealedBy = "anthropic"

// streamEvent is one Messages SSE event as the vendor writes it.
type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		ID    string           `json:"id"`
		Model string           `json:"model"`
		Usage *dto.ClaudeUsage `json:"usage"`
	} `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *dto.ClaudeUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	Input     json.RawMessage `json:"input"`
}

// StreamDecoder turns a Messages stream into content events. It keeps
// which block indexes are the vendor's own server tools (left out) and
// which tool_use starts carried their whole input (their deltas are then
// repeats).
type StreamDecoder struct {
	server map[int]bool
	whole  map[int]bool
	tools  map[int]bool
}

// Decode is the events of one SSE event (its data; the event name is
// redundant with data.type). Pings and shapes it does not know decode to
// nothing; an error event ends the reply.
func (d *StreamDecoder) Decode(data string) ([]textgen.Event, error) {
	if d == nil {
		return nil, nil
	}
	var ev streamEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil, err
	}
	if d.server == nil {
		d.server, d.whole, d.tools = map[int]bool{}, map[int]bool{}, map[int]bool{}
	}
	switch ev.Type {
	case "message_start":
		if ev.Message == nil {
			return nil, nil
		}
		events := []textgen.Event{{Kind: textgen.EventStart, ResponseID: ev.Message.ID, Model: ev.Message.Model}}
		if usage := UsageOf(ev.Message.Usage); usage != nil {
			events = append(events, textgen.Event{Kind: textgen.EventUsage, Usage: usage})
		}
		return events, nil
	case "content_block_start":
		var block contentBlock
		if err := json.Unmarshal(ev.ContentBlock, &block); err != nil {
			return nil, err
		}
		switch block.Type {
		case "text":
			if block.Text != "" {
				return []textgen.Event{{Kind: textgen.EventText, Text: block.Text}}, nil
			}
		case "thinking":
			events := []textgen.Event{{Kind: textgen.EventReasoningStart}}
			if block.Thinking != "" {
				events = append(events, textgen.Event{Kind: textgen.EventReasoning, Text: block.Thinking})
			}
			if block.Signature != "" {
				events = append(events, textgen.Event{Kind: textgen.EventReasoningSignature, Text: block.Signature, SealedBy: SealedBy})
			}
			return events, nil
		case "redacted_thinking":
			return []textgen.Event{{Kind: textgen.EventReasoningSealed, Sealed: append(json.RawMessage(nil), ev.ContentBlock...), SealedBy: SealedBy}}, nil
		case "tool_use":
			d.tools[ev.Index] = true
			events := []textgen.Event{{Kind: textgen.EventToolStart, ToolIndex: ev.Index, ToolCallID: block.ID, ToolName: block.Name}}
			if wholeInput(block.Input) {
				// a relay in front of another vendor's model may send the
				// whole input on the start, with no deltas after
				d.whole[ev.Index] = true
				events = append(events, textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: ev.Index, Text: string(block.Input)})
			}
			return events, nil
		case "server_tool_use", "web_search_tool_result":
			d.server[ev.Index] = true
		}
		return nil, nil
	case "content_block_delta":
		if ev.Delta == nil || d.server[ev.Index] {
			return nil, nil
		}
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text != "" {
				return []textgen.Event{{Kind: textgen.EventText, Text: ev.Delta.Text}}, nil
			}
		case "thinking_delta":
			if ev.Delta.Thinking != "" {
				return []textgen.Event{{Kind: textgen.EventReasoning, Text: ev.Delta.Thinking}}, nil
			}
		case "signature_delta":
			if ev.Delta.Signature != "" {
				return []textgen.Event{{Kind: textgen.EventReasoningSignature, Text: ev.Delta.Signature, SealedBy: SealedBy}}, nil
			}
		case "input_json_delta":
			if d.whole[ev.Index] || ev.Delta.PartialJSON == "" {
				return nil, nil
			}
			return []textgen.Event{{Kind: textgen.EventToolArguments, ToolIndex: ev.Index, Text: ev.Delta.PartialJSON}}, nil
		}
		return nil, nil
	case "content_block_stop":
		if d.tools[ev.Index] && !d.server[ev.Index] {
			return []textgen.Event{{Kind: textgen.EventToolDone, ToolIndex: ev.Index}}, nil
		}
		return nil, nil
	case "message_delta":
		var events []textgen.Event
		if ev.Delta != nil && ev.Delta.StopReason != "" {
			events = append(events, textgen.Event{Kind: textgen.EventStop, Stop: StopReasonOf(ev.Delta.StopReason)})
		}
		if usage := UsageOf(ev.Usage); usage != nil {
			events = append(events, textgen.Event{Kind: textgen.EventUsage, Usage: usage})
		}
		return events, nil
	case "error":
		message, errType := "upstream stream failed", "api_error"
		if ev.Error != nil {
			if ev.Error.Message != "" {
				message = ev.Error.Message
			}
			if ev.Error.Type != "" {
				errType = ev.Error.Type
			}
		}
		return []textgen.Event{{Kind: textgen.EventError, Error: &textgen.SemanticError{Type: errType, Code: "upstream_stream_failed", Message: message}}}, nil
	}
	return nil, nil
}

// wholeInput says whether a tool_use start's input is the call's own: an
// object with something in it.
func wholeInput(input json.RawMessage) bool {
	var m map[string]any
	return json.Unmarshal(input, &m) == nil && len(m) > 0
}

// UsageOf is Anthropic's usage as the shared usage fact: input_tokens
// leaves the cache buckets out, which the fact records as PromptExcludesCache.
func UsageOf(usage *dto.ClaudeUsage) *dto.Usage {
	if usage == nil {
		return nil
	}
	out := &dto.Usage{}
	if usage.InputTokens > 0 || usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		out.PromptTokens = usage.InputTokens
		out.PromptExcludesCache = true
		out.InputTokenSource = dto.UsageSourceProvider
		out.PromptTokensDetails.CachedTokens = usage.CacheReadInputTokens
		out.PromptTokensDetails.CachedCreationTokens = usage.CacheCreationInputTokens
		if usage.CacheCreation != nil {
			out.PromptTokensDetails.CachedCreation5mTokens = usage.CacheCreation.Ephemeral5mInputTokens
			out.PromptTokensDetails.CachedCreation1hTokens = usage.CacheCreation.Ephemeral1hInputTokens
		}
	}
	if usage.OutputTokens > 0 {
		out.CompletionTokens = usage.OutputTokens
		out.OutputTokenSource = dto.UsageSourceProvider
	}
	if thinking := usage.ThinkingTokens(); thinking > 0 {
		out.CompletionTokenDetails.ReasoningTokens = thinking
	}
	out.TotalTokens = out.PromptTokens + out.CompletionTokens
	return out
}

// StopReasonOf is an Anthropic stop_reason in the IR's words.
func StopReasonOf(reason string) textgen.StopReason {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "max_tokens":
		return textgen.StopLength
	case "tool_use":
		return textgen.StopTool
	case "refusal":
		return textgen.StopFilter
	}
	return textgen.StopEnd
}

// StopReasonTo is a stop reason in Anthropic's words.
func StopReasonTo(stop textgen.StopReason) string {
	switch stop {
	case textgen.StopLength:
		return "max_tokens"
	case textgen.StopTool:
		return "tool_use"
	case textgen.StopFilter:
		return "refusal"
	}
	return "end_turn"
}
