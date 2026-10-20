package providercap

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/types"
)

const (
	DialectOpenAI               = "openai"
	DialectAzureOpenAI          = "azure_openai"
	DialectOpenRouter           = "openrouter"
	DialectCloudflareAIV1       = "cloudflare_ai_v1"
	DialectOpenAIChatCompatible = "openai_chat_compatible"
	DialectAnthropic            = "anthropic"
	DialectGemini               = "gemini"
	// DialectDeepSeek is DeepSeek's OpenAI-compatible Chat API. It plans like
	// openai_chat_compatible; the name exists so the wire dialect patch layer
	// (internal/relay/wirepatch) can address DeepSeek's quirks by dialect.
	DialectDeepSeek = "deepseek"
)

type ProtocolFamily string

const (
	ProtocolFamilyUnknown ProtocolFamily = "unknown"
	ProtocolFamilyOpenAI  ProtocolFamily = "openai"
	ProtocolFamilyClaude  ProtocolFamily = "claude"
	ProtocolFamilyGemini  ProtocolFamily = "gemini"
	ProtocolFamilyCustom  ProtocolFamily = "custom"
)

type Profile struct {
	Family ProtocolFamily

	SupportsGemini    bool
	SupportsClaude    bool
	SupportsResponses bool
	SupportsRealtime  bool
}

var profiles = map[int]Profile{
	constant.APITypeOpenAI: {
		Family:            ProtocolFamilyOpenAI,
		SupportsGemini:    true,
		SupportsClaude:    true,
		SupportsResponses: true,
		SupportsRealtime:  true,
	},
	constant.APITypeAnthropic: {
		Family:            ProtocolFamilyClaude,
		SupportsClaude:    true,
		SupportsResponses: false,
		SupportsRealtime:  false,
	},
	constant.APITypeGemini: {
		Family:            ProtocolFamilyGemini,
		SupportsGemini:    true,
		SupportsClaude:    true,
		SupportsResponses: false,
		SupportsRealtime:  false,
	},
	constant.APITypeXai: {
		Family:            ProtocolFamilyCustom,
		SupportsClaude:    false,
		SupportsResponses: false,
		SupportsRealtime:  false,
	},
}

func defaultProfile() Profile {
	return Profile{
		Family:            ProtocolFamilyUnknown,
		SupportsGemini:    false,
		SupportsClaude:    true,
		SupportsResponses: false,
		SupportsRealtime:  false,
	}
}

func GetProfile(apiType int) Profile {
	if profile, ok := profiles[apiType]; ok {
		return profile
	}
	return defaultProfile()
}

func ProtocolFamilyOf(apiType int) ProtocolFamily {
	return GetProfile(apiType).Family
}

// DefaultDialect is deliberately conservative. Existing OpenAI-compatible
// channels must be backfilled to an explicit dialect before they can advertise
// a native Responses endpoint.
func DefaultDialect(apiType int) string {
	switch apiType {
	case constant.APITypeOpenAI:
		return DialectOpenAIChatCompatible
	case constant.APITypeAnthropic:
		return DialectAnthropic
	case constant.APITypeGemini:
		return DialectGemini
	default:
		return DialectOpenAIChatCompatible
	}
}

func NormalizeDialect(apiType int, configured string) string {
	dialect := strings.ToLower(strings.TrimSpace(configured))
	switch apiType {
	case constant.APITypeAnthropic:
		return DialectAnthropic
	case constant.APITypeGemini:
		return DialectGemini
	}
	switch dialect {
	case DialectOpenAI, DialectAzureOpenAI, DialectOpenRouter, DialectCloudflareAIV1, DialectOpenAIChatCompatible, DialectDeepSeek:
		return dialect
	}
	return DefaultDialect(apiType)
}

type SnapshotInput struct {
	APIType         int
	Dialect         string
	ModelFamily     string
	Model           model.ModelCapabilities
	ContextWindow   int64
	MaxOutputTokens int64
	Channel         *dto.ChannelCapabilities
}

