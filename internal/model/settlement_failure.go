package model

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

const (
	SettlementFailureStatusOpen         = "open"
	SettlementFailureStatusRetrying     = "retrying"
	SettlementFailureStatusResolved     = "resolved"
	SettlementFailureStatusManualReview = "manual_review"
)

type SettlementFailure struct {
	ProjectID      int              `json:"-" gorm:"index;not null;default:0"`
	ActorUserID    int              `json:"-" gorm:"not null;default:0"`
	ID             uint             `json:"id" gorm:"primaryKey"`
	RequestID      string           `json:"request_id" gorm:"size:64;index"`
	UserID         int              `json:"user_id" gorm:"index;not null"`
	TokenID        int              `json:"token_id" gorm:"index"`
	TokenKey       string           `json:"token_key" gorm:"type:varchar(191)"`
	TokenUnlimited bool             `json:"token_unlimited"`
	IsPlayground   bool             `json:"is_playground" gorm:"index"`
	QuotaDelta     billing.MicroUSD `json:"quota_delta" gorm:"type:bigint;not null"`
	PreConsumed    billing.MicroUSD `json:"pre_consumed" gorm:"type:bigint"`
	RefType        string           `json:"ref_type" gorm:"type:varchar(64);index"`
	RefID          string           `json:"ref_id" gorm:"type:varchar(191);index"`
	PricingVersion int64            `json:"pricing_version" gorm:"index;not null"`
	Error          string           `json:"error" gorm:"type:text"`
	Status         string           `json:"status" gorm:"type:varchar(32);index;not null;default:'open'"`
	Attempts       int              `json:"attempts" gorm:"not null;default:0"`
	LastError      string           `json:"last_error" gorm:"type:text"`
	NextRetryAt    *time.Time       `json:"next_retry_at" gorm:"index"`
	ResolvedAt     *time.Time       `json:"resolved_at" gorm:"index"`
	Metadata       string           `json:"metadata,omitempty" gorm:"type:text"`
	CreatedAt      time.Time        `json:"created_at" gorm:"index"`
	UpdatedAt      time.Time        `json:"updated_at" gorm:"index"`
}

// GetUserUnresolvedSettlementFailureCount includes retry/manual-review rows,
// except additional charges with a verified permanent platform waiver. A
// changed waiver/source is an error, never silently excluded from this count.
func GetUserUnresolvedSettlementFailureCount(userID int) (int64, error) {
	return VerifiedUnresolvedSettlementFailureCount(context.Background(), CoreDB(), userID)
}

func RecordSettlementFailure(row SettlementFailure) error {
	if CoreDB() == nil {
		return nil
	}
	if row.Status == "" {
		row.Status = SettlementFailureStatusOpen
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	if row.Metadata == "" {
		row.Metadata = "{}"
	}
	if err := CoreDB().Create(&row).Error; err != nil {
		common.SysLog("failed to record settlement failure: " + err.Error())
		return err
	}
	return nil
}

func ListSettlementFailuresForRetry(limit int, now time.Time) ([]SettlementFailure, error) {
	if CoreDB() == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	var rows []SettlementFailure
	err := CoreDB().
		Where("status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", SettlementFailureStatusOpen, now).
		Order("created_at ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func ReopenStaleSettlementFailureClaims(cutoff time.Time, now time.Time) (int64, error) {
	if CoreDB() == nil {
		return 0, nil
	}
	var stale []SettlementFailure
	if err := CoreDB().Where("status = ? AND updated_at <= ?", SettlementFailureStatusRetrying, cutoff).Find(&stale).Error; err != nil {
		return 0, err
	}
	for _, row := range stale {
		if waived, err := LegacySettlementFailureWaiverBlocksReplay(context.Background(), CoreDB(), row); err != nil {
			return 0, err
		} else if waived {
			return 0, ErrLegacySettlementWaived
		}
	}
	update := CoreDB().Model(&SettlementFailure{}).
		Where("status = ? AND updated_at <= ?", SettlementFailureStatusRetrying, cutoff).
		Updates(map[string]any{
			"status":        SettlementFailureStatusOpen,
			"last_error":    "retry claim timed out",
			"next_retry_at": &now,
			"updated_at":    now,
		})
	if update.Error != nil {
		return 0, update.Error
	}
	return update.RowsAffected, nil
}

func ClaimSettlementFailureForRetry(id uint, now time.Time) (bool, error) {
	if CoreDB() == nil {
		return false, nil
	}
	var row SettlementFailure
	if err := CoreDB().First(&row, id).Error; err != nil {
		return false, err
	}
	if waived, err := LegacySettlementFailureWaiverBlocksReplay(context.Background(), CoreDB(), row); err != nil || waived {
		return false, err
	}
	update := CoreDB().Model(&SettlementFailure{}).
		Where("id = ? AND status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", id, SettlementFailureStatusOpen, now).
		Updates(map[string]any{
			"status":     SettlementFailureStatusRetrying,
			"attempts":   gorm.Expr("attempts + 1"),
			"last_error": "",
			"updated_at": now,
		})
	if update.Error != nil {
		return false, update.Error
	}
	return update.RowsAffected == 1, nil
}

func ResolveSettlementFailure(id uint, now time.Time) error {
	if CoreDB() == nil {
		return nil
	}
	var row SettlementFailure
	if err := CoreDB().First(&row, id).Error; err != nil {
		return err
	}
	if waived, err := LegacySettlementFailureWaiverBlocksReplay(context.Background(), CoreDB(), row); err != nil {
		return err
	} else if waived {
		return ErrLegacySettlementWaived
	}
	return CoreDB().Model(&SettlementFailure{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        SettlementFailureStatusResolved,
			"last_error":    "",
			"next_retry_at": nil,
			"resolved_at":   &now,
			"updated_at":    now,
		}).Error
}

func ScheduleSettlementFailureRetry(id uint, lastErr string, nextRetryAt time.Time, now time.Time) error {
	if CoreDB() == nil {
		return nil
	}
	return CoreDB().Model(&SettlementFailure{}).
		Where("id = ? AND status = ?", id, SettlementFailureStatusRetrying).
		Updates(map[string]any{
			"status":        SettlementFailureStatusOpen,
			"last_error":    lastErr,
			"next_retry_at": &nextRetryAt,
			"updated_at":    now,
		}).Error
}

func MarkSettlementFailureManualReview(id uint, lastErr string, now time.Time) error {
	if CoreDB() == nil {
		return nil
	}
	return CoreDB().Model(&SettlementFailure{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        SettlementFailureStatusManualReview,
			"last_error":    lastErr,
			"next_retry_at": nil,
			"updated_at":    now,
		}).Error
}

func WalletLedgerEventExists(userID int, refType string, refID string) (bool, error) {
	if CoreDB() == nil || userID == 0 || refType == "" || refID == "" {
		return false, nil
	}
	var count int64
	err := CoreDB().Model(&WalletLedger{}).
		Where("user_id = ? AND ref_type = ? AND ref_id = ?", userID, refType, refID).
		Count(&count).Error
	return count > 0, err
}

func (row SettlementFailure) LedgerMetadata() map[string]any {
	metadata := map[string]any{}
	if row.Metadata != "" {
		_ = json.Unmarshal([]byte(row.Metadata), &metadata)
	}
	metadata["settlement_failure_id"] = row.ID
	metadata["settlement_failure_request_id"] = row.RequestID
	return metadata
}
