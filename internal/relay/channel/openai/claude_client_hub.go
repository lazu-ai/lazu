package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	anthropic_messages "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/anthropic_messages"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
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

// ClaudeClientStreamHandler serves a streamed Chat Completions or Responses
// reply to a Claude client (channel.RunHubStream).
func ClaudeClientStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.LazuError) {
	return channel.RunHubStream(c, resp, info, channel.HubStream{
		Decode: decoderFor(info),
		// a Chat upstream's usage chunk comes after its finish
		ReadPastStop: info.ProtocolPlan.UpstreamProtocol.Family == textgen.ProtocolOpenAIChat,
	}, channel.HubClientFor(c, info))
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
	var col textgen.Collector
	col.Add(events...)
	result := col.Result()
	if result.Model != "" {
		info.UpstreamModelName = result.Model
	}
	if result.StopSeen {
		info.FinishReason = openai_chat.FinishReasonOf(result.Stop)
	}
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