// BuildSnapshot composes the immutable capability view used by both routing
// and per-attempt planning. Overlays only narrow the dialect/model baseline.
func BuildSnapshot(input SnapshotInput) textgen.CapabilitySnapshot {
	dialect := NormalizeDialect(input.APIType, input.Dialect)
	var features textgen.FeatureSet
	add := func(values ...textgen.FeatureID) {
		for _, value := range values {
			features.Add(value)
		}
	}
	add(textgen.FeatureEndpointChat, textgen.FeatureInputText, textgen.FeatureTemperature,
		textgen.FeatureTopP, textgen.FeatureTopK, textgen.FeatureSeed, textgen.FeatureStop,
		textgen.FeatureN, textgen.FeatureLogProbs, textgen.FeatureMaxOutput,
		textgen.FeatureStream, textgen.FeatureStreamUsage)
	if dialect == DialectOpenAI || dialect == DialectAzureOpenAI || dialect == DialectOpenRouter {
		add(textgen.FeatureEndpointResponses, textgen.FeatureDeveloperRole)
	}
	if dialect == DialectDeepSeek {
		// DeepSeek serves /v1/responses (stateless, json_schema, function and
		// web_search tools; api-docs.deepseek.com/api/create-response,
		// 2026-10-20) beside Chat Completions.
		add(textgen.FeatureEndpointResponses)
	}
	// These are semantic model capabilities. An all-zero legacy record is
	// unknown, so it keeps only the safe scalar baseline above.
	if input.Model.SupportsFunctionCalling {
		add(textgen.FeatureFunctionTools, textgen.FeatureToolResult)
	}
	if input.Model.SupportsParallelFunctionCalling {
		add(textgen.FeatureParallelTools)
	}
	if input.Model.SupportsVision {
		add(textgen.FeatureInputImage)
	}
	if input.Model.SupportsAudioInput {
		add(textgen.FeatureInputAudio)
	}
	if input.Model.SupportsPDFInput {
		add(textgen.FeatureInputFile)
	}
	if input.Model.SupportsResponseSchema {
		add(textgen.FeatureJSONOutput, textgen.FeatureJSONSchema, textgen.FeatureStrictSchema)
	}
	if input.Model.SupportsReasoning {
		add(textgen.FeatureReasoningEffort, textgen.FeatureReasoningSummary)
	}
	if input.Model.SupportsWebSearch {
		add(textgen.FeatureHostedWebSearch)
	}
	// Channel overlays are restrictive. A nil field does not expand support.
	// An explicit *false is also the only signal that may reject a request
	// before the provider sees it, so it is recorded in Denied.
	var denied textgen.FeatureSet
	if input.Channel != nil {
		if !input.Channel.HasJSONSchema() {
			schema := textgen.FeatureSet(1)<<textgen.FeatureJSONSchema | textgen.FeatureSet(1)<<textgen.FeatureStrictSchema
			features &^= schema
			denied |= schema
		}
		if !input.Channel.HasChatCompletionsEndpoint() {
			features &^= textgen.FeatureSet(1) << textgen.FeatureEndpointChat
		}
		if !input.Channel.HasResponsesEndpoint() {
			features &^= textgen.FeatureSet(1) << textgen.FeatureEndpointResponses
		}
		if !input.Channel.HasTools() {
			tools := textgen.FeatureSet(1)<<textgen.FeatureFunctionTools | textgen.FeatureSet(1)<<textgen.FeatureToolResult | textgen.FeatureSet(1)<<textgen.FeatureParallelTools
			features &^= tools
			denied |= tools
		}
		if !input.Channel.HasVision() {
			features &^= textgen.FeatureSet(1) << textgen.FeatureInputImage
			denied |= textgen.FeatureSet(1) << textgen.FeatureInputImage
		}
		if !input.Channel.HasAudio() {
			features &^= textgen.FeatureSet(1) << textgen.FeatureInputAudio
			denied |= textgen.FeatureSet(1) << textgen.FeatureInputAudio
		}
	}
	constraints := map[textgen.FeatureID]textgen.Constraint{}
	contextWindow := input.ContextWindow
	if input.Channel != nil && input.Channel.ContextWindow > 0 && (contextWindow == 0 || int64(input.Channel.ContextWindow) < contextWindow) {
		contextWindow = int64(input.Channel.ContextWindow)
	}
	if contextWindow > 0 {
		constraints[textgen.FeatureInputText] = textgen.Constraint{Max: contextWindow}
	}
	maxOutput := input.MaxOutputTokens
	if input.Channel != nil && input.Channel.MaxOutputTokens > 0 && (maxOutput == 0 || int64(input.Channel.MaxOutputTokens) < maxOutput) {
		maxOutput = int64(input.Channel.MaxOutputTokens)
	}
	if maxOutput > 0 {
		constraints[textgen.FeatureMaxOutput] = textgen.Constraint{Max: maxOutput}
	}
	gaps := endpointGaps(dialect, features)
	revisionParts := append([]string{dialect, strings.TrimSpace(input.ModelFamily)}, features.Names()...)
	revisionParts = append(revisionParts, "denied:"+strings.Join(denied.Names(), ","))
	for _, endpoint := range []textgen.FeatureID{textgen.FeatureEndpointChat, textgen.FeatureEndpointResponses} {
		if gap := gaps[endpoint]; gap != 0 {
			revisionParts = append(revisionParts, "gap:"+endpoint.String()+":"+strings.Join(gap.Names(), ","))
		}
	}
	constraintKeys := make([]int, 0, len(constraints))
	for key := range constraints {
		constraintKeys = append(constraintKeys, int(key))
	}
	sort.Ints(constraintKeys)
	for _, key := range constraintKeys {
		revisionParts = append(revisionParts, fmt.Sprintf("%d:%d", key, constraints[textgen.FeatureID(key)].Max))
	}
	revision := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(revisionParts, "|"))))[:16]
	return textgen.CapabilitySnapshot{Dialect: dialect, Revision: revision, Features: features, Denied: denied, Constraints: constraints, EndpointGaps: gaps}
}

