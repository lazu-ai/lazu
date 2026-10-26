package openai_chat

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	responsescodec "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
)

func TestDecodePreservesPresenceAndMarksUnmodeledFieldsUnknown(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}],"temperature":0,"top_p":0,"seed":0,"store":false,"frequency_penalty":0.2}`)
	request, _, err := Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if !request.Sampling.Temperature.Set || request.Sampling.Temperature.Value != 0 || !request.Sampling.TopP.Set || !request.Sampling.Seed.Set {
		t.Fatalf("explicit zero presence was lost: %+v", request.Sampling)
	}
	if !request.Lifecycle.Store.Set || request.Lifecycle.Store.Value {
		t.Fatalf("explicit store=false was lost: %+v", request.Lifecycle.Store)
	}
	if _, ok := request.Raw.UnknownFields["frequency_penalty"]; !ok {
		t.Fatalf("unmodeled field must be rejected by a cross-protocol plan: %+v", request.Raw.UnknownFields)
	}
	wire, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var encodedObject map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &encodedObject); err != nil {
		t.Fatal(err)
	}
	if got, ok := encodedObject["top_p"]; !ok || string(got) != "0" {
		t.Fatalf("explicit top_p=0 was lost while encoding: %s", encoded)
	}

	omitted, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if omitted.Sampling.Temperature.Set || omitted.Lifecycle.Store.Set {
		t.Fatalf("omitted fields unexpectedly became present: %+v %+v", omitted.Sampling, omitted.Lifecycle)
	}
	omittedWire, err := Encode(omitted)
	if err != nil {
		t.Fatal(err)
	}
	omittedEncoded, err := json.Marshal(omittedWire)
	if err != nil {
		t.Fatal(err)
	}
	var omittedObject map[string]json.RawMessage
	if err := json.Unmarshal(omittedEncoded, &omittedObject); err != nil {
		t.Fatal(err)
	}
	if _, ok := omittedObject["top_p"]; ok {
		t.Fatalf("omitted top_p unexpectedly became present: %s", omittedEncoded)
	}
}

func TestEncodeDropsCompletedGeminiReasoning(t *testing.T) {
	request := &textgen.Request{Conversation: []textgen.InputItem{
		{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "first"}}},
		{Kind: textgen.InputReasoning, Role: "assistant", Opaque: []textgen.OpaqueValue{{Dialect: "gemini", Value: json.RawMessage(`"signature"`)}}},
		{Kind: textgen.InputMessage, Role: "assistant", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "answer"}}},
		{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "next"}}},
	}}
	wire, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire.Messages) != 3 {
		t.Fatalf("completed Gemini reasoning should be omitted, got %+v", wire.Messages)
	}
}

func TestDecodeTreatsNullableOptionsAsOmitted(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":"hello"}],"temperature":null,"top_p":null,"top_k":null,"seed":null,"stop":null,"n":null,"logprobs":null,"top_logprobs":null,"max_completion_tokens":null,"store":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if request.Sampling.Temperature.Set || request.Sampling.TopP.Set || request.Sampling.TopK.Set || request.Sampling.Seed.Set || request.Sampling.Stop.Set || request.Sampling.N.Set || request.Sampling.LogProbs.Set || request.Sampling.TopLogProbs.Set || request.Sampling.MaxOutput.Set || request.Lifecycle.Store.Set {
		t.Fatalf("nullable options must remain omitted: sampling=%+v lifecycle=%+v", request.Sampling, request.Lifecycle)
	}
}

func TestDecodeFallsBackToLegacyMaxTokensWhenNewAliasIsNull(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":null,"max_tokens":123}`))
	if err != nil {
		t.Fatal(err)
	}
	if !request.Sampling.MaxOutput.Set || request.Sampling.MaxOutput.Value != 123 {
		t.Fatalf("legacy max_tokens fallback was lost: %+v", request.Sampling.MaxOutput)
	}
}

func TestDecodeMarksNestedChatControlsNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","stream":true,"stream_options":{"include_usage":true,"include_obfuscation":false},"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["stream_options.include_obfuscation"]; !ok {
		t.Fatalf("nested control must remain native-only: %+v", request.Raw.UnknownFields)
	}
	// Anthropic's cache marker is carried on the part (textgen.VendorOptions)
	if _, unknown := request.Raw.UnknownFields["messages[0].content[0].cache_control"]; unknown || string(request.Conversation[0].Content[0].CacheControl) != `{"type":"ephemeral"}` {
		t.Fatalf("part cache_control must be carried: %+v %s", request.Raw.UnknownFields, request.Conversation[0].Content[0].CacheControl)
	}
}

