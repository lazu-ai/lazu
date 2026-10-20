package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	anthropic_messages "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/anthropic_messages"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/tidwall/gjson"
)

// The hub: a Chat or Responses client served from an Anthropic channel. The
// Messages stream is decoded into content events once and emitted in the
// client's protocol; a whole reply goes through the collector and the
// client's render. Side effects the outcome row and billing rely on, the
// upstream model from message_start, the finish reason, web search usage,
// are performed here as the legacy handler performed them.

// BridgesToMessages says whether the attempt serves an OpenAI-shaped client
// from the Messages API.
func BridgesToMessages(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ProtocolPlan != nil && !info.ProtocolPlan.Native &&
		info.ProtocolPlan.UpstreamProtocol.Family == textgen.ProtocolAnthropic &&
		(info.ProtocolPlan.ClientProtocol.Family == textgen.ProtocolOpenAIChat || info.ProtocolPlan.ClientProtocol.Family == textgen.ProtocolOpenAIResponses) &&
		!usesCompletionAPI(info.UpstreamModelName)
}

// EncodeBridgedRequest is the Messages request for the attempt's IR, with
// the thinking fitted to the model's policy and the output limit defaulted.
func EncodeBridgedRequest(c *gin.Context, info *relaycommon.RelayInfo, semantic *textgen.Request) (*dto.ClaudeRequest, error) {
	// Images named by URL are fetched and sent as base64, as the legacy
	// converter did: Bedrock and Vertex endpoints take no url source, and
	// the gateway's fetch is the guarded one (common.SSRFProtection).
	inline := func(url string) (string, string, error) {
		file, err := service.GetFileBase64FromUrl(c, url, "formatting image for Claude")
		if err != nil {
			return "", "", err
		}
		return file.MimeType, file.Base64Data, nil
	}
	return encodeBridged(semantic, inline, CatalogPolicy(info.OriginModelName))
}

func encodeBridged(semantic *textgen.Request, inline func(string) (string, string, error), resolve PolicyResolver) (*dto.ClaudeRequest, error) {
	if semantic == nil {
		return nil, fmt.Errorf("missing immutable protocol request")
	}
	request, err := anthropic_messages.Encode(semantic, anthropic_messages.EncodeOptions{InlineImage: inline})
	if err != nil {
		return nil, err
	}
	if resolve == nil {
		resolve = StaticPolicy(ModelPolicy{})
	}
	fitBridgedThinking(request, resolve)
	PrepareNativeThinking(request, resolve)
	continueToolLoopWithoutThinking(request, resolve)
	return request, nil
}

// continueToolLoopWithoutThinking turns thinking off for a request that
// continues a tool loop whose assistant turn carries no thinking block.
// With thinking on, the Messages API requires the last assistant turn of a
// tool loop to start with the thinking block Anthropic signed, which a Chat
// or Responses client cannot hand back; the continuation goes without
// thinking, as the API allows, rather than failing.
func continueToolLoopWithoutThinking(request *dto.ClaudeRequest, resolve PolicyResolver) {
	if request == nil || request.Thinking == nil || request.Thinking.Type == "disabled" || len(request.Messages) < 2 {
		return
	}
	last := request.Messages[len(request.Messages)-1]
	lastBlocks, _ := last.Content.([]dto.ClaudeMediaMessage)
	answering := false
	for _, block := range lastBlocks {
		answering = answering || block.Type == "tool_result"
	}
	if last.Role != "user" || !answering {
		return
	}
	for index := len(request.Messages) - 2; index >= 0; index-- {
		message := request.Messages[index]
		if message.Role != "assistant" {
			continue
		}
		blocks, _ := message.Content.([]dto.ClaudeMediaMessage)
		if len(blocks) > 0 && (blocks[0].Type == "thinking" || blocks[0].Type == "redacted_thinking") {
			return
		}
		request.Thinking = &dto.Thinking{Type: "disabled"}
		request.OutputConfig = withoutOutputConfigEffort(request.OutputConfig)
		NormalizeThinking(request, resolve(request.Model))
		return
	}
}

// clientUsage is the usage as a Chat or Responses client reads it: input
// counts the cached tokens, which Anthropic's input_tokens leaves out. The
// cache-exclusive form stays the one settled.
func clientUsage(usage *dto.Usage) *dto.Usage {
	if usage == nil || !usage.PromptExcludesCache {
		return usage
	}
	copyUsage := *usage
	copyUsage.PromptTokens += usage.PromptTokensDetails.CachedTokens + usage.PromptTokensDetails.CachedCreationTokens
	copyUsage.PromptExcludesCache = false
	copyUsage.TotalTokens = copyUsage.PromptTokens + copyUsage.CompletionTokens
	return &copyUsage
}

