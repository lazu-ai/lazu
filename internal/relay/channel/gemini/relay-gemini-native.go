package gemini

import (
	"io"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/logger"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/pkg/errors"

	"github.com/gin-gonic/gin"
)

func GeminiTextGenerationHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)

	// 读取响应体
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if common.DebugEnabled {
		println(string(responseBody))
	}

	// 解析为 Gemini 原生响应格式
	var geminiResponse dto.GeminiChatResponse
	err = common.Unmarshal(responseBody, &geminiResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	recordGeminiFinishReason(info, &geminiResponse)

	// 计算使用量（基于 UsageMetadata）
	fallbackPromptTokens := 0
	if geminiResponse.UsageMetadata.PromptTokenCount+geminiResponse.UsageMetadata.ToolUsePromptTokenCount <= 0 {
		fallbackPromptTokens = service.PromptTokensForUsage(c, info)
	}
	usage := buildUsageFromGeminiMetadata(geminiResponse.UsageMetadata, fallbackPromptTokens)

	service.IOCopyBytesGracefully(c, resp, responseBody)

	return &usage, nil
}

func NativeGeminiEmbeddingHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if common.DebugEnabled {
		println(string(responseBody))
	}

	promptTokens := service.PromptTokensForUsage(c, info)
	usage := &dto.Usage{
		PromptTokens: promptTokens,
		TotalTokens:  promptTokens,
	}
	service.ApplyLocalPromptUsage(c, info, usage)

	if info.IsGeminiBatchEmbedding {
		var geminiResponse dto.GeminiBatchEmbeddingResponse
		err = common.Unmarshal(responseBody, &geminiResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	} else {
		var geminiResponse dto.GeminiEmbeddingResponse
		err = common.Unmarshal(responseBody, &geminiResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)

	return usage, nil
}

func GeminiTextGenerationStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	var usage = &dto.Usage{}
	var imageCount int
	var audioTokens int
	hasProviderUsage := false

	helper.SetEventStreamHeaders(c)

	responseText := strings.Builder{}

	scan := helper.StreamScannerHandler(c, resp, info, func(data string) bool {
		var geminiResponse dto.GeminiChatResponse
		err := common.UnmarshalJsonStr(data, &geminiResponse)
		if err != nil {
			logger.LogError(c, "error unmarshalling stream response: "+err.Error())
			return false
		}
		recordGeminiFinishReason(info, &geminiResponse)

		// 统计图片数量
		for _, candidate := range geminiResponse.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.InlineData != nil {
					mimeType := strings.ToLower(strings.TrimSpace(part.InlineData.MimeType))
					switch {
					case strings.HasPrefix(mimeType, "image/"):
						imageCount++
					case strings.HasPrefix(mimeType, "audio/"):
						estimatedTokens, tokenErr := estimateGeminiInlineAudioTokens(part.InlineData)
						if tokenErr != nil {
							logger.LogError(c, "failed to estimate Gemini inline audio tokens: "+tokenErr.Error())
						} else {
							audioTokens += estimatedTokens
						}
					}
				}
				if part.Text != "" {
					responseText.WriteString(part.Text)
				}
			}
		}

		// 更新使用量统计
		if hasGeminiUsageMetadata(geminiResponse.UsageMetadata) {
			hasProviderUsage = true
			fallbackPromptTokens := 0
			if geminiResponse.UsageMetadata.PromptTokenCount+geminiResponse.UsageMetadata.ToolUsePromptTokenCount <= 0 {
				fallbackPromptTokens = service.PromptTokensForUsage(c, info)
			}
			mappedUsage := buildUsageFromGeminiMetadata(geminiResponse.UsageMetadata, fallbackPromptTokens)
			*usage = mappedUsage
		}

		// 直接发送 GeminiChatResponse 响应
		err = helper.StringData(c, data)
		if err != nil {
			logger.LogError(c, err.Error())
		}
		info.SendResponseCount++
		return true
	})

	if info.SendResponseCount == 0 {
		if scanErr := scan.RelayError(c.Writer.Written() || info.HasSendResponse()); scanErr != nil {
			return nil, scanErr
		}
		return nil, types.NewOpenAIError(errors.New("no response received from Gemini API"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
	}

	// 如果上游未返回 completion usage，则按各输出模态分别补齐，保留已返回的输入和缓存明细。
	fallbackPromptTokens := 0
	if !hasProviderUsage || usage.PromptTokens == 0 {
		fallbackPromptTokens = service.PromptTokensForUsage(c, info)
	}
	applyGeminiStreamFallbackUsage(
		usage,
		responseText.String(),
		info.UpstreamModelName,
		fallbackPromptTokens,
		imageCount,
		audioTokens,
		hasProviderUsage,
	)

	if scanErr := scan.RelayError(c.Writer.Written() || info.HasSendResponse()); scanErr != nil {
		if service.ValidUsage(usage) {
			common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		}
		return usage, scanErr
	}

	// 移除流式响应结尾的[Done]，因为Gemini API没有发送Done的行为
	//helper.Done(c)

	return usage, nil
}
