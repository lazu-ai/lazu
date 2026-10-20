package gemini

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// Gemini to Gemini keeps the thought signatures where Gemini put them: on
// the function call, and on the text part they followed.
func TestDecodeEncodeRoundTripsSignatures(t *testing.T) {
	body := `{"contents":[{"role":"user","parts":[{"text":"Weather?"}]},{"role":"model","parts":[{"text":"Checking.","thoughtSignature":"sig-text"},{"functionCall":{"id":"call_7","name":"get_weather","args":{"city":"Paris"}},"thoughtSignature":"sig-call"}]},{"role":"user","parts":[{"functionResponse":{"id":"call_7","name":"get_weather","response":{"temp":21}}}]}]}`
	request, _, err := Decode([]byte(body), "gemini-3.5-flash")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Encode(request, EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	model := encoded.Contents[1]
	if model.Role != "model" || len(model.Parts) != 2 || model.Parts[0].Text != "Checking." || model.Parts[0].ThoughtSignature != "sig-text" || model.Parts[1].FunctionCall == nil || model.Parts[1].ThoughtSignature != "sig-call" {
		t.Fatalf("model turn: %+v", model.Parts)
	}
	response := encoded.Contents[2].Parts[0].FunctionResponse
	if response == nil || response.ID != "call_7" || response.Name != "get_weather" || response.Response["temp"] != float64(21) {
		t.Fatalf("response: %+v", response)
	}
}

func TestDecodeMapsThinkingToolConfigAndUnknownFields(t *testing.T) {
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],
		"tools":[{"functionDeclarations":[{"name":"lookup"}]},{"googleSearch":{}},{"codeExecution":{}}],
		"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["lookup"]}},
		"generationConfig":{"stopSequences":["END"],"responseMimeType":"application/json","responseSchema":{"type":"OBJECT"},"thinkingConfig":{"thinkingBudget":9000,"includeThoughts":true},"speechConfig":{}},
		"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE"}],
		"cachedContent":"cachedContents/abc"}`
	request, _, err := Decode([]byte(body), "m")
	if err != nil {
		t.Fatal(err)
	}
	if request.ToolPolicy.Mode != "function" || request.ToolPolicy.FunctionName != "lookup" {
		t.Fatalf("tool policy: %+v", request.ToolPolicy)
	}
	if len(request.Tools) != 3 || request.Tools[1].Type != "web_search" || request.Tools[2].Type != "code_execution" {
		t.Fatalf("tools: %+v", request.Tools)
	}
	if !request.Reasoning.Budget.Set || request.Reasoning.Budget.Value != 9000 || request.Output.Type != "json_schema" {
		t.Fatalf("reasoning %+v output %+v", request.Reasoning, request.Output)
	}
	var stop []string
	if !request.Sampling.Stop.Set || json.Unmarshal(request.Sampling.Stop.Value, &stop) != nil || len(stop) != 1 {
		t.Fatalf("one stop sequence must survive: %+v", request.Sampling.Stop)
	}
	for _, path := range []string{"cachedContent", "generationConfig.speechConfig"} {
		if _, ok := request.Raw.UnknownFields[path]; !ok {
			t.Fatalf("%s must be unknown (native only): %v", path, request.Raw.UnknownFields)
		}
	}
	if _, ok := request.Raw.UnknownFields["safetySettings"]; ok {
		t.Fatal("safety settings are advisory, not unknown")
	}
	features, _ := textgen.ExtractFeatures(request)
	if !features.Has(textgen.FeatureHostedWebSearch) {
		t.Fatal("googleSearch is a hosted web search")
	}
}
