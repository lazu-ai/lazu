package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func TestEstimateBalanceRunwayAveragesOverTheHistoryItHas(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	week := BalanceRunwayWindow

	// Spending through the whole window: the average is over seven days.
	r := EstimateBalanceRunway(billing.USD(300), billing.USD(700), now.Add(-week), now, week)
	if !r.Known || r.DailySpend != billing.USD(100) || math.Abs(r.DaysLeft-3) > 1e-9 {
		t.Fatalf("full week: %+v", r)
	}

	// An account that started two days ago is averaged over two days, not
	// seven: a seven-day divisor would understate its burn 3.5x.
	r = EstimateBalanceRunway(billing.USD(300), billing.USD(400), now.Add(-48*time.Hour), now, week)
	if !r.Known || r.DailySpend != billing.USD(200) || math.Abs(r.DaysLeft-1.5) > 1e-9 {
		t.Fatalf("two days: %+v", r)
	}

	// A first spend older than the window is clamped to the window.
	r = EstimateBalanceRunway(billing.USD(70), billing.USD(70), now.Add(-30*24*time.Hour), now, week)
	if !r.Known || r.DailySpend != billing.USD(10) || math.Abs(r.DaysLeft-7) > 1e-9 {
		t.Fatalf("clamped: %+v", r)
	}

	// Nothing left: days left is zero, the rate is still reported.
	r = EstimateBalanceRunway(billing.USD(-5), billing.USD(700), now.Add(-week), now, week)
	if !r.Known || r.DaysLeft != 0 || r.DailySpend != billing.USD(100) {
		t.Fatalf("negative balance: %+v", r)
	}
}

func TestEstimateBalanceRunwayNeedsADayOfHistory(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	week := BalanceRunwayWindow
	for name, r := range map[string]BalanceRunway{
		"three hours": EstimateBalanceRunway(billing.USD(100), billing.USD(500), now.Add(-3*time.Hour), now, week),
		"no spend":    EstimateBalanceRunway(billing.USD(100), 0, now.Add(-week), now, week),
		"no first":    EstimateBalanceRunway(billing.USD(100), billing.USD(500), time.Time{}, now, week),
	} {
		if r.Known || r.DailySpend != 0 || r.DaysLeft != 0 {
			t.Fatalf("%s: expected no estimate, got %+v", name, r)
		}
	}
	// Exactly a day is enough.
	if r := EstimateBalanceRunway(billing.USD(100), billing.USD(500), now.Add(-24*time.Hour), now, week); !r.Known || r.DailySpend != billing.USD(500) {
		t.Fatalf("one day: %+v", r)
	}
}

