package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/tidwall/gjson"
	"io"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"

	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
)

func sendStreamData(c *gin.Context, info *relaycommon.RelayInfo, data string, forceFormat bool, thinkToContent bool) error {
	if data == "" {
		return nil
	}

	if !forceFormat && !thinkToContent {
		return helper.StringData(c, data)
	}

	var lastStreamResponse dto.ChatCompletionsStreamResponse
	if err := common.UnmarshalJsonStr(data, &lastStreamResponse); err != nil {
		return err
	}

	if !thinkToContent {
		return helper.ObjectData(c, lastStreamResponse)
	}

	hasThinkingContent := false
	hasContent := false
	var thinkingContent strings.Builder
	for _, choice := range lastStreamResponse.Choices {
		if len(choice.Delta.GetReasoningContent()) > 0 {
			hasThinkingContent = true
			thinkingContent.WriteString(choice.Delta.GetReasoningContent())
		}
		if len(choice.Delta.GetContentString()) > 0 {
			hasContent = true
		}
	}

	// Handle think to content conversion
	if info.ThinkingContentInfo.IsFirstThinkingContent {
		if hasThinkingContent {
			response := lastStreamResponse.Copy()
			for i := range response.Choices {
				// send `think` tag with thinking content
				response.Choices[i].Delta.SetContentString("<think>\n" + thinkingContent.String())
				response.Choices[i].Delta.ReasoningContent = nil
				response.Choices[i].Delta.Reasoning = nil
			}
			info.ThinkingContentInfo.IsFirstThinkingContent = false
			info.ThinkingContentInfo.HasSentThinkingContent = true
			return helper.ObjectData(c, response)
		}
	}

	if lastStreamResponse.Choices == nil || len(lastStreamResponse.Choices) == 0 {
		return helper.ObjectData(c, lastStreamResponse)
	}

	// Process each choice
	for i, choice := range lastStreamResponse.Choices {
		// Handle transition from thinking to content
		// only send `</think>` tag when previous thinking content has been sent
		if hasContent && !info.ThinkingContentInfo.SendLastThinkingContent && info.ThinkingContentInfo.HasSentThinkingContent {
			response := lastStreamResponse.Copy()
			for j := range response.Choices {
				response.Choices[j].Delta.SetContentString("\n</think>\n")
				response.Choices[j].Delta.ReasoningContent = nil
				response.Choices[j].Delta.Reasoning = nil
			}
			info.ThinkingContentInfo.SendLastThinkingContent = true
			helper.ObjectData(c, response)
		}

		// Convert reasoning content to regular content if any
		if len(choice.Delta.GetReasoningContent()) > 0 {
			lastStreamResponse.Choices[i].Delta.SetContentString(choice.Delta.GetReasoningContent())
			lastStreamResponse.Choices[i].Delta.ReasoningContent = nil
			lastStreamResponse.Choices[i].Delta.Reasoning = nil
		} else if !hasThinkingContent && !hasContent {
			// flush thinking content
			lastStreamResponse.Choices[i].Delta.ReasoningContent = nil
			lastStreamResponse.Choices[i].Delta.Reasoning = nil
		}
	}

	return helper.ObjectData(c, lastStreamResponse)
}

func OaiStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	if bridgesChatToResponses(info) {
		return OaiChatStreamToResponsesHandler(c, info, resp)
	}
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	model := info.UpstreamModelName
	var responseId string
	var createAt int64 = 0
	var systemFingerprint string
	var containStreamUsage bool
	var responseTextBuilder strings.Builder
	var toolCount int
	var usage = &dto.Usage{}
	streamLog := newBoundedStreamChunks(maxBridgedStreamLogBytes)
	var lastStreamData string
	var lastStreamPending bool
	var upstreamStreamError *types.LazuError
	var terminal bool

	scan := helper.StreamScannerHandler(c, resp, info, func(data string) bool {
		if strings.TrimSpace(data) == "" {
			return true
		}
		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			upstreamStreamError = types.NewError(err, types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
			return false
		}
		if upstreamErr := chunk.GetOpenAIError(); upstreamErr != nil {
			// A chat stream also reports failure in-band under a 200. When it
			// happens before any chunk has been forwarded, the attempt never
			// became a response, so it is retryable like a pre-stream failure.
			// Once the client has seen output, replaying would duplicate it.
			//
			// "Forwarded" has to be judged from this handler's own state.
			// info.HasSendResponse() is already true here — the scanner stamps
			// it when the first UPSTREAM event arrives, before this callback
			// runs — and c.Writer.Written() is true as soon as a keep-alive ping
			// has gone out, which says nothing about content. lastStreamData is
			// empty until this handler has accepted a provider chunk.
			if lastStreamData == "" {
				common.SetContextKey(c, constant.ContextKeyStreamFirstEventFailed, true)
				upstreamStreamError = chatFirstEventLazuError(c, upstreamErr)
				return false
			}
			upstreamStreamError = types.WithOpenAIError(*upstreamErr, http.StatusOK, types.ErrOptionWithSkipRetry())
			return false
		}
		if service.ValidUsage(chunk.Usage) {
			copyUsage := *chunk.Usage
			copyUsage.MarkProviderUsage()
			usage = &copyUsage
			containStreamUsage = true
		}
		if info.RelayMode == relayconstant.RelayModeCompletions {
			for _, text := range gjson.Get(data, "choices.#.text").Array() {
				responseTextBuilder.WriteString(text.Str)
			}
		} else {
			_ = ProcessStreamResponse(chunk, &responseTextBuilder, &toolCount)
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				terminal = true
				// Kept so the outcome row can say how the reply ended. A lane
				// that quietly caps max_tokens shows up as a truncation rate
				// and nowhere else.
				info.FinishReason = *choice.FinishReason
			}
		}
		if lastStreamPending {
			err := HandleStreamFormat(c, info, lastStreamData, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent)
			if err != nil {
				common.SysLog("error handling stream format: " + err.Error())
			}
		}
		if len(data) > 0 {
			lastStreamData = data
			lastStreamPending = true
			streamLog.Add(data)
			// Content frames need no final usage annotation. Forward them on
			// arrival so the client's first token does not wait for another
			// provider event. Keep terminal/usage frames for cost annotation.
			if info.RelayFormat == types.RelayFormatOpenAI && !terminal && !service.ValidUsage(chunk.Usage) {
				if err := HandleStreamFormat(c, info, data, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent); err != nil {
					common.SysLog("error handling stream format: " + err.Error())
				}
				lastStreamPending = false
			}
		}
		return true
	})
	if canceledErr := streamClientCanceledError(c); canceledErr != nil {
		var observed bool
		if !service.ValidUsage(usage) {
			service.PromptTokensForUsage(c, info)
		}
		usage, observed = observedAccumulatedChatStreamUsage(info, responseTextBuilder.String(), toolCount, usage)
		info.StreamFailedWithoutUsage = !observed
		markCanceledStreamForSettlement(c, observed)
		return usage, canceledErr
	}
	if upstreamStreamError == nil {
		upstreamStreamError = scan.RelayError(lastStreamData != "" || c.Writer.Written())
	}
	if upstreamStreamError == nil && !scan.Done && !terminal {
		// A clean EOF before any response reaches the client is an upstream
		// failure, not a completed stream. Leave it retryable so the controller
		// can try another compatible channel. Once bytes are visible downstream,
		// replaying would duplicate a partial response.
		// lastStreamData is flushed below before returning this error, so it is
		// also a client-visible partial response even when Gin has not written it
		// at this exact point yet.
		if lastStreamData != "" || c.Writer.Written() || info.HasSendResponse() {
			upstreamStreamError = types.NewError(io.ErrUnexpectedEOF, types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
		} else {
			upstreamStreamError = types.NewError(io.ErrUnexpectedEOF, types.ErrorCodeBadResponse)
		}
	}
	if upstreamStreamError != nil {
		// Flush any retained frame before reporting failure, without a success marker.
		if lastStreamPending {
			_ = HandleStreamFormat(c, info, lastStreamData, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent)
		}
		service.PromptTokensForUsage(c, info)
		partial, observed := observedAccumulatedChatStreamUsage(info, responseTextBuilder.String(), toolCount, usage)
		info.StreamFailedWithoutUsage = !observed
		markCanceledStreamForSettlement(c, observed)
		return partial, upstreamStreamError
	}

	// 处理最后的响应
	shouldSendLastResp := true
	if err := handleLastResponse(lastStreamData, &responseId, &createAt, &systemFingerprint, &model, &usage,
		&containStreamUsage, info, &shouldSendLastResp); err != nil {
		logger.LogError(c, fmt.Sprintf("error handling last response: %s, lastStreamData: [%s]", err.Error(), lastStreamData))
	}

	// Normalize provider aliases before deciding whether input usage is absent.
	// Otherwise input_tokens can be overwritten by local fallback and mislabeled.
	applyUsagePostProcessing(info, usage, nil)
	if !containStreamUsage {
		usage = service.ResponseText2Usage(responseTextBuilder.String(), info.UpstreamModelName, service.PromptTokensForUsage(c, info))
		usage.CompletionTokens += toolCount * 7
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	} else if usage.PromptTokens == 0 {
		service.ApplyLocalPromptUsage(c, info, usage)
		if usage.TotalTokens < usage.PromptTokens+usage.CompletionTokens {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
	}

	applyUsagePostProcessing(info, usage, nil)
	preparedQuote := service.BuildTextBillingQuote(c, info, usage, "")
	service.AnnotateUsageCost(c, usage, preparedQuote.Quote)

	if info.RelayFormat == types.RelayFormatOpenAI {
		if shouldSendLastResp && lastStreamPending {
			if containStreamUsage {
				lastStreamData = injectUsageCostIntoStreamData(lastStreamData, usage, service.QuoteCostUSD(preparedQuote.Quote))
			}
			_ = sendStreamData(c, info, lastStreamData, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent)
		}
	}

	HandleFinalResponse(c, info, lastStreamData, responseId, createAt, model, systemFingerprint, usage, containStreamUsage)

	streamPayload := &service.ModelLogPayload{
		ResponseChunks: streamLog.Values(),
		Metadata:       streamLog.Metadata(),
		Usage:          usage,
		IsStream:       true,
		StatusCode:     http.StatusOK,
	}
	if lastStreamData != "" {
		streamPayload.ResponseBody = append([]byte(nil), []byte(lastStreamData)...)
	}
	service.RecordModelExchange(c, streamPayload)

	return usage, nil
}

func OaiChatStreamToResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)
	bridge := openai_responses.NewChatStreamBridge(helper.GetResponseID(c), info.UpstreamModelName, openai_responses.ChatStreamRetainedOutputLimit(info.ProtocolRequest))
	usage := &dto.Usage{}
	usageAccumulator := newChatStreamUsageAccumulator(info)
	streamLog := newBoundedStreamChunks(maxBridgedStreamLogBytes)
	var conversionErr error
	var upstreamStreamErr *types.OpenAIError
	helper.StreamScannerHandler(c, resp, info, func(data string) bool {
		if strings.TrimSpace(data) == "" || strings.TrimSpace(data) == "[DONE]" {
			return true
		}
		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			conversionErr = fmt.Errorf("decode chat stream chunk: %w", err)
			return false
		}
		if upstreamErr := chunk.GetOpenAIError(); upstreamErr != nil {
			upstreamStreamErr = upstreamErr
			streamLog.Add(data)
			return false
		}
		usageAccumulator.Observe(&chunk)
		streamLog.Add(data)
		conversionTimer := common.StartHotPathStage(common.HotPathStageProtocolStreamEvent)
		events, err := bridge.Consume(&chunk)
		conversionTimer.Finish(err)
		if err != nil {
			conversionErr = err
			return false
		}
		for _, event := range events {
			if event.Kind == textgen.EventUsage {
				continue
			}
			if err := emitResponsesBridgeEvent(c, event, info.ProtocolRequest); err != nil {
				conversionErr = err
				return false
			}
		}
		return true
	})
	if canceledErr := streamClientCanceledError(c); canceledErr != nil {
		var observed bool
		if !service.ValidUsage(usageAccumulator.providerUsage) {
			service.PromptTokensForUsage(c, info)
		}
		usage, observed = usageAccumulator.Usage(info, bridge)
		markCanceledStreamForSettlement(c, observed)
		return usage, canceledErr
	}
	if !service.ValidUsage(usageAccumulator.providerUsage) {
		service.PromptTokensForUsage(c, info)
	}
	usage, _ = usageAccumulator.Usage(info, bridge)
	if upstreamStreamErr != nil {
		emitResponsesBridgeUpstreamFailure(c, bridge, upstreamStreamErr, info.ProtocolRequest, usage)
		return usage, types.WithOpenAIError(*upstreamStreamErr, http.StatusOK, types.ErrOptionWithSkipRetry())
	}
	if conversionErr != nil {
		emitResponsesBridgeFailure(c, bridge, conversionErr, info.ProtocolRequest, usage)
		return usage, types.NewError(conversionErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	preparedQuote := service.BuildTextBillingQuote(c, info, usage, "")
	service.AnnotateUsageCost(c, usage, preparedQuote.Quote)
	conversionTimer := common.StartHotPathStage(common.HotPathStageProtocolStreamEvent)
	events, err := bridge.Finalize(usage)
	conversionTimer.Finish(err)
	if err != nil {
		emitResponsesBridgeFailure(c, bridge, err, info.ProtocolRequest, usage)
		return usage, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	for _, event := range events {
		if err := emitResponsesBridgeEvent(c, event, info.ProtocolRequest); err != nil {
			return usage, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
	}
	service.RecordModelExchange(c, &service.ModelLogPayload{ResponseChunks: streamLog.Values(), Usage: usage, IsStream: true, StatusCode: http.StatusOK, Metadata: streamLog.Metadata()})
	return usage, nil
}

func emitResponsesBridgeFailure(c *gin.Context, bridge *openai_responses.ChatStreamBridge, conversionErr error, request *textgen.Request, usage *dto.Usage) {
	emitResponsesBridgeFailureEvents(c, bridge.Fail(conversionErr, usage), request)
}

func emitResponsesBridgeUpstreamFailure(c *gin.Context, bridge *openai_responses.ChatStreamBridge, upstreamErr *types.OpenAIError, request *textgen.Request, usage *dto.Usage) {
	errorType := strings.TrimSpace(upstreamErr.Type)
	if errorType == "" || errorType == "upstream_error" {
		errorType = types.ErrorTypeAPI
	}
	code := string(types.ErrorCodeUpstreamStreamFailed)
	if upstreamErr.Code != nil && strings.TrimSpace(fmt.Sprint(upstreamErr.Code)) != "" {
		code = fmt.Sprint(upstreamErr.Code)
	}
	message := strings.TrimSpace(upstreamErr.Message)
	if message == "" {
		message = "upstream stream failed"
	}
	emitResponsesBridgeFailureEvents(c, bridge.FailWithSemanticError(&textgen.SemanticError{Type: errorType, Code: code, Message: message}, usage), request)
}

func emitResponsesBridgeFailureEvents(c *gin.Context, events []textgen.Event, request *textgen.Request) {
	emitted := false
	for _, event := range events {
		if err := emitResponsesBridgeEvent(c, event, request); err != nil {
			return
		}
		if event.Kind == textgen.EventResponseFailed {
			emitted = true
		}
	}
	if emitted {
		common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, true)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
	}
}

func emitResponsesBridgeEvent(c *gin.Context, event textgen.Event, request *textgen.Request) error {
	payload := openai_responses.EmitEvent(event, request)
	eventType, _ := payload["type"].(string)
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: eventType}, string(data))
	return nil
}

