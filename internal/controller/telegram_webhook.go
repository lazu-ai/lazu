package controller

import (
	"crypto/subtle"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/telegram"
)

// TelegramWebhook receives the bot's updates when telegram.updates is
// "webhook". Telegram sends the configured secret in a header; anything
// without it is refused. The update is queued and answered asynchronously,
// so Telegram gets its 200 at once.
func TelegramWebhook(c *gin.Context) {
	if !telegram.Receiving() || telegram.Mode() != "webhook" {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	secret := telegram.WebhookSecret()
	got := c.GetHeader("X-Telegram-Bot-Api-Secret-Token")
	if secret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if err := telegram.Accept(raw); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	c.Status(http.StatusOK)
}