func TestBalanceRunwayAlertDueFallsBackToTheFixedThreshold(t *testing.T) {
	known := func(balance float64, days float64) BalanceRunway {
		return BalanceRunway{Balance: billing.USD(balance), DailySpend: billing.USD(1), DaysLeft: days, Known: true}
	}
	cases := []struct {
		name string
		r    BalanceRunway
		days int
		want bool
	}{
		{"under the line", known(200, 2.9), 3, true},
		{"on the line", known(300, 3), 3, false},
		{"plenty", known(5000, 40), 3, false},
		{"zero balance", known(0, 0), 3, true},
		{"negative balance without an estimate", BalanceRunway{Balance: billing.USD(-1)}, 3, true},
		// Little history and money left: the per-request fixed threshold
		// is the one that warns this account.
		{"no estimate, money left", BalanceRunway{Balance: billing.USD(5)}, 3, false},
		{"runway check off", known(200, 0.5), 0, false},
	}
	for _, tc := range cases {
		if got := BalanceRunwayAlertDue(tc.r, tc.days); got != tc.want {
			t.Errorf("%s: due = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLowBalanceMailSaysHowLongTheBalanceLasts(t *testing.T) {
	runway := BalanceRunway{Balance: billing.USD(210), DailySpend: billing.USD(100), DaysLeft: 2.1, Known: true}
	for _, tc := range []struct {
		lang    string
		subject string
		body    []string
	}{
		{"zh", "Lazu 余额约剩 2.1 天", []string{"$210.00", "$100.00", "2.1 天", "日均消耗"}},
		{"en", "Your Lazu balance lasts about 2.1 days", []string{"$210.00", "$100.00", "2.1 days", "Daily spend"}},
	} {
		user := &model.User{Id: 9, Email: "reader@example.test", Setting: fmt.Sprintf(`{"language":%q}`, tc.lang)}
		subject, body, err := lowBalanceMail(user, runway.Balance, 0, &runway)
		if err != nil {
			t.Fatal(err)
		}
		if subject != tc.subject {
			t.Fatalf("%s subject = %q", tc.lang, subject)
		}
		for _, want := range tc.body {
			if !strings.Contains(body, want) {
				t.Fatalf("%s body is missing %q", tc.lang, want)
			}
		}
	}
	// Without an estimate the mail still names the reminder line it crossed.
	user := &model.User{Id: 9, Email: "reader@example.test", Setting: `{"language":"en"}`}
	subject, body, err := lowBalanceMail(user, billing.USD(12), billing.USD(20), nil)
	if err != nil || subject != "Your Lazu balance is down to $12.00" || !strings.Contains(body, "$20.00 reminder") {
		t.Fatalf("threshold mail: %q %v", subject, err)
	}
}

// setupBalanceRunwayTestDB opens one in-memory store as both core and
// telemetry, with the tables the runway check reads and writes.
func setupBalanceRunwayTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousCore, previousTelemetry, previousRedis := model.CoreDB(), model.ConfiguredTelemetryDB(), common.RedisEnabled
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	common.RedisEnabled = false
	model.EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&model.User{}, &model.Log{}, &model.MailDelivery{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		model.SetCoreDB(previousCore)
		model.SetTelemetryDB(previousTelemetry)
		common.RedisEnabled = previousRedis
		raw, _ := db.DB()
		_ = raw.Close()
	})
	return db
}

func TestRunBalanceRunwayAlertsOncePerDayPerAccount(t *testing.T) {
	db := setupBalanceRunwayTestDB(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	cfg := email_setting.GetEmailSetting()
	oldRunway, oldEnabled, oldAdmin := cfg.LowBalanceRunwayDays, cfg.LowBalanceEnabled, cfg.AdminEmail
	cfg.LowBalanceRunwayDays, cfg.LowBalanceEnabled, cfg.AdminEmail = 3, true, "ops@example.test"
	oldSecret, oldResend, oldSMTP, oldRecorder := common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer, common.MailRecorder
	// No transport: every send fails locally and is archived as failed,
	// which is enough to prove what was attempted and what was not.
	common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer = "runway-test", "", ""
	common.MailRecorder = model.RecordMailDelivery
	t.Cleanup(func() {
		cfg.LowBalanceRunwayDays, cfg.LowBalanceEnabled, cfg.AdminEmail = oldRunway, oldEnabled, oldAdmin
		common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer, common.MailRecorder = oldSecret, oldResend, oldSMTP, oldRecorder
	})

	users := []model.User{
		// Burning $100 a day with $200 left: two days of runway.
		{Id: 11, Username: "heavy", Email: "heavy@example.test", Quota: billing.USD(200), Status: common.UserStatusEnabled, Setting: `{"language":"en"}`},
		// Same rate with $10,000 left: months of runway.
		{Id: 12, Username: "healthy", Email: "healthy@example.test", Quota: billing.USD(10000), Status: common.UserStatusEnabled},
		// Three hours old and already through $500: no estimate yet, the
		// fixed threshold covers it.
		{Id: 13, Username: "young", Email: "young@example.test", Quota: billing.USD(100), Status: common.UserStatusEnabled},
		// Out of money and still trying, but disabled: nobody is alerted.
		{Id: 14, Username: "off", Email: "off@example.test", Quota: billing.USD(-1), Status: common.UserStatusDisabled},
		// Project spend billed to this payer by another account's key.
		{Id: 15, Username: "payer", Email: "", Quota: billing.USD(45), Status: common.UserStatusEnabled},
	}
	for i := range users {
		if err := db.Create(&users[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	spend := func(user, payer int, at time.Time, dollars float64) {
		row := model.Log{UserId: user, PayerUserID: payer, Type: model.LogTypeConsume, AmountMicrousd: billing.USD(dollars), CreatedAt: at.Unix(), ModelName: "m"}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for day := 1; day <= 7; day++ {
		at := now.Add(-time.Duration(day) * 24 * time.Hour)
		spend(11, 0, at, 100)
		spend(12, 0, at, 100)
		spend(14, 0, at, 100)
		spend(99, 15, at, 20) // a member's key charged to payer 15
	}
	spend(13, 0, now.Add(-3*time.Hour), 500)
	// A top-up row inside the window must not count as spend.
	db.Create(&model.Log{UserId: 11, Type: model.LogTypeTopup, AmountMicrousd: billing.USD(5000), CreatedAt: now.Add(-time.Hour).Unix()})

	var alerts []common.OpsAlert
	common.RegisterOpsAlertSink(func(a common.OpsAlert) {
		if strings.Contains(a.Subject, "example.test") || strings.Contains(a.Subject, "(#1") {
			alerts = append(alerts, a)
		}
	})
	runwayRows := func() []model.MailDelivery {
		var rows []model.MailDelivery
		if err := db.Where("template = ?", balanceRunwayTemplate).Order("user_id, created_at").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows
	}

	if err := RunBalanceRunwayAlerts(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	rows := runwayRows()
	if len(rows) != 2 || rows[0].UserID != 11 || rows[1].UserID != 15 {
		t.Fatalf("first run archived %+v", rows)
	}
	if rows[0].Kind != common.MailKindBalance || rows[0].Status != model.MailStatusFailed || rows[0].Receiver != "heavy@example.test" {
		t.Fatalf("heavy account's row = %+v", rows[0])
	}
	if rows[1].Status != model.MailStatusSkipped || rows[1].Error != "no_email" {
		t.Fatalf("payer without an address = %+v", rows[1])
	}
	if len(alerts) != 2 {
		t.Fatalf("operator alerts = %+v", alerts)
	}
	heavy := alerts[0]
	if !strings.Contains(heavy.Subject, "heavy (#11)") || !strings.Contains(heavy.Subject, "2.0 天") {
		t.Fatalf("operator subject = %q", heavy.Subject)
	}
	for _, want := range []string{"$200.00", "$100.00", "2.0 天", "未发送（send_failed）"} {
		if !strings.Contains(heavy.Text, want) {
			t.Fatalf("operator text is missing %q:\n%s", want, heavy.Text)
		}
	}
	if payer := alerts[1]; !strings.Contains(payer.Subject, "payer (#15)") || !strings.Contains(payer.Text, "$20.00") || !strings.Contains(payer.Text, "no_email") {
		t.Fatalf("payer alert = %+v", payer)
	}

	// An hour later the archive says both accounts were alerted today, so
	// nothing is repeated, not even after a process restart (the guard is
	// the database, not memory).
	if err := RunBalanceRunwayAlerts(context.Background(), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := runwayRows(); len(got) != 2 || len(alerts) != 2 {
		t.Fatalf("an hour later: rows %d alerts %d", len(got), len(alerts))
	}

	// Just under a day later the two are still quiet, while the young
	// account now has a day of history behind its $500 and is alerted for
	// the first time.
	if err := RunBalanceRunwayAlerts(context.Background(), now.Add(BalanceRunwayAlertEvery-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := runwayRows(); len(got) != 3 || got[1].UserID != 13 || len(alerts) != 3 {
		t.Fatalf("just under a day later: rows %+v alerts %d", got, len(alerts))
	}

	// Past 24 hours the first two are re-evaluated and still qualify (their
	// week of spend is still inside the window); the young account's alert
	// is minutes old and is not repeated.
	if err := RunBalanceRunwayAlerts(context.Background(), now.Add(BalanceRunwayAlertEvery+time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := runwayRows(); len(got) != 5 || len(alerts) != 5 {
		t.Fatalf("after a day: rows %d alerts %d", len(got), len(alerts))
	}

	// With the runway check off nothing runs, whatever the balances.
	cfg.LowBalanceRunwayDays = 0
	if err := RunBalanceRunwayAlerts(context.Background(), now.Add(3*BalanceRunwayAlertEvery)); err != nil {
		t.Fatal(err)
	}
	if got := runwayRows(); len(got) != 5 {
		t.Fatalf("disabled check still archived rows: %d", len(got))
	}
}

func TestRunBalanceRunwayAlertsTellsTheOperatorWhenUserMailIsOff(t *testing.T) {
	db := setupBalanceRunwayTestDB(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	cfg := email_setting.GetEmailSetting()
	oldRunway, oldEnabled, oldAdmin := cfg.LowBalanceRunwayDays, cfg.LowBalanceEnabled, cfg.AdminEmail
	cfg.LowBalanceRunwayDays, cfg.LowBalanceEnabled, cfg.AdminEmail = 3, false, "ops@example.test"
	oldSecret, oldResend, oldSMTP := common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer
	common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer = "runway-test", "", ""
	t.Cleanup(func() {
		cfg.LowBalanceRunwayDays, cfg.LowBalanceEnabled, cfg.AdminEmail = oldRunway, oldEnabled, oldAdmin
		common.CryptoSecret, common.AppConfig.Resend.APIKey, common.SMTPServer = oldSecret, oldResend, oldSMTP
	})
	user := model.User{Id: 21, Username: "dry", Email: "dry@example.test", Quota: 0, Status: common.UserStatusEnabled}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&model.Log{UserId: 21, Type: model.LogTypeConsume, AmountMicrousd: billing.USD(30), CreatedAt: now.Add(-30 * time.Hour).Unix(), ModelName: "m"})

	var alerts []common.OpsAlert
	common.RegisterOpsAlertSink(func(a common.OpsAlert) {
		if strings.Contains(a.Subject, "dry (#21)") {
			alerts = append(alerts, a)
		}
	})
	if err := RunBalanceRunwayAlerts(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var row model.MailDelivery
	if err := db.Where("template = ? AND user_id = ?", balanceRunwayTemplate, 21).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != model.MailStatusSkipped || row.Error != "low_balance_mail_disabled" {
		t.Fatalf("row = %+v", row)
	}
	if len(alerts) != 1 || !strings.HasPrefix(alerts[0].Subject, "[Lazu ops] 余额已用完：dry (#21)") || !strings.Contains(alerts[0].Text, "low_balance_mail_disabled") {
		t.Fatalf("alerts = %+v", alerts)
	}
}