func bridgesChatToResponses(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ProtocolPlan != nil && !info.ProtocolPlan.Native &&
		info.ProtocolPlan.ClientProtocol.Family == textgen.ProtocolOpenAIResponses &&
		info.ProtocolPlan.UpstreamProtocol.Family == textgen.ProtocolOpenAIChat
}

func injectUsageCostIntoStreamData(streamData string, usage *dto.Usage, cost float64) string {
	if strings.TrimSpace(streamData) == "" {
		return streamData
	}
	var response dto.ChatCompletionsStreamResponse
	if err := common.Unmarshal(common.StringToByteSlice(streamData), &response); err != nil {
		return streamData
	}
	if response.Usage == nil {
		return streamData
	}
	if usage != nil {
		normalizedUsage := *usage
		response.Usage = &normalizedUsage
	}
	response.Usage.Cost = cost
	body, err := common.Marshal(response)
	if err != nil {
		return streamData
	}
	return string(body)
}

func OpenaiHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)

	var simpleResponse dto.OpenAITextResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		service.CaptureIncompleteModelResponse(c, responseBody, resp.StatusCode)
		return nil, service.UpstreamReadError(c, err)
	}
	if common.DebugEnabled {
		println("upstream response body:", string(responseBody))
	}
	err = common.Unmarshal(responseBody, &simpleResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if oaiError := simpleResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}
	// Non-streaming replies carry the finish reason on the choice itself.
	for _, choice := range simpleResponse.Choices {
		if choice.FinishReason != "" {
			info.FinishReason = choice.FinishReason
			break
		}
	}

	forceFormat := false
	if info.ChannelSetting.ForceFormat {
		forceFormat = true
	}

	usageModified := (simpleResponse.Usage.PromptTokens == 0 && simpleResponse.Usage.InputTokens != 0) ||
		(simpleResponse.Usage.CompletionTokens == 0 && simpleResponse.Usage.OutputTokens != 0) ||
		simpleResponse.Usage.TotalTokens == 0
	simpleResponse.Usage.MarkProviderUsage()
	applyUsagePostProcessing(info, &simpleResponse.Usage, responseBody)
	if simpleResponse.Usage.PromptTokens == 0 {
		service.ApplyLocalPromptUsage(c, info, &simpleResponse.Usage)
		usageModified = true
	}
	if simpleResponse.Usage.CompletionTokens == 0 {
		simpleResponse.Usage.OutputTokenSource = dto.UsageSourceLocalEstimate
		simpleResponse.Usage.CompletionTokens = estimateOpenAICompletionTokens(&simpleResponse, info.UpstreamModelName)
		if simpleResponse.Usage.CompletionTokens != 0 {
			usageModified = true
		}
	}
	if usageModified || simpleResponse.Usage.TotalTokens == 0 {
		simpleResponse.Usage.TotalTokens = simpleResponse.Usage.PromptTokens + simpleResponse.Usage.CompletionTokens
	}

	applyUsagePostProcessing(info, &simpleResponse.Usage, responseBody)
	preparedQuote := service.BuildTextBillingQuote(c, info, &simpleResponse.Usage, "")
	service.AnnotateUsageCost(c, &simpleResponse.Usage, preparedQuote.Quote)

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		if usageModified || simpleResponse.Usage.Cost != nil {
			var bodyMap map[string]interface{}
			err = common.Unmarshal(responseBody, &bodyMap)
			if err != nil {
				return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			}
			bodyMap["usage"] = simpleResponse.Usage
			responseBody, _ = common.Marshal(bodyMap)
		}
		if forceFormat {
			responseBody, err = common.Marshal(simpleResponse)
			if err != nil {
				return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
			}
		} else {
			break
		}
	case types.RelayFormatClaude:
		claudeResp := service.ResponseOpenAI2Claude(&simpleResponse, info)
		claudeRespStr, err := common.Marshal(claudeResp)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responseBody = claudeRespStr
	case types.RelayFormatGemini:
		geminiResp := service.ResponseOpenAI2Gemini(&simpleResponse, info)
		geminiRespStr, err := common.Marshal(geminiResp)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responseBody = geminiRespStr
	case types.RelayFormatOpenAIResponses:
		if bridgesChatToResponses(info) {
			responsesBody, marshalErr := common.Marshal(openai_responses.ChatResponseToResponses(&simpleResponse, info.UpstreamModelName, info.ProtocolRequest))
			if marshalErr != nil {
				return nil, types.NewError(marshalErr, types.ErrorCodeBadResponseBody)
			}
			responseBody = responsesBody
		}
	}

	service.AttachModelLogPayload(c, &service.ModelLogPayload{
		ResponseBody: append([]byte(nil), responseBody...),
		Usage:        simpleResponse.Usage,
		StatusCode:   resp.StatusCode,
	})
	service.IOCopyBytesGracefully(c, resp, responseBody)

	return &simpleResponse.Usage, nil
}

