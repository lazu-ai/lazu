package textgen

import (
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

// The ending keeps how a reply ended, never its content.
func TestEndingKeepsNoContent(t *testing.T) {
	var ending Ending
	chunk := strings.Repeat("x", 1<<10)
	for i := 0; i < 1000; i++ {
		ending.Add(Event{Kind: EventText, Text: chunk})
	}
	ending.Add(
		Event{Kind: EventToolStart, ToolIndex: 0},
		Event{Kind: EventToolArguments, ToolIndex: 0, Text: `{"a":1}`},
		Event{Kind: EventStop, Stop: StopEnd},
		Event{Kind: EventUsage, Usage: &dto.Usage{PromptTokens: 3, CompletionTokens: 4}},
	)
	result := ending.Result()
	if len(result.Parts) != 0 || result.Stop != StopTool || !result.StopSeen || result.Usage.CompletionTokens != 4 || !ending.ArgsSeen(0) || ending.ArgsSeen(1) {
		t.Fatalf("ending: %+v", result)
	}
}