// dialectEndpointGaps is what a dialect's endpoint does NOT carry although
// the dialect's other endpoint does: a known fact about the vendor's wire,
// not about a model. The planner routes a request using such a feature
// through the other endpoint (textgen.CapabilitySnapshot.EndpointGaps).
var dialectEndpointGaps = map[string]map[textgen.FeatureID][]textgen.FeatureID{
	// DeepSeek Chat Completions: response_format is text | json_object only
	// (api-docs.deepseek.com/api/create-chat-completion, 2026-10-20);
	// Responses takes text.format json_schema.
	DialectDeepSeek: {textgen.FeatureEndpointChat: {textgen.FeatureJSONSchema, textgen.FeatureStrictSchema}},
}

// endpointGaps is the dialect's gaps restricted to the features the snapshot
// carries, nil when the dialect has none.
func endpointGaps(dialect string, features textgen.FeatureSet) map[textgen.FeatureID]textgen.FeatureSet {
	declared, ok := dialectEndpointGaps[dialect]
	if !ok {
		return nil
	}
	out := map[textgen.FeatureID]textgen.FeatureSet{}
	for endpoint, missing := range declared {
		var gap textgen.FeatureSet
		for _, feature := range missing {
			if features.Has(feature) {
				gap.Add(feature)
			}
		}
		if gap != 0 {
			out[endpoint] = gap
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IsSupported returns whether the provider apiType supports the incoming relay format.
// Rules are intentionally conservative to prevent known panic/not-implemented paths.
func IsSupported(apiType int, relayFormat types.RelayFormat) (bool, string) {
	profile := GetProfile(apiType)

	switch relayFormat {
	case types.RelayFormatGemini:
		if !profile.SupportsGemini {
			return false, "provider profile does not enable gemini support"
		}
		return true, ""
	case types.RelayFormatClaude:
		if !profile.SupportsClaude {
			return false, "provider profile blocks claude compatibility path"
		}
		return true, ""
	case types.RelayFormatOpenAIResponses:
		if !profile.SupportsResponses {
			return false, "provider profile does not enable responses support"
		}
		return true, ""
	case types.RelayFormatOpenAIRealtime:
		if !profile.SupportsRealtime {
			return false, "provider profile does not enable realtime support"
		}
		return true, ""
	default:
		return true, ""
	}
}
