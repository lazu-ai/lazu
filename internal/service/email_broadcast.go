package service

import (
	"errors"
	"fmt"
	"html"
	"html/template"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
)

type EmailBroadcastRequest struct {
	Subject string
	Content string
	UserIDs []int
	SendAll bool
}

// EmailSendStats accounts for every reader a bulk send loaded: each one is
// skipped, attempted or unsent, and each attempt is sent or failed.
type EmailSendStats struct {
	Attempted int `json:"attempted"`
	Sent      int `json:"sent"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	// Unsent counts readers never tried because the run stopped early.
	Unsent int `json:"unsent"`
	// Stopped says why the run stopped early; empty when it ran to the end.
	Stopped string `json:"stopped,omitempty"`
}

// EmailSendStoppedQuota: the transport refused a send because its daily or
// monthly sending quota is used up, so the rest were not tried.
const EmailSendStoppedQuota = "quota_exceeded"

func (s *EmailSendStats) add(part EmailSendStats) {
	s.Attempted += part.Attempted
	s.Sent += part.Sent
	s.Failed += part.Failed
	s.Skipped += part.Skipped
	s.Unsent += part.Unsent
	if s.Stopped == "" {
		s.Stopped = part.Stopped
	}
}

// Bulk sends stay well inside the transport's limits so mail sent at the
// same time (sign-in codes, password resets) is not refused for them: at
// most bulkMailConcurrency requests in flight, started no faster than
// email_setting.BulkSendRatePerSecond, and a send refused for the rate
// limit is tried again with backoff.
const (
	bulkMailConcurrency      = 2
	bulkMailRateLimitRetries = 3
)

// sendNoticeMail hands one bulk mail to the transport; tests replace it.
var sendNoticeMail = common.SendMail

// bulkMailPacer is shared by every bulk run in the process, so two runs at
// once split the rate rather than each taking all of it.
var bulkMailPacer = &mailPacer{now: time.Now, sleep: time.Sleep}

// mailPacer spaces sends at least 1/perSecond apart.
type mailPacer struct {
	mu    sync.Mutex
	next  time.Time
	now   func() time.Time
	sleep func(time.Duration)
}

// wait blocks until the caller's slot comes up.
func (p *mailPacer) wait(perSecond float64) {
	if perSecond <= 0 {
		perSecond = email_setting.DefaultBulkSendRatePerSecond
	}
	interval := time.Duration(float64(time.Second) / perSecond)
	p.mu.Lock()
	now := p.now()
	slot := p.next
	if slot.Before(now) {
		slot = now
	}
	p.next = slot.Add(interval)
	p.mu.Unlock()
	if delay := slot.Sub(now); delay > 0 {
		p.sleep(delay)
	}
}

// bulkMailRun is shared by every batch of one bulk send: once a send hits
// the quota, the later batches are counted as unsent instead of tried.
type bulkMailRun struct {
	stopped atomic.Bool
}

type emailRecipient struct {
	Id          int
	Username    string
	DisplayName string
	Email       string
	Setting     string
	Quota       billing.MicroUSD
}

func SendBulkEmail(req EmailBroadcastRequest) (EmailSendStats, error) {
	stats := EmailSendStats{}
	if req.Subject == "" || req.Content == "" {
		field := "subject"
		if req.Subject != "" {
			field = "content"
		}
		return stats, types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": field})
	}

	cfg := email_setting.GetEmailSetting()
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 200
	}

	// Broadcast bodies are written by a platform administrator (RootAuth) and
	// may carry markup.
	notice := platformNotice{Title: req.Subject, Body: template.HTML(req.Content)}
	return dispatchEmails(notice, req.UserIDs, req.SendAll, batchSize)
}

// platformNotice is the one mail an operator sends to many accounts:
// announcements and broadcasts share it.
type platformNotice struct {
	Title string
	Body  template.HTML
	Link  string
	// ProductUpdate marks an announcement: readers who turned product
	// updates off are skipped and the mail carries a one-click opt-out.
	ProductUpdate bool
}

// noticePlaceholders are the personal fields a broadcast body may use.
func renderPlatformNotice(notice platformNotice, rec emailRecipient, receiver string) (string, string, error) {
	lang := common.MailLang((&model.User{Setting: rec.Setting}).GetSetting().Language)
	name := strings.TrimSpace(rec.DisplayName)
	if name == "" {
		name = rec.Username
	}
	body := string(notice.Body)
	for key, value := range map[string]string{
		"{{display_name}}": name, "{{username}}": rec.Username, "{{email}}": receiver,
		"{{balance}}": common.MailUSD(rec.Quota.Dollars()),
	} {
		body = strings.ReplaceAll(body, key, html.EscapeString(value))
	}
	blocks := []common.MailBlock{{HTML: template.HTML(body)}}
	if notice.Link != "" {
		blocks = append(blocks, common.MailBlock{Primary: &common.MailAction{Label: common.MailText(lang, "查看详情", "Read more"), URL: notice.Link}})
	}
	if notice.ProductUpdate {
		blocks = append(blocks, common.MailBlock{Muted: true,
			Text:      common.MailText(lang, "这是 Lazu 的产品通知，只在模型上架、下架等重要变更时发送。", "This is a Lazu product notice, sent only for important changes such as model launches and retirements."),
			LinkLabel: common.MailText(lang, "不再接收", "Unsubscribe"), LinkURL: MailUnsubscribeURL(rec.Id, MailOptOutProduct)})
	}
	rendered, err := common.RenderMail(common.Mail{
		Lang: lang, Tier: common.MailPaper, Eyebrow: "NOTICE",
		Preheader: notice.Title, Title: notice.Title, Blocks: blocks,
	})
	return notice.Title, rendered, err
}

func dispatchEmails(notice platformNotice, userIDs []int, sendAll bool, batchSize int) (EmailSendStats, error) {
	stats := EmailSendStats{}
	run := &bulkMailRun{}
	if len(userIDs) > 0 {
		recipients, err := loadRecipientsByIDs(userIDs)
		if err != nil {
			return stats, err
		}
		stats, err = sendEmailsToRecipients(notice, recipients, run)
		if err == nil {
			logBulkSendStats(notice, stats)
		}
		return stats, err
	}

	if !sendAll {
		return stats, types.Err(types.ErrorCodeEmailRecipientsRequired)
	}

	offset := 0
	for {
		recipients, err := loadRecipientsBatch(offset, batchSize)
		if err != nil {
			return stats, err
		}
		if len(recipients) == 0 {
			break
		}
		partStats, err := sendEmailsToRecipients(notice, recipients, run)
		if err != nil {
			return stats, err
		}
		stats.add(partStats)
		if len(recipients) < batchSize {
			break
		}
		offset += batchSize
	}

	logBulkSendStats(notice, stats)
	return stats, nil
}

// logBulkSendStats records the outcome of a finished run. The routes that
// start a run answer only when it ends, which at the paced rate can be long
// after a client or proxy gave up waiting; the log keeps the result.
func logBulkSendStats(notice platformNotice, stats EmailSendStats) {
	line := fmt.Sprintf("bulk email %q: attempted %d, sent %d, failed %d, skipped %d, unsent %d",
		notice.Title, stats.Attempted, stats.Sent, stats.Failed, stats.Skipped, stats.Unsent)
	if stats.Stopped != "" {
		line += ", stopped: " + stats.Stopped
	}
	common.SysLog(line)
}

func sendEmailsToRecipients(notice platformNotice, recipients []emailRecipient, run *bulkMailRun) (EmailSendStats, error) {
	stats := EmailSendStats{}
	if len(recipients) == 0 {
		return stats, nil
	}

	sem := make(chan struct{}, bulkMailConcurrency)
	var wg sync.WaitGroup
	var sent int32
	var failed int32
	var skipped int32
	ratePerSecond := email_setting.GetEmailSetting().BulkSendRatePerSecond

	for i, r := range recipients {
		if run.stopped.Load() {
			stats.Unsent += len(recipients) - i
			break
		}
		email := resolveEmailForRecipient(r)
		if notice.ProductUpdate && (&model.User{Setting: r.Setting}).GetSetting().ProductUpdatesOff {
			atomic.AddInt32(&skipped, 1)
			continue
		}
		if email == "" {
			atomic.AddInt32(&skipped, 1)
			continue
		}
		sem <- struct{}{}
		// A send that finished while this one waited for a slot may have
		// found the quota used up.
		if run.stopped.Load() {
			<-sem
			stats.Unsent += len(recipients) - i
			break
		}
		bulkMailPacer.wait(ratePerSecond)
		stats.Attempted++
		wg.Add(1)
		rec, receiver := r, email
		common.SafeGo("email-broadcast-send", func() {
			defer wg.Done()
			defer func() { <-sem }()
			subj, body, err := renderPlatformNotice(notice, rec, receiver)
			if err != nil {
				atomic.AddInt32(&failed, 1)
				common.SysError("render platform notice: " + err.Error())
				return
			}
			kind := common.MailKindNotice
			var headers map[string]string
			if notice.ProductUpdate {
				headers = MailUnsubscribeHeaders(rec.Id, MailOptOutProduct)
			}
			meta := common.MailMeta{Kind: kind, UserID: rec.Id, Headers: headers, RateLimitRetries: bulkMailRateLimitRetries}
			if err := sendNoticeMail(meta, subj, receiver, body); err == nil {
				atomic.AddInt32(&sent, 1)
			} else {
				atomic.AddInt32(&failed, 1)
				if errors.Is(err, common.ErrMailQuotaExceeded) && run.stopped.CompareAndSwap(false, true) {
					common.SysError(fmt.Sprintf("broadcast email stopped: the mail transport's sending quota is used up: %v", err))
				}
				common.SysError(fmt.Sprintf("failed to send broadcast email to %s: %v", common.MaskEmail(receiver), err))
			}
		})
	}

	wg.Wait()
	stats.Sent = int(sent)
	stats.Failed = int(failed)
	stats.Skipped += int(skipped)
	if run.stopped.Load() {
		stats.Stopped = EmailSendStoppedQuota
	}
	return stats, nil
}

func resolveEmailForRecipient(rec emailRecipient) string {
	user := &model.User{
		Id:          rec.Id,
		Username:    rec.Username,
		DisplayName: rec.DisplayName,
		Email:       rec.Email,
		Setting:     rec.Setting,
	}
	return resolveUserEmail(user)
}

func loadRecipientsBatch(offset, limit int) ([]emailRecipient, error) {
	var users []emailRecipient
	err := model.CoreDB().Model(&model.User{}).
		Select("id, username, display_name, email, setting, quota").
		Order("id asc").
		Limit(limit).
		Offset(offset).
		Find(&users).Error
	return users, err
}

func loadRecipientsByIDs(userIDs []int) ([]emailRecipient, error) {
	var users []emailRecipient
	err := model.CoreDB().Model(&model.User{}).
		Select("id, username, display_name, email, setting, quota").
		Where("id IN ?", userIDs).
		Find(&users).Error
	return users, err
}

type AnnouncementEmail struct {
	Title   string
	Content string
	Link    string
}

func SendAnnouncementEmail(payload AnnouncementEmail, userIDs []int, sendAll bool) (EmailSendStats, error) {
	cfg := email_setting.GetEmailSetting()
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 200
	}
	// Announcement text comes from the console's announcement list: plain
	// text, so it is escaped and its line breaks kept.
	body := strings.ReplaceAll(html.EscapeString(payload.Content), "\n", "<br>")
	notice := platformNotice{Title: payload.Title, Body: template.HTML(body), Link: payload.Link, ProductUpdate: true}
	return dispatchEmails(notice, userIDs, sendAll, batchSize)
}
