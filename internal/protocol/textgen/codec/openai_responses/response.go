package openai_responses

import (
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// Render writes a whole Responses object directly from response events. It
// shares the stream emitter's item ordering and envelope rules instead of
// flattening the reply through another protocol's DTO.
func Render(events []textgen.Event, responseID, model string, request *textgen.Request, usage *dto.Usage) (map[string]any, error) {
	// A whole reply is already materialized in memory. Give the reusable
	// stream state machine room for exactly the material at hand instead of
	// imposing its live-stream retention cap on this path.
	retainedLimit := 1
	for _, event := range events {
		retainedLimit += len(event.Text) + len(event.Sealed)
	}
	emitter := NewStreamEmitter(responseID, model, request, retainedLimit)
	if _, err := emitter.Emit(events...); err != nil {
		return nil, err
	}
	terminal, err := emitter.Finish(usage)
	if err != nil {
		return nil, err
	}
	if len(terminal) == 0 {
		return nil, fmt.Errorf("Responses renderer produced no terminal event")
	}
	response, ok := terminal[len(terminal)-1]["response"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Responses renderer produced no response object")
	}
	return response, nil
}

// DecodeResponse is a whole Responses reply as content events, in output
// order: start, turn, text and refusals, function calls, stop, usage. A
// failed reply is its error.
func DecodeResponse(response *dto.OpenAIResponsesResponse) []textgen.Event {
	if response == nil {
		return nil
	}
	events := []textgen.Event{{Kind: textgen.EventStart, ResponseID: response.ID, Model: response.Model, CreatedAt: int64(response.CreatedAt)}, {Kind: textgen.EventTurn, Role: "assistant"}}
	if strings.EqualFold(response.Status, "failed") {
		if response.Usage != nil {
			events = append(events, textgen.Event{Kind: textgen.EventUsage, Usage: UsageOf(response.Usage)})
		}
		return append(events, textgen.Event{Kind: textgen.EventError, Error: failedError(dto.ResponsesStreamResponse{Type: "response.failed", Response: response})})
	}
	stop := textgen.StopEnd
	tool := 0
	for _, output := range response.Output {
		switch output.Type {
		case dto.ResponsesOutputTypeFunctionCall:
			callID := output.CallID
			if callID == "" {
				callID = output.ID
			}
			events = append(events,
				textgen.Event{Kind: textgen.EventToolStart, ToolIndex: tool, ToolCallID: callID, ToolName: output.Name},
				textgen.Event{Kind: textgen.EventToolDone, ToolIndex: tool, ToolCallID: callID, ToolName: output.Name, Text: output.Arguments},
			)
			tool++
			stop = textgen.StopTool
		case "reasoning":
			var text strings.Builder
			for _, content := range output.Content {
				if content.Type == "reasoning_text" || content.Type == "summary_text" {
					text.WriteString(content.Text)
				}
			}
			if text.Len() > 0 {
				events = append(events, textgen.Event{Kind: textgen.EventReasoningStart, ItemID: output.ID}, textgen.Event{Kind: textgen.EventReasoning, Text: text.String(), ItemID: output.ID})
			}
		default:
			for _, content := range output.Content {
				switch content.Type {
				case "text", "output_text":
					if content.Text != "" {
						events = append(events, textgen.Event{Kind: textgen.EventText, Text: content.Text})
					}
				case "refusal":
					if content.Refusal != "" {
						events = append(events, textgen.Event{Kind: textgen.EventRefusal, Text: content.Refusal})
					}
				}
			}
		}
	}
	if strings.EqualFold(response.Status, "incomplete") {
		stop = textgen.StopLength
		if response.IncompleteDetails != nil && strings.EqualFold(response.IncompleteDetails.Reason, "content_filter") {
			stop = textgen.StopFilter
		}
	}
	events = append(events, textgen.Event{Kind: textgen.EventStop, Stop: stop})
	if response.Usage != nil {
		events = append(events, textgen.Event{Kind: textgen.EventUsage, Usage: UsageOf(response.Usage)})
	}
	return events
}
