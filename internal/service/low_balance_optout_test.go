package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// An account that turned the low-balance mail off in its wallet gets neither
// the fixed-threshold mail nor the runway mail; the operator still hears
// about a short runway.
func TestLowBalanceMailRespectsTheAccountsSwitch(t *testing.T) {
	db := setupBalanceRunwayTestDB(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	cfg := email_setting.GetEmailSetting()
	oldRunway, oldEnabled, oldThreshold, oldAdmin := cfg.LowBalanceRunwayDays, cfg.LowBalanceEnabled, cfg.LowBalanceThresholdUSD, cfg.AdminEmail
	cfg.LowBalanceRunwayDays, cfg.LowBalanceEnabled, cfg.LowBalanceThresholdUSD, cfg.AdminEmail = 3, true, 5, "ops@example.test"
	oldSecret, oldResend, oldSMTP, oldRecorder := common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer, common.MailRecorder
	common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer = "optout-test", "", ""
	common.MailRecorder = model.RecordMailDelivery
	t.Cleanup(func() {
		cfg.LowBalanceRunwayDays, cfg.LowBalanceEnabled, cfg.LowBalanceThresholdUSD, cfg.AdminEmail = oldRunway, oldEnabled, oldThreshold, oldAdmin
		common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer, common.MailRecorder = oldSecret, oldResend, oldSMTP, oldRecorder
	})

	user := model.User{Id: 31, Username: "quiet", Email: "quiet@example.test", Quota: billing.USD(2), Status: common.UserStatusEnabled, Setting: `{"low_balance_off":true}`}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	sent, suppress := SendLowBalanceEmailIfNeeded(&user, user.Quota, 0)
	if sent || !suppress {
		t.Fatalf("fixed-threshold mail for an opted-out account: sent=%v suppress=%v", sent, suppress)
	}
	var count int64
	db.Model(&model.MailDelivery{}).Where("user_id = ?", user.Id).Count(&count)
	if count != 0 {
		t.Fatalf("opted-out account has %d mail rows", count)
	}

	for day := 1; day <= 3; day++ {
		db.Create(&model.Log{UserId: 31, Type: model.LogTypeConsume, AmountMicrousd: billing.USD(1), CreatedAt: now.Add(-time.Duration(day) * 24 * time.Hour).Unix(), ModelName: "m"})
	}
	var alerts []common.OpsAlert
	common.RegisterOpsAlertSink(func(a common.OpsAlert) {
		if strings.Contains(a.Subject, "quiet (#31)") {
			alerts = append(alerts, a)
		}
	})
	if err := RunBalanceRunwayAlerts(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var row model.MailDelivery
	if err := db.Where("template = ? AND user_id = ?", balanceRunwayTemplate, 31).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != model.MailStatusSkipped || row.Error != "user_turned_off" {
		t.Fatalf("runway row = %+v", row)
	}
	if len(alerts) != 1 || !strings.Contains(alerts[0].Text, "user_turned_off") {
		t.Fatalf("operator alerts = %+v", alerts)
	}
}
