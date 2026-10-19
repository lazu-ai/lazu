package service

import (
	"context"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func init() {
	model.SetLedgerAlertNotifier(func(notice model.LedgerAlertNotice) error {
		subject, mail := ledgerAlertMail(notice)
		return SendOpsMail(subject, mail)
	})
	model.SetLedgerSettlementNotifier(notifyCommittedLedgerCharges)
}

// ledgerAlertMail turns a reconciler finding into an ops mail that names the
// account, Key or budget and the amounts on each side, so the operator can
// act without decoding a key=value line.
func ledgerAlertMail(notice model.LedgerAlertNotice) (string, common.Mail) {
	kind, id, _ := strings.Cut(notice.Key, ":")
	drift := false
	var title string
	switch kind {
	case "budget":
		title, drift = "预算 #"+id+" 两本账不一致", true
	case "balance":
		title, drift = "账户 #"+id+" 余额两本账不一致", true
	case "key":
		title, drift = "Key #"+id+" 额度两本账不一致", true
	case "negative":
		title = "账户 #" + id + " 余额为负"
	case "backlog":
		title = "账本事件积压未入库"
	case "dead-letter-24h":
		title = "账本死信超过 24 小时"
	case "spool-quarantine":
		title = "账本 spool 有隔离文件"
	case "redis-memory":
		title = "Redis 内存占用过高"
	case "redis-aof":
		title = "Redis AOF 持久化异常"
	default:
		title = "账本需要检查"
	}
	mail := common.Mail{Severity: "ALERT", Source: "ledger", Title: title, Preheader: notice.Key}
	facts := make([]common.MailFact, 0, len(notice.Facts)+2)
	for _, fact := range notice.Facts {
		facts = append(facts, common.MailFact{Label: fact.Label, Value: fact.Value, Mono: fact.Mono})
	}
	facts = append(facts, common.MailFact{Label: "检查项", Value: notice.Key, Mono: true})
	if drift {
		mail.Lead = "Redis 热账本和数据库算出的数不一致。对账器只报告、不改钱：请按下面的数字查明哪一边少记了。"
		facts = append(facts, common.MailFact{Label: "下次提醒", Value: "差额变化时（至多每小时一次），否则 24 小时后"})
		mail.Blocks = []common.MailBlock{{Facts: facts}, {Snippet: notice.Message}}
		return "账本：" + title, mail
	}
	mail.Lead = notice.Message
	mail.Blocks = []common.MailBlock{{Facts: facts}}
	return "账本：" + title, mail
}

// Called by the background writer after its SQL batch commits. Both alert
// helpers enqueue their existing asynchronous delivery work, preserving their
// threshold, opt-in and deduplication behavior without request-path SQL.
func notifyCommittedLedgerCharges(events []ledger.Event) {
	notifyCommittedLedgerChargesWithEngine(events, model.GatewayLedger())
}

func notifyCommittedLedgerChargesWithEngine(events []ledger.Event, engine *ledger.Engine) {
	type payerToken struct{ payer, token int }
	pairs := make(map[payerToken]bool, len(events))
	unique := make([]ledger.Event, 0, len(events))
	for _, event := range events {
		if event.Kind != "settle" || event.Amount <= 0 || event.PayerID <= 0 {
			continue
		}
		pair := payerToken{event.PayerID, event.TokenID}
		if !pairs[pair] {
			pairs[pair] = true
			unique = append(unique, event)
		}
	}
	users := map[int]*model.User{}
	balances := map[int]billing.MicroUSD{}
	payers := map[int]bool{}
	for _, event := range unique {
		payer := users[event.PayerID]
		if !payers[event.PayerID] {
			// Cache attempted lookups too: a missing payer or unavailable
			// balance must not produce one SQL lookup for every batch event.
			payers[event.PayerID] = true
			var err error
			payer, err = model.GetUserById(event.PayerID, true)
			if err != nil {
				common.SysError("ledger notification payer: " + err.Error())
				continue
			}
			remaining := payer.Quota
			if engine != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				available, err := engine.ReadBalances(ctx, []int{event.PayerID})
				if err == nil {
					if value, ok := available[event.PayerID]; ok {
						remaining = billing.MicroUSD(value)
					} else {
						var snapshot ledger.Snapshot
						snapshot, err = engine.Storage.Snapshot(ctx, ledger.Input{PayerID: event.PayerID, Unlimited: true})
						remaining = billing.MicroUSD(snapshot.Balance)
					}
				}
				cancel()
				if err != nil {
					common.SysError("ledger notification available balance: " + err.Error())
					continue
				}
			}
			users[event.PayerID], balances[event.PayerID] = payer, remaining
			info := &relaycommon.RelayInfo{UserId: event.PayerID, UserEmail: payer.Email, UserSetting: payer.GetSetting(), UserQuota: remaining}
			// UserQuota is already available after this batch, including newer
			// Redis holds. Do not subtract the committed charge a second time.
			checkAndSendQuotaNotify(info, 0, 0)
		}
		if payer == nil {
			continue
		}
		if event.TokenID > 0 {
			info := &relaycommon.RelayInfo{UserId: event.PayerID, ActorUserID: event.ActorID, PayerUserID: event.PayerID, ProjectID: event.ProjectID, TokenId: event.TokenID, RequestID: event.RequestID, UserEmail: payer.Email, UserSetting: payer.GetSetting(), UserQuota: balances[event.PayerID]}
			token, err := model.GetTokenById(event.TokenID)
			if err != nil {
				continue
			}
			info.TokenUnlimited, info.TokenUsageAlertPercent = token.UnlimitedQuota, token.UsageAlertPercent
			checkAndSendTokenUsageAlert(info)
		}
	}
}
