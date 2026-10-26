package model

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

type UsageData struct {
	Id             int              `json:"id"`
	UserID         int              `json:"user_id" gorm:"index"`
	Username       string           `json:"username" gorm:"index:idx_usage_model_user_name,priority:2;size:64;default:''"`
	ModelName      string           `json:"model_name" gorm:"index:idx_usage_model_user_name,priority:1;size:64;default:''"`
	CreatedAt      int64            `json:"created_at" gorm:"bigint;index:idx_usage_created_at,priority:2"`
	TokenUsed      int              `json:"token_used" gorm:"default:0"`
	Count          int              `json:"count" gorm:"default:0"`
	AmountMicrousd billing.MicroUSD `json:"amount_microusd" gorm:"type:bigint;default:0"`
}

func UpdateUsageData() { RunUsageDataUpdater(context.Background()) }

func RunUsageDataUpdater(ctx context.Context) {
	for ctx.Err() == nil {
		if common.DataExportEnabled {
			SaveUsageDataCache()
		}
		interval := time.Duration(common.DataExportInterval) * time.Minute
		if interval <= 0 {
			interval = time.Minute
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

var CacheUsageData = make(map[string]*UsageData)
var CacheUsageDataLock = sync.Mutex{}

var errUsageDataCacheRange = errors.New("usage export counter out of range")

func checkedUsageDataInt(a, b int) (int, error) {
	if a < 0 || b < 0 {
		return 0, errUsageDataCacheRange
	}
	total, err := checkedLedgerAdd(int64(a), int64(b))
	if err != nil || total > int64(^uint(0)>>1) {
		return 0, errUsageDataCacheRange
	}
	return int(total), nil
}

func logUsageDataCache(userId int, username string, modelName string, amountMicrousd billing.MicroUSD, createdAt int64, tokenUsed int) error {
	if tokenUsed < 0 {
		return errUsageDataCacheRange
	}
	key := fmt.Sprintf("%d-%s-%s-%d", userId, username, modelName, createdAt)
	usageData, ok := CacheUsageData[key]
	if ok {
		// Check every field before replacing the cached entry. This optional
		// post-commit projection cannot roll back the durable telemetry facts.
		next := *usageData
		var err error
		next.Count, err = checkedUsageDataInt(next.Count, 1)
		if err != nil {
			return err
		}
		amount, err := checkedLedgerAdd(int64(next.AmountMicrousd), int64(amountMicrousd))
		if err != nil {
			return errUsageDataCacheRange
		}
		next.AmountMicrousd = billing.MicroUSD(amount)
		next.TokenUsed, err = checkedUsageDataInt(next.TokenUsed, tokenUsed)
		if err != nil {
			return err
		}
		usageData = &next
	} else {
		usageData = &UsageData{
			UserID:         userId,
			Username:       username,
			ModelName:      modelName,
			CreatedAt:      createdAt,
			Count:          1,
			AmountMicrousd: amountMicrousd,
			TokenUsed:      tokenUsed,
		}
	}
	CacheUsageData[key] = usageData
	return nil
}

func LogUsageData(userId int, username string, modelName string, amountMicrousd billing.MicroUSD, createdAt int64, tokenUsed int) {
	// 只精确到小时
	createdAt = createdAt - (createdAt % 3600)

	CacheUsageDataLock.Lock()
	defer CacheUsageDataLock.Unlock()
	if err := logUsageDataCache(userId, username, modelName, amountMicrousd, createdAt, tokenUsed); err != nil {
		common.IncrementOperationalCounter("telemetry.export.counter_range")
		common.SysError("usage export counter out of range; existing cache entry retained")
	}
}

func SaveUsageDataCache() {
	CacheUsageDataLock.Lock()
	defer CacheUsageDataLock.Unlock()
	size := len(CacheUsageData)
	if size == 0 {
		return
	}
	// 如果缓存中有数据，就保存到数据库中
	// 1. 先查询数据库中是否有数据
	// 2. 如果有数据，就更新数据
	// 3. 如果没有数据，就插入数据
	for _, usageData := range CacheUsageData {
		usageDataDB := &UsageData{}
		TelemetryDB().Table("usage_data").Where("user_id = ? and username = ? and model_name = ? and created_at = ?",
			usageData.UserID, usageData.Username, usageData.ModelName, usageData.CreatedAt).First(usageDataDB)
		if usageDataDB.Id > 0 {
			increaseUsageData(usageData.UserID, usageData.Username, usageData.ModelName, usageData.Count, usageData.AmountMicrousd, usageData.CreatedAt, usageData.TokenUsed)
		} else {
			TelemetryDB().Table("usage_data").Create(usageData)
		}
	}
	CacheUsageData = make(map[string]*UsageData)
	common.SysDebug(fmt.Sprintf("saved usage dashboard data records=%d", size))
}

func increaseUsageData(userId int, username string, modelName string, count int, amountMicrousd billing.MicroUSD, createdAt int64, tokenUsed int) {
	err := TelemetryDB().Table("usage_data").Where("user_id = ? and username = ? and model_name = ? and created_at = ?",
		userId, username, modelName, createdAt).Updates(map[string]interface{}{
		"count":           gorm.Expr("count + ?", count),
		"amount_microusd": gorm.Expr("amount_microusd + ?", amountMicrousd),
		"token_used":      gorm.Expr("token_used + ?", tokenUsed),
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("increaseUsageData error: %s", err))
	}
}

func GetUsageDataByUsername(username string, startTime int64, endTime int64) (usageData []*UsageData, err error) {
	var usageRows []*UsageData
	err = TelemetryDB().Table("usage_data").Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).Find(&usageRows).Error
	return usageRows, err
}

func GetAllUsageData(startTime int64, endTime int64, username string) (usageData []*UsageData, err error) {
	if username != "" {
		return GetUsageDataByUsername(username, startTime, endTime)
	}
	var usageRows []*UsageData
	err = TelemetryDB().Table("usage_data").Select("model_name, sum(count) as count, sum(amount_microusd) as amount_microusd, sum(token_used) as token_used, created_at").Where("created_at >= ? and created_at <= ?", startTime, endTime).Group("model_name, created_at").Find(&usageRows).Error
	return usageRows, err
}
