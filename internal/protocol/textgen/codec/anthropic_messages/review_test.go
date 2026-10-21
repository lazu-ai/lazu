package anthropic_messages

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
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

// A Chat upstream may name a call on a later fragment; the tool_use block
// is opened only once the name is known, with the arguments held so far.
func TestEmitterWaitsForALateToolName(t *testing.T) {
	emitter := NewStreamEmitter("id", "m")
	frames := emitter.Emit(
		textgen.Event{Kind: textgen.EventStart},
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 0},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 0, Text: `{"x":`},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 0, ToolCallID: "call_a", ToolName: "a", Text: `1}`},
		textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopTool},
	)
	frames = append(frames, mustFinish(emitter.Finish(nil))...)
	var id, name, args string
	for _, frame := range frames {
		switch frame.Event {
		case "content_block_start":
			block := frame.Data["content_block"].(map[string]any)
			id, _ = block["id"].(string)
			name, _ = block["name"].(string)
		case "content_block_delta":
			if delta := frame.Data["delta"].(map[string]any); delta["type"] == "input_json_delta" {
				args += delta["partial_json"].(string)
			}
		}
	}
	if id != "call_a" || name != "a" || args != `{"x":1}` {
		t.Fatalf("id %q name %q args %q", id, name, args)
	}
}

func TestStopReasonOfContextWindowIsTruncation(t *testing.T) {
	if StopReasonOf("model_context_window_exceeded") != textgen.StopLength {
		t.Fatal("a context-window stop cuts the answer short")
	}
}

// pause_turn is not a finished reply: a bridged client cannot continue the
// server-tool loop, so it is an error, after the usage settlement reads.
func TestPauseTurnIsNotACompletion(t *testing.T) {
	events, err := (&StreamDecoder{}).Decode(`{"type":"message_delta","delta":{"stop_reason":"pause_turn"},"usage":{"output_tokens":7}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != textgen.EventUsage || events[1].Kind != textgen.EventError || events[1].Error.Code != "pause_turn" {
		t.Fatalf("events: %+v", events)
	}
}

// Messages allows text after a tool call in one assistant turn; Chat
// answers the calls right after their message, so the text joins it.
func TestTextAfterAToolCallStaysInTheChatTurn(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","max_tokens":10,"messages":[
		{"role":"user","content":"q"},
		{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"f","input":{}},{"type":"text","text":"checking"}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := openai_chat.Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	roles := ""
	for _, message := range wire.Messages {
		roles += message.Role + ","
	}
	if roles != "user,assistant,tool," || wire.Messages[1].StringContent() != "checking" || len(wire.Messages[1].ParseToolCalls()) != 1 {
		t.Fatalf("roles %s, assistant %+v", roles, wire.Messages[1])
	}
}

// The format's description becomes the schema's own; an empty cached
// instruction keeps its marker on a placeholder.
func TestEncodeKeepsSchemaDescriptionAndEmptyCachedInstruction(t *testing.T) {
	request := &textgen.Request{
		Instructions: []textgen.Instruction{{Role: "system", Text: "", CacheControl: json.RawMessage(`{"type":"ephemeral"}`)}},
		Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "q"}}}},
		Output:       textgen.OutputConstraint{Type: "json_schema", Description: "a post title", Schema: json.RawMessage(`{"type":"object"}`)},
	}
	out, err := Encode(request, EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.OutputConfig), `"description":"a post title"`) {
		t.Fatalf("output_config: %s", out.OutputConfig)
	}
	system, _ := json.Marshal(out.System)
	if !strings.Contains(string(system), `"cache_control"`) {
		t.Fatalf("system: %s", system)
	}
}

// A refused whole reply's text is the refusal.
func TestDecodeMessageTypesARefusal(t *testing.T) {
	events, err := DecodeMessage([]byte(`{"id":"msg_1","model":"m","content":[{"type":"text","text":"I can't help with that."}],"stop_reason":"refusal"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == textgen.EventText {
			t.Fatalf("refusal text decoded as text: %+v", events)
		}
	}
}

// A call named before its id arrives waits for the id: a tool_use block's
// identity is written once.
func TestEmitterWaitsForALateToolID(t *testing.T) {
	emitter := NewStreamEmitter("msg_1", "m")
	var frames []Frame
	frames = append(frames, emitter.Emit(
		textgen.Event{Kind: textgen.EventStart},
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 0, ToolName: "Read"},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 0, ToolCallID: "call_late", Text: `{"path":"a"}`},
		textgen.Event{Kind: textgen.EventToolDone, ToolIndex: 0},
	)...)
	for _, frame := range frames {
		if frame.Event == "content_block_start" {
			body, _ := json.Marshal(frame.Data)
			if !strings.Contains(string(body), `"id":"call_late"`) {
				t.Fatalf("tool_use opened without the upstream's id: %s", body)
			}
			return
		}
	}
	t.Fatal("no tool_use block")
}

// Arguments that are not a JSON object are not rewritten into an empty
// input.
func TestRenderRefusesNonObjectToolArguments(t *testing.T) {
	var col textgen.Collector
	col.Add(
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 0, ToolCallID: "c", ToolName: "f"},
		textgen.Event{Kind: textgen.EventToolDone, ToolIndex: 0, Text: `{"cut":`},
		textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopLength},
	)
	if _, err := Render(col.Result(), "m"); err == nil {
		t.Fatal("malformed arguments must not render as a tool call")
	}
}
