package openai_responses

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestChatStreamBridgeEmitsCompleteOrderedResponsesLifecycle(t *testing.T) {
	bridge := NewChatStreamBridge("chatcmpl_test", "m")
	finish := "tool_calls"
	chunks := []*dto.ChatCompletionsStreamResponse{
		{Id: "chatcmpl_test", Created: 123, Model: "m", Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: stringPointer("hel")}}}},
		{Id: "chatcmpl_test", Created: 123, Model: "m", Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: stringPointer("lo"), ToolCalls: []dto.ToolCallResponse{toolDelta(3, "call_3", "lookup", "{\"x\":")}}}}},
		{Id: "chatcmpl_test", Created: 123, Model: "m", Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{toolDelta(3, "", "", "1}")}}, FinishReason: &finish}}},
	}
	var events []textgen.Event
	for _, chunk := range chunks {
		got, err := bridge.Consume(chunk)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, got...)
	}
	terminal, err := bridge.Finalize(&dto.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5})
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, terminal...)
	for index, event := range events {
		if event.Sequence != uint64(index) {
			t.Fatalf("sequence[%d]=%d", index, event.Sequence)
		}
	}
	last := events[len(events)-1]
	if last.Kind != textgen.EventResponseCompleted || last.CreatedAt != 123 || len(last.Output) != 2 {
		t.Fatalf("unexpected terminal event: %+v", last)
	}
	if last.Output[0].Text != "hello" || last.Output[1].Arguments != `{"x":1}` || last.Output[1].CallID != "call_3" {
		t.Fatalf("terminal output incomplete: %+v", last.Output)
	}
	textDoneIndex, contentDoneIndex, argumentsDoneIndex, toolDoneIndex := -1, -1, -1, -1
	for index, event := range events {
		wireType, _ := EmitEvent(event)["type"].(string)
		switch wireType {
		case "response.output_text.done":
			textDoneIndex = index
		case "response.content_part.done":
			if event.Delta.Kind == textgen.DeltaText {
				contentDoneIndex = index
			}
		case "response.function_call_arguments.done":
			argumentsDoneIndex = index
		case "response.output_item.done":
			if event.Delta.Kind == textgen.DeltaToolArguments {
				toolDoneIndex = index
			}
		}
	}
	if textDoneIndex < 0 || textDoneIndex >= contentDoneIndex || argumentsDoneIndex < 0 || argumentsDoneIndex >= toolDoneIndex {
		t.Fatalf("protocol-specific done events are missing or out of order: %+v", events)
	}
	wire := EmitEvent(last)
	response := wire["response"].(map[string]any)
	if response["created_at"] != int64(123) || len(response["output"].([]any)) != 2 {
		t.Fatalf("terminal wire envelope incomplete: %#v", response)
	}
	usage := response["usage"].(*dto.ResponsesUsage)
	if usage.InputTokens != 2 || usage.OutputTokens != 3 {
		t.Fatalf("terminal usage was not normalized to Responses fields: %+v", usage)
	}
}

func TestChatStreamBridgePreservesReasoningLifecycle(t *testing.T) {
	bridge := NewChatStreamBridge("chatcmpl_reasoning", "m")
	reasoning := "think"
	answer := "done"
	finish := "stop"
	chunks := []*dto.ChatCompletionsStreamResponse{
		{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: &reasoning}}}},
		{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &answer}, FinishReason: &finish}}},
	}
	var events []textgen.Event
	for _, chunk := range chunks {
		got, err := bridge.Consume(chunk)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, got...)
	}
	terminal, err := bridge.Finalize(nil)
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, terminal...)
	if len(events) == 0 || events[len(events)-1].Output[0].Kind != textgen.InputReasoning || events[len(events)-1].Output[0].Text != reasoning {
		t.Fatalf("reasoning output was not retained in terminal response: %+v", events)
	}
	seen := map[string]bool{}
	for _, event := range events {
		if event.Delta.Kind == textgen.DeltaReasoning {
			if wireType, _ := EmitEvent(event)["type"].(string); wireType != "" {
				seen[wireType] = true
			}
		}
	}
	for _, want := range []string{"response.output_item.added", "response.content_part.added", "response.reasoning_text.delta", "response.reasoning_text.done", "response.content_part.done", "response.output_item.done"} {
		if !seen[want] {
			t.Fatalf("missing reasoning lifecycle event %q: %+v", want, events)
		}
	}
}

