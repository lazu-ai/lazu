package types

import "testing"

func TestChannelHealthVerdict(t *testing.T) {
	tests := []struct {
		name              string
		status            int
		code              ErrorCode
		providerAttempted bool
		eligible          bool
		failed            bool
	}{
		{name: "success", status: 200, providerAttempted: true, eligible: true},
		{name: "websocket upgrade", status: 101, providerAttempted: true, eligible: true},
		{name: "post-header stream failure", status: 200, code: ErrorCodeUpstreamNetworkError, providerAttempted: true, eligible: true, failed: true},
		{name: "post-header gateway conversion failure", status: 200, code: ErrorCodeConvertRequestFailed, providerAttempted: true},
		{name: "client canceled stream", status: 200, code: ErrorCodeClientCanceled, providerAttempted: true},
		{name: "content policy after headers", status: 200, code: ErrorCodePromptBlocked, providerAttempted: true},
		{name: "gateway rate limit", status: 429, code: ErrorCodeTokenRateLimitExceeded, providerAttempted: false},
		{name: "upstream rate limit", status: 429, code: ErrorCodeUpstreamRateLimited, providerAttempted: true, eligible: true, failed: true},
		{name: "invalid upstream credentials", status: 401, code: ErrorCodeChannelInvalidKey, providerAttempted: true, eligible: true, failed: true},
		{name: "ordinary caller fault", status: 400, code: ErrorCodeInvalidRequest, providerAttempted: true},
		{name: "upstream account out of balance", status: 402, code: ErrorCodeUpstreamError, providerAttempted: true, eligible: true, failed: true},
		{name: "caller out of balance before any provider", status: 402, code: ErrorCodeInsufficientQuota, providerAttempted: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eligible, failed := ChannelHealthVerdict(test.status, test.code, test.providerAttempted)
			if eligible != test.eligible || failed != test.failed {
				t.Fatalf("verdict = (%t,%t), want (%t,%t)", eligible, failed, test.eligible, test.failed)
			}
		})
	}
}
