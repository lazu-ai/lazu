package openai_responses

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestResponsesIRRoundTripPreservesExplicitFalseToolStrict(t *testing.T) {
	body := []byte(`{"model":"m","instructions":"Be concise","input":"weather","store":false,"tools":[{"type":"function","name":"weather","description":"lookup","parameters":{"type":"object"},"strict":false}]}`)
	request, _, err := Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != 1 || !request.Tools[0].Strict.Set || request.Tools[0].Strict.Value {
		t.Fatalf("explicit strict=false was not decoded with presence: %+v", request.Tools)
	}
	if len(request.Instructions) != 1 || request.Instructions[0].Role != "system" {
		t.Fatalf("Responses instructions should use portable system semantics: %+v", request.Instructions)
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
	if len(again.Tools) != 1 || !again.Tools[0].Strict.Set || again.Tools[0].Strict.Value {
		t.Fatalf("tool strict=false changed across round trip: %+v", again.Tools)
	}
}

func TestDecodeMarksExtendedInstructionBlocksNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","instructions":[{"type":"input_text","text":"Be concise","cache_control":{"type":"ephemeral"}},{"type":"future_instruction","text":"Keep citations"}],"input":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Instructions) != 2 || request.Instructions[0].Text != "Be concise" || request.Instructions[1].Text != "Keep citations" {
		t.Fatalf("instruction text was not retained for native handling: %+v", request.Instructions)
	}
	for _, path := range []string{"instructions[0].cache_control", "instructions[1].type"} {
		if _, ok := request.Raw.UnknownFields[path]; !ok {
			t.Fatalf("extended instruction field %q must remain native-only: %+v", path, request.Raw.UnknownFields)
		}
	}
}

func TestUnknownResponsesInputItemStaysOpaqueForNativeRoundTrip(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"type":"future_call","id":"item_1","payload":{"x":1}}],"store":false}`)
	request, _, err := Decode(body)
	if err != nil {
		t.Fatalf("native parse should retain an unknown input item: %v", err)
	}
	if len(request.Conversation) != 1 || request.Conversation[0].Kind != textgen.InputOpaque || request.Conversation[0].WireType != "future_call" {
		t.Fatalf("unknown input item was not retained as opaque: %+v", request.Conversation)
	}
	if _, ok := request.Raw.UnknownFields["input[0].type"]; !ok {
		t.Fatalf("unknown input path was not recorded: %+v", request.Raw.UnknownFields)
	}
	wire, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(wire.Input, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0]["type"] != "future_call" {
		t.Fatalf("opaque input item changed across native round trip: %+v", items)
	}
}

func TestDecodeMarksResponsesInputItemExtensionsNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}],"cache_control":{"type":"ephemeral"}},{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}","provider_metadata":{"trace":"abc"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"input[0].cache_control", "input[1].provider_metadata"} {
		if _, ok := request.Raw.UnknownFields[path]; !ok {
			t.Fatalf("Responses item extension %q must remain native-only: %+v", path, request.Raw.UnknownFields)
		}
	}
	if len(request.Conversation) != 2 || request.Conversation[0].Kind != textgen.InputMessage || request.Conversation[1].Kind != textgen.InputFunctionCall {
		t.Fatalf("supported input semantics were not retained: %+v", request.Conversation)
	}
}

func TestResponsesAudioFormatSurvivesChatBridgeAndToolExtensionsFailClosed(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"role":"user","content":[{"type":"input_audio","data":"YWJj","format":"mp3"}]}],"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"x-provider-cache":true}],"store":false}`)
	request, _, err := Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Conversation) != 1 || len(request.Conversation[0].Content) != 1 || request.Conversation[0].Content[0].Format != "mp3" {
		t.Fatalf("Responses audio format was lost: %+v", request.Conversation)
	}
	if _, ok := request.Raw.UnknownFields["tools[0].x-provider-cache"]; !ok {
		t.Fatalf("nested tool extension must be marked native-only: %+v", request.Raw.UnknownFields)
	}
}

func TestResponsesImageFileIDIsRetainedForBridgeValidation(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":[{"role":"user","content":[{"type":"input_image","file_id":"file-image"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Conversation) != 1 || len(request.Conversation[0].Content) != 1 || request.Conversation[0].Content[0].FileID != "file-image" {
		t.Fatalf("image file_id was lost: %+v", request.Conversation)
	}
}