func TestChatStreamBridgeTerminalOutputPreservesAssignedOutputIndexes(t *testing.T) {
	bridge := NewChatStreamBridge("chatcmpl_tool_first", "m")
	toolFirst := &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{toolDelta(0, "call_0", "lookup", `{}`)}},
	}}}
	if _, err := bridge.Consume(toolFirst); err != nil {
		t.Fatal(err)
	}
	text := "after tool"
	finish := "stop"
	if _, err := bridge.Consume(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text}, FinishReason: &finish,
	}}}); err != nil {
		t.Fatal(err)
	}
	events, err := bridge.Finalize(nil)
	if err != nil {
		t.Fatal(err)
	}
	terminal := events[len(events)-1]
	if len(terminal.Output) != 2 || terminal.Output[0].Kind != textgen.InputFunctionCall || terminal.Output[1].Kind != textgen.InputMessage {
		t.Fatalf("terminal output disagreed with assigned output indexes: %+v", terminal.Output)
	}
	wire := EmitEvent(terminal)["response"].(map[string]any)["output"].([]any)
	if wire[0].(map[string]any)["type"] != "function_call" || wire[1].(map[string]any)["type"] != "message" {
		t.Fatalf("wire terminal output order changed: %#v", wire)
	}
}

func TestChatStreamBridgeFailureIsTerminalExactlyOnce(t *testing.T) {
	bridge := NewChatStreamBridge("id", "m")
	events := bridge.Fail(assertionError("boom"))
	if len(events) != 3 || events[2].Kind != textgen.EventResponseFailed {
		t.Fatalf("expected created, in_progress, failed: %+v", events)
	}
	if again := bridge.Fail(assertionError("again")); len(again) != 0 {
		t.Fatalf("terminal must be emitted once: %+v", again)
	}
}

func TestChatStreamBridgeRejectsRetainedOutputBeyondLimit(t *testing.T) {
	bridge := NewChatStreamBridge("chatcmpl_bounded", "m", 8)
	content := "123456789"
	_, err := bridge.Consume(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &content}}}})
	if err == nil {
		t.Fatal("expected retained output limit failure")
	}
	events := bridge.Fail(err)
	if len(events) == 0 || events[len(events)-1].Kind != textgen.EventResponseFailed {
		t.Fatalf("retained output overflow did not fail cleanly: %+v", events)
	}
	if again := bridge.Fail(err); len(again) != 0 {
		t.Fatalf("retained output overflow emitted more than one terminal: %+v", again)
	}
}

func TestChatStreamBridgeFailureClosesAndRetainsPartialItems(t *testing.T) {
	bridge := NewChatStreamBridge("chatcmpl_partial", "m")
	partial := "partial"
	events, err := bridge.Consume(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
			Content:   &partial,
			ToolCalls: []dto.ToolCallResponse{toolDelta(0, "call_0", "lookup", `{"x":`)},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	failed := bridge.Fail(assertionError("malformed chunk"), &dto.Usage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6})
	events = append(events, failed...)
	last := events[len(events)-1]
	if last.Kind != textgen.EventResponseFailed || len(last.Output) != 2 {
		t.Fatalf("failed envelope did not retain partial outputs: %+v", last)
	}
	if last.Output[0].Text != partial || last.Output[0].Status != textgen.StatusIncomplete {
		t.Fatalf("partial message was not retained as incomplete: %+v", last.Output[0])
	}
	if last.Output[1].Arguments != `{"x":` || last.Output[1].Status != textgen.StatusIncomplete {
		t.Fatalf("partial tool call was not retained as incomplete: %+v", last.Output[1])
	}

	wantDone := map[string]bool{
		"response.output_text.done":               false,
		"response.content_part.done":              false,
		"response.function_call_arguments.done":   false,
		"response.output_item.done:message":       false,
		"response.output_item.done:function_call": false,
	}
	for index, event := range events {
		if event.Sequence != uint64(index) {
			t.Fatalf("sequence[%d]=%d", index, event.Sequence)
		}
		wire := EmitEvent(event)
		typeName, _ := wire["type"].(string)
		switch typeName {
		case "response.output_text.done", "response.content_part.done", "response.function_call_arguments.done":
			wantDone[typeName] = true
		case "response.output_item.done":
			item := wire["item"].(map[string]any)
			wantDone[typeName+":"+item["type"].(string)] = true
		}
	}
	for eventType, seen := range wantDone {
		if !seen {
			t.Fatalf("missing %s before failed terminal: %+v", eventType, events)
		}
	}
	response := EmitEvent(last)["response"].(map[string]any)
	if output := response["output"].([]any); len(output) != 2 {
		t.Fatalf("failed wire envelope lost partial output: %#v", response)
	}
	usage := response["usage"].(*dto.ResponsesUsage)
	if usage.InputTokens != 4 || usage.OutputTokens != 2 || usage.TotalTokens != 6 {
		t.Fatalf("failed wire envelope lost settled usage: %+v", usage)
	}
}

