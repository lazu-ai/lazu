package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	geminicodec "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/gemini"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

// The Gemini codec's whole-reply render is held to the legacy
// Chat-to-Gemini reply converter (ResponseOpenAI2Gemini) on the replies it
// renders right, until the legacy converter is deleted. A function call's
// id, which the legacy converter dropped, is left out of the comparison.
var geminiRenderCases = map[string]string{
	"text":   `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"Hello."},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
	"length": `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"Hel"},"finish_reason":"length"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`,
	"tool":   `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":9,"total_tokens":14}}`,
}

func TestGeminiRenderMatchesTheLegacyConverter(t *testing.T) {
	for name, body := range geminiRenderCases {
		t.Run(name, func(t *testing.T) {
			var chat dto.OpenAITextResponse
			if err := json.Unmarshal([]byte(body), &chat); err != nil {
				t.Fatal(err)
			}
			legacy := ResponseOpenAI2Gemini(&chat, &relaycommon.RelayInfo{})
			var col textgen.Collector
			col.Add(openai_chat.DecodeCompletion(&chat)...)
			got, want := normaliseGeminiReply(t, geminicodec.Render(col.Result())), normaliseGeminiReply(t, legacy)
			if !reflect.DeepEqual(got, want) {
				g, _ := json.MarshalIndent(got, "", "  ")
				w, _ := json.MarshalIndent(want, "", "  ")
				t.Fatalf("render differs from the legacy converter\n got: %s\nwant: %s", g, w)
			}
		})
	}
}

func normaliseGeminiReply(t *testing.T, response *dto.GeminiChatResponse) map[string]any {
	t.Helper()
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range out["candidates"].([]any) {
		content := candidate.(map[string]any)["content"].(map[string]any)
		for _, part := range content["parts"].([]any) {
			if call, ok := part.(map[string]any)["functionCall"].(map[string]any); ok {
				delete(call, "id")
			}
		}
	}
	return out
}