func TestStructuredFunctionOutputAndVerbosityFailClosedForChatBridge(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":[{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"structured"}]}],"text":{"verbosity":"low","format":{"type":"json_object"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"input[0].output", "text.verbosity"} {
		if _, ok := request.Raw.UnknownFields[path]; !ok {
			t.Fatalf("lossy Responses feature %q must be marked native-only: %+v", path, request.Raw.UnknownFields)
		}
	}
	if request.Output.Type != "json_object" {
		t.Fatalf("supported text.format was not decoded: %+v", request.Output)
	}
}

func TestIncompleteFunctionOutputFailsClosedForChatBridge(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":[{"type":"function_call_output","call_id":"call_1","output":"partial","status":"in_progress"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["input[0].status"]; !ok {
		t.Fatalf("incomplete function result must remain native-only: %+v", request.Raw.UnknownFields)
	}
}

func TestChatResponseToResponsesCarriesSemanticRequestConfiguration(t *testing.T) {
	request := &textgen.Request{
		Instructions: []textgen.Instruction{{Role: "system", Text: "Be concise"}},
		Tools: []textgen.ToolDefinition{{
			Type:       "function",
			Name:       "lookup",
			Parameters: json.RawMessage(`{"type":"object"}`),
		}},
		ToolPolicy: textgen.ToolPolicy{
			Mode:              "function",
			FunctionName:      "lookup",
			ParallelToolCalls: textgen.Some(true),
		},
		Sampling: textgen.SamplingOptions{
			Temperature: textgen.Some(0.7),
			TopP:        textgen.Some(0.8),
			MaxOutput:   textgen.Some(uint(321)),
		},
		Lifecycle: textgen.LifecycleOptions{Store: textgen.Some(false)},
		Metadata:  textgen.MetadataOptions{User: "user-1", Metadata: json.RawMessage(`{"trace":"t1"}`)},
	}
	response := ChatResponseToResponses(&dto.OpenAITextResponse{Id: "chatcmpl-config", Model: "m"}, "m", request)
	if response.ID != "resp_config" {
		t.Fatalf("Chat completion ID was not normalized for Responses: %q", response.ID)
	}
	if response.MaxOutputTokens == nil || *response.MaxOutputTokens != 321 || response.Temperature != 0.7 || response.TopP != 0.8 || !response.ParallelToolCalls {
		t.Fatalf("sampling or tool configuration was lost: %+v", response)
	}
	if len(response.Tools) != 1 || response.Tools[0]["name"] != "lookup" {
		t.Fatalf("tools were lost: %+v", response.Tools)
	}
	var choice map[string]any
	if err := json.Unmarshal(response.ToolChoice, &choice); err != nil || choice["name"] != "lookup" {
		t.Fatalf("tool choice was lost: %s (%v)", response.ToolChoice, err)
	}
	if string(response.Instructions) != `"Be concise"` || string(response.Metadata) != `{"trace":"t1"}` || string(response.User) != `"user-1"` {
		t.Fatalf("response metadata configuration was lost: %+v", response)
	}
}

func TestChatResponseToResponsesUsesChatConfigurationDefaults(t *testing.T) {
	request := &textgen.Request{}
	response := ChatResponseToResponses(&dto.OpenAITextResponse{Id: "chatcmpl-defaults", Model: "m"}, "m", request)
	if response.Temperature != 1 || response.TopP != 1 || !response.ParallelToolCalls || response.Truncation != "disabled" {
		t.Fatalf("Chat defaults were not represented in Responses configuration: %+v", response)
	}
	if response.MaxOutputTokens != nil || string(response.ToolChoice) != `"none"` || len(response.Tools) != 0 {
		t.Fatalf("omitted Chat configuration was misrepresented: %+v", response)
	}
	wire, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(wire, &encoded); err != nil {
		t.Fatal(err)
	}
	if encoded["max_output_tokens"] != nil || encoded["temperature"] != float64(1) || encoded["top_p"] != float64(1) || encoded["parallel_tool_calls"] != true {
		t.Fatalf("serialized Responses defaults were incorrect: %s", wire)
	}
	request.Tools = []textgen.ToolDefinition{{Type: "function", Name: "lookup"}}
	response = ChatResponseToResponses(&dto.OpenAITextResponse{Id: "chatcmpl-tool-defaults", Model: "m"}, "m", request)
	if string(response.ToolChoice) != `"auto"` {
		t.Fatalf("Chat tool default was not represented: %s", response.ToolChoice)
	}
}

