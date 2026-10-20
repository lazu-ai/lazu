package textgen

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestCollectorAssemblesPartsInOrder(t *testing.T) {
	var col Collector
	col.Add(
		Event{Kind: EventStart, ResponseID: "r1", Model: "m", CreatedAt: 7},
		Event{Kind: EventUsage, Usage: &dto.Usage{PromptTokens: 10}},
		Event{Kind: EventReasoningStart},
		Event{Kind: EventReasoning, Text: "th"},
		Event{Kind: EventReasoning, Text: "ink"},
		Event{Kind: EventReasoningSignature, Text: "sig", SealedBy: "anthropic"},
		Event{Kind: EventReasoningStart},
		Event{Kind: EventReasoning, Text: "again"},
		Event{Kind: EventText, Text: "hel"},
		Event{Kind: EventText, Text: "lo"},
		Event{Kind: EventToolStart, ToolIndex: 3},
		Event{Kind: EventToolArguments, ToolIndex: 3, ToolCallID: "call_3", ToolName: "lookup", Text: `{"x":`},
		Event{Kind: EventToolArguments, ToolIndex: 3, Text: `1}`},
		Event{Kind: EventToolDone, ToolIndex: 3, Text: `{"x":1}`},
		Event{Kind: EventStop, Stop: StopEnd},
		Event{Kind: EventUsage, Usage: &dto.Usage{CompletionTokens: 4}},
	)
	result := col.Result()
	if result.ResponseID != "r1" || result.Model != "m" || result.CreatedAt != 7 {
		t.Fatalf("start lost: %+v", result)
	}
	want := []Part{
		{Kind: PartReasoning, Text: "think", Signature: "sig", SealedBy: "anthropic"},
		{Kind: PartReasoning, Text: "again"},
		{Kind: PartText, Text: "hello"},
		{Kind: PartTool, ToolIndex: 3, CallID: "call_3", Name: "lookup", Text: `{"x":1}`},
	}
	if len(result.Parts) != len(want) {
		t.Fatalf("parts = %+v", result.Parts)
	}
	for i, part := range want {
		got := result.Parts[i]
		if got.Kind != part.Kind || got.Text != part.Text || got.Signature != part.Signature || got.CallID != part.CallID || got.Name != part.Name {
			t.Fatalf("part %d = %+v, want %+v", i, got, part)
		}
	}
	if result.Stop != StopTool {
		t.Fatalf("a reply with a tool call that said end stops as tool, got %q", result.Stop)
	}
	if result.Usage == nil || result.Usage.PromptTokens != 10 || result.Usage.CompletionTokens != 4 || result.Usage.TotalTokens != 14 {
		t.Fatalf("usage not merged: %+v", result.Usage)
	}
}

func TestCollectorToolDoneDoesNotDuplicateStreamedArguments(t *testing.T) {
	var col Collector
	col.Add(Event{Kind: EventToolStart, ToolIndex: 0, ToolCallID: "c", ToolName: "f"}, Event{Kind: EventToolArguments, ToolIndex: 0, Text: `{}`}, Event{Kind: EventToolDone, ToolIndex: 0, Text: `{}`})
	if got := col.Result().Parts[0].Text; got != `{}` {
		t.Fatalf("arguments = %q", got)
	}
	var fresh Collector
	fresh.Add(Event{Kind: EventToolStart, ToolIndex: 0}, Event{Kind: EventToolDone, ToolIndex: 0, Text: `{"a":1}`})
	if got := fresh.Result().Parts[0].Text; got != `{"a":1}` {
		t.Fatalf("done without deltas must supply the arguments, got %q", got)
	}
}
