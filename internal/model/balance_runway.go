package model

import (
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// PayerSpendWindow is what one paying account spent inside a window, read
// from the telemetry consume logs. Project requests bill the payer, so the
// account is the payer where one is recorded and the caller otherwise.
type PayerSpendWindow struct {
	PayerUserID int
	Spend       billing.MicroUSD
	// FirstAt and LastAt bound the spend inside the window (unix seconds).
	FirstAt int64
	LastAt  int64
}

// PayerSpendBetween sums consume logs per paying account in [since, until).
// Accounts with no positive spend are left out.
func PayerSpendBetween(since, until time.Time) ([]PayerSpendWindow, error) {
	if TelemetryDB() == nil {
		return nil, nil
	}
	var rows []PayerSpendWindow
	payer := "CASE WHEN payer_user_id > 0 THEN payer_user_id ELSE user_id END"
	err := TelemetryDB().Table("logs").
		Select(payer+" AS payer_user_id, COALESCE(SUM(amount_microusd), 0) AS spend, MIN(created_at) AS first_at, MAX(created_at) AS last_at").
		Where("type = ? AND created_at >= ? AND created_at < ?", LogTypeConsume, since.Unix(), until.Unix()).
		Group(payer).
		Having("SUM(amount_microusd) > 0").
		Scan(&rows).Error
	return rows, err
}

// ListEnabledUsersByIDs loads the enabled accounts among ids with the columns
// a balance alert needs. Disabled accounts are left out.
func ListEnabledUsersByIDs(ids []int) ([]*User, error) {
	var out []*User
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		var users []*User
		err := CoreDB().Select("id, username, display_name, email, quota, setting, home_surface, status").
			Where("id IN ? AND status = ?", ids[start:end], common.UserStatusEnabled).
			Order("id ASC").Find(&users).Error
		if err != nil {
			return nil, err
		}
		out = append(out, users...)
	}
	return out, nil
}

// MailDeliverySentSince reports whether a mail of this template was archived
// for the account at or after since, whatever became of it. A once-a-day
// alert asks this before claiming its row, so a restart or a second
// instance cannot repeat it.
func MailDeliverySentSince(userID int, template string, since time.Time) (bool, error) {
	var count int64
	err := TelemetryDB().Model(&MailDelivery{}).
		Where("user_id = ? AND template = ? AND created_at >= ?", userID, template, since.Unix()).
		Count(&count).Error
	return count > 0, err
}
