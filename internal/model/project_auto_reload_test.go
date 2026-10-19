package model

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

func setupAutoReloadTestDB(t *testing.T) (*gorm.DB, User, Project) {
	t.Helper()
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&ProjectAutoReload{}, &WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	previousSecret, previousMin := common.CryptoSecret, setting.StripeMinTopUp
	common.CryptoSecret, setting.StripeMinTopUp = "auto-reload-test", 5 // secret-scan: allow -- test fixture
	t.Cleanup(func() { common.CryptoSecret, setting.StripeMinTopUp = previousSecret, previousMin })
	owner := User{Username: "reload-owner", Email: "owner@example.com", Quota: 2_000_000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	setTestProjectStripeCustomer(t, db, project.ID, "cus_reload")
	return db, owner, project
}

func TestAutoReloadSettingsValidateAgainstTopUpRange(t *testing.T) {
	previous := setting.StripeMinTopUp
	setting.StripeMinTopUp = 5
	t.Cleanup(func() { setting.StripeMinTopUp = previous })
	cases := []struct {
		name  string
		s     ProjectAutoReloadSettings
		field string
	}{
		{"ok", ProjectAutoReloadSettings{ThresholdUSD: 10, AmountUSD: 50, MonthlyCapUSD: 300}, ""},
		{"threshold zero", ProjectAutoReloadSettings{ThresholdUSD: 0, AmountUSD: 50, MonthlyCapUSD: 300}, "threshold_usd"},
		{"amount under floor", ProjectAutoReloadSettings{ThresholdUSD: 10, AmountUSD: 4, MonthlyCapUSD: 300}, "amount_usd"},
		{"amount over ceiling", ProjectAutoReloadSettings{ThresholdUSD: 10, AmountUSD: 10001, MonthlyCapUSD: 100000}, "amount_usd"},
		{"cap under amount", ProjectAutoReloadSettings{ThresholdUSD: 10, AmountUSD: 50, MonthlyCapUSD: 49}, "monthly_cap_usd"},
	}
	for _, tc := range cases {
		err := tc.s.Validate()
		if tc.field == "" {
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			continue
		}
		var apiErr *types.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != types.ErrorCodeAutoReloadSettingsInvalid || apiErr.Details["field"] != tc.field {
			t.Fatalf("%s: expected auto_reload_settings_invalid on %s, got %v", tc.name, tc.field, err)
		}
	}
	if got := ClampAutoReloadAmount(3); got != 5 {
		t.Fatalf("clamp to floor: %d", got)
	}
	if got := ClampAutoReloadAmount(20000); got != setting.StripeMaxTopUp {
		t.Fatalf("clamp to ceiling: %d", got)
	}
	if got := ClampAutoReloadAmount(50); got != 50 {
		t.Fatalf("clamp leaves a valid amount: %d", got)
	}
}

func TestAutoReloadTurningOnNeedsASavedCard(t *testing.T) {
	_, _, project := setupAutoReloadTestDB(t)
	on := true
	_, err := SaveProjectAutoReloadSettings(project.ID, ProjectAutoReloadSettings{ThresholdUSD: 10, AmountUSD: 50, MonthlyCapUSD: 300}, &on)
	var apiErr *types.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != types.ErrorCodeAutoReloadCardRequired {
		t.Fatalf("expected auto_reload_card_required, got %v", err)
	}
	row, err := SaveProjectAutoReloadSettings(project.ID, ProjectAutoReloadSettings{ThresholdUSD: 10, AmountUSD: 50, MonthlyCapUSD: 300}, nil)
	if err != nil || row.Enabled || row.AmountUSD != 50 {
		t.Fatalf("numbers save without a card: %+v %v", row, err)
	}
	// The card lands through a setup session; a stale session is ignored.
	if _, err := BeginProjectAutoReloadSetup(project.ID, ProjectAutoReloadSettings{ThresholdUSD: 10, AmountUSD: 50, MonthlyCapUSD: 300}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := AttachProjectAutoReloadSetupSession(project.ID, "cs_new"); err != nil {
		t.Fatal(err)
	}
	_, applied, err := CompleteProjectAutoReloadSetup(ProjectAutoReloadCard{ProjectID: project.ID, CustomerID: "cus_reload", SessionID: "cs_old", PaymentMethodID: "pm_old", Brand: "visa", Last4: "0001"})
	if err != nil || applied {
		t.Fatalf("stale session must not save a card: %v %v", applied, err)
	}
	if _, _, err := CompleteProjectAutoReloadSetup(ProjectAutoReloadCard{ProjectID: project.ID, CustomerID: "cus_other", SessionID: "cs_new", PaymentMethodID: "pm_x"}); !errors.Is(err, ErrProjectStateConflict) {
		t.Fatalf("another customer's card: %v", err)
	}
	row, applied, err = CompleteProjectAutoReloadSetup(ProjectAutoReloadCard{ProjectID: project.ID, CustomerID: "cus_reload", SessionID: "cs_new", PaymentMethodID: "pm_new", Brand: "visa", Last4: "4242"})
	if err != nil || !applied || !row.Enabled || row.CardLabel() != "Visa ···· 4242" || row.SetupSessionID != "" {
		t.Fatalf("saved card turns reloads on: %+v %v %v", row, applied, err)
	}
	row, err = DisableProjectAutoReload(project.ID)
	if err != nil || row.Enabled || row.StripePaymentMethodID != "pm_new" {
		t.Fatalf("disable keeps the card: %+v %v", row, err)
	}
	row, err = SaveProjectAutoReloadSettings(project.ID, ProjectAutoReloadSettings{ThresholdUSD: 20, AmountUSD: 50, MonthlyCapUSD: 300}, &on)
	if err != nil || !row.Enabled || row.ThresholdUSD != 20 {
		t.Fatalf("re-enable with the card on file: %+v %v", row, err)
	}
}

func TestAutoReloadOneOrderInFlightAndMonthlySum(t *testing.T) {
	db, _, project := setupAutoReloadTestDB(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if _, err := SaveProjectAutoReloadSettings(project.ID, ProjectAutoReloadSettings{ThresholdUSD: 10, AmountUSD: 50, MonthlyCapUSD: 300}, nil); err != nil {
		t.Fatal(err)
	}
	order, err := BeginProjectAutoReloadTopUp(project.ID, 50, "https://lazu.test", now)
	if err != nil {
		t.Fatal(err)
	}
	if !order.AutoReload || order.PaymentMethod != AutoReloadPaymentMethod || order.InvoiceStatus != "legacy" || order.PaidByUserID != project.OwnerUserID || order.StripeCustomerID != "cus_reload" {
		t.Fatalf("order shape: %+v", order)
	}
	if _, err := BeginProjectAutoReloadTopUp(project.ID, 50, "", now.Add(time.Second)); !errors.Is(err, ErrAutoReloadInFlight) {
		t.Fatalf("second order while one is pending: %v", err)
	}
	// A manual Checkout order in the same month never counts toward the cap.
	manual := TopUp{PublicID: "top_manual", TradeNo: "ref_manual", ProjectID: project.ID, UserId: project.OwnerUserID, Amount: 500, Status: common.TopUpStatusSuccess, CompleteTime: now.Unix()}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatal(err)
	}
	lastMonth := TopUp{PublicID: "top_last", TradeNo: "ref_last", ProjectID: project.ID, UserId: project.OwnerUserID, Amount: 70, AutoReload: true, Status: common.TopUpStatusSuccess, CompleteTime: now.AddDate(0, -1, 0).Unix()}
	if err := db.Create(&lastMonth).Error; err != nil {
		t.Fatal(err)
	}
	if err := RecordProjectAutoReloadIntent(order.Id, "pi_reload"); err != nil {
		t.Fatal(err)
	}
	_, credited, err := CompleteProjectTopUp(ProjectPaymentReceipt{Reference: order.TradeNo, CustomerID: "cus_reload", PaymentIntentID: "pi_reload", PaidMicrousd: 50_000_000, Currency: "USD", Method: "Visa ···· 4242"})
	if err != nil || !credited {
		t.Fatalf("off-session settlement: %v %v", credited, err)
	}
	spent, err := ProjectAutoReloadMonthSpentUSD(project.ID, now)
	if err != nil || spent != 50 {
		t.Fatalf("month sum counts only this month's auto-reloads: %d %v", spent, err)
	}
	var saved TopUp
	if err := db.First(&saved, order.Id).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != common.TopUpStatusSuccess || saved.PaymentMethod != "Visa ···· 4242" || !saved.AutoReload {
		t.Fatalf("settled order: %+v", saved)
	}
	var owner User
	db.First(&owner, project.OwnerUserID)
	if owner.Quota != 2_000_000+50_000_000 {
		t.Fatalf("wallet credited: %d", owner.Quota)
	}
	var row ProjectAutoReload
	if err := db.Where("project_id = ?", project.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.LastChargeAt != saved.CompleteTime {
		t.Fatalf("last_charge_at follows the credit: %d vs %d", row.LastChargeAt, saved.CompleteTime)
	}
	// A Checkout order still needs its session: the relaxed path is only
	// for auto-reload orders.
	checkout, err := BeginProjectTopUp(project.ID, project.OwnerUserID, "manual-op", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := CompleteProjectTopUp(ProjectPaymentReceipt{Reference: checkout.TradeNo, CustomerID: "cus_reload", PaymentIntentID: "pi_checkout", PaidMicrousd: 10_000_000, Currency: "USD"}); !errors.Is(err, ErrProjectInvalidInput) {
		t.Fatalf("checkout order without a session: %v", err)
	}
	// The next reload can start once nothing is pending.
	if _, err := BeginProjectAutoReloadTopUp(project.ID, 50, "", now.Add(time.Hour)); err != nil {
		t.Fatalf("next order after settlement: %v", err)
	}
}

func TestAutoReloadDeclinePausesOnceAndMailsOnce(t *testing.T) {
	db, owner, project := setupAutoReloadTestDB(t)
	now := time.Date(2026, 10, 19, 3, 12, 0, 0, time.UTC)
	if _, err := BeginProjectAutoReloadSetup(project.ID, ProjectAutoReloadSettings{ThresholdUSD: 10, AmountUSD: 50, MonthlyCapUSD: 300}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CompleteProjectAutoReloadSetup(ProjectAutoReloadCard{ProjectID: project.ID, CustomerID: "cus_reload", PaymentMethodID: "pm_card", Brand: "visa", Last4: "0341"}); err != nil {
		t.Fatal(err)
	}
	order, err := BeginProjectAutoReloadTopUp(project.ID, 50, "https://lazu.test", now)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := FailProjectAutoReloadTopUp(order.TradeNo, "insufficient_funds", "Your card has insufficient funds.", now)
	if err != nil || !failed {
		t.Fatalf("decline: %v %v", failed, err)
	}
	again, err := FailProjectAutoReloadTopUp(order.TradeNo, "insufficient_funds", "", now.Add(time.Minute))
	if err != nil || again {
		t.Fatalf("a second arrival changes nothing: %v %v", again, err)
	}
	var saved TopUp
	db.First(&saved, order.Id)
	if saved.Status != "failed" {
		t.Fatalf("order status: %s", saved.Status)
	}
	var row ProjectAutoReload
	db.Where("project_id = ?", project.ID).First(&row)
	if row.PausedReason != AutoReloadPauseCardDeclined || row.LastFailure != "insufficient_funds" || row.PausedAt != now.Unix() || !row.Enabled {
		t.Fatalf("pause: %+v", row)
	}
	var mails []ProjectNotification
	if err := db.Where("recipient_user_id = ?", owner.Id).Find(&mails).Error; err != nil {
		t.Fatal(err)
	}
	if len(mails) != 1 || mails[0].EventKey != "auto-reload-failed:"+order.PublicID+":"+itoa(owner.Id) || mails[0].Status != "pending" {
		t.Fatalf("one mail queued: %+v", mails)
	}
	email, err := mails[0].Email()
	if err != nil {
		t.Fatal(err)
	}
	if email.Receiver != owner.Email || !strings.Contains(email.Subject, "paused") || !strings.Contains(email.HTML, "Visa ···· 0341") || !strings.Contains(email.HTML, "Insufficient funds") || !strings.Contains(email.HTML, "auto_reload=card") {
		t.Fatalf("mail content: %s %s", email.Subject, email.HTML)
	}
	// Paused rows are not claimed by the worker; a resumed one is.
	if rows, err := ClaimProjectAutoReloadDue(now.Add(time.Hour), 10); err != nil || len(rows) != 0 {
		t.Fatalf("paused row claimed: %d %v", len(rows), err)
	}
	if _, _, err := CompleteProjectAutoReloadSetup(ProjectAutoReloadCard{ProjectID: project.ID, CustomerID: "cus_reload", PaymentMethodID: "pm_new", Brand: "mastercard", Last4: "4444"}); err != nil {
		t.Fatal(err)
	}
	db.Where("project_id = ?", project.ID).First(&row)
	if row.PausedReason != "" || row.LastFailure != "" {
		t.Fatalf("a new card ends the pause: %+v", row)
	}
	// The failed order no longer blocks the wallet.
	if _, err := BeginProjectAutoReloadTopUp(project.ID, 50, "", now.Add(time.Hour)); err != nil {
		t.Fatalf("next order after a decline: %v", err)
	}
	rows, err := ClaimProjectAutoReloadDue(now.Add(2*time.Hour), 10)
	if err != nil || len(rows) != 1 || rows[0].ProjectID != project.ID {
		t.Fatalf("resumed row is due: %d %v", len(rows), err)
	}
	if rows, err := ClaimProjectAutoReloadDue(now.Add(2*time.Hour+time.Minute), 10); err != nil || len(rows) != 0 {
		t.Fatalf("a claimed row is leased: %d %v", len(rows), err)
	}
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
