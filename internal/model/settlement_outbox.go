package model

import (
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
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
