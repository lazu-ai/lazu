package errmap

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
)

func TestMapAnthropic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status int
		body   string
		want   types.ErrorCode
	}{
		{529, `{"type":"overloaded_error"}`, types.ErrorCodeUpstreamOverloaded},
		{401, `{"error":{"type":"invalid_api_key"}}`, types.ErrorCodeChannelInvalidKey},
		{429, `{"type":"rate_limit_error"}`, types.ErrorCodeUpstreamRateLimited},
		{400, `{"error":{"message":"prompt is too long"}}`, types.ErrorCodeContextLengthExceeded},
	}
	for _, tc := range tests {
		got, _ := MapAnthropic(tc.status, []byte(tc.body))
		if got != tc.want {
			t.Fatalf("status=%d body=%s got %q want %q", tc.status, tc.body, got, tc.want)
		}
	}
}
