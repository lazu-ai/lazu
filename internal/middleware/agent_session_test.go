package middleware

import (
	"fmt"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func sessionContext(headers map[string]string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	for name, value := range headers {
		c.Request.Header.Set(name, value)
	}
	return c
}

func TestResolveAgentSessionRecognisesEachAgent(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		body    string
		client  string
		want    agentSession
	}{
		{
			name:    "codex main thread",
			headers: map[string]string{"session-id": "0199f3", "thread-id": "0199f3", "originator": "codex_cli_rs"},
			client:  "codex",
			want:    agentSession{ID: "0199f3", Source: SessionSourceCodex, Kind: RequestKindMain},
		},
		{
			name:    "codex sub-agent",
			headers: map[string]string{"session-id": "0199f3", "thread-id": "0199aa", "x-codex-parent-thread-id": "0199f3"},
			client:  "codex",
			want:    agentSession{ID: "0199f3", Source: SessionSourceCodex, ThreadID: "0199aa", ParentThreadID: "0199f3", Kind: RequestKindSubagent},
		},
		{
			name:    "codex review",
			headers: map[string]string{"session_id": "0199f3", "x-openai-subagent": "review", "originator": "codex_cli_rs"},
			client:  "codex",
			want:    agentSession{ID: "0199f3", Source: SessionSourceCodex, Kind: RequestKindReview},
		},
		{
			name:    "claude code sub-agent",
			headers: map[string]string{"X-Claude-Code-Session-Id": "3f2a9c", "x-claude-code-agent-id": "agent-1", "x-claude-code-parent-agent-id": "main"},
			want:    agentSession{ID: "3f2a9c", Source: SessionSourceClaudeCode, ThreadID: "agent-1", ParentThreadID: "main", Kind: RequestKindSubagent},
		},
		{
			name: "claude code metadata json",
			body: `{"metadata":{"user_id":"{\"device_id\":\"d\",\"account_uuid\":\"\",\"session_id\":\"a91d07\"}"}}`,
			want: agentSession{ID: "a91d07", Source: SessionSourceClaudeCode, Kind: RequestKindMain},
		},
		{
			name: "claude code metadata legacy",
			body: `{"metadata":{"user_id":"user_abc_account_x_session_3f2a9c1e-7d40-4b1a-9e52-0c8a61f2b7d3"}}`,
			want: agentSession{ID: "3f2a9c1e-7d40-4b1a-9e52-0c8a61f2b7d3", Source: SessionSourceClaudeCode, Kind: RequestKindMain},
		},
		{
			name:    "opencode child session",
			headers: map[string]string{"X-Session-Id": "ses_child", "x-parent-session-id": "ses_parent"},
			client:  "opencode",
			want:    agentSession{ID: "ses_parent", Source: SessionSourceOpenCode, ThreadID: "ses_child", ParentThreadID: "ses_parent", Kind: RequestKindSubagent},
		},
		{
			name:    "goose",
			headers: map[string]string{"agent-session-id": "20260929_4"},
			want:    agentSession{ID: "20260929_4", Source: SessionSourceGoose, Kind: RequestKindMain},
		},
		{
			name: "prompt cache key",
			body: `{"prompt_cache_key":"7c1e44"}`,
			want: agentSession{ID: "7c1e44", Source: SessionSourcePromptCache, Kind: RequestKindMain},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveAgentSession(sessionContext(tc.headers), []byte(tc.body), nil, 9, tc.client)
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveAgentSessionFallsBackToKeyPerDay(t *testing.T) {
	got := resolveAgentSession(sessionContext(nil), nil, nil, 42, "cursor")
	if got.Source != SessionSourceToken || !strings.HasPrefix(got.ID, "token-42-") || len(got.ID) != len("token-42-20260930") {
		t.Fatalf("fallback = %+v", got)
	}
}

func TestFirstUserMessageSkipsInjectedContext(t *testing.T) {
	claude := `{"messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>ctx</system-reminder>"},{"type":"text","text":"把总览页改成 A3 方案"}]},{"role":"assistant","content":"ok"}]}`
	if got := firstUserMessage([]byte(claude)); got != "把总览页改成 A3 方案" {
		t.Fatalf("claude = %q", got)
	}
	codex := `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>cwd</environment_context>"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"修一下 billing 的竞态"}]}]}`
	if got := firstUserMessage([]byte(codex)); got != "修一下 billing 的竞态" {
		t.Fatalf("codex = %q", got)
	}
	chat := `{"messages":[{"role":"system","content":"you are"},{"role":"user","content":"hello"}]}`
	if got := firstUserMessage([]byte(chat)); got != "hello" {
		t.Fatalf("chat = %q", got)
	}
}

func TestSessionContentRespectsCaptureSwitch(t *testing.T) {
	previous := common.ModelLogEnabled
	t.Cleanup(func() { common.ModelLogEnabled = previous })
	for _, enabled := range []bool{false, true} {
		common.ModelLogEnabled = enabled
		c := sessionContext(map[string]string{"Content-Type": "application/json", "session_id": fmt.Sprintf("capture-%v", enabled), "originator": "codex_cli_rs"})
		c.Request.Body = io.NopCloser(strings.NewReader(`{"input":[{"role":"user","content":"Private task"}]}`))
		c.Set("id", 99)
		session := applyAgentSession(c, nil, 1)
		if session.ID == "" {
			t.Fatal("disabling content must preserve session attribution")
		}
		content := common.GetContextKeyString(c, constant.ContextKeySessionFirstMessage)
		if enabled && content == "" || !enabled && content != "" {
			t.Fatalf("capture enabled=%v, text=%q", enabled, content)
		}
	}
}
