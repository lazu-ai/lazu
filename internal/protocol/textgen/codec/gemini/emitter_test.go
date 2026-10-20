package gemini

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
)

// A Chat upstream streams a call's arguments in fragments; a Gemini client
// gets the call once, whole, as Gemini sends one.
func TestEmitterWritesARecordedChatToolCallWhole(t *testing.T) {
	decoder := &openai_chat.StreamDecoder{}
	emitter := NewStreamEmitter()
	var chunks []*dto.GeminiChatResponse
	for _, data := range fixtures.Data(t, "deepseek/chat_tool_call.sse") {
		if data == "[DONE]" {
			continue
		}
		var chunk dto.ChatCompletionsStreamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, emitter.Emit(decoder.Decode(&chunk)...)...)
	}
	end, err := emitter.Finish(nil)
	if err != nil {
		t.Fatal(err)
	}
	chunks = append(chunks, end...)
	calls := 0
	for _, chunk := range chunks {
		for _, part := range chunk.Candidates[0].Content.Parts {
			if part.FunctionCall != nil {
				calls++
				args, _ := part.FunctionCall.Arguments.(map[string]any)
				if part.FunctionCall.FunctionName != "get_weather" || args["city"] != "Paris" {
					t.Fatalf("call: %+v", part.FunctionCall)
				}
			}
		}
	}
	last := chunks[len(chunks)-1]
	if calls != 1 || last.Candidates[0].FinishReason == nil || *last.Candidates[0].FinishReason != "STOP" || last.UsageMetadata.PromptTokenCount == 0 {
		t.Fatalf("calls %d, last chunk %+v", calls, last)
	}
}

// Gemini to Gemini keeps every thought signature where Gemini put it.
func TestEmitterRoundTripsRecordedSignatures(t *testing.T) {
	for _, name := range []string{"gemini/text.sse", "gemini/tool_call.sse"} {
		decoder := &StreamDecoder{}
		emitter := NewStreamEmitter()
		in, out := 0, 0
		for _, data := range fixtures.Data(t, name) {
			var response dto.GeminiChatResponse
			_ = json.Unmarshal([]byte(data), &response)
			for _, part := range response.Candidates[0].Content.Parts {
				if part.ThoughtSignature != "" {
					in++
				}
			}
			events, err := decoder.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			for _, chunk := range emitter.Emit(events...) {
				for _, part := range chunk.Candidates[0].Content.Parts {
					if part.ThoughtSignature != "" {
						out++
					}
				}
			}
		}
		end, err := emitter.Finish(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, part := range end[0].Candidates[0].Content.Parts {
			if part.ThoughtSignature != "" {
				out++
			}
		}
		if in == 0 || in != out {
			t.Fatalf("%s: %d signatures in, %d out", name, in, out)
		}
	}
}

func TestRenderKeepsTextBesideACallAndCountsThoughtsApart(t *testing.T) {
	var col textgen.Collector
	usage := &dto.Usage{PromptTokens: 10, CompletionTokens: 30}
	usage.CompletionTokenDetails.ReasoningTokens = 12
	col.Add(
		textgen.Event{Kind: textgen.EventText, Text: "Checking."},
		textgen.Event{Kind: textgen.EventToolStart, ToolIndex: 0, ToolCallID: "call_1", ToolName: "get_weather", Text: "sig", SealedBy: SealedBy},
		textgen.Event{Kind: textgen.EventToolDone, ToolIndex: 0, Text: `{"city":"Paris"}`},
		textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopTool},
		textgen.Event{Kind: textgen.EventUsage, Usage: usage},
	)
	response := Render(col.Result())
	parts := response.Candidates[0].Content.Parts
	if len(parts) != 2 || parts[0].Text != "Checking." || parts[1].FunctionCall == nil || parts[1].ThoughtSignature != "sig" {
		t.Fatalf("parts: %+v", parts)
	}
	if response.UsageMetadata.CandidatesTokenCount != 18 || response.UsageMetadata.ThoughtsTokenCount != 12 || response.UsageMetadata.TotalTokenCount != 40 {
		t.Fatalf("usage: %+v", response.UsageMetadata)
	}
}
