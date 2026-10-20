package relay

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/tidwall/gjson"
)

// captureChatAdaptor is an OpenAI-compatible channel's adaptor that keeps
// the body it would send.
type captureChatAdaptor struct {
	channel.BaseAdaptor
	body []byte
}

func (a *captureChatAdaptor) Init(*relaycommon.RelayInfo) {}
func (a *captureChatAdaptor) DoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, body io.Reader) (interface{}, error) {
	a.body, _ = io.ReadAll(body)
	return nil, errors.New("stop after outbound capture")
}

// Claude Code's recorded request, planned onto a Chat channel, leaves the
// Claude handler as a Chat request built from the IR.
func TestClaudeHelperBridgesClaudeCodeToChat(t *testing.T) {
	raw := fixtures.ClientRequest(t, "claude_code/tool_image_turn.json")
	parsed, err := textgenruntime.Parse(textgen.ProtocolAnthropic, raw)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(raw))
	c.Set(common.KeyRequestBody, raw)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "deepseek-flash")
	adapter := &captureChatAdaptor{}
	old := adaptorFactories[constant.APITypeOpenAI]
	adaptorFactories[constant.APITypeOpenAI] = func() channel.BaseAdaptor { return adapter }
	defer func() { adaptorFactories[constant.APITypeOpenAI] = old }()
	info := &relaycommon.RelayInfo{
		Request:         parsed.Wire,
		OriginModelName: "deepseek-flash",
		IsStream:        true,
		ProtocolRequest: parsed.Semantic,
		ProtocolPlan: &textgen.RelayPlan{
			ClientProtocol:   parsed.Client,
			UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat, Dialect: "deepseek"},
		},
	}
	_ = ClaudeHelper(c, info)
	body := adapter.body
	if len(body) == 0 {
		t.Fatal("request did not reach the adapter")
	}
	if gjson.GetBytes(body, "model").String() != "deepseek-flash" || !gjson.GetBytes(body, "messages").IsArray() {
		t.Fatalf("not a Chat request for the mapped model: %s", body)
	}
	for _, absent := range []string{"system", "thinking", "output_config", "context_management", "user"} {
		if gjson.GetBytes(body, absent).Exists() {
			t.Fatalf("Messages field %s reached a Chat upstream", absent)
		}
	}
	if strings.Contains(string(body), "cache_control") {
		t.Fatal("advisory cache markers reached a Chat upstream")
	}
	if gjson.GetBytes(body, "messages.0.role").String() != "system" || gjson.GetBytes(body, "tools.0.type").String() != "function" {
		t.Fatalf("system or tools not in Chat's shape: %s", gjson.GetBytes(body, "messages.0").Raw)
	}
}
