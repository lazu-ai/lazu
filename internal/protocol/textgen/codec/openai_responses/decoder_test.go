package openai_responses

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
)

func decodeRecording(t *testing.T, name string) []textgen.Event {
	t.Helper()
	decoder := &StreamDecoder{}
	var events []textgen.Event
	for _, data := range fixtures.Data(t, name) {
		got, err := decoder.Decode(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		events = append(events, got...)
	}
	return events
}

func collect(events []textgen.Event) textgen.Result {
	var col textgen.Collector
	col.Add(events...)
	return col.Result()
}

// DeepSeek's recorded Responses stream: a reasoning item (reasoning_text
// deltas) then a message, completed with usage.
func TestDecodeRecordedDeepSeekResponsesText(t *testing.T) {
	events := decodeRecording(t, "deepseek/responses_text.sse")
	if events[0].Kind != textgen.EventStart || !strings.HasPrefix(events[0].ResponseID, "resp_") && events[0].ResponseID == "" {
		t.Fatalf("expected a start event, got %+v", events[0])
	}
	result := collect(events)
	kinds := []string{}
	for _, part := range result.Parts {
		kinds = append(kinds, string(part.Kind))
	}
	if !strings.HasPrefix(strings.Join(kinds, ","), "reasoning,") || !strings.HasSuffix(strings.Join(kinds, ","), ",text") {
		t.Fatalf("expected reasoning then text, got %v", kinds)
	}
	if result.Parts[0].ItemID == "" || result.Parts[0].Text == "" {
		t.Fatalf("reasoning part must keep the upstream item id and text: %+v", result.Parts[0])
	}
	if result.Stop != textgen.StopEnd || result.Usage == nil || result.Usage.PromptTokens == 0 || result.Usage.CompletionTokens == 0 {
		t.Fatalf("stop or usage lost: %q %+v", result.Stop, result.Usage)
	}
}

func TestDecodeRecordedDeepSeekResponsesToolCall(t *testing.T) {
	result := collect(decodeRecording(t, "deepseek/responses_tool_call.sse"))
	var tool *textgen.Part
	for i := range result.Parts {
		if result.Parts[i].Kind == textgen.PartTool {
			tool = &result.Parts[i]
		}
	}
	if tool == nil || tool.Name != "get_weather" || tool.CallID == "" {
		t.Fatalf("expected a tool part with name and call id, got %+v", result.Parts)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(tool.Text), &args); err != nil || args["city"] == nil {
		t.Fatalf("arguments did not reassemble: %q", tool.Text)
	}
	if result.Stop != textgen.StopTool {
		t.Fatalf("stop = %q, want tool", result.Stop)
	}
}

func TestDecodeRecordedDeepSeekResponsesJSONSchema(t *testing.T) {
	result := collect(decodeRecording(t, "deepseek/responses_json_schema.sse"))
	var answer map[string]any
	text := ""
	for _, part := range result.Parts {
		if part.Kind == textgen.PartText {
			text += part.Text
		}
	}
	if json.Unmarshal([]byte(text), &answer) != nil || answer["title"] == nil {
		t.Fatalf("structured output did not arrive as JSON: %q", text)
	}
}

// Responses upstream, Chat client: the recorded tool call becomes Chat
// tool_calls chunks with the call id and name, finishing with tool_calls.
func TestRecordedResponsesToChatClient(t *testing.T) {
	events := decodeRecording(t, "deepseek/responses_tool_call.sse")
	emitter := openai_chat.NewStreamEmitter("", "m")
	chunks := emitter.Emit(events...)
	chunks = append(chunks, mustFinish(emitter.Finish(nil))...)
	var id, name, args string
	for _, chunk := range chunks {
		for _, call := range chunk.Choices[0].Delta.ToolCalls {
			if call.ID != "" {
				id = call.ID
			}
			if call.Function.Name != "" {
				name = call.Function.Name
			}
			args += call.Function.Arguments
		}
	}
	last := chunks[len(chunks)-1]
	if id == "" || name != "get_weather" || !strings.Contains(args, "Paris") || *last.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("id=%q name=%q args=%q finish=%v", id, name, args, last.Choices[0].FinishReason)
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(args), &parsed) != nil {
		t.Fatalf("arguments were duplicated or broken across chunks: %q", args)
	}
}

// Responses upstream, Responses client, through the IR: the reply comes out
// with the same items.
func TestRecordedResponsesRoundTrip(t *testing.T) {
	events := decodeRecording(t, "deepseek/responses_text.sse")
	emitter := NewStreamEmitter("", "m", nil)
	payloads, err := emitter.Emit(events...)
	if err != nil {
		t.Fatal(err)
	}
	end, err := emitter.Finish(nil)
	if err != nil {
		t.Fatal(err)
	}
	payloads = append(payloads, end...)
	raw, _ := json.Marshal(payloads[len(payloads)-1]["response"].(map[string]any)["output"])
	var output []map[string]any
	_ = json.Unmarshal(raw, &output)
	if len(output) != 2 || output[0]["type"] != "reasoning" || output[1]["type"] != "message" {
		t.Fatalf("expected reasoning then message, got %+v", output)
	}
	if output[0]["id"] == "" {
		t.Fatalf("reasoning item lost its id: %+v", output[0])
	}
}

// mustFinish is an emitter's Finish for a stream known to have ended well.
func mustFinish[T any](out []T, err error) []T {
	if err != nil {
		panic(err)
	}
	return out
}