func estimateOpenAICompletionTokens(response *dto.OpenAITextResponse, modelName string) int {
	if response == nil {
		return 0
	}

	var content strings.Builder
	toolCount := 0
	for _, choice := range response.Choices {
		content.WriteString(choice.Message.StringContent())
		if choice.Message.Refusal != nil {
			content.WriteString(*choice.Message.Refusal)
		}
		content.WriteString(choice.Message.ReasoningContent)
		content.WriteString(choice.Message.Reasoning)
		toolCalls := choice.Message.ParseToolCalls()
		toolCount += len(toolCalls)
		for _, toolCall := range toolCalls {
			content.WriteString(toolCall.Function.Name)
			content.WriteString(toolCall.Function.Arguments)
		}
	}

	return service.CountTextToken(content.String(), modelName) + toolCount*7
}

func streamTTSResponse(c *gin.Context, resp *http.Response) {
	c.Writer.WriteHeaderNow()

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		logger.LogWarn(c, "streaming not supported")
		_, err := io.Copy(c.Writer, resp.Body)
		if err != nil {
			logger.LogWarn(c, err.Error())
		}
		return
	}

	buffer := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buffer)
		//logger.LogInfo(c, fmt.Sprintf("streamTTSResponse read %d bytes", n))
		if n > 0 {
			if _, writeErr := c.Writer.Write(buffer[:n]); writeErr != nil {
				logger.LogError(c, writeErr.Error())
				break
			}
			flusher.Flush()
		}
		if err != nil {
			if err != io.EOF {
				logger.LogError(c, err.Error())
			}
			break
		}
	}
}

func OpenaiTTSHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) *dto.Usage {
	// the status code has been judged before, if there is a body reading failure,
	// it should be regarded as a non-recoverable error, so it should not return err for external retry.
	// Analogous to nginx's load balancing, it will only retry if it can't be requested or
	// if the upstream returns a specific status code, once the upstream has already written the header,
	// the subsequent failure of the response body should be regarded as a non-recoverable error,
	// and can be terminated directly.
	defer service.CloseResponseBodyGracefully(resp)
	usage := &dto.Usage{}
	service.ApplyLocalPromptUsage(c, info, usage)
	usage.TotalTokens = usage.PromptTokens
	for k, v := range resp.Header {
		c.Writer.Header().Set(k, v[0])
	}
	c.Writer.WriteHeader(resp.StatusCode)

	isStreaming := resp.ContentLength == -1 || resp.Header.Get("Content-Length") == ""
	if isStreaming {
		streamTTSResponse(c, resp)
	} else {
		c.Writer.WriteHeaderNow()
		_, err := io.Copy(c.Writer, resp.Body)
		if err != nil {
			logger.LogError(c, err.Error())
		}
	}
	statusCode := http.StatusOK
	metadata := map[string]any{
		"response_type": "audio",
	}
	if resp != nil {
		statusCode = resp.StatusCode
		metadata["content_type"] = resp.Header.Get("Content-Type")
	}
	service.RecordModelExchange(c, &service.ModelLogPayload{
		StatusCode: statusCode,
		Usage:      usage,
		Metadata:   metadata,
	})
	return usage
}

func OpenaiSTTHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, responseFormat string) (*types.LazuError, *dto.Usage) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		service.CaptureIncompleteModelResponse(c, responseBody, resp.StatusCode)
		return service.UpstreamReadError(c, err), nil
	}

	var responseData struct {
		Usage *dto.Usage `json:"usage"`
	}
	var (
		usage  *dto.Usage
		apiErr *types.LazuError
	)

	if err := json.Unmarshal(responseBody, &responseData); err == nil && service.ValidUsage(responseData.Usage) {
		usage = responseData.Usage
		usage.MarkProviderUsage()
		applyUsagePostProcessing(info, usage, responseBody)
	} else {
		audioTokens, tokenErr := countAudioTokens(c)
		if tokenErr != nil {
			apiErr = types.NewError(tokenErr, types.ErrorCodeTokenizationError)
		} else {
			usage = &dto.Usage{
				PromptTokens:     audioTokens,
				InputTokenSource: dto.UsageSourceLocalEstimate,
				CompletionTokens: 0,
				TotalTokens:      audioTokens,
			}
		}
	}

	payload := &service.ModelLogPayload{
		ResponseBody: append([]byte(nil), responseBody...),
		StatusCode:   resp.StatusCode,
	}
	if usage != nil {
		payload.Usage = usage
	}
	service.AttachModelLogPayload(c, payload)
	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	if apiErr != nil {
		return apiErr, nil
	}
	return nil, usage
}

