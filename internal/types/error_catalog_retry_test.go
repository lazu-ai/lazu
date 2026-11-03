package types

import (
	"net/http"
	"testing"
)

// Wallet and budget rows must not read as rate limits: clients retry a 429
// for minutes before they show the message. The Messages wire answers 402
// billing_error as Anthropic does; every wire asks clients not to retry.
func TestWalletAndBudgetRowsAskClientsNotToRetry(t *testing.T) {
	for _, code := range []ErrorCode{
		ErrorCodeInsufficientQuota,
		ErrorCodeKeyBudgetExceeded,
		ErrorCodePreConsumeTokenQuotaFailed,
		ErrorCodeMemberBudgetExceeded,
		ErrorCodeProjectBudgetExceeded,
		ErrorCodePlatformBudgetExceeded,
	} {
		t.Run(string(code), func(t *testing.T) {
			// Call sites carry 429 at runtime; the written status must not
			// depend on that.
			w := RenderWire(code, nil, http.StatusTooManyRequests)
			if w.Status != http.StatusTooManyRequests {
				t.Fatalf("OpenAI-shaped status = %d, want 429", w.Status)
			}
			if w.MessagesStatus != http.StatusPaymentRequired {
				t.Fatalf("Messages status = %d, want 402", w.MessagesStatus)
			}
			if got := ClaudeErrorType(w.Type, w.MessagesStatus); got != "billing_error" {
				t.Fatalf("Messages type = %q, want billing_error", got)
			}
			if !w.NoClientRetry || !NoClientRetry(code) {
				t.Fatal("row does not ask clients to stop retrying")
			}
			if got := MessagesWireStatus(code, w.Status); got != http.StatusPaymentRequired {
				t.Fatalf("MessagesWireStatus = %d, want 402", got)
			}
			// The server's own reasoning (gateway retry, breaker) is unchanged.
			if StatusForCode(code, 0) == http.StatusPaymentRequired {
				t.Fatal("server-side status moved to 402")
			}
		})
	}
}

func TestRateLimitsStayRetryableOnEveryWire(t *testing.T) {
	w := RenderWire(ErrorCodeRequestRateLimitExceeded, nil, http.StatusTooManyRequests)
	if w.Status != http.StatusTooManyRequests || w.MessagesStatus != http.StatusTooManyRequests {
		t.Fatalf("rate limit statuses = %d / %d, want 429 / 429", w.Status, w.MessagesStatus)
	}
	if w.NoClientRetry || NoClientRetry(ErrorCodeRequestRateLimitExceeded) {
		t.Fatal("a rate limit must stay retryable")
	}
	if got := MessagesWireStatus(ErrorCodeRequestRateLimitExceeded, http.StatusTooManyRequests); got != http.StatusTooManyRequests {
		t.Fatalf("MessagesWireStatus = %d, want 429", got)
	}
	// A runtime status passed through for a row without MessagesStatus is
	// kept on the Messages wire too.
	if w := RenderWire(ErrorCodeModelNotFound, nil, http.StatusNotFound); w.MessagesStatus != http.StatusNotFound {
		t.Fatalf("pass-through Messages status = %d, want 404", w.MessagesStatus)
	}
}
