package textgen

import (
	"encoding/json"
	"sort"
)

type FeatureID uint8

const (
	FeatureEndpointChat FeatureID = iota
	FeatureEndpointResponses
	FeatureInputText
	FeatureInputImage
	FeatureInputAudio
	FeatureInputFile
	FeatureDeveloperRole
	FeatureFunctionTools
	FeatureParallelTools
	FeatureToolResult
	FeatureHostedWebSearch
	FeatureJSONOutput
	FeatureJSONSchema
	FeatureStrictSchema
	FeatureReasoningEffort
	FeatureReasoningSummary
	FeatureTemperature
	FeatureTopP
	FeatureTopK
	FeatureSeed
	FeatureStop
	FeatureN
	FeatureLogProbs
	FeatureMaxOutput
	FeatureStream
	FeatureStreamUsage
	FeaturePreviousResponse
	FeatureConversation
	FeaturePromptTemplate
	FeatureIncludeEncryptedReasoning
	FeatureIncludeUnsupported
	FeatureOpaqueReasoningItem
	FeatureInputReference
	FeatureStore
	FeatureHostedToolNativeOnly
	FeatureOpaqueInputItem
	FeatureUnknownField
	// FeatureEndpointMessages is Anthropic's Messages API as a native
	// endpoint of the channel.
	FeatureEndpointMessages
	// FeatureReasoningBudget is a thinking budget in tokens.
	FeatureReasoningBudget
	// FeatureAnthropicOptions names Anthropic's speed tier
	// (Request.VendorOptions) in a refusal. It is never extracted or
	// required: a native relay passes its own wire, the Messages encoder
	// writes it, and only a bridge elsewhere refuses it.
	FeatureAnthropicOptions
)

var featureNames = map[FeatureID]string{
	FeatureEndpointChat: "endpoint.chat_completions", FeatureEndpointResponses: "endpoint.responses",
	FeatureInputText: "input.text", FeatureInputImage: "input.image", FeatureInputAudio: "input.audio", FeatureInputFile: "input.file",
	FeatureDeveloperRole: "input.developer_role", FeatureFunctionTools: "tools.function", FeatureParallelTools: "tools.parallel",
	FeatureToolResult: "tools.result", FeatureHostedWebSearch: "tools.web_search", FeatureJSONOutput: "output.json_object",
	FeatureJSONSchema: "output.json_schema", FeatureStrictSchema: "output.json_schema.strict", FeatureReasoningEffort: "reasoning.effort",
	FeatureReasoningSummary: "reasoning.summary", FeatureTemperature: "sampling.temperature", FeatureTopP: "sampling.top_p",
	FeatureTopK: "sampling.top_k", FeatureSeed: "sampling.seed", FeatureStop: "sampling.stop", FeatureN: "sampling.n",
	FeatureLogProbs: "sampling.logprobs", FeatureMaxOutput: "sampling.max_output", FeatureStream: "lifecycle.stream",
	FeatureStreamUsage: "lifecycle.stream_usage", FeaturePreviousResponse: "state.previous_response", FeatureConversation: "state.conversation",
	FeaturePromptTemplate: "state.prompt_template", FeatureIncludeEncryptedReasoning: "include.reasoning.encrypted_content",
	FeatureIncludeUnsupported: "include.unsupported", FeatureOpaqueReasoningItem: "input.reasoning_opaque", FeatureInputReference: "input.reference",
	FeatureStore: "lifecycle.store", FeatureHostedToolNativeOnly: "tools.hosted_native_only", FeatureOpaqueInputItem: "input.opaque_item",
	FeatureEndpointMessages: "endpoint.messages", FeatureReasoningBudget: "reasoning.budget", FeatureAnthropicOptions: "vendor.anthropic",
	FeatureUnknownField: "wire.unknown_field",
}

func (f FeatureID) String() string {
	if name, ok := featureNames[f]; ok {
		return name
	}
	return "unknown"
}

type FeatureSet uint64

func (s FeatureSet) Has(feature FeatureID) bool             { return s&(FeatureSet(1)<<feature) != 0 }
func (s *FeatureSet) Add(feature FeatureID)                 { *s |= FeatureSet(1) << feature }
func (s FeatureSet) Contains(required FeatureSet) bool      { return s&required == required }
func (s FeatureSet) Missing(required FeatureSet) FeatureSet { return required &^ s }
func (s FeatureSet) Names() []string {
	result := make([]string, 0)
	for feature := FeatureID(0); feature <= FeatureUnknownField; feature++ {
		if s.Has(feature) {
			result = append(result, feature.String())
		}
	}
	sort.Strings(result)
	return result
}

type FeatureUse struct {
	Feature FeatureID
	Path    string
	// Detail carries a client-supplied identifier (such as an opaque item's
	// wire type) so planner diagnostics can name it.
	Detail string
}

