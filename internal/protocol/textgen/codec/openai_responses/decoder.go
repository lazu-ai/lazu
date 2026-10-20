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
	// calls are held, by output index in arrival order, until the terminal
	// event says how the reply ended: a call the provider did not mark
	// completed goes to the client only when the reply was cut at its
	// output cap (a partial call the caller may resume), and is withheld,
	// still billed, when a filter or another reason stopped it
	calls     map[int]*heldCall
	order     []int
	reasoning map[int]string
	called    bool
	turn      bool
}

type heldCall struct {
	events []textgen.Event
	status string
}

// callID is the item's call id, or its own id when the provider left the
// call id out, as the whole-response decoder does.
func callID(item *dto.ResponsesOutput) string {
	if item.CallID != "" {
		return item.CallID
	}
	return item.ID
}

// hold opens the call at index if it is new.
func (d *StreamDecoder) hold(index int, item *dto.ResponsesOutput) *heldCall {
	if call, ok := d.calls[index]; ok {
		return call
	}
	call := &heldCall{events: []textgen.Event{{Kind: textgen.EventToolStart, ToolIndex: index, ToolCallID: callID(item), ToolName: item.Name, ItemID: item.ID}}}
	d.calls[index] = call
	d.order = append(d.order, index)
	d.called = true
	return call
}

// release is the held calls' events, in order: each call kept, or withheld
// when keepUnfinished is false and the provider did not mark it completed.
func (d *StreamDecoder) release(keepUnfinished bool) []textgen.Event {
	var out []textgen.Event
	for _, index := range d.order {
		call := d.calls[index]
		withhold := !keepUnfinished && !strings.EqualFold(call.status, "completed")
		for _, event := range call.events {
			event.Withheld = withhold
			out = append(out, event)
		}
	}
	d.calls, d.order = map[int]*heldCall{}, nil
	return out
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
		d.calls, d.reasoning = map[int]*heldCall{}, map[int]string{}
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
			d.hold(index, ev.Item)
			return nil, nil
		case "reasoning":
			d.reasoning[index] = ev.Item.ID
			return []textgen.Event{{Kind: textgen.EventReasoningStart, ItemID: ev.Item.ID}}, nil
		case "message":
			// the assistant's turn begins, so an empty message is still a
			// (empty) message
			if !d.turn {
				d.turn = true
				// the item's id marks the message as the upstream's own,
				// kept even beside tool calls
				return []textgen.Event{{Kind: textgen.EventTurn, Role: "assistant", ItemID: ev.Item.ID}}, nil
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
		if call, ok := d.calls[index]; ok && ev.Delta != "" {
			call.events = append(call.events, textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: index, Text: ev.Delta})
		}
	case "response.output_item.done":
		if ev.Item == nil {
			return nil, nil
		}
		switch ev.Item.Type {
		case "function_call":
			call := d.hold(index, ev.Item)
			call.events = append(call.events, textgen.Event{Kind: textgen.EventToolDone, ToolIndex: index, ToolCallID: callID(ev.Item), ToolName: ev.Item.Name, Text: ev.Item.Arguments})
			call.status = ev.Item.Status
			return nil, nil
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
			events = append(d.release(false), events...)
			return append(events, textgen.Event{Kind: textgen.EventError, Error: failedError(ev)}), nil
		}
		var events []textgen.Event
		stop := textgen.StopEnd
		keepUnfinished := true
		if ev.Type == "response.incomplete" || (ev.Response != nil && ev.Response.Status == "incomplete") {
			stop = textgen.StopLength
			reason := ""
			if ev.Response != nil && ev.Response.IncompleteDetails != nil {
				reason = ev.Response.IncompleteDetails.Reason
			}
			if reason == "content_filter" {
				stop = textgen.StopFilter
			}
			// a filtered or otherwise stopped reply does not hand out the
			// calls it never finished
			keepUnfinished = reason == "max_output_tokens"
		}
		events = append(events, d.release(keepUnfinished)...)
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
		return append(d.release(false), textgen.Event{Kind: textgen.EventError, Error: failedError(ev)}), nil
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