// fitBridgedThinking fits the thinking the encoder wrote for a Chat or
// Responses client to the model, as the request's own suffix and the
// catalog say (PrepareNativeThinking does the rest). An effort on an
// adaptive model is asked as lazu asks it (ApplyAdaptiveThinking); on a
// model that takes a budget, or one the catalog does not describe, it
// becomes the effort's share of max_tokens, the budget every Claude accepts.
// A Claude client's own request never comes here.
func fitBridgedThinking(request *dto.ClaudeRequest, resolve PolicyResolver) {
	if request == nil || request.Thinking == nil {
		return
	}
	base := request.Model
	if trimmed, _, ok := TrimEffortSuffix(base); ok && resolve(trimmed).Known {
		base = trimmed
	}
	base, _ = TrimThinkingSuffix(base)
	policy := resolve(base)
	effort := outputConfigEffort(request.OutputConfig)
	switch {
	case request.Thinking.Type == "adaptive" && policy.Adaptive():
		if snapped := model.SnapEffort(base, effort); snapped != "none" {
			effort = snapped
		}
		ApplyAdaptiveThinking(request, effort)
	case request.Thinking.Type == "adaptive":
		maxTokens := request.MaxTokens
		if maxTokens == 0 {
			maxTokens = uint(GetDefaultMaxTokensForThinkingModel(request.Model, base))
		}
		share := EffortToPercentage(effort)
		if share <= 0 {
			share = model_setting.GetClaudeSettings().ThinkingAdapterBudgetTokensPercentage
		}
		budget := int(float64(maxTokens) * share)
		request.Thinking = &dto.Thinking{Type: "enabled", BudgetTokens: &budget}
		request.OutputConfig = withoutOutputConfigEffort(request.OutputConfig)
	case request.Thinking.Type == "enabled" && !policy.Adaptive():
		// the budget shape with the effort the client gave beside it; only
		// an adaptive model takes the effort instead (NormalizeThinking)
		request.OutputConfig = withoutOutputConfigEffort(request.OutputConfig)
	}
}

// clientEmitter writes content events in the client's protocol.
type clientEmitter interface {
	emit(c *gin.Context, events ...textgen.Event) error
	finish(c *gin.Context, usage *dto.Usage, includeUsage bool) error
	fail(c *gin.Context, err *textgen.SemanticError, usage *dto.Usage)
}

type chatClient struct{ emitter *openai_chat.StreamEmitter }

