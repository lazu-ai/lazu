package openai_responses

import (
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
)

// chatStream drives Chat chunks through the Chat decoder and the Responses
// emitter and returns every Responses event written, Finish included when
// finish is true.
func chatStream(t *testing.T, emitter *StreamEmitter, finishUsage *dto.Usage, finish bool, chunks ...*dto.ChatCompletionsStreamResponse) []map[string]any {
	t.Helper()
	decoder := &openai_chat.StreamDecoder{}
	var out []map[string]any
	for _, chunk := range chunks {
		payloads, err := emitter.Emit(decoder.Decode(chunk)...)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, payloads...)
	}
	if finish {
		payloads, err := emitter.Finish(finishUsage)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, payloads...)
	}
	return out
}

func types(payloads []map[string]any) []string {
	out := make([]string, 0, len(payloads))
	for _, payload := range payloads {
		out = append(out, payload["type"].(string))
	}
	return out
}

func terminalOutput(t *testing.T, payloads []map[string]any) []map[string]any {
	t.Helper()
	last := payloads[len(payloads)-1]
	response, ok := last["response"].(map[string]any)
	if !ok {
		t.Fatalf("last event is not terminal: %+v", last)
	}
	var out []map[string]any
	for _, item := range response["output"].([]any) {
		out = append(out, wireObject(t, item))
	}
	return out
}

func TestChatStreamEmitsCompleteOrderedResponsesLifecycle(t *testing.T) {
	emitter := NewStreamEmitter("chatcmpl_test", "m", nil)
	finish := "tool_calls"
	payloads := chatStream(t, emitter, &dto.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}, true,
		&dto.ChatCompletionsStreamResponse{Id: "chatcmpl_test", Created: 123, Model: "m", Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: stringPointer("hel")}}}},
		&dto.ChatCompletionsStreamResponse{Id: "chatcmpl_test", Created: 123, Model: "m", Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: stringPointer("lo"), ToolCalls: []dto.ToolCallResponse{toolDelta(3, "call_3", "lookup", "{\"x\":")}}}}},
		&dto.ChatCompletionsStreamResponse{Id: "chatcmpl_test", Created: 123, Model: "m", Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{toolDelta(3, "", "", "1}")}}, FinishReason: &finish}}},
	)
	for index, payload := range payloads {
		if payload["sequence_number"] != uint64(index) {
			t.Fatalf("sequence[%d]=%v", index, payload["sequence_number"])
		}
	}
	last := payloads[len(payloads)-1]
	response := last["response"].(map[string]any)
	if last["type"] != "response.completed" || response["created_at"] != int64(123) || response["id"] != "resp_chatcmpl_test" {
		t.Fatalf("unexpected terminal event: %+v", last)
	}
	output := terminalOutput(t, payloads)
	if len(output) != 2 || output[0]["type"] != "message" || output[1]["type"] != "function_call" {
		t.Fatalf("terminal output incomplete: %+v", output)
	}
	if output[0]["content"].([]any)[0].(map[string]any)["text"] != "hello" || output[1]["arguments"] != `{"x":1}` || output[1]["call_id"] != "call_3" {
		t.Fatalf("terminal output wrong: %+v", output)
	}
	order := map[string]int{}
	for index, payload := range payloads {
		switch payload["type"] {
		case "response.output_text.done", "response.function_call_arguments.done":
			order[payload["type"].(string)] = index
		case "response.content_part.done":
			if order["part"] == 0 {
				order["part"] = index
			}
		case "response.output_item.done":
			if item, _ := payload["item"].(map[string]any); item != nil && item["type"] == "function_call" {
				order["tool"] = index
			}
		}
	}
	if order["response.output_text.done"] >= order["part"] || order["response.function_call_arguments.done"] >= order["tool"] {
		t.Fatalf("protocol-specific done events are missing or out of order: %v", types(payloads))
	}
	usage := response["usage"].(*dto.ResponsesUsage)
	if usage.InputTokens != 2 || usage.OutputTokens != 3 {
		t.Fatalf("terminal usage was not normalized to Responses fields: %+v", usage)
	}
}

