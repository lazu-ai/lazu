package relay

import (
	"fmt"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	claudechannel "github.com/lazu-ai/lazu/internal/relay/channel/claude"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/wirepatch"
	"github.com/lazu-ai/lazu/internal/types"
)

// planBridges says whether the attempt is planned as a bridge: the client's
// protocol is not the upstream's, and the request goes as the IR encoded.
// A Claude model on the legacy Text Completions API is not: its adaptor
// sends a prompt to /v1/complete, which no Messages encoding fits.
func planBridges(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ProtocolPlan == nil || info.ProtocolPlan.Native || info.ProtocolRequest == nil {
		return false
	}
	return info.ProtocolPlan.UpstreamProtocol.Family != textgen.ProtocolAnthropic || !claudechannel.UsesCompletionAPI(info.UpstreamModelName)
}

// BridgeBody is the outbound body of an attempt planned as a bridge, for any
// client protocol: the attempt's IR encoded by the upstream protocol's codec,
// converted by the channel's adaptor as any request of that protocol is,
// then the disabled fields removed and the wire dialect patch applied.
// bridged is false when the attempt is not a bridge, and the body is then
// nil.
//
// Between Chat and Responses a bridge's overrides are the typed semantic
// ones, applied to the IR when the attempt was planned
// (textgenruntime.PrepareAttempt). Across families (a Claude client, or an
// Anthropic channel) the channel's raw param_override is written for the
// upstream's wire and is applied to the encoded body, as the legacy
// conversions did (textgenruntime.OverrideIsUpstreamWire).
func BridgeBody(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.BaseAdaptor) (body []byte, bridged bool, lazuErr *types.LazuError) {
	if !planBridges(info) {
		return nil, false, nil
	}
	semantic := info.ProtocolRequest.Clone()
	stripLazuMetadataFromSemantic(semantic)
	semantic.Model.Upstream = info.UpstreamModelName
	encodeFailed := func(err error) *types.LazuError {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeConvertRequestFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	var converted any
	switch info.ProtocolPlan.UpstreamProtocol.Family {
	case textgen.ProtocolAnthropic:
		request, err := claudechannel.EncodeBridgedRequest(c, info, semantic)
		if err != nil {
			return nil, true, encodeFailed(err)
		}
		converted = request
	case textgen.ProtocolOpenAIResponses:
		request, err := openai_responses.Encode(semantic)
		if err != nil {
			return nil, true, encodeFailed(err)
		}
		relaycommon.PopulateResponsesUsageInfo(info, request)
		converter, converterErr := requireResponsesRequestConverter(adaptor, info)
		if converterErr != nil {
			return nil, true, converterErr
		}
		converted, err = converter.ConvertOpenAIResponsesRequest(c, info, *request)
		if err != nil {
			return nil, true, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
	case textgen.ProtocolOpenAIChat:
		request, err := openai_chat.Encode(semantic)
		if err != nil {
			return nil, true, encodeFailed(err)
		}
		if info.IsStream && info.SupportStreamOptions && request.StreamOptions == nil {
			// the hub settles from the provider's usage, which a Chat stream
			// reports only when asked
			request.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
		}
		converter, converterErr := requireOpenAIRequestConverter(adaptor, info)
		if converterErr != nil {
			return nil, true, converterErr
		}
		converted, err = converter.ConvertOpenAIRequest(c, info, request)
		if err != nil {
			return nil, true, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
	default:
		return nil, true, types.NewError(fmt.Errorf("no codec encodes for %s", info.ProtocolPlan.UpstreamProtocol.Family), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	jsonData, err := common.Marshal(converted)
	if err != nil {
		return nil, true, types.NewError(err, types.ErrorCodeJsonMarshalFailed, types.ErrOptionWithSkipRetry())
	}
	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
	if err != nil {
		return nil, true, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if len(info.ParamOverride) > 0 && crossFamily(info) {
		jsonData, err = relaycommon.ApplyParamOverride(jsonData, info.ParamOverride)
		if err != nil {
			return nil, true, types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
		}
		if jsonData, lazuErr = afterRawOverride(info, jsonData); lazuErr != nil {
			return nil, true, lazuErr
		}
	}
	// wire dialect patch: vendor quirks on the compiled body, once per
	// attempt, after the override (internal/relay/wirepatch)
	return wirepatch.Apply(wirepatch.ResolveDialect(info), jsonData), true, nil
}

// crossFamily says whether the bridge crosses between Anthropic's Messages
// and the OpenAI protocols, where the channel's override is its upstream
// wire's (textgenruntime.OverrideIsUpstreamWire).
func crossFamily(info *relaycommon.RelayInfo) bool {
	return (info.ProtocolPlan.ClientProtocol.Family == textgen.ProtocolAnthropic) != (info.ProtocolPlan.UpstreamProtocol.Family == textgen.ProtocolAnthropic)
}

// afterRawOverride holds what the plan decided against a channel's raw
// override: the transport stays the client's (the response handler was
// chosen by it), a Messages body keeps the thinking constraints, and a
// Responses body stays stateless, with the hosted tools it now carries
// known to billing.
func afterRawOverride(info *relaycommon.RelayInfo, body []byte) ([]byte, *types.LazuError) {
	if stream := gjson.GetBytes(body, "stream"); stream.Bool() != info.IsStream {
		// changed or deleted: the encoder wrote it for a streaming client,
		// and a non-streaming one stays without it
		body, _ = sjson.SetBytes(body, "stream", info.IsStream)
	}
	switch info.ProtocolPlan.UpstreamProtocol.Family {
	case textgen.ProtocolAnthropic:
		body = claudechannel.ReassertThinkingConstraints(body)
	case textgen.ProtocolOpenAIResponses:
		for _, field := range []string{"previous_response_id", "conversation"} {
			if gjson.GetBytes(body, field).Exists() {
				return nil, types.NewError(fmt.Errorf("param_override sets %s, which a stateless protocol bridge cannot send", field), types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
			}
		}
		if gjson.GetBytes(body, "store").Bool() {
			return nil, types.NewError(fmt.Errorf("param_override sets store=true, which a stateless protocol bridge cannot send"), types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
		}
		var final dto.OpenAIResponsesRequest
		if common.Unmarshal(body, &final) == nil {
			relaycommon.PopulateResponsesUsageInfo(info, &final)
		}
	}
	return body, nil
}
