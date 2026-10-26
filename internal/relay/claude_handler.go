package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	claudechannel "github.com/lazu-ai/lazu/internal/relay/channel/claude"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/relay/wirepatch"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

func ClaudeHelper(c *gin.Context, info *relaycommon.RelayInfo) (lazuErr *types.LazuError) {

	info.InitChannelMeta(c)

	claudeReq, ok := info.Request.(*dto.ClaudeRequest)

	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected *dto.ClaudeRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	request, err := DeepCopy(claudeReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to ClaudeRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	if !bridgesClaudeClient(info) {
		// thinking fitted to a Claude model; a bridged attempt's model is
		// another vendor's, and its reasoning was planned (PrepareAttempt)
		originalModel := request.Model
		resolve := claudechannel.CatalogPolicy(info.OriginModelName)
		claudechannel.PrepareNativeThinking(request, resolve)
		if request.Model != originalModel {
			info.UpstreamModelName = request.Model
		}
		// sampling fields the model's catalog row refuses are left out, as
		// the OpenAI channel leaves them out for its reasoning models; the
		// client is told in X-Lazu-Warning
		relaycommon.WarnSamplingRefused(c, info, claudechannel.DropRefusedSampling(request, resolve))
	}

	var requestBody io.Reader
	if bridgesClaudeClient(info) {
		// hub: the IR encoded for the OpenAI-shaped upstream the plan chose
		body, _, bridgeErr := BridgeBody(c, info, adaptor)
		if bridgeErr != nil {
			return bridgeErr
		}
		requestBody = bytes.NewBuffer(body)
	} else if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		body, err := common.GetRequestBody(c)
		if err != nil {
			return types.NewRequestBodyError(err)
		}
		requestBody = bytes.NewBuffer(body)
	} else {
		claudeConverter, converterErr := requireClaudeRequestConverter(adaptor, info)
		if converterErr != nil {
			return converterErr
		}
		convertedRequest, err := claudeConverter.ConvertClaudeRequest(c, info, request)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		jsonData, err := common.Marshal(convertedRequest)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		switch convertedRequest.(type) {
		case dto.ClaudeRequest, *dto.ClaudeRequest:
			jsonData, err = relaycommon.MergeUnknownTopLevelFieldsFromContext(c, jsonData)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}
		}

		// remove disabled fields for Claude API
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
		requestBody = bytes.NewBuffer(jsonData)
	}
	if info.ChannelOtherSettings.ExplicitStreamRequired {
		body, readErr := io.ReadAll(requestBody)
		if readErr != nil {
			return types.NewRequestBodyError(readErr)
		}
		body, err = applyExplicitStreamRequirement(body, info)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		requestBody = bytes.NewBuffer(body)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeAPIConnectionError, http.StatusInternalServerError)
	}

	if resp != nil {
		httpResp = resp.(*http.Response)
		info.IsStream = info.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
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

	service.PostClaudeConsumeQuota(c, info, usage.(*dto.Usage))
	return nil
}

// bridgesClaudeClient says whether the attempt serves a Claude client from an
// OpenAI-shaped upstream (Chat Completions or Responses) through the hub.
func bridgesClaudeClient(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ProtocolPlan == nil || info.ProtocolPlan.Native || info.ProtocolRequest == nil ||
		info.ProtocolPlan.ClientProtocol.Family != textgen.ProtocolAnthropic {
		return false
	}
	switch info.ProtocolPlan.UpstreamProtocol.Family {
	case textgen.ProtocolOpenAIChat, textgen.ProtocolOpenAIResponses, textgen.ProtocolGemini:
		return true
	}
	return false
}
