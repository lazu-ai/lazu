package service

import (
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
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
func SendOpsMail(subject string, mail common.Mail) error {
	receiver := opsMailRecipient()
	if receiver == "" {
		common.SysError("ops mail not sent, no admin email or root email: " + subject)
		return nil
	}
	mail.Tier = common.MailOps
	body, err := common.RenderMail(mail)
	if err != nil {
		return fmt.Errorf("render ops mail: %w", err)
	}
	return common.SendMail(common.MailMeta{Kind: common.MailKindOps}, "[Lazu ops] "+subject, receiver, body)
}
