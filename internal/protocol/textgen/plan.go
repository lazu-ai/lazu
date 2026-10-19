package textgen

import "fmt"

type Constraint struct {
	Max    int64
	Values []string
}

type CapabilitySnapshot struct {
	Dialect  string
	Revision string
	// Features is what the channel and model catalog are KNOWN to support. It
	// drives bridge-fidelity decisions (which upstream protocol, which
	// warnings) but is not a permission list: a model row that simply lacks a
	// flag is unknown, not unsupported.
	Features FeatureSet
	// Denied is what the operator EXPLICITLY declared unsupported on the
	// channel (supports_tools=false, supports_vision=false, ...). Only these
	// reject a request before it reaches the provider; everything else is the
	// provider's call, exactly as it was before the protocol planner existed.
	Denied      FeatureSet
	Constraints map[FeatureID]Constraint
}

type Issue struct {
	Feature FeatureID `json:"feature"`
	Path    string    `json:"path"`
	Message string    `json:"message"`
}

type RelayPlan struct {
	ClientProtocol   Protocol
	UpstreamProtocol Protocol
	RequestCodec     string
	ResponseEmitter  string
	Native           bool
	CapabilityRev    string
	Features         FeatureSet
	Warnings         []Issue
	ChannelID        int
	UpstreamModel    string
}

type UnsupportedFeatureError struct {
	Feature FeatureID
	Path    string
	Reason  string
}

func (e *UnsupportedFeatureError) Error() string {
	return fmt.Sprintf("unsupported feature %s at %s: %s", e.Feature.String(), e.Path, e.Reason)
}

func PlanAttempt(client Protocol, snapshot CapabilitySnapshot, request *Request, channelID int, upstreamModel string, inputTokens ...int) (*RelayPlan, error) {
	features, uses := ExtractFeatures(request)
	inputTokenEstimate := 0
	if len(inputTokens) > 0 {
		inputTokenEstimate = inputTokens[0]
	}
	if err := requireConstraints(snapshot, request, inputTokenEstimate); err != nil {
		return nil, err
	}
	endpoint := FeatureEndpointChat
	if client.Family == ProtocolOpenAIResponses {
		endpoint = FeatureEndpointResponses
	}
	if snapshot.Features.Has(endpoint) {
		if err := requireFeatures(snapshot, features, uses); err != nil {
			return nil, err
		}
		upstreamProtocol := client
		upstreamProtocol.Dialect = snapshot.Dialect
		return &RelayPlan{ClientProtocol: client, UpstreamProtocol: upstreamProtocol, RequestCodec: string(client.Family), ResponseEmitter: string(client.Family), Native: true, CapabilityRev: snapshot.Revision, Features: features, ChannelID: channelID, UpstreamModel: upstreamModel}, nil
	}
	var upstream ProtocolFamily
	switch client.Family {
	case ProtocolOpenAIChat:
		if snapshot.Features.Has(FeatureEndpointResponses) {
			upstream = ProtocolOpenAIResponses
		}
	case ProtocolOpenAIResponses:
		if snapshot.Features.Has(FeatureEndpointChat) {
			upstream = ProtocolOpenAIChat
		}
	}
	if upstream == "" {
		return nil, &UnsupportedFeatureError{Feature: endpoint, Path: "", Reason: "channel has no native endpoint and no lossless bridge target"}
	}
	if client.Family == ProtocolOpenAIResponses && upstream == ProtocolOpenAIChat && !supportsOpenAIChatWireBridge(snapshot.Dialect) {
		return nil, &UnsupportedFeatureError{Feature: endpoint, Path: "", Reason: "channel transport is not an OpenAI-compatible chat wire dialect"}
	}
	// With reasoning off there is no reasoning state for Chat to lose, so the
	// bridge stays lossless for tool calls.
	if client.Family == ProtocolOpenAIChat && upstream == ProtocolOpenAIResponses && snapshot.Features.Has(FeatureReasoningEffort) &&
		request.Reasoning.Effort != "none" {
		toolMode := request.ToolPolicy.Mode
		if toolMode == "" {
			toolMode = "auto"
		}
		if len(request.Tools) > 0 && toolMode != "none" {
			return nil, &UnsupportedFeatureError{
				Feature: FeatureFunctionTools,
				Path:    "tool_choice",
				Reason:  "reasoning-capable Responses models require reasoning state that Chat cannot preserve across tool-call continuation",
			}
		}
		for index, item := range request.Conversation {
			if item.Kind == InputFunctionCall {
				return nil, &UnsupportedFeatureError{
					Feature: FeatureFunctionTools,
					Path:    fmt.Sprintf("input[%d]", index),
					Reason:  "reasoning-capable Responses models require prior reasoning items for tool-call continuation",
				}
			}
		}
	}
	if issue := bridgeIssue(client.Family, upstream, request, uses); issue != nil {
		return nil, &UnsupportedFeatureError{Feature: issue.Feature, Path: issue.Path, Reason: issue.Message}
	}
	if err := requireFeatures(snapshot, features, uses); err != nil {
		return nil, err
	}
	warnings := []Issue(nil)
	if client.Family == ProtocolOpenAIResponses && !request.Lifecycle.Store.Set {
		warnings = append(warnings, Issue{Path: "store", Message: "stateless_bridge"})
	}
	return &RelayPlan{ClientProtocol: client, UpstreamProtocol: Protocol{Family: upstream, Dialect: snapshot.Dialect}, RequestCodec: string(upstream), ResponseEmitter: string(client.Family), Native: false, CapabilityRev: snapshot.Revision, Features: features, Warnings: warnings, ChannelID: channelID, UpstreamModel: upstreamModel}, nil
}

