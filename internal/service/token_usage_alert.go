package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// How long a "already told them" marker lives. It only has to outlast the
// period in which the same Key could cross the same threshold against the same
// funding, and the key itself already changes when the Key is topped up.
const tokenUsageAlertTTL = 90 * 24 * time.Hour

var (
	tokenUsageAlertJobs  sync.WaitGroup
	tokenUsageAlertMu    sync.Mutex
	tokenUsageAlertStore = make(map[string]time.Time)
)

/*
checkAndSendTokenUsageAlert mails the payer once when a Key crosses the share of
its own quota that its owner asked to hear about.

This is a different question from the account-level low-balance mail next to it.
That one asks whether the wallet is running out; this one asks whether one Key
is spending more of its allowance than expected — which is how a runaway agent
announces itself while the account balance still looks healthy.

The alert fires once per (Key, threshold, funding). Folding the Key's total
allowance into the marker is what gives "topping up resets it" for free: adding
quota changes the total, which changes the marker, which makes the next crossing
a new event rather than a suppressed repeat. No reset hook, and nothing to go
stale if one is ever forgotten.
*/
func checkAndSendTokenUsageAlert(relayInfo *relaycommon.RelayInfo) {
	if relayInfo == nil || relayInfo.TokenId <= 0 || (relayInfo.TokenUnlimited && relayInfo.TokenUsageAlertPercent <= 0) {
		return
	}
	tokenUsageAlertJobs.Add(1)
	common.SafeGo("token-usage-alert", func() {
		defer tokenUsageAlertJobs.Done()
		token, err := model.GetTokenById(relayInfo.TokenId)
		if err != nil || token == nil {
			return
		}
		percent := token.UsageAlertPercent
		if percent <= 0 || percent > 100 {
			return
		}
		allowance, err := readTokenAlertAllowance(token, time.Now().UTC())
		if err != nil || allowance.total <= 0 {
			return
		}
		usedShare := float64(allowance.used.Int64()) / float64(allowance.total.Int64()) * 100
		if usedShare < float64(percent) {
			return
		}
		recipientSetting, userEmail, err := quotaNotifyRecipient(relayInfo)
		if err != nil || userEmail == "" {
			return
		}
		if !claimTokenUsageAlert(token.Id, percent, allowance.total, allowance.period) {
			return
		}

		name := token.Name
		if name == "" {
			name = token.DisplayKey()
		}
		subject, content, renderErr := tokenUsageAlertMail(common.MailLang(recipientSetting.Language), name, percent, usedShare, allowance)
		if renderErr != nil {
			common.SysError("render token usage alert: " + renderErr.Error())
			releaseTokenUsageAlert(token.Id, percent, allowance.total, allowance.period)
			return
		}
		if sendErr := common.SendMail(common.MailMeta{Kind: common.MailKindKeyUsage}, subject, userEmail, content); sendErr != nil {
			common.SysError("failed to send token usage alert: " + sendErr.Error())
			// Let the next request try again rather than swallowing the alert.
			releaseTokenUsageAlert(token.Id, percent, allowance.total, allowance.period)
		}
	})
}

// tokenUsageAlertMail is an ink-tier mail: the news is the share of the
// Key's allowance already spent.
func tokenUsageAlertMail(lang, name string, percent int, usedShare float64, allowance tokenAlertAllowance) (string, string, error) {
	share := fmt.Sprintf("%d%%", percent)
	used, total, left := renderUSD(allowance.used), renderUSD(allowance.total), renderUSD(allowance.remaining)
	monthly := allowance.period != ""
	subject := common.MailText(lang, "密钥 "+name+" 已用掉 "+share+" 额度", "Key "+name+" has used "+share+" of its allowance")
	message := common.MailText(lang,
		"密钥 "+name+" 已用掉额度的 "+fmt.Sprintf("%.0f%%", usedShare)+"。用完后这把 Key 会自动停用，其他 Key 不受影响。",
		"Key "+name+" has used "+fmt.Sprintf("%.0f%%", usedShare)+" of its allowance. It stops when the allowance runs out; your other Keys keep working.")
	facts := []common.MailFact{
		{Label: common.MailText(lang, "已用", "Used"), Value: used},
		{Label: common.MailText(lang, "额度", "Allowance"), Value: total},
		{Label: common.MailText(lang, "剩余", "Left"), Value: left},
	}
	if monthly {
		subject = common.MailText(lang, "密钥 "+name+" 已用掉 "+share+" 月上限", "Key "+name+" has used "+share+" of its monthly limit")
		message = common.MailText(lang,
			"密钥 "+name+" 已用掉本月上限的 "+fmt.Sprintf("%.0f%%", usedShare)+"。到上限后这把 Key 暂停请求，每月 1 日 00:00 UTC 重置。",
			"Key "+name+" has used "+fmt.Sprintf("%.0f%%", usedShare)+" of this month's limit. At the limit it pauses until the 1st, 00:00 UTC.")
		facts = []common.MailFact{
			{Label: common.MailText(lang, "本月已用", "Used this month"), Value: used},
			{Label: common.MailText(lang, "月上限", "Monthly limit"), Value: total},
			{Label: common.MailText(lang, "剩余", "Left"), Value: left},
		}
	}
	body, err := common.RenderMail(common.Mail{
		Lang: lang, Tier: common.MailInk, Eyebrow: "KEY USAGE",
		Preheader: common.MailText(lang, "已用 "+used+" / "+total, used+" of "+total+" used"),
		Hero:      share, Message: message,
		Blocks: []common.MailBlock{
			{Facts: facts},
			{Primary: &common.MailAction{Label: common.MailText(lang, "调整额度", "Change the limit"), URL: common.MailSiteURL("/console/token")},
				Secondary: &common.MailAction{Label: common.MailText(lang, "查看用量", "View usage"), URL: common.MailSiteURL("/console/usage")}},
		},
	})
	return subject, body, err
}

