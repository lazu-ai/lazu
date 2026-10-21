package openai_chat

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestChatEmitterRefusesToFinishWithoutAStop(t *testing.T) {
	emitter := NewStreamEmitter("id", "m")
	_ = emitter.Emit(textgen.Event{Kind: textgen.EventText, Text: "partial"})
	if chunks, err := emitter.Finish(nil); err == nil || len(chunks) != 0 {
		t.Fatalf("finish without a stop must fail and write nothing: %v %v", chunks, err)
	}
}

func TestRenderTypesARefusal(t *testing.T) {
	var col textgen.Collector
	col.Add(textgen.Event{Kind: textgen.EventRefusal, Text: "I can't help with that."}, textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopEnd})
	message := Render(col.Result(), "m").Choices[0].Message
	if message.Refusal == nil || *message.Refusal != "I can't help with that." {
		t.Fatalf("refusal not typed: %+v", message)
	}
	if message.Content != nil && message.StringContent() != "" {
		t.Fatalf("a refusal-only reply has no content: %+v", message.Content)
	}
}

// Chat writes a call's id and name once; a client that concatenates deltas
// would otherwise double the name.
func TestChatEmitterNamesACallOnce(t *testing.T) {
	emitter := NewStreamEmitter("id", "m")
	chunks := emitter.Emit(
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 0, ToolCallID: "call_1", ToolName: "get_weather"},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 0, ToolCallID: "call_1", ToolName: "get_weather", Text: `{"city":`},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 0, Text: `"Paris"}`},
	)
	var ids, names, args string
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			for _, call := range choice.Delta.ToolCalls {
				ids += call.ID
				names += call.Function.Name
				args += call.Function.Arguments
			}
		}
	}
	if ids != "call_1" || names != "get_weather" || args != `{"city":"Paris"}` {
		t.Fatalf("concatenated deltas: id %q name %q args %q", ids, names, args)
	}
}

func TestStreamDecoderTurnsAnErrorChunkIntoAnError(t *testing.T) {
	var chunk dto.ChatCompletionsStreamResponse
	if err := json.Unmarshal([]byte(`{"error":{"message":"Overloaded","type":"server_error","code":"overloaded"}}`), &chunk); err != nil {
		t.Fatal(err)
	}
	events := (&StreamDecoder{}).Decode(&chunk)
	if len(events) != 1 || events[0].Kind != textgen.EventError || events[0].Error.Message != "Overloaded" || events[0].Error.Type != "server_error" || events[0].Error.Code != "overloaded" {
		t.Fatalf("events: %+v", events)
	}
}

// An id and name first given on the done event are written even when the
// arguments it restates are not.
func TestChatEmitterWritesMetadataFromTheDoneEvent(t *testing.T) {
	emitter := NewStreamEmitter("id", "m")
	chunks := emitter.Emit(
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 0},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 0, Text: `{"q":1}`},
		textgen.Event{Kind: textgen.EventToolDone, ToolIndex: 0, ToolCallID: "call_1", ToolName: "lookup", Text: `{"q":1}`},
	)
	var ids, names, args string
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			for _, call := range choice.Delta.ToolCalls {
				ids += call.ID
				names += call.Function.Name
				args += call.Function.Arguments
			}
		}
	}
	if ids != "call_1" || names != "lookup" || args != `{"q":1}` {
		t.Fatalf("id %q name %q args %q", ids, names, args)
	}
}
