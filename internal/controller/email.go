package controller

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/console_setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

type EmailSendRequest struct {
	Subject string `json:"subject"`
	Content string `json:"content"`
	UserIDs []int  `json:"user_ids"`
	SendAll bool   `json:"send_all"`
}

type AnnouncementEmailRequest struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	Link      string `json:"link"`
	UserIDs   []int  `json:"user_ids"`
	SendAll   bool   `json:"send_all"`
	UseLatest bool   `json:"use_latest"`
}

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
	if err := common.SendMail(common.MailMeta{Kind: common.MailKindOps}, "[Lazu ops] 测试邮件", email, body); err != nil {
		// The SMTP server's reply can carry credentials or internal host
		// names; it is logged with the request ID, never sent.
		httperr.Write(c, types.WrapErr(err, types.ErrorCodeEmailTestFailed))
		return
	}
	common.ApiSuccess(c, gin.H{"email": email})
}

func SendBroadcastEmail(c *gin.Context) {
	var req EmailSendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest, types.D{"reason": "the body must be a JSON object"})
		return
	}

	stats, err := service.SendBulkEmail(service.EmailBroadcastRequest{
		Subject: req.Subject,
		Content: req.Content,
		UserIDs: req.UserIDs,
		SendAll: req.SendAll,
	})
	if err != nil {
		httperr.Write(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    stats,
	})
}

func SendAnnouncementEmail(c *gin.Context) {
	var req AnnouncementEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest, types.D{"reason": "the body must be a JSON object"})
		return
	}

	payload := service.AnnouncementEmail{
		Title:   strings.TrimSpace(req.Title),
		Content: strings.TrimSpace(req.Content),
		Link:    strings.TrimSpace(req.Link),
	}

	if payload.Title == "" || payload.Content == "" || req.UseLatest {
		announcements := console_setting.GetAnnouncements()
		if len(announcements) == 0 {
			httperr.Code(c, types.ErrorCodeAnnouncementEmailContentMissing)
			return
		}
		latest := announcements[0]
		if payload.Content == "" {
			if content, ok := latest["content"].(string); ok {
				payload.Content = content
			}
		}
		if payload.Title == "" {
			summary := payload.Content
			if extra, ok := latest["extra"].(string); ok && extra != "" {
				summary = fmt.Sprintf("%s · %s", summary, extra)
			}
			if len(summary) > 60 {
				summary = summary[:60] + "..."
			}
			payload.Title = fmt.Sprintf("系统公告更新：%s", summary)
		}
		if payload.Link == "" {
			payload.Link = common.MailSiteURL("/console")
		}
	}

	stats, err := service.SendAnnouncementEmail(payload, req.UserIDs, req.SendAll)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    stats,
	})
}
