package model

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

// Digests name every client the User-Agent table recognises as a program,
// and fold SDKs, HTTP clients and unknown callers into direct API.
func TestDigestSourceKeyFollowsClientTable(t *testing.T) {
	for _, tag := range []string{"claude-code", "codex", "opencode", "kilo-code", "roo-code", "crush", "goose", "pi", "openclaw", "gemini-cli", "qwen-code", "kimi-cli", "cursor", "zed", "aider", "continue", "cline", "github-copilot", "cherry-studio"} {
		if !common.IsAgentClient(tag) {
			t.Fatalf("%s is no longer in the client table", tag)
		}
		if got := DigestSourceKey(tag); got != tag {
			t.Errorf("%s: got %q", tag, got)
		}
	}
	for _, tag := range []string{"", "openai-python", "anthropic-typescript", "ai-sdk", "stainless-js", "curl", "browser-chrome", "electron"} {
		if got := DigestSourceKey(tag); got != "api" {
			t.Errorf("%q: got %q want api", tag, got)
		}
	}
	if got := DigestSourceKey(" Claude-Code "); got != "claude-code" {
		t.Errorf("tags are normalised, got %q", got)
	}
}
