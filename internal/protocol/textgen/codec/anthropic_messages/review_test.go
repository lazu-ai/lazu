package anthropic_messages

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// Chat and Responses interleave parallel calls; Messages blocks are
// sequential. Each tool block gets exactly its own call's fragments.
func TestEmitterKeepsInterleavedToolArgumentsApart(t *testing.T) {
	emitter := NewStreamEmitter("id", "m")
	frames := emitter.Emit(
		textgen.Event{Kind: textgen.EventStart},
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 0, ToolCallID: "call_a", ToolName: "a"},
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 1, ToolCallID: "call_b", ToolName: "b"},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 0, Text: `{"x":`},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 1, Text: `{"y":`},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 0, Text: `1}`},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 1, Text: `2}`},
		textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopTool},
	)
	frames = append(frames, mustFinish(emitter.Finish(nil))...)
	blocks := map[int]string{}
	ids := map[int]string{}
	for _, frame := range frames {
		index, _ := frame.Data["index"].(int)
		switch frame.Event {
		case "content_block_start":
			block := frame.Data["content_block"].(map[string]any)
			ids[index], _ = block["id"].(string)
		case "content_block_delta":
			delta := frame.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				blocks[index] += delta["partial_json"].(string)
			}
		}
	}
	if ids[0] != "call_a" || blocks[0] != `{"x":1}` || ids[1] != "call_b" || blocks[1] != `{"y":2}` {
		t.Fatalf("tool blocks mixed: ids=%v args=%v", ids, blocks)
	}
}

// A stream that never said how it ended was cut off; it must not close as a
// normal end_turn.
func TestEmitterRefusesToFinishWithoutAStop(t *testing.T) {
	emitter := NewStreamEmitter("id", "m")
	_ = emitter.Emit(textgen.Event{Kind: textgen.EventStart}, textgen.Event{Kind: textgen.EventText, Text: "partial"})
	if frames, err := emitter.Finish(nil); err == nil || len(frames) != 0 {
		t.Fatalf("finish without a stop must fail and write nothing: %v %v", frames, err)
	}
	if failed := emitter.Fail(nil); len(failed) == 0 || failed[len(failed)-1].Event != "error" {
		t.Fatalf("the stream ends with an error event: %v", failed)
	}
}

// Messages' input_tokens excludes its cache buckets.
func TestUsageToExcludesCacheFromAnInclusivePromptCount(t *testing.T) {
	inclusive := &dto.Usage{PromptTokens: 100, CompletionTokens: 5}
	inclusive.PromptTokensDetails.CachedTokens = 80
	if got := usageTo(inclusive); got["input_tokens"] != 20 || got["cache_read_input_tokens"] != 80 {
		t.Fatalf("inclusive usage: %v", got)
	}
	exclusive := &dto.Usage{PromptTokens: 20, CompletionTokens: 5, PromptExcludesCache: true}
	exclusive.PromptTokensDetails.CachedTokens = 80
	if got := usageTo(exclusive); got["input_tokens"] != 20 {
		t.Fatalf("exclusive usage: %v", got)
	}
}

func decodeAll(t *testing.T, events ...string) []textgen.Event {
	t.Helper()
	decoder := &StreamDecoder{}
	var out []textgen.Event
	for _, data := range events {
		decoded, err := decoder.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, decoded...)
	}
	return out
}

// A call with no parameters carries input {} on its start and no deltas.
func TestDecoderGivesAZeroParameterCallEmptyObjectArguments(t *testing.T) {
	events := decodeAll(t,
		`{"type":"message_start","message":{"id":"msg_1","model":"m","usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"now","input":{}}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`,
	)
	var col textgen.Collector
	col.Add(events...)
	for _, part := range col.Result().Parts {
		if part.Kind == textgen.PartTool {
			if !json.Valid([]byte(part.Text)) || part.Text != "{}" {
				t.Fatalf("arguments = %q, want {}", part.Text)
			}
			return
		}
	}
	t.Fatal("no tool call collected")
}

// An empty reply is still an assistant turn.
func TestDecoderEmitsTheTurnForAnEmptyReply(t *testing.T) {
	events := decodeAll(t,
		`{"type":"message_start","message":{"id":"msg_1","model":"m","usage":{"input_tokens":1,"output_tokens":0}}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":0}}`,
		`{"type":"message_stop"}`,
	)
	for _, event := range events {
		if event.Kind == textgen.EventTurn && event.Role == "assistant" {
			return
		}
	}
	t.Fatalf("no assistant turn in %+v", events)
}
