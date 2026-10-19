package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
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
	chatCompat := bridgesResponsesToChat(info)
	chatCompatState := &responsesChatCompatStreamState{}
	var conversionErr error
	var terminalFailure error
	terminalSeen := false
	terminalType := ""
	var terminalResponse *dto.OpenAIResponsesResponse
	var terminalStreamError *types.OpenAIError
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
			if !chatCompat {
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
			}
			chatCompatState.syncFromResponsesMeta(c, info, streamResponse.Response)

			switch streamResponse.Type {
			case "error":
				// Content-bearing native failures stay passthrough, while the
				// bridge retains its existing classified terminal error. Both are
				// protocol ends; neither needs to wait for the HTTP transfer EOF.
				logicalTerminal = true
				stopAfterEvent = true
				if chatCompat {
					terminalSeen = true
					terminalType = streamResponse.Type
					terminalStreamError = streamResponse.GetOpenAIError()
					stopAfterEvent = true
				}
			case "response.completed", "response.incomplete", "response.failed":
				terminalSeen = true
				terminalType = streamResponse.Type
				terminalResponse = streamResponse.Response
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
				if chatCompat {
					sendResponsesCompatChatTextDelta(c, info, chatCompatState, streamResponse.Delta)
				}
			case "response.refusal.delta":
				responseTextBuilder.WriteString(streamResponse.Delta)
				if chatCompat {
					sendResponsesCompatChatRefusalDelta(c, info, chatCompatState, streamResponse.Delta)
				}
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
						if chatCompat {
							chatCompatState.queueToolCall(streamResponse.Item)
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
			if chatCompat {
				conversionErr = fmt.Errorf("decode responses stream event: %w", err)
				return false
			}
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
		return usage, responsesFirstEventLazuError(c, nativeFirstEventError)
	}
	if conversionErr != nil {
		if !service.ValidUsage(usage) {
			service.PromptTokensForUsage(c, info)
		}
		observedResponsesStreamUsage(info, usage, responseTextBuilder.String(), functionCallCount)
		failResponsesCompatChatStream(c, types.ErrorCodeBadResponse, "")
		return usage, types.NewError(conversionErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if chatCompat && !terminalSeen {
		if !service.ValidUsage(usage) {
			service.PromptTokensForUsage(c, info)
		}
		observedResponsesStreamUsage(info, usage, responseTextBuilder.String(), functionCallCount)
		terminalErr := fmt.Errorf("responses stream ended without a terminal event")
		failResponsesCompatChatStream(c, types.ErrorCodeUpstreamStreamFailed, "")
		return usage, types.NewError(terminalErr, types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
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
	if chatCompat {
		if terminalType == "error" {
			terminalFailure = sendResponsesCompatChatStreamError(c, terminalStreamError)
		} else if terminalType == "response.failed" {
			terminalFailure = sendResponsesCompatChatFailure(c, terminalResponse)
		} else {
			sendResponsesCompatChatCompletion(c, info, chatCompatState, terminalResponse, usage)
		}
	}
	statusCode := http.StatusOK
	if terminalFailure != nil {
		statusCode = http.StatusBadGateway
	}
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
	if terminalFailure != nil {
		helper.Done(c)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, true)
		// The error is emitted after the response has started, so the controller
		// cannot safely run its normal pre-consumption refund path. Ask the relay
		// layer to settle the captured usage (including an all-zero full refund)
		// before it returns the terminal error.
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		return usage, types.NewErrorWithStatusCode(terminalFailure, types.ErrorCodeUpstreamError, http.StatusBadGateway)
	}
	if chatCompat {
		helper.Done(c)
	}

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

// responsesFirstEventLazuError converts an in-band stream failure that reached
// no client byte into a normal relay error. The upstream's own message is kept
// (it is what the client will display) but the status is re-derived locally: the
// transport said 200, which would otherwise classify as success and suppress
// both the retry and the error row.
func responsesFirstEventLazuError(c *gin.Context, upstreamError *types.OpenAIError) *types.LazuError {
	message := "upstream responses stream failed before sending any output"
	if upstreamError != nil && strings.TrimSpace(upstreamError.Message) != "" {
		message = strings.TrimSpace(upstreamError.Message)
	}
	// Default to a generic upstream failure, NOT to "overloaded". Only a text
	// that actually names a transient condition earns a retry: a provider that
	// reports a blocked prompt or an oversized context in-band would otherwise
	// be re-sent three times to fail identically, and be described to the
	// customer as a busy model they should wait out or pay more to avoid.
	code := types.ErrorCodeUpstreamError
	if upstreamError != nil {
		if mapped, ok := responsesStreamErrorCode(upstreamError); ok {
			code = mapped
		}
	}
	logger.LogError(c, fmt.Sprintf("responses stream failed on its first event: %s", message))
	return types.NewErrorWithStatusCode(
		fmt.Errorf("%s", message),
		code,
		types.StatusForCode(code, http.StatusBadGateway),
	)
}

// responsesStreamErrorCode classifies an in-band stream error by its text, the
// only signal available when the transport status is 200.
//
// The wrong-fit classes come first and on purpose: they map onto codes the
// retry policy refuses to repeat, because the same prompt on the same channel
// produces the same rejection. Everything that is not recognised stays a
// generic upstream failure for the caller to decide about.
func responsesStreamErrorCode(upstreamError *types.OpenAIError) (types.ErrorCode, bool) {
	text := strings.ToLower(fmt.Sprint(upstreamError.Code) + " " + upstreamError.Type + " " + upstreamError.Message)
	switch {
	case strings.Contains(text, "insufficient_quota") || strings.Contains(text, "billing_error") ||
		strings.Contains(text, "exceeded your current quota") || strings.Contains(text, "credit balance") ||
		strings.Contains(text, "insufficient balance") || strings.Contains(text, "insufficient_balance"):
		return types.ErrorCodeUpstreamBillingFailed, true
	case strings.Contains(text, "invalid_api_key") || strings.Contains(text, "authentication_error") ||
		strings.Contains(text, "invalid api key") || strings.Contains(text, "unauthorized"):
		return types.ErrorCodeChannelInvalidKey, true
	case strings.Contains(text, "content filter") || strings.Contains(text, "content_filter") ||
		strings.Contains(text, "content policy") || strings.Contains(text, "safety") ||
		strings.Contains(text, "blocked"):
		return types.ErrorCodePromptBlocked, true
	// Same classification the HTTP-status error map uses for these texts
	// (internal/relay/errmap), so an in-band failure and a 400 carrying the
	// same words are treated alike.
	case strings.Contains(text, "context_length_exceeded") || strings.Contains(text, "context length") || strings.Contains(text, "context window") ||
		strings.Contains(text, "maximum context") || strings.Contains(text, "prompt is too long"):
		return types.ErrorCodeContextLengthExceeded, true
	case strings.Contains(text, "invalid_request") || strings.Contains(text, "invalid request"):
		return types.ErrorCodeBadRequestBody, true
	case strings.Contains(text, "rate limit") || strings.Contains(text, "rate_limit") || strings.Contains(text, "too many requests"):
		return types.ErrorCodeUpstreamRateLimited, true
	case strings.Contains(text, "overload") || strings.Contains(text, "capacity") || strings.Contains(text, "busy") || strings.Contains(text, "过载"):
		return types.ErrorCodeUpstreamOverloaded, true
	}
	return "", false
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

// streamFailureFrame is the chat-completions error frame for a stream the
// gateway ends mid-flight. Its code, type and LZ number come from the same
// catalog row; operator failures never expose the provider's diagnostics.
func streamFailureFrame(code types.ErrorCode, upstreamError *types.OpenAIError) (types.OpenAIError, string) {
	if upstreamError != nil {
		if mapped, ok := responsesStreamErrorCode(upstreamError); ok {
			code = mapped
		}
	}
	w := types.RenderWire(code, nil, 0)
	frame := types.OpenAIError{
		Message: w.Message,
		Type:    w.Type,
		Code:    string(w.Code),
		Lazu:    w.ID,
		Docs:    w.Docs,
	}
	if upstreamError != nil && w.Audience == types.AudienceCustomer {
		if value := strings.TrimSpace(upstreamError.Message); value != "" {
			frame.Message = common.MaskSensitiveInfo(value)
		}
	}
	return frame, frame.Message
}

func sendResponsesCompatChatStreamError(c *gin.Context, upstreamError *types.OpenAIError) error {
	frame, message := streamFailureFrame(types.ErrorCodeUpstreamStreamFailed, upstreamError)
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
	frame, _ := streamFailureFrame(code, upstream)
	frame.RequestID = c.GetString(common.RequestIdKey)
	_ = helper.ObjectData(c, gin.H{"error": frame})
	helper.Done(c)
	common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, true)
	common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
}

func sendResponsesCompatChatFailure(c *gin.Context, response *dto.OpenAIResponsesResponse) error {
	var upstream *types.OpenAIError
	if response != nil {
		upstream = response.GetOpenAIError()
	}
	frame, message := streamFailureFrame(types.ErrorCodeUpstreamStreamFailed, upstream)
	frame.RequestID = c.GetString(common.RequestIdKey)
	if err := helper.ObjectData(c, gin.H{"error": frame}); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
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

type responsesChatCompatStreamState struct {
	responseID    string
	createdAt     int64
	model         string
	sentRole      bool
	sentToolCall  bool
	toolCallIndex int
	pendingTools  []dto.ResponsesOutput
}

func (s *responsesChatCompatStreamState) syncFromResponsesMeta(c *gin.Context, info *relaycommon.RelayInfo, response *dto.OpenAIResponsesResponse) {
	if s == nil {
		return
	}
	if response != nil {
		if s.responseID == "" && response.ID != "" {
			s.responseID = openai_responses.NormalizeChatCompletionID(response.ID)
		}
		if s.createdAt == 0 && response.CreatedAt > 0 {
			s.createdAt = int64(response.CreatedAt)
		}
		if s.model == "" && response.Model != "" {
			s.model = response.Model
		}
	}
	if s.responseID == "" {
		s.responseID = openai_responses.NormalizeChatCompletionID(helper.GetResponseID(c))
	}
	if s.createdAt == 0 {
		s.createdAt = time.Now().Unix()
	}
	if s.model == "" {
		if info != nil && info.UpstreamModelName != "" {
			s.model = info.UpstreamModelName
		} else if info != nil {
			s.model = info.OriginModelName
		}
	}
}

func sendResponsesCompatChatTextDelta(c *gin.Context, info *relaycommon.RelayInfo, state *responsesChatCompatStreamState, delta string) {
	if state == nil || delta == "" {
		return
	}
	state.syncFromResponsesMeta(c, info, nil)
	if !state.sentRole {
		start := helper.GenerateStartEmptyResponse(state.responseID, state.createdAt, state.model, nil)
		_ = helper.ObjectData(c, start)
		state.sentRole = true
	}

	chunk := dto.ChatCompletionsStreamResponse{
		Id:      state.responseID,
		Object:  "chat.completion.chunk",
		Created: state.createdAt,
		Model:   state.model,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Content: common.GetPointer(delta),
				},
				Index: 0,
			},
		},
	}
	_ = helper.ObjectData(c, chunk)
}

func sendResponsesCompatChatRefusalDelta(c *gin.Context, info *relaycommon.RelayInfo, state *responsesChatCompatStreamState, delta string) {
	if state == nil || delta == "" {
		return
	}
	state.syncFromResponsesMeta(c, info, nil)
	if !state.sentRole {
		start := helper.GenerateStartEmptyResponse(state.responseID, state.createdAt, state.model, nil)
		_ = helper.ObjectData(c, start)
		state.sentRole = true
	}
	chunk := dto.ChatCompletionsStreamResponse{
		Id: state.responseID, Object: "chat.completion.chunk", Created: state.createdAt, Model: state.model,
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Refusal: common.GetPointer(delta)}, Index: 0,
		}},
	}
	_ = helper.ObjectData(c, chunk)
}

