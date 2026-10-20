package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		service.CaptureIncompleteModelResponse(c, responseBody, resp.StatusCode)
		return nil, service.UpstreamReadError(c, err)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	if responsesResponse.HasImageGenerationCall() {
		c.Set("image_generation_call", true)
		c.Set("image_generation_call_quality", responsesResponse.GetQuality())
		c.Set("image_generation_call_size", responsesResponse.GetSize())
	}

	// compute usage
	usage := &dto.Usage{}
	if responsesResponse.Usage != nil {
		applyResponsesUsage(usage, responsesResponse.Usage)
	}
	recordResponsesFinishReason(info, &responsesResponse)
	countResponsesBuiltInToolCalls(info, responsesResponse.Output)
	modelName := responsesResponse.Model
	if info.ChannelMeta != nil && info.UpstreamModelName != "" {
		modelName = info.UpstreamModelName
	}
	chatResponse := openai_responses.ResponsesResponseToChat(&responsesResponse, modelName)
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = estimateOpenAICompletionTokens(chatResponse, modelName)
		usage.OutputTokenSource = dto.UsageSourceLocalEstimate
	}
	if usage.PromptTokens == 0 && (usage.CompletionTokens > 0 || responsesResponse.Status == "completed" || responsesResponse.Status == "incomplete") {
		service.ApplyLocalPromptUsage(c, info, usage)
	}
	usage.TotalTokens = max(usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)

	if bridgesResponsesToChat(info) {
		chatResponse.Usage = *usage
		responseBody, err = common.Marshal(chatResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		service.AttachModelLogPayload(c, &service.ModelLogPayload{
			ResponseBody: append([]byte(nil), responseBody...),
			Usage:        chatResponse.Usage,
			StatusCode:   resp.StatusCode,
		})
		service.IOCopyBytesGracefully(c, resp, responseBody)
		return &chatResponse.Usage, nil
	}

	service.AttachModelLogPayload(c, &service.ModelLogPayload{
		ResponseBody: append([]byte(nil), responseBody...),
		Usage:        usage,
		StatusCode:   resp.StatusCode,
	})
	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	if bridgesResponsesToChat(info) {
		// a Chat client on a Responses upstream: the hub
		return chatClientResponsesStream(c, info, resp)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	var functionCallCount int
	functionArgumentBytes := make(map[string]int)
	unkeyedFunctionArgumentBytes := 0
	streamLog := newBoundedStreamChunks(maxBridgedStreamLogBytes)
	var lastData string
	defer func() {
		// The normal path has already archived the exchange. On early returns
		// retain the last received event for the controller's terminal hook.
		service.CaptureIncompleteModelResponse(c, []byte(lastData), resp.StatusCode)
	}()
	terminalType := ""
	// Native passthrough only: whether any event has been forwarded yet, and the
	// failure carried by a stream that failed before forwarding any.
	nativeStreamStarted := false
	nativeFirstEventFailed := false
	var nativeFirstEventError *types.OpenAIError
	logicalTerminal := false

	helper.StreamScannerHandlerWithTerminal(c, resp, info, func(data string) bool {
		conversionTimer := common.StartHotPathStage(common.HotPathStageProtocolStreamEvent)

		// 检查当前数据是否包含终止状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err == nil {
			stopAfterEvent := false
			// A Responses stream reports failure in-band: the transport is a
			// healthy 200 and the provider sends `error` or `response.failed`
			// instead of output. Forwarding that first event would make the
			// attempt look successful to the controller — no error row, no
			// health signal — and would start the response, which bars any
			// retry. Hold it instead and fail the attempt for real.
			//
			// nativeStreamStarted tracks forwarded CONTENT, deliberately not
			// c.Writer.Written(): a keep-alive ping commits the SSE response
			// without carrying any content, and a stall long enough to emit
			// one is exactly when this hold matters most.
			if !nativeStreamStarted && isResponsesFailureEvent(&streamResponse) {
				nativeFirstEventFailed = true
				nativeFirstEventError = responsesStreamFailureError(&streamResponse)
				conversionTimer.Finish(nil)
				return false
			}
			nativeStreamStarted = true
			sendResponsesStreamData(c, streamResponse, data)
			switch streamResponse.Type {
			case "error":
				// a content-bearing native failure stays passthrough; it is a
				// protocol end and need not wait for the HTTP transfer EOF
				logicalTerminal = true
				stopAfterEvent = true
			case "response.completed", "response.incomplete", "response.failed":
				terminalType = streamResponse.Type
				if streamResponse.Response != nil {
					recordResponsesFinishReason(info, streamResponse.Response)
					if streamResponse.Response.Usage != nil {
						applyResponsesUsage(usage, streamResponse.Response.Usage)
					}
					if streamResponse.Response.HasImageGenerationCall() {
						c.Set("image_generation_call", true)
						c.Set("image_generation_call_quality", streamResponse.Response.GetQuality())
						c.Set("image_generation_call_size", streamResponse.Response.GetSize())
					}
				}
				// Failure is authoritative by event type even when its optional
				// response/error payload is absent. Success requires its response
				// object so a malformed completion cannot claim a drained stream.
				if streamResponse.Response != nil || streamResponse.Type == "response.failed" {
					logicalTerminal = true
					stopAfterEvent = true
				}
			case "response.output_text.delta":
				// 处理输出文本
				responseTextBuilder.WriteString(streamResponse.Delta)
			case "response.refusal.delta":
				responseTextBuilder.WriteString(streamResponse.Delta)
			case "response.function_call_arguments.delta":
				responseTextBuilder.WriteString(streamResponse.Delta)
				if key := responsesFunctionArgumentKey(&streamResponse, nil); key != "" {
					functionArgumentBytes[key] += len(streamResponse.Delta)
				} else {
					unkeyedFunctionArgumentBytes += len(streamResponse.Delta)
				}
			case dto.ResponsesOutputTypeItemDone:
				// 函数调用处理
				if streamResponse.Item != nil {
					switch streamResponse.Item.Type {
					case dto.BuildInCallWebSearchCall:
						if isBillableResponsesWebSearchCall(streamResponse.Item) {
							incrementResponsesBuiltInToolCall(info, dto.BuildInToolWebSearch, dto.BuildInToolWebSearchPreview)
						}
					case dto.BuildInCallFileSearchCall:
						incrementResponsesBuiltInToolCall(info, dto.BuildInToolFileSearch)
					case dto.ResponsesOutputTypeFunctionCall:
						functionCallCount++
						responseTextBuilder.WriteString(streamResponse.Item.Name)
						observedBytes := functionArgumentBytes[responsesFunctionArgumentKey(&streamResponse, streamResponse.Item)]
						if observedBytes == 0 && unkeyedFunctionArgumentBytes > 0 {
							observedBytes = unkeyedFunctionArgumentBytes
							unkeyedFunctionArgumentBytes = 0
						}
						if len(streamResponse.Item.Arguments) > observedBytes {
							responseTextBuilder.WriteString(streamResponse.Item.Arguments[observedBytes:])
						}
					}
				}
			}
			conversionTimer.Finish(nil)
			streamLog.Add(data)
			lastData = data
			return !stopAfterEvent
		} else {
			conversionTimer.Finish(err)
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
		}
		streamLog.Add(data)
		lastData = data
		return true
	}, func() bool { return logicalTerminal })
	if canceledErr := streamClientCanceledError(c); canceledErr != nil {
		if !service.ValidUsage(usage) {
			service.PromptTokensForUsage(c, info)
		}
		observed := observedResponsesStreamUsage(info, usage, responseTextBuilder.String(), functionCallCount)
		markCanceledStreamForSettlement(c, observed)
		return usage, canceledErr
	}
	if nativeFirstEventFailed {
		// Nothing reached the client, so this is indistinguishable from a
		// pre-stream failure: retryable, and recorded as an error rather than a
		// zero-token "consumption". Map it onto the upstream status it stands
		// for so the retry classifier treats it like the overload it is.
		service.PromptTokensForUsage(c, info)
		info.StreamFailedWithoutUsage = true
		common.SetContextKey(c, constant.ContextKeyStreamFirstEventFailed, true)
		return usage, channel.FirstEventFailure(c, nativeFirstEventError)
	}

	if usage.CompletionTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := responseTextBuilder.String()
		if len(tempStr) > 0 || functionCallCount > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := service.CountTextToken(tempStr, info.UpstreamModelName)
			completionTokens += functionCallCount * 7
			usage.CompletionTokens = completionTokens
			usage.OutputTokenSource = dto.UsageSourceLocalEstimate
		}
	}

	if usage.PromptTokens == 0 && (usage.CompletionTokens != 0 || terminalType == "response.completed" || terminalType == "response.incomplete") {
		service.ApplyLocalPromptUsage(c, info, usage)
	}

	if usage.TotalTokens < usage.PromptTokens+usage.CompletionTokens {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	statusCode := http.StatusOK
	streamPayload := &service.ModelLogPayload{
		ResponseChunks: streamLog.Values(),
		Metadata:       streamLog.Metadata(),
		Usage:          usage,
		IsStream:       true,
		StatusCode:     statusCode,
	}
	if lastData != "" {
		streamPayload.ResponseBody = append([]byte(nil), []byte(lastData)...)
	}
	service.RecordModelExchange(c, streamPayload)
	return usage, nil
}

