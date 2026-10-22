package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestSupportReportValidation(t *testing.T) {
	cases := []struct {
		report SupportReport
		ok     bool
	}{
		{SupportReport{Message: "top-up failed twice"}, true},
		{SupportReport{Message: " x "}, false},
		{SupportReport{Message: strings.Repeat("字", 4001)}, false},
		{SupportReport{Message: "ok", RequestID: "req_abc123"}, true},
		{SupportReport{Message: "ok", RequestID: "req abc"}, false},
		{SupportReport{Message: "ok", Error: "lz-3101"}, true},
		{SupportReport{Message: "ok", Error: "3101"}, false},
		{SupportReport{Message: "ok", Page: "/console/wallet?x=1"}, true},
		{SupportReport{Message: "ok", Page: "https://evil.example"}, false},
	}
	for _, tc := range cases {
		r := tc.report
		err := r.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("Validate(%+v) = %v, want ok=%v", tc.report, err, tc.ok)
		}
	}
	r := SupportReport{Message: "ok", Error: " lz-3101 "}
	_ = r.Validate()
	if r.Error != "LZ-3101" {
		t.Fatalf("error number not normalized: %q", r.Error)
	}
}

func TestSupportFamily(t *testing.T) {
	for lz, want := range map[string]string{
		"LZ-1001": "请求", "LZ-2201": "登录", "LZ-2001": "密钥与权限", "LZ-3101": "钱包",
		"LZ-4001": "模型与线路", "LZ-5001": "限流", "LZ-6001": "上游", "LZ-7404": "控制台", "LZ-9001": "内部",
	} {
		if got := supportFamily(lz); got != want {
			t.Errorf("supportFamily(%s) = %s, want %s", lz, got, want)
		}
	}
}

func supportReportFixture(t *testing.T) (*model.User, *[]common.MailRecord) {
	t.Helper()
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:support_report?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}
	user := &model.User{Id: 9, Username: "dana", Password: "x", Email: "dana@example.com"}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if err := db.Create(&model.Log{UserId: 9, RequestID: "req_mine01", RequestTS: now, CreatedAt: now, Type: model.LogTypeError, ModelName: "gpt-x", Content: "upstream timed out"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Log{UserId: 10, RequestID: "req_other1", RequestTS: now + 1, CreatedAt: now, Type: model.LogTypeError, ModelName: "secret-model", Content: "someone else's"}).Error; err != nil {
		t.Fatal(err)
	}

	var sent []common.MailRecord
	oldRecorder, oldServer, oldAccount, oldToken, oldPort := common.MailRecorder, common.SMTPServer, common.SMTPAccount, common.SMTPToken, common.SMTPPort
	oldSite, oldRedis, oldResend, oldNotify := common.AppConfig.Site, common.RedisEnabled, common.AppConfig.Resend.APIKey, common.AppConfig.EmailNotifications
	common.MailRecorder = func(r common.MailRecord) { sent = append(sent, r) }
	// A transport that is configured but refuses connections: the mail is
	// built and recorded, and delivery fails fast.
	common.SMTPServer, common.SMTPAccount, common.SMTPToken, common.SMTPPort = "127.0.0.1", "smtp-user", "smtp-pass", 1 // secret-scan: allow -- placeholder SMTP credentials for a closed local port
	common.AppConfig.Resend.APIKey = ""
	common.AppConfig.Site.SupportEmail = "help@example.com"
	common.RedisEnabled = false
	resetSupportReportCounts()
	t.Cleanup(func() {
		common.MailRecorder, common.SMTPServer, common.SMTPAccount, common.SMTPToken, common.SMTPPort = oldRecorder, oldServer, oldAccount, oldToken, oldPort
		common.AppConfig.Site, common.RedisEnabled, common.AppConfig.Resend.APIKey, common.AppConfig.EmailNotifications = oldSite, oldRedis, oldResend, oldNotify
		resetSupportReportCounts()
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	})
	return user, &sent
}

func TestSupportReportMailCarriesContextAndReplyTo(t *testing.T) {
	user, sent := supportReportFixture(t)
	err := SubmitSupportReport(context.Background(), user, SupportReport{
		Message: "充值后余额没变", RequestID: "req_mine01", Error: "LZ-3101", Page: "/console/wallet",
	}, "Mozilla/5.0 test")
	if err == nil {
		t.Fatal("delivery to a closed port should fail")
	}
	if len(*sent) != 1 {
		t.Fatalf("mails = %d", len(*sent))
	}
	mail := (*sent)[0]
	if mail.Subject != "[Lazu support] 钱包 LZ-3101 dana" || mail.Receiver != "help@example.com" || mail.Meta.ReplyTo != "dana@example.com" {
		t.Fatalf("mail = %q to %q reply-to %q", mail.Subject, mail.Receiver, mail.Meta.ReplyTo)
	}
	for _, want := range []string{"充值后余额没变", "req_mine01", "gpt-x", "upstream timed out", "/console/wallet", "insufficient_quota"} {
		if !strings.Contains(mail.HTML, want) {
			t.Errorf("mail body lacks %q", want)
		}
	}
}

func TestSupportReportNeverShowsAnotherUsersRequest(t *testing.T) {
	user, sent := supportReportFixture(t)
	_ = SubmitSupportReport(context.Background(), user, SupportReport{Message: "look at this", RequestID: "req_other1"}, "")
	if len(*sent) != 1 {
		t.Fatalf("mails = %d", len(*sent))
	}
	body := (*sent)[0].HTML
	if strings.Contains(body, "secret-model") || strings.Contains(body, "someone else") {
		t.Fatal("another account's request leaked into the report")
	}
	if !strings.Contains(body, "30 天内没有这个账户的这条请求") {
		t.Fatal("the report does not say the request was not found")
	}
}

func TestSupportReportLimitsAndAvailability(t *testing.T) {
	user, _ := supportReportFixture(t)
	for i := 0; i < supportReportPerHour; i++ {
		if err := SubmitSupportReport(context.Background(), user, SupportReport{Message: "again"}, ""); errors.Is(err, ErrSupportRateLimited) {
			t.Fatalf("report %d was rate limited", i+1)
		}
	}
	if err := SubmitSupportReport(context.Background(), user, SupportReport{Message: "again"}, ""); !errors.Is(err, ErrSupportRateLimited) {
		t.Fatalf("report over the hourly cap: %v", err)
	}

	common.AppConfig.Site.SupportEmail = ""
	common.AppConfig.EmailNotifications.AdminEmail = ""
	if err := SubmitSupportReport(context.Background(), user, SupportReport{Message: "anyone?"}, ""); !errors.Is(err, ErrSupportUnavailable) {
		t.Fatalf("no support address: %v", err)
	}
}
