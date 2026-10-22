package controller

import (
	"context"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/telegram"
	"github.com/lazu-ai/lazu/internal/types"
)

// telegramStatus is the Telegram part of /api/admin/deployment-status. It
// says what is configured, never the token or the chat id.
func telegramStatus(ctx context.Context) gin.H {
	status := gin.H{
		"bot_configured":      telegram.Configured(),
		"ops_chat_configured": telegram.OpsConfigured(),
		"ops_muted_until":     int64(0),
		"updates":             telegram.Mode(),
		"answer_configured":   telegram.AnswerConfigured(),
		"answer_chats":        len(common.AppConfig.Telegram.Answer.Chats),
	}
	if until := telegram.OpsMutedUntil(ctx); until.After(time.Now()) {
		status["ops_muted_until"] = until.Unix()
	}
	return status
}

// SendTelegramTest posts a test message to telegram.ops_chat.
func SendTelegramTest(c *gin.Context) {
	text := "<b>[Lazu ops] Telegram 测试</b>\n\n收到这条消息，说明运维告警会同时发到这里。同一主题一小时内只推送一次。"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	if err := telegram.SendOpsTest(ctx, text); err != nil {
		writeTelegramError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"sent": true})
}

type telegramMuteRequest struct {
	Minutes *int `json:"minutes"`
}

// MuteTelegramOps silences ops alerts in Telegram for a number of minutes;
// 0 lifts the mute. Mail is never muted.
func MuteTelegramOps(c *gin.Context) {
	var req telegramMuteRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Minutes == nil || *req.Minutes < 0 || *req.Minutes > 7*24*60 {
		httperr.Code(c, types.ErrorCodeTelegramMuteInvalid)
		return
	}
	until, err := telegram.MuteOps(c.Request.Context(), time.Duration(*req.Minutes)*time.Minute)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	var untilUnix int64
	if !until.IsZero() {
		untilUnix = until.Unix()
	}
	common.ApiSuccess(c, gin.H{"ops_muted_until": untilUnix})
}

func writeTelegramError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, telegram.ErrNotConfigured):
		httperr.Code(c, types.ErrorCodeTelegramNotConfigured, types.D{"reason": "telegram.bot_token is empty"})
	case errors.Is(err, telegram.ErrNoOpsChat):
		httperr.Code(c, types.ErrorCodeTelegramNotConfigured, types.D{"reason": "telegram.ops_chat is empty or not a chat id"})
	default:
		var apiErr *telegram.APIError
		if errors.As(err, &apiErr) {
			httperr.Code(c, types.ErrorCodeTelegramSendFailed, types.D{"reason": apiErr.Description})
			return
		}
		// Transport errors are already free of the token; they are logged
		// with the request id rather than echoed.
		httperr.Write(c, types.WrapErr(err, types.ErrorCodeTelegramSendFailed))
	}
}
