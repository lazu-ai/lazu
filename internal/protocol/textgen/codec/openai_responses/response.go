package openai_responses

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

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
