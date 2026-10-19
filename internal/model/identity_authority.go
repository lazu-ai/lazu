package model

import (
	"errors"
	"fmt"

	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// ErrIdentityAuthorityUnavailable means an authoritative credential query
// failed before the gateway could confirm whether a credential is valid.
// It must not be reported as a confirmed invalid credential.
var ErrIdentityAuthorityUnavailable = errors.New("identity authority unavailable")

// IdentityAuthorityQueryError preserves the underlying SQL error for internal
// classification. Call only for credential authority queries: a confirmed
// missing row remains invalid, while a failed query must fail closed with 503.
func IdentityAuthorityQueryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrInvalidAPIKey
	}
	var validation *types.LazuError
	var apiValidation *types.APIError
	if errors.As(err, &validation) || errors.As(err, &apiValidation) || errors.Is(err, ErrIdentityAuthorityUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrIdentityAuthorityUnavailable, err)
}