func TestChatStreamPreservesReasoningLifecycle(t *testing.T) {
	emitter := NewStreamEmitter("chatcmpl_reasoning", "m", nil)
	reasoning, answer, finish := "think", "done", "stop"
	payloads := chatStream(t, emitter, nil, true,
		&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: &reasoning}}}},
		&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &answer}, FinishReason: &finish}}},
	)
	output := terminalOutput(t, payloads)
	if output[0]["type"] != "reasoning" {
		t.Fatalf("reasoning output was not retained first in the terminal response: %+v", output)
	}
	assertReasoningSchema(t, output[0])
	if output[0]["content"].([]any)[0].(map[string]any)["text"] != reasoning {
		t.Fatalf("reasoning text lost: %+v", output[0])
	}
	seen := map[string]bool{}
	for _, payload := range payloads {
		seen[payload["type"].(string)] = true
	}
	for _, want := range []string{"response.output_item.added", "response.content_part.added", "response.reasoning_text.delta", "response.reasoning_text.done", "response.content_part.done", "response.output_item.done"} {
		if !seen[want] {
			t.Fatalf("missing reasoning lifecycle event %q: %v", want, types(payloads))
		}
	}
}

// A reasoning boundary (Anthropic's thinking blocks) opens a second
// reasoning item; the Chat decoder never emits one, so it is driven here.
func TestEmitterOpensANewReasoningItemAtABoundary(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil)
	payloads, err := emitter.Emit(
		textgen.Event{Kind: textgen.EventStart, ResponseID: "id"},
		textgen.Event{Kind: textgen.EventReasoningStart},
		textgen.Event{Kind: textgen.EventReasoning, Text: "first"},
		textgen.Event{Kind: textgen.EventReasoningStart},
		textgen.Event{Kind: textgen.EventReasoning, Text: "second"},
		textgen.Event{Kind: textgen.EventText, Text: "answer"},
		textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopEnd},
	)
	if err != nil {
		t.Fatal(err)
	}
	end, err := emitter.Finish(nil)
	if err != nil {
		t.Fatal(err)
	}
	output := terminalOutput(t, append(payloads, end...))
	if len(output) != 3 || output[0]["type"] != "reasoning" || output[1]["type"] != "reasoning" || output[2]["type"] != "message" {
		t.Fatalf("expected two reasoning items then a message, got %+v", output)
	}
	if output[0]["id"] == output[1]["id"] {
		t.Fatal("reasoning items must have distinct ids")
	}
}

func TestChatStreamTerminalOutputPreservesAssignedOutputIndexes(t *testing.T) {
	emitter := NewStreamEmitter("chatcmpl_tool_first", "m", nil)
	text, finish := "after tool", "stop"
	payloads := chatStream(t, emitter, nil, true,
		&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{toolDelta(0, "call_0", "lookup", `{}`)}}}}},
		&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text}, FinishReason: &finish}}},
	)
	output := terminalOutput(t, payloads)
	if len(output) != 2 || output[0]["type"] != "function_call" || output[1]["type"] != "message" {
		t.Fatalf("terminal output disagreed with assigned output indexes: %+v", output)
	}
}

func TestChatStreamFailureIsTerminalExactlyOnce(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil)
	first := emitter.Fail(assertionError("boom"))
	if len(first) == 0 || first[len(first)-1]["type"] != "response.failed" {
		t.Fatalf("expected response.failed, got %v", types(first))
	}
	if again := emitter.Fail(assertionError("again")); len(again) != 0 {
		t.Fatalf("failure emitted twice: %v", types(again))
	}
	if payloads, err := emitter.Finish(nil); err != nil || len(payloads) != 0 {
		t.Fatalf("finish after failure must be a no-op: %v %v", payloads, err)
	}
}

func TestChatStreamRejectsRetainedOutputBeyondLimit(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil, minimumChatStreamRetainedOutputBytes)
	decoder := &openai_chat.StreamDecoder{}
	big := strings.Repeat("x", minimumChatStreamRetainedOutputBytes+1)
	payloads, err := emitter.Emit(decoder.Decode(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &big}}}})...)
	if err == nil {
		t.Fatal("expected the retained output limit to be enforced")
	}
	for _, payload := range payloads {
		if payload["type"] == "response.output_text.delta" {
			t.Fatal("the offending delta must not be written")
		}
	}
}