func TestDecodeMarksFunctionToolExtensionsNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","cache_control":{"type":"ephemeral"},"function":{"name":"lookup","description":"lookup","parameters":{"type":"object"},"strict":true,"provider_options":{"cache":"short"}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["tools[0].function.provider_options"]; !ok {
		t.Fatalf("function tool extension must remain native-only: %+v", request.Raw.UnknownFields)
	}
	if len(request.Tools) != 1 || request.Tools[0].Name != "lookup" || !request.Tools[0].Strict.Set || !request.Tools[0].Strict.Value {
		t.Fatalf("supported function tool fields were not retained: %+v", request.Tools)
	}
	if _, unknown := request.Raw.UnknownFields["tools[0].cache_control"]; unknown || string(request.Tools[0].CacheControl) != `{"type":"ephemeral"}` {
		t.Fatalf("tool cache_control must be carried: %+v %s", request.Raw.UnknownFields, request.Tools[0].CacheControl)
	}
}

// A system message of text parts is one instruction per part, each with its
// cache marker; a non-text part in an instruction stays native-only.
func TestDecodeArrayInstructionContent(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"system","content":[{"type":"text","text":"policy","cache_control":{"type":"ephemeral"}},{"type":"text","text":"more"}]},{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Raw.UnknownFields) != 0 {
		t.Fatalf("text instruction parts are carried: %+v", request.Raw.UnknownFields)
	}
	if len(request.Instructions) != 2 || request.Instructions[0].Text != "policy" || string(request.Instructions[0].CacheControl) != `{"type":"ephemeral"}` || request.Instructions[1].Text != "more" || request.Instructions[1].CacheControl != nil {
		t.Fatalf("instructions: %+v", request.Instructions)
	}
	request, _, err = Decode([]byte(`{"model":"m","messages":[{"role":"system","content":[{"type":"text","text":"policy"},{"type":"image_url","image_url":{"url":"https://x/y.png"}}]},{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["messages[0].content"]; !ok {
		t.Fatalf("a non-text instruction part must remain native-only: %+v", request.Raw.UnknownFields)
	}
	if len(request.Instructions) != 1 || request.Instructions[0].Text != "policy" {
		t.Fatalf("native semantic instruction decoding changed: %+v", request.Instructions)
	}
}

// The request-level cache marker and speed tier ride along as vendor
// options rather than unknown fields.
func TestDecodeCarriesAnthropicVendorOptions(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":"hello"}],"cache_control":{"type":"ephemeral"},"speed":"fast"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Raw.UnknownFields) != 0 || string(request.Vendor.CacheControl) != `{"type":"ephemeral"}` || string(request.Vendor.Speed) != `"fast"` {
		t.Fatalf("vendor options: %+v %+v", request.Raw.UnknownFields, request.Vendor)
	}
}

func TestChatIRRoundTripKeepsToolLoopAndStructuredOutput(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"weather"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Taipei\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"sunny"}],"tools":[{"type":"function","function":{"name":"weather","description":"lookup","parameters":{"type":"object"},"strict":true}}],"response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object"}}}}`)
	request, _, err := Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Tools) != 1 || len(again.Conversation) != 3 || again.Conversation[1].CallID != "call_1" || again.Conversation[2].CallID != "call_1" {
		t.Fatalf("tool loop changed across round trip: %+v", again)
	}
	if !again.Tools[0].Strict.Set || !again.Tools[0].Strict.Value {
		t.Fatalf("tool strict changed across round trip: %+v", again.Tools[0])
	}
	if again.Output.Type != "json_schema" || !again.Output.Strict.Set || !again.Output.Strict.Value {
		t.Fatalf("structured output changed: %+v", again.Output)
	}
}