func TestChatStreamEnvelopeCarriesSemanticRequestConfiguration(t *testing.T) {
	request := &textgen.Request{
		ToolPolicy: textgen.ToolPolicy{Mode: "auto", ParallelToolCalls: textgen.Some(false)},
		Sampling: textgen.SamplingOptions{
			Temperature: textgen.Some(0.25),
			TopP:        textgen.Some(0.9),
			MaxOutput:   textgen.Some(uint(99)),
		},
	}
	event := textgen.Event{Kind: textgen.EventResponseCompleted, ResponseID: "resp_config", Model: "m"}
	response := EmitEvent(event, request)["response"].(map[string]any)
	if response["temperature"] != 0.25 || response["top_p"] != 0.9 || response["max_output_tokens"] != uint(99) {
		t.Fatalf("stream response sampling configuration was lost: %#v", response)
	}
	if response["parallel_tool_calls"] != false || response["tool_choice"] != "auto" {
		t.Fatalf("stream response tool configuration was lost: %#v", response)
	}
}

func TestChatStreamEnvelopeUsesChatConfigurationDefaults(t *testing.T) {
	request := &textgen.Request{}
	response := EmitEvent(textgen.Event{Kind: textgen.EventResponseCompleted, ResponseID: "resp_defaults", Model: "m"}, request)["response"].(map[string]any)
	if response["temperature"] != float64(1) || response["top_p"] != float64(1) || response["parallel_tool_calls"] != true || response["truncation"] != "disabled" {
		t.Fatalf("stream response defaults were not represented: %#v", response)
	}
	if response["max_output_tokens"] != nil || response["tool_choice"] != "none" {
		t.Fatalf("omitted stream configuration was misrepresented: %#v", response)
	}
}

func TestChatStreamBridgeRejectsMissingTerminalFinishReason(t *testing.T) {
	bridge := NewChatStreamBridge("id", "m")
	partial := "partial"
	if _, err := bridge.Consume(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &partial}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.Finalize(nil); err == nil {
		t.Fatal("expected truncated stream without finish_reason to fail")
	}
	events := bridge.Fail(assertionError("truncated"))
	if len(events) == 0 || events[len(events)-1].Kind != textgen.EventResponseFailed {
		t.Fatalf("expected response.failed after truncated stream, got %+v", events)
	}
}

func TestChatStreamBridgeContentFilterIsIncomplete(t *testing.T) {
	bridge := NewChatStreamBridge("chatcmpl_filter", "m")
	finish := "content_filter"
	partial := "partial"
	if _, err := bridge.Consume(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &partial}, FinishReason: &finish}}}); err != nil {
		t.Fatal(err)
	}
	events, err := bridge.Finalize(nil)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Kind != textgen.EventResponseIncomplete || last.Status != textgen.StatusIncomplete || last.IncompleteReason != "content_filter" {
		t.Fatalf("content filter terminal state was misreported: %+v", last)
	}
	for _, event := range events {
		if event.Kind == textgen.EventItemDone {
			item := EmitEvent(event)["item"].(map[string]any)
			if item["status"] != "incomplete" {
				t.Fatalf("content-filtered item was misreported: %#v", item)
			}
		}
	}
}

