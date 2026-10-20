package openai_responses

import (
	"encoding/json"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/tidwall/gjson"
)

// StreamDecoder turns a Responses stream into content events. It keeps
// which output indexes are function calls (their argument deltas) and
// reasoning items (whose done event may carry a seal).
type StreamDecoder struct {
	calls     map[int]bool
	reasoning map[int]string
	called    bool
	turn      bool
	// done-only calls not marked completed: held until the terminal event
	// says whether the reply was cut at its output cap (kept, a partial call
	// the caller may resume) or stopped for another reason (dropped)
	unfinished []textgen.Event
}

// Decode is the events of one SSE event's data. Shapes it does not know
// decode to nothing; an error event or a failed response ends the reply.
func (d *StreamDecoder) Decode(data string) ([]textgen.Event, error) {
	if d == nil {
		return nil, nil
	}
	if strings.TrimSpace(data) == "" || strings.TrimSpace(data) == "[DONE]" {
		return nil, nil
	}
	var ev dto.ResponsesStreamResponse
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil, err
	}
	if d.calls == nil {
		d.calls, d.reasoning = map[int]bool{}, map[int]string{}
	}
	index := 0
	if ev.OutputIndex != nil {
		index = *ev.OutputIndex
	}
	switch ev.Type {
	case "response.created":
		if ev.Response == nil {
			return nil, nil
		}
		return []textgen.Event{{Kind: textgen.EventStart, ResponseID: ev.Response.ID, Model: ev.Response.Model, CreatedAt: int64(ev.Response.CreatedAt)}}, nil
	case "response.output_item.added":
		if ev.Item == nil {
			return nil, nil
		}
		switch ev.Item.Type {
		case "function_call":
			d.calls[index], d.called = true, true
			return []textgen.Event{{Kind: textgen.EventToolStart, ToolIndex: index, ToolCallID: ev.Item.CallID, ToolName: ev.Item.Name, ItemID: ev.Item.ID}}, nil
		case "reasoning":
			d.reasoning[index] = ev.Item.ID
			return []textgen.Event{{Kind: textgen.EventReasoningStart, ItemID: ev.Item.ID}}, nil
		case "message":
			// the assistant's turn begins, so an empty message is still a
			// (empty) message
			if !d.turn {
				d.turn = true
				return []textgen.Event{{Kind: textgen.EventTurn, Role: "assistant"}}, nil
			}
		}
		return nil, nil
	case "response.output_text.delta":
		if ev.Delta != "" {
			return []textgen.Event{{Kind: textgen.EventText, Text: ev.Delta}}, nil
		}
	case "response.refusal.delta":
		if ev.Delta != "" {
			return []textgen.Event{{Kind: textgen.EventRefusal, Text: ev.Delta}}, nil
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if ev.Delta != "" {
			return []textgen.Event{{Kind: textgen.EventReasoning, Text: ev.Delta, ItemID: d.reasoning[index], Summary: ev.Type == "response.reasoning_summary_text.delta"}}, nil
		}
	case "response.function_call_arguments.delta":
		if ev.Delta != "" {
			return []textgen.Event{{Kind: textgen.EventToolArguments, ToolIndex: index, Text: ev.Delta}}, nil
		}
	case "response.output_item.done":
		if ev.Item == nil {
			return nil, nil
		}
		switch ev.Item.Type {
		case "function_call":
			done := textgen.Event{Kind: textgen.EventToolDone, ToolIndex: index, ToolCallID: ev.Item.CallID, ToolName: ev.Item.Name, Text: ev.Item.Arguments}
			if !d.calls[index] {
				// a call reported only by its done item (no added event, no
				// argument deltas) still opens first
				d.calls[index], d.called = true, true
				call := []textgen.Event{{Kind: textgen.EventToolStart, ToolIndex: index, ToolCallID: ev.Item.CallID, ToolName: ev.Item.Name, ItemID: ev.Item.ID}, done}
				if ev.Item.Status != "" && !strings.EqualFold(ev.Item.Status, "completed") {
					d.unfinished = append(d.unfinished, call...)
					return nil, nil
				}
				return call, nil
			}
			return []textgen.Event{done}, nil
		case "reasoning":
			// sealed: the item as the upstream wrote it, for a Responses
			// client and for its next request
			if raw := gjson.Get(data, "item"); raw.Exists() && raw.Get("encrypted_content").String() != "" {
				return []textgen.Event{{Kind: textgen.EventReasoningSealed, Sealed: json.RawMessage(raw.Raw), SealedBy: SealedBy, ItemID: ev.Item.ID}}, nil
			}
		}
		return nil, nil
	case "response.completed", "response.incomplete", "response.failed":
		if ev.Type == "response.failed" || (ev.Response != nil && ev.Response.Status == "failed") {
			// the provider's counters first: the error ends the reply, and
			// settlement reads the usage it saw
			var events []textgen.Event
			if ev.Response != nil && ev.Response.Usage != nil {
				events = append(events, textgen.Event{Kind: textgen.EventUsage, Usage: UsageOf(ev.Response.Usage)})
			}
			d.unfinished = nil
			return append(events, textgen.Event{Kind: textgen.EventError, Error: failedError(ev)}), nil
		}
		var events []textgen.Event
		stop := textgen.StopEnd
		unfinished := d.unfinished
		d.unfinished = nil
		if ev.Type == "response.incomplete" || (ev.Response != nil && ev.Response.Status == "incomplete") {
			stop = textgen.StopLength
			reason := ""
			if ev.Response != nil && ev.Response.IncompleteDetails != nil {
				reason = ev.Response.IncompleteDetails.Reason
			}
			if reason == "content_filter" {
				stop = textgen.StopFilter
			}
			if reason != "max_output_tokens" {
				// a filtered or otherwise stopped reply does not hand out
				// the calls it never finished
				unfinished = nil
			}
		}
		events = append(events, unfinished...)
		if stop == textgen.StopEnd && d.called {
			stop = textgen.StopTool
		} else if stop == textgen.StopEnd && ev.Response != nil {
			for _, item := range ev.Response.Output {
				if item.Type == "function_call" {
					stop = textgen.StopTool
				}
			}
		}
		events = append(events, textgen.Event{Kind: textgen.EventStop, Stop: stop})
		if ev.Response != nil && ev.Response.Usage != nil {
			events = append(events, textgen.Event{Kind: textgen.EventUsage, Usage: UsageOf(ev.Response.Usage)})
		}
		return events, nil
	case "error":
		return []textgen.Event{{Kind: textgen.EventError, Error: failedError(ev)}}, nil
	}
	return nil, nil
}

