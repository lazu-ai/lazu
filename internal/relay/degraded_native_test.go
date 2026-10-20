package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
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

// The degraded request differs from the client's only by the degradation:
// fields the semantic encoder would drop or rename go as the client sent
// them.
func TestDegradedChatRequestKeepsTheClientsFields(t *testing.T) {
	raw := []byte(`{"model":"m","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hi"}],"web_search_options":{"search_context_size":"low"},"store":true,"max_tokens":64,"response_format":{"type":"json_schema","json_schema":{"name":"t","schema":{"type":"object"}}}}`)
	semantic, wire, err := openai_chat.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	degraded, _ := textgen.Degrade(textgen.CapabilitySnapshot{Dialect: "openai_chat_compatible", Features: textgen.FeatureSet(1)<<textgen.FeatureEndpointChat | textgen.FeatureSet(1)<<textgen.FeatureJSONOutput, Denied: textgen.FeatureSet(1) << textgen.FeatureJSONSchema}, semantic)
	out, lazuErr := degradedChatRequest(&relaycommon.RelayInfo{ProtocolRequest: degraded}, wire)
	if lazuErr != nil {
		t.Fatal(lazuErr)
	}
	body, _ := json.Marshal(out)
	if !gjson.GetBytes(body, "web_search_options").Exists() || !gjson.GetBytes(body, "store").Bool() || gjson.GetBytes(body, "max_tokens").Int() != 64 || gjson.GetBytes(body, "max_completion_tokens").Exists() {
		t.Fatalf("a client field changed: %s", body)
	}
	if gjson.GetBytes(body, "response_format.type").String() != "json_object" {
		t.Fatalf("response_format: %s", body)
	}
	system := gjson.GetBytes(body, "messages.0.content").String()
	if gjson.GetBytes(body, "messages.#").Int() != 2 || !strings.HasPrefix(system, "be brief") || !strings.Contains(system, `"type":"object"`) {
		t.Fatalf("the schema instruction must extend the client's system message: %s", body)
	}
}

// responsesBridgeAdaptor takes the Chat->Responses bridge and captures its
// outbound body.
type responsesBridgeAdaptor struct{ captureNativeAdaptor }

func (a *responsesBridgeAdaptor) ConvertOpenAIResponsesRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return request, nil
}

// The Responses bridge body gets the dialect's wire patch like any other:
// DeepSeek rejects the \0 escape in a tool schema's pattern.
func TestChatToResponsesBridgeAppliesTheWirePatch(t *testing.T) {
	raw := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"s":{"type":"string","pattern":"^[^\\0]*$"}}}}}],"response_format":{"type":"json_schema","json_schema":{"name":"t","schema":{"type":"object"}}}}`)
	semantic, wire, err := openai_chat.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(raw))
	c.Set(common.KeyRequestBody, raw)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "m")
	adapter := &responsesBridgeAdaptor{}
	old := adaptorFactories[constant.APITypeOpenAI]
	adaptorFactories[constant.APITypeOpenAI] = func() channel.BaseAdaptor { return adapter }
	defer func() { adaptorFactories[constant.APITypeOpenAI] = old }()
	info := &relaycommon.RelayInfo{
		Request:         wire,
		OriginModelName: "m",
		ProtocolRequest: semantic,
		ProtocolPlan: &textgen.RelayPlan{
			ClientProtocol:   textgen.Protocol{Family: textgen.ProtocolOpenAIChat, Dialect: "openai"},
			UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses, Dialect: "deepseek"},
		},
	}
	_ = TextHelper(c, info)
	if len(adapter.body) == 0 {
		t.Fatal("no outbound body captured")
	}
	if bytes.Contains(adapter.body, []byte(`\\0`)) || !bytes.Contains(adapter.body, []byte(`\\u0000`)) {
		t.Fatalf("the DeepSeek tool-pattern patch was not applied: %s", adapter.body)
	}
}
