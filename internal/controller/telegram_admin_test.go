package controller

import (
	"net/http"
	"testing"

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
