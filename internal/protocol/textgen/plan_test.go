package textgen

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func minimalRequest() *Request {
	return &Request{Model: ModelRef{Canonical: "test-model"}, Conversation: []InputItem{{Kind: InputMessage, Role: "user", Content: []ContentPart{{Kind: ContentText, Text: "hello"}}}}}
}

func snapshot(dialect string, endpoints ...FeatureID) CapabilitySnapshot {
	var features FeatureSet
	features.Add(FeatureInputText)
	for _, endpoint := range endpoints {
		features.Add(endpoint)
	}
	return CapabilitySnapshot{Dialect: dialect, Revision: "test-rev", Features: features}
}

func TestPlanAttemptResponsesToCloudflareChatBridgeStoreTriState(t *testing.T) {
	client := Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"}
	capabilities := snapshot("cloudflare_ai_v1", FeatureEndpointChat)

	omitted, err := PlanAttempt(client, capabilities, minimalRequest(), 7, "upstream")
	if err != nil {
		t.Fatalf("omitted store: %v", err)
	}
	if omitted.Native || omitted.UpstreamProtocol.Family != ProtocolOpenAIChat {
		t.Fatalf("expected Responses->Chat bridge, got %+v", omitted)
	}
	if len(omitted.Warnings) != 1 || omitted.Warnings[0].Message != "stateless_bridge" {
		t.Fatalf("expected stateless warning, got %+v", omitted.Warnings)
	}

	storeFalse := minimalRequest()
	storeFalse.Lifecycle.Store = Some(false)
	falsePlan, err := PlanAttempt(client, capabilities, storeFalse, 7, "upstream")
	if err != nil || len(falsePlan.Warnings) != 0 {
		t.Fatalf("store=false should be lossless without warning: plan=%+v err=%v", falsePlan, err)
	}

	storeTrue := minimalRequest()
	storeTrue.Lifecycle.Store = Some(true)
	_, err = PlanAttempt(client, capabilities, storeTrue, 7, "upstream")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Path != "store" || unsupported.Feature != FeatureStore {
		t.Fatalf("store=true should fail at store, got %v", err)
	}
}

func TestPlanAttemptChatToResponsesOnlyBridge(t *testing.T) {
	plan, err := PlanAttempt(
		Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"},
		snapshot("openai", FeatureEndpointResponses),
		minimalRequest(), 9, "upstream",
	)
	if err != nil {
		t.Fatalf("Chat to Responses-only planning failed: %v", err)
	}
	if plan.Native || plan.UpstreamProtocol.Family != ProtocolOpenAIResponses {
		t.Fatalf("expected Chat->Responses bridge, got %+v", plan)
	}
}

func TestPlanAttemptRejectsReasoningToolHistoryOnChatToResponsesBridge(t *testing.T) {
	request := minimalRequest()
	request.Conversation = append(request.Conversation, InputItem{Kind: InputFunctionCall, Role: "assistant", CallID: "call_1", FunctionName: "lookup", Arguments: `{}`})
	capabilities := snapshot("openai", FeatureEndpointResponses, FeatureFunctionTools, FeatureReasoningEffort)
	_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, capabilities, request, 1, "reasoning-model")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureFunctionTools || unsupported.Path != "input[1]" {
		t.Fatalf("reasoning tool history should fail closed, got %v", err)
	}

	capabilities.Features &^= FeatureSet(1) << FeatureReasoningEffort
	if _, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, capabilities, request, 1, "non-reasoning-model"); err != nil {
		t.Fatalf("non-reasoning tool history should remain bridgeable: %v", err)
	}
}

