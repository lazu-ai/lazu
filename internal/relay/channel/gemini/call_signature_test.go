package gemini

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/codec/anthropic_messages"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

// A Claude client's second tool step: the tool_use it sends back has no
// Gemini thought signature, because the Messages protocol has nowhere to put
// one. The earlier turn's call is left alone; only the running turn needs one.
const claudeToolLoop = `{
  "model": "m",
  "max_tokens": 64,
  "tools": [{"name": "get_weather", "input_schema": {"type": "object"}}],
  "messages": [
    {"role": "user", "content": "weather in Paris?"},
    {"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": {"city": "Paris"}}]},
    {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "sunny"}]},
    {"role": "assistant", "content": "It is sunny."},
    {"role": "user", "content": "and in Rome and Oslo?"},
    {"role": "assistant", "content": [
      {"type": "tool_use", "id": "toolu_2", "name": "get_weather", "input": {"city": "Rome"}},
      {"type": "tool_use", "id": "toolu_3", "name": "get_weather", "input": {"city": "Oslo"}}
    ]},
    {"role": "user", "content": [
      {"type": "tool_result", "tool_use_id": "toolu_2", "content": "rain"},
      {"type": "tool_result", "tool_use_id": "toolu_3", "content": "snow"}
    ]}
  ]
}`

func encodeClaudeToolLoop(t *testing.T, upstream string) *dto.GeminiChatRequest {
	t.Helper()
	request, _, err := anthropic_messages.Decode([]byte(claudeToolLoop))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: upstream}}
	out, err := encodeBridgedRequest(info, request, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return out
}

func callSignatures(out *dto.GeminiChatRequest) map[string]string {
	signatures := map[string]string{}
	for _, content := range out.Contents {
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				signatures[part.FunctionCall.ID] = part.ThoughtSignature
			}
		}
	}
	return signatures
}

func TestRunningTurnCallsGetThePlaceholderSignatureOnGemini3(t *testing.T) {
	got := callSignatures(encodeClaudeToolLoop(t, "gemini-3.1-pro-preview"))
	want := map[string]string{
		"toolu_1": "",                     // a finished turn: not checked
		"toolu_2": skipSignatureValidator, // the step's first call
		"toolu_3": "",                     // parallel calls carry none
	}
	for id, signature := range want {
		if got[id] != signature {
			t.Fatalf("%s: signature %q, want %q (all: %v)", id, got[id], signature, got)
		}
	}
}

func TestPlaceholderSignatureOnlyWhereTheModelEnforcesThem(t *testing.T) {
	for _, upstream := range []string{"gemini-2.5-pro", "gemma-3-27b-it"} {
		for id, signature := range callSignatures(encodeClaudeToolLoop(t, upstream)) {
			if signature != "" {
				t.Fatalf("%s: %s got signature %q", upstream, id, signature)
			}
		}
	}
}

func TestPlaceholderNeverReplacesARealSignature(t *testing.T) {
	out := &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{
		{Role: "user", Parts: []dto.GeminiPart{{Text: "go"}}},
		{Role: "model", Parts: []dto.GeminiPart{{FunctionCall: &dto.FunctionCall{ID: "a", FunctionName: "f"}, ThoughtSignature: "real"}}},
		{Role: "user", Parts: []dto.GeminiPart{{FunctionResponse: &dto.GeminiFunctionResponse{ID: "a", Name: "f"}}}},
		{Role: "model", Parts: []dto.GeminiPart{{Text: "thinking aloud"}, {FunctionCall: &dto.FunctionCall{ID: "b", FunctionName: "f"}}}},
	}}
	fillMissingCallSignatures(out)
	if got := callSignatures(out); got["a"] != "real" || got["b"] != skipSignatureValidator {
		t.Fatalf("signatures %v", got)
	}
}
