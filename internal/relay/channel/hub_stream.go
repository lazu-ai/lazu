package channel

import (
	"context"
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
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

// The protocol hub's stream runner: one upstream stream, decoded once into
// content events and written in the client's protocol. A hub supplies the
// upstream's decoder and the side effects billing reads off its events; the
// runner owns everything the hubs must agree on:
//
//   - Nothing is written before the first content. The start, turn and usage
//     events are held, so a failure up to then reaches no client byte.
//   - A failure before any output is retryable, classified by what the
//     upstream said (FirstEventFailure). After output it is written in the
//     client's protocol (StreamFailureFrame), settled, and not replayed.
//   - A stream that ends before the upstream said how the reply ended is cut
//     off, by the same two rules.
//   - Reading stops at the terminal event; the provider's EOF may come later.
//     A Chat upstream's stop is not its last event (the usage chunk follows
//     it), so its stream is read to [DONE].
//   - Usage is settled from the collected reply (SettleHubUsage) and the
//     exchange is logged.

const hubStreamLogBytes = 256 << 10

// HubClient writes a reply to the client in the client's protocol.
type HubClient interface {
	// Emit writes the events; it says whether anything reached the client.
	Emit(c *gin.Context, events []textgen.Event) (bool, error)
	// Finish ends a reply whose upstream said how it ended.
	Finish(c *gin.Context, usage *dto.Usage) error
	// Fail ends a reply already under way with the failure given.
	Fail(c *gin.Context, frame types.OpenAIError, usage *dto.Usage)
}

// HubStream is one hub's part: the upstream's decoder, and the side effects
// of its events beyond the upstream model and finish reason the runner
// records itself.
type HubStream struct {
	Decode  func(data string) ([]textgen.Event, error)
	Observe func(c *gin.Context, info *relaycommon.RelayInfo, data string, events []textgen.Event)
	// ReadPastStop keeps reading after the stop event, for an upstream
	// whose usage arrives after it (Chat's include_usage chunk).
	ReadPastStop bool
	// Unreleased is what the decoder still holds when the stream ends (a
	// Responses call whose fate the terminal event would have decided):
	// never written, but billed when the stream was cut off.
	Unreleased func() []textgen.Event
	// ClientUsage is the usage as the client reads it, where that is not
	// the settled one (Messages' cache-exclusive input, for a Chat or
	// Responses client).
	ClientUsage func(*dto.Usage) *dto.Usage
}

// RunHubStream serves a streamed upstream reply to the client through the
// writer for its protocol.
func RunHubStream(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, hub HubStream, client HubClient) (*dto.Usage, *types.LazuError) {
	defer service.CloseResponseBodyGracefully(resp)
	log := NewStreamLog(hubStreamLogBytes)
	var lastData string
	defer func() {
		// the normal path logs the exchange; an early return keeps the last
		// event for the controller's terminal hook
		service.CaptureIncompleteModelResponse(c, []byte(lastData), resp.StatusCode)
	}()
	var col textgen.Collector
	var held []textgen.Event
	forwarded, terminal := false, false
	var decodeErr, writeErr error
	var failure *textgen.SemanticError
	scan := helper.StreamScannerHandlerWithTerminal(c, resp, info, func(data string) bool {
		timer := common.StartHotPathStage(common.HotPathStageProtocolStreamEvent)
		events, err := hub.Decode(data)
		if err != nil {
			timer.Finish(err)
			decodeErr = err
			return false
		}
		log.Add(data)
		lastData = data
		observeHubEvents(info, events)
		if hub.Observe != nil {
			hub.Observe(c, info, data, events)
		}
		col.Add(events...)
		for _, event := range events {
			switch event.Kind {
			case textgen.EventError:
				failure = event.Error
				if failure == nil {
					failure = &textgen.SemanticError{Type: "api_error", Message: "upstream stream failed"}
				}
				terminal = true
			case textgen.EventStop:
				terminal = terminal || !hub.ReadPastStop
			}
		}
		timer.Finish(nil)
		if failure != nil {
			return false
		}
		if !forwarded && !contentEvents(events) {
			held = append(held, events...)
			return !terminal
		}
		wrote, err := client.Emit(c, append(held, events...))
		held = nil
		forwarded = forwarded || wrote
		if err != nil {
			writeErr = err
			return false
		}
		return !terminal
	}, func() bool { return terminal })

	if hub.Unreleased != nil {
		col.Add(hub.Unreleased()...)
	}
	usage := SettleHubUsage(c, info, col.Result())
	shown := usage
	if hub.ClientUsage != nil {
		shown = hub.ClientUsage(usage)
	}
	if canceled := clientCanceled(c); canceled != nil {
		if service.ValidUsage(usage) {
			common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		}
		return usage, canceled
	}
	// a failure after output: told in the client's protocol, settled, not
	// replayed
	failAfterOutput := func(code types.ErrorCode, upstream *types.OpenAIError, status int, err error, returned ...types.ErrorCode) (*dto.Usage, *types.LazuError) {
		frame, message := StreamFailureFrame(code, upstream)
		frame.RequestID = c.GetString(common.RequestIdKey)
		client.Fail(c, frame, shown)
		recordHubExchange(c, log, lastData, usage, status)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, true)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		if err == nil {
			err = fmt.Errorf("%s", message)
		}
		// the code the failure was classified by (a scanner timeout stays
		// upstream_timeout), else the generic upstream error
		errorCode := types.ErrorCodeUpstreamError
		if len(returned) > 0 && returned[0] != "" {
			errorCode = returned[0]
		}
		return usage, types.NewErrorWithStatusCode(err, errorCode, status, types.ErrOptionWithSkipRetry())
	}
	switch {
	case writeErr != nil:
		// the client went away: what the upstream produced up to then is
		// still owed
		if service.ValidUsage(usage) {
			common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		}
		return usage, types.NewError(writeErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	case failure != nil && !forwarded:
		// nothing reached the client: a pre-stream failure, retryable when
		// what the upstream said is transient. The gateway absorbs the
		// attempt, so it is refunded even when the failure carried usage,
		// and its exchange is not logged: a retry's is the request's.
		info.StreamFailedWithoutUsage = true
		common.SetContextKey(c, constant.ContextKeyStreamFirstEventFailed, true)
		return usage, FirstEventFailure(c, openAIErrorOf(failure))
	case failure != nil:
		return failAfterOutput(types.ErrorCodeUpstreamStreamFailed, openAIErrorOf(failure), http.StatusBadGateway, nil)
	case decodeErr != nil:
		if !forwarded {
			// the upstream's stream is corrupt, not the request: a
			// retryable pre-stream failure, refunded
			info.StreamFailedWithoutUsage = true
			common.SetContextKey(c, constant.ContextKeyStreamFirstEventFailed, true)
			return usage, types.NewError(fmt.Errorf("decode upstream stream event: %w", decodeErr), types.ErrorCodeBadResponseBody)
		}
		frame, _ := StreamFailureFrame(types.ErrorCodeBadResponse, nil)
		frame.RequestID = c.GetString(common.RequestIdKey)
		client.Fail(c, frame, shown)
		recordHubExchange(c, log, lastData, usage, http.StatusBadGateway)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, true)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		return usage, types.NewError(fmt.Errorf("decode upstream stream event: %w", decodeErr), types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
	}
	if relayErr := scan.RelayError(forwarded); relayErr != nil {
		if !forwarded {
			return usage, relayErr
		}
		return failAfterOutput(types.ErrorCodeUpstreamStreamFailed, nil, relayErr.StatusCode, relayErr.Err, relayErr.GetErrorCode())
	}
	if !col.Result().StopSeen {
		// cut off before the upstream said how the reply ended
		if !forwarded {
			// metadata may have arrived, but no output: a retryable
			// pre-stream failure, refunded
			info.StreamFailedWithoutUsage = true
			common.SetContextKey(c, constant.ContextKeyStreamFirstEventFailed, true)
			return usage, types.NewError(io.ErrUnexpectedEOF, types.ErrorCodeBadResponse)
		}
		return failAfterOutput(types.ErrorCodeUpstreamStreamFailed, nil, http.StatusBadGateway, fmt.Errorf("upstream stream ended before the reply finished"))
	}
	if len(held) > 0 {
		// an empty reply: nothing but its start, turn and usage
		if _, err := client.Emit(c, held); err != nil {
			return usage, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
	}
	if err := client.Finish(c, shown); err != nil {
		// the upstream finished and its usage is known: settled even
		// though the client went away before the last frame
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		return usage, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	recordHubExchange(c, log, lastData, usage, http.StatusOK)
	return usage, nil
}

// observeHubEvents records what the outcome row reads off every upstream:
// the model that answered and how the reply ended.
func observeHubEvents(info *relaycommon.RelayInfo, events []textgen.Event) {
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

// contentEvents says whether the events put anything in front of the
// client: a start, a turn or a usage report alone does not, nor does the
// boundary of a reasoning item before its first delta, nor a call without
// its name and id yet (the Messages and Responses writers hold it until
// both are known).
func contentEvents(events []textgen.Event) bool {
	for _, event := range events {
		switch event.Kind {
		case textgen.EventStart, textgen.EventUsage, textgen.EventTurn, textgen.EventReasoningStart:
		case textgen.EventToolStart:
			if event.ToolName != "" && event.ToolCallID != "" {
				return true
			}
		default:
			if event.Withheld {
				continue
			}
			return true
		}
	}
	return false
}

func openAIErrorOf(semantic *textgen.SemanticError) *types.OpenAIError {
	if semantic == nil {
		return nil
	}
	return &types.OpenAIError{Type: semantic.Type, Code: semantic.Code, Message: semantic.Message}
}

// clientCanceled is the client going away mid-stream, and only that: a
// deadline is the scanner's to classify (StreamScanResult.RelayError), as a
// timeout, logged.
func clientCanceled(c *gin.Context) *types.LazuError {
	if c == nil || c.Request == nil || c.Request.Context().Err() != context.Canceled {
		return nil
	}
	return types.NewError(context.Canceled, types.ErrorCodeClientCanceled, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog(), types.ErrOptionWithHideErrMsg("request canceled by client"))
}

func recordHubExchange(c *gin.Context, log *StreamLog, lastData string, usage *dto.Usage, status int) {
	payload := &service.ModelLogPayload{
		ResponseChunks: log.Values(),
		Metadata:       log.Metadata(),
		Usage:          usage,
		IsStream:       true,
		StatusCode:     status,
	}
	if lastData != "" {
		payload.ResponseBody = []byte(lastData)
	}
	service.RecordModelExchange(c, payload)
}

// HubClientFor is the writer for the attempt's client protocol.
func HubClientFor(c *gin.Context, info *relaycommon.RelayInfo) HubClient {
	switch info.ProtocolPlan.ClientProtocol.Family {
	case textgen.ProtocolOpenAIResponses:
		return &responsesHubClient{emitter: openai_responses.NewStreamEmitter(helper.GetResponseID(c), info.UpstreamModelName, info.ProtocolRequest, openai_responses.RetainedOutputLimit(info.ProtocolRequest))}
	case textgen.ProtocolAnthropic:
		return &anthropicHubClient{emitter: anthropic_messages.NewStreamEmitter(helper.GetResponseID(c), info.UpstreamModelName)}
	}
	return &chatHubClient{emitter: openai_chat.NewStreamEmitter(helper.GetResponseID(c), info.UpstreamModelName), includeUsage: info.ShouldIncludeUsage}
}

// chatHubClient writes Chat Completions chunks, [DONE] at the end.
type chatHubClient struct {
	emitter      *openai_chat.StreamEmitter
	includeUsage bool
}

func (w *chatHubClient) Emit(c *gin.Context, events []textgen.Event) (bool, error) {
	chunks := w.emitter.Emit(events...)
	for _, chunk := range chunks {
		if err := helper.ObjectData(c, chunk); err != nil {
			return true, err
		}
	}
	return len(chunks) > 0, nil
}

func (w *chatHubClient) Finish(c *gin.Context, usage *dto.Usage) error {
	chunks, err := w.emitter.Finish(usage)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if !w.includeUsage {
			if openai_chat.IsUsageChunk(chunk) {
				continue
			}
			chunk.Usage = nil
		}
		if err := helper.ObjectData(c, chunk); err != nil {
			return err
		}
	}
	helper.Done(c)
	return nil
}

func (w *chatHubClient) Fail(c *gin.Context, frame types.OpenAIError, _ *dto.Usage) {
	_ = helper.ObjectData(c, gin.H{"error": frame})
	helper.Done(c)
}

// responsesHubClient writes Responses events.
type responsesHubClient struct {
	emitter *openai_responses.StreamEmitter
}

func (w *responsesHubClient) write(c *gin.Context, payloads []map[string]any) error {
	for _, payload := range payloads {
		eventType, _ := payload["type"].(string)
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: eventType}, string(data))
	}
	return nil
}

func (w *responsesHubClient) Emit(c *gin.Context, events []textgen.Event) (bool, error) {
	payloads, err := w.emitter.Emit(events...)
	if writeErr := w.write(c, payloads); writeErr != nil {
		return len(payloads) > 0, writeErr
	}
	return len(payloads) > 0, err
}

func (w *responsesHubClient) Finish(c *gin.Context, usage *dto.Usage) error {
	payloads, err := w.emitter.Finish(usage)
	if err != nil {
		return err
	}
	return w.write(c, payloads)
}

func (w *responsesHubClient) Fail(c *gin.Context, frame types.OpenAIError, usage *dto.Usage) {
	_ = w.write(c, w.emitter.FailWithSemanticError(&textgen.SemanticError{Type: frame.Type, Code: fmt.Sprint(frame.Code), Message: frame.Message}, usage))
}

// anthropicHubClient writes Messages SSE frames.
type anthropicHubClient struct {
	emitter *anthropic_messages.StreamEmitter
}

func (w *anthropicHubClient) write(c *gin.Context, frames []anthropic_messages.Frame) error {
	for _, frame := range frames {
		data, err := json.Marshal(frame.Data)
		if err != nil {
			return err
		}
		c.Render(-1, helper.CustomEvent{Data: "event: " + frame.Event + "\n"})
		c.Render(-1, helper.CustomEvent{Data: "data: " + string(data)})
		_ = helper.FlushWriter(c)
	}
	return nil
}

func (w *anthropicHubClient) Emit(c *gin.Context, events []textgen.Event) (bool, error) {
	frames := w.emitter.Emit(events...)
	return len(frames) > 0, w.write(c, frames)
}

func (w *anthropicHubClient) Finish(c *gin.Context, usage *dto.Usage) error {
	frames, err := w.emitter.Finish(usage)
	if err != nil {
		return err
	}
	return w.write(c, frames)
}

func (w *anthropicHubClient) Fail(c *gin.Context, frame types.OpenAIError, _ *dto.Usage) {
	_ = w.write(c, w.emitter.FailWithSemanticError(&textgen.SemanticError{Type: types.ClaudeErrorType(frame.Type, http.StatusBadGateway), Message: frame.Message}))
}
