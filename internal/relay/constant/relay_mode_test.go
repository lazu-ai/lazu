package constant

import "testing"

func TestPath2RelayMode_TransportCriticalPaths(t *testing.T) {
	tests := []struct {
		path string
		want int
	}{
		{path: "/v1/chat/completions", want: RelayModeChatCompletions},
		{path: "/pg/chat/completions", want: RelayModeChatCompletions},
		{path: "/v1/responses", want: RelayModeResponses},
		{path: "/v1/realtime", want: RelayModeRealtime},
		{path: "/v1/messages", want: RelayModeUnknown},
	}

	for _, tc := range tests {
		if got := Path2RelayMode(tc.path); got != tc.want {
			t.Fatalf("path %s: got mode=%d want=%d", tc.path, got, tc.want)
		}
	}
}

func TestPath2RelayMode_RealtimeIsNotOverMatched(t *testing.T) {
	paths := []string{
		"/v1/realtimex",
		"/v1/chat/realtime",
	}
	for _, path := range paths {
		if got := Path2RelayMode(path); got == RelayModeRealtime {
			t.Fatalf("path %s should not be classified as realtime", path)
		}
	}
}
