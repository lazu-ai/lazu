package relay

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	claudechannel "github.com/lazu-ai/lazu/internal/relay/channel/claude"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/relay/wirepatch"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

func ResponsesHelper(c *gin.Context, info *relaycommon.RelayInfo) (lazuErr *types.LazuError) {
	info.InitChannelMeta(c)

	responsesReq, ok := info.Request.(*dto.OpenAIResponsesRequest)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected dto.OpenAIResponsesRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	if responsesReq.Reasoning != nil {
		info.ReasoningEffort = responsesReq.Reasoning.Effort
	}
	requestCopy := *responsesReq
	request := &requestCopy
	var err error

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	bridgeToChat := info.ProtocolPlan != nil && !info.ProtocolPlan.Native &&
		info.ProtocolPlan.ClientProtocol.Family == textgen.ProtocolOpenAIResponses &&
		info.ProtocolPlan.UpstreamProtocol.Family == textgen.ProtocolOpenAIChat
	bridgeToMessages := claudechannel.BridgesToMessages(info)
	var outboundBody []byte
	nativeConverter, nativeConvertible := adaptor.(interface {
		ConvertNativeRequest([]byte, *relaycommon.RelayInfo, bool) ([]byte, error)
	})
	if nativeConvertible && !bridgeToChat && !bridgeToMessages {
		bodyTimer := common.StartHotPathStage(common.HotPathStageOutboundBody)
		if info.NativeBodyCache != nil && info.NativeBodyChannelID == info.ChannelId {
			outboundBody = info.NativeBodyCache
		} else {
			body, bodyErr := common.GetRequestBody(c)
			if bodyErr != nil {
				return types.NewRequestBodyError(bodyErr)
			}
			outboundBody, err = nativeConverter.ConvertNativeRequest(body, info, true)
			if err == nil {
				info.NativeBodyCache = outboundBody
				info.NativeBodyChannelID = info.ChannelId
			}
		}
		bodyTimer.Finish(err)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
	} else if (model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled) && !bridgeToChat && !bridgeToMessages {
		body, err := common.GetRequestBody(c)
		if err != nil {
			return types.NewRequestBodyError(err)
		}
		outboundBody = body
	} else if bridgeToMessages {
		body, _, encodeErr := MessagesBridgeBody(c, info)
		if encodeErr != nil {
			return encodeErr
		}
		outboundBody = body
	} else if bridgeToChat {
		semantic := info.ProtocolRequest.Clone()
		if semantic == nil {
			return types.NewError(fmt.Errorf("missing immutable protocol request"), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		stripLazuMetadataFromSemantic(semantic)
		semantic.Model.Upstream = info.UpstreamModelName
		chatRequest, encodeErr := openai_chat.Encode(semantic)
		if encodeErr != nil {
			return types.NewErrorWithStatusCode(encodeErr, types.ErrorCodeConvertRequestFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		openAIConverter, converterErr := requireOpenAIRequestConverter(adaptor, info)
		if converterErr != nil {
			return converterErr
		}
		convertedRequest, convertErr := openAIConverter.ConvertOpenAIRequest(c, info, chatRequest)
		if convertErr != nil {
			return types.NewError(convertErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		jsonData, marshalErr := common.Marshal(convertedRequest)
		if marshalErr != nil {
			return types.NewError(marshalErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		// wire dialect patch on the bridged Chat body (internal/relay/wirepatch):
		// a Responses client reaching a Chat-only vendor takes this path.
		jsonData = wirepatch.Apply(wirepatch.ResolveDialect(info), jsonData)
		outboundBody = jsonData
	} else {
		responsesConverter, converterErr := requireResponsesRequestConverter(adaptor, info)
		if converterErr != nil {
			return converterErr
		}
		convertedRequest, err := responsesConverter.ConvertOpenAIResponsesRequest(c, info, *request)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		jsonData, err := common.Marshal(convertedRequest)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		switch convertedRequest.(type) {
		case dto.OpenAIResponsesRequest, *dto.OpenAIResponsesRequest:
			jsonData, err = relaycommon.MergeUnknownTopLevelFieldsFromContext(c, jsonData)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}
		}

		// remove disabled fields for OpenAI Responses API
		jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		// apply param override
		if len(info.ParamOverride) > 0 {
			jsonData, err = relaycommon.ApplyParamOverride(jsonData, info.ParamOverride)
			if err != nil {
				return types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
			}
		}
		// wire dialect patch: vendor quirks on the compiled body, once per
		// attempt, after ParamOverride (internal/relay/wirepatch).
		jsonData = wirepatch.Apply(wirepatch.ResolveDialect(info), jsonData)

		if common.DebugEnabled {
			println("requestBody: ", string(jsonData))
		}
		outboundBody = jsonData
	}

	// Whether store is actually disabled is only settled here: a channel may
	// strip `store` via disable_store (making server-side state usable again),
	// or a param override may force `store: false` on a request that never
	// asked for it. So validate the body that really goes upstream, not the
	// one the client sent.
	if !bridgeToChat {
		if err := helper.ValidateOutboundResponsesStore(outboundBody); err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
	}
	outboundBody, err = normalizeSingleUserTextResponses(outboundBody, info)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	outboundBody, err = applyExplicitStreamRequirement(outboundBody, info)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	requestBody := bytes.NewBuffer(outboundBody)

	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeAPIConnectionError, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	if resp != nil {
		httpResp = resp.(*http.Response)

		if httpResp.StatusCode != http.StatusOK {
			lazuErr = service.RelayErrorHandler(c, httpResp, false)
			// reset status code 重置状态码
			service.ResetStatusCode(lazuErr, statusCodeMappingStr)
			return lazuErr
		}
	}

	usage, lazuErr := adaptor.DoResponse(c, httpResp, info)
	if lazuErr != nil {
		settleTerminalProtocolStream(c, info, usage, lazuErr)
		// reset status code 重置状态码
		service.ResetStatusCode(lazuErr, statusCodeMappingStr)
		return lazuErr
	}

	if strings.HasPrefix(info.OriginModelName, "gpt-4o-audio") {
		service.PostAudioConsumeQuota(c, info, usage.(*dto.Usage), "")
	} else {
		postConsumeQuota(c, info, usage.(*dto.Usage), "")
	}
	return nil
}
