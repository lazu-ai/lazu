package controller

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

type TestEmailRequest struct {
	Email string `json:"email"`
}

func SendTestEmail(c *gin.Context) {
	var req TestEmailRequest
	_ = c.ShouldBindJSON(&req)

	email := strings.TrimSpace(req.Email)
	if email == "" {
		if user, err := model.GetUserById(c.GetInt("id"), false); err == nil {
			email = strings.TrimSpace(user.Email)
		}
	}
	if email == "" {
		httperr.Code(c, types.ErrorCodeEmailTestRecipientMissing)
		return
	}

	transport := "SMTP"
	if strings.TrimSpace(common.AppConfig.Resend.APIKey) != "" {
		transport = "Resend"
	}
	body, err := common.RenderMail(common.Mail{
		Tier: common.MailOps, Source: "CONFIG", Severity: "NOTICE",
		Preheader: "邮件配置可用", Title: "测试邮件",
		Blocks: []common.MailBlock{
			{Text: "收到这封邮件，说明 Lazu 的发信配置可用。"},
			{Facts: []common.MailFact{{Label: "发件方式", Value: transport}, {Label: "收件人", Value: email}}},
		},
	})
	if err != nil {
		httperr.Write(c, err)
		return
	}
	if err := common.SendMail(common.MailMeta{Kind: common.MailKindOps, NotAnAlert: true}, "[Lazu ops] 测试邮件", email, body); err != nil {
		// The SMTP server's reply can carry credentials or internal host
		// names; it is logged with the request ID, never sent.
		httperr.Write(c, types.WrapErr(err, types.ErrorCodeEmailTestFailed))
		return
	}
	common.ApiSuccess(c, gin.H{"email": email})
}
