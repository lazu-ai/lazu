package xai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/channel/openai"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"

	"github.com/gin-gonic/gin"
)

func streamResponseXAI2OpenAI(xAIResp *dto.ChatCompletionsStreamResponse, usage *dto.Usage) *dto.ChatCompletionsStreamResponse {
	if xAIResp == nil {
		return nil
	}
	if xAIResp.Usage != nil {
		xAIResp.Usage = usage
	}
	openAIResp := &dto.ChatCompletionsStreamResponse{
		Id:      xAIResp.Id,
		Object:  xAIResp.Object,
		Created: xAIResp.Created,
		Model:   xAIResp.Model,
		Choices: xAIResp.Choices,
		Usage:   xAIResp.Usage,
	}

	return openAIResp
}

func normalizeXAIUsage(usage *dto.Usage, fallbackPromptTokens int) {
	if usage == nil {
		return
	}
	usagepkg.NormalizeDTOUsageFields(usage)

	if usage.PromptTokens == 0 && usage.CompletionTokens > 0 && usage.TotalTokens >= usage.CompletionTokens {
		usage.PromptTokens = usage.TotalTokens - usage.CompletionTokens
	}
	if usage.CompletionTokens == 0 && usage.PromptTokens > 0 && usage.TotalTokens >= usage.PromptTokens {
		usage.CompletionTokens = usage.TotalTokens - usage.PromptTokens
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens > 0 &&
		fallbackPromptTokens > 0 && fallbackPromptTokens <= usage.TotalTokens {
		usage.PromptTokens = fallbackPromptTokens
		usage.CompletionTokens = usage.TotalTokens - fallbackPromptTokens
		usage.InputTokenSource = dto.UsageSourceLocalEstimate
		usage.OutputTokenSource = dto.UsageSourceLocalEstimate
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	if usage.CompletionTokenDetails.TextTokens == 0 && usage.CompletionTokens > 0 {
		textTokens := usage.CompletionTokens -
			usage.CompletionTokenDetails.ReasoningTokens -
			usage.CompletionTokenDetails.AudioTokens -
			usage.CompletionTokenDetails.ImageTokens
		if textTokens > 0 {
			usage.CompletionTokenDetails.TextTokens = textTokens
		}
	}
}

func xAIStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	usage := &dto.Usage{}
	var responseTextBuilder strings.Builder
	var toolCount int
	var containStreamUsage bool
	var streamErr *types.LazuError

	helper.SetEventStreamHeaders(c)

	scan := helper.StreamScannerHandler(c, resp, info, func(data string) bool {
		var xAIResp *dto.ChatCompletionsStreamResponse
		err := json.Unmarshal([]byte(data), &xAIResp)
		if err != nil || xAIResp == nil {
			common.SysLog("invalid xAI stream response")
			return true
		}
		if upstreamErr := xAIResp.GetOpenAIError(); upstreamErr != nil {
			streamErr = types.WithOpenAIError(*upstreamErr, http.StatusOK, types.ErrOptionWithSkipRetry())
			return false
		}

		// 把 xAI 的usage转换为 OpenAI 的usage
		if service.ValidUsage(xAIResp.Usage) || (xAIResp.Usage != nil && xAIResp.Usage.TotalTokens > 0) {
			containStreamUsage = true
			xAIResp.Usage.MarkProviderUsage()
			// Only total-only usage needs a local split; complete provider
			// counters never trigger request counting or consume admission.
			normalizeXAIUsage(xAIResp.Usage, 0)
			if !service.ValidUsage(xAIResp.Usage) {
				normalizeXAIUsage(xAIResp.Usage, service.PromptTokensForUsage(c, info))
			}
			*usage = *xAIResp.Usage
		}

		openaiResponse := streamResponseXAI2OpenAI(xAIResp, usage)
		_ = openai.ProcessStreamResponse(*openaiResponse, &responseTextBuilder, &toolCount)
		err = helper.ObjectData(c, openaiResponse)
		if err != nil {
			common.SysLog(err.Error())
		}
		return true
	})

	if !containStreamUsage {
		if responseTextBuilder.Len() > 0 || toolCount > 0 {
			usage = service.ResponseText2Usage(responseTextBuilder.String(), info.UpstreamModelName, service.PromptTokensForUsage(c, info))
		}
		usage.CompletionTokens += toolCount * 7
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	} else {
		fillXAIUsage(c, info, usage, responseTextBuilder.String(), toolCount)
	}

	service.CloseResponseBodyGracefully(resp)
	if streamErr == nil {
		streamErr = scan.RelayError(c.Writer.Written() || info.HasSendResponse())
	}
	if c.Request != nil && c.Request.Context().Err() != nil {
		streamErr = types.NewError(c.Request.Context().Err(), types.ErrorCodeClientCanceled, types.ErrOptionWithSkipRetry())
	}
	if streamErr != nil {
		if service.ValidUsage(usage) {
			common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		}
		return usage, streamErr
	}
	helper.Done(c)
	return usage, nil
}

func xAIHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	var xaiResponse ChatCompletionResponse
	var errorResponse dto.OpenAITextResponse
	if common.Unmarshal(responseBody, &errorResponse) == nil {
		if upstreamErr := errorResponse.GetOpenAIError(); upstreamErr != nil {
			return nil, types.WithOpenAIError(*upstreamErr, resp.StatusCode)
		}
	}
	err = common.Unmarshal(responseBody, &xaiResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	var output strings.Builder
	toolCount := 0
	for _, choice := range xaiResponse.Choices {
		output.WriteString(choice.Message.StringContent())
		output.WriteString(choice.Message.ReasoningContent)
		for _, call := range choice.Message.ParseToolCalls() {
			output.WriteString(call.Function.Name)
			output.WriteString(call.Function.Arguments)
			toolCount++
		}
	}
	if xaiResponse.Usage == nil {
		xaiResponse.Usage = &dto.Usage{}
	}
	xaiResponse.Usage.MarkProviderUsage()
	fillXAIUsage(c, info, xaiResponse.Usage, output.String(), toolCount)

	// new body
	encodeJson, err := common.Marshal(xaiResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	service.IOCopyBytesGracefully(c, resp, encodeJson)

	return xaiResponse.Usage, nil
}

func fillXAIUsage(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage, output string, toolCount int) {
	normalizeXAIUsage(usage, 0)
	if !service.ValidUsage(usage) && usage.TotalTokens > 0 {
		normalizeXAIUsage(usage, service.PromptTokensForUsage(c, info))
	}
	observed := output != "" || toolCount > 0
	if usage.PromptTokens == 0 && (observed || usage.CompletionTokens > 0) {
		service.ApplyLocalPromptUsage(c, info, usage)
	}
	if usage.CompletionTokens == 0 && observed {
		usage.CompletionTokens = service.CountTextToken(output, info.UpstreamModelName) + toolCount*7
		usage.OutputTokenSource = dto.UsageSourceLocalEstimate
	}
	usage.TotalTokens = max(usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
}
