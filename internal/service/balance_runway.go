package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// The runway check asks a different question from the fixed threshold: not
// "is the balance under $20" but "how many days of this account's own spend
// does the balance still cover". A heavy account can run out within a day
// of crossing a fixed line; the runway alert reaches it, and the operator
// who may need to credit it, days earlier.

const (
	// BalanceRunwayWindow is how far back daily spend is averaged.
	BalanceRunwayWindow = 7 * 24 * time.Hour
	// balanceRunwayMinHistory is how much spend history an estimate needs.
	// Under a day, one burst would be read as a daily rate; those accounts
	// stay with the fixed-threshold mail until they have a day behind them.
	balanceRunwayMinHistory = 24 * time.Hour
	// BalanceRunwayAlertEvery is the least time between two runway alerts
	// for one account, kept in the mail archive so restarts respect it.
	BalanceRunwayAlertEvery = 24 * time.Hour
	// balanceRunwayTemplate marks the archive rows of this alert.
	balanceRunwayTemplate = "balance-runway"
)

// BalanceRunway is how long a balance lasts at the account's recent rate.
type BalanceRunway struct {
	Balance billing.MicroUSD
	// DailySpend is the average spend per day over the history used. Zero
	// with Known false when there is not enough history.
	DailySpend billing.MicroUSD
	// DaysLeft is Balance / DailySpend; 0 for a balance at or under zero.
	DaysLeft float64
	// Known is false when the estimate could not be made.
	Known bool
}

// EstimateBalanceRunway averages spend inside the window over the days it
// covers: the whole window for an account that was spending throughout,
// only the days since its first spend for a younger one. firstSpend is when
// the earliest spend in the window happened; less than a day of history
// yields no estimate.
func EstimateBalanceRunway(balance, windowSpend billing.MicroUSD, firstSpend, now time.Time, window time.Duration) BalanceRunway {
	r := BalanceRunway{Balance: balance}
	if windowSpend <= 0 || firstSpend.IsZero() || window <= 0 {
		return r
	}
	history := now.Sub(firstSpend)
	if history < balanceRunwayMinHistory {
		return r
	}
	if history > window {
		history = window
	}
	days := history.Hours() / 24
	r.DailySpend = billing.MicroUSD(math.Round(float64(windowSpend) / days))
	if r.DailySpend <= 0 {
		return r
	}
	r.Known = true
	if balance > 0 {
		r.DaysLeft = float64(balance) / float64(r.DailySpend)
	}
	return r
}

// BalanceRunwayAlertDue says whether an account needs the alert: its
// estimated runway is under runwayDays (a balance at zero with a day of
// spend behind it has a runway of zero), or its balance is negative. An
// account without an estimate and with money left is the fixed threshold's
// to warn, and so is a trial account that spent its first cents to zero
// within a day: neither is the operator's business yet.
func BalanceRunwayAlertDue(r BalanceRunway, runwayDays int) bool {
	if runwayDays <= 0 {
		return false
	}
	if r.Balance < 0 {
		return true
	}
	return r.Known && r.DaysLeft < float64(runwayDays)
}

// RunBalanceRunwayAlerts checks every enabled account that spent anything in
// the window and alerts the ones whose balance will not last. It runs
// hourly; the mail archive keeps each account to one alert a day across
// restarts and instances.
func RunBalanceRunwayAlerts(ctx context.Context, now time.Time) error {
	cfg := email_setting.GetEmailSetting()
	runwayDays := cfg.LowBalanceRunwayDays
	if runwayDays <= 0 || model.CoreDB() == nil || model.TelemetryDB() == nil {
		return nil
	}
	spend, err := model.PayerSpendBetween(now.Add(-BalanceRunwayWindow), now)
	if err != nil {
		return fmt.Errorf("balance runway: read spend: %w", err)
	}
	if len(spend) == 0 {
		return nil
	}
	byPayer := make(map[int]model.PayerSpendWindow, len(spend))
	ids := make([]int, 0, len(spend))
	for _, s := range spend {
		if s.PayerUserID <= 0 {
			continue
		}
		byPayer[s.PayerUserID] = s
		ids = append(ids, s.PayerUserID)
	}
	sort.Ints(ids)
	users, err := model.ListEnabledUsersByIDs(ids)
	if err != nil {
		return fmt.Errorf("balance runway: load accounts: %w", err)
	}
	for _, user := range users {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := byPayer[user.Id]
		runway := EstimateBalanceRunway(user.Quota, s.Spend, time.Unix(s.FirstAt, 0), now, BalanceRunwayWindow)
		if !BalanceRunwayAlertDue(runway, runwayDays) {
			continue
		}
		if err := sendBalanceRunwayAlert(user, runway, runwayDays, now); err != nil {
			common.SysError(fmt.Sprintf("balance runway alert for user %d: %v", user.Id, err))
		}
	}
	return nil
}