func TestChatStreamFailureClosesAndRetainsPartialItems(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil)
	text := "partial"
	payloads := chatStream(t, emitter, nil, false, &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text, ToolCalls: []dto.ToolCallResponse{toolDelta(0, "call_0", "lookup", `{"a":`)}}}}})
	failed := emitter.Fail(assertionError("cut"), &dto.Usage{PromptTokens: 1, CompletionTokens: 1})
	payloads = append(payloads, failed...)
	last := payloads[len(payloads)-1]
	if last["type"] != "response.failed" {
		t.Fatalf("expected response.failed last, got %v", types(payloads))
	}
	response := last["response"].(map[string]any)
	if response["status"] != "failed" || response["error"] == nil {
		t.Fatalf("failed envelope incomplete: %+v", response)
	}
	output := terminalOutput(t, payloads)
	if len(output) != 2 || output[0]["status"] != "incomplete" || output[1]["status"] != "incomplete" || output[1]["arguments"] != `{"a":` {
		t.Fatalf("partial items must be closed as incomplete and retained: %+v", output)
	}
	seen := map[string]bool{}
	for _, payload := range failed {
		seen[payload["type"].(string)] = true
	}
	if !seen["response.output_item.done"] || !seen["response.function_call_arguments.done"] {
		t.Fatalf("open items were not closed before failing: %v", types(failed))
	}
}

func TestChatStreamEnvelopeCarriesSemanticRequestConfiguration(t *testing.T) {
	request := &textgen.Request{Model: textgen.ModelRef{Canonical: "m"}, Instructions: []textgen.Instruction{{Role: "system", Text: "be brief"}}, Sampling: textgen.SamplingOptions{MaxOutput: textgen.Some[uint](64), Temperature: textgen.Some(0.2)}, Metadata: textgen.MetadataOptions{User: "u1"}}
	emitter := NewStreamEmitter("id", "m", request)
	finish := "stop"
	text := "ok"
	payloads := chatStream(t, emitter, nil, true, &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text}, FinishReason: &finish}}})
	response := wireObject(t, payloads[len(payloads)-1]["response"])
	if response["instructions"] != "be brief" || response["max_output_tokens"] != float64(64) || response["temperature"] != 0.2 || response["user"] != "u1" {
		t.Fatalf("envelope did not carry the request configuration: %+v", response)
	}
}

func TestChatStreamEnvelopeUsesChatConfigurationDefaults(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil)
	finish := "stop"
	text := "ok"
	payloads := chatStream(t, emitter, nil, true, &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text}, FinishReason: &finish}}})
	response := wireObject(t, payloads[len(payloads)-1]["response"])
	if response["temperature"] != float64(1) || response["top_p"] != float64(1) || response["parallel_tool_calls"] != true || response["truncation"] != "disabled" {
		t.Fatalf("stream response defaults were not represented: %+v", response)
	}
	if response["max_output_tokens"] != nil || response["tool_choice"] != "none" || response["store"] != false {
		t.Fatalf("omitted stream configuration was misrepresented: %+v", response)
	}
}

func TestChatStreamRejectsMissingTerminalFinishReason(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil)
	text := "ok"
	chatStream(t, emitter, nil, false, &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text}}}})
	if _, err := emitter.Finish(nil); err == nil {
		t.Fatal("a stream without a stop reason must not complete")
	}
}

func TestChatStreamContentFilterIsIncomplete(t *testing.T) {
	for reason, want := range map[string]string{"length": "max_output_tokens", "content_filter": "content_filter"} {
		emitter := NewStreamEmitter("id", "m", nil)
		finish := reason
		text := "ok"
		payloads := chatStream(t, emitter, nil, true, &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text}, FinishReason: &finish}}})
		last := payloads[len(payloads)-1]
		details, _ := last["response"].(map[string]any)["incomplete_details"].(map[string]any)
		if last["type"] != "response.incomplete" || details["reason"] != want {
			t.Fatalf("%s: expected response.incomplete with %s, got %+v", reason, want, last)
		}
	}
}

