package textgen_test

import (
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	geminicodec "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/gemini"
)

// A Gemini client's turn that is still running on Gemini's thoughts (a thought
// signature after the last user prompt) stays on a Gemini upstream: Gemini
// refuses the turn's next call without its signatures. Signatures of finished
// turns mean nothing to another vendor and are left out.
func TestGeminiClientKeepsARunningSignedTurnOnGemini(t *testing.T) {
	var features textgen.FeatureSet
	for _, feature := range []textgen.FeatureID{textgen.FeatureEndpointChat, textgen.FeatureInputText, textgen.FeatureFunctionTools, textgen.FeatureToolResult} {
		features.Add(feature)
	}
	chatOnly := textgen.CapabilitySnapshot{Dialect: "openai", Revision: "test-rev", Features: features}
	client := textgen.Protocol{Family: textgen.ProtocolGemini, Dialect: "gemini"}
	for name, tc := range map[string]struct {
		body    string
		refused bool
	}{
		"signed call in the running turn": {refused: true, body: `{"contents":[
			{"role":"user","parts":[{"text":"北京的天气怎么样，现在几点了？"}]},
			{"role":"model","parts":[{"thoughtSignature":"sig-123","functionCall":{"id":"call_1","name":"get_weather","args":{"city":"北京"}}},{"functionCall":{"id":"call_2","name":"get_time","args":{}}}]},
			{"role":"user","parts":[{"functionResponse":{"name":"get_weather","response":{"result":{"temperature":26}}}},{"functionResponse":{"name":"get_time","response":{"result":{"time":"10:00"}}}}]}]}`},
		"running turn without signatures": {body: `{"contents":[
			{"role":"user","parts":[{"text":"北京的天气怎么样，现在几点了？"}]},
			{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"get_weather","args":{"city":"北京"}}},{"functionCall":{"id":"call_2","name":"get_time","args":{}}}]},
			{"role":"user","parts":[{"functionResponse":{"name":"get_weather","response":{"result":{"temperature":26}}}},{"functionResponse":{"name":"get_time","response":{"result":{"time":"10:00"}}}}]}]}`},
		"unsigned thought in the running turn": {body: `{"contents":[
			{"role":"user","parts":[{"text":"北京的天气怎么样？"}]},
			{"role":"model","parts":[{"thought":true,"text":"先查天气。"},{"functionCall":{"id":"call_1","name":"get_weather","args":{"city":"北京"}}}]},
			{"role":"user","parts":[{"functionResponse":{"name":"get_weather","response":{"result":{"temperature":26}}}}]}]}`},
		"signed call in a finished turn": {body: `{"contents":[
			{"role":"user","parts":[{"text":"北京的天气怎么样？"}]},
			{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"get_weather","args":{"city":"北京"}},"thoughtSignature":"sig-123"}]},
			{"role":"user","parts":[{"functionResponse":{"name":"get_weather","response":{"result":{"temperature":26}}}}]},
			{"role":"model","parts":[{"text":"北京现在晴，26 度。"}]},
			{"role":"user","parts":[{"text":"那上海呢？"}]}]}`},
		"signed text in a finished turn": {body: `{"contents":[
			{"role":"user","parts":[{"text":"先想一下。"}]},
			{"role":"model","parts":[{"text":"这是历史回答。","thoughtSignature":"sig-thinking-only"}]},
			{"role":"user","parts":[{"text":"继续。"}]}]}`},
	} {
		t.Run(name, func(t *testing.T) {
			request, _, err := geminicodec.Decode([]byte(tc.body), "m")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := textgen.PlanAttempt(client, chatOnly, request, 1, "m")
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), "thought signature") {
					t.Fatalf("a running signed turn must not be bridged: %+v, %v", plan, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if plan.Native || plan.UpstreamProtocol.Family != textgen.ProtocolOpenAIChat {
				t.Fatalf("expected a Chat bridge, got %+v", plan)
			}
		})
	}
}
