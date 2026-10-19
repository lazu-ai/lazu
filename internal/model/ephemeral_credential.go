package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"

	"gorm.io/gorm"
)

const (
	EphemeralCredentialPrefix = "lazu-ephem-"

	EphemeralCredentialScopeRelay = "relay"

	EphemeralCredentialAuditIssue   = "issue"
	EphemeralCredentialAuditUse     = "use"
	EphemeralCredentialAuditDenied  = "denied"
	EphemeralCredentialAuditExpired = "expired"

	defaultEphemeralCredentialTTLSeconds = 3600
	maxEphemeralCredentialTTLSeconds     = 24 * 3600
)

type EphemeralCredential struct {
	ID               uint        `json:"id" gorm:"primaryKey"`
	PublicID         string      `json:"public_id" gorm:"type:varchar(64);uniqueIndex"`
	CredentialDigest string      `json:"-" gorm:"size:64;uniqueIndex;not null"`
	UserID           int         `json:"user_id" gorm:"index;not null"`
	TokenID          int         `json:"token_id" gorm:"index;not null"`
	Scope            string      `json:"scope" gorm:"size:32;index;not null;default:'relay'"`
	SessionID        string      `json:"session_id" gorm:"size:64;index"`
	AgentID          string      `json:"agent_id" gorm:"size:64;index"`
	Models           StringSlice `json:"models" gorm:"type:json"`
	Providers        StringSlice `json:"providers" gorm:"type:json"`
	ExpiresAt        int64       `json:"expires_at" gorm:"bigint;index;not null"`
	RevokedAt        int64       `json:"revoked_at" gorm:"bigint;index"`
	LastUsedAt       int64       `json:"last_used_at" gorm:"bigint"`
	UseCount         int         `json:"use_count" gorm:"default:0"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

type EphemeralCredentialAudit struct {
	ID                 uint   `json:"id" gorm:"primaryKey"`
	CredentialID       uint   `json:"credential_id" gorm:"index"`
	CredentialPublicID string `json:"credential_public_id" gorm:"size:64;index"`
	EventType          string `json:"event_type" gorm:"size:32;index"`
	UserID             int    `json:"user_id" gorm:"index"`
	TokenID            int    `json:"token_id" gorm:"index"`
	SessionID          string `json:"session_id" gorm:"size:64;index"`
	AgentID            string `json:"agent_id" gorm:"size:64;index"`
	RequestID          string `json:"request_id" gorm:"size:64;index"`
	ModelName          string `json:"model_name" gorm:"size:191;index"`
	Provider           string `json:"provider" gorm:"size:128;index"`
	Reason             string `json:"reason" gorm:"type:text"`
	CreatedAt          int64  `json:"created_at" gorm:"bigint;index"`
}

type IssueEphemeralCredentialInput struct {
	UserID     int
	TokenID    int
	Scope      string
	SessionID  string
	AgentID    string
	Models     []string
	Providers  []string
	TTLSeconds int
	Now        time.Time
}

type EphemeralCredentialIssueResult struct {
	Credential string               `json:"credential"`
	Record     *EphemeralCredential `json:"record"`
}

func IsEphemeralCredentialKey(key string) bool {
	return strings.HasPrefix(strings.TrimSpace(key), EphemeralCredentialPrefix)
}

func (credential *EphemeralCredential) BeforeCreate(*gorm.DB) error {
	if credential.PublicID == "" {
		credential.PublicID = common.GeneratePublicID("eph")
	}
	return nil
}

func IssueEphemeralCredential(input IssueEphemeralCredentialInput) (*EphemeralCredentialIssueResult, error) {
	if CoreDB() == nil {
		return nil, errors.New("database is not initialized")
	}
	input = normalizeIssueEphemeralCredentialInput(input)
	if input.UserID <= 0 || input.TokenID <= 0 {
		return nil, errors.New("user_id and token_id are required")
	}
	token, err := GetTokenByIds(input.TokenID, input.UserID)
	if err != nil {
		return nil, err
	}
	if err := ValidateTokenState(token, fmt.Sprintf("token:%d", input.TokenID)); err != nil {
		return nil, err
	}
	secret, err := common.GenerateRandomCharsKey(48)
	if err != nil {
		return nil, err
	}
	rawCredential := EphemeralCredentialPrefix + secret
	row := &EphemeralCredential{
		CredentialDigest: common.GenerateHMAC(rawCredential),
		UserID:           input.UserID,
		TokenID:          input.TokenID,
		Scope:            input.Scope,
		SessionID:        truncateEphemeralCredentialString(input.SessionID, 64),
		AgentID:          truncateEphemeralCredentialString(input.AgentID, 64),
		Models:           normalizeEphemeralCredentialList(input.Models),
		Providers:        normalizeEphemeralCredentialList(input.Providers),
		ExpiresAt:        input.Now.Unix() + int64(input.TTLSeconds),
	}
	if err := CoreDB().Create(row).Error; err != nil {
		return nil, err
	}
	RecordEphemeralCredentialAudit(EphemeralCredentialAudit{
		CredentialID:       row.ID,
		CredentialPublicID: row.PublicID,
		EventType:          EphemeralCredentialAuditIssue,
		UserID:             row.UserID,
		TokenID:            row.TokenID,
		SessionID:          row.SessionID,
		AgentID:            row.AgentID,
		Reason:             fmt.Sprintf("ttl_seconds=%d", input.TTLSeconds),
		CreatedAt:          input.Now.Unix(),
	})
	return &EphemeralCredentialIssueResult{Credential: rawCredential, Record: row}, nil
}

func ValidateEphemeralCredential(rawCredential string) (*EphemeralCredential, *Token, error) {
	return ValidateEphemeralCredentialAt(rawCredential, time.Now().UTC())
}

func ValidateEphemeralCredentialAt(rawCredential string, now time.Time) (*EphemeralCredential, *Token, error) {
	rawCredential = strings.TrimSpace(rawCredential)
	if !IsEphemeralCredentialKey(rawCredential) {
		return nil, nil, errors.New("invalid ephemeral credential")
	}
	var row EphemeralCredential
	if err := CoreDB().Where("credential_digest = ?", common.GenerateHMAC(rawCredential)).First(&row).Error; err != nil {
		return nil, nil, IdentityAuthorityQueryError(err)
	}
	if row.RevokedAt > 0 {
		return &row, nil, errors.New("ephemeral credential revoked")
	}
	if row.ExpiresAt <= now.Unix() {
		RecordEphemeralCredentialAudit(EphemeralCredentialAudit{
			CredentialID:       row.ID,
			CredentialPublicID: row.PublicID,
			EventType:          EphemeralCredentialAuditExpired,
			UserID:             row.UserID,
			TokenID:            row.TokenID,
			SessionID:          row.SessionID,
			AgentID:            row.AgentID,
			Reason:             "credential expired",
			CreatedAt:          now.Unix(),
		})
		return &row, nil, errors.New("ephemeral credential expired")
	}
	token, err := GetTokenByIds(row.TokenID, row.UserID)
	if err != nil {
		return &row, nil, IdentityAuthorityQueryError(err)
	}
	if err := ValidateTokenState(token, row.PublicID); err != nil {
		return &row, token, err
	}
	return &row, token, nil
}

func ValidateEphemeralCredentialRequest(credential *EphemeralCredential, modelName string, provider string, sessionID string) error {
	if credential == nil {
		return nil
	}
	if credential.Scope != "" && credential.Scope != EphemeralCredentialScopeRelay {
		return fmt.Errorf("ephemeral credential scope %q cannot call relay", credential.Scope)
	}
	if credential.SessionID != "" && strings.TrimSpace(sessionID) != credential.SessionID {
		return fmt.Errorf("ephemeral credential is bound to a different session")
	}
	if len(credential.Models) > 0 && !ephemeralCredentialListContainsModel(credential.Models, modelName) {
		return fmt.Errorf("ephemeral credential cannot call model %q", modelName)
	}
	provider = normalizeEphemeralCredentialText(provider)
	if len(credential.Providers) > 0 && (provider == "" || !slices.Contains(credential.Providers, provider)) {
		return fmt.Errorf("ephemeral credential cannot call provider %q", provider)
	}
	return nil
}

func MarkEphemeralCredentialUsed(id uint, now time.Time) {
	if CoreDB() == nil || id == 0 {
		return
	}
	if err := CoreDB().Model(&EphemeralCredential{}).Where("id = ?", id).Updates(map[string]any{
		"last_used_at": now.Unix(),
		"use_count":    gorm.Expr("use_count + 1"),
	}).Error; err != nil {
		common.SysLog("failed to mark ephemeral credential used: " + err.Error())
	}
}

func RecordEphemeralCredentialAudit(row EphemeralCredentialAudit) {
	if CoreDB() == nil {
		return
	}
	if row.CreatedAt == 0 {
		row.CreatedAt = time.Now().UTC().Unix()
	}
	row.EventType = normalizeEphemeralCredentialText(row.EventType)
	row.SessionID = truncateEphemeralCredentialString(row.SessionID, 64)
	row.AgentID = truncateEphemeralCredentialString(row.AgentID, 64)
	row.RequestID = truncateEphemeralCredentialString(row.RequestID, 64)
	row.ModelName = truncateEphemeralCredentialString(row.ModelName, 191)
	row.Provider = truncateEphemeralCredentialString(normalizeEphemeralCredentialText(row.Provider), 128)
	if err := CoreDB().Create(&row).Error; err != nil {
		common.SysLog("failed to record ephemeral credential audit: " + err.Error())
	}
}

func normalizeIssueEphemeralCredentialInput(input IssueEphemeralCredentialInput) IssueEphemeralCredentialInput {
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	} else {
		input.Now = input.Now.UTC()
	}
	input.Scope = normalizeEphemeralCredentialText(input.Scope)
	if input.Scope == "" {
		input.Scope = EphemeralCredentialScopeRelay
	}
	if input.TTLSeconds <= 0 {
		input.TTLSeconds = defaultEphemeralCredentialTTLSeconds
	}
	if input.TTLSeconds > maxEphemeralCredentialTTLSeconds {
		input.TTLSeconds = maxEphemeralCredentialTTLSeconds
	}
	return input
}

func normalizeEphemeralCredentialList(values []string) StringSlice {
	result := make(StringSlice, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = normalizeEphemeralCredentialText(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func ephemeralCredentialListContainsModel(values StringSlice, modelName string) bool {
	modelName = normalizeEphemeralCredentialText(modelName)
	normalizedModelName := NormalizePricingModelName(modelName)
	for _, value := range values {
		if value == modelName || value == normalizedModelName {
			return true
		}
		if strings.HasSuffix(value, "*") && strings.HasPrefix(modelName, strings.TrimSuffix(value, "*")) {
			return true
		}
	}
	return false
}

func normalizeEphemeralCredentialText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func truncateEphemeralCredentialString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