// With reasoning off there is no reasoning state to lose, so a Chat request
// with tools — first call or continuation — bridges to Responses.
func TestPlanAttemptBridgesChatToolsWithReasoningOff(t *testing.T) {
	request := minimalRequest()
	request.Tools = []ToolDefinition{{Type: "function", Name: "lookup"}}
	request.Conversation = append(request.Conversation, InputItem{Kind: InputFunctionCall, Role: "assistant", CallID: "call_1", FunctionName: "lookup", Arguments: `{}`})
	request.Reasoning.Effort = "none"
	capabilities := snapshot("openai", FeatureEndpointResponses, FeatureFunctionTools, FeatureReasoningEffort)
	plan, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, capabilities, request, 1, "gpt-6-astra")
	if err != nil {
		t.Fatalf("reasoning-off tool bridge refused: %v", err)
	}
	if plan.Native || plan.UpstreamProtocol.Family != ProtocolOpenAIResponses {
		t.Fatalf("expected Chat->Responses bridge, got %+v", plan)
	}
}

func TestPlanAttemptRejectsReasoningToolCapableChatBridgeBeforeFirstCall(t *testing.T) {
	request := minimalRequest()
	request.Tools = []ToolDefinition{{Type: "function", Name: "lookup"}}
	capabilities := snapshot("openai", FeatureEndpointResponses, FeatureFunctionTools, FeatureReasoningEffort)
	_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, capabilities, request, 1, "reasoning-model")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureFunctionTools || unsupported.Path != "tool_choice" {
		t.Fatalf("default-auto reasoning tool bridge should fail before the first call, got %v", err)
	}

	request.ToolPolicy.Mode = "none"
	if _, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, capabilities, request, 1, "reasoning-model"); err != nil {
		t.Fatalf("tool_choice=none should remain bridgeable: %v", err)
	}

	request.ToolPolicy.Mode = "required"
	capabilities.Features &^= FeatureSet(1) << FeatureReasoningEffort
	if _, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, capabilities, request, 1, "non-reasoning-model"); err != nil {
		t.Fatalf("non-reasoning tool bridge should remain supported: %v", err)
	}
}

func TestPlanAttemptChatToResponsesAllowsExactlyOneOutput(t *testing.T) {
	capabilities := snapshot("openai", FeatureEndpointResponses, FeatureN)

	one := minimalRequest()
	one.Sampling.N = Some(1)
	if _, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, capabilities, one, 1, "test-model"); err != nil {
		t.Fatalf("explicit n=1 should bridge losslessly: %v", err)
	}

	two := minimalRequest()
	two.Sampling.N = Some(2)
	_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, capabilities, two, 1, "test-model")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureN || unsupported.Path != "n" {
		t.Fatalf("n=2 should be rejected by Responses bridge, got %v", err)
	}

	responsesTwo := minimalRequest()
	responsesTwo.Sampling.N = Some(2)
	_, err = PlanAttempt(
		Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"},
		snapshot("openai_chat_compatible", FeatureEndpointChat, FeatureN),
		responsesTwo, 1, "test-model",
	)
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureN || unsupported.Path != "n" {
		t.Fatalf("Responses-to-Chat n=2 should fail closed, got %v", err)
	}
}

func TestPlanAttemptRejectsBridgeFieldsWithoutLosslessResponseMapping(t *testing.T) {
	capabilities := snapshot("openai", FeatureEndpointResponses, FeatureDeveloperRole, FeatureLogProbs)

	t.Run("developer authority", func(t *testing.T) {
		request := minimalRequest()
		request.Instructions = []Instruction{{Role: "system", Text: "policy"}, {Role: "developer", Text: "task"}}
		_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, capabilities, request, 1, "test-model")
		var unsupported *UnsupportedFeatureError
		if !errors.As(err, &unsupported) || unsupported.Feature != FeatureDeveloperRole || unsupported.Path != "instructions[1].role" {
			t.Fatalf("developer authority should fail closed, got %v", err)
		}
	})

	t.Run("logprobs response", func(t *testing.T) {
		request := minimalRequest()
		request.Sampling.LogProbs = Some(true)
		_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, capabilities, request, 1, "test-model")
		var unsupported *UnsupportedFeatureError
		if !errors.As(err, &unsupported) || unsupported.Feature != FeatureLogProbs || unsupported.Path != "logprobs" {
			t.Fatalf("logprobs bridge should fail closed, got %v", err)
		}
	})

	t.Run("multiple system boundaries", func(t *testing.T) {
		request := minimalRequest()
		request.Instructions = []Instruction{{Role: "system", Text: "first"}, {Role: "system", Text: "second"}}
		_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, capabilities, request, 1, "test-model")
		var unsupported *UnsupportedFeatureError
		if !errors.As(err, &unsupported) || unsupported.Feature != FeatureUnknownField || unsupported.Path != "instructions" {
			t.Fatalf("multiple system-message boundaries should fail closed, got %v", err)
		}
	})
}