func TestChatStreamBridgePreservesRefusalLifecycle(t *testing.T) {
	bridge := NewChatStreamBridge("chatcmpl_refusal", "m")
	finish := "stop"
	refusal := "not allowed"
	events, err := bridge.Consume(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Refusal: &refusal}, FinishReason: &finish}}})
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := bridge.Finalize(nil)
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, terminal...)
	var deltaSeen, refusalDoneSeen, partDoneSeen bool
	for _, event := range events {
		wire := EmitEvent(event)
		switch wire["type"] {
		case "response.refusal.delta":
			deltaSeen = wire["delta"] == refusal
		case "response.refusal.done":
			refusalDoneSeen = wire["refusal"] == refusal
		case "response.content_part.done":
			part, _ := wire["part"].(map[string]any)
			partDoneSeen = part["type"] == "refusal" && part["refusal"] == refusal
		}
	}
	if !deltaSeen || !refusalDoneSeen || !partDoneSeen {
		t.Fatalf("refusal lifecycle was incomplete: %+v", events)
	}
	last := events[len(events)-1]
	if len(last.Output) != 1 || last.Output[0].Refusal != refusal || last.Output[0].Text != "" {
		t.Fatalf("terminal refusal output was lost: %+v", last.Output)
	}
	response := wireObject(t, EmitEvent(last))["response"].(map[string]any)
	output := response["output"].([]any)
	message := output[0].(map[string]any)
	content := message["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["type"] != "refusal" {
		t.Fatalf("terminal refusal wire output was malformed: %#v", response)
	}
}

func TestChatStreamBridgePreservesRoleOnlyEmptyAssistantOutput(t *testing.T) {
	bridge := NewChatStreamBridge("chatcmpl_empty", "m")
	finish := "stop"
	events, err := bridge.Consume(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"}, FinishReason: &finish,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := bridge.Finalize(nil)
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, terminal...)
	wantTypes := []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("empty assistant lifecycle length = %d, want %d: %+v", len(events), len(wantTypes), events)
	}
	for index, event := range events {
		if got, _ := EmitEvent(event)["type"].(string); got != wantTypes[index] {
			t.Fatalf("event[%d] = %q, want %q: %+v", index, got, wantTypes[index], events)
		}
	}
	last := events[len(events)-1]
	if len(last.Output) != 1 || last.Output[0].Role != "assistant" || last.Output[0].Text != "" {
		t.Fatalf("empty assistant terminal output was lost: %+v", last.Output)
	}
	response := wireObject(t, EmitEvent(last))["response"].(map[string]any)
	output := response["output"].([]any)
	content := output[0].(map[string]any)["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["type"] != "output_text" || content[0].(map[string]any)["text"] != "" {
		t.Fatalf("empty assistant wire output was malformed: %#v", response)
	}
}

func TestChatStreamBridgeDoesNotAddEmptyMessageBeforeToolOnlyOutput(t *testing.T) {
	bridge := NewChatStreamBridge("chatcmpl_tool_only", "m")
	finish := "tool_calls"
	_, err := bridge.Consume(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bridge.Consume(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{toolDelta(0, "call_0", "lookup", "{}")}}, FinishReason: &finish,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	events, err := bridge.Finalize(nil)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if len(last.Output) != 1 || last.Output[0].Kind != textgen.InputFunctionCall {
		t.Fatalf("tool-only stream gained a spurious empty message: %+v", last.Output)
	}
}

type assertionError string

func (e assertionError) Error() string { return string(e) }

func stringPointer(value string) *string { return &value }

func toolDelta(index int, id, name, arguments string) dto.ToolCallResponse {
	call := dto.ToolCallResponse{ID: id, Type: "function", Function: dto.FunctionResponse{Name: name, Arguments: arguments}}
	call.SetIndex(index)
	return call
}
