package service

import (
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

// eventsFixture reuses the account-link fixture (database, fake Bot API)
// and adds the top-up table and an events chat.
func eventsFixture(t *testing.T, name string) *fakeBot {
	t.Helper()
	bot := telegramLinkFixture(t, name)
	if err := model.CoreDB().AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	common.AppConfig.Telegram.EventsChat = "-1007"
	return bot
}

func lastEvent(t *testing.T, bot *fakeBot) string {
	t.Helper()
	msgs := bot.messages()
	if len(msgs) == 0 {
		t.Fatal("no event was sent")
	}
	if msgs[len(msgs)-1]["chat_id"] != "-1007" {
		t.Fatalf("event went to %v", msgs[len(msgs)-1]["chat_id"])
	}
	return msgs[len(msgs)-1]["text"].(string)
}

func TestMaskEmail(t *testing.T) {
	for in, want := range map[string]string{"alice@example.com": "a***@example.com", " 李雷@例子.cn ": "李***@例子.cn", "nope": "", "@x.com": ""} {
		if got := MaskEmail(in); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSignupIsReportedOnceWithAMaskedEmail(t *testing.T) {
	bot := eventsFixture(t, "tg_events_signup")
	user := &model.User{Id: 2, Username: "bob", Email: "bob@example.com", NeedsPassword: true}
	if err := model.CoreDB().Create(&model.UserIdentity{UserID: 2, Provider: "github", Subject: "gh-2"}).Error; err != nil {
		t.Fatal(err)
	}
	ReportSignup(user)
	ReportSignup(user)
	if n := len(bot.messages()); n != 1 {
		t.Fatalf("signup reported %d times", n)
	}
	text := lastEvent(t, bot)
	for _, want := range []string{"新用户", "bob（ID 2）", "GitHub 登录", "b***@example.com"} {
		if !strings.Contains(text, want) {
			t.Errorf("signup message lacks %q: %s", want, text)
		}
	}
	if strings.Contains(text, "bob@example.com") {
		t.Fatal("the full email address was posted")
	}
}

func TestTopUpIsReportedOnlyAfterItSucceeds(t *testing.T) {
	bot := eventsFixture(t, "tg_events_topup")
	order := model.TopUp{Id: 41, UserId: 1, Amount: 20, PaidAmount: 20, CreditedMicrousd: 22_000_000, PaymentMethod: "stripe", TradeNo: "t-41", Status: common.TopUpStatusPending}
	if err := model.CoreDB().Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	reportTopUpWhenCommitted(41, []time.Duration{time.Millisecond, time.Millisecond})
	if n := len(bot.messages()); n != 0 {
		t.Fatalf("a pending top-up was reported: %d messages", n)
	}
	model.CoreDB().Model(&model.TopUp{}).Where("id = ?", 41).Update("status", common.TopUpStatusSuccess)
	reportTopUpWhenCommitted(41, []time.Duration{time.Millisecond})
	reportTopUpWhenCommitted(41, []time.Duration{time.Millisecond})
	if n := len(bot.messages()); n != 1 {
		t.Fatalf("top-up reported %d times", n)
	}
	text := lastEvent(t, bot)
	for _, want := range []string{"充值到账 $20.00", "alice（ID 1）", "到账 $22.00", "stripe", "首次充值"} {
		if !strings.Contains(text, want) {
			t.Errorf("top-up message lacks %q: %s", want, text)
		}
	}
}

func TestPaymentRiskNoticesAreOpsAlerts(t *testing.T) {
	for _, key := range []string{"payment-risk:refund:ch_1:500", "payment-risk:dispute-open:dp_1", "payment-alert:T1:missing"} {
		if got := projectNotificationMailMeta(model.ProjectNotification{EventKey: key}).Kind; got != common.MailKindOps {
			t.Errorf("%s filed as %q, want ops", key, got)
		}
	}
	if got := projectNotificationMailMeta(model.ProjectNotification{EventKey: "invite:x"}).Kind; got == common.MailKindOps {
		t.Error("an invitation was filed as an ops alert")
	}
}