func TestChatResponseToResponsesPreservesEmptyAssistantMessage(t *testing.T) {
	message := dto.Message{Role: "assistant"}
	message.SetStringContent("")
	response := ChatResponseToResponses(&dto.OpenAITextResponse{
		Id: "chatcmpl-empty", Model: "m",
		Choices: []dto.OpenAITextResponseChoice{{Message: message, FinishReason: "stop"}},
	}, "m")
	if len(response.Output) != 1 || response.Output[0].Type != "message" || len(response.Output[0].Content) != 1 {
		t.Fatalf("empty assistant turn was lost: %+v", response.Output)
	}
	content := response.Output[0].Content[0]
	if content.Type != "output_text" || content.Text != "" {
		t.Fatalf("empty assistant content was malformed: %+v", content)
	}
}

func TestChatResponseToResponsesPreservesReasoningContent(t *testing.T) {
	reasoning := "short provider reasoning"
	message := dto.Message{Role: "assistant", ReasoningContent: reasoning}
	message.SetStringContent("final answer")
	response := ChatResponseToResponses(&dto.OpenAITextResponse{
		Id: "chatcmpl-reasoning", Model: "m",
		Choices: []dto.OpenAITextResponseChoice{{Message: message, FinishReason: "stop"}},
	}, "m")
	if len(response.Output) != 2 || response.Output[0].Type != "reasoning" || response.Output[1].Type != "message" {
		t.Fatalf("reasoning output was not ordered before the assistant message: %+v", response.Output)
	}
	if len(response.Output[0].Content) != 1 || response.Output[0].Content[0].Type != "reasoning_text" || response.Output[0].Content[0].Text != reasoning {
		t.Fatalf("reasoning content was not preserved: %+v", response.Output[0])
	}
	wire, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(wire, &encoded); err != nil {
		t.Fatal(err)
	}
	output := encoded["output"].([]any)
	if output[0].(map[string]any)["type"] != "reasoning" {
		t.Fatalf("reasoning item wire type was not preserved: %s", wire)
	}
	if _, ok := output[0].(map[string]any)["role"]; ok {
		t.Fatalf("reasoning item must not emit message-only role field: %s", wire)
	}
}

func TestDecodeResponsesMarksAnnotatedOutputHistoryNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"cited","annotations":[{"type":"url_citation","url":"https://example.com"}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["input[0].content[0].annotations"]; !ok {
		t.Fatalf("annotated output history must remain native-only: %+v", request.Raw.UnknownFields)
	}
	empty, _, err := Decode([]byte(`{"model":"m","input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"plain","annotations":[]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := empty.Raw.UnknownFields["input[0].content[0].annotations"]; ok {
		t.Fatalf("empty annotations should remain bridgeable: %+v", empty.Raw.UnknownFields)
	}
}

func TestDecodeResponsesMarksExtensionsOnEveryRecognizedContentPartNativeOnly(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello","cache_control":{"type":"ephemeral"}},{"type":"text","text":"alias","vendor_text":"x"},{"type":"input_image","image_url":"https://example.com/a.png","provider_metadata":{"id":"image-1"}},{"type":"input_file","file_id":"file-1","cache_control":{"type":"ephemeral"}},{"type":"input_audio","data":"YWJj","format":"wav","vendor_audio":true}]}]}`)
	request, _, err := Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"input[0].content[0].cache_control",
		"input[0].content[1].vendor_text",
		"input[0].content[2].provider_metadata",
		"input[0].content[3].cache_control",
		"input[0].content[4].vendor_audio",
	} {
		if _, ok := request.Raw.UnknownFields[path]; !ok {
			t.Fatalf("recognized content extension %q was silently dropped: %+v", path, request.Raw.UnknownFields)
		}
	}
}

func TestEncodeResponsesPreservesExplicitZeroSamplingValues(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":"hello","temperature":0,"top_p":0}`))
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
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["temperature"]) != "0" || string(raw["top_p"]) != "0" {
		t.Fatalf("explicit zero sampling values were omitted: %s", encoded)
	}
}