func TestPlanAttemptKeepsUnmodeledHostedToolsNativeOnly(t *testing.T) {
	request := minimalRequest()
	request.Tools = []ToolDefinition{{Type: "file_search"}}

	native, err := PlanAttempt(
		Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"},
		snapshot("openai", FeatureEndpointResponses),
		request, 1, "test-model",
	)
	if err != nil || native == nil || !native.Native {
		t.Fatalf("provider-native hosted tool should remain eligible for native Responses: plan=%+v err=%v", native, err)
	}

	_, err = PlanAttempt(
		Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"},
		snapshot("cloudflare_ai_v1", FeatureEndpointChat),
		request, 1, "test-model",
	)
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureHostedToolNativeOnly || unsupported.Path != "tools[0]" {
		t.Fatalf("unmodeled hosted tool should fail closed across protocols, got %v", err)
	}
}

func TestPlanAttemptAllowsReasoningResponsesToChatBridge(t *testing.T) {
	request := minimalRequest()
	capabilities := snapshot("openai_chat_compatible", FeatureEndpointChat, FeatureReasoningEffort)
	plan, err := PlanAttempt(Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"}, capabilities, request, 1, "reasoning-model")
	if err != nil || plan == nil || plan.Native || plan.UpstreamProtocol.Family != ProtocolOpenAIChat {
		t.Fatalf("reasoning output should be preserved by the Responses-to-Chat bridge, plan=%+v err=%v", plan, err)
	}

	request.Reasoning.Effort = "high"
	capabilities.Features.Add(FeatureEndpointResponses)
	native, err := PlanAttempt(Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"}, capabilities, request, 1, "reasoning-model")
	if err != nil || native == nil || !native.Native {
		t.Fatalf("native Responses reasoning request should remain supported: plan=%+v err=%v", native, err)
	}
}

func TestPlanAttemptCatalogFlagsAreAdvisoryOnlyExplicitDenialRejects(t *testing.T) {
	// A catalog row that merely lacks a flag (strict schema, web_search, ...)
	// is unknown, not unsupported: the provider decides. Only an operator's
	// explicit channel denial rejects before the request leaves the gateway.
	request := minimalRequest()
	request.Tools = []ToolDefinition{{Type: "function", Name: "lookup", Strict: Some(true)}, {Type: "web_search"}}
	capabilities := snapshot("openai", FeatureEndpointChat, FeatureEndpointResponses)
	client := Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"}
	plan, err := PlanAttempt(client, capabilities, request, 1, "gpt-5.6")
	if err != nil || plan == nil || !plan.Native {
		t.Fatalf("missing catalog flags must not reject a native request, plan=%+v err=%v", plan, err)
	}

	capabilities.Denied.Add(FeatureFunctionTools)
	_, err = PlanAttempt(client, capabilities, request, 1, "gpt-5.6")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureFunctionTools || unsupported.Path != "tools[0]" {
		t.Fatalf("explicit channel denial should reject the tool request, got %v", err)
	}
}

func TestPlanAttemptEnforcesMaxOutputConstraint(t *testing.T) {
	request := minimalRequest()
	request.Sampling.MaxOutput = Some(uint(4097))
	capabilities := snapshot("openai", FeatureEndpointResponses, FeatureMaxOutput)
	capabilities.Constraints = map[FeatureID]Constraint{FeatureMaxOutput: {Max: 4096}}
	_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"}, capabilities, request, 1, "test-model")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureMaxOutput || unsupported.Path != "max_output_tokens" {
		t.Fatalf("expected max output constraint failure, got %v", err)
	}
}

func TestPlanAttemptEnforcesInputContextConstraint(t *testing.T) {
	request := minimalRequest()
	capabilities := snapshot("openai", FeatureEndpointResponses, FeatureInputText)
	capabilities.Constraints = map[FeatureID]Constraint{FeatureInputText: {Max: 4096}}
	_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"}, capabilities, request, 1, "test-model", 4097)
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureInputText || unsupported.Path != "input" {
		t.Fatalf("expected input context constraint failure, got %v", err)
	}
}

func TestPlanAttemptRejectsResponsesFileURLToChatBridge(t *testing.T) {
	request := minimalRequest()
	request.Conversation[0].Content = append(request.Conversation[0].Content, ContentPart{Kind: ContentFile, URL: "https://files.example.test/input.pdf"})
	capabilities := snapshot("openai_chat_compatible", FeatureEndpointChat, FeatureInputFile)
	_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"}, capabilities, request, 1, "test-model")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureInputFile || unsupported.Path != "input[0].content[1].file_url" {
		t.Fatalf("expected file URL bridge rejection, got %v", err)
	}
}