func countAudioTokens(c *gin.Context) (int, error) {
	body, err := common.GetRequestBody(c)
	if err != nil {
		return 0, errors.WithStack(err)
	}

	var reqBody struct {
		File *multipart.FileHeader `form:"file" binding:"required"`
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	if err = c.ShouldBind(&reqBody); err != nil {
		return 0, errors.WithStack(err)
	}
	ext := filepath.Ext(reqBody.File.Filename) // 获取文件扩展名
	reqFp, err := reqBody.File.Open()
	if err != nil {
		return 0, errors.WithStack(err)
	}
	defer reqFp.Close()

	tmpFp, err := os.CreateTemp("", "audio-*"+ext)
	if err != nil {
		return 0, errors.WithStack(err)
	}
	defer os.Remove(tmpFp.Name())

	_, err = io.Copy(tmpFp, reqFp)
	if err != nil {
		return 0, errors.WithStack(err)
	}
	if err = tmpFp.Close(); err != nil {
		return 0, errors.WithStack(err)
	}

	duration, err := common.GetAudioDuration(c.Request.Context(), tmpFp.Name(), ext)
	if err != nil {
		return 0, errors.WithStack(err)
	}

	return int(math.Round(math.Ceil(duration) / 60.0 * 1000)), nil // 1 minute 相当于 1k tokens
}

func OpenaiRealtimeHandler(c *gin.Context, info *relaycommon.RelayInfo) (*types.LazuError, *dto.RealtimeUsage) {
	if info == nil || info.ClientWs == nil || info.TargetWs == nil {
		return types.NewError(fmt.Errorf("invalid websocket connection"), types.ErrorCodeBadResponse), nil
	}

	info.IsStream = true
	clientConn := info.ClientWs
	targetConn := info.TargetWs

	clientClosed := make(chan struct{})
	targetClosed := make(chan struct{})
	sendChan := make(chan []byte, 100)
	receiveChan := make(chan []byte, 100)
	errChan := make(chan error, 2)

	usage := &dto.RealtimeUsage{}
	localUsage := &dto.RealtimeUsage{}
	sumUsage := &dto.RealtimeUsage{}
	var usageMu sync.Mutex
	var readers sync.WaitGroup
	readers.Add(2)

	common.SafeCtxGo(c.Request.Context(), "openai-realtime-client-reader", func() {
		defer readers.Done()
		usageLocked := false
		defer func() {
			if usageLocked {
				usageMu.Unlock()
			}
			if r := recover(); r != nil {
				errChan <- fmt.Errorf("panic in client reader: %v", r)
			}
		}()
		for {
			select {
			case <-c.Request.Context().Done():
				return
			default:
				_, message, err := clientConn.ReadMessage()
				if err != nil {
					if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && !errors.Is(err, net.ErrClosed) {
						errChan <- fmt.Errorf("error reading from client: %v", err)
					}
					close(clientClosed)
					return
				}

				realtimeEvent := &dto.RealtimeEvent{}
				err = common.Unmarshal(message, realtimeEvent)
				if err != nil {
					errChan <- fmt.Errorf("error unmarshalling message: %v", err)
					return
				}

				usageMu.Lock()
				usageLocked = true
				if realtimeEvent.Type == dto.RealtimeEventTypeSessionUpdate {
					if realtimeEvent.Session != nil {
						if realtimeEvent.Session.Tools != nil {
							info.RealtimeTools = realtimeEvent.Session.Tools
						}
					}
				}

				textToken, audioToken, err := service.CountTokenRealtime(info, *realtimeEvent, info.UpstreamModelName)
				usageMu.Unlock()
				usageLocked = false
				if err != nil {
					errChan <- fmt.Errorf("error counting text token: %v", err)
					return
				}
				logger.LogInfo(c, fmt.Sprintf("type: %s, textToken: %d, audioToken: %d", realtimeEvent.Type, textToken, audioToken))
				err = helper.WssString(c, targetConn, string(message))
				if err != nil {
					errChan <- fmt.Errorf("error writing to target: %v", err)
					return
				}
				usageMu.Lock()
				usageLocked = true
				localUsage.TotalTokens += textToken + audioToken
				localUsage.InputTokens += textToken + audioToken
				localUsage.InputTokenDetails.TextTokens += textToken
				localUsage.InputTokenDetails.AudioTokens += audioToken
				usageMu.Unlock()
				usageLocked = false

				select {
				case sendChan <- message:
				default:
				}
			}
		}
	})

	common.SafeCtxGo(c.Request.Context(), "openai-realtime-target-reader", func() {
		defer readers.Done()
		usageLocked := false
		defer func() {
			if usageLocked {
				usageMu.Unlock()
			}
			if r := recover(); r != nil {
				errChan <- fmt.Errorf("panic in target reader: %v", r)
			}
		}()
		for {
			select {
			case <-c.Request.Context().Done():
				return
			default:
				_, message, err := targetConn.ReadMessage()
				if err != nil {
					if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && !errors.Is(err, net.ErrClosed) {
						errChan <- fmt.Errorf("error reading from target: %v", err)
					}
					close(targetClosed)
					return
				}
				info.SetFirstResponseTime()
				realtimeEvent := &dto.RealtimeEvent{}
				err = common.Unmarshal(message, realtimeEvent)
				if err != nil {
					errChan <- fmt.Errorf("error unmarshalling message: %v", err)
					return
				}

				usageMu.Lock()
				usageLocked = true
				if realtimeEvent.Type == dto.RealtimeEventTypeResponseDone {
					if realtimeEvent.Response == nil {
						usageMu.Unlock()
						usageLocked = false
						errChan <- fmt.Errorf("response.done has no response")
						return
					}
					realtimeUsage := realtimeEvent.Response.Usage
					if realtimeUsage != nil {
						accumulateRealtimeUsage(usage, realtimeUsage)
						err := preConsumeUsage(c, info, usage, sumUsage)
						if err != nil {
							usageMu.Unlock()
							usageLocked = false
							errChan <- fmt.Errorf("error consume usage: %v", err)
							return
						}
						// 本次计费完成，清除
						usage = &dto.RealtimeUsage{}

						localUsage = &dto.RealtimeUsage{}
					} else {
						textToken, audioToken, err := service.CountTokenRealtime(info, *realtimeEvent, info.UpstreamModelName)
						if err != nil {
							usageMu.Unlock()
							usageLocked = false
							errChan <- fmt.Errorf("error counting text token: %v", err)
							return
						}
						logger.LogInfo(c, fmt.Sprintf("type: %s, textToken: %d, audioToken: %d", realtimeEvent.Type, textToken, audioToken))
						localUsage.TotalTokens += textToken + audioToken
						info.IsFirstRequest = false
						localUsage.InputTokens += textToken + audioToken
						localUsage.InputTokenDetails.TextTokens += textToken
						localUsage.InputTokenDetails.AudioTokens += audioToken
						err = preConsumeUsage(c, info, localUsage, sumUsage)
						if err != nil {
							usageMu.Unlock()
							usageLocked = false
							errChan <- fmt.Errorf("error consume usage: %v", err)
							return
						}
						// 本次计费完成，清除
						localUsage = &dto.RealtimeUsage{}
						// print now usage
					}
					logger.LogInfo(c, fmt.Sprintf("realtime streaming sumUsage: %v", sumUsage))
					logger.LogInfo(c, fmt.Sprintf("realtime streaming localUsage: %v", localUsage))
					logger.LogInfo(c, fmt.Sprintf("realtime streaming localUsage: %v", localUsage))

				} else if realtimeEvent.Type == dto.RealtimeEventTypeSessionUpdated || realtimeEvent.Type == dto.RealtimeEventTypeSessionCreated {
					realtimeSession := realtimeEvent.Session
					if realtimeSession != nil {
						// update audio format
						info.InputAudioFormat = common.GetStringIfEmpty(realtimeSession.InputAudioFormat, info.InputAudioFormat)
						info.OutputAudioFormat = common.GetStringIfEmpty(realtimeSession.OutputAudioFormat, info.OutputAudioFormat)
					}
				} else {
					textToken, audioToken, err := service.CountTokenRealtime(info, *realtimeEvent, info.UpstreamModelName)
					if err != nil {
						usageMu.Unlock()
						usageLocked = false
						errChan <- fmt.Errorf("error counting text token: %v", err)
						return
					}
					logger.LogInfo(c, fmt.Sprintf("type: %s, textToken: %d, audioToken: %d", realtimeEvent.Type, textToken, audioToken))
					localUsage.TotalTokens += textToken + audioToken
					localUsage.OutputTokens += textToken + audioToken
					localUsage.OutputTokenDetails.TextTokens += textToken
					localUsage.OutputTokenDetails.AudioTokens += audioToken
				}

				usageMu.Unlock()
				usageLocked = false
				err = helper.WssString(c, clientConn, string(message))
				if err != nil {
					errChan <- fmt.Errorf("error writing to client: %v", err)
					return
				}

				select {
				case receiveChan <- message:
				default:
				}
			}
		}
	})

	var terminalErr *types.LazuError
	select {
	case <-clientClosed:
	case <-targetClosed:
	case err := <-errChan:
		logger.LogError(c, "realtime error: "+err.Error())
		terminalErr = realtimeTerminationError(err)
	case <-c.Request.Context().Done():
		terminalErr = types.NewError(c.Request.Context().Err(), types.ErrorCodeClientCanceled, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	// Closing unblocks both socket readers. Join them before the final usage
	// snapshot, including disconnects, so no usage can arrive after settlement.
	_ = clientConn.Close()
	_ = targetConn.Close()
	readers.Wait()
	if terminalErr == nil {
		// A read failure also closes its completion channel. If the select saw
		// that channel first, retain the accompanying failure after both readers
		// have exited instead of nondeterministically reporting success.
		select {
		case err := <-errChan:
			terminalErr = realtimeTerminationError(err)
		default:
		}
	}

	if usage.TotalTokens != 0 {
		_ = preConsumeUsage(c, info, usage, sumUsage)
	}

	if localUsage.TotalTokens != 0 {
		_ = preConsumeUsage(c, info, localUsage, sumUsage)
	}

	// check usage total tokens, if 0, use local usage

	return terminalErr, sumUsage
}

func realtimeTerminationError(err error) *types.LazuError {
	if strings.HasPrefix(err.Error(), "error reading from client:") || strings.HasPrefix(err.Error(), "error writing to client:") {
		return types.NewError(err, types.ErrorCodeClientCanceled, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return types.NewError(err, types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
}

func preConsumeUsage(ctx *gin.Context, info *relaycommon.RelayInfo, usage *dto.RealtimeUsage, totalUsage *dto.RealtimeUsage) error {
	if usage == nil || totalUsage == nil {
		return fmt.Errorf("invalid usage pointer")
	}

	segment := *usage
	accumulateRealtimeUsage(totalUsage, &segment)
	// A failed segment settlement must not make termination count it again.
	*usage = dto.RealtimeUsage{}
	return service.PreWssConsumeQuota(ctx, info, &segment)
}

func accumulateRealtimeUsage(target *dto.RealtimeUsage, source *dto.RealtimeUsage) {
	if target == nil || source == nil {
		return
	}

	target.TotalTokens += source.TotalTokens
	target.InputTokens += source.InputTokens
	target.OutputTokens += source.OutputTokens
	target.InputTokenDetails.CachedTokens += source.InputTokenDetails.CachedTokens
	target.InputTokenDetails.CachedTokensDetails.TextTokens += source.InputTokenDetails.CachedTokensDetails.TextTokens
	target.InputTokenDetails.CachedTokensDetails.AudioTokens += source.InputTokenDetails.CachedTokensDetails.AudioTokens
	target.InputTokenDetails.CachedTokensDetails.ImageTokens += source.InputTokenDetails.CachedTokensDetails.ImageTokens
	target.InputTokenDetails.CacheWriteTokens += source.InputTokenDetails.CacheWriteTokens
	target.InputTokenDetails.CacheMissTokens += source.InputTokenDetails.CacheMissTokens
	target.InputTokenDetails.CachedCreationTokens += source.InputTokenDetails.CachedCreationTokens
	target.InputTokenDetails.CachedCreation5mTokens += source.InputTokenDetails.CachedCreation5mTokens
	target.InputTokenDetails.CachedCreation1hTokens += source.InputTokenDetails.CachedCreation1hTokens
	target.InputTokenDetails.TextTokens += source.InputTokenDetails.TextTokens
	target.InputTokenDetails.AudioTokens += source.InputTokenDetails.AudioTokens
	target.InputTokenDetails.ImageTokens += source.InputTokenDetails.ImageTokens
	target.OutputTokenDetails.TextTokens += source.OutputTokenDetails.TextTokens
	target.OutputTokenDetails.AudioTokens += source.OutputTokenDetails.AudioTokens
	target.OutputTokenDetails.ImageTokens += source.OutputTokenDetails.ImageTokens
	target.OutputTokenDetails.ReasoningTokens += source.OutputTokenDetails.ReasoningTokens
	target.OutputTokenDetails.AcceptedPredictionTokens += source.OutputTokenDetails.AcceptedPredictionTokens
	target.OutputTokenDetails.RejectedPredictionTokens += source.OutputTokenDetails.RejectedPredictionTokens
}

func OpenaiHandlerWithUsage(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		service.CaptureIncompleteModelResponse(c, responseBody, resp.StatusCode)
		return nil, service.UpstreamReadError(c, err)
	}

	if request, ok := info.Request.(*dto.ImageRequest); ok && dto.IsSeedream(request.Model) {
		return seedreamResponse(c, info, resp, responseBody, request)
	}

	var usageResp dto.SimpleResponse
	err = common.Unmarshal(responseBody, &usageResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	usageResp.Usage.MarkProviderUsage()
	applyUsagePostProcessing(info, &usageResp.Usage, responseBody)
	preparedQuote := service.BuildTextBillingQuote(c, info, &usageResp.Usage, "")
	service.AnnotateUsageCost(c, &usageResp.Usage, preparedQuote.Quote)
	var bodyMap map[string]interface{}
	if err := common.Unmarshal(responseBody, &bodyMap); err == nil {
		bodyMap["usage"] = usageResp.Usage
		if updatedBody, marshalErr := common.Marshal(bodyMap); marshalErr == nil {
			responseBody = updatedBody
		}
	}

	service.AttachModelLogPayload(c, &service.ModelLogPayload{
		ResponseBody: append([]byte(nil), responseBody...),
		Usage:        usageResp.Usage,
		StatusCode:   resp.StatusCode,
	})

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// Once we've written to the client, we should not return errors anymore
	// because the upstream has already consumed resources and returned content
	// We should still perform billing even if parsing fails
	return &usageResp.Usage, nil
}

func applyUsagePostProcessing(info *relaycommon.RelayInfo, usage *dto.Usage, responseBody []byte) {
	if info == nil || usage == nil {
		return
	}
	if usage.PromptTokensDetails.CachedTokens == 0 && usage.PromptCacheHitTokens > 0 {
		usage.PromptTokensDetails.CachedTokens = usage.PromptCacheHitTokens
	}
	if usage.PromptTokensDetails.CachedTokens == 0 && usage.InputTokensDetails != nil && usage.InputTokensDetails.CachedTokens > 0 {
		usage.PromptTokensDetails.CachedTokens = usage.InputTokensDetails.CachedTokens
	}
	if usage.PromptTokensDetails.CachedTokens == 0 {
		if cachedTokens, ok := extractCachedTokensFromBody(responseBody); ok {
			usage.PromptTokensDetails.CachedTokens = cachedTokens
		}
	}
	if usage.PromptTokensDetails.CacheMissTokens == 0 && usage.PromptCacheMissTokens > 0 {
		usage.PromptTokensDetails.CacheMissTokens = usage.PromptCacheMissTokens
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens > 0 && usage.TotalTokens >= usage.CompletionTokens {
		usage.PromptTokens = usage.TotalTokens - usage.CompletionTokens
	}
	if usage.CompletionTokens == 0 && usage.PromptTokens > 0 && usage.TotalTokens >= usage.PromptTokens {
		usage.CompletionTokens = usage.TotalTokens - usage.PromptTokens
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	usagepkg.NormalizeDTOUsageFields(usage)
}

func extractCachedTokensFromBody(body []byte) (int, bool) {
	if len(body) == 0 {
		return 0, false
	}

	var payload struct {
		Usage struct {
			PromptTokensDetails struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CachedTokens         *int `json:"cached_tokens"`
			PromptCacheHitTokens *int `json:"prompt_cache_hit_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, false
	}

	if payload.Usage.PromptTokensDetails.CachedTokens != nil {
		return *payload.Usage.PromptTokensDetails.CachedTokens, true
	}
	if payload.Usage.CachedTokens != nil {
		return *payload.Usage.CachedTokens, true
	}
	if payload.Usage.PromptCacheHitTokens != nil {
		return *payload.Usage.PromptCacheHitTokens, true
	}
	return 0, false
}