func TestArrayValuedToolResultFailsClosedForResponsesBridge(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"tool","tool_call_id":"call_1","content":[{"type":"text","text":"structured"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["messages[0].content"]; !ok {
		t.Fatalf("array-valued tool result must remain native-only: %+v", request.Raw.UnknownFields)
	}
	if len(request.Conversation) != 1 || request.Conversation[0].Output != "structured" {
		t.Fatalf("native semantic decoding should remain available: %+v", request.Conversation)
	}
}

func TestDecodeMarksUnknownNestedChatUnionWithoutBreakingNativeParse(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"future_content","value":"x"}]}],"tools":[{"type":"custom","custom":{"name":"shell"}}]}`)
	request, _, err := Decode(body)
	if err != nil {
		t.Fatalf("native parse should retain unknown nested unions for upstream validation: %v", err)
	}
	for _, path := range []string{"messages[0].content[0].type", "tools[0].type"} {
		if _, ok := request.Raw.UnknownFields[path]; !ok {
			t.Fatalf("missing nested unknown path %q: %+v", path, request.Raw.UnknownFields)
		}
	}
}

func TestDecodeAllowsNativeFIMAndRejectsNamedMessageBridge(t *testing.T) {
	fim, _, err := Decode([]byte(`{"model":"m","prefix":"func main() {","suffix":"}"}`))
	if err != nil {
		t.Fatalf("native FIM request should decode without messages: %v", err)
	}
	if _, ok := fim.Raw.UnknownFields["prefix"]; !ok {
		t.Fatalf("FIM fields must remain native-only: %+v", fim.Raw.UnknownFields)
	}

	named, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","name":"alice","content":"hello"}]}`))
	if err != nil {
		t.Fatalf("decode named message: %v", err)
	}
	if len(named.Conversation) != 1 || named.Conversation[0].Name != "alice" {
		t.Fatalf("message name not retained in semantic request: %+v", named.Conversation)
	}
	if _, ok := named.Raw.UnknownFields["messages[0].name"]; !ok {
		t.Fatalf("named message must be rejected by cross-protocol planning: %+v", named.Raw.UnknownFields)
	}
}

func TestDecodeRetainsAudioFormatAndMarksAssistantReasoningNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"YWJj","format":"wav"}}]},{"role":"assistant","content":"answer","reasoning_content":"private chain","reasoning":"provider trace"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Conversation) == 0 || len(request.Conversation[0].Content) != 1 || request.Conversation[0].Content[0].Format != "wav" {
		t.Fatalf("input audio format was lost: %+v", request.Conversation)
	}
	for _, path := range []string{"messages[1].reasoning_content", "messages[1].reasoning"} {
		if _, ok := request.Raw.UnknownFields[path]; !ok {
			t.Fatalf("assistant reasoning must remain native-only at %s: %+v", path, request.Raw.UnknownFields)
		}
	}
	wire, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(wire.Messages[0].Content)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `[{"input_audio":{"data":"YWJj","format":"wav"},"type":"input_audio"}]` {
		t.Fatalf("audio format changed during native codec round trip: %s", encoded)
	}
}

func TestDecodeMarksLateSystemMessageNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":"hello"},{"role":"system","content":"late policy"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["messages[1].role"]; !ok {
		t.Fatalf("late system message must be rejected by a cross-protocol plan: %+v", request.Raw.UnknownFields)
	}
}

func TestDecodeMarksAssistantRefusalHistoryNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"assistant","content":null,"refusal":"not allowed"},{"role":"user","content":"continue"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["messages[0].refusal"]; !ok {
		t.Fatalf("assistant refusal history must be rejected by a cross-protocol plan: %+v", request.Raw.UnknownFields)
	}
}

func TestDecodeMarksAssistantAnnotationsNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"assistant","content":"cited","annotations":[{"type":"url_citation","url":"https://example.com"}]},{"role":"user","content":"continue"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["messages[0].annotations"]; !ok {
		t.Fatalf("assistant annotations must remain native-only: %+v", request.Raw.UnknownFields)
	}

	empty, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"assistant","content":"plain","annotations":[]},{"role":"user","content":"continue"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := empty.Raw.UnknownFields["messages[0].annotations"]; ok {
		t.Fatalf("empty assistant annotations should remain bridgeable: %+v", empty.Raw.UnknownFields)
	}
}

