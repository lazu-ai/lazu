package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

// withEmptyCatalog gives the model catalog an empty database for the test,
// so the thinking policy resolves as "unknown model" without a seeded
// catalog (the hub asks the catalog for every bridged request).
func withEmptyCatalog(t *testing.T) {
	t.Helper()
	previousDB := model.CoreDB()
	previousRedis, previousMemoryCache := common.RedisEnabled, common.MemoryCacheEnabled
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousMemoryCache
		model.SetCoreDB(previousDB)
	})
}

// captureAnthropicAdaptor stands in for the Claude adaptor and keeps the body
// the handler would send to the Messages API.
type captureAnthropicAdaptor struct {
	channel.BaseAdaptor
	body []byte
}

func (a *captureAnthropicAdaptor) Init(*relaycommon.RelayInfo) {}
func (a *captureAnthropicAdaptor) DoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, body io.Reader) (interface{}, error) {
	a.body, _ = io.ReadAll(body)
	return nil, errors.New("stop after outbound capture")
}

// messagesBridgeContext is an attempt an OpenAI-shaped client's request
// planned onto an Anthropic channel's Messages API, as the controller sets
// it up: the immutable IR beside the plan.
func messagesBridgeContext(t *testing.T, family textgen.ProtocolFamily, path string, raw []byte) (*gin.Context, *relaycommon.RelayInfo, *captureAnthropicAdaptor) {
	t.Helper()
	withEmptyCatalog(t)
	parsed, err := textgenruntime.Parse(family, raw)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", path, bytes.NewReader(raw))
	c.Set(common.KeyRequestBody, raw)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "claude-haiku-5-5")
	// the channel's override, applied to the IR when the attempt was planned
	// (PrepareAttempt) and so never patched onto the Messages body again:
	// a raw "stop" there would be a field Anthropic rejects
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{"path": "temperature", "mode": "set", "value": 0.2},
			map[string]interface{}{"path": "stop", "mode": "set", "value": []interface{}{"END"}},
		},
	})
	planned := parsed.Semantic.Clone()
	planned.Sampling.Temperature = textgen.Some(0.2)
	planned.Sampling.Stop = textgen.Some(json.RawMessage(`["END"]`))
	parsed.Semantic = planned
	adapter := &captureAnthropicAdaptor{}
	old := adaptorFactories[constant.APITypeAnthropic]
	adaptorFactories[constant.APITypeAnthropic] = func() channel.BaseAdaptor { return adapter }
	t.Cleanup(func() { adaptorFactories[constant.APITypeAnthropic] = old })
	// planned as the controller plans it: the channel's typed override
	// (temperature) is applied to the IR here, which is the only way an
	// override reaches a bridged body
	override := `{"operations":[{"path":"temperature","mode":"set","value":0.2}]}`
	attempt, err := textgenruntime.PrepareAttempt(parsed, &model.Channel{Type: constant.ChannelTypeAnthropic, ParamOverride: &override}, "claude-haiku-5-5")
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Plan.Native || attempt.Plan.UpstreamProtocol.Family != textgen.ProtocolAnthropic {
		t.Fatalf("expected a Messages bridge, got %+v", attempt.Plan)
	}
	info := &relaycommon.RelayInfo{
		Request:         parsed.Wire,
		OriginModelName: "claude-haiku-5-5",
		ProtocolRequest: attempt.Request,
		ProtocolPlan:    attempt.Plan,
	}
	return c, info, adapter
}

func TestTextHelperBridgesAChatClientToMessages(t *testing.T) {
	raw := []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"weather in Paris?"}],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"d","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}],
		"tool_choice":"auto","max_tokens":300,"reasoning_effort":"low","user":"u-1","metadata":{"lazu":{"route":"private"}},"stream":true}`)
	c, info, adapter := messagesBridgeContext(t, textgen.ProtocolOpenAIChat, "/v1/chat/completions", raw)
	_ = TextHelper(c, info)
	body := adapter.body
	if len(body) == 0 {
		t.Fatal("request did not reach the adapter")
	}
	if !json.Valid(body) {
		t.Fatalf("outbound body is not JSON: %s", body)
	}
	for path, want := range map[string]string{
		"model":                           "claude-haiku-5-5",
		"system.0.text":                   "be brief",
		"messages.0.role":                 "user",
		"messages.0.content.0.text":       "weather in Paris?",
		"tools.0.name":                    "get_weather",
		"tools.0.input_schema.required.0": "city",
		"tool_choice.type":                "auto",
		"metadata.user_id":                "u-1",
		"max_tokens":                      "1280", // raised to the thinking minimum beside the budget
		"stream":                          "true",
		"temperature":                     "1", // thinking is on: Anthropic takes temperature 1 beside it, and the override is fitted with the rest
		"thinking.type":                   "enabled",
	} {
		if got := gjson.GetBytes(body, path).String(); got != want {
			t.Errorf("%s = %q, want %q (body %s)", path, got, want, body)
		}
	}
	if got := gjson.GetBytes(body, "stop_sequences.0").String(); got != "END" {
		t.Errorf("the planned stop override must reach the body as stop_sequences: %s", body)
	}
	for _, absent := range []string{"reasoning_effort", "messages.0.content.0.type.lazu", "metadata.lazu", "user", "stop"} {
		if gjson.GetBytes(body, absent).Exists() {
			t.Errorf("%s must not reach the Messages API: %s", absent, body)
		}
	}
}

func TestResponsesHelperBridgesAResponsesClientToMessages(t *testing.T) {
	raw := []byte(`{"model":"claude-haiku-5-5","instructions":"be brief","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}],"max_output_tokens":200,"reasoning":{"effort":"none"},"store":false}`)
	c, info, adapter := messagesBridgeContext(t, textgen.ProtocolOpenAIResponses, "/v1/responses", raw)
	_ = ResponsesHelper(c, info)
	body := adapter.body
	if len(body) == 0 {
		t.Fatal("request did not reach the adapter")
	}
	for path, want := range map[string]string{
		"model":                     "claude-haiku-5-5",
		"system.0.text":             "be brief",
		"messages.0.role":           "user",
		"messages.0.content.0.text": "hi",
		"max_tokens":                "200",
		"temperature":               "0.2",
	} {
		if got := gjson.GetBytes(body, path).String(); got != want {
			t.Errorf("%s = %q, want %q (body %s)", path, got, want, body)
		}
	}
	if gjson.GetBytes(body, "input").Exists() || gjson.GetBytes(body, "instructions").Exists() || gjson.GetBytes(body, "store").Exists() {
		t.Fatalf("Responses fields leaked into the Messages body: %s", body)
	}
}
