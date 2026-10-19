package errmap

import (
	"net/http"
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
)

func TestMapDefault(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		want   types.ErrorCode
	}{
		{name: "rate limit body", status: http.StatusBadRequest, body: `{"error":"rate limit exceeded"}`, want: types.ErrorCodeUpstreamRateLimited},
		{name: "timeout body", status: http.StatusBadGateway, body: `deadline exceeded`, want: types.ErrorCodeUpstreamTimeout},
		{name: "invalid key body", status: http.StatusBadRequest, body: `invalid_api_key`, want: types.ErrorCodeChannelInvalidKey},
		{name: "prompt blocked body", status: http.StatusBadRequest, body: `content_filter blocked`, want: types.ErrorCodePromptBlocked},
		{name: "bad gateway", status: 502, body: ``, want: types.ErrorCodeUpstreamError},
		{name: "unavailable", status: 503, body: ``, want: types.ErrorCodeUpstreamOverloaded},
		{name: "explicit overload", status: 500, body: `{"error":"overloaded"}`, want: types.ErrorCodeUpstreamOverloaded},
		{name: "status 500", status: http.StatusInternalServerError, body: ``, want: types.ErrorCodeUpstreamError},
		{name: "context window", status: http.StatusBadRequest, body: `maximum context length is 8192 tokens`, want: types.ErrorCodeContextLengthExceeded},
		{name: "provider 402", status: http.StatusPaymentRequired, body: ``, want: types.ErrorCodeUpstreamBillingFailed},
		{name: "openai account out of credit", status: http.StatusTooManyRequests, body: `{"error":{"type":"insufficient_quota","message":"You exceeded your current quota"}}`, want: types.ErrorCodeUpstreamBillingFailed},
		{name: "anthropic credit", status: http.StatusBadRequest, body: `{"error":{"message":"Your credit balance is too low"}}`, want: types.ErrorCodeUpstreamBillingFailed},
		{name: "gemini per-minute quota stays a rate limit", status: http.StatusTooManyRequests, body: `Quota exceeded for metric`, want: types.ErrorCodeUpstreamRateLimited},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _ := MapDefault(tc.status, []byte(tc.body))
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
