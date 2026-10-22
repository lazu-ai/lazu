package service

import (
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	// Registers the Telegram ops-alert sink in every process that sends
	// ops mail (the API and the jobs worker both link this package).
	_ "github.com/lazu-ai/lazu/internal/telegram"
)

// opsMailRecipient is where operational alerts go: ADMIN_EMAIL, or the root
// account's address when no admin address is configured.
func opsMailRecipient() string {
	if email := strings.TrimSpace(email_setting.GetEmailSetting().AdminEmail); email != "" {
		return email
	}
	if model.CoreDB() == nil {
		return ""
	}
	return strings.TrimSpace(model.GetRootUser().Email)
}

// SendOpsMail renders an ops-tier alert and mails it to the operator. The
// subject carries the [Lazu ops] prefix so operators can filter on it.
//
// Chat sinks (common.RegisterOpsAlertSink) receive the alert as plain text
// whether or not a mail recipient exists.
func SendOpsMail(subject string, mail common.Mail) error {
	subject = "[Lazu ops] " + subject
	text := common.MailAlertText(mail)
	receiver := opsMailRecipient()
	if receiver == "" {
		common.SysError("ops mail not sent, no admin email or root email: " + subject)
		common.PushOpsAlert(common.OpsAlert{Subject: subject, Text: text})
		return nil
	}
	mail.Tier = common.MailOps
	body, err := common.RenderMail(mail)
	if err != nil {
		return fmt.Errorf("render ops mail: %w", err)
	}
	return common.SendMail(common.MailMeta{Kind: common.MailKindOps, AlertText: text}, subject, receiver, body)
}
