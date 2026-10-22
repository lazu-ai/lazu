package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/telegram"
	"gorm.io/gorm"
)

type fakeBot struct {
	mu   sync.Mutex
	sent []map[string]any
}

func (f *fakeBot) messages() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.sent...)
}

// telegramLinkFixture opens an in-memory database with two users and points
// the bot at a fake Bot API that records sendMessage calls.
func telegramLinkFixture(t *testing.T, name string) *fakeBot {
	t.Helper()
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	if err := db.AutoMigrate(&model.User{}, &model.UserIdentity{}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []model.User{
		{Id: 1, Username: "alice", Password: "x", Setting: `{"notify_type":"telegram","quota_warning_threshold":5,"sidebar_modules":"keep-me"}`},
		{Id: 2, Username: "bob", Password: "x"},
	} {
		u := u
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}

	bot := &fakeBot{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":999,"is_bot":true,"username":"lazu_help_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			bot.mu.Lock()
			bot.sent = append(bot.sent, body)
			bot.mu.Unlock()
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
		default:
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		}
	}))
	oldCfg, oldRedis := common.AppConfig.Telegram, common.RedisEnabled
	common.AppConfig.Telegram = common.TelegramConfig{BotToken: "42:link-test", APIBase: server.URL, Updates: "polling"} // secret-scan: allow -- fake token for the httptest Bot API
	common.RedisEnabled = false
	t.Cleanup(func() {
		server.Close()
		common.AppConfig.Telegram = oldCfg
		common.RedisEnabled = oldRedis
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	})
	return bot
}

func startMessage(telegramUserID int64, text, chatType string) telegram.Update {
	return telegram.Update{UpdateID: 1, Message: &telegram.IncomingMessage{
		MessageID: 3, From: &telegram.User{ID: telegramUserID, LanguageCode: "en"},
		Chat: telegram.ChatInfo{ID: telegramUserID, Type: chatType}, Text: text,
	}}
}

