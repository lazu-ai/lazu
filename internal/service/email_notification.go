package service

import (
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func resolveUserEmail(user *model.User) string {
	setting := user.GetSetting()
	if setting.NotificationEmail != "" {
		return setting.NotificationEmail
	}
	return user.Email
}

// userMailLang is the language mail to this account is written in.
func userMailLang(user *model.User) string {
	if user == nil {
		return common.MailDefaultLang
	}
	return common.MailLang(user.GetSetting().Language)
}

// userMailLink is where a mail sends this reader: the console page, or its
// counterpart on the surface the account signed up through.
func userMailLink(user *model.User, link model.SurfaceLink) string {
	surface := ""
	if user != nil {
		surface = user.HomeSurface
	}
	return common.MailSiteURL(model.UserSurfacePath(surface, link))
}

// BalanceChangeReason names why an operator moved a balance. The mail says
// the reason in the reader's language; an operator's free-text note stays
// internal.
type BalanceChangeReason string

const (
	BalanceReasonAdminAdjust BalanceChangeReason = "admin_adjust"
	BalanceReasonAdminTopUp  BalanceChangeReason = "admin_topup"
)

func (r BalanceChangeReason) text(lang string) string {
	if r == BalanceReasonAdminTopUp {
		return common.MailText(lang, "补录的充值到账", "A top-up was completed manually")
	}
	return common.MailText(lang, "Lazu 调整了余额", "Adjusted by Lazu")
}

// NotifyBalanceChange mails the account when an operator changes its balance.
// Paid top-ups send the payment mail instead.
func NotifyBalanceChange(userId int, delta billing.MicroUSD, reason BalanceChangeReason) {
	cfg := email_setting.GetEmailSetting()
	if !cfg.BalanceChangeEnabled || delta == 0 {
		return
	}
	user, err := model.GetUserById(userId, true)
	if err != nil {
		common.SysError(fmt.Sprintf("failed to load user for balance change notify: %v", err))
		return
	}
	email := resolveUserEmail(user)
	if email == "" {
		common.SysLog(fmt.Sprintf("user %d has no email to notify balance change", user.Id))
		return
	}
	lang := userMailLang(user)
	change := common.MailSignedUSD(delta.Dollars())
	balance := common.MailUSD(user.Quota.Dollars())
	subject := common.MailText(lang, "你的 Lazu 余额 "+change, "Your Lazu balance "+change)
	body, err := common.RenderMail(common.Mail{
		Lang: lang, Tier: common.MailInk, Eyebrow: "BALANCE ADJUSTED",
		Preheader: common.MailText(lang, "调整后余额 "+balance, "Balance after the change: "+balance),
		Hero:      change, CompactHero: absDollars(delta) >= 1000,
		Message: common.MailText(lang, "Lazu 调整了你的账户余额。", "Lazu adjusted your account balance."),
		Blocks: []common.MailBlock{
			{Facts: []common.MailFact{
				{Label: common.MailText(lang, "原因", "Reason"), Value: reason.text(lang)},
				{Label: common.MailText(lang, "调整后余额", "Balance"), Value: balance},
				{Label: common.MailText(lang, "时间", "Time"), Value: time.Now().UTC().Format("2006-01-02 15:04 UTC")},
			}},
			{Primary: &common.MailAction{Label: common.MailText(lang, "打开钱包", "Open wallet"), URL: userMailLink(user, model.SurfaceLinkWallet)}},
		},
	})
	if err != nil {
		common.SysError("render balance change email: " + err.Error())
		return
	}
	common.SafeGo("balance-change-email", func() {
		if err := common.SendMail(common.MailMeta{Kind: common.MailKindBalance}, subject, email, body); err != nil {
			common.SysError(fmt.Sprintf("failed to send balance change email to %s: %v", common.MaskEmail(email), err))
		}
	})

	_, _ = SendLowBalanceEmailIfNeeded(user, user.Quota, 0)
}

func absDollars(v billing.MicroUSD) float64 {
	d := v.Dollars()
	if d < 0 {
		return -d
	}
	return d
}

// SendLowBalanceEmailIfNeeded sends the low-balance mail when balance sits at
// or under threshold (the platform threshold when threshold <= 0) and the
// reminder schedule is due.
// Returns:
// - sent: whether an email dispatch was triggered.
// - suppressGenericEmail: whether generic quota-exceed email should be suppressed.
func SendLowBalanceEmailIfNeeded(user *model.User, balance billing.MicroUSD, threshold billing.MicroUSD) (sent bool, suppressGenericEmail bool) {
	cfg := email_setting.GetEmailSetting()
	if !cfg.LowBalanceEnabled {
		return false, false
	}
	if threshold <= 0 {
		threshold = billing.USD(cfg.LowBalanceThresholdUSD)
	}
	if threshold <= 0 {
		return false, false
	}
	if balance > threshold {
		resetLowBalanceEmailCooldown(user.Id)
		clearLowBalanceSendFailure(user.Id)
		return false, false
	}

	email := resolveUserEmail(user)
	if email == "" {
		return false, false
	}
	if !shouldSendLowBalanceEmail(user.Id, lowBalanceRemindDays(cfg)) {
		return false, true
	}

	lang := userMailLang(user)
	left := common.MailUSD(balance.Dollars())
	line := common.MailUSD(threshold.Dollars())
	subject := common.MailText(lang, "Lazu 余额只剩 "+left, "Your Lazu balance is down to "+left)
	message := common.MailText(lang,
		"余额已低于提醒线 "+line+"，用完后 API 请求会返回 402。",
		"Your balance is under your "+line+" reminder. When it runs out, API requests return 402.")
	if balance <= 0 {
		message = common.MailText(lang, "余额已用完，API 请求会返回 402，充值后立即恢复。", "Your balance has run out. API requests return 402 until you top up.")
	}
	body, err := common.RenderMail(common.Mail{
		Lang: lang, Tier: common.MailInk, Eyebrow: "LOW BALANCE",
		Preheader: common.MailText(lang, "低于提醒线 "+line, "Under your "+line+" reminder"),
		Hero:      left, CompactHero: absDollars(balance) >= 1000,
		Message: message,
		Blocks: []common.MailBlock{
			{Facts: []common.MailFact{
				{Label: common.MailText(lang, "当前余额", "Balance"), Value: left},
				{Label: common.MailText(lang, "提醒线", "Reminder at"), Value: line},
			}},
			{Primary: &common.MailAction{Label: common.MailText(lang, "去充值", "Top up"), URL: userMailLink(user, model.SurfaceLinkWallet)},
				Secondary: &common.MailAction{Label: common.MailText(lang, "调整提醒", "Change reminders"), URL: userMailLink(user, model.SurfaceLinkSettings)}},
		},
	})
	if err != nil {
		common.SysError("render low balance email: " + err.Error())
		return false, false
	}

	common.SafeGo("low-balance-email", func() {
		if err := common.SendMail(common.MailMeta{Kind: common.MailKindBalance, UserID: user.Id}, subject, email, body); err != nil {
			common.SysError(fmt.Sprintf("failed to send low-balance email to %s: %v", common.MaskEmail(email), err))
			handleLowBalanceEmailSendFailure(user.Id)
		} else {
			clearLowBalanceSendFailure(user.Id)
		}
	})
	return true, true
}
