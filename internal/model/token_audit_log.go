package model

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

const (
	TokenAuditActionCreate = "create"
	TokenAuditActionUpdate = "update"
	TokenAuditActionDelete = "delete"
)

type TokenAuditLog struct {
	ID            uint      `gorm:"primaryKey"`
	TokenID       int       `gorm:"index"`
	TokenPublicID string    `gorm:"size:64;index"`
	UserID        int       `gorm:"index"`
	Username      string    `gorm:"size:128"`
	Action        string    `gorm:"size:32;index"`
	BeforeState   string    `gorm:"type:json"`
	AfterState    string    `gorm:"type:json"`
	CreatedAt     time.Time `gorm:"index"`
}

func WriteTokenAuditLog(c *gin.Context, action string, before *Token, after *Token) {
	if CoreDB() == nil {
		return
	}
	token := after
	if token == nil {
		token = before
	}
	if token == nil {
		return
	}
	entry := &TokenAuditLog{
		TokenID:       token.Id,
		TokenPublicID: token.PublicID,
		UserID:        token.UserId,
		Action:        action,
		BeforeState:   tokenAuditStateJSON(before),
		AfterState:    tokenAuditStateJSON(after),
	}
	if c != nil {
		entry.Username = c.GetString("username")
		if operatorID := c.GetInt("id"); operatorID > 0 {
			entry.UserID = operatorID
		}
	}
	if err := CoreDB().Create(entry).Error; err != nil {
		common.SysLog("failed to write token audit log: " + err.Error())
	}
}

func tokenAuditStateJSON(token *Token) string {
	if token == nil {
		return "{}"
	}
	state := map[string]any{
		"id":                      token.PublicID,
		"name":                    token.Name,
		"product":                 token.Product,
		"metadata":                token.Metadata,
		"status":                  token.Status,
		"expired_time":            token.ExpiredTime,
		"unlimited_quota":         token.UnlimitedQuota,
		"rate_limits_enabled":     token.RateLimitsEnabled,
		"rpm_limit":               token.RPMLimit,
		"tpm_limit":               token.TPMLimit,
		"model_limits_enabled":    token.ModelLimitsEnabled,
		"model_limits":            token.ModelLimits,
		"ip_whitelist_enabled":    token.IPWhitelistEnabled,
		"ip_blacklist_enabled":    token.IPBlacklistEnabled,
		"vendor_limits_enabled":   token.VendorLimitsEnabled,
		"vendor_limits":           token.VendorLimits,
		"vendor_groups":           token.VendorGroups.Normalize(),
		"model_lanes":             token.ModelLanes.Normalize(),
		"search_limits_enabled":   token.SearchLimitsEnabled,
		"search_providers":        token.SearchProviders,
		"search_domain_allowlist": token.SearchDomainAllowlist,
		"group":                   token.Group,
	}
	raw, err := common.Marshal(state)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
