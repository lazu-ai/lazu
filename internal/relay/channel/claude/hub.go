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
	return request, nil
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
	return channel.RunHubStream(c, resp, info, channel.HubStream{
		Decode: decoder.Decode,
		Observe: func(c *gin.Context, _ *relaycommon.RelayInfo, data string, _ []textgen.Event) {
			observeServerTools(c, data)
		},
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
	observeServerTools(c, string(body))
	observeWhole(info, events)
	var col textgen.Collector
	col.Add(events...)
	result := col.Result()
	if result.Error != nil {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("%s", result.Error.Message), types.ErrorCodeUpstreamError, http.StatusBadGateway)
	}
	usage := channel.SettleHubUsage(c, info, result)
	result.Usage = usage
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