func failedError(ev dto.ResponsesStreamResponse) *textgen.SemanticError {
	semantic := &textgen.SemanticError{Type: "api_error", Code: "upstream_stream_failed", Message: "upstream stream failed"}
	if upstream := ev.GetOpenAIError(); upstream != nil {
		if upstream.Type != "" && upstream.Type != "upstream_error" {
			semantic.Type = upstream.Type
		}
		if upstream.Message != "" {
			semantic.Message = upstream.Message
		}
		if code := strings.TrimSpace(codeString(upstream.Code)); code != "" {
			semantic.Code = code
		}
		return semantic
	}
	if ev.Response != nil {
		if upstream := dto.GetOpenAIError(ev.Response.Error); upstream != nil {
			if upstream.Type != "" && upstream.Type != "upstream_error" {
				semantic.Type = upstream.Type
			}
			if upstream.Message != "" {
				semantic.Message = upstream.Message
			}
			if code := strings.TrimSpace(codeString(upstream.Code)); code != "" {
				semantic.Code = code
			}
		}
	}
	return semantic
}

func codeString(code any) string {
	switch v := code.(type) {
	case string:
		return v
	case nil:
		return ""
	}
	raw, _ := json.Marshal(code)
	return strings.Trim(string(raw), `"`)
}

// UsageOf is Responses' usage as the shared usage fact: input_tokens is the
// whole prompt, the cached part among it. Every dimension the provider
// reported is kept (modality and cache-write splits, image counts, cost),
// since prices differ by dimension.
func UsageOf(usage *dto.Usage) *dto.Usage {
	if usage == nil {
		return nil
	}
	out := *usage
	out.InputTokenSource, out.OutputTokenSource = "", ""
	if usage.InputTokens != 0 || usage.PromptTokens == 0 {
		out.PromptTokens = usage.InputTokens
	}
	if usage.OutputTokens != 0 || usage.CompletionTokens == 0 {
		out.CompletionTokens = usage.OutputTokens
	}
	if usage.InputTokensDetails != nil {
		details := *usage.InputTokensDetails
		if details.CachedCreationTokens == 0 {
			details.CachedCreationTokens = details.CacheWriteTokens
		}
		out.InputTokensDetails = &details
		out.PromptTokensDetails = details
	}
	if usage.OutputTokensDetails != nil {
		details := *usage.OutputTokensDetails
		out.OutputTokensDetails = &details
		out.CompletionTokenDetails = details
	}
	if out.PromptTokens > 0 {
		out.InputTokenSource = dto.UsageSourceProvider
	}
	if out.CompletionTokens > 0 {
		out.OutputTokenSource = dto.UsageSourceProvider
	}
	if out.TotalTokens < out.PromptTokens+out.CompletionTokens {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	return &out
}
