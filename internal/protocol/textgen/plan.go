package textgen

import (
	"fmt"
	"strings"
)

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
	// EndpointGaps lists, per native endpoint (FeatureEndpointChat,
	// FeatureEndpointResponses), the features of Features that endpoint does
	// NOT carry although another endpoint of the same dialect does: DeepSeek's
	// Chat API takes no json_schema while its Responses API does. A gap is a
	// known fact about the dialect, so the planner routes around it (the
	// other endpoint, through a lossless bridge) or, when no endpoint carries
	// the feature, treats it as unsupported; see FeaturesOn and
	// UnsupportedEverywhere.
	EndpointGaps map[FeatureID]FeatureSet
}

// FeaturesOn is what the channel carries through endpoint: Features minus
// that endpoint's gaps.
func (s CapabilitySnapshot) FeaturesOn(endpoint FeatureID) FeatureSet {
	return s.Features &^ s.EndpointGaps[endpoint]
}

// endpoints lists the native endpoints the snapshot has.
func (s CapabilitySnapshot) endpoints() []FeatureID {
	var out []FeatureID
	for _, endpoint := range []FeatureID{FeatureEndpointChat, FeatureEndpointResponses, FeatureEndpointMessages} {
		if s.Features.Has(endpoint) {
			out = append(out, endpoint)
		}
	}
	return out
}

// endpointOf is the native endpoint a client protocol speaks.
func endpointOf(family ProtocolFamily) FeatureID {
	switch family {
	case ProtocolOpenAIResponses:
		return FeatureEndpointResponses
	case ProtocolAnthropic:
		return FeatureEndpointMessages
	}
	return FeatureEndpointChat
}

// familyOf is the protocol family served at a native endpoint.
func familyOf(endpoint FeatureID) ProtocolFamily {
	switch endpoint {
	case FeatureEndpointResponses:
		return ProtocolOpenAIResponses
	case FeatureEndpointMessages:
		return ProtocolAnthropic
	}
	return ProtocolOpenAIChat
}

