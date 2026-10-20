package openai

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/packages/domain/providercap"
	"github.com/tidwall/gjson"
)

// The native (byte-preserving) path is the hot path for a DeepSeek channel.
// ParamOverride introduces the \0 here, so the \u0000 in the result proves the
// wire dialect patch ran after it, with the dialect taken from the plan.
func TestConvertNativeRequestAppliesWirePatchAfterParamOverride(t *testing.T) {
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"id":{"type":"string","pattern":"^[a-z]+$"}}}}}]}`)
	override := map[string]interface{}{"operations": []interface{}{
		map[string]interface{}{"path": "tools.0.function.parameters.properties.id.pattern", "mode": "set", "value": `^[^\0]+$`},
	}}
	for _, tc := range []struct {
		dialect string
		want    string
	}{
		{providercap.DialectDeepSeek, `^[^\u0000]+$`},
		{providercap.DialectOpenAIChatCompatible, `^[^\0]+$`},
	} {
		info := &relaycommon.RelayInfo{
			ProtocolPlan: &textgen.RelayPlan{Native: true, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat, Dialect: tc.dialect}},
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeOpenAI,
				ApiType:           constant.APITypeOpenAI,
				UpstreamModelName: "deepseek-chat",
				ParamOverride:     override,
				ChannelSetting:    dto.ChannelSettings{},
			},
		}
		out, err := (&Adaptor{}).ConvertNativeRequest(body, info, false)
		if err != nil {
			t.Fatalf("%s: %v", tc.dialect, err)
		}
		if got := gjson.GetBytes(out, "tools.0.function.parameters.properties.id.pattern").String(); got != tc.want {
			t.Fatalf("%s: pattern = %q, want %q (body %s)", tc.dialect, got, tc.want, out)
		}
	}
}
