package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	anthropic_messages "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/anthropic_messages"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

// The hub for a Claude client served from an OpenAI-shaped upstream: the
// upstream's reply (Chat Completions or Responses) is decoded once into
// content events and written by the Anthropic emitter; a whole reply goes
// through the collector and anthropic_messages.Render. The mirror of the
// Messages hub in internal/relay/channel/claude/hub.go, with the same rules
// for output already forwarded, retries and settlement.

// ServesClaudeClient says whether the attempt serves a Claude client from
// Chat Completions or Responses.
func ServesClaudeClient(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ProtocolPlan == nil || info.ProtocolPlan.Native ||
		info.ProtocolPlan.ClientProtocol.Family != textgen.ProtocolAnthropic {
		return false
	}
	switch info.ProtocolPlan.UpstreamProtocol.Family {
	case textgen.ProtocolOpenAIChat, textgen.ProtocolOpenAIResponses:
		return true
	}
	return false
}

// upstreamDecoder decodes one SSE data payload of the upstream's stream.
type upstreamDecoder func(data string) ([]textgen.Event, error)

func decoderFor(info *relaycommon.RelayInfo) upstreamDecoder {
	if info.ProtocolPlan.UpstreamProtocol.Family == textgen.ProtocolOpenAIResponses {
		decoder := &openai_responses.StreamDecoder{}
		return decoder.Decode
	}
	decoder := &openai_chat.StreamDecoder{}
	return func(data string) ([]textgen.Event, error) {
		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			return nil, err
		}
		if upstream := chunk.GetOpenAIError(); upstream != nil {
			// a Chat stream reports failure in-band under a 200
			return []textgen.Event{{Kind: textgen.EventError, Error: semanticErrorOf(upstream)}}, nil
		}
		return markProviderUsage(decoder.Decode(&chunk)), nil
	}
}

// markProviderUsage marks the usage a Chat upstream reported as the
// provider's, as the Chat handler does: settlement trusts a provider count
// over a local estimate.
func markProviderUsage(events []textgen.Event) []textgen.Event {
	for index, event := range events {
		if event.Kind == textgen.EventUsage && event.Usage != nil && service.ValidUsage(event.Usage) {
			usage := *event.Usage
			usage.MarkProviderUsage()
			events[index].Usage = &usage
		}
	}
	return events
}

func semanticErrorOf(upstream *types.OpenAIError) *textgen.SemanticError {
	semantic := &textgen.SemanticError{Type: "api_error", Code: "upstream_stream_failed", Message: "upstream stream failed"}
	if upstream == nil {
		return semantic
	}
	if upstream.Type != "" && upstream.Type != "upstream_error" {
		semantic.Type = upstream.Type
	}
	if upstream.Message != "" {
		semantic.Message = upstream.Message
	}
	return semantic
}

// writeFrames writes Messages SSE frames to the Claude client.
func writeFrames(c *gin.Context, frames []anthropic_messages.Frame) error {
	for _, frame := range frames {
		data, err := json.Marshal(frame.Data)
		if err != nil {
			return err
		}
		c.Render(-1, common.CustomEvent{Data: "event: " + frame.Event + "\n"})
		c.Render(-1, common.CustomEvent{Data: "data: " + string(data)})
		_ = helper.FlushWriter(c)
	}
	return nil
}

// observeClaudeClient performs the side effects of the events the outcome
// row and billing read.
func observeClaudeClient(info *relaycommon.RelayInfo, events []textgen.Event) {
	for _, event := range events {
		switch event.Kind {
		case textgen.EventStart:
			if event.Model != "" {
				info.UpstreamModelName = event.Model
			}
		case textgen.EventStop:
			info.FinishReason = openai_chat.FinishReasonOf(event.Stop)
		}
	}
}