func TestPlanAttemptRejectsResponsesImageFileIDToChatBridge(t *testing.T) {
	request := minimalRequest()
	request.Conversation[0].Content = append(request.Conversation[0].Content, ContentPart{Kind: ContentImage, FileID: "file-image"})
	capabilities := snapshot("openai_chat_compatible", FeatureEndpointChat, FeatureInputImage)
	_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"}, capabilities, request, 1, "test-model")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureInputImage || unsupported.Path != "input[0].content[1].file_id" {
		t.Fatalf("expected image file reference rejection, got %v", err)
	}
}

func TestPlanAttemptInstructionRoleCapability(t *testing.T) {
	t.Run("responses instructions bridge to portable system role", func(t *testing.T) {
		request := minimalRequest()
		request.Instructions = []Instruction{{Role: "system", Text: "Be concise"}}
		plan, err := PlanAttempt(
			Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"},
			snapshot("cloudflare_ai_v1", FeatureEndpointChat),
			request, 1, "test-model",
		)
		if err != nil || plan.Native || plan.UpstreamProtocol.Family != ProtocolOpenAIChat {
			t.Fatalf("portable instructions should bridge to Cloudflare Chat: plan=%+v err=%v", plan, err)
		}
	})

	t.Run("developer role is forwarded natively and the provider decides", func(t *testing.T) {
		request := minimalRequest()
		request.Instructions = []Instruction{{Role: "developer", Text: "Be concise"}}
		plan, err := PlanAttempt(
			Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"},
			snapshot("cloudflare_ai_v1", FeatureEndpointChat),
			request, 1, "test-model",
		)
		if err != nil || plan == nil || !plan.Native {
			t.Fatalf("a native Chat relay must not reject the developer role on the gateway's behalf: plan=%+v err=%v", plan, err)
		}

		openAI := snapshot("openai", FeatureEndpointChat, FeatureDeveloperRole)
		plan, err = PlanAttempt(
			Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"},
			openAI, request, 1, "test-model",
		)
		if err != nil || !plan.Native {
			t.Fatalf("native OpenAI Chat should preserve developer role: plan=%+v err=%v", plan, err)
		}
	})
}

func TestPlanAttemptPrefersNativeAndPreservesUnknownFields(t *testing.T) {
	request := minimalRequest()
	request.Raw.UnknownFields = map[string]json.RawMessage{"new_field": json.RawMessage(`true`)}
	plan, err := PlanAttempt(
		Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"},
		snapshot("openai", FeatureEndpointChat, FeatureEndpointResponses),
		request, 1, "test-model",
	)
	if err != nil {
		t.Fatalf("native request should preserve unknown fields: %v", err)
	}
	if !plan.Native || plan.UpstreamProtocol.Family != ProtocolOpenAIResponses {
		t.Fatalf("expected native Responses route, got %+v", plan)
	}
}

