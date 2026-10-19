package runtime

import (
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/packages/domain/providercap"
)

type ModelDescriptor struct {
	Family          string
	Capabilities    model.ModelCapabilities
	ContextWindow   int64
	MaxOutputTokens int64
}

func Descriptor(modelName string) ModelDescriptor {
	return descriptorFromPricing(model.GetPricing(), modelName)
}

func descriptorFromPricing(pricings []model.Pricing, modelName string) ModelDescriptor {
	for index := range pricings {
		if strings.EqualFold(pricings[index].ModelName, modelName) {
			return descriptorFromPricingRow(pricings, &pricings[index])
		}
	}
	normalized := model.NormalizePricingModelName(modelName)
	if !strings.EqualFold(normalized, modelName) {
		for index := range pricings {
			if strings.EqualFold(pricings[index].ModelName, normalized) {
				return descriptorFromPricingRow(pricings, &pricings[index])
			}
		}
	}
	return ModelDescriptor{}
}

func descriptorFromPricingRow(pricings []model.Pricing, pricing *model.Pricing) ModelDescriptor {
	if pricing == nil {
		return ModelDescriptor{}
	}
	descriptor := ModelDescriptor{Family: pricing.Family, Capabilities: pricing.Capabilities, ContextWindow: pricing.ContextLength, MaxOutputTokens: pricing.MaxOutputTokens}
	family := strings.TrimSpace(pricing.Family)
	if family == "" || strings.EqualFold(pricing.ModelName, family) {
		return descriptor
	}
	for index := range pricings {
		profile := &pricings[index]
		if !strings.EqualFold(profile.ModelName, family) {
			continue
		}
		descriptor.Capabilities = mergeModelCapabilities(profile.Capabilities, pricing.Capabilities)
		if descriptor.ContextWindow == 0 {
			descriptor.ContextWindow = profile.ContextLength
		}
		if descriptor.MaxOutputTokens == 0 {
			descriptor.MaxOutputTokens = profile.MaxOutputTokens
		}
		break
	}
	return descriptor
}

func mergeModelCapabilities(base, override model.ModelCapabilities) model.ModelCapabilities {
	return model.ModelCapabilities{
		SupportsFunctionCalling:         base.SupportsFunctionCalling || override.SupportsFunctionCalling,
		SupportsParallelFunctionCalling: base.SupportsParallelFunctionCalling || override.SupportsParallelFunctionCalling,
		SupportsVision:                  base.SupportsVision || override.SupportsVision,
		SupportsReasoning:               base.SupportsReasoning || override.SupportsReasoning,
		SupportsResponseSchema:          base.SupportsResponseSchema || override.SupportsResponseSchema,
		SupportsPromptCaching:           base.SupportsPromptCaching || override.SupportsPromptCaching,
		SupportsAudioInput:              base.SupportsAudioInput || override.SupportsAudioInput,
		SupportsAudioOutput:             base.SupportsAudioOutput || override.SupportsAudioOutput,
		SupportsWebSearch:               base.SupportsWebSearch || override.SupportsWebSearch,
		SupportsPDFInput:                base.SupportsPDFInput || override.SupportsPDFInput,
		SupportsComputerUse:             base.SupportsComputerUse || override.SupportsComputerUse,
	}
}

func SnapshotForChannel(channel *model.Channel, modelName string) (textgen.CapabilitySnapshot, string, error) {
	if channel == nil {
		return textgen.CapabilitySnapshot{}, "", fmt.Errorf("channel is nil")
	}
	apiType, ok := common.ChannelType2APIType(channel.Type)
	if !ok && channel.Type == constant.ChannelTypeCustom {
		apiType = constant.APITypeOpenAI
		ok = true
	}
	if !ok {
		return textgen.CapabilitySnapshot{}, "", fmt.Errorf("invalid channel type %d", channel.Type)
	}
	settings := channel.GetOtherSettings()
	descriptor := Descriptor(modelName)
	snapshot := providercap.BuildSnapshot(providercap.SnapshotInput{APIType: apiType, Dialect: settings.ProtocolDialect, ModelFamily: descriptor.Family, Model: descriptor.Capabilities, ContextWindow: descriptor.ContextWindow, MaxOutputTokens: descriptor.MaxOutputTokens, Channel: settings.GetCapabilities()})
	dialect := providercap.NormalizeDialect(apiType, settings.ProtocolDialect)
	if channel.Type == constant.ChannelTypeCustom {
		// A Custom channel sends every request to one fixed URL. Do not infer
		// both OpenAI endpoints from its dialect: without an explicit endpoint
		// declaration, preserve the historical Chat-compatible behavior.
		restrictCustomFixedEndpoint(&snapshot, settings.GetCapabilities())
	}
	if channel.Type == constant.ChannelTypeXai {
		// xAI uses a separate adaptor that is not protocol-plan aware. Native
		// Chat remains valid, while cross-protocol planning must fail closed.
		dialect = "xai"
		snapshot.Dialect = dialect
	}
	return snapshot, dialect, nil
}

func restrictCustomFixedEndpoint(snapshot *textgen.CapabilitySnapshot, capabilities *dto.ChannelCapabilities) {
	if snapshot == nil {
		return
	}
	chat, responses := true, false
	if capabilities != nil {
		switch {
		case capabilities.SupportsResponses != nil && *capabilities.SupportsResponses &&
			(capabilities.SupportsChatCompletions == nil || !*capabilities.SupportsChatCompletions):
			chat, responses = false, true
		case capabilities.SupportsChatCompletions != nil && !*capabilities.SupportsChatCompletions &&
			capabilities.SupportsResponses == nil:
			chat, responses = false, true
		case capabilities.SupportsChatCompletions != nil && !*capabilities.SupportsChatCompletions &&
			capabilities.SupportsResponses != nil && !*capabilities.SupportsResponses:
			chat, responses = false, false
		}
	}
	if !chat {
		snapshot.Features &^= textgen.FeatureSet(1) << textgen.FeatureEndpointChat
	}
	if !responses {
		snapshot.Features &^= textgen.FeatureSet(1) << textgen.FeatureEndpointResponses
	}
}

func PlanForChannel(parsed *textgen.ParsedRequest, channel *model.Channel, modelName string) (*textgen.RelayPlan, error) {
	attempt, err := PrepareAttempt(parsed, channel, modelName)
	if err != nil {
		return nil, err
	}
	return attempt.Plan, nil
}
