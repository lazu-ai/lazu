package openai_chat

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// StreamDecoder turns a Chat Completions stream, chunk by chunk, into
// content events (textgen.Event). It keeps only what the wire needs: which
// tool indexes it has opened. Reply ids, output indexes and the like are the
// emitter's.
type StreamDecoder struct {
	started bool
	turn    bool
	tools   map[int]bool
}

// Decode is the events of one chunk, in the order the chunk said them.
// "[DONE]" is the caller's to recognise; a nil chunk decodes to nothing.
func (d *StreamDecoder) Decode(chunk *dto.ChatCompletionsStreamResponse) []textgen.Event {
	if d == nil || chunk == nil {
		return nil
	}
	var events []textgen.Event
	if !d.started {
		d.started = true
		events = append(events, textgen.Event{Kind: textgen.EventStart, ResponseID: chunk.Id, Model: chunk.Model, CreatedAt: chunk.Created})
	}
	if chunk.Usage != nil {
		events = append(events, textgen.Event{Kind: textgen.EventUsage, Usage: chunk.Usage})
	}
	for _, choice := range chunk.Choices {
		if !d.turn && strings.EqualFold(strings.TrimSpace(choice.Delta.Role), "assistant") {
			d.turn = true
			events = append(events, textgen.Event{Kind: textgen.EventTurn, Role: "assistant"})
		}
		if reasoning := choice.Delta.GetReasoningContent(); reasoning != "" {
			events = append(events, textgen.Event{Kind: textgen.EventReasoning, Text: reasoning})
		}
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			events = append(events, textgen.Event{Kind: textgen.EventText, Text: *choice.Delta.Content})
		}
		if choice.Delta.Refusal != nil && *choice.Delta.Refusal != "" {
			events = append(events, textgen.Event{Kind: textgen.EventRefusal, Text: *choice.Delta.Refusal})
		}
		for _, call := range choice.Delta.ToolCalls {
			index := 0
			if call.Index != nil {
				index = *call.Index
			}
			if d.tools == nil {
				d.tools = map[int]bool{}
			}
			if !d.tools[index] {
				d.tools[index] = true
				events = append(events, textgen.Event{Kind: textgen.EventToolStart, ToolIndex: index, ToolCallID: call.ID, ToolName: call.Function.Name})
			} else if call.ID != "" || call.Function.Name != "" {
				// a later fragment naming the call: carried on the next
				// arguments event, or alone when there are none
				if call.Function.Arguments == "" {
					events = append(events, textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: index, ToolCallID: call.ID, ToolName: call.Function.Name})
					continue
				}
			}
			if call.Function.Arguments != "" {
				events = append(events, textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: index, ToolCallID: call.ID, ToolName: call.Function.Name, Text: call.Function.Arguments})
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			events = append(events, textgen.Event{Kind: textgen.EventStop, Stop: StopReasonOf(*choice.FinishReason)})
		}
	}
	return events
}

// StopReasonOf is a Chat finish_reason in the IR's words.
func StopReasonOf(finishReason string) textgen.StopReason {
	switch strings.ToLower(strings.TrimSpace(finishReason)) {
	case "length":
		return textgen.StopLength
	case "tool_calls", "function_call":
		return textgen.StopTool
	case "content_filter":
		return textgen.StopFilter
	}
	return textgen.StopEnd
}

// FinishReasonOf is a stop reason in Chat's words.
func FinishReasonOf(stop textgen.StopReason) string {
	switch stop {
	case textgen.StopLength:
		return "length"
	case textgen.StopTool:
		return "tool_calls"
	case textgen.StopFilter:
		return "content_filter"
	}
	return "stop"
}
