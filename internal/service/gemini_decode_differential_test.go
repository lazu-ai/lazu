package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	geminicodec "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/gemini"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

// The Gemini codec's request decoder, encoded for Chat, is held to the
// legacy Gemini-to-Chat converter (GeminiToOpenAIRequest) on the requests
// it converts right, until the legacy converter is deleted. Stop sequences
// are left out: the legacy converter slices them to four and panics on
// fewer.
var geminiDecodeCases = map[string]string{
	"text":       `{"systemInstruction":{"parts":[{"text":"Be terse."}]},"contents":[{"role":"user","parts":[{"text":"Hi"}]},{"role":"model","parts":[{"text":"Hello."}]},{"role":"user","parts":[{"text":"Again?"}]}],"generationConfig":{"temperature":0.3,"topP":0.9,"topK":20,"maxOutputTokens":200,"candidateCount":1}}`,
	"tool_round": `{"contents":[{"role":"user","parts":[{"text":"Weather in Paris?"}]},{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"Paris"}}}]},{"role":"user","parts":[{"functionResponse":{"name":"get_weather","response":{"temp":21}}}]}],"tools":[{"functionDeclarations":[{"name":"get_weather","description":"Weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}]}]}`,
	"image":      `{"contents":[{"role":"user","parts":[{"text":"What is this?"},{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgo="}}]}]}`,
}

func TestGeminiDecodeMatchesTheLegacyConverter(t *testing.T) {
	for name, body := range geminiDecodeCases {
		t.Run(name, func(t *testing.T) {
			var wire dto.GeminiChatRequest
			if err := json.Unmarshal([]byte(body), &wire); err != nil {
				t.Fatal(err)
			}
			legacy, err := GeminiToOpenAIRequest(&wire, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "m"}})
			if err != nil {
				t.Fatal(err)
			}
			request, _, err := geminicodec.Decode([]byte(body), "m")
			if err != nil {
				t.Fatal(err)
			}
			request.Model.Upstream = "m"
			chat, err := openai_chat.Encode(request)
			if err != nil {
				t.Fatal(err)
			}
			got, want := normaliseChat(t, chat), normaliseChat(t, legacy)
			for _, message := range append(got["messages"].([]any), want["messages"].([]any)...) {
				// the legacy converter wrote image detail "auto" and the mime type
				if parts, ok := message.(map[string]any)["content"].([]any); ok {
					for _, part := range parts {
						if image, ok := part.(map[string]any)["image_url"].(map[string]any); ok {
							delete(image, "detail")
							delete(image, "MimeType")
						}
					}
				}
			}
			if !reflect.DeepEqual(got, want) {
				g, _ := json.MarshalIndent(got, "", "  ")
				w, _ := json.MarshalIndent(want, "", "  ")
				t.Fatalf("codec differs from the legacy converter\n got: %s\nwant: %s", g, w)
			}
		})
	}
}
