package model

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

type AdminAuditLog struct {
	ID            uint      `json:"id" gorm:"primaryKey"`
	PublicID      string    `json:"public_id" gorm:"type:varchar(64);uniqueIndex"`
	ActorID       int       `json:"actor_id" gorm:"index"`
	ActorUsername string    `json:"actor_username" gorm:"size:128;index"`
	ActorRole     int       `json:"actor_role"`
	Action        string    `json:"action" gorm:"size:96;index"`
	TargetType    string    `json:"target_type" gorm:"size:64;index"`
	TargetID      string    `json:"target_id" gorm:"size:128;index"`
	TargetName    string    `json:"target_name" gorm:"size:191"`
	DiffSummary   string    `json:"diff_summary" gorm:"type:json"`
	SourceIP      string    `json:"source_ip" gorm:"size:64"`
	UserAgent     string    `json:"user_agent" gorm:"type:text"`
	CreatedAt     time.Time `json:"created_at" gorm:"index"`
}

func (AdminAuditLog) TableName() string {
	return "admin_audit_logs"
}

func (log *AdminAuditLog) BeforeCreate(tx *gorm.DB) error {
	if log.PublicID == "" {
		log.PublicID = common.GeneratePublicID(common.PublicAuditIDPrefix)
	}
	return nil
}

type AdminAuditQuery struct {
	Action     string
	TargetType string
	ActorID    int
	Limit      int
	Offset     int
}

func WriteAdminAuditLog(c *gin.Context, action string, targetType string, targetID string, targetName string, before any, after any) {
	if CoreDB() == nil {
		return
	}
	entry := &AdminAuditLog{
		Action:      strings.TrimSpace(action),
		TargetType:  strings.TrimSpace(targetType),
		TargetID:    strings.TrimSpace(targetID),
		TargetName:  strings.TrimSpace(targetName),
		DiffSummary: AdminAuditDiffJSON(before, after),
	}
	if c != nil {
		entry.ActorID = c.GetInt("id")
		entry.ActorUsername = c.GetString("username")
		entry.ActorRole = c.GetInt("role")
		entry.SourceIP = common.GetRealClientIP(c)
		if c.Request != nil {
			entry.UserAgent = c.Request.UserAgent()
		}
	}
	if err := CoreDB().Create(entry).Error; err != nil {
		common.SysLog("failed to write admin audit log: " + err.Error())
	}
}

func AdminAuditDiffJSON(before any, after any) string {
	beforeMap := auditStateMap(before)
	afterMap := auditStateMap(after)
	diff := map[string]any{}
	keys := map[string]struct{}{}
	for key := range beforeMap {
		keys[key] = struct{}{}
	}
	for key := range afterMap {
		keys[key] = struct{}{}
	}
	sortedKeys := make([]string, 0, len(keys))
	for key := range keys {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)
	for _, key := range sortedKeys {
		beforeValue, beforeOK := beforeMap[key]
		afterValue, afterOK := afterMap[key]
		if beforeOK && afterOK && reflect.DeepEqual(beforeValue, afterValue) {
			continue
		}
		diff[key] = map[string]any{
			"before": maskAuditValue(key, beforeValue),
			"after":  maskAuditValue(key, afterValue),
		}
	}
	raw, err := json.Marshal(diff)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func auditStateMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"value": fmt.Sprintf("%v", value)}
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return map[string]any{"value": string(raw)}
	}
	return result
}

func maskAuditValue(key string, value any) any {
	if isSensitiveAuditKey(key) {
		if isEmptyAuditValue(value) {
			return value
		}
		return "***changed***"
	}
	switch typed := value.(type) {
	case map[string]any:
		masked := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			masked[nestedKey] = maskAuditValue(nestedKey, nestedValue)
		}
		return masked
	case []any:
		masked := make([]any, 0, len(typed))
		for _, item := range typed {
			if nested, ok := item.(map[string]any); ok {
				masked = append(masked, maskAuditValue("", nested))
			} else {
				masked = append(masked, item)
			}
		}
		return masked
	default:
		return value
	}
}

func isSensitiveAuditKey(key string) bool {
	lower := strings.ToLower(key)
	return lower == "key" ||
		lower == "access_token" ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "password") ||
		strings.Contains(lower, "token") ||
		strings.HasSuffix(lower, "_key")
}

func isEmptyAuditValue(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return text == ""
	}
	return false
}

func ListAdminAuditLogs(query AdminAuditQuery) ([]AdminAuditLog, int64, error) {
	if CoreDB() == nil {
		return nil, 0, nil
	}
	db := CoreDB().Model(&AdminAuditLog{})
	if query.Action != "" {
		db = db.Where("action = ?", query.Action)
	}
	if query.TargetType != "" {
		db = db.Where("target_type = ?", query.TargetType)
	}
	if query.ActorID > 0 {
		db = db.Where("actor_id = ?", query.ActorID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []AdminAuditLog
	err := db.Order("created_at desc, id desc").Limit(limit).Offset(query.Offset).Find(&rows).Error
	return rows, total, err
}
