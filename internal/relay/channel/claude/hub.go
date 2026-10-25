package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
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
// UsesCompletionAPI says whether the model is served by the legacy Text
// Completions API (claude-2, claude-instant), which no bridge reaches.
func UsesCompletionAPI(upstreamModel string) bool { return usesCompletionAPI(upstreamModel) }

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
	// after the thinking is fitted: EnsureThinkingConstraints writes
	// temperature 1 beside a budget, which a refusing model takes no better
	DropRefusedSampling(request, resolve)
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

func observeServerTools(c *gin.Context, data string) {
	if requests := gjson.Get(data, "usage.server_tool_use.web_search_requests"); requests.Exists() && requests.Int() > 0 {
		c.Set("claude_web_search_requests", int(requests.Int()))
	}
}

// HubStreamHandler serves a streamed Messages reply to a Chat or Responses
// client (channel.RunHubStream).
func HubStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.LazuError) {
	decoder := &anthropic_messages.StreamDecoder{}
	summarized := asksSummary(info)
	return channel.RunHubStream(c, resp, info, channel.HubStream{
		Decode: func(data string) ([]textgen.Event, error) {
			events, err := decoder.Decode(data)
			return markSummary(events, summarized), err
		},
		Observe: func(c *gin.Context, _ *relaycommon.RelayInfo, data string, _ []textgen.Event) {
			observeServerTools(c, data)
		},
		ClientUsage: clientUsage,
	}, channel.HubClientFor(c, info))
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
	events = markSummary(events, asksSummary(info))
	observeServerTools(c, string(body))
	observeWhole(info, events)
	var col textgen.Collector
	col.Add(events...)
	result := col.Result()
	if result.Error != nil {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("%s", result.Error.Message), types.ErrorCodeUpstreamError, http.StatusBadGateway)
	}
	usage := channel.SettleHubUsage(c, info, result)
	result.Usage = clientUsage(usage)
	var payload any
	if info.ProtocolPlan.ClientProtocol.Family == textgen.ProtocolOpenAIResponses {
		// rendered from the events themselves, one hop, items in order
		rendered, renderErr := renderResponses(c, info, events, result.Usage)
		if renderErr != nil {
			return nil, types.NewError(renderErr, types.ErrorCodeBadResponseBody)
		}
		payload = rendered
	} else {
		chat := openai_chat.Render(result, info.UpstreamModelName)
		chat.Id = openai_chat.CompletionID(helper.GetResponseID(c))
		payload = chat
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	service.IOCopyBytesGracefully(c, resp, data)
	return usage, nil
}

// asksSummary says whether a Responses client asked for a reasoning
// summary: the thinking it gets is then that summary (Messages shows it
// summarized on an adaptive model, whole on a budget one).
func asksSummary(info *relaycommon.RelayInfo) bool {
	return info.ProtocolPlan.ClientProtocol.Family == textgen.ProtocolOpenAIResponses && info.ProtocolRequest != nil && info.ProtocolRequest.Reasoning.Summary != ""
}

// markSummary marks the thinking as the summary the client asked for.
func markSummary(events []textgen.Event, summarized bool) []textgen.Event {
	if !summarized {
		return events
	}
	for i := range events {
		if events[i].Kind == textgen.EventReasoning {
			events[i].Summary = true
		}
	}
	return events
}

// renderResponses is a whole reply as a Responses object, written by the
// Responses emitter from the events: its terminal event carries it.
func renderResponses(c *gin.Context, info *relaycommon.RelayInfo, events []textgen.Event, usage *dto.Usage) (any, error) {
	emitter := openai_responses.NewStreamEmitter(helper.GetResponseID(c), info.UpstreamModelName, info.ProtocolRequest)
	if _, err := emitter.Emit(events...); err != nil {
		return nil, err
	}
	payloads, err := emitter.Finish(usage)
	if err != nil {
		return nil, err
	}
	if len(payloads) == 0 {
		return nil, fmt.Errorf("the reply rendered no response")
	}
	response, ok := payloads[len(payloads)-1]["response"]
	if !ok {
		return nil, fmt.Errorf("the reply rendered no response")
	}
	return response, nil
}

// observeWhole records the model that answered and how a whole reply ended.
func observeWhole(info *relaycommon.RelayInfo, events []textgen.Event) {
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