func TestPlanAttemptRejectsUnsafeBridgeFeaturesAndNonOpenAIWire(t *testing.T) {
	tests := []struct {
		name     string
		request  *Request
		client   ProtocolFamily
		dialect  string
		endpoint FeatureID
		path     string
	}{
		{name: "unknown", request: func() *Request {
			r := minimalRequest()
			r.Raw.UnknownFields = map[string]json.RawMessage{"future": json.RawMessage(`1`)}
			return r
		}(), client: ProtocolOpenAIResponses, dialect: "cloudflare_ai_v1", endpoint: FeatureEndpointChat, path: "future"},
		{name: "include", request: func() *Request {
			r := minimalRequest()
			r.Lifecycle.Include = []string{"message.output_text.logprobs"}
			return r
		}(), client: ProtocolOpenAIResponses, dialect: "cloudflare_ai_v1", endpoint: FeatureEndpointChat, path: "include[0]"},
		{name: "chat stop", request: func() *Request { r := minimalRequest(); r.Sampling.Stop = Some(json.RawMessage(`"END"`)); return r }(), client: ProtocolOpenAIChat, dialect: "openai", endpoint: FeatureEndpointResponses, path: "stop"},
		{name: "anthropic wire", request: minimalRequest(), client: ProtocolOpenAIResponses, dialect: "anthropic", endpoint: FeatureEndpointChat, path: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := PlanAttempt(Protocol{Family: test.client, Dialect: "openai"}, snapshot(test.dialect, test.endpoint), test.request, 1, "test-model")
			var unsupported *UnsupportedFeatureError
			if !errors.As(err, &unsupported) || unsupported.Path != test.path {
				t.Fatalf("expected unsupported path %q, got %v", test.path, err)
			}
		})
	}
}

func TestPlanAttemptNamesOpaqueInputItemWhenBridgingToChat(t *testing.T) {
	// Codex (ChatGPT desktop / codex CLI) opens every Responses request with
	// an "additional_tools" item. A Chat-only channel must reject the bridge
	// with the real wire type, not by mislabeling it as an item_reference.
	request := minimalRequest()
	request.Conversation = append([]InputItem{{Kind: InputOpaque, WireType: "additional_tools", Opaque: []OpaqueValue{{Dialect: "openai", Value: []byte(`{"type":"additional_tools","role":"developer","tools":[]}`)}}}}, request.Conversation...)
	client := Protocol{Family: ProtocolOpenAIResponses, Dialect: "openai"}

	_, err := PlanAttempt(client, snapshot("openai_chat_compatible", FeatureEndpointChat), request, 1, "gpt-5.6")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureOpaqueInputItem || unsupported.Path != "input[0]" {
		t.Fatalf("opaque input item must fail the Chat bridge at input[0], got %v", err)
	}
	if !strings.Contains(err.Error(), `"additional_tools"`) || strings.Contains(err.Error(), "input.reference") {
		t.Fatalf("bridge rejection must name the wire type instead of item_reference: %v", err)
	}

	plan, err := PlanAttempt(client, snapshot("openai", FeatureEndpointChat, FeatureEndpointResponses), request, 1, "gpt-5.6")
	if err != nil || plan == nil || !plan.Native {
		t.Fatalf("opaque input items must stay native-relayable on a Responses channel, plan=%+v err=%v", plan, err)
	}
}