func ExtractFeatures(request *Request) (FeatureSet, []FeatureUse) {
	var set FeatureSet
	uses := make([]FeatureUse, 0)
	addDetail := func(feature FeatureID, path, detail string) {
		set.Add(feature)
		uses = append(uses, FeatureUse{Feature: feature, Path: path, Detail: detail})
	}
	add := func(feature FeatureID, path string) {
		addDetail(feature, path, "")
	}
	add(FeatureInputText, "input")
	if request == nil {
		return set, uses
	}
	for index, instruction := range request.Instructions {
		if instruction.Role == "developer" {
			add(FeatureDeveloperRole, "instructions["+itoa(index)+"].role")
		}
	}
	for index, item := range request.Conversation {
		if item.Kind == InputMessage && item.Role == "developer" {
			add(FeatureDeveloperRole, "input["+itoa(index)+"].role")
		}
		switch item.Kind {
		case InputFunctionCall:
			add(FeatureFunctionTools, "input["+itoa(index)+"]")
		case InputFunctionResult:
			add(FeatureToolResult, "input["+itoa(index)+"]")
		case InputReasoning:
			if !item.PlainReasoning.Set {
				// the dialect that sealed it, so a bridge can tell reasoning
				// its target could use from reasoning bound to another vendor
				dialect := ""
				if len(item.Opaque) > 0 {
					dialect = item.Opaque[0].Dialect
				}
				addDetail(FeatureOpaqueReasoningItem, "input["+itoa(index)+"]", dialect)
			}
		case InputReference:
			add(FeatureInputReference, "input["+itoa(index)+"]")
		case InputOpaque:
			addDetail(FeatureOpaqueInputItem, "input["+itoa(index)+"]", item.WireType)
		}
		for partIndex, part := range item.Content {
			path := "input[" + itoa(index) + "].content[" + itoa(partIndex) + "]"
			switch part.Kind {
			case ContentImage:
				add(FeatureInputImage, path)
			case ContentAudio:
				add(FeatureInputAudio, path)
			case ContentFile:
				add(FeatureInputFile, path)
			}
		}
	}
	for index, tool := range request.Tools {
		if tool.Type == "function" {
			add(FeatureFunctionTools, "tools["+itoa(index)+"]")
			if tool.Strict.Set && tool.Strict.Value {
				add(FeatureStrictSchema, "tools["+itoa(index)+"].strict")
			}
		} else if tool.Type == "web_search" || tool.Type == "web_search_preview" {
			add(FeatureHostedWebSearch, "tools["+itoa(index)+"]")
		} else {
			// Keep provider-native hosted tools eligible for a native Responses
			// endpoint without pretending they require web-search capability. The
			// bridge planner rejects this marker because Chat has no lossless wire
			// representation for these tool definitions.
			add(FeatureHostedToolNativeOnly, "tools["+itoa(index)+"]")
		}
	}
	if request.ToolPolicy.ParallelToolCalls.Set && request.ToolPolicy.ParallelToolCalls.Value {
		add(FeatureParallelTools, "parallel_tool_calls")
	}
	switch request.Output.Type {
	case "json_object":
		add(FeatureJSONOutput, "response_format")
	case "json_schema":
		add(FeatureJSONSchema, "response_format")
		if request.Output.Strict.Set && request.Output.Strict.Value {
			add(FeatureStrictSchema, "response_format.json_schema.strict")
		}
	}
	if request.Reasoning.Effort != "" {
		add(FeatureReasoningEffort, "reasoning.effort")
	}
	if request.Reasoning.Summary != "" {
		add(FeatureReasoningSummary, "reasoning.summary")
	}
	if request.Reasoning.Budget.Set {
		add(FeatureReasoningBudget, "reasoning.max_tokens")
	}
	if request.Sampling.Temperature.Set {
		add(FeatureTemperature, "temperature")
	}
	if request.Sampling.TopP.Set {
		add(FeatureTopP, "top_p")
	}
	if request.Sampling.TopK.Set {
		add(FeatureTopK, "top_k")
	}
	if request.Sampling.Seed.Set {
		add(FeatureSeed, "seed")
	}
	if request.Sampling.Stop.Set {
		add(FeatureStop, "stop")
	}
	if request.Sampling.N.Set {
		add(FeatureN, "n")
	}
	if (request.Sampling.LogProbs.Set && request.Sampling.LogProbs.Value) || (request.Sampling.TopLogProbs.Set && request.Sampling.TopLogProbs.Value > 0) {
		add(FeatureLogProbs, "logprobs")
	}
	if request.Sampling.MaxOutput.Set {
		add(FeatureMaxOutput, "max_output_tokens")
	}
	if request.Lifecycle.Stream {
		add(FeatureStream, "stream")
	}
	if request.Lifecycle.StreamIncludeUsage.Set && request.Lifecycle.StreamIncludeUsage.Value {
		add(FeatureStreamUsage, "stream_options.include_usage")
	}
	if request.Lifecycle.PreviousResponseID != "" {
		add(FeaturePreviousResponse, "previous_response_id")
	}
	if len(request.Lifecycle.Conversation) > 0 {
		add(FeatureConversation, "conversation")
	}
	if len(request.Lifecycle.PromptTemplate) > 0 {
		add(FeaturePromptTemplate, "prompt")
	}
	for index, include := range request.Lifecycle.Include {
		if include == "reasoning.encrypted_content" {
			add(FeatureIncludeEncryptedReasoning, "include["+itoa(index)+"]")
		} else {
			add(FeatureIncludeUnsupported, "include["+itoa(index)+"]")
		}
	}
	if len(request.Raw.UnknownFields) > 0 {
		add(FeatureUnknownField, firstUnknownPath(request.Raw.UnknownFields))
	}
	return set, uses
}

func firstUnknownPath(fields map[string]json.RawMessage) string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := [20]byte{}
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}