func linkToken(t *testing.T, userID int) string {
	t.Helper()
	link, _, err := IssueTelegramLink(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "https://t.me/lazu_help_bot?start=link_"
	if !strings.HasPrefix(link, prefix) || len(link)-len("https://t.me/lazu_help_bot?start=") > 64 {
		t.Fatalf("link = %q", link)
	}
	return strings.TrimPrefix(link, prefix)
}

func TestTelegramLinkIsOneTimeAndBindsTheSender(t *testing.T) {
	bot := telegramLinkFixture(t, "tg_link_once")
	token := linkToken(t, 2)

	telegram.Dispatch(context.Background(), startMessage(777, "/start link_"+token, "private"))
	id, linked, err := TelegramUserID(2)
	if err != nil || !linked || id != 777 {
		t.Fatalf("TelegramUserID = %d, %v, %v", id, linked, err)
	}
	sent := bot.messages()
	if len(sent) != 1 || !strings.Contains(sent[0]["text"].(string), "Linked to the account bob") {
		t.Fatalf("reply = %v", sent)
	}

	telegram.Dispatch(context.Background(), startMessage(777, "/start link_"+token, "private"))
	if sent := bot.messages(); len(sent) != 2 || !strings.Contains(sent[1]["text"].(string), "expired") {
		t.Fatalf("second use of the link: %v", sent)
	}
}

func TestTelegramLinkRefusesAnAccountLinkedElsewhere(t *testing.T) {
	bot := telegramLinkFixture(t, "tg_link_elsewhere")
	if err := LinkTelegram(1, 555); err != nil {
		t.Fatal(err)
	}
	telegram.Dispatch(context.Background(), startMessage(555, "/start link_"+linkToken(t, 2), "private"))
	if _, linked, _ := TelegramUserID(2); linked {
		t.Fatal("a Telegram account linked to alice was moved to bob")
	}
	if sent := bot.messages(); len(sent) != 1 || !strings.Contains(sent[0]["text"].(string), "another account") {
		t.Fatalf("reply = %v", sent)
	}
}

func TestTelegramLinkInAGroupIsNotCompleted(t *testing.T) {
	bot := telegramLinkFixture(t, "tg_link_group")
	token := linkToken(t, 2)
	update := startMessage(777, "/start link_"+token, "supergroup")
	update.Message.Chat.ID = -100
	telegram.Dispatch(context.Background(), update)
	if _, linked, _ := TelegramUserID(2); linked {
		t.Fatal("a link was completed in a group")
	}
	if sent := bot.messages(); len(sent) != 1 || !strings.Contains(sent[0]["text"].(string), "private chat") {
		t.Fatalf("reply = %v", sent)
	}
}

func TestRelinkReplacesTheAccountsEarlierTelegram(t *testing.T) {
	telegramLinkFixture(t, "tg_relink")
	if err := LinkTelegram(2, 100); err != nil {
		t.Fatal(err)
	}
	if err := LinkTelegram(2, 200); err != nil {
		t.Fatal(err)
	}
	if id, linked, _ := TelegramUserID(2); !linked || id != 200 {
		t.Fatalf("relink kept %d", id)
	}
	var n int64
	model.CoreDB().Model(&model.UserIdentity{}).Where("user_id = ? AND provider = ?", 2, TelegramIdentityProvider).Count(&n)
	if n != 1 {
		t.Fatalf("identity rows = %d", n)
	}
}

func TestUnlinkMovesTelegramRemindersBackToMailAndKeepsOtherSettings(t *testing.T) {
	telegramLinkFixture(t, "tg_unlink")
	if err := LinkTelegram(1, 300); err != nil {
		t.Fatal(err)
	}
	if err := UnlinkTelegram(1); err != nil {
		t.Fatal(err)
	}
	if err := UnlinkTelegram(1); err != ErrTelegramNotLinked {
		t.Fatalf("second unlink: %v", err)
	}
	var user model.User
	model.CoreDB().Select("setting").First(&user, 1)
	var settings map[string]any
	_ = json.Unmarshal([]byte(user.Setting), &settings)
	if settings["notify_type"] != dto.NotifyTypeEmail || settings["sidebar_modules"] != "keep-me" || settings["quota_warning_threshold"] != float64(5) {
		t.Fatalf("settings after unlink = %v", settings)
	}
}

func TestTelegramNotifyGoesToTheLinkedChat(t *testing.T) {
	bot := telegramLinkFixture(t, "tg_notify")
	if err := sendTelegramNotify(1, dto.NewNotify(dto.NotifyTypeQuotaExceed, "余额不足", "剩余 {{value}}", []interface{}{"$1.00"})); err != ErrTelegramNotLinked {
		t.Fatalf("unlinked notify: %v", err)
	}
	if err := LinkTelegram(1, 888); err != nil {
		t.Fatal(err)
	}
	if err := sendTelegramNotify(1, dto.NewNotify(dto.NotifyTypeQuotaExceed, "余额<不足>", "剩余 {{value}}", []interface{}{"$1.00"})); err != nil {
		t.Fatal(err)
	}
	sent := bot.messages()
	if len(sent) != 1 || sent[0]["chat_id"] != "888" || sent[0]["text"] != "<b>余额&lt;不足&gt;</b>\n剩余 $1.00" {
		t.Fatalf("sent = %v", sent)
	}
}

func TestAccountContextOnlyForLinkedUsers(t *testing.T) {
	telegramLinkFixture(t, "tg_account_context")
	if got := telegramAccountContext(context.Background(), 999999); got != "" {
		t.Fatalf("unlinked context = %q", got)
	}
	if err := LinkTelegram(2, 444); err != nil {
		t.Fatal(err)
	}
	got := telegramAccountContext(context.Background(), 444)
	if !strings.Contains(got, "Username: bob") || !strings.Contains(got, "Balance: ") {
		t.Fatalf("context = %q", got)
	}
}
