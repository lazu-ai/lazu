package claude

import (
	"encoding/json"
	"errors"
	"fmt"
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

	"github.com/gin-gonic/gin"
)

func stopReasonClaude2OpenAI(reason string) string {
	switch reason {
	case "stop_sequence":
		return "stop"
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return reason
	}
}

func RequestOpenAI2ClaudeComplete(textRequest dto.GeneralOpenAIRequest) *dto.ClaudeRequest {
	topP := 0.0
	if textRequest.TopP != nil {
		topP = *textRequest.TopP
	}

	claudeRequest := dto.ClaudeRequest{
		Model:         textRequest.Model,
		Prompt:        "",
		StopSequences: nil,
		Temperature:   textRequest.Temperature,
		TopP:          topP,
		TopK:          textRequest.TopK,
		Stream:        textRequest.Stream,
	}
	if claudeRequest.MaxTokensToSample == 0 {
		claudeRequest.MaxTokensToSample = 4096
	}
	prompt := ""
	for _, message := range textRequest.Messages {
		if message.Role == "user" {
			prompt += fmt.Sprintf("\n\nHuman: %s", message.StringContent())
		} else if message.Role == "assistant" {
			prompt += fmt.Sprintf("\n\nAssistant: %s", message.StringContent())
		} else if message.Role == "system" {
			if prompt == "" {
				prompt = message.StringContent()
			}
		}
	}
	prompt += "\n\nAssistant:"
	claudeRequest.Prompt = prompt
	return &claudeRequest
}

// completionStreamChunk is the Chat chunk for a legacy /v1/complete stream
// event. A Messages stream for a Chat client is decoded and emitted by the
// hub (hub.go) and never comes here.
func completionStreamChunk(claudeResponse *dto.ClaudeResponse) *dto.ChatCompletionsStreamResponse {
	var response dto.ChatCompletionsStreamResponse
	response.Object = "chat.completion.chunk"
	response.Model = claudeResponse.Model
	var choice dto.ChatCompletionsStreamResponseChoice
	choice.Delta.SetContentString(claudeResponse.Completion)
	if finishReason := stopReasonClaude2OpenAI(claudeResponse.StopReason); finishReason != "null" {
		choice.FinishReason = &finishReason
	}
	response.Choices = []dto.ChatCompletionsStreamResponseChoice{choice}
	return &response
}

// completionResponse is the Chat reply for a legacy /v1/complete reply.
func completionResponse(claudeResponse *dto.ClaudeResponse) *dto.OpenAITextResponse {
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role:    "assistant",
			Content: strings.TrimPrefix(claudeResponse.Completion, " "),
		},
		FinishReason: stopReasonClaude2OpenAI(claudeResponse.StopReason),
	}
	return &dto.OpenAITextResponse{
		Id:      common.GenerateChatCompletionID(),
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Model:   claudeResponse.Model,
		Choices: []dto.OpenAITextResponseChoice{choice},
	}
}

type ClaudeResponseInfo struct {
	ResponseId   string
	Created      int64
	Model        string
	ResponseText strings.Builder
	Usage        *dto.Usage
	Done         bool
}

func applyClaudeUsageDetails(target *dto.Usage, usage *dto.ClaudeUsage) {
	if target == nil || usage == nil {
		return
	}
	target.PromptTokens = usage.InputTokens
	target.PromptExcludesCache = true
	target.InputTokenSource = dto.UsageSourceProvider
	target.PromptTokensDetails.CachedTokens = usage.CacheReadInputTokens
	target.PromptTokensDetails.CachedCreationTokens = usage.CacheCreationInputTokens
	target.CompletionTokens = usage.OutputTokens
	target.OutputTokenSource = dto.UsageSourceProvider
	applyClaudeThinkingTokens(target, usage)
	if usage.CacheCreation != nil {
		target.PromptTokensDetails.CachedCreation5mTokens = usage.CacheCreation.Ephemeral5mInputTokens
		target.PromptTokensDetails.CachedCreation1hTokens = usage.CacheCreation.Ephemeral1hInputTokens
	}
}

// applyClaudeThinkingTokens carries Claude's thinking count into the shared
// reasoning field, the one every other protocol fills. It is informational:
// thinking is already inside CompletionTokens and is billed there.
func applyClaudeThinkingTokens(target *dto.Usage, usage *dto.ClaudeUsage) {
	if target == nil {
		return
	}
	if thinking := usage.ThinkingTokens(); thinking > 0 {
		target.CompletionTokenDetails.ReasoningTokens = thinking
	}
}

