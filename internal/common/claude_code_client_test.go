package common

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClaudeCodeRequestCompatibility(t *testing.T) {
	for _, tc := range []struct {
		method, ua string
		want       bool
	}{
		{"POST", "claude-cli/2.1.276 (external, cli)", true},
		{"POST", "claude-cli/2.1.276 (external, claude-desktop, agent-sdk/0.3.284)", true},
		{"POST", "Claude-CLI/2.0.0", true},
		{"GET", "claude-cli/2.1.276 (external, cli)", false},
		{"POST", "claude-code-router/1.0", false},
		{"POST", "Anthropic/Python 0.40.0", false},
		{"POST", "", false},
	} {
		r := httptest.NewRequest(tc.method, "/v1/messages", nil)
		r.Header.Set("User-Agent", tc.ua)
		if IsClaudeCodeRequest(r) != tc.want {
			t.Errorf("unexpected compatibility for %+v", tc)
		}
	}
	if IsClaudeCodeRequest(nil) {
		t.Error("nil request")
	}
}

func TestClaudeCodeHeadersDoNotLeakCredentialsOrReuseSessions(t *testing.T) {
	dst := http.Header{}
	for _, session := range []string{"first", "second"} {
		src := http.Header{}
		src.Set("User-Agent", "claude-cli/2.1.276 (external, cli)")
		src.Set("X-Claude-Code-Session-Id", session)
		src.Set("X-Stainless-OS", "MacOS")
		src.Set("Authorization", "secret")
		src.Set("x-api-key", "secret")
		src.Set("Cookie", "secret")
		ForwardClaudeCodeHeaders(src, dst)
		if dst.Get("X-Claude-Code-Session-Id") != session || dst.Get("User-Agent") == "" || dst.Get("X-Stainless-OS") != "MacOS" {
			t.Fatal("client metadata not forwarded")
		}
		if dst.Get("Authorization") != "" || dst.Get("x-api-key") != "" || dst.Get("Cookie") != "" {
			t.Fatal("credentials forwarded")
		}
	}
	ForwardClaudeCodeHeaders(http.Header{}, dst)
	if dst.Get("X-Claude-Code-Session-Id") != "" {
		t.Fatal("stale session")
	}
}
