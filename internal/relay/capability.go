package relay

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/packages/domain/providercap"
)

type openAIRequestIdentityConverter struct{}

func (openAIRequestIdentityConverter) ConvertOpenAIRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	return request, nil
}

type responsesRequestIdentityConverter struct{}

func (responsesRequestIdentityConverter) ConvertOpenAIResponsesRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return request, nil
}

type claudeRequestIdentityConverter struct{}

func (claudeRequestIdentityConverter) ConvertClaudeRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	return request, nil
}

type geminiRequestIdentityConverter struct{}

func (geminiRequestIdentityConverter) ConvertGeminiRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	return request, nil
}

var (
	defaultOpenAIRequestConverter    channel.OpenAIRequestConverter    = openAIRequestIdentityConverter{}
	defaultResponsesRequestConverter channel.ResponsesRequestConverter = responsesRequestIdentityConverter{}
	defaultClaudeRequestConverter    channel.ClaudeRequestConverter    = claudeRequestIdentityConverter{}
	defaultGeminiRequestConverter    channel.GeminiRequestConverter    = geminiRequestIdentityConverter{}
)

func relayAPIType(info *relaycommon.RelayInfo) (int, bool) {
	if info == nil || info.ChannelMeta == nil {
		return -1, false
	}
	return info.ApiType, true
}

func missingCapabilityError(info *relaycommon.RelayInfo, capability string) *types.LazuError {
	apiType := -1
	if v, ok := relayAPIType(info); ok {
		apiType = v
	}
	return types.NewError(
		fmt.Errorf("api type %d does not support capability %s", apiType, capability),
		types.ErrorCodeInvalidApiType,
		types.ErrOptionWithSkipRetry(),
	)
}

func requireOpenAIRequestConverter(adaptor channel.BaseAdaptor, info *relaycommon.RelayInfo) (channel.OpenAIRequestConverter, *types.LazuError) {
	converter, ok := adaptor.(channel.OpenAIRequestConverter)
	if !ok {
		if apiType, hasAPIType := relayAPIType(info); hasAPIType && providercap.ProtocolFamilyOf(apiType) == providercap.ProtocolFamilyOpenAI {
			return defaultOpenAIRequestConverter, nil
		}
		return nil, missingCapabilityError(info, "openai_request_converter")
	}
	return converter, nil
}

func requireResponsesRequestConverter(adaptor channel.BaseAdaptor, info *relaycommon.RelayInfo) (channel.ResponsesRequestConverter, *types.LazuError) {
	converter, ok := adaptor.(channel.ResponsesRequestConverter)
	if !ok {
		if apiType, hasAPIType := relayAPIType(info); hasAPIType && providercap.GetProfile(apiType).SupportsResponses {
			return defaultResponsesRequestConverter, nil
		}
		return nil, missingCapabilityError(info, "responses_request_converter")
	}
	return converter, nil
}

func requireClaudeRequestConverter(adaptor channel.BaseAdaptor, info *relaycommon.RelayInfo) (channel.ClaudeRequestConverter, *types.LazuError) {
	converter, ok := adaptor.(channel.ClaudeRequestConverter)
	if !ok {
		if apiType, hasAPIType := relayAPIType(info); hasAPIType && providercap.ProtocolFamilyOf(apiType) == providercap.ProtocolFamilyClaude {
			return defaultClaudeRequestConverter, nil
		}
		return nil, missingCapabilityError(info, "claude_request_converter")
	}
	return converter, nil
}

func requireGeminiRequestConverter(adaptor channel.BaseAdaptor, info *relaycommon.RelayInfo) (channel.GeminiRequestConverter, *types.LazuError) {
	converter, ok := adaptor.(channel.GeminiRequestConverter)
	if !ok {
		if apiType, hasAPIType := relayAPIType(info); hasAPIType && providercap.ProtocolFamilyOf(apiType) == providercap.ProtocolFamilyGemini {
			return defaultGeminiRequestConverter, nil
		}
		return nil, missingCapabilityError(info, "gemini_request_converter")
	}
	return converter, nil
}

func requireImageRequestConverter(adaptor channel.BaseAdaptor, info *relaycommon.RelayInfo) (channel.ImageRequestConverter, *types.LazuError) {
	converter, ok := adaptor.(channel.ImageRequestConverter)
	if !ok {
		return nil, missingCapabilityError(info, "image_request_converter")
	}
	return converter, nil
}

func requireAudioRequestConverter(adaptor channel.BaseAdaptor, info *relaycommon.RelayInfo) (channel.AudioRequestConverter, *types.LazuError) {
	converter, ok := adaptor.(channel.AudioRequestConverter)
	if !ok {
		return nil, missingCapabilityError(info, "audio_request_converter")
	}
	return converter, nil
}

func requireEmbeddingRequestConverter(adaptor channel.BaseAdaptor, info *relaycommon.RelayInfo) (channel.EmbeddingRequestConverter, *types.LazuError) {
	converter, ok := adaptor.(channel.EmbeddingRequestConverter)
	if !ok {
		return nil, missingCapabilityError(info, "embedding_request_converter")
	}
	return converter, nil
}

func requireRerankRequestConverter(adaptor channel.BaseAdaptor, info *relaycommon.RelayInfo) (channel.RerankRequestConverter, *types.LazuError) {
	converter, ok := adaptor.(channel.RerankRequestConverter)
	if !ok {
		return nil, missingCapabilityError(info, "rerank_request_converter")
	}
	return converter, nil
}
