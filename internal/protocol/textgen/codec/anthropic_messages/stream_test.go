package anthropic_messages

import (
	"encoding/json"
	"github.com/lazu-ai/lazu/internal/dto"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
)

func decodeRecording(t *testing.T, name string) []textgen.Event {
	t.Helper()
	decoder := &StreamDecoder{}
	var events []textgen.Event
	for _, ev := range fixtures.Events(t, name) {
		got, err := decoder.Decode(ev.Data)
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

func TestDecodeRecordedText(t *testing.T) {
	events := decodeRecording(t, "anthropic/text.sse")
	// message_start's usage comes first, so an emitter's start has it
	if events[0].Kind != textgen.EventUsage || events[0].Usage.PromptTokens == 0 {
		t.Fatalf("expected the input usage before the start, got %+v", events[0])
	}
	if events[1].Kind != textgen.EventStart || events[1].Model != "claude-haiku-5-5" || !strings.HasPrefix(events[1].ResponseID, "msg_") {
		t.Fatalf("expected a start event naming message and model, got %+v", events[1])
	}
	result := collect(events)
	if len(result.Parts) != 1 || result.Parts[0].Kind != textgen.PartText || result.Parts[0].Text == "" {
		t.Fatalf("expected one text part, got %+v", result.Parts)
	}
	if !result.StopSeen || result.Stop != textgen.StopEnd || result.Usage == nil || result.Usage.PromptTokens == 0 || result.Usage.CompletionTokens == 0 || !result.Usage.PromptExcludesCache {
		t.Fatalf("stop or usage lost: stop=%q usage=%+v", result.Stop, result.Usage)
	}
}

// The thinking_block recording: a thinking block with a signature, then text.
func TestDecodeRecordedThinkingBlock(t *testing.T) {
	events := decodeRecording(t, "anthropic/thinking_block.sse")
	kinds := map[textgen.EventKind]int{}
	for _, event := range events {
		kinds[event.Kind]++
	}
	if kinds[textgen.EventReasoningStart] != 1 || kinds[textgen.EventReasoning] == 0 || kinds[textgen.EventReasoningSignature] != 1 {
		t.Fatalf("expected one reasoning block with deltas and a signature: %v", kinds)
	}
	result := collect(events)
	if len(result.Parts) != 2 || result.Parts[0].Kind != textgen.PartReasoning || result.Parts[1].Kind != textgen.PartText {
		t.Fatalf("expected reasoning then text, got %+v", result.Parts)
	}
	if result.Parts[0].Signature == "" || result.Parts[0].SealedBy != SealedBy || result.Parts[0].Text == "" {
		t.Fatalf("the reasoning part must carry its text and Anthropic's signature: %+v", result.Parts[0])
	}
	if result.Usage == nil || result.Usage.CompletionTokenDetails.ReasoningTokens == 0 {
		t.Fatalf("thinking tokens lost: %+v", result.Usage)
	}
}

func TestDecodeRecordedToolCall(t *testing.T) {
	events := decodeRecording(t, "anthropic/tool_call.sse")
	result := collect(events)
	if len(result.Parts) != 1 || result.Parts[0].Kind != textgen.PartTool || result.Parts[0].Name != "get_weather" || !strings.HasPrefix(result.Parts[0].CallID, "toolu_") {
		t.Fatalf("expected one tool part, got %+v", result.Parts)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(result.Parts[0].Text), &args); err != nil || args["city"] == nil {
		t.Fatalf("arguments did not reassemble: %q (%v)", result.Parts[0].Text, err)
	}
	if result.Stop != textgen.StopTool {
		t.Fatalf("stop = %q, want tool", result.Stop)
	}
	var started, done bool
	for _, event := range events {
		started = started || event.Kind == textgen.EventToolStart
		done = done || event.Kind == textgen.EventToolDone
	}
	if !started || !done {
		t.Fatal("tool start and done must both be emitted")
	}
}

func TestDecodeRecordedJSONSchemaAnswersJSON(t *testing.T) {
	result := collect(decodeRecording(t, "anthropic/json_schema.sse"))
	var answer map[string]any
	if len(result.Parts) != 1 || json.Unmarshal([]byte(result.Parts[0].Text), &answer) != nil || answer["title"] == nil {
		t.Fatalf("structured output did not arrive as JSON: %+v", result.Parts)
	}
}

// Anthropic upstream, Chat client: the recorded thinking stream becomes
// reasoning_content and content chunks with a stop finish.
func TestRecordedAnthropicToChatClient(t *testing.T) {
	events := decodeRecording(t, "anthropic/thinking_block.sse")
	emitter := openai_chat.NewStreamEmitter("", "m")
	chunks := emitter.Emit(events...)
	chunks = append(chunks, mustFinish(emitter.Finish(nil))...)
	var usageChunk *dto.ChatCompletionsStreamResponse
	if n := len(chunks); n > 0 && openai_chat.IsUsageChunk(chunks[n-1]) {
		usageChunk, chunks = chunks[n-1], chunks[:n-1]
	}
	_ = usageChunk
	if chunks[0].Choices[0].Delta.Role != "assistant" || !strings.HasPrefix(chunks[0].Id, "chatcmpl-") {
		t.Fatalf("expected a role chunk first, got %+v", chunks[0])
	}
	var reasoning, text string
	for _, chunk := range chunks {
		delta := chunk.Choices[0].Delta
		reasoning += delta.GetReasoningContent()
		if delta.Content != nil {
			text += *delta.Content
		}
	}
	last := chunks[len(chunks)-1]
	if reasoning == "" || text == "" || last.Choices[0].FinishReason == nil || *last.Choices[0].FinishReason != "stop" || usageChunk == nil || usageChunk.Usage.CompletionTokens == 0 {
		t.Fatalf("reasoning=%v text=%v last=%+v", reasoning != "", text != "", last)
	}
	tool := decodeRecording(t, "anthropic/tool_call.sse")
	toolEmitter := openai_chat.NewStreamEmitter("", "m")
	toolChunks := toolEmitter.Emit(tool...)
	toolChunks = append(toolChunks, mustFinish(toolEmitter.Finish(nil))...)
	if n := len(toolChunks); n > 0 && openai_chat.IsUsageChunk(toolChunks[n-1]) {
		usageChunk, toolChunks = toolChunks[n-1], toolChunks[:n-1]
	}
	_ = usageChunk
	var callID, name, args string
	for _, chunk := range toolChunks {
		for _, call := range chunk.Choices[0].Delta.ToolCalls {
			if call.ID != "" {
				callID = call.ID
			}
			if call.Function.Name != "" {
				name = call.Function.Name
			}
			args += call.Function.Arguments
		}
	}
	if !strings.HasPrefix(callID, "toolu_") || name != "get_weather" || !strings.Contains(args, "Paris") || *toolChunks[len(toolChunks)-1].Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("tool call did not reach the Chat client: id=%q name=%q args=%q", callID, name, args)
	}
}

// Anthropic upstream, Responses client: a reasoning item, a message, the
// terminal event with both.
func TestRecordedAnthropicToResponsesClient(t *testing.T) {
	events := decodeRecording(t, "anthropic/thinking_block.sse")
	emitter := openai_responses.NewStreamEmitter("", "m", nil)
	payloads, err := emitter.Emit(events...)
	if err != nil {
		t.Fatal(err)
	}
	end, err := emitter.Finish(nil)
	if err != nil {
		t.Fatal(err)
	}
	payloads = append(payloads, end...)
	last := payloads[len(payloads)-1]
	if last["type"] != "response.completed" {
		t.Fatalf("expected response.completed, got %v", last["type"])
	}
	raw, _ := json.Marshal(last["response"].(map[string]any)["output"])
	var output []map[string]any
	_ = json.Unmarshal(raw, &output)
	if len(output) != 2 || output[0]["type"] != "reasoning" || output[1]["type"] != "message" {
		t.Fatalf("expected reasoning then message, got %+v", output)
	}
}

// Chat upstream (DeepSeek's recorded streams), Claude client.
func TestRecordedDeepSeekChatToClaudeClient(t *testing.T) {
	decoder := &openai_chat.StreamDecoder{}
	emitter := NewStreamEmitter("", "deepseek-flash")
	var frames []Frame
	for _, data := range fixtures.Data(t, "deepseek/chat_text.sse") {
		if strings.TrimSpace(data) == "[DONE]" {
			continue
		}
		chunk := decodeChatChunk(t, data)
		frames = append(frames, emitter.Emit(decoder.Decode(chunk)...)...)
	}
	frames = append(frames, mustFinish(emitter.Finish(nil))...)
	names := make([]string, 0, len(frames))
	for _, frame := range frames {
		names = append(names, frame.Event)
	}
	joined := strings.Join(names, " ")
	if !strings.HasPrefix(joined, "message_start content_block_start content_block_delta") || !strings.HasSuffix(joined, "message_delta message_stop") {
		t.Fatalf("unexpected frame order: %s", joined)
	}
	blocks := []string{}
	for _, frame := range frames {
		if frame.Event == "content_block_start" {
			blocks = append(blocks, frame.Data["content_block"].(map[string]any)["type"].(string))
		}
	}
	if strings.Join(blocks, ",") != "thinking,text" {
		t.Fatalf("expected a thinking block then a text block, got %v", blocks)
	}
	delta := frames[len(frames)-2].Data["delta"].(map[string]any)
	usage := frames[len(frames)-2].Data["usage"].(map[string]any)
	// a Chat upstream reports usage on its last chunk, so message_start
	// cannot carry the input count; message_delta does
	if delta["stop_reason"] != "end_turn" || usage["output_tokens"].(int) == 0 || usage["input_tokens"].(int) == 0 {
		t.Fatalf("message_delta incomplete: %+v %+v", delta, usage)
	}
	// tool call: one tool_use block with the arguments streamed, stop tool_use
	toolDecoder := &openai_chat.StreamDecoder{}
	toolEmitter := NewStreamEmitter("", "deepseek-flash")
	frames = nil
	for _, data := range fixtures.Data(t, "deepseek/chat_tool_call.sse") {
		if strings.TrimSpace(data) == "[DONE]" {
			continue
		}
		frames = append(frames, toolEmitter.Emit(toolDecoder.Decode(decodeChatChunk(t, data))...)...)
	}
	frames = append(frames, mustFinish(toolEmitter.Finish(nil))...)
	var args string
	var toolBlock map[string]any
	for _, frame := range frames {
		if frame.Event == "content_block_start" {
			toolBlock = frame.Data["content_block"].(map[string]any)
		}
		if frame.Event == "content_block_delta" {
			if d := frame.Data["delta"].(map[string]any); d["type"] == "input_json_delta" {
				args += d["partial_json"].(string)
			}
		}
	}
	if toolBlock == nil || toolBlock["type"] != "tool_use" || toolBlock["name"] != "get_weather" || !strings.Contains(args, "Paris") {
		t.Fatalf("tool_use block incomplete: %+v args=%q", toolBlock, args)
	}
	if frames[len(frames)-2].Data["delta"].(map[string]any)["stop_reason"] != "tool_use" {
		t.Fatalf("stop reason must be tool_use: %+v", frames[len(frames)-2].Data)
	}
}

func TestRenderWholeMessage(t *testing.T) {
	result := collect(decodeRecording(t, "anthropic/tool_call.sse"))
	message, err := Render(result, "m")
	if err != nil {
		t.Fatal(err)
	}
	content := message["content"].([]any)
	if message["stop_reason"] != "tool_use" || len(content) != 1 || content[0].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("rendered message wrong: %+v", message)
	}
	input := content[0].(map[string]any)["input"].(map[string]any)
	if input["city"] == nil {
		t.Fatalf("tool input must be an object: %+v", input)
	}
}

