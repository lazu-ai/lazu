package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/tidwall/gjson"
)

// probeAdaptor is a channel that keeps what it would send. It converts
// requests unchanged, so a test sees exactly what the handler does around
// the conversion; it has no native converter, so the handler takes its
// generic paths.
type probeAdaptor struct {
	channel.BaseAdaptor
	bodies [][]byte
}

func (a *probeAdaptor) Init(*relaycommon.RelayInfo) {}
func (a *probeAdaptor) ConvertOpenAIRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	return request, nil
}
func (a *probeAdaptor) ConvertClaudeRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	return request, nil
}
func (a *probeAdaptor) DoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, body io.Reader) (any, error) {
	raw, _ := io.ReadAll(body)
	a.bodies = append(a.bodies, raw)
	return nil, errors.New("stop after outbound capture")
}

func (a *probeAdaptor) last(t *testing.T) []byte {
	t.Helper()
	if len(a.bodies) == 0 {
		t.Fatal("request did not reach the adaptor")
	}
	return a.bodies[len(a.bodies)-1]
}

func useProbe(t *testing.T, apiType int) *probeAdaptor {
	t.Helper()
	probe := &probeAdaptor{}
	old := adaptorFactories[apiType]
	adaptorFactories[apiType] = func() channel.BaseAdaptor { return probe }
	t.Cleanup(func() { adaptorFactories[apiType] = old })
	return probe
}

func probeContext(path string, raw []byte, channelType int, model string, settings dto.ChannelSettings) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", path, bytes.NewReader(raw))
	c.Set(common.KeyRequestBody, raw)
	common.SetContextKey(c, constant.ContextKeyChannelType, channelType)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, model)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, settings)
	return c
}

// disabledByDefault are the fields RemoveDisabledFields strips from a channel
// with default other settings.
var disabledByDefault = []string{"service_tier", "safety_identifier", "stream_options.include_obfuscation"}

func assertField(t *testing.T, body []byte, path string, want bool) {
	t.Helper()
	if got := gjson.GetBytes(body, path).Exists(); got != want {
		t.Fatalf("%s present=%t, want %t: %s", path, got, want, body)
	}
}

