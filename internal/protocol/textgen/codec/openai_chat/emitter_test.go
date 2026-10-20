package openai_chat

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestEmitterRoleOnceToolIndexesAndFinish(t *testing.T) {
	emitter := NewStreamEmitter("resp_abc", "m")
	chunks := emitter.Emit(
		textgen.Event{Kind: textgen.EventStart, ResponseID: "resp_abc", Model: "upstream-model", CreatedAt: 5},
		textgen.Event{Kind: textgen.EventReasoning, Text: "think"},
		textgen.Event{Kind: textgen.EventText, Text: "hi"},
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 7, ToolCallID: "call_a", ToolName: "a"},
		textgen.Event{Kind: textgen.EventToolArguments, ToolIndex: 7, Text: `{}`},
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 2, ToolCallID: "call_b", ToolName: "b"},
		textgen.Event{Kind: textgen.EventToolDone, ToolIndex: 2, Text: `{"k":1}`},
		textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopTool},
	)
	chunks = append(chunks, mustFinish(emitter.Finish(&dto.Usage{PromptTokens: 1, CompletionTokens: 2}))...)
	var usageChunk *dto.ChatCompletionsStreamResponse
	if n := len(chunks); n > 0 && IsUsageChunk(chunks[n-1]) {
		usageChunk, chunks = chunks[n-1], chunks[:n-1]
	}
	_ = usageChunk
	if chunks[0].Choices[0].Delta.Role != "assistant" || chunks[0].Id != "chatcmpl-abc" || chunks[0].Model != "upstream-model" || chunks[0].Created != 5 {
		t.Fatalf("role chunk: %+v", chunks[0])
	}
	roles := 0
	indexes := map[string]int{}
	args := map[string]string{}
	for _, chunk := range chunks {
		if chunk.Choices[0].Delta.Role != "" {
			roles++
		}
		for _, call := range chunk.Choices[0].Delta.ToolCalls {
			if call.ID != "" {
				indexes[call.ID] = *call.Index
			}
			if call.Index != nil {
				args[map[int]string{0: "a", 1: "b"}[*call.Index]] += call.Function.Arguments
			}
		}
	}
	if roles != 1 || indexes["call_a"] != 0 || indexes["call_b"] != 1 || args["a"] != `{}` || args["b"] != `{"k":1}` {
		t.Fatalf("roles=%d indexes=%v args=%v", roles, indexes, args)
	}
	last := chunks[len(chunks)-1]
	if last.Choices[0].FinishReason == nil || *last.Choices[0].FinishReason != "tool_calls" || usageChunk == nil || usageChunk.Usage.TotalTokens != 3 {
		t.Fatalf("finish chunk: %+v", last)
	}
	if again, _ := emitter.Finish(nil); len(again) != 0 {
		t.Fatal("finish must be terminal once")
	}
}

func TestEmitterErrorIsTerminal(t *testing.T) {
	emitter := NewStreamEmitter("", "m")
	chunks := emitter.Emit(textgen.Event{Kind: textgen.EventText, Text: "part"}, textgen.Event{Kind: textgen.EventError, Error: &textgen.SemanticError{Type: "api_error", Code: "x", Message: "boom"}})
	last := chunks[len(chunks)-1]
	if last.Error == nil || len(last.Choices) != 0 {
		t.Fatalf("expected an error chunk, got %+v", last)
	}
	if more := emitter.Emit(textgen.Event{Kind: textgen.EventText, Text: "late"}); len(more) != 0 {
		t.Fatal("nothing after the error")
	}
}

func TestRenderWholeCompletion(t *testing.T) {
	var col textgen.Collector
	col.Add(
		textgen.Event{Kind: textgen.EventStart, ResponseID: "msg_1", Model: "m", CreatedAt: 9},
		textgen.Event{Kind: textgen.EventReasoning, Text: "why"},
		textgen.Event{Kind: textgen.EventText, Text: "answer"},
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 0, ToolCallID: "c1", ToolName: "f"},
		textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopTool},
		textgen.Event{Kind: textgen.EventUsage, Usage: &dto.Usage{PromptTokens: 3, CompletionTokens: 4}},
	)
	response := Render(col.Result(), "fallback")
	choice := response.Choices[0]
	if response.Id != "chatcmpl-1" || response.Model != "m" || choice.FinishReason != "tool_calls" || choice.Message.StringContent() != "answer" || choice.Message.ReasoningContent != "why" {
		t.Fatalf("rendered: %+v", response)
	}
	calls := choice.Message.ParseToolCalls()
	if len(calls) != 1 || calls[0].ID != "c1" || calls[0].Function.Arguments != "{}" {
		t.Fatalf("tool calls: %+v", calls)
	}
	if response.Usage.TotalTokens != 7 {
		t.Fatalf("usage: %+v", response.Usage)
	}
}

// mustFinish is an emitter's Finish for a stream known to have ended well.
func mustFinish[T any](out []T, err error) []T {
	if err != nil {
		panic(err)
	}
	return out
}