func TestEmitterPassesOwnSealedReasoningAndDropsForeign(t *testing.T) {
	emitter := NewStreamEmitter("id", "m")
	frames := emitter.Emit(
		textgen.Event{Kind: textgen.EventStart},
		textgen.Event{Kind: textgen.EventReasoningSealed, Sealed: []byte(`{"type":"redacted_thinking","data":"abc"}`), SealedBy: SealedBy},
		textgen.Event{Kind: textgen.EventReasoningSealed, Sealed: []byte(`{"type":"reasoning","id":"rs_x","encrypted_content":"y"}`), SealedBy: openai_responses.SealedBy},
		textgen.Event{Kind: textgen.EventText, Text: "ok"},
		textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopEnd},
	)
	frames = append(frames, mustFinish(emitter.Finish(nil))...)
	blocks := []string{}
	for _, frame := range frames {
		if frame.Event == "content_block_start" {
			blocks = append(blocks, frame.Data["content_block"].(map[string]any)["type"].(string))
		}
	}
	if strings.Join(blocks, ",") != "redacted_thinking,text" {
		t.Fatalf("expected the own seal passed and the foreign one dropped, got %v", blocks)
	}
}

func decodeChatChunk(t *testing.T, data string) *dtoChatChunk {
	t.Helper()
	var chunk dtoChatChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		t.Fatal(err)
	}
	return &chunk
}

// mustFinish is an emitter's Finish for a stream known to have ended well.
func mustFinish[T any](out []T, err error) []T {
	if err != nil {
		panic(err)
	}
	return out
}
