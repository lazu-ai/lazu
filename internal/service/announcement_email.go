package service

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/internal/types"
)

const announcementMailTemplate = "announcement_v1"

// AnnouncementMailable reports whether an announcement of this kind may be
// mailed when it is published. Release notes go out with every deploy and
// stay in the console.
func AnnouncementMailable(kind string) bool {
	switch strings.TrimSpace(kind) {
	case model.AnnouncementKindModelUpdate, model.AnnouncementKindOperational,
		model.AnnouncementKindMaintenance, model.AnnouncementKindIncident:
		return true
	}
	return false
}

// announcementMailRefKey is the archive key that lets an announcement reach
// each reader once, however often it is published again.
func announcementMailRefKey(announcementID uint, userID int) string {
	return fmt.Sprintf("announcement:%d:%d", announcementID, userID)
}

// announcementMailContent is the subject and body a reader gets: their own
// language when the announcement has it, else its default locale. A reader
// who never chose a language reads the default locale.
func announcementMailContent(a model.Announcement, language string) (string, template.HTML) {
	locale := strings.TrimSpace(language)
	if locale == "" {
		locale = a.DefaultLocale
	}
	view := a.ToView(locale, nil)
	text := view.Summary
	if len(view.Body) > 0 {
		text = strings.Join(view.Body, "\n\n")
	}
	return view.Title, plainTextMailBody(text)
}

// SendPublishedAnnouncementEmail mails a published announcement to every
// account that has an address and has not turned product updates off, each
// in the reader's language. Every delivery is claimed in the mail archive
// first, so publishing the same announcement again skips readers it already
// reached.
func SendPublishedAnnouncementEmail(a model.Announcement) (EmailSendStats, error) {
	if a.ID == 0 || !AnnouncementMailable(a.Kind) || a.PublishedAt == nil || a.UnpublishedAt != nil {
		return EmailSendStats{}, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "send_email", "reason": "only a published model update, operational, maintenance or incident announcement is mailed"})
	}
	batchSize := email_setting.GetEmailSetting().BatchSize
	if batchSize <= 0 {
		batchSize = 200
	}
	notice := platformNotice{
		Link:          common.MailSiteURL("/console"),
		ProductUpdate: true,
		Localize: func(language string) (string, template.HTML) {
			return announcementMailContent(a, language)
		},
		RefKey: func(rec emailRecipient) string {
			return announcementMailRefKey(a.ID, rec.Id)
		},
		Template: announcementMailTemplate,
	}
	return dispatchEmails(notice, nil, true, batchSize)
}