// isResponsesFailureEvent reports whether a stream event is the provider
// reporting failure rather than producing output.
func isResponsesFailureEvent(event *dto.ResponsesStreamResponse) bool {
	if event == nil {
		return false
	}
	switch event.Type {
	case "error", "response.failed":
		return true
	}
	return false
}

// responsesStreamFailureError pulls the provider's error out of a failure event.
// An `error` event carries it at the top level; a `response.failed` event nests
// it under the response object. Either may be absent, and the failure still
// stands on the event type alone.
func responsesStreamFailureError(event *dto.ResponsesStreamResponse) *types.OpenAIError {
	if event == nil {
		return nil
	}
	if upstreamError := event.GetOpenAIError(); upstreamError != nil {
		return upstreamError
	}
	if event.Response != nil {
		return event.Response.GetOpenAIError()
	}
	return nil
}

func responsesFunctionArgumentKey(event *dto.ResponsesStreamResponse, item *dto.ResponsesOutput) string {
	if item != nil && item.ID != "" {
		return "id:" + item.ID
	}
	if event == nil {
		return ""
	}
	if event.ItemID != "" {
		return "id:" + event.ItemID
	}
	if event.OutputIndex != nil {
		return fmt.Sprintf("output:%d", *event.OutputIndex)
	}
	return ""
}