func (s *responsesChatCompatStreamState) queueToolCall(item *dto.ResponsesOutput) {
	if s == nil || item == nil {
		return
	}
	s.pendingTools = append(s.pendingTools, *item)
}

func flushResponsesCompatChatToolCalls(c *gin.Context, info *relaycommon.RelayInfo, state *responsesChatCompatStreamState, completedOnly bool) {
	if state == nil || len(state.pendingTools) == 0 {
		return
	}
	for i := range state.pendingTools {
		if completedOnly && !strings.EqualFold(state.pendingTools[i].Status, "completed") {
			continue
		}
		sendResponsesCompatChatToolCall(c, info, state, &state.pendingTools[i])
	}
	state.pendingTools = nil
}

func sendResponsesCompatChatToolCall(c *gin.Context, info *relaycommon.RelayInfo, state *responsesChatCompatStreamState, item *dto.ResponsesOutput) {
	if state == nil || item == nil {
		return
	}
	state.syncFromResponsesMeta(c, info, nil)
	if !state.sentRole {
		start := helper.GenerateStartEmptyResponse(state.responseID, state.createdAt, state.model, nil)
		_ = helper.ObjectData(c, start)
		state.sentRole = true
	}

	callID := item.CallID
	if callID == "" {
		callID = item.ID
	}
	toolCall := dto.ToolCallResponse{
		ID:   callID,
		Type: "function",
		Function: dto.FunctionResponse{
			Name:      item.Name,
			Arguments: item.Arguments,
		},
	}
	toolCall.SetIndex(state.toolCallIndex)
	state.toolCallIndex++
	state.sentToolCall = true

	chunk := dto.ChatCompletionsStreamResponse{
		Id:      state.responseID,
		Object:  "chat.completion.chunk",
		Created: state.createdAt,
		Model:   state.model,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ToolCalls: []dto.ToolCallResponse{toolCall},
				},
				Index: 0,
			},
		},
	}
	_ = helper.ObjectData(c, chunk)
}

