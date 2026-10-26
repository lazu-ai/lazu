package runtime

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// Attempt is the immutable, channel-specific request and plan. The original
// ParsedRequest remains unchanged so retries always start from client input.
type Attempt struct {
	Request *textgen.Request
	Plan    *textgen.RelayPlan
}

// PrepareAttempt applies the small set of typed semantic channel overrides
// before feature extraction and planning. Arbitrary wire patches remain valid
// for native same-protocol relays, but are rejected for bridges because they
// could introduce semantics the planner never approved.
func PrepareAttempt(parsed *textgen.ParsedRequest, channel *model.Channel, modelName string) (*Attempt, error) {
	if parsed == nil || parsed.Semantic == nil {
		return nil, fmt.Errorf("parsed text-generation request is missing")
	}
	if channel == nil {
		return nil, fmt.Errorf("channel is nil")
	}
	singleUserText := channel.GetOtherSettings().ResponsesSingleUserText
	if singleUserText {
		if _, err := textgen.SingleUserText(parsed.Semantic); err != nil {
			return nil, err
		}
	}
	snapshot, dialect, err := SnapshotForChannel(channel, modelName)
	if err != nil {
		return nil, err
	}
	effective := parsed.Semantic
	override := channel.GetParamOverride()
	unsupportedPatch := ""
	if len(override) > 0 && !OverrideIsUpstreamWire(parsed.Client, snapshot) {
		effective = effective.Clone()
		if unsupportedPatch, err = applySemanticOverrides(effective, override); err != nil {
			return nil, err
		}
	}
	if singleUserText {
		if !snapshot.Features.Has(textgen.FeatureEndpointResponses) || snapshot.Features.Has(textgen.FeatureEndpointChat) {
			return nil, fmt.Errorf("responses_single_user_text requires a Responses-only channel")
		}
		if _, err := textgen.SingleUserText(effective); err != nil {
			return nil, err
		}
	}
	client := parsed.Client
	if client.Family == textgen.ProtocolOpenAIChat && dialect == "openai" && chatToolsNeedReasoningOff(effective, modelName) {
		if effective == parsed.Semantic {
			effective = effective.Clone()
		}
		effective.Reasoning.Effort = "none"
	}
	// Bounded degradation (textgen/degrade.go): a feature this channel cannot
	// carry at all is stood in for by a registered rule, on a clone, before
	// planning; the plan carries the warning. Degrade clones for itself. A
	// degraded request is sent encoded from the IR (the client's wire still
	// carries the feature), so it is degraded only when that encoding loses
	// nothing else: a nested field the IR does not model would be dropped,
	// and the refusal stands instead. Counting happens where the attempt is
	// chosen (controller), not here, since routing plans every candidate.
	var degradations []textgen.Issue
	inputTokens := parsed.InputTokens
	// A Claude client is never degraded: its native path sends the client's
	// own bytes, and Messages has no json_object to stand in with; a denial
	// stands as a pre-flight refusal. A Gemini client's native path sends its
	// own bytes too.
	if !hasNestedUnknownField(effective) && client.Family != textgen.ProtocolAnthropic && client.Family != textgen.ProtocolGemini {
		var degraded *textgen.Request
		degraded, degradations = textgen.Degrade(snapshot, effective)
		if len(degradations) > 0 {
			// the stand-in puts text in the prompt: the context-window
			// check counts it
			// the estimate already counted the schema (TokenCountMeta), so
			// only the words around it are new
			if added := instructionText(degraded)[len(instructionPrefix(effective, degraded)):]; added != "" && inputTokens > 0 {
				extra := EstimateTextTokens(added, modelName) - EstimateTextTokens(string(effective.Output.Schema), modelName)
				if extra > 0 {
					inputTokens += extra
				}
			}
			effective = degraded
		}
	}
	if client.Family == textgen.ProtocolAnthropic {
		client.Dialect = "anthropic"
	} else {
		client.Dialect = "openai"
	}
	plan, err := textgen.PlanAttempt(client, snapshot, effective, channel.Id, modelName, inputTokens)
	if err != nil {
		return nil, err
	}
	plan.UpstreamProtocol.Dialect = dialect
	if chatToolsLoseExplicitReasoning(client, plan, effective, modelName) && snapshot.Features.Has(textgen.FeatureEndpointResponses) {
		// the model takes tools on Chat only with reasoning off, and the
		// Claude client asked for reasoning by name: Responses takes both
		responsesOnly := snapshot
		responsesOnly.Features &^= textgen.FeatureSet(1) << textgen.FeatureEndpointChat
		if replanned, replanErr := textgen.PlanAttempt(client, responsesOnly, effective, channel.Id, modelName, inputTokens); replanErr == nil {
			replanned.UpstreamProtocol.Dialect = dialect
			plan = replanned
		}
	}
	if !plan.Native && (client.Family == textgen.ProtocolAnthropic || client.Family == textgen.ProtocolGemini) &&
		!snapshot.Features.Has(textgen.FeatureReasoningEffort) && effective.Reasoning != (textgen.ReasoningOptions{}) {
		// Claude and Gemini clients can ask for thinking on every request.
		// Another vendor's model takes that as reasoning_effort, which a
		// model that does not reason refuses; the ask goes only to a model
		// the catalog says reasons. The legacy conversions did not send it.
		if effective == parsed.Semantic {
			effective = effective.Clone()
		}
		effective.Reasoning = textgen.ReasoningOptions{}
	}
	if !plan.Native && client.Family == textgen.ProtocolAnthropic && plan.UpstreamProtocol.Family == textgen.ProtocolOpenAIChat &&
		len(effective.Tools) > 0 && effective.ToolPolicy.Mode != "none" && common.ChatToolsNeedReasoningOff(modelName) {
		// a model that takes tools on Chat only with reasoning off: a
		// Claude client's tool call goes without thinking (the legacy
		// conversion sent none), as a Chat client's does
		if effective == parsed.Semantic {
			effective = effective.Clone()
		}
		effective.Reasoning = textgen.ReasoningOptions{Effort: "none"}
	}
	if !plan.Native && client.Family == textgen.ProtocolAnthropic && effective.Reasoning.Effort == "none" && !model.CanDisableReasoning(modelName) {
		// thinking off, asked of a model that cannot stop reasoning: the
		// ask is left out, as an upstream would refuse effort none
		if effective == parsed.Semantic {
			effective = effective.Clone()
		}
		effective.Reasoning.Effort = ""
	}
	if !plan.Native && client.Family == textgen.ProtocolAnthropic && !snapshot.Features.Has(textgen.FeatureInputImage) {
		var omitted []textgen.Issue
		effective, omitted = omitToolResultImages(effective, parsed.Semantic)
		plan.Warnings = append(plan.Warnings, omitted...)
	}
	plan.Warnings = append(plan.Warnings, degradations...)
	if err := validateBridgeOverride(plan.Native, unsupportedPatch); err != nil {
		return nil, err
	}
	return &Attempt{Request: effective, Plan: plan}, nil
}

