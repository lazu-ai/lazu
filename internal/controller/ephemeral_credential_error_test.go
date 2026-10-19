package controller

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestEphemeralCredentialRemapsStateAndPreservesCause(t *testing.T) {
	for _, tc := range []struct {
		cause  error
		code   types.ErrorCode
		status int
	}{
		{model.ErrTokenExpired, types.ErrorCodeTokenExpired, http.StatusUnauthorized},
		{model.ErrTokenDisabled, types.ErrorCodeTokenDisabled, http.StatusUnauthorized},
		{model.ErrTokenAccessInactive, types.ErrorCodeTokenDisabled, http.StatusUnauthorized},
		{model.ErrTokenBudgetExhausted, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusTooManyRequests},
	} {
		t.Run(string(types.ErrCode(tc.cause)), func(t *testing.T) {
			cause := fmt.Errorf("credential issuance: %w", tc.cause)
			err := ephemeralCredentialIssueError(cause)
			if types.ErrCode(err) != tc.code || !errors.Is(err, cause) || !errors.Is(err, tc.cause) {
				t.Fatalf("wrong mapping or lost cause: %v", err)
			}
			c, w := newUserTestContext(http.MethodPost, "/api/credentials/ephemeral", "{}")
			writeControlPlaneAPIError(c, err)
			requireUserAPIError(t, w, tc.status, tc.code)
		})
	}
}
