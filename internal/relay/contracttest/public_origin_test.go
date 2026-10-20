//go:build record

package contracttest

import "testing"

func TestPublicOriginKeepsSecretsOutOfFixtures(t *testing.T) {
	for raw, want := range map[string]string{
		"https://api.deepseek.com/v1/chat/completions":            "https://api.deepseek.com/v1/chat/completions",
		"https://user:pass@api.lazu.ai/v1/messages?key=x":         "https://api.lazu.ai/v1/messages",
		"https://staging.internal.example/v1beta/models/g:stream": "(private gateway)/v1beta/models/g:stream",
	} {
		if got := publicOrigin(raw); got != want {
			t.Fatalf("publicOrigin(%q) = %q, want %q", raw, got, want)
		}
	}
}