func sendResponsesCompatChatCompletion(c *gin.Context, info *relaycommon.RelayInfo, state *responsesChatCompatStreamState, response *dto.OpenAIResponsesResponse, usage *dto.Usage) {
	if state == nil {
		return
	}
	state.syncFromResponsesMeta(c, info, response)
	if !state.sentRole {
		start := helper.GenerateStartEmptyResponse(state.responseID, state.createdAt, state.model, nil)
		_ = helper.ObjectData(c, start)
		state.sentRole = true
	}

	incomplete := response != nil && strings.EqualFold(response.Status, "incomplete")
	completedOnly := incomplete
	if incomplete && response.IncompleteDetails != nil && strings.EqualFold(response.IncompleteDetails.Reason, "max_output_tokens") {
		// Chat tool-call deltas may legally contain an unfinished argument string.
		// Preserve the billable partial call for length truncation so the caller
		// can inspect or resume it; other incomplete reasons remain fail-closed.
		completedOnly = false
	}
	flushResponsesCompatChatToolCalls(c, info, state, completedOnly)

	finishReason := "stop"
	if incomplete {
		finishReason = "length"
		if response.IncompleteDetails != nil && strings.EqualFold(response.IncompleteDetails.Reason, "content_filter") {
			finishReason = "content_filter"
		}
	} else if state.sentToolCall {
		finishReason = "tool_calls"
	}
	stopResp := helper.GenerateStopResponse(state.responseID, state.createdAt, state.model, finishReason)
	_ = helper.ObjectData(c, stopResp)

	if info != nil && info.ShouldIncludeUsage {
		safeUsage := usage
		if safeUsage == nil {
			promptTokens := service.PromptTokensForUsage(c, info)
			safeUsage = &dto.Usage{
				PromptTokens:     promptTokens,
				CompletionTokens: 0,
				TotalTokens:      promptTokens,
			}
			service.ApplyLocalPromptUsage(c, info, safeUsage)
		}
		chatUsage := dto.ProjectChatUsage(safeUsage)
		finalUsage := helper.GenerateFinalUsageResponse(state.responseID, state.createdAt, state.model, *chatUsage)
		_ = helper.ObjectData(c, finalUsage)
	}
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
	return responsesFirstEventLazuError(c, upstreamError)
}
