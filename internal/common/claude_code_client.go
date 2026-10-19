package common

import (
	"net/http"
	"strings"
)

// IsClaudeCodeRequest is a routing compatibility hint, not authentication.
// Claude Code names itself with a `claude-cli/<version>` User-Agent on every
// Messages call; the path is not checked because health probes reach an
// Anthropic source through the chat-completions converter.
func IsClaudeCodeRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.UserAgent())), "claude-cli/")
}

var claudeCodeClientHeaders = []string{
	"User-Agent",
	"x-app",
	"X-Claude-Code-Session-Id",
	"X-Stainless-Arch",
	"X-Stainless-Lang",
	"X-Stainless-OS",
	"X-Stainless-Package-Version",
	"X-Stainless-Retry-Count",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
	"X-Stainless-Timeout",
}

// ForwardClaudeCodeHeaders copies only this request's client metadata.
// Credentials, cookies and arbitrary headers must never be forwarded by this
// opt-in; anthropic-* headers already travel through the protocol passthrough.
func ForwardClaudeCodeHeaders(src http.Header, dst http.Header) {
	for _, name := range claudeCodeClientHeaders {
		dst.Del(name)
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}
}
