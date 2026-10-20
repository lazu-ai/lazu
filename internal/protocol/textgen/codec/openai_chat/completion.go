package openai_chat

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// DecodeCompletion is a whole Chat completion as content events, in the
// order its stream would have said them: start, turn, reasoning, text or
// refusal, tool calls, stop, usage. Only the first choice is a reply; a
// bridge never asks for more than one.
func DecodeCompletion(response *dto.OpenAITextResponse) []textgen.Event {
	if response == nil {
		return nil
	}
	created, _ := response.Created.(float64)
	events := []textgen.Event{{Kind: textgen.EventStart, ResponseID: response.Id, Model: response.Model, CreatedAt: int64(created)}}
	if len(response.Choices) > 0 {
		choice := response.Choices[0]
		message := choice.Message
		events = append(events, textgen.Event{Kind: textgen.EventTurn, Role: "assistant"})
		reasoning := message.ReasoningContent
		if strings.TrimSpace(reasoning) == "" {
			reasoning = message.Reasoning
		}
		if reasoning != "" {
			events = append(events, textgen.Event{Kind: textgen.EventReasoningStart}, textgen.Event{Kind: textgen.EventReasoning, Text: reasoning})
		}
		if text := message.StringContent(); text != "" {
			events = append(events, textgen.Event{Kind: textgen.EventText, Text: text})
		}
		if message.Refusal != nil && *message.Refusal != "" {
			events = append(events, textgen.Event{Kind: textgen.EventRefusal, Text: *message.Refusal})
		}
		for index, call := range message.ParseToolCalls() {
			events = append(events,
				textgen.Event{Kind: textgen.EventToolStart, ToolIndex: index, ToolCallID: call.ID, ToolName: call.Function.Name},
				textgen.Event{Kind: textgen.EventToolDone, ToolIndex: index, ToolCallID: call.ID, ToolName: call.Function.Name, Text: call.Function.Arguments},
			)
		}
		events = append(events, textgen.Event{Kind: textgen.EventStop, Stop: StopReasonOf(choice.FinishReason)})
	}
	usage := response.Usage
	events = append(events, textgen.Event{Kind: textgen.EventUsage, Usage: &usage})
	return events
}