func (e *chatClient) emit(c *gin.Context, events ...textgen.Event) error {
	for _, chunk := range e.emitter.Emit(events...) {
		if err := helper.ObjectData(c, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (e *chatClient) finish(c *gin.Context, usage *dto.Usage, includeUsage bool) error {
	chunks, err := e.emitter.Finish(clientUsage(usage))
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if !includeUsage {
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

func (e *chatClient) fail(c *gin.Context, semantic *textgen.SemanticError, usage *dto.Usage) {
	for _, chunk := range e.emitter.Emit(textgen.Event{Kind: textgen.EventError, Error: semantic}) {
		_ = helper.ObjectData(c, chunk)
	}
	helper.Done(c)
}

type responsesClient struct {
	emitter *openai_responses.StreamEmitter
}

func (e *responsesClient) write(c *gin.Context, payloads []map[string]any) error {
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

func (e *responsesClient) emit(c *gin.Context, events ...textgen.Event) error {
	payloads, err := e.emitter.Emit(events...)
	if writeErr := e.write(c, payloads); writeErr != nil {
		return writeErr
	}
	return err
}

func (e *responsesClient) finish(c *gin.Context, usage *dto.Usage, _ bool) error {
	payloads, err := e.emitter.Finish(clientUsage(usage))
	if err != nil {
		return err
	}
	return e.write(c, payloads)
}

func (e *responsesClient) fail(c *gin.Context, semantic *textgen.SemanticError, usage *dto.Usage) {
	_ = e.write(c, e.emitter.FailWithSemanticError(semantic, usage))
}

func emitterFor(c *gin.Context, info *relaycommon.RelayInfo) clientEmitter {
	if info.ProtocolPlan.ClientProtocol.Family == textgen.ProtocolOpenAIResponses {
		return &responsesClient{emitter: openai_responses.NewStreamEmitter(helper.GetResponseID(c), info.UpstreamModelName, info.ProtocolRequest, openai_responses.RetainedOutputLimit(info.ProtocolRequest))}
	}
	return &chatClient{emitter: openai_chat.NewStreamEmitter(helper.GetResponseID(c), info.UpstreamModelName)}
}

// observe performs the side effects of the events the outcome row and
// billing read.
func observe(c *gin.Context, info *relaycommon.RelayInfo, events []textgen.Event) {
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

func observeServerTools(c *gin.Context, data string) {
	if requests := gjson.Get(data, "usage.server_tool_use.web_search_requests"); requests.Exists() && requests.Int() > 0 {
		c.Set("claude_web_search_requests", int(requests.Int()))
	}
}

// HubStreamHandler serves a streamed Messages reply to a Chat or Responses
// client.
func HubStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.LazuError) {
	decoder := &anthropic_messages.StreamDecoder{}
	client := emitterFor(c, info)
	var col textgen.Collector
	var relayErr *types.LazuError
	var upstreamFailed *textgen.SemanticError
	// forwarded is whether this handler has emitted content to the client.
	// Judged from its own state: the scanner stamps info.HasSendResponse on
	// the first upstream event and a keep-alive ping marks the writer
	// written, and neither says the client saw output.
	forwarded := false
	scan := helper.StreamScannerHandler(c, resp, info, func(data string) bool {
		events, err := decoder.Decode(data)
		if err != nil {
			relayErr = types.NewError(err, types.ErrorCodeBadResponseBody)
			return false
		}
		observeServerTools(c, data)
		observe(c, info, events)
		col.Add(events...)
		for _, event := range events {
			if event.Kind == textgen.EventError {
				upstreamFailed = event.Error
			}
		}
		if upstreamFailed != nil && !forwarded {
			// An in-band failure before any output: the attempt never
			// became a response, so it is retryable like a pre-stream
			// failure, and nothing is written to the client.
			common.SetContextKey(c, constant.ContextKeyStreamFirstEventFailed, true)
			return false
		}
		if err := client.emit(c, events...); err != nil {
			relayErr = types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			return false
		}
		if len(events) > 0 {
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
			// the gateway absorbs the attempt: refunded even when the
			// failure carried usage
			info.StreamFailedWithoutUsage = true
			return usage, types.NewErrorWithStatusCode(fmt.Errorf("%s", upstreamFailed.Message), types.ErrorCodeUpstreamError, http.StatusBadGateway)
		}
		// Once the client has seen output, replaying would duplicate it; the
		// emitter already wrote the failure in the client's protocol.
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
	if !col.Result().StopSeen {
		// a clean end of the stream before the upstream said how the reply
		// ended: cut off, not complete. Before any output it is retryable
		// like a pre-stream failure; after, the client is told in its own
		// protocol and the attempt is not replayed.
		if !forwarded {
			info.StreamFailedWithoutUsage = true
			common.SetContextKey(c, constant.ContextKeyStreamFirstEventFailed, true)
			return usage, types.NewError(io.ErrUnexpectedEOF, types.ErrorCodeBadResponse)
		}
		client.fail(c, &textgen.SemanticError{Type: "api_error", Code: "upstream_stream_failed", Message: "upstream stream ended before the reply finished"}, usage)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, true)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		return usage, types.NewError(io.ErrUnexpectedEOF, types.ErrorCodeBadResponse, types.ErrOptionWithSkipRetry())
	}
	if err := client.finish(c, usage, info.ShouldIncludeUsage); err != nil {
		// the upstream finished and its usage is known: settled even
		// though the client went away before the last frame
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, true)
		return usage, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return usage, nil
}

// HubHandler serves a whole Messages reply to a Chat or Responses client.
func HubHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.LazuError) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeReadResponseBodyFailed)
	}
	service.CloseResponseBodyGracefully(resp)
	events, err := anthropic_messages.DecodeMessage(body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	observeServerTools(c, string(body))
	observe(c, info, events)
	var col textgen.Collector
	col.Add(events...)
	result := col.Result()
	if result.Error != nil {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("%s", result.Error.Message), types.ErrorCodeUpstreamError, http.StatusBadGateway)
	}
	usage := channel.SettleHubUsage(c, info, result)
	result.Usage = clientUsage(usage)
	chat := openai_chat.Render(result, info.UpstreamModelName)
	chat.Id = openai_chat.CompletionID(helper.GetResponseID(c))
	var payload any = chat
	if info.ProtocolPlan.ClientProtocol.Family == textgen.ProtocolOpenAIResponses {
		payload = openai_responses.ChatResponseToResponses(chat, info.UpstreamModelName, info.ProtocolRequest)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	service.IOCopyBytesGracefully(c, resp, data)
	return usage, nil
}
