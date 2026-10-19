package controller

import (
	"errors"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

type issueEphemeralCredentialRequest struct {
	AgentID    string   `json:"agent_id"`
	Models     []string `json:"models"`
	Providers  []string `json:"providers"`
	Scope      string   `json:"scope"`
	SessionID  string   `json:"session_id"`
	TTLSeconds int      `json:"ttl_seconds"`
}

type issueEphemeralCredentialResponse struct {
	AgentID    string   `json:"agent_id"`
	Credential string   `json:"credential"`
	ExpiresAt  int64    `json:"expires_at"`
	Models     []string `json:"models"`
	Providers  []string `json:"providers"`
	PublicID   string   `json:"id"`
	Scope      string   `json:"scope"`
	SessionID  string   `json:"session_id"`
	TokenID    int      `json:"token_id"`
	UserID     int      `json:"user_id"`
}

func IssueEphemeralCredential(c *gin.Context) {
	if credential, ok := common.GetContextKeyType[*model.EphemeralCredential](c, constant.ContextKeyEphemeralCredential); ok && credential != nil {
		writeControlPlaneCatalogError(c, types.ErrorCodeEphemeralCredentialDenied, types.D{"reason": "ephemeral credentials cannot issue new credentials"})
		return
	}
	var payload issueEphemeralCredentialRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		writeControlPlaneAPIError(c, types.WrapErr(err, types.ErrorCodeInvalidRequest, types.D{"field": "body", "reason": "the body is not valid JSON"}))
		return
	}
	userID := c.GetInt("id")
	tokenID := c.GetInt("token_id")
	result, err := model.IssueEphemeralCredential(model.IssueEphemeralCredentialInput{
		UserID:     userID,
		TokenID:    tokenID,
		Scope:      payload.Scope,
		SessionID:  payload.SessionID,
		AgentID:    firstNonEmptyCredentialValue(payload.AgentID, c.GetHeader("X-Agent-Id"), c.GetHeader("X-Lazu-Agent-Id")),
		Models:     payload.Models,
		Providers:  payload.Providers,
		TTLSeconds: payload.TTLSeconds,
	})
	if err != nil {
		writeControlPlaneAPIError(c, ephemeralCredentialIssueError(err))
		return
	}
	record := result.Record
	common.ApiSuccess(c, issueEphemeralCredentialResponse{
		AgentID:    record.AgentID,
		Credential: result.Credential,
		ExpiresAt:  record.ExpiresAt,
		Models:     []string(record.Models),
		Providers:  []string(record.Providers),
		PublicID:   record.PublicID,
		Scope:      record.Scope,
		SessionID:  record.SessionID,
		TokenID:    record.TokenID,
		UserID:     record.UserID,
	})
}

// ephemeralCredentialIssueError names why a Key could not issue a credential.
// TokenAuth already rejects inactive Keys, so these are races with a Key that
// changed state mid-request.
func ephemeralCredentialIssueError(err error) error {
	switch {
	case errors.Is(err, model.ErrTokenExpired):
		return &types.APIError{Code: types.ErrorCodeTokenExpired, Cause: err}
	case errors.Is(err, model.ErrTokenDisabled), errors.Is(err, model.ErrTokenAccessInactive):
		return &types.APIError{Code: types.ErrorCodeTokenDisabled, Cause: err}
	case errors.Is(err, model.ErrTokenBudgetExhausted):
		return &types.APIError{Code: types.ErrorCodePreConsumeTokenQuotaFailed, Cause: err}
	}
	return types.WrapErr(err, types.ErrorCodeInternalError)
}

func firstNonEmptyCredentialValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