// sendBalanceRunwayAlert claims the day's archive row for the account, mails
// the account when low-balance mail is on and it has an address, and tells
// the operator either way.
func sendBalanceRunwayAlert(user *model.User, runway BalanceRunway, runwayDays int, now time.Time) error {
	recently, err := model.MailDeliverySentSince(user.Id, balanceRunwayTemplate, now.Add(-BalanceRunwayAlertEvery))
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	if recently {
		return nil
	}
	email := resolveUserEmail(user)
	lang := userMailLang(user)
	key := fmt.Sprintf("%s:%d:%d", balanceRunwayTemplate, user.Id, now.Unix()/3600)
	row := &model.MailDelivery{Kind: common.MailKindBalance, UserID: user.Id, Receiver: email, RefKey: &key,
		Template: balanceRunwayTemplate, Lang: lang, CreatedAt: now.Unix()}
	claimed, err := model.ClaimMailDelivery(row)
	if err != nil {
		return fmt.Errorf("claim archive row: %w", err)
	}
	if !claimed {
		return nil
	}

	mailed, reason := false, ""
	switch {
	case runway.Balance <= 0:
		// An exhausted account is already on the fixed path's reminder
		// schedule; a second daily mail saying the same would only pile up.
		// The runway alert's job here is the operator line.
		reason = "exhausted_fixed_mail_owns_it"
	case !email_setting.GetEmailSetting().LowBalanceEnabled:
		reason = "low_balance_mail_disabled"
	case email == "":
		reason = "no_email"
	default:
		subject, body, renderErr := lowBalanceMail(user, runway.Balance, 0, &runway)
		if renderErr != nil {
			reason = "render_failed"
			break
		}
		if err := model.SaveMailDeliveryContent(row.ID, subject, body, ""); err != nil {
			common.SysError(fmt.Sprintf("archive balance runway email for user %d: %v", user.Id, err))
		}
		meta := common.MailMeta{Kind: common.MailKindBalance, UserID: user.Id, DeliveryID: row.ID, Template: balanceRunwayTemplate, Lang: lang}
		if sendErr := common.SendMail(meta, subject, email, body); sendErr != nil {
			common.SysError(fmt.Sprintf("failed to send balance runway email to %s: %v", common.MaskEmail(email), sendErr))
			reason = "send_failed"
		} else {
			mailed = true
		}
	}
	if !mailed && reason != "send_failed" {
		// The row still records that the account was alerted today; the
		// reason tells the ops list why no mail went out.
		model.TelemetryDB().Model(&model.MailDelivery{}).Where("id = ?", row.ID).
			Updates(map[string]any{"status": model.MailStatusSkipped, "error": reason})
	}

	subject, mail := balanceRunwayOpsMail(user, runway, runwayDays, mailed, reason)
	if err := SendOpsMail(subject, mail); err != nil {
		return fmt.Errorf("ops mail: %w", err)
	}
	return nil
}

// balanceRunwayOpsMail is the operator's one line per account: who, how much
// is left, how fast it goes and how long that lasts. Ops mail is Chinese
// only (brand/foundations/emails.md § 3).
func balanceRunwayOpsMail(user *model.User, runway BalanceRunway, runwayDays int, mailed bool, reason string) (string, common.Mail) {
	account := fmt.Sprintf("%s (#%d)", strings.TrimSpace(user.Username), user.Id)
	balance := common.MailUSD(runway.Balance.Dollars())
	daysLeft := balanceRunwayDaysText(runway, common.MailZH)
	var subject, lead string
	if runway.Balance <= 0 {
		subject = fmt.Sprintf("余额已用完：%s", account)
		lead = "这个账户的余额已经用完，它的 API 请求正在返回 402。靠人工入账的账户需要有人处理。"
	} else {
		subject = fmt.Sprintf("余额告急：%s 约剩 %s", account, daysLeft)
		lead = fmt.Sprintf("按最近 7 天的日均消耗，这个账户的余额撑不过 %d 天。靠人工入账的账户需要提前处理。", runwayDays)
	}
	userMail := "已发送"
	if !mailed {
		userMail = "未发送（" + reason + "）"
	}
	facts := []common.MailFact{
		{Label: "账户", Value: account, Mono: true},
		{Label: "当前余额", Value: balance},
	}
	if runway.Known {
		facts = append(facts, common.MailFact{Label: "日均消耗（近 7 天）", Value: common.MailUSD(runway.DailySpend.Dollars())})
	}
	facts = append(facts,
		common.MailFact{Label: "预计可用", Value: daysLeft},
		common.MailFact{Label: "用户邮件", Value: userMail},
		common.MailFact{Label: "下次提醒", Value: "仍满足条件时，24 小时后"},
	)
	return subject, common.Mail{Severity: "WARN", Source: "balance", Title: subject, Preheader: balance + " · " + daysLeft, Lead: lead,
		Blocks: []common.MailBlock{{Facts: facts}}}
}

// balanceRunwayDaysText says how long the balance lasts, for a reader.
func balanceRunwayDaysText(r BalanceRunway, lang string) string {
	switch {
	case r.Balance <= 0:
		return common.MailText(lang, "已用完", "none")
	case !r.Known:
		return common.MailText(lang, "未知", "unknown")
	case r.DaysLeft < 1:
		return common.MailText(lang, "不到 1 天", "under a day")
	case r.DaysLeft >= 100:
		return common.MailText(lang, fmt.Sprintf("%.0f 天", r.DaysLeft), fmt.Sprintf("%.0f days", r.DaysLeft))
	default:
		return common.MailText(lang, fmt.Sprintf("%.1f 天", r.DaysLeft), fmt.Sprintf("%.1f days", r.DaysLeft))
	}
}
