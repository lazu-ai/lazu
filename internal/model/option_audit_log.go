package model

import (
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"

	"github.com/gin-gonic/gin"
)

type OptionAuditLog struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    int       `gorm:"index"`
	Username  string    `gorm:"size:128"`
	Key       string    `gorm:"index;size:191"`
	OldValue  string    `gorm:"type:text"`
	NewValue  string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"index"`
}

func maskOptionAuditValue(key string, value string) string {
	lowerKey := strings.ToLower(key)
	if strings.Contains(lowerKey, "secret") ||
		strings.Contains(lowerKey, "token") ||
		strings.Contains(lowerKey, "password") ||
		strings.HasSuffix(lowerKey, "key") {
		if value == "" {
			return ""
		}
		return "***changed***"
	}
	return value
}

func WriteOptionAuditLog(c *gin.Context, key string, oldValue string, newValue string) {
	if CoreDB() == nil {
		return
	}
	entry := &OptionAuditLog{
		UserID:   c.GetInt("id"),
		Username: c.GetString("username"),
		Key:      key,
		OldValue: maskOptionAuditValue(key, oldValue),
		NewValue: maskOptionAuditValue(key, newValue),
	}
	if err := CoreDB().Create(entry).Error; err != nil {
		common.SysLog("failed to write option audit log: " + err.Error())
	}
}
