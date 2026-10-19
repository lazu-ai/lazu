package model

import (
	"errors"
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

func TestIdentityAuthorityQueryErrorPreservesCauseAndValidation(t *testing.T) {
	cause := errors.New("driver outage fixture")
	unavailable := IdentityAuthorityQueryError(cause)
	if !errors.Is(unavailable, ErrIdentityAuthorityUnavailable) || !errors.Is(unavailable, cause) {
		t.Fatalf("SQL cause/classification lost: %v", unavailable)
	}
	if IdentityAuthorityQueryError(unavailable) != unavailable {
		t.Fatal("already classified query error was rewrapped")
	}
	if err := IdentityAuthorityQueryError(gorm.ErrRecordNotFound); !errors.Is(err, ErrInvalidAPIKey) || errors.Is(err, ErrIdentityAuthorityUnavailable) {
		t.Fatalf("confirmed missing credential became unavailable: %v", err)
	}
	validation := types.Err(types.ErrorCodeInvalidRequest)
	if IdentityAuthorityQueryError(validation) != validation || IdentityAuthorityQueryError(nil) != nil {
		t.Fatal("validation/no-error result changed")
	}
}