func TestChatStreamPreservesRefusalLifecycle(t *testing.T) {
	emitter := NewStreamEmitter("chatcmpl_refusal", "m", nil)
	finish, refusal := "stop", "not allowed"
	payloads := chatStream(t, emitter, nil, true, &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Refusal: &refusal}, FinishReason: &finish}}})
	var deltaSeen, refusalDoneSeen, partDoneSeen bool
	for _, payload := range payloads {
		switch payload["type"] {
		case "response.refusal.delta":
			deltaSeen = payload["delta"] == refusal
		case "response.refusal.done":
			refusalDoneSeen = payload["refusal"] == refusal
		case "response.content_part.done":
			part, _ := payload["part"].(map[string]any)
			partDoneSeen = part["type"] == "refusal" && part["refusal"] == refusal
		}
	}
	if !deltaSeen || !refusalDoneSeen || !partDoneSeen {
		t.Fatalf("refusal lifecycle was incomplete: %v", types(payloads))
	}
	output := terminalOutput(t, payloads)
	content := output[0]["content"].([]any)
	if len(output) != 1 || len(content) != 1 || content[0].(map[string]any)["type"] != "refusal" || content[0].(map[string]any)["refusal"] != refusal {
		t.Fatalf("terminal refusal wire output was malformed: %+v", output)
	}
}

func TestChatStreamPreservesRoleOnlyEmptyAssistantOutput(t *testing.T) {
	emitter := NewStreamEmitter("chatcmpl_empty", "m", nil)
	finish := "stop"
	payloads := chatStream(t, emitter, nil, true, &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"}, FinishReason: &finish}}})
	want := []string{"response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.output_text.done", "response.content_part.done", "response.output_item.done", "response.completed"}
	if got := types(payloads); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("empty assistant lifecycle = %v, want %v", got, want)
	}
	output := terminalOutput(t, payloads)
	content := output[0]["content"].([]any)
	if len(output) != 1 || output[0]["role"] != "assistant" || len(content) != 1 || content[0].(map[string]any)["type"] != "output_text" || content[0].(map[string]any)["text"] != "" {
		t.Fatalf("empty assistant wire output was malformed: %+v", output)
	}
}

func TestChatStreamDoesNotAddEmptyMessageBeforeToolOnlyOutput(t *testing.T) {
	emitter := NewStreamEmitter("chatcmpl_tool_only", "m", nil)
	finish := "tool_calls"
	payloads := chatStream(t, emitter, nil, true,
		&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"}}}},
		&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{toolDelta(0, "call_0", "lookup", "{}")}}, FinishReason: &finish}}},
	)
	output := terminalOutput(t, payloads)
	if len(output) != 1 || output[0]["type"] != "function_call" {
		t.Fatalf("tool-only stream gained a spurious empty message: %+v", output)
	}
}

// A sealed Responses reasoning item goes back to a Responses client as it
// came; another dialect's seal is not something a Responses client can read.
func TestEmitterPassesOwnSealedReasoningThrough(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil)
	raw := `{"id":"rs_up","type":"reasoning","summary":[],"encrypted_content":"abc"}`
	payloads, err := emitter.Emit(
		textgen.Event{Kind: textgen.EventStart},
		textgen.Event{Kind: textgen.EventReasoningSealed, Sealed: []byte(raw), SealedBy: SealedBy},
		textgen.Event{Kind: textgen.EventReasoningSealed, Sealed: []byte(`{"type":"redacted_thinking","data":"x"}`), SealedBy: "anthropic"},
		textgen.Event{Kind: textgen.EventText, Text: "ok"},
		textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopEnd},
	)
	if err != nil {
		t.Fatal(err)
	}
	end, _ := emitter.Finish(nil)
	output := terminalOutput(t, append(payloads, end...))
	if len(output) != 2 || output[0]["id"] != "rs_up" || output[0]["encrypted_content"] != "abc" || output[1]["type"] != "message" {
		t.Fatalf("sealed item not passed through as it came, or a foreign seal leaked: %+v", output)
	}
}

type assertionError string

func (e assertionError) Error() string { return string(e) }

func stringPointer(value string) *string { return &value }

func toolDelta(index int, id, name, arguments string) dto.ToolCallResponse {
	call := dto.ToolCallResponse{ID: id, Type: "function", Function: dto.FunctionResponse{Name: name, Arguments: arguments}}
	call.Index = &index
	return call
}
