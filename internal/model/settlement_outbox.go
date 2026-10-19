package model

import (
	"encoding/json"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SettlementOutbox struct {
	ProjectID      int              `json:"-" gorm:"index;not null;default:0"`
	ActorUserID    int              `json:"-" gorm:"not null;default:0"`
	ID             uint             `json:"id" gorm:"primaryKey"`
	RequestID      string           `json:"request_id" gorm:"size:64;index"`
	UserID         int              `json:"user_id" gorm:"index;index:idx_settlement_outbox_ref,unique;not null"`
	TokenID        int              `json:"token_id" gorm:"index"`
	TokenKey       string           `json:"token_key" gorm:"type:varchar(191)"`
	TokenUnlimited bool             `json:"token_unlimited"`
	IsPlayground   bool             `json:"is_playground" gorm:"index"`
	QuotaDelta     billing.MicroUSD `json:"quota_delta" gorm:"type:bigint;not null"`
	PreConsumed    billing.MicroUSD `json:"pre_consumed" gorm:"type:bigint"`
	RefType        string           `json:"ref_type" gorm:"type:varchar(64);index;index:idx_settlement_outbox_ref,unique"`
	RefID          string           `json:"ref_id" gorm:"type:varchar(191);index;index:idx_settlement_outbox_ref,unique"`
	PricingVersion int64            `json:"pricing_version" gorm:"index;not null"`
	Status         string           `json:"status" gorm:"type:varchar(32);index;not null;default:'open'"`
	Attempts       int              `json:"attempts" gorm:"not null;default:0"`
	LastError      string           `json:"last_error" gorm:"type:text"`
	NextRetryAt    *time.Time       `json:"next_retry_at" gorm:"index"`
	ResolvedAt     *time.Time       `json:"resolved_at" gorm:"index"`
	Metadata       string           `json:"metadata,omitempty" gorm:"type:text"`
	CreatedAt      time.Time        `json:"created_at" gorm:"index"`
	UpdatedAt      time.Time        `json:"updated_at" gorm:"index"`
}

func RecordSettlementOutbox(row SettlementOutbox) (*SettlementOutbox, error) {
	if CoreDB() == nil || row.QuotaDelta == 0 {
		return nil, nil
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
	err := CoreDB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "ref_type"}, {Name: "ref_id"}},
		DoNothing: true,
	}).Create(&row).Error
	if err != nil {
		common.SysLog("failed to record settlement outbox: " + err.Error())
		return nil, err
	}
	if row.ID != 0 {
		return &row, nil
	}
	var existing SettlementOutbox
	err = CoreDB().Where("user_id = ? AND ref_type = ? AND ref_id = ?", row.UserID, row.RefType, row.RefID).First(&existing).Error
	if err != nil {
		return nil, err
	}
	return &existing, nil
}

func ListSettlementOutboxForRetry(limit int, now time.Time) ([]SettlementOutbox, error) {
	if CoreDB() == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	var rows []SettlementOutbox
	err := CoreDB().
		Where("status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", SettlementFailureStatusOpen, now).
		Order("created_at ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func ReopenStaleSettlementOutboxClaims(cutoff time.Time, now time.Time) (int64, error) {
	if CoreDB() == nil {
		return 0, nil
	}
	update := CoreDB().Model(&SettlementOutbox{}).
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

func ClaimSettlementOutboxForRetry(id uint, now time.Time) (bool, error) {
	if CoreDB() == nil {
		return false, nil
	}
	update := CoreDB().Model(&SettlementOutbox{}).
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

func ResolveSettlementOutbox(id uint, now time.Time) error {
	if CoreDB() == nil || id == 0 {
		return nil
	}
	return CoreDB().Model(&SettlementOutbox{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        SettlementFailureStatusResolved,
			"last_error":    "",
			"next_retry_at": nil,
			"resolved_at":   &now,
			"updated_at":    now,
		}).Error
}

func ScheduleSettlementOutboxRetry(id uint, lastErr string, nextRetryAt time.Time, now time.Time) error {
	if CoreDB() == nil {
		return nil
	}
	return CoreDB().Model(&SettlementOutbox{}).
		Where("id = ? AND status = ?", id, SettlementFailureStatusRetrying).
		Updates(map[string]any{
			"status":        SettlementFailureStatusOpen,
			"last_error":    lastErr,
			"next_retry_at": &nextRetryAt,
			"updated_at":    now,
		}).Error
}

func MarkSettlementOutboxManualReview(id uint, lastErr string, now time.Time) error {
	if CoreDB() == nil {
		return nil
	}
	return CoreDB().Model(&SettlementOutbox{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        SettlementFailureStatusManualReview,
			"last_error":    lastErr,
			"next_retry_at": nil,
			"updated_at":    now,
		}).Error
}

func (row SettlementOutbox) LedgerMetadata() map[string]any {
	metadata := map[string]any{}
	if row.Metadata != "" {
		_ = json.Unmarshal([]byte(row.Metadata), &metadata)
	}
	metadata["settlement_outbox_id"] = row.ID
	metadata["settlement_outbox_request_id"] = row.RequestID
	return metadata
}
