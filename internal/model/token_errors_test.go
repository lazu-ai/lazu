package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
)

// The gateway tells a verified-but-inactive Key from an unknown credential
// with errors.Is. These relationships are the contract it depends on.
func TestTokenStateSentinelsKeepTheirRelationships(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		target error
		want   bool
	}{
		{"expired is inactive", ErrTokenExpired, ErrTokenAccessInactive, true},
		{"disabled is inactive", ErrTokenDisabled, ErrTokenAccessInactive, true},
		{"expired is not disabled", ErrTokenExpired, ErrTokenDisabled, false},
		{"exhausted is not inactive", ErrTokenBudgetExhausted, ErrTokenAccessInactive, false},
		{"invalid key is not inactive", ErrInvalidAPIKey, ErrTokenAccessInactive, false},
		{"invalid key is not exhausted", ErrInvalidAPIKey, ErrTokenBudgetExhausted, false},
		{"fresh invalid_api_key is not inactive", types.Err(types.ErrorCodeInvalidAPIKey), ErrTokenAccessInactive, false},
	} {
		if got := errors.Is(tc.err, tc.target); got != tc.want {
			t.Errorf("%s: errors.Is = %v, want %v", tc.name, got, tc.want)
		}
	}

	for err, code := range map[error]types.ErrorCode{
		ErrTokenExpired:         types.ErrorCodeTokenExpired,
		ErrTokenDisabled:        types.ErrorCodeTokenDisabled,
		ErrTokenBudgetExhausted: types.ErrorCodeKeyBudgetExceeded,
		ErrTokenAccessInactive:  types.ErrorCodeKeyInactive,
	} {
		if got := types.ErrCode(err); got != code {
			t.Errorf("ErrCode(%v) = %q, want %q", err, got, code)
		}
	}
	// A bare inactive Key is invalid_api_key on the wire.
	if w := types.RenderWire(types.ErrorCodeKeyInactive, nil, 0); w.Code != types.ErrorCodeInvalidAPIKey || w.Status != 401 {
		t.Fatalf("key_inactive renders as %+v", w)
	}
}

func TestValidateUserTokenRejectsMissingKeyAsInvalidAPIKey(t *testing.T) {
	if _, err := ValidateUserToken(""); !types.IsErrCode(err, types.ErrorCodeInvalidAPIKey) {
		t.Fatalf("empty key: got %v", err)
	}
	if err := ValidateTokenState(nil, ""); !types.IsErrCode(err, types.ErrorCodeInvalidAPIKey) {
		t.Fatalf("nil token: got %v", err)
	}
}

func TestValidateLegacyTokenUsesStateSentinels(t *testing.T) {
	// Exercise the actual embedded legacy path, including best-effort stored
	// state changes. A pure in-memory Token must not rely on the previous
	// test leaving Redis enabled to avoid those writes.
	db := setupTokenKeyStorageTestDB(t, "legacy_state_sentinels")
	withTokenKeyStorageGlobals(t, db)
	for i, tc := range []struct {
		name  string
		token Token
		want  error
	}{
		{"exhausted status", Token{Status: common.TokenStatusExhausted, ExpiredTime: -1}, ErrTokenBudgetExhausted},
		{"expired status", Token{Status: common.TokenStatusExpired, ExpiredTime: -1}, ErrTokenExpired},
		{"disabled status", Token{Status: common.TokenStatusDisabled, ExpiredTime: -1}, ErrTokenDisabled},
		{"past expiry", Token{Status: common.TokenStatusEnabled, ExpiredTime: 1, UnlimitedQuota: true}, ErrTokenExpired},
		{"empty allowance", Token{Status: common.TokenStatusEnabled, ExpiredTime: -1}, ErrTokenBudgetExhausted},
	} {
		token := tc.token
		token.Id = i + 1
		token.Key = fmt.Sprintf("example-legacy-sentinel-key-%d", i)
		token.KeyDigest = TokenKeyDigest(token.Key)
		if err := db.Create(&token).Error; err != nil {
			t.Fatal(err)
		}
		if err := ValidateTokenState(&token, "sk-test"); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
		var stored Token
		if err := db.Select("status").First(&stored, token.Id).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Status != token.Status {
			t.Errorf("%s: stored status=%d validated status=%d", tc.name, stored.Status, token.Status)
		}
		if tc.name == "past expiry" && stored.Status != common.TokenStatusExpired || tc.name == "empty allowance" && stored.Status != common.TokenStatusExhausted {
			t.Errorf("%s: legacy state transition was not persisted: %d", tc.name, stored.Status)
		}
	}
}