// With pass-through on, a same-protocol request reaches the upstream as the
// client sent it: fields the channel would otherwise strip stay, and unknown
// fields keep their exact bytes. With it off, the channel's field policy
// applies and unknown fields still go through. Chat and Messages behave the
// same way.
func TestPassThroughDecidesFieldPolicyOnSameProtocolPaths(t *testing.T) {
	chat := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true,` +
		`"stream_options":{"include_usage":true,"include_obfuscation":true},` +
		`"service_tier":"priority","safety_identifier":"user-1","store":true,"vendor_x":9007199254740993}`)
	messages := []byte(`{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"hi"}],` +
		`"service_tier":"auto","vendor_x":9007199254740993}`)
	for _, passThrough := range []bool{true, false} {
		mode := map[bool]string{true: "pass-through", false: "field policy"}[passThrough]
		t.Run("chat/"+mode, func(t *testing.T) {
			probe := useProbe(t, constant.APITypeOpenAI)
			var request dto.GeneralOpenAIRequest
			if err := json.Unmarshal(chat, &request); err != nil {
				t.Fatal(err)
			}
			c := probeContext("/v1/chat/completions", chat, constant.ChannelTypeOpenAI, "m", dto.ChannelSettings{PassThroughBodyEnabled: passThrough})
			_ = TextHelper(c, &relaycommon.RelayInfo{Request: &request, OriginModelName: "m", IsStream: true})
			body := probe.last(t)
			for _, field := range disabledByDefault {
				assertField(t, body, field, passThrough)
			}
			assertField(t, body, "store", true)
			if got := gjson.GetBytes(body, "vendor_x").Raw; got != "9007199254740993" {
				t.Fatalf("unknown field changed: %s", got)
			}
		})
		t.Run("messages/"+mode, func(t *testing.T) {
			withEmptyCatalog(t)
			probe := useProbe(t, constant.APITypeAnthropic)
			var request dto.ClaudeRequest
			if err := json.Unmarshal(messages, &request); err != nil {
				t.Fatal(err)
			}
			c := probeContext("/v1/messages", messages, constant.ChannelTypeAnthropic, "m", dto.ChannelSettings{PassThroughBodyEnabled: passThrough})
			_ = ClaudeHelper(c, &relaycommon.RelayInfo{Request: &request, OriginModelName: "m"})
			body := probe.last(t)
			assertField(t, body, "service_tier", passThrough)
			if got := gjson.GetBytes(body, "vendor_x").Raw; got != "9007199254740993" {
				t.Fatalf("unknown field changed: %s", got)
			}
		})
	}
}

// A failed attempt must not leave its rewrites on the request a retry starts
// from: the handler copies the client's request before model mapping,
// thinking and sampling adjust it, and the second attempt sends the same
// body as the first.
func TestClaudeRetryStartsFromTheClientsRequest(t *testing.T) {
	raw := []byte(`{"model":"claude-alias","max_tokens":64,"temperature":0.7,"top_p":0.9,` +
		`"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hi"}]}`)
	var request dto.ClaudeRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	before, err := common.DeepCopy(&request)
	if err != nil {
		t.Fatal(err)
	}
	withEmptyCatalog(t)
	probe := useProbe(t, constant.APITypeAnthropic)
	c := probeContext("/v1/messages", raw, constant.ChannelTypeAnthropic, "claude-alias", dto.ChannelSettings{})
	c.Set(string(constant.ContextKeyChannelModelMapping), `{"claude-alias":"claude-real"}`)
	info := &relaycommon.RelayInfo{Request: &request, OriginModelName: "claude-alias"}
	for attempt := 0; attempt < 2; attempt++ {
		_ = ClaudeHelper(c, info)
		if !reflect.DeepEqual(&request, before) {
			t.Fatalf("attempt %d changed the client's request: %+v", attempt+1, request)
		}
	}
	if len(probe.bodies) != 2 {
		t.Fatalf("want two attempts, got %d", len(probe.bodies))
	}
	if gjson.GetBytes(probe.bodies[0], "model").String() != "claude-real" {
		t.Fatalf("model mapping not applied: %s", probe.bodies[0])
	}
	if !bytes.Equal(probe.bodies[0], probe.bodies[1]) {
		t.Fatalf("retry sent a different body:\n%s\n%s", probe.bodies[0], probe.bodies[1])
	}
}

// The hub path builds its body from the IR, so it has no raw body to pass
// through; it honours the same switch by not stripping the channel's
// disabled fields when pass-through is on, and stripping them when it is off,
// as the same-protocol paths do.
func TestPassThroughDecidesFieldPolicyOnTheHubPath(t *testing.T) {
	raw := []byte(`{"model":"claude-x","max_tokens":16,"service_tier":"auto","messages":[{"role":"user","content":"hi"}]}`)
	for _, passThrough := range []bool{true, false} {
		mode := map[bool]string{true: "pass-through", false: "field policy"}[passThrough]
		t.Run(mode, func(t *testing.T) {
			withEmptyCatalog(t)
			parsed, err := textgenruntime.Parse(textgen.ProtocolAnthropic, raw)
			if err != nil {
				t.Fatal(err)
			}
			probe := useProbe(t, constant.APITypeOpenAI)
			c := probeContext("/v1/messages", raw, constant.ChannelTypeOpenAI, "chat-model", dto.ChannelSettings{PassThroughBodyEnabled: passThrough})
			info := &relaycommon.RelayInfo{
				Request:         parsed.Wire,
				OriginModelName: "chat-model",
				ProtocolRequest: parsed.Semantic,
				ProtocolPlan: &textgen.RelayPlan{
					ClientProtocol:   parsed.Client,
					UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat},
				},
			}
			_ = ClaudeHelper(c, info)
			body := probe.last(t)
			if !gjson.GetBytes(body, "messages").IsArray() {
				t.Fatalf("not a Chat body: %s", body)
			}
			assertField(t, body, "service_tier", passThrough)
		})
	}
}