// omitToolResultImages replaces the images tool results returned with a
// note, for a model the catalog does not say sees images. Claude Code reads
// screenshots with tools; one image in a session's history would make every
// later request to a model that cannot take images fail, where the legacy
// conversion sent the image as text the model could not read either. The
// plan warns once per result (X-Lazu-Warning). Images the user sent are left
// alone, as they always were. The request is cloned before it changes.
func omitToolResultImages(effective, original *textgen.Request) (*textgen.Request, []textgen.Issue) {
	var issues []textgen.Issue
	for index, item := range effective.Conversation {
		if item.Kind != textgen.InputFunctionResult || len(item.Content) == 0 {
			continue
		}
		if effective == original {
			effective = effective.Clone()
		}
		count := 0
		kept := item.Content[:0:0]
		for _, part := range item.Content {
			if part.Kind == textgen.ContentImage {
				count++
				continue
			}
			kept = append(kept, part)
		}
		if count == 0 {
			continue
		}
		result := &effective.Conversation[index]
		result.Content = kept
		note := fmt.Sprintf("[%d image(s) returned by this tool were omitted: the model cannot view images]", count)
		if result.Output == "" {
			result.Output = note
		} else {
			result.Output += "\n" + note
		}
		issues = append(issues, textgen.Issue{Feature: textgen.FeatureInputImage, Path: fmt.Sprintf("input[%d].content", index), Message: "omitted:tool_result_image"})
	}
	return effective, issues
}

// EstimateTextTokens counts the tokens of text for the model. The service
// package sets the tokenizer's count (it depends on this package); the
// default overestimates, which only narrows the context-window check.
var EstimateTextTokens = func(text, _ string) int { return (len(text) + 2) / 3 }

// instructionText is the request's instructions, joined.
func instructionText(request *textgen.Request) string {
	var out strings.Builder
	for _, instruction := range request.Instructions {
		out.WriteString(instruction.Text)
	}
	return out.String()
}

// instructionPrefix is the part of before's instructions after keeps
// unchanged at its start: a degradation only appends.
func instructionPrefix(before, after *textgen.Request) string {
	old, now := instructionText(before), instructionText(after)
	n := 0
	for n < len(old) && n < len(now) && old[n] == now[n] {
		n++
	}
	return now[:n]
}

