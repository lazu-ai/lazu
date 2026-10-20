package openai_responses

import (
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestDecoderKeepsUsageOfAFailedResponse(t *testing.T) {
	decoder := &StreamDecoder{}
	events, err := decoder.Decode(`{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_error","message":"boom"},"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != textgen.EventUsage || events[0].Usage.CompletionTokens != 4 || events[1].Kind != textgen.EventError {
		t.Fatalf("want usage then error, got %+v", events)
	}
}

func TestEmitterCountsSealedReasoningAgainstTheRetentionLimit(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil, 64)
	sealed := `{"type":"reasoning","id":"rs_1","encrypted_content":"` + strings.Repeat("x", 128) + `"}`
	if _, err := emitter.Emit(textgen.Event{Kind: textgen.EventReasoningSealed, Sealed: []byte(sealed), SealedBy: SealedBy}); err == nil {
		t.Fatal("a sealed item larger than the retention limit must fail the stream")
	}
}

// Partial usage reports add up: input on the start, output at the end.
func TestEmitterMergesPartialUsage(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil)
	input := &dto.Usage{PromptTokens: 20}
	input.PromptTokensDetails.CachedTokens = 80
	if _, err := emitter.Emit(textgen.Event{Kind: textgen.EventUsage, Usage: input}, textgen.Event{Kind: textgen.EventText, Text: "hi"}, textgen.Event{Kind: textgen.EventUsage, Usage: &dto.Usage{CompletionTokens: 5}}, textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopEnd}); err != nil {
		t.Fatal(err)
	}
	end, err := emitter.Finish(nil)
	if err != nil {
		t.Fatal(err)
	}
	usage := end[len(end)-1]["response"].(map[string]any)["usage"].(*dto.ResponsesUsage)
	if usage.InputTokens != 20 || usage.OutputTokens != 5 || usage.InputTokensDetails.CachedTokens != 80 {
		t.Fatalf("usage: %+v", usage)
	}
}

// A call announced before its name is known would go out nameless; it is
// announced once named, with the arguments held so far.
func TestEmitterAnnouncesACallOnceItIsNamed(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil)
	payloads, err := emitter.Emit(
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 0},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 0, Text: `{"x":`},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 0, ToolCallID: "call_a", ToolName: "a", Text: `1}`},
	)
	if err != nil {
		t.Fatal(err)
	}
	var added map[string]any
	args := ""
	for _, payload := range payloads {
		switch payload["type"] {
		case "response.output_item.added":
			added = payload["item"].(map[string]any)
		case "response.function_call_arguments.delta":
			args += payload["delta"].(string)
		}
	}
	if added == nil || added["name"] != "a" || added["call_id"] != "call_a" || args != `{"x":1}` {
		t.Fatalf("added %v args %q", added, args)
	}
}

// A reasoning summary stays a summary from Responses to Responses.
func TestSummaryDeltasStaySummaries(t *testing.T) {
	decoder := &StreamDecoder{}
	emitter := NewStreamEmitter("id", "m", nil)
	var types []string
	for _, data := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1"}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"Thought about it."}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}`,
	} {
		events, err := decoder.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		payloads, err := emitter.Emit(events...)
		if err != nil {
			t.Fatal(err)
		}
		for _, payload := range payloads {
			types = append(types, payload["type"].(string))
		}
	}
	end, err := emitter.Finish(nil)
	if err != nil {
		t.Fatal(err)
	}
	item := end[len(end)-1]["response"].(map[string]any)["output"].([]any)[0].(dto.ResponsesReasoningItem)
	if len(item.Summary) != 1 || item.Summary[0].Text != "Thought about it." || len(item.Content) != 0 {
		t.Fatalf("reasoning item: %+v (events %v)", item, types)
	}
}

func TestDecoderKeepsAnEmbeddedFailureTypeAndAnEmptyMessageTurn(t *testing.T) {
	decoder := &StreamDecoder{}
	events, err := decoder.Decode(`{"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":"overloaded","message":"busy"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if last := events[len(events)-1]; last.Kind != textgen.EventError || last.Error.Type != "server_error" {
		t.Fatalf("failure type lost: %+v", last.Error)
	}
	events, err = (&StreamDecoder{}).Decode(`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`)
	if err != nil || len(events) != 1 || events[0].Kind != textgen.EventTurn {
		t.Fatalf("an empty message item is still a turn: %+v %v", events, err)
	}
}

func TestDecodeResponseKeepsReasoning(t *testing.T) {
	response := &dto.OpenAIResponsesResponse{Status: "completed", Output: []dto.ResponsesOutput{
		{Type: "reasoning", ID: "rs_1", Content: []dto.ResponsesOutputContent{{Type: "reasoning_text", Text: "thinking"}}},
		{Type: "message", Role: "assistant", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "hi"}}},
	}}
	var col textgen.Collector
	col.Add(DecodeResponse(response)...)
	parts := col.Result().Parts
	if len(parts) != 2 || parts[0].Kind != textgen.PartReasoning || parts[0].Text != "thinking" {
		t.Fatalf("parts: %+v", parts)
	}
}

// A done-only call the provider did not finish is held for the terminal
// event: kept when the reply hit its output cap, dropped otherwise.
func TestDecoderHoldsUnfinishedCallsForTheTerminal(t *testing.T) {
	for _, tc := range []struct {
		reason string
		keep   bool
	}{{"max_output_tokens", true}, {"content_filter", false}} {
		d := &StreamDecoder{}
		events, err := d.Decode(`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","status":"incomplete","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"q\":"}}`)
		if err != nil || len(events) != 0 {
			t.Fatalf("%s: an unfinished call must be held: %+v %v", tc.reason, events, err)
		}
		events, _ = d.Decode(`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"` + tc.reason + `"}}}`)
		calls := 0
		for _, event := range events {
			if event.Kind == textgen.EventToolStart {
				calls++
			}
		}
		if (calls == 1) != tc.keep {
			t.Fatalf("%s: events %+v", tc.reason, events)
		}
	}
}

// Every dimension the provider reported survives, since prices differ by
// dimension.
func TestUsageOfKeepsEveryDimension(t *testing.T) {
	images := 2
	usage := UsageOf(&dto.Usage{
		InputTokens: 100, OutputTokens: 40, GeneratedImages: &images,
		InputTokensDetails:  &dto.InputTokenDetails{CachedTokens: 10, ImageTokens: 30, CachedCreation1hTokens: 5, CacheMissTokens: 7},
		OutputTokensDetails: &dto.OutputTokenDetails{ReasoningTokens: 8, ImageTokens: 20},
	})
	if usage.PromptTokens != 100 || usage.CompletionTokens != 40 || usage.GeneratedImages == nil || *usage.GeneratedImages != 2 {
		t.Fatalf("usage: %+v", usage)
	}
	in, out := usage.PromptTokensDetails, usage.CompletionTokenDetails
	if in.CachedTokens != 10 || in.ImageTokens != 30 || in.CachedCreation1hTokens != 5 || in.CacheMissTokens != 7 || out.ReasoningTokens != 8 || out.ImageTokens != 20 {
		t.Fatalf("details: %+v %+v", in, out)
	}
	merged := textgen.MergeUsage(&dto.Usage{PromptTokens: 100}, usage)
	if merged.PromptTokensDetails.ImageTokens != 30 || merged.CompletionTokenDetails.ImageTokens != 20 || merged.GeneratedImages == nil {
		t.Fatalf("merge lost a dimension: %+v", merged)
	}
}

// An upstream's empty message beside a tool call is kept, in its place.
func TestEmptyMessageBesideAToolCallIsKept(t *testing.T) {
	decoder := &StreamDecoder{}
	emitter := NewStreamEmitter("id", "m", nil)
	for _, data := range []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_up","role":"assistant","content":[]}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"f"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"f","arguments":"{}","status":"completed"}}`,
		`{"type":"response.completed","response":{"status":"completed"}}`,
	} {
		events, err := decoder.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := emitter.Emit(events...); err != nil {
			t.Fatal(err)
		}
	}
	final, err := emitter.Finish(nil)
	if err != nil {
		t.Fatal(err)
	}
	response := final[len(final)-1]["response"].(map[string]any)
	output := response["output"].([]any)
	if len(output) != 2 || output[0].(map[string]any)["type"] != "message" || output[1].(map[string]any)["type"] != "function_call" {
		t.Fatalf("output: %+v", output)
	}
}

// Anthropic usage leaves the cache out of input_tokens; a Responses client
// reads input as including it.
func TestResponsesUsageCountsTheCacheAsInput(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil)
	usage := &dto.Usage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 25, PromptExcludesCache: true}
	usage.PromptTokensDetails.CachedTokens = 80
	if _, err := emitter.Emit(textgen.Event{Kind: textgen.EventText, Text: "hi"}, textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopEnd}, textgen.Event{Kind: textgen.EventUsage, Usage: usage}); err != nil {
		t.Fatal(err)
	}
	final, err := emitter.Finish(nil)
	if err != nil {
		t.Fatal(err)
	}
	projected := final[len(final)-1]["response"].(map[string]any)["usage"].(*dto.ResponsesUsage)
	if projected.InputTokens != 100 || projected.TotalTokens != 105 {
		t.Fatalf("usage: %+v", projected)
	}
}