func TestDecodeRetainsAndMarksAssistantAudioHistoryNativeOnly(t *testing.T) {
	request, wire, err := Decode([]byte(`{"model":"m","messages":[{"role":"assistant","content":null,"audio":{"id":"audio_1","transcript":"hello"}},{"role":"user","content":"continue"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["messages[0].audio"]; !ok {
		t.Fatalf("assistant audio history must be rejected by a cross-protocol plan: %+v", request.Raw.UnknownFields)
	}
	if len(wire.Messages) != 2 || string(wire.Messages[0].Audio) != `{"id":"audio_1","transcript":"hello"}` {
		t.Fatalf("assistant audio history was discarded during decode: %+v", wire.Messages)
	}

	nullAudio, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"assistant","content":"plain","audio":null}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nullAudio.Raw.UnknownFields["messages[0].audio"]; ok {
		t.Fatalf("null assistant audio should remain bridgeable: %+v", nullAudio.Raw.UnknownFields)
	}
}

func TestDecodeMarksAssistantPrefixNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"assistant","content":"prefix text","prefix":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["messages[0].prefix"]; !ok {
		t.Fatalf("assistant prefix must be rejected by a cross-protocol plan: %+v", request.Raw.UnknownFields)
	}
}

func TestDecodeMarksToolCallExtraContentNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"},"extra_content":{"google":{"thought_signature":"sig_1"}}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Conversation) != 1 || request.Conversation[0].Kind != textgen.InputFunctionCall {
		t.Fatalf("native tool-call history was not retained: %+v", request.Conversation)
	}
	if _, ok := request.Raw.UnknownFields["messages[0].tool_calls[0].extra_content"]; !ok {
		t.Fatalf("provider tool-call metadata must be rejected by a cross-protocol plan: %+v", request.Raw.UnknownFields)
	}
}

func TestDecodeConsumesModelsFallbackAsGatewayRoutingMetadata(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"primary","models":["fallback"],"messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["models"]; ok {
		t.Fatalf("gateway-owned models routing metadata must not block bridging: %+v", request.Raw.UnknownFields)
	}
	if string(request.Raw.TopLevel["models"]) != `["fallback"]` {
		t.Fatalf("raw routing metadata should remain available to native handling: %+v", request.Raw.TopLevel)
	}
}

func TestDecodePreservesOnlyExplicitImageDetail(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/a.png"}},{"type":"image_url","image_url":{"url":"https://example.test/b.png","detail":"high"}}]}]}`)
	request, _, err := Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	parts := request.Conversation[0].Content
	if len(parts) != 2 || parts[0].Detail != "" || parts[1].Detail != "high" {
		t.Fatalf("image detail presence changed: %+v", parts)
	}
}

func TestDecodePreservesLegacyFunctionResult(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":"q"},{"role":"function","name":"lookup","content":"{\"ok\":true}"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	result := request.Conversation[1]
	if result.Kind != textgen.InputFunctionResult || result.Role != "function" || result.Name != "lookup" || result.Output != `{"ok":true}` {
		t.Fatalf("legacy function result lost: %+v", result)
	}
	wire, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire.Messages) != 2 || wire.Messages[1].Role != "function" || wire.Messages[1].Name == nil || *wire.Messages[1].Name != "lookup" {
		t.Fatalf("legacy function result did not round-trip: %+v", wire.Messages)
	}
}

func TestDecodeMarksImageMimeTypeNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/signed","detail":"high","mime_type":"image/png"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	parts := request.Conversation[0].Content
	if len(parts) != 1 || parts[0].URL != "https://example.test/signed" || parts[0].Detail != "high" {
		t.Fatalf("supported image fields were not retained: %+v", parts)
	}
	if _, ok := request.Raw.UnknownFields["messages[0].content[0].image_url.mime_type"]; !ok {
		t.Fatalf("image MIME hint must remain native-only: %+v", request.Raw.UnknownFields)
	}
}

func TestChatFileFilenameSurvivesResponsesBridge(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"file","file":{"file_data":"data:application/pdf;base64,JVBERg==","filename":"doc.pdf"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	parts := request.Conversation[0].Content
	if len(parts) != 1 || parts[0].Filename != "doc.pdf" {
		t.Fatalf("Chat file filename was not decoded: %+v", parts)
	}
	wire, err := responsescodec.Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(wire.Input, &items); err != nil {
		t.Fatal(err)
	}
	var content []map[string]any
	if len(items) != 1 || json.Unmarshal(items[0]["content"], &content) != nil || len(content) != 1 {
		t.Fatalf("bridged Responses input was malformed: %s", wire.Input)
	}
	if content[0]["filename"] != "doc.pdf" || content[0]["file_data"] != "data:application/pdf;base64,JVBERg==" {
		t.Fatalf("Chat file metadata was lost in Responses bridge: %#v", content[0])
	}
}

// A bridged output cap goes out as max_tokens, which every Chat-compatible
// upstream reads; max_completion_tokens is OpenAI's own alias only.
func TestEncodeWritesTheOutputCapAsMaxTokens(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_completion_tokens":64}`))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(wire)
	var object map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &object)
	if string(object["max_tokens"]) != "64" {
		t.Fatalf("max_tokens: %s", encoded)
	}
	if _, ok := object["max_completion_tokens"]; ok {
		t.Fatalf("max_completion_tokens must not be set: %s", encoded)
	}
}
