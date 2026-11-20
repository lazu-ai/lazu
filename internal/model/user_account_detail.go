package model

import (
	"github.com/lazu-ai/lazu/internal/common"
)

// UserRatePeak is the busiest calendar minute an account had in a window:
// requests (successful and failed) and tokens billed in that minute. It
// approximates what the rate limiter counts, which works on a sliding
// minute and on admission estimates rather than billed tokens.
type UserRatePeak struct {
	RPM int64 `json:"rpm"`
	TPM int64 `json:"tpm"`
}

// GetUserRatePeak reads one account's per-minute peak since a timestamp.
// Each maximum is taken on its own, so the two may come from different
// minutes. The scan is keyed on the indexed (user_id, created_at) pair.
func GetUserRatePeak(userID int, since int64) (UserRatePeak, error) {
	var peak UserRatePeak
	if TelemetryDB() == nil {
		return peak, nil
	}
	minutes := TelemetryDB().Model(&Log{}).
		Select("count(*) as requests, coalesce(sum(prompt_tokens + completion_tokens), 0) as tokens").
		Where("user_id = ? AND type IN ? AND created_at >= ?", userID, []int{LogTypeConsume, LogTypeError}, since).
		Group("created_at / 60")
	err := TelemetryDB().Table("(?) as minutes", minutes).
		Select("coalesce(max(requests), 0) as rpm, coalesce(max(tokens), 0) as tpm").
		Scan(&peak).Error
	return peak, err
}

// LatestBalanceAlertAt is when the newest low-balance reminder (the
// low-balance mail or the runway mail) left for an account; 0 when none has.
func LatestBalanceAlertAt(userID int) (int64, error) {
	if TelemetryDB() == nil || userID <= 0 {
		return 0, nil
	}
	var row MailDelivery
	err := TelemetryDB().Model(&MailDelivery{}).
		Select("created_at", "sent_at").
		Where("user_id = ? AND kind = ? AND template IN ? AND status IN ?",
			userID, common.MailKindBalance,
			[]string{"", "balance-runway"},
			[]string{MailStatusSent, MailStatusDelivered, MailStatusDelayed}).
		Order("created_at desc").
		Limit(1).
		Find(&row).Error
	if err != nil {
		return 0, err
	}
	if row.SentAt > 0 {
		return row.SentAt, nil
	}
	return row.CreatedAt, nil
}

// GetUserPendingTopUps lists an account's orders still waiting for payment
// from the last 30 days, newest first.
func GetUserPendingTopUps(userID int, limit int) ([]*TopUp, error) {
	var rows []*TopUp
	err := CoreDB().
		Where("user_id = ? AND status = ? AND create_time >= ?", userID, common.TopUpStatusPending, topUpQueryCutoff()).
		Order("id desc").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}