// ClaudeClientStreamHandler serves a streamed Chat Completions or Responses
// reply to a Claude client.
func ClaudeClientStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.LazuError) {
	decode := decoderFor(info)
	emitter := anthropic_messages.NewStreamEmitter(helper.GetResponseID(c), info.UpstreamModelName)
	var col textgen.Collector
	var relayErr *types.LazuError
	var upstreamFailed *textgen.SemanticError
	// forwarded is whether this handler has written anything to the client,
	// judged from its own state (see the Messages hub). The emitter writes
	// message_start on the upstream's start, so the start, turn and usage
	// events are held until the first content: before it nothing is
	// written and a failure stays retryable.
	forwarded := false
	var held []textgen.Event
	scan := helper.StreamScannerHandler(c, resp, info, func(data string) bool {
		events, err := decode(data)
		if err != nil {
			relayErr = types.NewError(err, types.ErrorCodeBadResponseBody)
			return false
		}
		observeClaudeClient(info, events)
		col.Add(events...)
		for _, event := range events {
			if event.Kind == textgen.EventError {
				upstreamFailed = event.Error
			}
		}
		if upstreamFailed != nil && !forwarded {
			// an in-band failure before any output is retryable, and
			// nothing reaches the client
			common.SetContextKey(c, constant.ContextKeyStreamFirstEventFailed, true)
			return false
		}
		if !forwarded && !contentEvents(events) {
			held = append(held, events...)
			return true
		}
		frames := emitter.Emit(append(held, events...)...)
		held = nil
		if err := writeFrames(c, frames); err != nil {
			relayErr = types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			return false
		}
		if len(frames) > 0 {
			forwarded = true
		}
		return upstreamFailed == nil
	})
	if relayErr == nil {
		relayErr = scan.RelayError(forwarded)
	}
	usage := channel.SettleHubUsage(c, info, col.Result())
	if upstreamFailed != nil {
		if !forwarded {
			return usage, types.NewErrorWithStatusCode(fmt.Errorf("%s", upstreamFailed.Message), types.ErrorCodeUpstreamError, http.StatusBadGateway)
		}
		// the emitter already wrote the failure in Messages' error event
		common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, true)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		return usage, types.NewErrorWithStatusCode(fmt.Errorf("%s", upstreamFailed.Message), types.ErrorCodeUpstreamError, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	if relayErr != nil {
		if service.ValidUsage(usage) {
			common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		}
		return usage, relayErr
	}
	if len(held) > 0 && col.Result().StopSeen {
		// an empty reply: nothing but its start, turn and usage. Written
		// only when it ended well; a cut-off one stays retryable below.
		if err := writeFrames(c, emitter.Emit(held...)); err != nil {
			return usage, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		forwarded = true
	}
	frames, finishErr := emitter.Finish(usage)
	if finishErr != nil {
		// the stream ended cleanly before the upstream said how the reply
		// ended: cut off. Retryable before any output; after, the client
		// gets Messages' error event and the attempt is not replayed.
		if !forwarded {
			return usage, types.NewError(io.ErrUnexpectedEOF, types.ErrorCodeBadResponse)
		}
		_ = writeFrames(c, emitter.Fail(fmt.Errorf("upstream stream ended before the reply finished")))
		common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, true)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		return usage, types.NewError(io.ErrUnexpectedEOF, types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
	}
	if err := writeFrames(c, frames); err != nil {
		return usage, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return usage, nil
}

// contentEvents says whether the events put anything in front of the
// client: a start or a usage report alone does not.
func contentEvents(events []textgen.Event) bool {
	for _, event := range events {
		switch event.Kind {
		case textgen.EventStart, textgen.EventUsage, textgen.EventTurn:
		default:
			return true
		}
	}
	return false
}

// ClaudeClientHandler serves a whole Chat Completions or Responses reply to
// a Claude client.
func ClaudeClientHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.LazuError) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeReadResponseBodyFailed)
	}
	service.CloseResponseBodyGracefully(resp)
	var events []textgen.Event
	if info.ProtocolPlan.UpstreamProtocol.Family == textgen.ProtocolOpenAIResponses {
		var response dto.OpenAIResponsesResponse
		if err := common.Unmarshal(body, &response); err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		events = openai_responses.DecodeResponse(&response)
	} else {
		var response dto.OpenAITextResponse
		if err := common.Unmarshal(body, &response); err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		if upstream := response.GetOpenAIError(); upstream != nil && upstream.Message != "" {
			return nil, types.WithOpenAIError(*upstream, http.StatusBadGateway)
		}
		events = markProviderUsage(openai_chat.DecodeCompletion(&response))
	}
	observeClaudeClient(info, events)
	var col textgen.Collector
	col.Add(events...)
	result := col.Result()
	if result.Error != nil {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("%s", result.Error.Message), types.ErrorCodeUpstreamError, http.StatusBadGateway)
	}
	usage := channel.SettleHubUsage(c, info, result)
	result.Usage = usage
	data, err := json.Marshal(anthropic_messages.Render(result, info.UpstreamModelName))
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	service.IOCopyBytesGracefully(c, resp, data)
	return usage, nil
}
