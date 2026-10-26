package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	geminicodec "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/gemini"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
)

// The protocol hub's Gemini-client path (gemini.Decode, then
// openai_chat.Encode) is held to golden files under testdata/gemini_to_chat:
// the legacy Gemini-to-Chat converter's output on the requests it got right,
// saved before it was deleted (AGENTS.md, Protocol Layer 7). gemini_cli is a
// recorded Gemini CLI request (testdata/client/gemini_cli/tool_turn.json); its
// golden is the hub's output, which differs from the legacy converter's on
// purpose in two places: each tool keeps the schema the client sent as
// parametersJsonSchema (the legacy converter sent the tools without
// parameters), and thinkingBudget is carried as reasoning_effort.
// Stop sequences are left out: the legacy converter sliced them to four and
// panicked on fewer.
var geminiDecodeCases = map[string]string{
	"text":       `{"systemInstruction":{"parts":[{"text":"Be terse."}]},"contents":[{"role":"user","parts":[{"text":"Hi"}]},{"role":"model","parts":[{"text":"Hello."}]},{"role":"user","parts":[{"text":"Again?"}]}],"generationConfig":{"temperature":0.3,"topP":0.9,"topK":20,"maxOutputTokens":200,"candidateCount":1}}`,
	"tool_round": `{"contents":[{"role":"user","parts":[{"text":"Weather in Paris?"}]},{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"Paris"}}}]},{"role":"user","parts":[{"functionResponse":{"name":"get_weather","response":{"temp":21}}}]}],"tools":[{"functionDeclarations":[{"name":"get_weather","description":"Weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}]}]}`,
	"image":      `{"contents":[{"role":"user","parts":[{"text":"What is this?"},{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgo="}}]}]}`,
}

func TestGeminiToChatMatchesTheGoldenFiles(t *testing.T) {
	cases := map[string][]byte{"gemini_cli": fixtures.ClientRequest(t, "gemini_cli/tool_turn.json")}
	for name, body := range geminiDecodeCases {
		cases[name] = []byte(body)
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			request, _, err := geminicodec.Decode(body, "m")
			if err != nil {
				t.Fatal(err)
			}
			request.Model.Upstream = "m"
			chat, err := openai_chat.Encode(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join("testdata", "gemini_to_chat", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			got := normaliseChat(t, chat)
			for _, message := range got["messages"].([]any) {
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
				t.Fatalf("hub differs from %s.json\n got: %s\nwant: %s", name, g, raw)
			}
		})
	}
}