func TestDecodeResponsesTreatsNullableOptionsAsOmitted(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":"hello","temperature":null,"top_p":null,"top_logprobs":null,"max_output_tokens":null,"store":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if request.Sampling.Temperature.Set || request.Sampling.TopP.Set || request.Sampling.TopLogProbs.Set || request.Sampling.LogProbs.Set || request.Sampling.MaxOutput.Set || request.Lifecycle.Store.Set {
		t.Fatalf("nullable options must remain omitted: sampling=%+v lifecycle=%+v", request.Sampling, request.Lifecycle)
	}
}

func TestEncodeChatBridgePreservesNonStoringDefault(t *testing.T) {
	request := &textgen.Request{
		Model:        textgen.ModelRef{Canonical: "m"},
		Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "hello"}}}},
		Raw:          textgen.RawRetention{Protocol: textgen.ProtocolOpenAIChat},
	}
	wire, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	if string(wire.Store) != "false" {
		t.Fatalf("omitted Chat store default was not preserved: %s", wire.Store)
	}

	explicit := request.Clone()
	explicit.Lifecycle.Store = textgen.Some(true)
	wire, err = Encode(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if string(wire.Store) != "true" {
		t.Fatalf("explicit Chat store=true was overwritten: %s", wire.Store)
	}

	native := request.Clone()
	native.Raw.Protocol = textgen.ProtocolOpenAIResponses
	wire, err = Encode(native)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire.Store) != 0 {
		t.Fatalf("native Responses omission should retain its own default: %s", wire.Store)
	}
}

func TestDecodeResponsesMarksUnmodeledStreamOptionNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":"hello","stream":true,"stream_options":{"include_usage":true,"include_obfuscation":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["stream_options.include_obfuscation"]; !ok {
		t.Fatalf("unmodeled stream option must remain native-only: %+v", request.Raw.UnknownFields)
	}
}

func TestDecodeResponsesMarksIncompleteFunctionCallHistoryNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"x\":","status":"in_progress"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["input[0].status"]; !ok {
		t.Fatalf("incomplete function-call status must remain native-only: %+v", request.Raw.UnknownFields)
	}
}

func TestDecodeResponsesMarksIncompleteMessageHistoryNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":[{"type":"message","role":"assistant","status":"incomplete","content":[{"type":"output_text","text":"partial"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["input[0].status"]; !ok {
		t.Fatalf("incomplete message status must remain native-only: %+v", request.Raw.UnknownFields)
	}
}

func TestDecodeResponsesMarksUnsupportedToolChoiceObjectNativeOnly(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":"hello","tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"lookup"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := request.Raw.UnknownFields["tool_choice"]; !ok {
		t.Fatalf("unsupported tool-choice object must remain native-only: %+v", request.Raw.UnknownFields)
	}
}

func TestChatResponseContentFilterBecomesIncomplete(t *testing.T) {
	message := dto.Message{Role: "assistant"}
	message.SetStringContent("partial")
	response := ChatResponseToResponses(&dto.OpenAITextResponse{Id: "chatcmpl-filtered", Model: "m", Choices: []dto.OpenAITextResponseChoice{{Message: message, FinishReason: "content_filter"}}}, "m")
	if response.Status != "incomplete" || response.IncompleteDetails == nil || response.IncompleteDetails.Reason != "content_filter" {
		t.Fatalf("content filter terminal state was misreported: %+v", response)
	}
	if len(response.Output) != 1 || response.Output[0].Status != "incomplete" {
		t.Fatalf("content filter output must also be incomplete: %+v", response.Output)
	}
}

func TestChatResponseToResponsesPreservesRefusal(t *testing.T) {
	refusal := "I cannot help with that."
	response := ChatResponseToResponses(&dto.OpenAITextResponse{
		Id:    "chatcmpl-refusal",
		Model: "m",
		Choices: []dto.OpenAITextResponseChoice{{
			Message:      dto.Message{Role: "assistant", Refusal: &refusal},
			FinishReason: "stop",
		}},
	}, "m")
	if len(response.Output) != 1 || len(response.Output[0].Content) != 1 {
		t.Fatalf("refusal message was lost: %+v", response.Output)
	}
	content := response.Output[0].Content[0]
	if content.Type != "refusal" || content.Refusal != refusal {
		t.Fatalf("unexpected refusal content: %+v", content)
	}
	wire, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(wire, &payload); err != nil {
		t.Fatal(err)
	}
	output := payload["output"].([]any)[0].(map[string]any)
	refusalContent := output["content"].([]any)[0].(map[string]any)
	if _, ok := refusalContent["text"]; ok {
		t.Fatalf("refusal content must not include an output_text field: %s", wire)
	}
}