// chatToolsLoseExplicitReasoning says whether a Claude client's request,
// planned onto Chat, would lose reasoning it asked for by name (an effort
// or a budget, not thinking left adaptive) to a model that takes tools on
// Chat only with reasoning off.
func chatToolsLoseExplicitReasoning(client textgen.Protocol, plan *textgen.RelayPlan, request *textgen.Request, modelName string) bool {
	if plan.Native || client.Family != textgen.ProtocolAnthropic || plan.UpstreamProtocol.Family != textgen.ProtocolOpenAIChat {
		return false
	}
	if len(request.Tools) == 0 || request.ToolPolicy.Mode == "none" || !common.ChatToolsNeedReasoningOff(modelName) {
		return false
	}
	effort := strings.ToLower(strings.TrimSpace(request.Reasoning.Effort))
	return request.Reasoning.Budget.Set || (effort != "" && effort != "none")
}

// hasNestedUnknownField says whether the request carries a field below the
// top level that the IR does not model (top-level unknown fields go back on
// the wire by name; see relay.TopLevelUnknownFields).
func hasNestedUnknownField(request *textgen.Request) bool {
	for path := range request.Raw.UnknownFields {
		if strings.ContainsAny(path, ".[") {
			return true
		}
	}
	return false
}

// chatToolsNeedReasoningOff identifies a Chat request with
// tools to a model that only takes tools on Chat with reasoning off, when the
// client left the effort unset. It is what the client would have to send to
// the model's own API; an effort the client chose is left alone, and the
// upstream's refusal of it reaches the client.
func chatToolsNeedReasoningOff(request *textgen.Request, modelName string) bool {
	if request.Reasoning.Effort != "" || len(request.Tools) == 0 || request.ToolPolicy.Mode == "none" ||
		!common.ChatToolsNeedReasoningOff(modelName) {
		return false
	}
	if _, set := request.Raw.TopLevel["reasoning"]; set {
		return false
	}
	return true
}

// OverrideIsUpstreamWire says whether the channel's param_override is
// written for the upstream's own wire and goes on the encoded body as it
// is, as the legacy conversions applied it: a Chat or Responses client on
// an Anthropic channel (the Messages body), a Claude client on any other
// (the OpenAI-shaped body). Such a channel only ever received that wire,
// so its operator's override names its fields, and it is neither mapped
// onto the IR nor a reason to refuse the bridge. Between Chat and
// Responses, where one channel serves both, the override stays semantic.
func OverrideIsUpstreamWire(client textgen.Protocol, snapshot textgen.CapabilitySnapshot) bool {
	if snapshot.Features.Has(textgen.FeatureEndpointGemini) {
		// a Gemini channel's override names generateContent fields, as it
		// did when the channel converted every request itself
		return client.Family != textgen.ProtocolGemini
	}
	if client.Family == textgen.ProtocolGemini {
		// a Gemini client on another vendor's channel: the override names
		// that channel's fields, applied to the encoded body as it was to
		// the converted one
		return true
	}
	return (client.Family == textgen.ProtocolAnthropic) != snapshot.Features.Has(textgen.FeatureEndpointMessages)
}

func validateBridgeOverride(native bool, unsupportedPatch string) error {
	if !native && unsupportedPatch != "" {
		return &textgen.UnsupportedFeatureError{
			Feature: textgen.FeatureUnknownField,
			Path:    "param_override." + unsupportedPatch,
			Reason:  "channel param_override is not safe for protocol bridging",
		}
	}
	return nil
}

