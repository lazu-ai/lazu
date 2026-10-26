package gemini

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

// A bridged Chat request to a Gemini channel (EncodeBridgedRequest: the
// codec, then the channel's policy) is held to golden files under
// testdata/chat_to_gemini: the legacy Chat-to-Gemini converter's output on
// these requests, saved before it was deleted (AGENTS.md, Protocol Layer 7).
// What is the channel's to decide is compared apart, by its own tests:
// safety settings, thinking, response modalities.
var geminiDifferentialCases = map[string]string{
	"text":        `{"model":"gemini-2.0-flash","messages":[{"role":"system","content":"Be terse."},{"role":"user","content":"Hi"},{"role":"assistant","content":"Hello."},{"role":"user","content":"Again?"}],"temperature":0.3,"top_p":0.9,"max_tokens":200,"seed":7}`,
	"tool_round":  `{"model":"gemini-2.0-flash","tools":[{"type":"function","function":{"name":"get_weather","description":"Weather","parameters":{"type":"object","properties":{"city":{"type":"string","format":"uri"}},"required":["city"],"additionalProperties":false}}}],"messages":[{"role":"user","content":"Weather in Paris?"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"{\"temp\":21}"},{"role":"tool","tool_call_id":"call_1","content":"sunny"}]}`,
	"image":       `{"model":"gemini-2.0-flash","messages":[{"role":"user","content":[{"type":"text","text":"What is this?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}]}`,
	"json_schema": `{"model":"gemini-2.0-flash","messages":[{"role":"user","content":"A city"}],"response_format":{"type":"json_schema","json_schema":{"name":"city","schema":{"type":"object","title":"City","properties":{"name":{"type":"string"}},"additionalProperties":false}}}}`,
	"hosted":      `{"model":"gemini-2.0-flash","messages":[{"role":"user","content":"news"}],"tools":[{"type":"function","function":{"name":"googleSearch"}},{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{}}}}]}`,
}

func TestBridgedChatToGeminiMatchesTheGoldenFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := constant.GeminiVisionMaxImageNum
	constant.GeminiVisionMaxImageNum = -1 // the image limit is the channel's policy, tested apart
	t.Cleanup(func() { constant.GeminiVisionMaxImageNum = previous })
	for name, body := range geminiDifferentialCases {
		t.Run(name, func(t *testing.T) {
			request, _, err := openai_chat.Decode([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{OriginModelName: request.Model.Canonical, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: request.Model.Canonical}}
			encoded, err := EncodeBridgedRequest(c, info, request)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join("testdata", "chat_to_gemini", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			if got := normaliseGemini(t, encoded); !reflect.DeepEqual(got, want) {
				g, _ := json.MarshalIndent(got, "", "  ")
				t.Fatalf("bridged request differs from %s.json\n got: %s\nwant: %s", name, g, raw)
			}
		})
	}
}

// normaliseGemini drops the channel's fields and folds what Gemini reads
// alike: an empty tools list.
func normaliseGemini(t *testing.T, request *dto.GeminiChatRequest) map[string]any {
	t.Helper()
	copyRequest := *request
	copyRequest.SafetySettings = nil
	copyRequest.GenerationConfig.ThinkingConfig = nil
	copyRequest.GenerationConfig.ResponseModalities = nil
	raw, err := json.Marshal(copyRequest)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if tools, ok := out["tools"].([]any); ok && len(tools) == 0 {
		delete(out, "tools")
	}
	if config, ok := out["generationConfig"].(map[string]any); ok && len(config) == 0 {
		delete(out, "generationConfig")
	}
	return out
}
