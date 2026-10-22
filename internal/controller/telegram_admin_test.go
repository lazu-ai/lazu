package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

func TestTelegramAdminHandlersAnswerWithCatalogCodes(t *testing.T) {
	oldCfg, oldRedis := common.AppConfig.Telegram, common.RedisEnabled
	common.AppConfig.Telegram = common.TelegramConfig{}
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.AppConfig.Telegram = oldCfg
		common.RedisEnabled = oldRedis
	})

	recorder, body := serveSettingsRequest(t, http.MethodPost, "/api/admin/telegram/test", "/api/admin/telegram/test", "{}", SendTelegramTest)
	expectSettingsError(t, recorder, body, http.StatusBadRequest, "telegram_not_configured")
	if body.Error.Lazu != "LZ-7431" {
		t.Fatalf("lazu = %q", body.Error.Lazu)
	}

	common.AppConfig.Telegram.BotToken = "1:x"
	recorder, body = serveSettingsRequest(t, http.MethodPost, "/api/admin/telegram/test", "/api/admin/telegram/test", "{}", SendTelegramTest)
	expectSettingsError(t, recorder, body, http.StatusBadRequest, "telegram_not_configured")
	if body.Error.Details["reason"] != "telegram.ops_chat is empty or not a chat id" {
		t.Fatalf("details = %v", body.Error.Details)
	}

	for _, payload := range []string{`{}`, `{"minutes":-1}`, `{"minutes":10081}`, `{"minutes":"5"}`} {
		recorder, body = serveSettingsRequest(t, http.MethodPost, "/api/admin/telegram/mute", "/api/admin/telegram/mute", payload, MuteTelegramOps)
		expectSettingsError(t, recorder, body, http.StatusBadRequest, "telegram_mute_invalid")
	}
}

func TestTelegramWebhookChecksModeAndSecret(t *testing.T) {
	oldCfg := common.AppConfig.Telegram
	t.Cleanup(func() { common.AppConfig.Telegram = oldCfg })
	gin.SetMode(gin.TestMode)
	post := func(secret, body string) int {
		router := gin.New()
		router.POST("/api/telegram/webhook", TelegramWebhook)
		req := httptest.NewRequest(http.MethodPost, "/api/telegram/webhook", strings.NewReader(body))
		if secret != "" {
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	// An update without a message is accepted and ignored, so no handler
	// reaches the real Bot API.
	update := `{"update_id":5}`

	common.AppConfig.Telegram = common.TelegramConfig{BotToken: "1:x"}
	if code := post("s3cret", update); code != http.StatusNotFound {
		t.Fatalf("webhook without webhook mode: %d", code)
	}
	common.AppConfig.Telegram.Updates = "webhook"
	if code := post("s3cret", update); code != http.StatusUnauthorized {
		t.Fatalf("webhook mode without a configured secret: %d", code)
	}
	common.AppConfig.Telegram.WebhookSecret = "s3cret"
	if code := post("", update); code != http.StatusUnauthorized {
		t.Fatalf("missing secret header: %d", code)
	}
	if code := post("wrong", update); code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", code)
	}
	if code := post("s3cret", `{"nope":true}`); code != http.StatusBadRequest {
		t.Fatalf("non-update body: %d", code)
	}
	if code := post("s3cret", update); code != http.StatusOK {
		t.Fatalf("valid update: %d", code)
	}
}
