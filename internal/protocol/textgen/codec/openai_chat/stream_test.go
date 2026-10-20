package openai_chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
)

// decodeRecording runs a recorded Chat stream through the decoder.
func decodeRecording(t *testing.T, name string) []textgen.Event {
	t.Helper()
	decoder := &StreamDecoder{}
	var events []textgen.Event
	for _, data := range fixtures.Data(t, name) {
		if strings.TrimSpace(data) == "[DONE]" {
			continue
		}
		var chunk dto.ChatCompletionsStreamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		events = append(events, decoder.Decode(&chunk)...)
	}
	return events
}

func gather(events []textgen.Event) (text, reasoning, args string, kinds map[textgen.EventKind]int, stop textgen.StopReason) {
	kinds = map[textgen.EventKind]int{}
	for _, event := range events {
		kinds[event.Kind]++
		switch event.Kind {
		case textgen.EventText:
			text += event.Text
		case textgen.EventReasoning:
			reasoning += event.Text
		case textgen.EventToolArguments:
			args += event.Text
		case textgen.EventStop:
			stop = event.Stop
		}
	}
	return
}

// DeepSeek's recorded Chat stream: reasoning_content deltas, then content,
// one stop, usage on the last chunk.
func TestDecodeRecordedDeepSeekChatText(t *testing.T) {
	events := decodeRecording(t, "deepseek/chat_text.sse")
	text, reasoning, _, kinds, stop := gather(events)
	if kinds[textgen.EventStart] != 1 || events[0].Kind != textgen.EventStart || events[0].Model != "deepseek-flash" {
		t.Fatalf("expected one start event naming the model first, got %+v", events[0])
	}
	if reasoning == "" || text == "" || stop != textgen.StopEnd {
		t.Fatalf("reasoning present=%v text=%q stop=%q", len(reasoning) > 0, text, stop)
	}
	if kinds[textgen.EventUsage] != 1 || kinds[textgen.EventToolStart] != 0 {
		t.Fatalf("unexpected event mix: %v", kinds)
	}
}

// DeepSeek's recorded tool call: the first fragment carries id and name, the
// rest arguments; the stop reason is tool.
func TestDecodeRecordedDeepSeekChatToolCall(t *testing.T) {
	events := decodeRecording(t, "deepseek/chat_tool_call.sse")
	_, _, args, kinds, stop := gather(events)
	if kinds[textgen.EventToolStart] != 1 || stop != textgen.StopTool {
		t.Fatalf("expected one tool call ending in tool stop, got %v stop=%q", kinds, stop)
	}
	var start textgen.Event
	for _, event := range events {
		if event.Kind == textgen.EventToolStart {
			start = event
		}
	}
	if start.ToolName != "get_weather" || start.ToolCallID == "" {
		t.Fatalf("tool start lost its name or id: %+v", start)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil || parsed["city"] == nil {
		t.Fatalf("arguments did not reassemble to the call: %q (%v)", args, err)
	}
}

func TestDecoderOpensEachToolIndexOnceAndCarriesLateNames(t *testing.T) {
	decoder := &StreamDecoder{}
	index := 2
	first := decoder.Decode(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &index, Function: dto.FunctionResponse{Arguments: "{"}}}}}}})
	second := decoder.Decode(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &index, ID: "call_2", Function: dto.FunctionResponse{Name: "lookup", Arguments: "}"}}}}}}})
	if first[1].Kind != textgen.EventToolStart || first[1].ToolIndex != 2 || first[2].Kind != textgen.EventToolArguments || first[2].Text != "{" {
		t.Fatalf("first fragment: %+v", first)
	}
	if len(second) != 1 || second[0].Kind != textgen.EventToolArguments || second[0].ToolCallID != "call_2" || second[0].ToolName != "lookup" || second[0].Text != "}" {
		t.Fatalf("late id and name must ride the arguments event: %+v", second)
	}
}

func TestStopReasonRoundTrip(t *testing.T) {
	for finish, stop := range map[string]textgen.StopReason{"stop": textgen.StopEnd, "length": textgen.StopLength, "tool_calls": textgen.StopTool, "content_filter": textgen.StopFilter} {
		if StopReasonOf(finish) != stop || FinishReasonOf(stop) != finish {
			t.Fatalf("%s <-> %s", finish, stop)
		}
	}
}