func renderUSD(value billing.MicroUSD) string {
	return fmt.Sprintf("$%.2f", float64(value.Int64())/float64(billing.MicroUSDPerUSD))
}

func tokenUsageAlertKey(tokenID int, percent int, total billing.MicroUSD, period ...string) string {
	key := fmt.Sprintf("notify:token_usage_alert:%d:%d:%d", tokenID, percent, total.Int64())
	if len(period) > 0 && period[0] != "" {
		key += ":" + period[0]
	}
	return key
}

// claimTokenUsageAlert returns true for exactly one caller per marker.
func claimTokenUsageAlert(tokenID int, percent int, total billing.MicroUSD, period ...string) bool {
	key := tokenUsageAlertKey(tokenID, percent, total, period...)
	if common.RedisEnabled && common.RDB != nil {
		ok, err := common.RDB.SetNX(context.Background(), key, "1", tokenUsageAlertTTL).Result()
		if err == nil {
			return ok
		}
	}
	// Without Redis the marker is process-local, so a multi-process deployment
	// can mail once per process. That is the right failure: a duplicate alert
	// is a nuisance, a missed one is the incident it was meant to prevent.
	tokenUsageAlertMu.Lock()
	defer tokenUsageAlertMu.Unlock()
	now := time.Now()
	for marker, expiry := range tokenUsageAlertStore {
		if !now.Before(expiry) {
			delete(tokenUsageAlertStore, marker)
		}
	}
	if expiry, seen := tokenUsageAlertStore[key]; seen && now.Before(expiry) {
		return false
	}
	tokenUsageAlertStore[key] = now.Add(tokenUsageAlertTTL)
	return true
}

func releaseTokenUsageAlert(tokenID int, percent int, total billing.MicroUSD, period ...string) {
	key := tokenUsageAlertKey(tokenID, percent, total, period...)
	if common.RedisEnabled {
		_ = common.RedisDel(key)
	}
	tokenUsageAlertMu.Lock()
	delete(tokenUsageAlertStore, key)
	tokenUsageAlertMu.Unlock()
}

// Monthly team caps and lifetime personal quota have different denominators.
// This runs asynchronously, and only for keys that enabled usage notifications.
type tokenAlertAllowance struct {
	used, total, remaining billing.MicroUSD
	period                 string
}

func readTokenAlertAllowance(token *model.Token, now time.Time) (tokenAlertAllowance, error) {
	if !token.UnlimitedQuota {
		return tokenAlertAllowance{used: token.UsedQuota, total: token.UsedQuota + token.RemainQuota, remaining: token.RemainQuota}, nil
	}
	if token.ProjectID <= 0 {
		return tokenAlertAllowance{}, nil
	}
	var budgets []model.SpendBudget
	err := model.CoreDB().Where("project_id = ? AND token_id = ? AND scope = ? AND period = ? AND enabled = ?", token.ProjectID, token.Id, model.SpendBudgetScopeToken, model.SpendBudgetPeriodMonth, true).Order("id ASC").Limit(1).Find(&budgets).Error
	if err != nil || len(budgets) == 0 {
		return tokenAlertAllowance{}, err
	}
	budget := budgets[0]
	used, err := model.ReadSpendBudgetUsed(budget, now)
	if err != nil {
		return tokenAlertAllowance{}, err
	}
	remaining := budget.LimitMicrousd - used
	if remaining < 0 {
		remaining = 0
	}
	return tokenAlertAllowance{used: used, total: budget.LimitMicrousd, remaining: remaining, period: now.UTC().Format("2006-01")}, nil
}