func TestResponsesResponseToChatPreservesContentFilter(t *testing.T) {
	response := &dto.OpenAIResponsesResponse{
		ID:                "resp_filtered",
		Status:            "incomplete",
		IncompleteDetails: &dto.IncompleteDetails{Reason: "content_filter"},
	}
	chat := ResponsesResponseToChat(response, "m")
	if chat.Id != "chatcmpl-filtered" {
		t.Fatalf("Responses ID was not normalized for Chat: %q", chat.Id)
	}
	if len(chat.Choices) != 1 || chat.Choices[0].FinishReason != "content_filter" {
		t.Fatalf("expected content_filter finish reason, got %+v", chat.Choices)
	}
}

func TestBridgeUsageJSONContainsOnlyDestinationProtocolFields(t *testing.T) {
	responses := ChatResponseToResponses(&dto.OpenAITextResponse{
		Id:    "chatcmpl-usage",
		Model: "m",
		Usage: dto.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}, "m")
	responsesWire, err := json.Marshal(responses)
	if err != nil {
		t.Fatal(err)
	}
	var responsesPayload map[string]any
	if err := json.Unmarshal(responsesWire, &responsesPayload); err != nil {
		t.Fatal(err)
	}
	responsesUsage := responsesPayload["usage"].(map[string]any)
	if responsesUsage["input_tokens"] != float64(3) || responsesUsage["output_tokens"] != float64(2) {
		t.Fatalf("Responses usage aliases are missing: %s", responsesWire)
	}
	for _, key := range []string{"prompt_tokens", "completion_tokens", "prompt_tokens_details", "completion_tokens_details"} {
		if _, ok := responsesUsage[key]; ok {
			t.Fatalf("Responses usage leaked Chat field %q: %s", key, responsesWire)
		}
	}

	chat := ResponsesResponseToChat(&dto.OpenAIResponsesResponse{
		ID:    "resp-usage",
		Model: "m",
		Usage: &dto.Usage{InputTokens: 7, OutputTokens: 4, TotalTokens: 11},
	}, "m")
	chatWire, err := json.Marshal(chat)
	if err != nil {
		t.Fatal(err)
	}
	var chatPayload map[string]any
	if err := json.Unmarshal(chatWire, &chatPayload); err != nil {
		t.Fatal(err)
	}
	chatUsage := chatPayload["usage"].(map[string]any)
	if chatUsage["prompt_tokens"] != float64(7) || chatUsage["completion_tokens"] != float64(4) {
		t.Fatalf("Chat usage fields are missing: %s", chatWire)
	}
	for _, key := range []string{"input_tokens", "output_tokens", "input_tokens_details", "output_tokens_details"} {
		if _, ok := chatUsage[key]; ok {
			t.Fatalf("Chat usage leaked Responses field %q: %s", key, chatWire)
		}
	}
}

func TestResponsesResponseToChatPreservesRefusal(t *testing.T) {
	response := &dto.OpenAIResponsesResponse{
		ID:     "resp-refusal",
		Status: "completed",
		Output: []dto.ResponsesOutput{{
			Type: "message",
			Role: "assistant",
			Content: []dto.ResponsesOutputContent{{
				Type:    "refusal",
				Refusal: "I cannot help with that.",
			}},
		}},
	}
	chat := ResponsesResponseToChat(response, "m")
	if len(chat.Choices) != 1 || chat.Choices[0].Message.Refusal == nil || *chat.Choices[0].Message.Refusal != "I cannot help with that." {
		t.Fatalf("refusal was lost: %+v", chat.Choices)
	}
	if chat.Choices[0].Message.Content != nil {
		t.Fatalf("refusal-only response should keep content null: %+v", chat.Choices[0].Message)
	}
}