func deepseekSnapshot(withResponses bool) CapabilitySnapshot {
	endpoints := []FeatureID{FeatureEndpointChat}
	if withResponses {
		endpoints = append(endpoints, FeatureEndpointResponses)
	}
	snapshot := snapshot("deepseek", endpoints...)
	snapshot.Features.Add(FeatureJSONSchema)
	snapshot.Features.Add(FeatureJSONOutput)
	snapshot.Features.Add(FeatureFunctionTools)
	snapshot.Features.Add(FeatureReasoningEffort)
	snapshot.EndpointGaps = map[FeatureID]FeatureSet{FeatureEndpointChat: FeatureSet(1)<<FeatureJSONSchema | FeatureSet(1)<<FeatureStrictSchema}
	return snapshot
}

func schemaChatRequest() *Request {
	request := minimalRequest()
	request.Output = OutputConstraint{Type: "json_schema", Name: "session_title", Schema: json.RawMessage(`{"type":"object"}`)}
	return request
}

// Hermes' title request: Chat + json_schema to DeepSeek, no tools. The Chat
// endpoint is native but cannot carry the schema; Responses can, losslessly.
func TestPlanAttemptRoutesAGappedFeatureThroughTheOtherEndpoint(t *testing.T) {
	plan, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, deepseekSnapshot(true), schemaChatRequest(), 1, "deepseek-flash")
	if err != nil {
		t.Fatalf("expected a Responses bridge, got %v", err)
	}
	if plan.Native || plan.UpstreamProtocol.Family != ProtocolOpenAIResponses {
		t.Fatalf("expected Chat->Responses bridge, got %+v", plan)
	}
	if len(plan.Warnings) != 1 || plan.Warnings[0].Message != "endpoint_bridge:output.json_schema" || plan.Warnings[0].Path != "response_format" {
		t.Fatalf("expected an endpoint_bridge warning, got %+v", plan.Warnings)
	}
	// without the feature the native endpoint is still the route
	plain, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, deepseekSnapshot(true), minimalRequest(), 1, "deepseek-flash")
	if err != nil || !plain.Native {
		t.Fatalf("a request without the gapped feature stays native: %+v %v", plain, err)
	}
}

// With tools and reasoning on, the Chat->Responses bridge is not lossless, so
// the gapped feature cannot be routed: the planner refuses, naming it.
func TestPlanAttemptRefusesAGappedFeatureWhenTheBridgeIsLossy(t *testing.T) {
	request := schemaChatRequest()
	request.Tools = []ToolDefinition{{Type: "function", Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}
	request.Reasoning.Effort = "high"
	_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, deepseekSnapshot(true), request, 1, "deepseek-flash")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureJSONSchema || unsupported.Path != "response_format" {
		t.Fatalf("expected a json_schema refusal, got %v", err)
	}
	// reasoning off makes the bridge lossless again (Hermes sends effort none)
	request.Reasoning.Effort = "none"
	plan, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, deepseekSnapshot(true), request, 1, "deepseek-flash")
	if err != nil || plan.Native || plan.UpstreamProtocol.Family != ProtocolOpenAIResponses {
		t.Fatalf("expected a bridge with reasoning off, got %+v %v", plan, err)
	}
	// no other endpoint at all: refused as well (a degradation rule would
	// have run before planning)
	_, err = PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, deepseekSnapshot(false), schemaChatRequest(), 1, "deepseek-flash")
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureJSONSchema {
		t.Fatalf("expected a refusal without a second endpoint, got %v", err)
	}
}

func anthropicSnapshot() CapabilitySnapshot {
	snapshot := snapshot("anthropic", FeatureEndpointMessages)
	snapshot.Features.Add(FeatureFunctionTools)
	snapshot.Features.Add(FeatureReasoningEffort)
	snapshot.Features.Add(FeatureJSONSchema)
	snapshot.Features.Add(FeatureJSONOutput)
	return snapshot
}

