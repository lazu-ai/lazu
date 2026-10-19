package email_setting

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/config"
)

// EmailSetting holds the operator switches for platform mail. Subjects and
// layouts are fixed in code (brand/foundations/emails.md); the editable
// templates they replaced are retired.
type EmailSetting struct {
	FromName               string  `json:"from_name"`
	BatchSize              int     `json:"batch_size"`
	BalanceChangeEnabled   bool    `json:"balance_change_enabled"`
	LowBalanceEnabled      bool    `json:"low_balance_enabled"`
	LowBalanceThresholdUSD float64 `json:"low_balance_threshold_usd"`
	LowBalanceRemindDays   []int   `json:"low_balance_remind_days"`
	AdminEmail             string  `json:"admin_email"`
}

var emailSetting = EmailSetting{
	FromName:               "Lazu",
	BatchSize:              200,
	BalanceChangeEnabled:   true,
	LowBalanceEnabled:      true,
	LowBalanceThresholdUSD: 20,
	LowBalanceRemindDays:   []int{1, 3, 7, 15, 30},
	AdminEmail:             "",
}

func init() {
	config.GlobalConfig.Register("email_setting", &emailSetting)
}

// RetiredOptionKeys are email_setting options that no longer exist. Older
// consoles may still send them; they are ignored rather than stored.
var RetiredOptionKeys = []string{
	"email_setting.balance_change_subject", "email_setting.balance_change_template",
	"email_setting.low_balance_subject", "email_setting.low_balance_template",
	"email_setting.announcement_subject", "email_setting.announcement_template",
	"email_setting.broadcast_subject", "email_setting.broadcast_template",
	"email_setting.payment_notify_admin_enabled", "email_setting.payment_notify_admin_subject", "email_setting.payment_notify_admin_template",
	"email_setting.payment_pending_admin_enabled", "email_setting.payment_pending_admin_subject", "email_setting.payment_pending_admin_template",
	"email_setting.payment_receipt_enabled", "email_setting.payment_receipt_subject", "email_setting.payment_receipt_template",
}

// LoadFromAppConfig merges YAML overrides into runtime defaults before options are hydrated.
func LoadFromAppConfig(app common.AppConfigStruct) {
	if name := strings.TrimSpace(app.EmailNotifications.FromName); name != "" {
		emailSetting.FromName = name
	}
	if app.EmailNotifications.BatchSize > 0 {
		emailSetting.BatchSize = app.EmailNotifications.BatchSize
	}
	emailSetting.BalanceChangeEnabled = app.EmailNotifications.BalanceChange.Enable

	lb := app.EmailNotifications.LowBalance
	emailSetting.LowBalanceEnabled = lb.Enable
	if lb.ThresholdUSD > 0 {
		emailSetting.LowBalanceThresholdUSD = lb.ThresholdUSD
	}
	if remindDays := common.NormalizePositiveIntSlice(lb.RemindDays); len(remindDays) > 0 {
		emailSetting.LowBalanceRemindDays = remindDays
	}
	if email := strings.TrimSpace(app.EmailNotifications.AdminEmail); email != "" {
		emailSetting.AdminEmail = email
	}
}

func GetEmailSetting() *EmailSetting {
	return &emailSetting
}