// UnsupportedEverywhere is the part of required that no attempt on this
// channel can preserve: what the operator explicitly denied, and what every
// native endpoint of the dialect lacks. It is what a degradation rule
// (degrade.go) may stand in for; anything else is left to the planner.
func (s CapabilitySnapshot) UnsupportedEverywhere(required FeatureSet) FeatureSet {
	unsupported := required & s.Denied
	endpoints := s.endpoints()
	if len(endpoints) == 0 || len(s.EndpointGaps) == 0 {
		return unsupported
	}
	gappedEverywhere := required
	for _, endpoint := range endpoints {
		gappedEverywhere &= s.EndpointGaps[endpoint]
	}
	return unsupported | gappedEverywhere
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

// DegradationPrefix starts the message of a plan warning that records a
// registered degradation (textgen/degrade.go).
const DegradationPrefix = "degraded:"

// Degradations are the IDs of the registered degradations the plan applied,
// in order; none for a lossless plan. Routing ranks a degraded plan below
// every lossless one, and the handlers encode it from the IR, since the
// client's wire still carries the feature that was stood in for.
func (p *RelayPlan) Degradations() []string {
	if p == nil {
		return nil
	}
	var ids []string
	for _, warning := range p.Warnings {
		if strings.HasPrefix(warning.Message, DegradationPrefix) {
			ids = append(ids, strings.TrimPrefix(warning.Message, DegradationPrefix))
		}
	}
	return ids
}

// Degraded says whether the plan applied a registered degradation.
func (p *RelayPlan) Degraded() bool { return len(p.Degradations()) > 0 }

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
	endpoint := endpointOf(client.Family)
	if snapshot.Features.Has(endpoint) {
		if err := requireFeatures(snapshot, features, uses); err != nil {
			return nil, err
		}
		// Native, unless the native endpoint is known to lack a feature this
		// request uses that the dialect's other endpoint carries: then the
		// lossless bridge to that endpoint is the faithful route.
		gapped := features & snapshot.EndpointGaps[endpoint]
		if gapped == 0 {
			upstreamProtocol := client
			upstreamProtocol.Dialect = snapshot.Dialect
			return &RelayPlan{ClientProtocol: client, UpstreamProtocol: upstreamProtocol, RequestCodec: string(client.Family), ResponseEmitter: string(client.Family), Native: true, CapabilityRev: snapshot.Revision, Features: features, ChannelID: channelID, UpstreamModel: upstreamModel}, nil
		}
		for _, other := range snapshot.endpoints() {
			if other == endpoint || features&snapshot.EndpointGaps[other] != 0 {
				continue
			}
			if plan, err := planBridge(client, familyOf(other), snapshot, request, features, uses, channelID, upstreamModel); err == nil {
				plan.Warnings = append(plan.Warnings, Issue{Feature: firstFeature(gapped), Path: firstPath(uses, gapped), Message: "endpoint_bridge:" + strings.Join(gapped.Names(), ",")})
				return plan, nil
			}
		}
		return nil, &UnsupportedFeatureError{Feature: firstFeature(gapped), Path: firstPath(uses, gapped), Reason: fmt.Sprintf("the channel's %s endpoint does not carry this feature and no lossless route to another endpoint exists", endpoint.String())}
	}
	// No native endpoint: the first lossless one-hop bridge, in the order
	// the bridges are known to be faithful.
	var firstErr error
	for _, upstream := range bridgeOrder(client.Family) {
		if !snapshot.Features.Has(endpointOf(upstream)) {
			continue
		}
		plan, err := planBridge(client, upstream, snapshot, request, features, uses, channelID, upstreamModel)
		if err == nil {
			return plan, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, &UnsupportedFeatureError{Feature: endpoint, Path: "", Reason: "channel has no native endpoint and no lossless bridge target"}
}

// bridgeOrder lists the upstream protocols a client protocol may be bridged
// to, preferred first: the OpenAI sibling, then Anthropic's Messages.
func bridgeOrder(client ProtocolFamily) []ProtocolFamily {
	switch client {
	case ProtocolOpenAIChat:
		return []ProtocolFamily{ProtocolOpenAIResponses, ProtocolAnthropic}
	case ProtocolOpenAIResponses:
		return []ProtocolFamily{ProtocolOpenAIChat, ProtocolAnthropic}
	}
	return nil
}

// planBridge plans the one-hop bridge from the client protocol to upstream,
// or fails closed.
func planBridge(client Protocol, upstream ProtocolFamily, snapshot CapabilitySnapshot, request *Request, features FeatureSet, uses []FeatureUse, channelID int, upstreamModel string) (*RelayPlan, error) {
	endpoint := endpointOf(client.Family)
	if upstream == "" || upstream == client.Family || !snapshot.Features.Has(endpointOf(upstream)) {
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

// firstFeature is the lowest feature of set, for naming an error or warning.
func firstFeature(set FeatureSet) FeatureID {
	for id := FeatureID(0); id < 64; id++ {
		if set.Has(id) {
			return id
		}
	}
	return FeatureUnknownField
}

// firstPath is the field path the request used a feature of set at, "" when
// the uses carry none.
func firstPath(uses []FeatureUse, set FeatureSet) string {
	for _, use := range uses {
		if set.Has(use.Feature) {
			return use.Path
		}
	}
	return ""
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
	case "openai", "azure_openai", "openrouter", "cloudflare_ai_v1", "openai_chat_compatible", "deepseek":
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
	if upstream == ProtocolAnthropic {
		return messagesBridgeIssue(request, uses)
	}
	// Anthropic's cache markers are advisory and fall out of other encoders;
	// its speed tier is not, and has no equivalent elsewhere.
	if path, ok := request.SpeedPath(); ok {
		return &Issue{Feature: FeatureAnthropicOptions, Path: path, Message: "Anthropic's speed tier has no equivalent outside the Messages API"}
	}
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
		case FeatureStop, FeatureSeed, FeatureTopK, FeatureInputAudio, FeatureReasoningBudget:
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

// messagesBridgeIssue is what a Chat or Responses request cannot carry to
// Anthropic's Messages API losslessly. Instructions of either role become
// the system prompt; stop sequences, top_k and a JSON schema have their own
// fields; web search is Anthropic's own tool. Everything stateful, opaque
// or sampled in a way Messages does not take is refused.
func messagesBridgeIssue(request *Request, uses []FeatureUse) *Issue {
	for _, use := range uses {
		switch use.Feature {
		case FeatureUnknownField, FeaturePreviousResponse, FeatureConversation, FeaturePromptTemplate, FeatureIncludeEncryptedReasoning, FeatureIncludeUnsupported, FeatureHostedToolNativeOnly, FeatureOpaqueReasoningItem, FeatureInputReference, FeatureOpaqueInputItem:
			return &Issue{Feature: use.Feature, Path: use.Path, Message: "feature cannot be preserved by the stateless protocol bridge"}
		case FeatureLogProbs, FeatureSeed, FeatureInputAudio, FeatureInputFile:
			return &Issue{Feature: use.Feature, Path: use.Path, Message: "Messages has no lossless equivalent"}
		case FeatureJSONOutput:
			return &Issue{Feature: use.Feature, Path: use.Path, Message: "Messages takes a JSON schema, not a bare JSON object format"}
		case FeatureN:
			if !request.Sampling.N.Set || request.Sampling.N.Value != 1 {
				return &Issue{Feature: use.Feature, Path: use.Path, Message: "a Messages bridge supports exactly one output"}
			}
		}
	}
	if request != nil && request.Lifecycle.Store.Set && request.Lifecycle.Store.Value {
		return &Issue{Feature: FeatureStore, Path: "store", Message: "explicit store=true cannot be preserved by a stateless Messages upstream"}
	}
	return nil
}