func requireConstraints(snapshot CapabilitySnapshot, request *Request, inputTokens int) error {
	if inputTokens > 0 {
		if constraint, ok := snapshot.Constraints[FeatureInputText]; ok && constraint.Max > 0 && int64(inputTokens) > constraint.Max {
			return &UnsupportedFeatureError{
				Feature: FeatureInputText,
				Path:    "input",
				Reason:  fmt.Sprintf("estimated input tokens %d exceed channel context limit %d", inputTokens, constraint.Max),
			}
		}
	}
	if request == nil || !request.Sampling.MaxOutput.Set {
		return nil
	}
	constraint, ok := snapshot.Constraints[FeatureMaxOutput]
	if !ok || constraint.Max <= 0 || uint64(request.Sampling.MaxOutput.Value) <= uint64(constraint.Max) {
		return nil
	}
	return &UnsupportedFeatureError{
		Feature: FeatureMaxOutput,
		Path:    "max_output_tokens",
		Reason:  fmt.Sprintf("requested value %d exceeds channel limit %d", request.Sampling.MaxOutput.Value, constraint.Max),
	}
}

func supportsOpenAIChatWireBridge(dialect string) bool {
	switch dialect {
	case "openai", "azure_openai", "openrouter", "cloudflare_ai_v1", "openai_chat_compatible":
		return true
	default:
		return false
	}
}

// requireFeatures rejects only features the channel explicitly denies. The
// model catalog is advisory: its flags are synced from third parties or typed
// by hand, and a missing flag (for example supports_web_search on a fresh
// gpt-5.x row) must not turn into a 400 that the provider itself would never
// have returned.
func requireFeatures(snapshot CapabilitySnapshot, required FeatureSet, uses []FeatureUse) error {
	denied := required & snapshot.Denied
	if denied == 0 {
		return nil
	}
	for _, use := range uses {
		if denied.Has(use.Feature) {
			return &UnsupportedFeatureError{Feature: use.Feature, Path: use.Path, Reason: "channel explicitly declares this feature unsupported"}
		}
	}
	return &UnsupportedFeatureError{Reason: "channel explicitly declares a required feature unsupported"}
}

func bridgeIssue(client, upstream ProtocolFamily, request *Request, uses []FeatureUse) *Issue {
	for _, use := range uses {
		switch use.Feature {
		case FeatureUnknownField, FeaturePreviousResponse, FeatureConversation, FeaturePromptTemplate, FeatureIncludeEncryptedReasoning, FeatureIncludeUnsupported, FeatureHostedWebSearch, FeatureHostedToolNativeOnly, FeatureOpaqueReasoningItem, FeatureInputReference:
			return &Issue{Feature: use.Feature, Path: use.Path, Message: "feature cannot be preserved by the stateless protocol bridge"}
		case FeatureOpaqueInputItem:
			itemType := use.Detail
			if itemType == "" {
				itemType = "unknown"
			}
			return &Issue{Feature: use.Feature, Path: use.Path, Message: fmt.Sprintf("input item type %q has no Chat Completions equivalent and cannot be preserved by the stateless protocol bridge; this model's upstream channel only exposes Chat Completions", itemType)}
		case FeatureLogProbs:
			return &Issue{Feature: use.Feature, Path: use.Path, Message: "log probabilities cannot yet be preserved in the bridged response"}
		case FeatureDeveloperRole:
			if upstream == ProtocolOpenAIResponses {
				return &Issue{Feature: use.Feature, Path: use.Path, Message: "Responses instructions cannot preserve the Chat developer-role authority boundary"}
			}
		case FeatureN:
			if !request.Sampling.N.Set || request.Sampling.N.Value != 1 {
				return &Issue{Feature: use.Feature, Path: use.Path, Message: "a Responses bridge supports exactly one output"}
			}
		case FeatureStop, FeatureSeed, FeatureTopK, FeatureInputAudio:
			if upstream == ProtocolOpenAIResponses {
				return &Issue{Feature: use.Feature, Path: use.Path, Message: "Responses has no lossless equivalent"}
			}
		case FeatureReasoningSummary:
			if upstream == ProtocolOpenAIChat {
				return &Issue{Feature: use.Feature, Path: use.Path, Message: "Chat Completions has no lossless equivalent"}
			}
		}
	}
	if client == ProtocolOpenAIChat && upstream == ProtocolOpenAIResponses && request != nil && len(request.Instructions) > 1 {
		return &Issue{Feature: FeatureUnknownField, Path: "instructions", Message: "multiple Chat system-message boundaries cannot be preserved in one Responses instructions field"}
	}
	if client == ProtocolOpenAIResponses && upstream == ProtocolOpenAIChat && request != nil {
		for itemIndex, item := range request.Conversation {
			for partIndex, part := range item.Content {
				if part.Kind == ContentImage && part.FileID != "" {
					return &Issue{Feature: FeatureInputImage, Path: fmt.Sprintf("input[%d].content[%d].file_id", itemIndex, partIndex), Message: "Chat Completions has no lossless image file reference"}
				}
				if part.Kind == ContentFile && part.URL != "" {
					return &Issue{Feature: FeatureInputFile, Path: fmt.Sprintf("input[%d].content[%d].file_url", itemIndex, partIndex), Message: "Chat Completions has no lossless file URL representation"}
				}
			}
		}
	}
	if client == ProtocolOpenAIResponses && request.Lifecycle.Store.Set && request.Lifecycle.Store.Value {
		return &Issue{Feature: FeatureStore, Path: "store", Message: "explicit store=true cannot be preserved by a stateless chat-completions upstream"}
	}
	return nil
}
