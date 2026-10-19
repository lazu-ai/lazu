package service

import (
	"fmt"
	"html"
	"html/template"
	"strings"
	"sync"
	"sync/atomic"

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

type EmailSendStats struct {
	Attempted int `json:"attempted"`
	Sent      int `json:"sent"`
	Skipped   int `json:"skipped"`
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
			Text:      common.MailText(lang, "这是 Lazu 的产品更新，一年只有几封。", "This is a Lazu product update; there are only a few a year."),
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
	if len(userIDs) > 0 {
		recipients, err := loadRecipientsByIDs(userIDs)
		if err != nil {
			return stats, err
		}
		return sendEmailsToRecipients(notice, recipients)
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
		partStats, err := sendEmailsToRecipients(notice, recipients)
		if err != nil {
			return stats, err
		}
		stats.Attempted += partStats.Attempted
		stats.Sent += partStats.Sent
		stats.Skipped += partStats.Skipped
		if len(recipients) < batchSize {
			break
		}
		offset += batchSize
	}

	return stats, nil
}

func sendEmailsToRecipients(notice platformNotice, recipients []emailRecipient) (EmailSendStats, error) {
	stats := EmailSendStats{}
	if len(recipients) == 0 {
		return stats, nil
	}

	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	var sent int32
	var skipped int32

	for _, r := range recipients {
		email := resolveEmailForRecipient(r)
		if notice.ProductUpdate && (&model.User{Setting: r.Setting}).GetSetting().ProductUpdatesOff {
			atomic.AddInt32(&skipped, 1)
			continue
		}
		if email == "" {
			atomic.AddInt32(&skipped, 1)
			continue
		}
		stats.Attempted++
		sem <- struct{}{}
		wg.Add(1)
		rec, receiver := r, email
		common.SafeGo("email-broadcast-send", func() {
			defer wg.Done()
			defer func() { <-sem }()
			subj, body, err := renderPlatformNotice(notice, rec, receiver)
			if err != nil {
				common.SysError("render platform notice: " + err.Error())
				return
			}
			kind := common.MailKindNotice
			var headers map[string]string
			if notice.ProductUpdate {
				headers = MailUnsubscribeHeaders(rec.Id, MailOptOutProduct)
			}
			if err := common.SendMail(common.MailMeta{Kind: kind, UserID: rec.Id, Headers: headers}, subj, receiver, body); err == nil {
				atomic.AddInt32(&sent, 1)
			} else {
				common.SysError(fmt.Sprintf("failed to send broadcast email to %s: %v", common.MaskEmail(receiver), err))
			}
		})
	}

	wg.Wait()
	stats.Sent = int(sent)
	stats.Skipped += int(skipped)
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
