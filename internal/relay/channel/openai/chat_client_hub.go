package openai

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

// The hub for a Chat client served from a Responses upstream: the stream is
// decoded once by the Responses decoder and written by the Chat emitter,
// with the rules of the other hubs (internal/relay/channel/claude/hub.go,
// claude_client_hub.go): nothing is written before the first content, a
// failure before it is retryable, after it the client is told and the
// attempt is not replayed. The side effects billing reads off a Responses
// stream are kept: built-in tool calls, image generation, the finish reason
// and the exchange log.

// chatClientResponsesStream serves a streamed Responses reply to a Chat
// client (bridgesResponsesToChat).
func chatClientResponsesStream(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)
	decoder := &openai_responses.StreamDecoder{}
	emitter := openai_chat.NewStreamEmitter(helper.GetResponseID(c), info.UpstreamModelName)
	streamLog := newBoundedStreamChunks(maxBridgedStreamLogBytes)
	var lastData string
	defer func() {
		// the normal path archives the exchange; an early return keeps the
		// last event for the controller's terminal hook
		service.CaptureIncompleteModelResponse(c, []byte(lastData), resp.StatusCode)
	}()
	var col textgen.Collector
	var held []textgen.Event
	forwarded := false
	logicalTerminal := false
	var conversionErr error
	var failure *types.OpenAIError
	failed := false
	helper.StreamScannerHandlerWithTerminal(c, resp, info, func(data string) bool {
		timer := common.StartHotPathStage(common.HotPathStageProtocolStreamEvent)
		events, err := decoder.Decode(data)
		if err != nil {
			timer.Finish(err)
			conversionErr = fmt.Errorf("decode responses stream event: %w", err)
			return false
		}
		streamLog.Add(data)
		lastData = data
		observeResponsesStreamEvent(c, info, data)
		col.Add(events...)
		for _, event := range events {
			switch event.Kind {
			case textgen.EventError:
				failed = true
				var stream dto.ResponsesStreamResponse
				if common.UnmarshalJsonStr(data, &stream) == nil {
					failure = responsesStreamFailureError(&stream)
				}
			case textgen.EventStop:
				logicalTerminal = true
			}
		}
		timer.Finish(nil)
		if failed {
			logicalTerminal = true
			return false
		}
		if !forwarded && !contentEvents(events) {
			held = append(held, events...)
			// a terminal event ends the reading; the provider's EOF may
			// come much later
			return !logicalTerminal
		}
		chunks := emitter.Emit(append(held, events...)...)
		held = nil
		for _, chunk := range chunks {
			if err := helper.ObjectData(c, chunk); err != nil {
				conversionErr = err
				return false
			}
		}
		if len(chunks) > 0 {
			forwarded = true
		}
		return !logicalTerminal
	}, func() bool { return logicalTerminal })

	usage := channel.SettleHubUsage(c, info, col.Result())
	if canceledErr := streamClientCanceledError(c); canceledErr != nil {
		markCanceledStreamForSettlement(c, service.ValidUsage(usage))
		return usage, canceledErr
	}
	if failed && !forwarded {
		// nothing reached the client: as a pre-stream failure, retryable,
		// recorded as an error, classified by what the upstream said. The
		// gateway absorbs the attempt, so its reservation is refunded even
		// when the failure carried usage, as on the native Responses path.
		info.StreamFailedWithoutUsage = true
		common.SetContextKey(c, constant.ContextKeyStreamFirstEventFailed, true)
		return usage, responsesFirstEventLazuError(c, failure)
	}
	if conversionErr != nil {
		failResponsesCompatChatStream(c, types.ErrorCodeBadResponse, "")
		return usage, types.NewError(conversionErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if failed {
		frameErr := sendResponsesCompatChatStreamError(c, failure)
		recordChatClientExchange(c, streamLog, lastData, usage, http.StatusBadGateway)
		helper.Done(c)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, true)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		return usage, types.NewErrorWithStatusCode(frameErr, types.ErrorCodeUpstreamError, http.StatusBadGateway)
	}
	if len(held) > 0 && col.Result().StopSeen {
		// an empty reply: its role chunk before the finish
		for _, chunk := range emitter.Emit(held...) {
			_ = helper.ObjectData(c, chunk)
		}
		forwarded = true
	}
	chunks, finishErr := emitter.Finish(usage)
	if finishErr != nil {
		// cut off before the upstream said how the reply ended
		if !forwarded {
			// metadata may have arrived, but no output did: a retryable
			// pre-stream failure, refunded
			info.StreamFailedWithoutUsage = true
			common.SetContextKey(c, constant.ContextKeyStreamFirstEventFailed, true)
			return usage, types.NewError(io.ErrUnexpectedEOF, types.ErrorCodeBadResponse)
		}
		failResponsesCompatChatStream(c, types.ErrorCodeUpstreamStreamFailed, "")
		return usage, types.NewError(fmt.Errorf("responses stream ended without a terminal event"), types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
	}
	for _, chunk := range chunks {
		if !info.ShouldIncludeUsage {
			if openai_chat.IsUsageChunk(chunk) {
				continue
			}
			chunk.Usage = nil
		}
		if err := helper.ObjectData(c, chunk); err != nil {
			// the upstream finished and its usage is known: settle it even
			// though the client went away before the last chunk
			common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
			return usage, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
	}
	recordChatClientExchange(c, streamLog, lastData, usage, http.StatusOK)
	helper.Done(c)
	return usage, nil
}

// observeResponsesStreamEvent performs the side effects of a Responses
// event that billing and the outcome row read: built-in tool calls, image
// generation, the finish reason.
func observeResponsesStreamEvent(c *gin.Context, info *relaycommon.RelayInfo, data string) {
	var stream dto.ResponsesStreamResponse
	if common.UnmarshalJsonStr(data, &stream) != nil {
		return
	}
	switch stream.Type {
	case dto.ResponsesOutputTypeItemDone:
		if stream.Item == nil {
			return
		}
		switch stream.Item.Type {
		case dto.BuildInCallWebSearchCall:
			if isBillableResponsesWebSearchCall(stream.Item) {
				incrementResponsesBuiltInToolCall(info, dto.BuildInToolWebSearch, dto.BuildInToolWebSearchPreview)
			}
		case dto.BuildInCallFileSearchCall:
			incrementResponsesBuiltInToolCall(info, dto.BuildInToolFileSearch)
		}
	case "response.completed", "response.incomplete", "response.failed":
		if stream.Response == nil {
			return
		}
		recordResponsesFinishReason(info, stream.Response)
		if stream.Response.HasImageGenerationCall() {
			c.Set("image_generation_call", true)
			c.Set("image_generation_call_quality", stream.Response.GetQuality())
			c.Set("image_generation_call_size", stream.Response.GetSize())
		}
	}
}

func recordChatClientExchange(c *gin.Context, streamLog *boundedStreamChunks, lastData string, usage *dto.Usage, status int) {
	payload := &service.ModelLogPayload{
		ResponseChunks: streamLog.Values(),
		Metadata:       streamLog.Metadata(),
		Usage:          usage,
		IsStream:       true,
		StatusCode:     status,
	}
	if lastData != "" {
		payload.ResponseBody = []byte(lastData)
	}
	service.RecordModelExchange(c, payload)
}
