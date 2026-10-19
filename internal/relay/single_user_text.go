package relay

import (
	"fmt"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

// Run after overrides and passthrough selection as well as during routing:
// a late wire patch must not bypass the channel's restricted input contract.
func normalizeSingleUserTextResponses(body []byte, info *relaycommon.RelayInfo) ([]byte, error) {
	if info == nil || info.ChannelMeta == nil || !info.ChannelOtherSettings.ResponsesSingleUserText {
		return body, nil
	}
	if info.ProtocolPlan == nil || info.ProtocolPlan.UpstreamProtocol.Family != textgen.ProtocolOpenAIResponses {
		return nil, fmt.Errorf("responses_single_user_text requires an upstream Responses protocol plan")
	}
	request := info.ProtocolRequest
	// Arbitrary overrides may replace input or introduce tools/state after the
	// planner approved the IR. Only that exceptional path needs another decode.
	if request == nil || len(info.ParamOverride) > 0 {
		decoded, _, err := openai_responses.Decode(body)
		if err != nil {
			return nil, err
		}
		request = decoded
	}
	text, err := textgen.SingleUserText(request)
	if err != nil {
		return nil, err
	}
	if input := gjson.GetBytes(body, "input"); input.Type == gjson.String && input.Str == text {
		return body, nil
	}
	return sjson.SetBytes(body, "input", text)
}