func sendResponsesCompatChatStreamError(c *gin.Context, upstreamError *types.OpenAIError) error {
	frame, message := channel.StreamFailureFrame(types.ErrorCodeUpstreamStreamFailed, upstreamError)
	frame.RequestID = c.GetString(common.RequestIdKey)
	if err := helper.ObjectData(c, gin.H{"error": frame}); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func failResponsesCompatChatStream(c *gin.Context, code types.ErrorCode, message string) {
	var upstream *types.OpenAIError
	if message != "" {
		upstream = &types.OpenAIError{Message: message}
	}
	frame, _ := channel.StreamFailureFrame(code, upstream)
	frame.RequestID = c.GetString(common.RequestIdKey)
	_ = helper.ObjectData(c, gin.H{"error": frame})
	helper.Done(c)
	common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, true)
	common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
}

// recordResponsesFinishReason translates how a Responses reply ended into the
// chat vocabulary the outcome row stores. The Responses API has no
// finish_reason: a reply cut off at max_output_tokens arrives as status
// "incomplete" with a reason, and without this every Codex-shaped request left
// the lane's truncation rate unmeasured.
func recordResponsesFinishReason(info *relaycommon.RelayInfo, response *dto.OpenAIResponsesResponse) {
	if info == nil || response == nil {
		return
	}
	switch response.Status {
	case "completed":
		info.FinishReason = "stop"
	case "incomplete":
		reason := ""
		if response.IncompleteDetails != nil {
			reason = response.IncompleteDetails.Reason
		}
		switch reason {
		case "max_output_tokens":
			info.FinishReason = "length"
		case "content_filter":
			info.FinishReason = "content_filter"
		default:
			info.FinishReason = "incomplete"
		}
	}
}

