package openai

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
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
// client (bridgesResponsesToChat) through channel.RunHubStream.
func chatClientResponsesStream(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.LazuError) {
	decoder := &openai_responses.StreamDecoder{}
	return channel.RunHubStream(c, resp, info, channel.HubStream{
		Decode:     decoder.Decode,
		Unreleased: decoder.Unreleased,
		Observe: func(c *gin.Context, info *relaycommon.RelayInfo, data string, _ []textgen.Event) {
			observeResponsesStreamEvent(c, info, data)
		},
	}, channel.HubClientFor(c, info))
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
