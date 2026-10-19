package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	claudechannel "github.com/lazu-ai/lazu/internal/relay/channel/claude"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
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

	request, err := common.DeepCopy(claudeReq)
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
	claudeConverter, lazuErr := requireClaudeRequestConverter(adaptor, info)
	if lazuErr != nil {
		return lazuErr
	}
	originalModel := request.Model
	adaptiveEffort := ""

	if baseModel, effort, ok := claudechannel.TrimEffortSuffix(request.Model); ok && claudechannel.SupportsAdaptiveThinkingModel(baseModel) {
		request.Model = baseModel
		info.UpstreamModelName = request.Model
		adaptiveEffort = effort
	}

	// 检测 -thinking 或 #thinking 模型后缀
	suffixTriggeredThinking := false
	if model_setting.GetClaudeSettings().ThinkingAdapterEnabled {
		if model, suffix := claudechannel.TrimThinkingSuffix(request.Model); suffix != "" {
			suffixTriggeredThinking = true
			request.Model = model
			info.UpstreamModelName = request.Model
		}
	}
	if request.MaxTokens == 0 {
		request.MaxTokens = uint(claudechannel.GetDefaultMaxTokensForThinkingModel(originalModel, request.Model))
	}
	if adaptiveEffort != "" && request.Thinking == nil {
		claudechannel.ApplyAdaptiveThinking(request, adaptiveEffort)
	}
	if suffixTriggeredThinking && request.Thinking == nil {
		if strings.HasPrefix(request.Model, "claude-opus-4-7") {
			claudechannel.ApplyAdaptiveThinking(request, "high")
		} else {
			budgetTokens := int(float64(request.MaxTokens) * model_setting.GetClaudeSettings().ThinkingAdapterBudgetTokensPercentage)
			request.Thinking = &dto.Thinking{
				Type:         "enabled",
				BudgetTokens: &budgetTokens,
			}
		}
	}
	if strings.HasPrefix(request.Model, "claude-opus-4-7") && request.Thinking != nil && request.Thinking.Type == "enabled" {
		effort := adaptiveEffort
		if effort == "" {
			effort = "high"
		}
		claudechannel.ApplyAdaptiveThinking(request, effort)
	}
	claudechannel.EnsureThinkingConstraints(request)

	var requestBody io.Reader
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		body, err := common.GetRequestBody(c)
		if err != nil {
			return types.NewRequestBodyError(err)
		}
		requestBody = bytes.NewBuffer(body)
	} else {
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