func applyResponsesUsage(usage *dto.Usage, responsesUsage *dto.Usage) {
	if usage == nil || responsesUsage == nil {
		return
	}

	*usage = *responsesUsage
	usage.MarkProviderUsage()
	if responsesUsage.InputTokens != 0 || responsesUsage.PromptTokens == 0 {
		usage.PromptTokens = responsesUsage.InputTokens
	}
	if responsesUsage.OutputTokens != 0 || responsesUsage.CompletionTokens == 0 {
		usage.CompletionTokens = responsesUsage.OutputTokens
	}

	if responsesUsage.InputTokensDetails != nil {
		inputDetails := *responsesUsage.InputTokensDetails
		usage.InputTokensDetails = &inputDetails
		usage.PromptTokensDetails = inputDetails
	}
	if responsesUsage.OutputTokensDetails != nil {
		outputDetails := *responsesUsage.OutputTokensDetails
		usage.OutputTokensDetails = &outputDetails
		usage.CompletionTokenDetails = outputDetails
	}
	if usage.TotalTokens < usage.PromptTokens+usage.CompletionTokens {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
}

func bridgesResponsesToChat(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ProtocolPlan != nil && !info.ProtocolPlan.Native &&
		info.ProtocolPlan.ClientProtocol.Family == textgen.ProtocolOpenAIChat &&
		info.ProtocolPlan.UpstreamProtocol.Family == textgen.ProtocolOpenAIResponses
}

func incrementResponsesBuiltInToolCall(info *relaycommon.RelayInfo, toolTypes ...string) {
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		return
	}
	for _, toolType := range toolTypes {
		if tool, exists := info.ResponsesUsageInfo.BuiltInTools[toolType]; exists && tool != nil {
			tool.CallCount++
			return
		}
	}
}

func isBillableResponsesWebSearchCall(output *dto.ResponsesOutput) bool {
	return output != nil &&
		output.Type == dto.BuildInCallWebSearchCall &&
		strings.EqualFold(output.Status, "completed") &&
		output.Action != nil &&
		strings.EqualFold(output.Action.Type, "search")
}

func countResponsesBuiltInToolCalls(info *relaycommon.RelayInfo, outputs []dto.ResponsesOutput) {
	for _, output := range outputs {
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			if isBillableResponsesWebSearchCall(&output) {
				incrementResponsesBuiltInToolCall(info, dto.BuildInToolWebSearch, dto.BuildInToolWebSearchPreview)
			}
		case dto.BuildInCallFileSearchCall:
			incrementResponsesBuiltInToolCall(info, dto.BuildInToolFileSearch)
		}
	}
}

// chatFirstEventLazuError converts an in-band chat-stream failure that reached
// no client byte into a normal relay error, so it is retried, recorded and
// counted against channel health like any other upstream failure. Shares the
// Responses classifier: both protocols carry the same provider text under a
// transport-level 200.
func chatFirstEventLazuError(c *gin.Context, upstreamError *types.OpenAIError) *types.LazuError {
	return channel.FirstEventFailure(c, upstreamError)
}