func TestResponsesResponseToChatKeepsOnlyCompletedCallsWhenGloballyIncomplete(t *testing.T) {
	response := &dto.OpenAIResponsesResponse{
		ID:     "resp-partial-tools",
		Status: "incomplete",
		Output: []dto.ResponsesOutput{
			{Type: dto.ResponsesOutputTypeFunctionCall, Status: "completed", CallID: "call_done", Name: "done", Arguments: `{}`},
			{Type: dto.ResponsesOutputTypeFunctionCall, Status: "in_progress", CallID: "call_partial", Name: "partial", Arguments: `{"x":`},
		},
	}
	chat := ResponsesResponseToChat(response, "m")
	calls := chat.Choices[0].Message.ParseToolCalls()
	if len(calls) != 1 || calls[0].ID != "call_done" {
		t.Fatalf("completed call should survive while partial call is suppressed: %+v", calls)
	}
	if chat.Choices[0].FinishReason != "length" || chat.Choices[0].Message.Content != nil {
		t.Fatalf("global incomplete state should remain visible with null tool-call content: %+v", chat.Choices[0])
	}
}

func TestResponsesResponseToChatPreservesPartialCallsWhenMaxOutputTokensReached(t *testing.T) {
	response := &dto.OpenAIResponsesResponse{
		ID:                "resp-partial-tools",
		Status:            "incomplete",
		IncompleteDetails: &dto.IncompleteDetails{Reason: "max_output_tokens"},
		Output: []dto.ResponsesOutput{
			{Type: dto.ResponsesOutputTypeFunctionCall, Status: "completed", CallID: "call_done", Name: "done", Arguments: `{}`},
			{Type: dto.ResponsesOutputTypeFunctionCall, Status: "in_progress", CallID: "call_partial", Name: "partial", Arguments: `{"x":`},
		},
	}
	chat := ResponsesResponseToChat(response, "m")
	calls := chat.Choices[0].Message.ParseToolCalls()
	if len(calls) != 2 || calls[0].ID != "call_done" || calls[1].ID != "call_partial" || calls[1].Function.Arguments != `{"x":` {
		t.Fatalf("length-truncated partial call was lost: %+v", calls)
	}
	if chat.Choices[0].FinishReason != "length" || chat.Choices[0].Message.Content != nil {
		t.Fatalf("max-output truncation state changed: %+v", chat.Choices[0])
	}
}

func TestDecodeCodexStyleInputKeepsAdditionalToolsOpaqueAndMessageIDsBridgeable(t *testing.T) {
	// Shape captured from codex-cli 0.147.0 (the client bundled in ChatGPT
	// desktop): an additional_tools item first, then messages carrying ids.
	body := []byte(`{"model":"gpt-5.6","store":false,"stream":true,"input":[
		{"type":"additional_tools","role":"developer","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","description":"run"}]}]},
		{"type":"message","role":"developer","content":[{"type":"input_text","text":"You are Codex"}]},
		{"type":"message","id":"msg_0","role":"user","content":[{"type":"input_text","text":"reply with hi"}]}
	]}`)
	request, _, err := Decode(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(request.Conversation) != 3 {
		t.Fatalf("expected three input items: %+v", request.Conversation)
	}
	first := request.Conversation[0]
	if first.Kind != textgen.InputOpaque || first.WireType != "additional_tools" {
		t.Fatalf("additional_tools must decode as an opaque item, not a reference: %+v", first)
	}
	if _, ok := request.Raw.UnknownFields["input[0].type"]; !ok {
		t.Fatalf("opaque item type must stay native-only: %+v", request.Raw.UnknownFields)
	}
	if _, ok := request.Raw.UnknownFields["input[2].id"]; ok {
		t.Fatalf("message ids are inert without server-side state and must not block a bridge: %+v", request.Raw.UnknownFields)
	}
	features, uses := textgen.ExtractFeatures(request)
	if features.Has(textgen.FeatureInputReference) {
		t.Fatalf("no item_reference was sent, feature set: %v", features.Names())
	}
	found := false
	for _, use := range uses {
		if use.Feature == textgen.FeatureOpaqueInputItem && use.Path == "input[0]" && use.Detail == "additional_tools" {
			found = true
		}
	}
	if !found {
		t.Fatalf("opaque item use must carry its wire type: %+v", uses)
	}
	wire, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(wire.Input, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0]["type"] != "additional_tools" {
		t.Fatalf("opaque item changed across native round trip: %+v", items)
	}
}