// A Chat or Responses client on an Anthropic channel is bridged to the
// Messages API; a Claude client is native there.
func TestPlanAttemptBridgesOpenAIClientsToMessages(t *testing.T) {
	for _, family := range []ProtocolFamily{ProtocolOpenAIChat, ProtocolOpenAIResponses} {
		request := minimalRequest()
		request.Tools = []ToolDefinition{{Type: "function", Name: "lookup"}}
		request.Reasoning.Effort = "high"
		plan, err := PlanAttempt(Protocol{Family: family, Dialect: "openai"}, anthropicSnapshot(), request, 1, "claude-haiku-5-5")
		if err != nil {
			t.Fatalf("%s: %v", family, err)
		}
		if plan.Native || plan.UpstreamProtocol.Family != ProtocolAnthropic || plan.UpstreamProtocol.Dialect != "anthropic" || plan.RequestCodec != string(ProtocolAnthropic) || plan.ResponseEmitter != string(family) {
			t.Fatalf("%s: expected a Messages bridge, got %+v", family, plan)
		}
	}
	native, err := PlanAttempt(Protocol{Family: ProtocolAnthropic, Dialect: "anthropic"}, anthropicSnapshot(), minimalRequest(), 1, "claude-haiku-5-5")
	if err != nil || !native.Native {
		t.Fatalf("a Claude client is native on an Anthropic channel: %+v %v", native, err)
	}
}

func TestPlanAttemptMessagesBridgeRefusesWhatMessagesCannotCarry(t *testing.T) {
	cases := map[string]func(*Request){
		"json_object": func(r *Request) { r.Output = OutputConstraint{Type: "json_object"} },
		"logprobs":    func(r *Request) { r.Sampling.LogProbs = Some(true) },
		"seed":        func(r *Request) { r.Sampling.Seed = Some[int64](7) },
		"n=2":         func(r *Request) { r.Sampling.N = Some(2) },
		"store=true":  func(r *Request) { r.Lifecycle.Store = Some(true) },
	}
	for name, mutate := range cases {
		request := minimalRequest()
		mutate(request)
		_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, anthropicSnapshot(), request, 1, "claude-haiku-5-5")
		var unsupported *UnsupportedFeatureError
		if !errors.As(err, &unsupported) {
			t.Fatalf("%s: expected a refusal, got %v", name, err)
		}
	}
	// what Messages carries: json_schema, stop sequences, top_k, a developer instruction
	request := minimalRequest()
	request.Output = OutputConstraint{Type: "json_schema", Name: "t", Schema: json.RawMessage(`{"type":"object"}`)}
	request.Sampling.Stop = Some(json.RawMessage(`["END"]`))
	request.Sampling.TopK = Some(5)
	request.Instructions = []Instruction{{Role: "developer", Text: "be brief"}}
	if _, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, anthropicSnapshot(), request, 1, "claude-haiku-5-5"); err != nil {
		t.Fatalf("Messages carries these losslessly: %v", err)
	}
}

// Anthropic's cache markers and speed tier go to Messages and nowhere else.
func TestPlanAttemptAnthropicOptionsOnlyBridgeToMessages(t *testing.T) {
	request := minimalRequest()
	request.Conversation[0].Content[0].CacheControl = json.RawMessage(`{"type":"ephemeral"}`)
	if _, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, anthropicSnapshot(), request, 1, "claude-haiku-5-5"); err != nil {
		t.Fatalf("Messages carries cache_control: %v", err)
	}
	responsesOnly := snapshot("openai", FeatureEndpointResponses)
	_, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, responsesOnly, request, 1, "gpt-5")
	var unsupported *UnsupportedFeatureError
	if !errors.As(err, &unsupported) || unsupported.Feature != FeatureAnthropicOptions || unsupported.Path != "input[0].content[0].cache_control" {
		t.Fatalf("a Responses bridge refuses cache_control by name: %v", err)
	}
	native := snapshot("openai", FeatureEndpointChat)
	if _, err := PlanAttempt(Protocol{Family: ProtocolOpenAIChat, Dialect: "openai"}, native, request, 1, "gpt-5"); err != nil {
		t.Fatalf("a native relay passes its own wire: %v", err)
	}
}