func applyClaudeServerToolUsage(c *gin.Context, usage *dto.ClaudeUsage) {
	if c == nil || usage == nil || usage.ServerToolUse == nil || usage.ServerToolUse.WebSearchRequests <= 0 {
		return
	}
	c.Set("claude_web_search_requests", usage.ServerToolUse.WebSearchRequests)
}

func fillClaudeUsageFromAccumulated(current *dto.ClaudeUsage, accumulated *dto.Usage) *dto.ClaudeUsage {
	if accumulated == nil {
		return current
	}
	if current == nil {
		current = &dto.ClaudeUsage{}
	}
	if current.InputTokens == 0 {
		current.InputTokens = accumulated.PromptTokens
	}
	if current.OutputTokens == 0 {
		current.OutputTokens = accumulated.CompletionTokens
	}
	if current.CacheReadInputTokens == 0 {
		current.CacheReadInputTokens = accumulated.PromptTokensDetails.CachedTokens
	}
	if current.CacheCreationInputTokens == 0 {
		current.CacheCreationInputTokens = accumulated.PromptTokensDetails.CachedCreationTokens
	}
	if accumulated.PromptTokensDetails.CachedCreation5mTokens > 0 || accumulated.PromptTokensDetails.CachedCreation1hTokens > 0 {
		if current.CacheCreation == nil {
			current.CacheCreation = &dto.ClaudeCacheCreation{}
		}
		if current.CacheCreation.Ephemeral5mInputTokens == 0 {
			current.CacheCreation.Ephemeral5mInputTokens = accumulated.PromptTokensDetails.CachedCreation5mTokens
		}
		if current.CacheCreation.Ephemeral1hInputTokens == 0 {
			current.CacheCreation.Ephemeral1hInputTokens = accumulated.PromptTokensDetails.CachedCreation1hTokens
		}
	}
	return current
}

func FormatClaudeResponseInfo(requestMode int, claudeResponse *dto.ClaudeResponse, oaiResponse *dto.ChatCompletionsStreamResponse, claudeInfo *ClaudeResponseInfo) bool {
	if requestMode == RequestModeCompletion {
		claudeInfo.ResponseText.WriteString(claudeResponse.Completion)
	} else {
		if claudeResponse.Type == "message_start" {
			claudeInfo.ResponseId = claudeResponse.Message.Id
			claudeInfo.Model = claudeResponse.Message.Model

			// message_start, 获取usage
			applyClaudeUsageDetails(claudeInfo.Usage, claudeResponse.Message.Usage)
		} else if claudeResponse.Type == "content_block_delta" {
			if claudeResponse.Delta.Text != nil {
				claudeInfo.ResponseText.WriteString(*claudeResponse.Delta.Text)
			}
			if claudeResponse.Delta.Thinking != nil {
				claudeInfo.ResponseText.WriteString(*claudeResponse.Delta.Thinking)
			}
		} else if claudeResponse.Type == "message_delta" {
			// 最终的usage获取
			if claudeResponse.Usage.InputTokens > 0 {
				// 不叠加，只取最新的
				claudeInfo.Usage.PromptTokens = claudeResponse.Usage.InputTokens
				claudeInfo.Usage.PromptExcludesCache = true
				claudeInfo.Usage.InputTokenSource = dto.UsageSourceProvider
			}
			if claudeResponse.Usage.CacheReadInputTokens > 0 {
				claudeInfo.Usage.InputTokenSource = dto.UsageSourceProvider
				claudeInfo.Usage.PromptTokensDetails.CachedTokens = claudeResponse.Usage.CacheReadInputTokens
			}
			if claudeResponse.Usage.CacheCreationInputTokens > 0 {
				claudeInfo.Usage.InputTokenSource = dto.UsageSourceProvider
				claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens = claudeResponse.Usage.CacheCreationInputTokens
			}
			if claudeResponse.Usage.CacheCreation != nil {
				claudeInfo.Usage.PromptTokensDetails.CachedCreation5mTokens = claudeResponse.Usage.CacheCreation.Ephemeral5mInputTokens
				claudeInfo.Usage.PromptTokensDetails.CachedCreation1hTokens = claudeResponse.Usage.CacheCreation.Ephemeral1hInputTokens
			}
			claudeInfo.Usage.CompletionTokens = claudeResponse.Usage.OutputTokens
			claudeInfo.Usage.OutputTokenSource = dto.UsageSourceProvider
			claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
			applyClaudeThinkingTokens(claudeInfo.Usage, claudeResponse.Usage)

			// 判断是否完整
			claudeInfo.Done = true
		} else if claudeResponse.Type == "content_block_start" {
		} else {
			return false
		}
	}
	if oaiResponse != nil {
		oaiResponse.Id = claudeInfo.ResponseId
		oaiResponse.Created = claudeInfo.Created
		oaiResponse.Model = claudeInfo.Model
	}
	return true
}

func HandleStreamResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, data string, requestMode int) *types.LazuError {
	var claudeResponse dto.ClaudeResponse
	err := common.UnmarshalJsonStr(data, &claudeResponse)
	if err != nil {
		common.SysLog("error unmarshalling stream response: " + err.Error())
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	applyClaudeServerToolUsage(c, claudeResponse.Usage)
	// How the reply ended, kept for the outcome row. Claude reports it once, on
	// the message_delta that closes the message, and in its own vocabulary.
	if claudeResponse.Delta != nil && claudeResponse.Delta.StopReason != nil {
		if reason := stopReasonClaude2OpenAI(*claudeResponse.Delta.StopReason); reason != "null" {
			info.FinishReason = reason
		}
	}
	if info.RelayFormat == types.RelayFormatClaude {
		FormatClaudeResponseInfo(requestMode, &claudeResponse, nil, claudeInfo)
		if requestMode != RequestModeCompletion {
			if claudeResponse.Type == "message_start" {
				info.UpstreamModelName = claudeResponse.Message.Model
				if claudeResponse.Message != nil {
					claudeResponse.Message.Usage = fillClaudeUsageFromAccumulated(claudeResponse.Message.Usage, claudeInfo.Usage)
				}
			}
			if claudeResponse.Type == "message_delta" {
				claudeResponse.Usage = fillClaudeUsageFromAccumulated(claudeResponse.Usage, claudeInfo.Usage)
			}
		}
		helper.ClaudeData(c, claudeResponse)
	} else if requestMode == RequestModeCompletion {
		response := completionStreamChunk(&claudeResponse)
		if !FormatClaudeResponseInfo(requestMode, &claudeResponse, response, claudeInfo) {
			return nil
		}
		err = helper.ObjectData(c, response)
		if err != nil {
			logger.LogError(c, "send_stream_response_failed: "+err.Error())
		}
	} else {
		return types.NewError(errHubServesOpenAIClients, types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
	}
	return nil
}

// errHubServesOpenAIClients is returned when a Messages reply for an
// OpenAI-shaped client reaches the native handlers: the adaptor routes such
// an attempt to the hub (BridgesToMessages), so this is a wiring fault.
var errHubServesOpenAIClients = errors.New("a Messages reply for an OpenAI-shaped client is served by the protocol hub")

func finalizeClaudeStreamUsage(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, requestMode int) {

	if requestMode == RequestModeCompletion {
		if claudeInfo.ResponseText.Len() == 0 {
			return
		}
		claudeInfo.Usage = service.ResponseText2Usage(claudeInfo.ResponseText.String(), info.UpstreamModelName, service.PromptTokensForUsage(c, info))
	} else {

		if claudeInfo.Usage.CompletionTokens == 0 || !claudeInfo.Done {
			if common.DebugEnabled {
				common.SysLog("claude response usage is not complete, maybe upstream error")
			}
			fallbackUsage := service.ResponseText2Usage(claudeInfo.ResponseText.String(), info.UpstreamModelName, claudeInfo.Usage.PromptTokens)
			// Claude input excludes cache buckets; a provider-reported zero is valid.
			// A whole-prompt estimate here would count the cached input twice.
			if claudeInfo.Usage.PromptTokens == 0 && claudeInfo.Usage.InputTokenSource != dto.UsageSourceProvider {
				if claudeInfo.ResponseText.Len() > 0 || service.ValidUsage(claudeInfo.Usage) {
					service.ApplyLocalPromptUsage(c, info, claudeInfo.Usage)
				}
			}
			if claudeInfo.Usage.CompletionTokens == 0 {
				claudeInfo.Usage.CompletionTokens = fallbackUsage.CompletionTokens
				claudeInfo.Usage.OutputTokenSource = dto.UsageSourceLocalEstimate
			}
			claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
		} else if claudeInfo.Usage.TotalTokens == 0 {
			claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
		}
	}

}

func HandleStreamFinalResponse(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, requestMode int) {
	finalizeClaudeStreamUsage(c, info, claudeInfo, requestMode)

	if info.RelayFormat == types.RelayFormatClaude {
		//
	} else if requestMode == RequestModeCompletion {
		if info.ShouldIncludeUsage {
			response := helper.GenerateFinalUsageResponse(claudeInfo.ResponseId, claudeInfo.Created, info.UpstreamModelName, *claudeInfo.Usage)
			err := helper.ObjectData(c, response)
			if err != nil {
				common.SysLog("send final response failed: " + err.Error())
			}
		}
		helper.Done(c)
	}
}

func ClaudeStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, requestMode int) (*dto.Usage, *types.LazuError) {
	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	var err *types.LazuError
	scan := helper.StreamScannerHandler(c, resp, info, func(data string) bool {
		err = HandleStreamResponseData(c, info, claudeInfo, data, requestMode)
		if err != nil {
			return false
		}
		return true
	})
	if err == nil {
		err = scan.RelayError(c.Writer.Written() || info.HasSendResponse())
	}
	if err != nil {
		finalizeClaudeStreamUsage(c, info, claudeInfo, requestMode)
		if service.ValidUsage(claudeInfo.Usage) {
			common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		}
		return claudeInfo.Usage, err
	}

	HandleStreamFinalResponse(c, info, claudeInfo, requestMode)
	return claudeInfo.Usage, nil
}

func HandleClaudeResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, httpResp *http.Response, data []byte, requestMode int) *types.LazuError {
	var claudeResponse dto.ClaudeResponse
	err := common.Unmarshal(data, &claudeResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	// How the reply ended, kept for the outcome row exactly as the stream
	// handler does from its closing message_delta.
	if claudeResponse.StopReason != "" {
		info.FinishReason = stopReasonClaude2OpenAI(claudeResponse.StopReason)
	}
	if requestMode == RequestModeCompletion {
		claudeInfo.Usage = service.ResponseText2Usage(claudeResponse.Completion, info.OriginModelName, service.PromptTokensForUsage(c, info))
	} else {
		claudeInfo.Usage.PromptTokens = claudeResponse.Usage.InputTokens
		claudeInfo.Usage.PromptExcludesCache = true
		claudeInfo.Usage.InputTokenSource = dto.UsageSourceProvider
		claudeInfo.Usage.CompletionTokens = claudeResponse.Usage.OutputTokens
		claudeInfo.Usage.OutputTokenSource = dto.UsageSourceProvider
		claudeInfo.Usage.TotalTokens = claudeResponse.Usage.InputTokens + claudeResponse.Usage.OutputTokens
		applyClaudeThinkingTokens(claudeInfo.Usage, claudeResponse.Usage)
		claudeInfo.Usage.PromptTokensDetails.CachedTokens = claudeResponse.Usage.CacheReadInputTokens
		claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens = claudeResponse.Usage.CacheCreationInputTokens
		if claudeResponse.Usage.CacheCreation != nil {
			claudeInfo.Usage.PromptTokensDetails.CachedCreation5mTokens = claudeResponse.Usage.CacheCreation.Ephemeral5mInputTokens
			claudeInfo.Usage.PromptTokensDetails.CachedCreation1hTokens = claudeResponse.Usage.CacheCreation.Ephemeral1hInputTokens
		}
	}
	var responseData []byte
	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		if requestMode != RequestModeCompletion {
			return types.NewError(errHubServesOpenAIClients, types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
		}
		openaiResponse := completionResponse(&claudeResponse)
		openaiResponse.Usage = *claudeInfo.Usage
		responseData, err = json.Marshal(openaiResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	case types.RelayFormatClaude:
		responseData = data
	}

	applyClaudeServerToolUsage(c, claudeResponse.Usage)

	service.IOCopyBytesGracefully(c, httpResp, responseData)
	return nil
}

func ClaudeHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, requestMode int) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)

	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if common.DebugEnabled {
		println("responseBody: ", string(responseBody))
	}
	handleErr := HandleClaudeResponseData(c, info, claudeInfo, resp, responseBody, requestMode)
	if handleErr != nil {
		return nil, handleErr
	}
	return claudeInfo.Usage, nil
}
