package common

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodexRequestCompatibility(t *testing.T) {
	for _, tc := range []struct {
		ua, origin, path string
		want             bool
	}{
		{"Codex Desktop/0.147.0", "Codex Desktop", "/v1/responses", true},
		{"codex_cli_rs/0.147.0", "codex_cli_rs", "/v1/responses", true},
		{"OpenAI/Python", "", "/v1/responses", false},
		{"Codex Desktop/0.147.0", "", "/v1/responses", false},
		{"Codex Desktop/0.147.0", "Codex Desktop", "/v1/chat/completions", false},
	} {
		r := httptest.NewRequest("POST", tc.path, nil)
		r.Header.Set("User-Agent", tc.ua)
		r.Header.Set("originator", tc.origin)
		if IsCodexResponsesRequest(r) != tc.want {
			t.Errorf("unexpected compatibility for %+v", tc)
		}
	}
}
func TestCodexHeadersDoNotLeakCredentialsOrReuseSessions(t *testing.T) {
	dst := http.Header{}
	for _, session := range []string{"first", "second"} {
		src := http.Header{}
		src.Set("session-id", session)
		src.Set("Authorization", "secret")
		src.Set("Cookie", "secret")
		src.Set("x-codex-api-key", "secret")
		ForwardCodexHeaders(src, dst)
		if dst.Get("session-id") != session || dst.Get("Authorization") != "" || dst.Get("Cookie") != "" || dst.Get("x-codex-api-key") != "" {
			t.Fatal("metadata isolation failed")
		}
	}
	ForwardCodexHeaders(http.Header{}, dst)
	if dst.Get("session-id") != "" {
		t.Fatal("stale session")
	}
}