func applySemanticOverrides(request *textgen.Request, override map[string]interface{}) (string, error) {
	if request == nil || len(override) == 0 {
		return "", nil
	}
	if operations, ok := override["operations"]; ok {
		items, ok := operations.([]interface{})
		if !ok {
			return "operations", fmt.Errorf("channel param_override operations must be an array")
		}
		unsupported := ""
		for index, item := range items {
			op, ok := item.(map[string]interface{})
			if !ok {
				return "operations", fmt.Errorf("channel param_override operation %d must be an object", index)
			}
			if len(op) > 3 || op["conditions"] != nil || op["from"] != nil || op["to"] != nil || op["keep_origin"] != nil {
				unsupported = "operations"
				continue
			}
			path, _ := op["path"].(string)
			if strings.Contains(path, ".") || strings.ContainsAny(path, "[]") {
				unsupported = "operations"
				continue
			}
			mode, _ := op["mode"].(string)
			switch mode {
			case "set":
				if recognized, err := setSemanticOverride(request, path, op["value"]); err != nil {
					return path, err
				} else if !recognized {
					unsupported = "operations"
				}
			case "delete":
				if !deleteSemanticOverride(request, path) {
					unsupported = "operations"
				}
			default:
				unsupported = "operations"
			}
		}
		return unsupported, nil
	}
	keys := make([]string, 0, len(override))
	for key := range override {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	unsupported := ""
	for _, key := range keys {
		value := override[key]
		recognized, err := setSemanticOverride(request, key, value)
		if err != nil {
			return key, err
		}
		if !recognized {
			if unsupported == "" {
				unsupported = key
			}
		}
	}
	return unsupported, nil
}

func setSemanticOverride(request *textgen.Request, key string, value interface{}) (bool, error) {
	switch strings.TrimSpace(key) {
	case "temperature":
		number, err := overrideFloat(value)
		if err != nil || number < 0 || number > 2 {
			return true, overrideRangeError(key, err, "0..2")
		}
		request.Sampling.Temperature = textgen.Some(number)
	case "top_p":
		number, err := overrideFloat(value)
		if err != nil || number < 0 || number > 1 {
			return true, overrideRangeError(key, err, "0..1")
		}
		request.Sampling.TopP = textgen.Some(number)
	case "top_k":
		number, err := overrideInt(value)
		if err != nil || number < 0 {
			return true, overrideRangeError(key, err, "a non-negative integer")
		}
		request.Sampling.TopK = textgen.Some(int(number))
	case "seed":
		number, err := overrideInt(value)
		if err != nil {
			return true, err
		}
		request.Sampling.Seed = textgen.Some(number)
	case "stop":
		raw, err := json.Marshal(value)
		if err != nil {
			return true, err
		}
		request.Sampling.Stop = textgen.Some(json.RawMessage(raw))
	case "n":
		number, err := overrideInt(value)
		if err != nil || number < 1 {
			return true, overrideRangeError(key, err, "a positive integer")
		}
		request.Sampling.N = textgen.Some(int(number))
	case "logprobs":
		flag, ok := value.(bool)
		if !ok {
			return true, fmt.Errorf("channel param_override %s must be boolean", key)
		}
		request.Sampling.LogProbs = textgen.Some(flag)
	case "top_logprobs":
		number, err := overrideInt(value)
		if err != nil || number < 0 {
			return true, overrideRangeError(key, err, "a non-negative integer")
		}
		request.Sampling.TopLogProbs = textgen.Some(int(number))
	case "max_tokens", "max_completion_tokens", "max_output_tokens":
		number, err := overrideInt(value)
		if err != nil || number < 1 {
			return true, overrideRangeError(key, err, "a positive integer")
		}
		request.Sampling.MaxOutput = textgen.Some(uint(number))
	case "parallel_tool_calls":
		flag, ok := value.(bool)
		if !ok {
			return true, fmt.Errorf("channel param_override %s must be boolean", key)
		}
		request.ToolPolicy.ParallelToolCalls = textgen.Some(flag)
	case "reasoning_effort":
		effort, ok := value.(string)
		if !ok {
			return true, fmt.Errorf("channel param_override %s must be a string", key)
		}
		request.Reasoning.Effort = effort
	case "store":
		flag, ok := value.(bool)
		if !ok {
			return true, fmt.Errorf("channel param_override %s must be boolean", key)
		}
		request.Lifecycle.Store = textgen.Some(flag)
	default:
		return false, nil
	}
	return true, nil
}

func deleteSemanticOverride(request *textgen.Request, key string) bool {
	switch strings.TrimSpace(key) {
	case "temperature":
		request.Sampling.Temperature = textgen.Option[float64]{}
	case "top_p":
		request.Sampling.TopP = textgen.Option[float64]{}
	case "top_k":
		request.Sampling.TopK = textgen.Option[int]{}
	case "seed":
		request.Sampling.Seed = textgen.Option[int64]{}
	case "stop":
		request.Sampling.Stop = textgen.Option[json.RawMessage]{}
	case "n":
		request.Sampling.N = textgen.Option[int]{}
	case "logprobs":
		request.Sampling.LogProbs = textgen.Option[bool]{}
	case "top_logprobs":
		request.Sampling.TopLogProbs = textgen.Option[int]{}
	case "max_tokens", "max_completion_tokens", "max_output_tokens":
		request.Sampling.MaxOutput = textgen.Option[uint]{}
	case "parallel_tool_calls":
		request.ToolPolicy.ParallelToolCalls = textgen.Option[bool]{}
	case "reasoning_effort":
		request.Reasoning.Effort = ""
	case "store":
		request.Lifecycle.Store = textgen.Option[bool]{}
	default:
		return false
	}
	return true
}

func overrideFloat(value interface{}) (float64, error) {
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("must be a finite number")
	}
	return number, nil
}

func overrideInt(value interface{}) (int64, error) {
	number, err := overrideFloat(value)
	if err != nil || math.Trunc(number) != number {
		return 0, fmt.Errorf("must be an integer")
	}
	return int64(number), nil
}

func overrideRangeError(key string, err error, expected string) error {
	if err != nil {
		return fmt.Errorf("channel param_override %s %w", key, err)
	}
	return fmt.Errorf("channel param_override %s must be %s", key, expected)
}
