package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/packages/domain/providercap"
	"github.com/tidwall/gjson"
)

// captureDTOAdaptor has no ConvertNativeRequest and no ConvertOpenAIRequest,
// so TextHelper takes the DTO conversion branch (with the default OpenAI
// converter) and the body it would send upstream is captured here.
type captureDTOAdaptor struct {
	channel.BaseAdaptor
	body []byte
}

func (a *captureDTOAdaptor) Init(*relaycommon.RelayInfo) {}
func (a *captureDTOAdaptor) DoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, body io.Reader) (interface{}, error) {
	a.body, _ = io.ReadAll(body)
	return nil, errors.New("stop after outbound capture")
}

// The wire dialect patch runs after ParamOverride: the override here is what
// introduces the \0 pattern, so only a patch applied afterwards can turn it
// into \u0000. The channel is identified by its configured dialect alone
// (no ProtocolPlan), which exercises the fallback resolver.
func TestWirePatchRunsAfterParamOverrideForDeepSeekChannel(t *testing.T) {
	raw := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"id":{"type":"string","pattern":"^[a-z]+$"}}}}}]}`)
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{"path": "tools.0.function.parameters.properties.id.pattern", "mode": "set", "value": `^[^\0]+$`},
			map[string]interface{}{"path": "temperature", "mode": "set", "value": 0.2},
		},
	}
	for _, tc := range []struct {
		name    string
		dialect string
		want    string
	}{
		{name: "deepseek dialect is patched", dialect: providercap.DialectDeepSeek, want: `^[^\u0000]+$`},
		{name: "other dialect keeps the operator's bytes", dialect: providercap.DialectOpenAIChatCompatible, want: `^[^\0]+$`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(raw))
			c.Set(common.KeyRequestBody, raw)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "deepseek-chat")
			common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{ProtocolDialect: tc.dialect})
			common.SetContextKey(c, constant.ContextKeyChannelParamOverride, override)
			adapter := &captureDTOAdaptor{}
			old := adaptorFactories[constant.APITypeOpenAI]
			adaptorFactories[constant.APITypeOpenAI] = func() channel.BaseAdaptor { return adapter }
			defer func() { adaptorFactories[constant.APITypeOpenAI] = old }()

			info := &relaycommon.RelayInfo{Request: &request, OriginModelName: "deepseek-chat"}
			_ = TextHelper(c, info)
			if len(adapter.body) == 0 {
				t.Fatal("request did not reach the adapter")
			}
			if got := gjson.GetBytes(adapter.body, "temperature").Num; got != 0.2 {
				t.Fatalf("param override was not applied: %s", adapter.body)
			}
			if got := gjson.GetBytes(adapter.body, "tools.0.function.parameters.properties.id.pattern").String(); got != tc.want {
				t.Fatalf("pattern = %q, want %q (body %s)", got, tc.want, adapter.body)
			}
		})
	}
}
