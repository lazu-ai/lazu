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
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/tidwall/gjson"
)

// captureNativeAdaptor offers the native body path; a degraded plan must not
// take it.
type captureNativeAdaptor struct {
	channel.BaseAdaptor
	body         []byte
	nativeCalled bool
}

func (a *captureNativeAdaptor) Init(*relaycommon.RelayInfo) {}
func (a *captureNativeAdaptor) ConvertNativeRequest(body []byte, _ *relaycommon.RelayInfo, _ bool) ([]byte, error) {
	a.nativeCalled = true
	return body, nil
}
func (a *captureNativeAdaptor) DoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, body io.Reader) (interface{}, error) {
	a.body, _ = io.ReadAll(body)
	return nil, errors.New("stop after outbound capture")
}

// A json_schema request on a channel that denies it is degraded to
// json_object; the native plan must send the degraded request, not the
// client's bytes, and keep the client's own unknown top-level fields.
func TestTextHelperSendsTheDegradedRequestOnANativePlan(t *testing.T) {
	raw := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"t","schema":{"type":"object"}}},"vendor_flag":7}`)
	semantic, wire, err := openai_chat.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	degraded, issues := textgen.Degrade(textgen.CapabilitySnapshot{Dialect: "openai_chat_compatible", Features: textgen.FeatureSet(1)<<textgen.FeatureEndpointChat | textgen.FeatureSet(1)<<textgen.FeatureJSONOutput, Denied: textgen.FeatureSet(1) << textgen.FeatureJSONSchema}, semantic)
	if len(issues) != 1 {
		t.Fatalf("expected one degradation, got %+v", issues)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(raw))
	c.Set(common.KeyRequestBody, raw)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "m")
	adapter := &captureNativeAdaptor{}
	old := adaptorFactories[constant.APITypeOpenAI]
	adaptorFactories[constant.APITypeOpenAI] = func() channel.BaseAdaptor { return adapter }
	defer func() { adaptorFactories[constant.APITypeOpenAI] = old }()
	info := &relaycommon.RelayInfo{
		Request:         wire,
		OriginModelName: "m",
		ProtocolRequest: degraded,
		ProtocolPlan: &textgen.RelayPlan{
			ClientProtocol:   textgen.Protocol{Family: textgen.ProtocolOpenAIChat, Dialect: "openai"},
			UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat, Dialect: "openai_chat_compatible"},
			Native:           true,
			Warnings:         issues,
		},
	}
	_ = TextHelper(c, info)
	if adapter.nativeCalled {
		t.Fatal("a degraded plan must not send the client's bytes through the native path")
	}
	if got := gjson.GetBytes(adapter.body, "response_format.type").String(); got != "json_object" {
		t.Fatalf("response_format.type = %q, want json_object (body %s)", got, adapter.body)
	}
	if gjson.GetBytes(adapter.body, "response_format.json_schema").Exists() {
		t.Fatalf("the schema must not reach the upstream: %s", adapter.body)
	}
	if gjson.GetBytes(adapter.body, "vendor_flag").Int() != 7 {
		t.Fatalf("the client's unknown field was lost: %s", adapter.body)
	}
	var messages []map[string]any
	_ = json.Unmarshal([]byte(gjson.GetBytes(adapter.body, "messages").Raw), &messages)
	if len(messages) != 2 || messages[0]["role"] != "system" {
		t.Fatalf("the schema instruction is missing: %s", adapter.body)
	}
}
