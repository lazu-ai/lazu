package common

import (
	"net/http"
	"strings"
)

// IsCodexResponsesRequest is a routing compatibility hint, not authentication.
// Never synthesize client identity for customer traffic: the upstream remains
// responsible for admission. Probes are the one exception, and only with an
// operator-configured identity (probe_identities in config.yaml).
func IsCodexResponsesRequest(r *http.Request) bool {
	if r == nil || r.URL == nil || r.Method != http.MethodPost || !strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/responses") {
		return false
	}
	ua := strings.ToLower(r.UserAgent())
	origin := strings.ToLower(r.Header.Get("originator"))
	return strings.HasPrefix(ua, "codex") && strings.HasPrefix(origin, "codex")
}

// ForwardCodexHeaders copies only this request's client metadata. Credentials,
// cookies and arbitrary headers must never be forwarded by this opt-in.
func ForwardCodexHeaders(src http.Header, dst http.Header) {
	for _, name := range []string{"User-Agent", "originator", "session-id", "thread-id", "session_id", "conversation_id", "x-client-request-id", "x-codex-beta-features", "x-codex-window-id", "x-codex-turn-metadata", "x-codex-turn-state"} {
		dst.Del(name)
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}
}
