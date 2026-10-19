package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/types"
)

// describeResetProblem renders a validation error as "code/rule" for
// password rules and "code/field" otherwise.
func describeResetProblem(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *types.APIError
	if !errors.As(err, &apiErr) {
		return err.Error()
	}
	if rule, ok := apiErr.Details["rule"].(string); ok {
		return string(apiErr.Code) + "/" + rule
	}
	field, _ := apiErr.Details["field"].(string)
	return string(apiErr.Code) + "/" + field
}

func passwordResetVersion(value int) *int {
	return &value
}

func TestValidatePasswordResetRequest(t *testing.T) {
	tests := []struct {
		name    string
		request PasswordResetRequest
		want    string
	}{
		{
			name: "valid",
			request: PasswordResetRequest{
				Email:                "user@example.com",
				UserID:               42,
				IssuedSessionVersion: passwordResetVersion(0),
				Token:                "reset-token",
				Password:             "Lazu#2026",
			},
		},
		{
			name: "valid Unicode code points",
			request: PasswordResetRequest{
				Email:                "user@example.com",
				UserID:               42,
				IssuedSessionVersion: passwordResetVersion(0),
				Token:                "reset-token",
				Password:             strings.Repeat("😀", 11),
			},
		},
		{
			name: "missing email",
			request: PasswordResetRequest{
				UserID:               42,
				IssuedSessionVersion: passwordResetVersion(0),
				Token:                "reset-token",
				Password:             "Lazu#2026",
			},
			want: "invalid_request/email",
		},
		{
			name: "missing password",
			request: PasswordResetRequest{
				Email:                "user@example.com",
				UserID:               42,
				IssuedSessionVersion: passwordResetVersion(0),
				Token:                "reset-token",
			},
			want: "invalid_request/password",
		},
		{
			name: "missing user id",
			request: PasswordResetRequest{
				Email:                "user@example.com",
				IssuedSessionVersion: passwordResetVersion(0),
				Token:                "reset-token",
				Password:             "Lazu#2026",
			},
			want: "invalid_request/user_id",
		},
		{
			name: "missing session version",
			request: PasswordResetRequest{
				Email:    "user@example.com",
				UserID:   42,
				Token:    "reset-token",
				Password: "Lazu#2026",
			},
			want: "invalid_request/session_version",
		},
		{
			name: "negative session version",
			request: PasswordResetRequest{
				Email:                "user@example.com",
				UserID:               42,
				IssuedSessionVersion: passwordResetVersion(-1),
				Token:                "reset-token",
				Password:             "Lazu#2026",
			},
			want: "invalid_request/session_version",
		},
		{
			name: "password too short",
			request: PasswordResetRequest{
				Email:                "user@example.com",
				UserID:               42,
				IssuedSessionVersion: passwordResetVersion(0),
				Token:                "reset-token",
				Password:             "short",
			},
			want: "password_invalid/min_length",
		},
		{
			name: "password too many characters",
			request: PasswordResetRequest{
				Email:                "user@example.com",
				UserID:               42,
				IssuedSessionVersion: passwordResetVersion(0),
				Token:                "reset-token",
				Password:             strings.Repeat("a", 65),
			},
			want: "password_invalid/max_length",
		},
		{
			name: "password exceeds bcrypt byte limit",
			request: PasswordResetRequest{
				Email:                "user@example.com",
				UserID:               42,
				IssuedSessionVersion: passwordResetVersion(0),
				Token:                "reset-token",
				Password:             strings.Repeat("界", 65) + "ab",
			},
			want: "password_invalid/max_length",
		},
		{
			name: "password reaches bcrypt byte limit before character limit",
			request: PasswordResetRequest{
				Email:                "user@example.com",
				UserID:               42,
				IssuedSessionVersion: passwordResetVersion(0),
				Token:                "reset-token",
				Password:             strings.Repeat("😀", 19),
			},
			want: "password_invalid/max_bytes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := describeResetProblem(validatePasswordResetRequest(test.request)); got != test.want {
				t.Fatalf("validatePasswordResetRequest() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResetPasswordLegacyRequiresClientUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/reset", http.NoBody)

	ResetPassword(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
			Lazu string `json:"lazu"`
		} `json:"error"`
		Data    *string `json:"data"`
		Success bool    `json:"success"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Success {
		t.Fatalf("expected legacy reset to fail safely, got %#v", response)
	}
	if response.Error.Code != string(types.ErrorCodePasswordResetClientUpgradeRequired) || response.Error.Lazu != "LZ-7415" {
		t.Fatalf("expected upgrade code, got %#v", response.Error)
	}
	if response.Data != nil {
		t.Fatalf("legacy response must not contain plaintext password data: %#v", response)
	}
}
