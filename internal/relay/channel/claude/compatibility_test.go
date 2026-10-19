package claude

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestClaudeHeadersPreserveMultipleBetaValues(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Add("anthropic-beta", "claude-code-20250219,adaptive-thinking-2025-11-24")
	c.Request.Header.Add("anthropic-beta", "effort-2025-11-24,mid-conversation-system-2026-04-07")
	headers := http.Header{}
	info := &relaycommon.RelayInfo{OriginModelName: "claude-sonnet-5", ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "upstream-key"}}
	if err := new(Adaptor).SetupRequestHeader(c, &headers, info); err != nil {
		t.Fatal(err)
	}
	got := headers.Get("anthropic-beta")
	for _, flag := range []string{"claude-code-20250219", "effort-2025-11-24", "mid-conversation-system-2026-04-07"} {
		if !strings.Contains(got, flag) {
			t.Fatalf("lost %s: %s", flag, got)
		}
	}
	if strings.Contains(got, "adaptive-thinking-2025-11-24") {
		t.Fatal("legacy behavior changed")
	}
	if len(c.Request.Header.Values("anthropic-beta")) != 2 {
		t.Fatal("mutated client headers")
	}
}
