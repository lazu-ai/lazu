package openai_chat

import (
	"testing"

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
