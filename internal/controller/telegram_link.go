package controller

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

// GetSelfTelegram says whether this deployment can link Telegram and
// whether the current account has.
func GetSelfTelegram(c *gin.Context) {
	status, err := service.GetTelegramLinkStatus(c.GetInt("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, status)
}

// CreateSelfTelegramLink issues a one-time t.me deep link that links the
// Telegram account opening it.
func CreateSelfTelegramLink(c *gin.Context) {
	link, ttl, err := service.IssueTelegramLink(c.Request.Context(), c.GetInt("id"))
	if errors.Is(err, service.ErrTelegramUnavailable) {
		httperr.Code(c, types.ErrorCodeTelegramLinkUnavailable)
		return
	}
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"url": link, "expires_in": int(ttl.Seconds())})
}

// DeleteSelfTelegram unlinks the account's Telegram. Reminders that went to
// Telegram go back to mail.
func DeleteSelfTelegram(c *gin.Context) {
	err := service.UnlinkTelegram(c.GetInt("id"))
	if errors.Is(err, service.ErrTelegramNotLinked) {
		httperr.Code(c, types.ErrorCodeTelegramNotLinked)
		return
	}
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"linked": false})
}
