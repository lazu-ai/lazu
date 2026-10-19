package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestMediaAdmissionErrorsPreserveRateAndCredentialSemantics(t *testing.T) {
	for _, test := range []struct {
		name   string
		cause  error
		code   types.ErrorCode
		status int
	}{
		{"rate", &ledger.Rejection{Code: "rate", RateDecision: "token_rpm_exceeded", RateLimit: 1, WindowMinutes: 1, RetryAfterSeconds: 12}, types.ErrorCodeTokenRateLimitExceeded, http.StatusTooManyRequests},
		{"expired credential", model.ErrInvalidAPIKey, types.ErrorCodeInvalidAPIKey, http.StatusUnauthorized},
		{"expired key", model.ErrTokenExpired, types.ErrorCodeTokenExpired, http.StatusUnauthorized},
		{"disabled key", model.ErrTokenDisabled, types.ErrorCodeTokenDisabled, http.StatusUnauthorized},
		{"stale generation", ledger.ErrStale, types.ErrorCodeGatewayTemporarilyUnavailable, http.StatusServiceUnavailable},
		{"rebuilding", ledger.ErrRebuilding, types.ErrorCodeGatewayOverloaded, http.StatusServiceUnavailable},
		{"admission paused", ledger.ErrAdmissionPaused, types.ErrorCodeGatewayTemporarilyUnavailable, http.StatusServiceUnavailable},
		{"missing private state", model.ErrMediaJobAdmissionMissing, types.ErrorCodeGatewayTemporarilyUnavailable, http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, w := newUserTestContext(http.MethodPost, "/v1/videos", "{}")
			cause := errors.Join(model.ErrMediaJobBillingAdmission, test.cause)
			err := mediaJobAdmissionError(c, cause)
			if err.GetErrorCode() != test.code || err.StatusCode != test.status {
				t.Fatalf("wrong media admission mapping: %+v", err)
			}
			if !errors.Is(err.Err, test.cause) {
				t.Fatal("media admission lost error cause")
			}
			writeControlPlaneAPIError(c, err)
			requireUserAPIError(t, w, test.status, types.PublicCode(test.code))
			if test.name == "rate" && w.Header().Get("Retry-After") != "12" {
				t.Fatal("rate rejection lost retry guidance")
			}
			if test.name == "admission paused" {
				if w.Header().Get("Retry-After") != "30" {
					t.Fatalf("admission pause retry guidance=%q want 30", w.Header().Get("Retry-After"))
				}
			} else if test.status == http.StatusServiceUnavailable && w.Header().Get("Retry-After") != "1" {
				t.Fatal("transient admission lost retry guidance")
			}
		})
	}
}
