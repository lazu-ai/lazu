package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Functions only tests in this package call. They seed state or read it
// back; production reaches the same tables through the live paths the tests
// name. Kept here so the production package carries none of them.

// From announcement.go.
// HasUnseenCriticalAnnouncement is deliberately independent of the bounded
// unseen feed. The version plate uses it to decide whether to open the update
// panel automatically, so a critical item must not be hidden behind newer
// notices that filled the current page.
func HasUnseenCriticalAnnouncement(userID int) (bool, error) {
	item, err := UnseenCriticalAnnouncement(userID, "zh")
	return item != nil, err
}

// From log.go.
func GetTokenUsageSeriesByTokenID(tokenID int, startTimestamp int64, endTimestamp int64, granularity string) ([]TokenUsageSeriesPoint, error) {
	return getUsageSeriesByScope("token_id", tokenID, startTimestamp, startTimestamp > 0, endTimestamp, endTimestamp > 0, granularity)
}

// From log_diagnostics.go.
// UpdateRequestLogDiagnosticsBatch merges independent diagnostic events for
// each request and updates one PostgreSQL batch. Missing rows remain unacked so
// a diagnostic published before its final log can be retried by the consumer.
func UpdateRequestLogDiagnosticsBatch(events []RequestDiagnosticsEvent) (map[string]bool, error) {
	if len(events) == 0 {
		return map[string]bool{}, nil
	}
	db := TelemetryDB()
	if db == nil {
		return nil, errors.New("telemetry database unavailable")
	}
	if db.Dialector.Name() == "postgres" {
		return updateRequestLogDiagnosticsBatchTx(db, events)
	}
	var found map[string]bool
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		found, err = updateRequestLogDiagnosticsBatchTx(tx, events)
		return err
	})
	return found, err
}

// From media_job_eta.go.
func resetMediaJobTypicalCache() {
	mediaJobTypicalMu.Lock()
	mediaJobTypicalCache = map[string]mediaJobTypical{}
	mediaJobTypicalMu.Unlock()
}

// From model_metadata.go.
func modelRequestIncrementConflictClause(now time.Time) clause.OnConflict {
	return clause.OnConflict{
		Columns: []clause.Column{{Name: "model_name"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"total_requests": incrementCurrentTableColumn("total_requests"),
			"updated_at":     now,
		}),
	}
}

// From model_metadata.go.
func incrementCurrentTableColumn(columnName string) clause.Expr {
	return gorm.Expr("? + 1", clause.Column{
		Table: clause.CurrentTable,
		Name:  columnName,
	})
}

// From model_metadata.go.
func ResetWeeklyStats() error {
	return resetModelRequestCounter(CoreDB(), "weekly_requests")
}

// From model_metadata.go.
func ResetMonthlyStats() error {
	return resetModelRequestCounter(CoreDB(), "monthly_requests")
}

// From project_billing.go.
// Move an attempted order behind untouched ones, including when Stripe has
// not produced a final invoice yet. A failed old order must not block the queue.
func MarkProjectTopUpDocumentChecked(id int, checkedAt int64) error {
	return CoreDB().Model(&TopUp{}).Where("id = ?", id).Update("document_checked_at", checkedAt).Error
}

// From relay_events.go.
func relayEventXAddArgs(eventType string, raw []byte) *redis.XAddArgs {
	return &redis.XAddArgs{
		Stream: common.RelayEventStream,
		MaxLen: relayEventMaxLength(),
		Values: map[string]any{
			"type":    eventType,
			"payload": string(raw),
		},
	}
}

// From routing_queries.go.
func UpdateOutcomeQualitySignals(requestID string, retried bool, switched bool) error {
	return UpdateOutcomeQualitySignalsContext(context.Background(), requestID, retried, switched)
}

// From telemetry_incremental.go.
func recordHourlyUsageTx(tx *gorm.DB, log *Log, outcome *RoutingOutcome) error {
	if log == nil && outcome == nil {
		return nil
	}
	at := int64(0)
	if log != nil {
		at = log.CreatedAt
	}
	if outcome != nil {
		at = outcome.OutcomeAt.Unix()
	}
	if at <= 0 {
		at = time.Now().Unix()
	}
	hour := at - at%3600
	state := dashboardRollupState()
	if err := buildHourlyState(state, log, outcome); err != nil {
		return err
	}
	return applyHourlyStateTx(tx, hour, state)
}

// From token.go.
// BatchDeleteTokens 删除指定用户的一组令牌，返回成功删除数量
func BatchDeleteTokens(ids []int, userId int) (int, error) {
	if len(ids) == 0 {
		return 0, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "ids"})
	}

	tx := CoreDB().Begin()

	var tokens []Token
	if err := excludeInternalTokens(tx).Where("user_id = ? AND id IN (?)", userId, ids).Find(&tokens).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := excludeInternalTokens(tx).Where("user_id = ? AND id IN (?)", userId, ids).Delete(&Token{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}

	digests := make([]string, 0, len(tokens))
	for index := range tokens {
		digests = append(digests, tokens[index].CacheDigest())
	}
	cacheErr := cacheInvalidateTokenSnapshotsWithFence(digests)
	if cacheErr != nil {
		return len(tokens), &TokenMutationCommittedError{Operation: "batch deletion", Cause: cacheErr}
	}

	return len(tokens), nil
}

// From token_cache.go.
func cacheSetToken(token Token) error {
	key := token.CacheDigest()
	if key == "" {
		return fmt.Errorf("token cache digest is empty")
	}
	token.Clean()
	err := common.RedisHSetObj(fmt.Sprintf("token:%s", key), &token, time.Duration(common.RedisKeyCacheSeconds())*time.Second)
	if err != nil {
		return err
	}
	return nil
}

// From user.go.
// GetUserSetting gets setting from Redis first, falls back to CoreDB() if needed
func GetUserSetting(id int, fromDB bool) (settingMap dto.UserSetting, err error) {
	var setting string
	var needsPassword bool
	defer func() {
		// Update Redis cache asynchronously on successful CoreDB() read
		if shouldUpdateRedis(fromDB, err) {
			common.SafeGo("user-setting-cache-refresh", func() {
				if err := updateUserSettingCache(id, setting); err != nil {
					common.SysLog("failed to update user setting cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		setting, err := getUserSettingCache(id)
		if err == nil {
			return setting, nil
		}
		// Don't return error - fall through to CoreDB()
	}
	fromDB = true
	var settingRow struct {
		Setting       string
		NeedsPassword bool
	}
	err = CoreDB().Model(&User{}).Where("id = ?", id).Select("setting", "needs_password").Find(&settingRow).Error
	if err != nil {
		return settingMap, err
	}
	setting = settingRow.Setting
	needsPassword = settingRow.NeedsPassword
	userBase := &UserBase{
		Setting:       setting,
		NeedsPassword: needsPassword,
	}
	return userBase.GetSetting(), nil
}

// From user_cache.go.
func getUserSettingCache(userId int) (dto.UserSetting, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return dto.UserSetting{}, err
	}
	return cache.GetSetting(), nil
}

// From user_cache.go.
func updateUserSettingCache(userId int, setting string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Setting", setting)
}
