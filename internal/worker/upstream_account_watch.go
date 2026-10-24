package worker

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

// UpstreamAccountWatchTask tells the operator when a channel's upstream
// keeps refusing attempts for an account reason: the account cannot pay
// (billing) or its key is rejected (credentials). The gateway fails over, so
// customers mostly still get answers, but from a slower or dearer channel,
// and nothing else says so until the fallback runs out too.
type UpstreamAccountWatchTask struct{}

const (
	upstreamAccountWindow    = 15 * time.Minute
	upstreamAccountThreshold = 3
	upstreamAccountAlertTTL  = 6 * time.Hour
)

// upstreamAccountReasons maps each watched code to its alert wording.
var upstreamAccountReasons = map[string]struct{ title, lead string }{
	string(types.ErrorCodeUpstreamBillingFailed): {
		title: "计费失败",
		lead:  "上游以付款或额度原因拒绝了请求（余额用完、额度到顶或结算账户被停）。网关会改走其他渠道，用户多半仍能得到回答，但可能更慢或更贵。请到这个渠道的上游后台检查账户余额和结算状态。",
	},
	string(types.ErrorCodeUpstreamAuthFailed): {
		title: "鉴权失败",
		lead:  "上游拒绝了这个渠道的密钥（失效、被吊销或没有权限）。网关会改走其他渠道。请检查并更换这个渠道的密钥。",
	},
	string(types.ErrorCodeChannelInvalidKey): {
		title: "鉴权失败",
		lead:  "上游拒绝了这个渠道的密钥（失效、被吊销或没有权限）。网关会改走其他渠道。请检查并更换这个渠道的密钥。",
	},
}

func NewUpstreamAccountWatchTask() *UpstreamAccountWatchTask { return &UpstreamAccountWatchTask{} }
func (*UpstreamAccountWatchTask) Name() string               { return "upstream_account_watch" }
func (*UpstreamAccountWatchTask) Interval() time.Duration    { return 5 * time.Minute }

func (t *UpstreamAccountWatchTask) Run(ctx context.Context) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil // alerts are deduplicated through Redis leases
	}
	codes := make([]string, 0, len(upstreamAccountReasons))
	for code := range upstreamAccountReasons {
		codes = append(codes, code)
	}
	rows, err := model.RecentUpstreamAccountFailures(ctx, time.Now().Add(-upstreamAccountWindow), codes)
	if err != nil {
		return err
	}
	// Both auth codes describe one condition; count them together.
	type key struct {
		channel int
		title   string
	}
	merged := map[key]model.UpstreamAccountFailure{}
	for _, r := range rows {
		k := key{r.ChannelID, upstreamAccountReasons[r.ErrorCode].title}
		m := merged[k]
		if m.Failures == 0 {
			m = r
		} else {
			m.Failures += r.Failures
			if r.LastMs > m.LastMs {
				m.LastMs, m.StatusCode = r.LastMs, r.StatusCode
			}
		}
		merged[k] = m
	}
	for _, f := range merged {
		if f.Failures < upstreamAccountThreshold {
			continue
		}
		if err := alertUpstreamAccount(ctx, f); err != nil {
			common.SysLog("upstream account alert for channel " + strconv.Itoa(f.ChannelID) + ": " + err.Error())
		}
	}
	return nil
}

func alertUpstreamAccount(ctx context.Context, f model.UpstreamAccountFailure) error {
	reason := upstreamAccountReasons[f.ErrorCode]
	name, lane := "渠道 #"+strconv.Itoa(f.ChannelID), ""
	if ch, err := model.GetChannelById(f.ChannelID, false); err == nil && ch != nil {
		name, lane = ch.Name, ch.Group
	}
	subject := "上游渠道" + reason.title + "：" + name
	facts := []common.MailFact{
		{Label: "渠道", Value: fmt.Sprintf("%s（#%d）", name, f.ChannelID)},
		{Label: "近 15 分钟失败", Value: fmt.Sprintf("%d 次", f.Failures)},
		{Label: "最近一次", Value: time.UnixMilli(f.LastMs).UTC().Format("2006-01-02 15:04 UTC")},
		{Label: "上游状态码", Value: strconv.Itoa(f.StatusCode), Mono: true},
		{Label: "下次提醒", Value: "仍失败时，至多每 6 小时一次"},
	}
	if lane != "" {
		facts = append(facts[:1], append([]common.MailFact{{Label: "线路", Value: lane, Mono: true}}, facts[1:]...)...)
	}
	mail := common.Mail{
		Severity:  "ALERT",
		Source:    "channel",
		Title:     subject,
		Preheader: fmt.Sprintf("近 15 分钟 %d 次", f.Failures),
		Lead:      reason.lead,
		Blocks:    []common.MailBlock{{Facts: facts}},
	}
	lease := fmt.Sprintf("upstream-account:%s:%d", reason.title, f.ChannelID)
	return sendLeasedOpsMail(ctx, lease, upstreamAccountAlertTTL, subject, mail)
}
