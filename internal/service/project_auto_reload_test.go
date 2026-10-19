package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/client"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const promotionConfigOptionKey = "PromotionConfig"

type autoReloadFixture struct {
	db      *gorm.DB
	owner   model.User
	project model.Project
	now     time.Time
}

func setupAutoReloadServiceDB(t *testing.T) autoReloadFixture {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	previousDB, previousRedis, previousSecret, previousMin := model.CoreDB(), common.RedisEnabled, common.CryptoSecret, setting.StripeMinTopUp
	common.RedisEnabled = false
	common.CryptoSecret = "auto-reload-service-test" // secret-scan: allow -- test fixture
	setting.StripeMinTopUp = 5
	model.SetCoreDB(db)
	model.EnsureColumnNamesInitialized()
	// Payment tests assert exact credits; the first-top-up bonus is off.
	// The first-top-up bonus (a hosted promotion, when present) is off so
	// credits are exact; the key is ignored where no promotion is registered.
	raw := `{"enabled":false}`
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previousPromotion, hadPromotion := common.OptionMap[promotionConfigOptionKey]
	common.OptionMap[promotionConfigOptionKey] = raw
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		common.RedisEnabled, common.CryptoSecret, setting.StripeMinTopUp = previousRedis, previousSecret, previousMin
		common.OptionMapRWMutex.Lock()
		if hadPromotion {
			common.OptionMap[promotionConfigOptionKey] = previousPromotion
		} else {
			delete(common.OptionMap, promotionConfigOptionKey)
		}
		common.OptionMapRWMutex.Unlock()
	})
	if err := db.AutoMigrate(&model.User{}, &model.Project{}, &model.ProjectMember{}, &model.TopUp{}, &model.ProjectAutoReload{}, &model.ProjectNotification{}, &model.WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner := model.User{Username: "reload-" + name, Email: "owner@example.com", Quota: 3_000_000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Update("stripe_customer_id", "cus_reload").Error; err != nil {
		t.Fatal(err)
	}
	project.StripeCustomerID = "cus_reload"
	return autoReloadFixture{db: db, owner: owner, project: project, now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
}

func (f autoReloadFixture) enable(t *testing.T, threshold, amount, cap int64) model.ProjectAutoReload {
	t.Helper()
	if _, err := model.BeginProjectAutoReloadSetup(f.project.ID, model.ProjectAutoReloadSettings{ThresholdUSD: threshold, AmountUSD: amount, MonthlyCapUSD: cap}, f.now); err != nil {
		t.Fatal(err)
	}
	row, applied, err := model.CompleteProjectAutoReloadSetup(model.ProjectAutoReloadCard{ProjectID: f.project.ID, CustomerID: "cus_reload", PaymentMethodID: "pm_card", Brand: "visa", Last4: "4242"})
	if err != nil || !applied {
		t.Fatalf("enable: %v %v", applied, err)
	}
	return row
}

func (f autoReloadFixture) row(t *testing.T) model.ProjectAutoReload {
	t.Helper()
	var row model.ProjectAutoReload
	if err := f.db.Where("project_id = ?", f.project.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func (f autoReloadFixture) quota(t *testing.T) int64 {
	t.Helper()
	var owner model.User
	if err := f.db.First(&owner, f.owner.Id).Error; err != nil {
		t.Fatal(err)
	}
	return int64(owner.Quota)
}

func (f autoReloadFixture) orders(t *testing.T) []model.TopUp {
	t.Helper()
	var orders []model.TopUp
	if err := f.db.Where("project_id = ? AND auto_reload = ?", f.project.ID, true).Order("id ASC").Find(&orders).Error; err != nil {
		t.Fatal(err)
	}
	return orders
}

// stripeStub answers PaymentIntents.New with one scripted body and records
// what was asked.
type stripeStub struct {
	status   int
	body     func(form url.Values) string
	calls    atomic.Int32
	lastForm url.Values
	lastKey  string
}

func newStripeStub(t *testing.T, stub *stripeStub) *client.API {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/payment_intents" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		stub.lastForm, stub.lastKey = form, r.Header.Get("Idempotency-Key")
		stub.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(stub.status)
		fmt.Fprint(w, stub.body(form))
	}))
	t.Cleanup(server.Close)
	api := &client.API{}
	api.Init("sk_test", stripe.NewBackendsWithConfig(&stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0)}))
	return api
}

func succeededIntent(form url.Values) string {
	return fmt.Sprintf(`{"id":"pi_ok","object":"payment_intent","status":"succeeded","amount":%s,"currency":"usd","customer":"cus_reload",
		"metadata":{"lazu_top_up_reference":%q,"lazu_auto_reload":"charge"},
		"latest_charge":{"id":"ch_ok","object":"charge","payment_intent":"pi_ok","receipt_url":"https://pay.stripe.com/receipts/ok","payment_method_details":{"type":"card","card":{"brand":"visa","last4":"4242"}}}}`,
		form.Get("amount"), form.Get("metadata[lazu_top_up_reference]"))
}

func TestAutoReloadChargesOnlyUnderThreshold(t *testing.T) {
	f := setupAutoReloadServiceDB(t)
	row := f.enable(t, 10, 50, 300) // balance $3 < $10
	stub := &stripeStub{status: http.StatusOK, body: succeededIntent}
	api := newStripeStub(t, stub)

	// Above the line: nothing happens, Stripe is not called.
	if err := f.db.Model(&model.User{}).Where("id = ?", f.owner.Id).Update("quota", 20_000_000).Error; err != nil {
		t.Fatal(err)
	}
	if err := ProcessProjectAutoReload(t.Context(), api, row, f.now); err != nil {
		t.Fatal(err)
	}
	if stub.calls.Load() != 0 || len(f.orders(t)) != 0 {
		t.Fatalf("charged above the threshold: calls=%d orders=%d", stub.calls.Load(), len(f.orders(t)))
	}

	// Under the line: one order, one off-session intent, wallet credited.
	if err := f.db.Model(&model.User{}).Where("id = ?", f.owner.Id).Update("quota", 3_000_000).Error; err != nil {
		t.Fatal(err)
	}
	if err := ProcessProjectAutoReload(t.Context(), api, row, f.now); err != nil {
		t.Fatal(err)
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("expected one PaymentIntent, got %d", stub.calls.Load())
	}
	form := stub.lastForm
	if form.Get("amount") != "5000" || form.Get("currency") != "usd" || form.Get("customer") != "cus_reload" || form.Get("payment_method") != "pm_card" ||
		form.Get("off_session") != "true" || form.Get("confirm") != "true" || form.Get("error_on_requires_action") != "true" || form.Get("metadata[lazu_auto_reload]") == "" {
		t.Fatalf("intent params: %v", form)
	}
	orders := f.orders(t)
	if len(orders) != 1 || orders[0].Status != common.TopUpStatusSuccess || orders[0].StripePaymentIntentID != "pi_ok" || orders[0].StripeChargeID != "ch_ok" || orders[0].PaymentMethod != "Visa ···· 4242" || orders[0].StripeReceiptURL == "" {
		t.Fatalf("settled order: %+v", orders)
	}
	if stub.lastKey != "lazu-auto-reload:"+orders[0].PublicID {
		t.Fatalf("idempotency key per order: %q", stub.lastKey)
	}
	if got := f.quota(t); got != 3_000_000+50_000_000 {
		t.Fatalf("wallet after reload: %d", got)
	}
	if f.row(t).LastChargeAt == 0 {
		t.Fatal("last_charge_at not recorded")
	}
	// The payment mail is queued for the owner like any card payment.
	var mails int64
	f.db.Model(&model.ProjectNotification{}).Where("event_key LIKE ?", "payment:"+orders[0].PublicID+":%").Count(&mails)
	if mails != 1 {
		t.Fatalf("payment mail queued once, got %d", mails)
	}
}

func TestAutoReloadClampsAmountToTheOperatorFloor(t *testing.T) {
	f := setupAutoReloadServiceDB(t)
	row := f.enable(t, 10, 5, 300)
	setting.StripeMinTopUp = 8 // the operator raised the floor after the owner chose $5
	stub := &stripeStub{status: http.StatusOK, body: succeededIntent}
	api := newStripeStub(t, stub)
	if err := ProcessProjectAutoReload(t.Context(), api, row, f.now); err != nil {
		t.Fatal(err)
	}
	if stub.lastForm.Get("amount") != "800" {
		t.Fatalf("amount clamped to the floor: %v", stub.lastForm.Get("amount"))
	}
	if orders := f.orders(t); len(orders) != 1 || orders[0].Amount != 8 {
		t.Fatalf("order carries the charged amount: %+v", orders)
	}
}

func TestAutoReloadMonthlyCapPausesAndResumesNextMonth(t *testing.T) {
	f := setupAutoReloadServiceDB(t)
	row := f.enable(t, 10, 50, 120)
	spent := model.TopUp{PublicID: "top_spent", TradeNo: "ref_spent", ProjectID: f.project.ID, UserId: f.owner.Id, Amount: 100, AutoReload: true, Status: common.TopUpStatusSuccess, CompleteTime: f.now.Add(-48 * time.Hour).Unix()}
	if err := f.db.Create(&spent).Error; err != nil {
		t.Fatal(err)
	}
	stub := &stripeStub{status: http.StatusOK, body: succeededIntent}
	api := newStripeStub(t, stub)
	if err := ProcessProjectAutoReload(t.Context(), api, row, f.now); err != nil {
		t.Fatal(err)
	}
	if stub.calls.Load() != 0 {
		t.Fatal("charged over the monthly cap")
	}
	if paused := f.row(t); paused.PausedReason != model.AutoReloadPauseCapReached || paused.PausedAt != f.now.Unix() {
		t.Fatalf("cap pause: %+v", paused)
	}
	// Still this month: the paused row is read again and left paused.
	if err := ProcessProjectAutoReload(t.Context(), api, f.row(t), f.now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if stub.calls.Load() != 0 || f.row(t).PausedReason != model.AutoReloadPauseCapReached {
		t.Fatal("cap pause did not hold within the month")
	}
	// Next month: the cap has room, the pause clears and the card is charged.
	nextMonth := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	if err := ProcessProjectAutoReload(t.Context(), api, f.row(t), nextMonth); err != nil {
		t.Fatal(err)
	}
	if stub.calls.Load() != 1 || f.row(t).PausedReason != "" {
		t.Fatalf("cap did not resume: calls=%d row=%+v", stub.calls.Load(), f.row(t))
	}
}

func TestAutoReloadDeclinedCardPausesUntilChanged(t *testing.T) {
	f := setupAutoReloadServiceDB(t)
	row := f.enable(t, 10, 50, 300)
	stub := &stripeStub{status: http.StatusPaymentRequired, body: func(form url.Values) string {
		return fmt.Sprintf(`{"error":{"type":"card_error","code":"card_declined","decline_code":"insufficient_funds","message":"Your card has insufficient funds.",
			"payment_intent":{"id":"pi_declined","object":"payment_intent","status":"requires_payment_method","customer":"cus_reload","metadata":{"lazu_top_up_reference":%q,"lazu_auto_reload":"charge"}}}}`, form.Get("metadata[lazu_top_up_reference]"))
	}}
	api := newStripeStub(t, stub)
	if err := ProcessProjectAutoReload(t.Context(), api, row, f.now); err != nil {
		t.Fatal(err)
	}
	orders := f.orders(t)
	if len(orders) != 1 || orders[0].Status != "failed" || orders[0].StripePaymentIntentID != "pi_declined" {
		t.Fatalf("declined order: %+v", orders)
	}
	paused := f.row(t)
	if paused.PausedReason != model.AutoReloadPauseCardDeclined || paused.LastFailure != "insufficient_funds" {
		t.Fatalf("pause after decline: %+v", paused)
	}
	if got := f.quota(t); got != 3_000_000 {
		t.Fatalf("wallet must not move on a decline: %d", got)
	}
	var mails int64
	f.db.Model(&model.ProjectNotification{}).Where("event_key LIKE ?", "auto-reload-failed:%").Count(&mails)
	if mails != 1 {
		t.Fatalf("one decline mail, got %d", mails)
	}
	// The worker never retries a declined card: the row is not due.
	if rows, err := model.ClaimProjectAutoReloadDue(f.now.Add(time.Hour), 10); err != nil || len(rows) != 0 {
		t.Fatalf("declined row claimed: %d %v", len(rows), err)
	}
	if err := RunProjectAutoReload(t.Context(), api, f.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("retried a declined card: %d calls", stub.calls.Load())
	}
}

func TestAutoReloadLeavesAnInFlightOrderAlone(t *testing.T) {
	f := setupAutoReloadServiceDB(t)
	row := f.enable(t, 10, 50, 300)
	pending, err := model.BeginProjectAutoReloadTopUp(f.project.ID, 50, "", f.now.Add(-5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	stub := &stripeStub{status: http.StatusOK, body: succeededIntent}
	api := newStripeStub(t, stub)
	if err := ProcessProjectAutoReload(t.Context(), api, row, f.now); err != nil {
		t.Fatal(err)
	}
	if stub.calls.Load() != 0 || len(f.orders(t)) != 1 {
		t.Fatalf("a second reload started while one was pending: calls=%d orders=%d", stub.calls.Load(), len(f.orders(t)))
	}
	// The webhook settles it; the next pass is free to reload again.
	intent := &stripe.PaymentIntent{ID: "pi_hook", Status: stripe.PaymentIntentStatusSucceeded, Amount: 5000, Currency: "usd", Customer: &stripe.Customer{ID: "cus_reload"},
		Metadata: map[string]string{"lazu_top_up_reference": pending.TradeNo, AutoReloadMetadataKey: "charge"}}
	credited, err := SettleAutoReloadIntent(intent, f.now)
	if err != nil || !credited {
		t.Fatalf("webhook settlement: %v %v", credited, err)
	}
	if credited, err := SettleAutoReloadIntent(intent, f.now); err != nil || credited {
		t.Fatalf("replayed event credits twice: %v %v", credited, err)
	}
	if got := f.quota(t); got != 3_000_000+50_000_000 {
		t.Fatalf("wallet after webhook: %d", got)
	}
	// A Checkout intent carries the reference key too; without the mark it
	// is not auto-reload's to settle.
	checkout, err := model.BeginProjectTopUp(f.project.ID, f.owner.Id, "manual", 10)
	if err != nil {
		t.Fatal(err)
	}
	foreign := &stripe.PaymentIntent{ID: "pi_checkout", Status: stripe.PaymentIntentStatusSucceeded, Amount: 1000, Currency: "usd", Customer: &stripe.Customer{ID: "cus_reload"},
		Metadata: map[string]string{"lazu_top_up_reference": checkout.TradeNo}}
	if credited, err := SettleAutoReloadIntent(foreign, f.now); err != nil || credited {
		t.Fatalf("checkout intent settled by auto-reload: %v %v", credited, err)
	}
	// A failed intent reported by the webhook pauses reloads too.
	second, err := model.BeginProjectAutoReloadTopUp(f.project.ID, 50, "", f.now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	failed := &stripe.PaymentIntent{ID: "pi_fail", Status: stripe.PaymentIntentStatusRequiresPaymentMethod, Amount: 5000, Currency: "usd", Customer: &stripe.Customer{ID: "cus_reload"},
		Metadata:         map[string]string{"lazu_top_up_reference": second.TradeNo, AutoReloadMetadataKey: "charge"},
		LastPaymentError: &stripe.Error{Type: stripe.ErrorTypeCard, Code: stripe.ErrorCodeExpiredCard, Msg: "Your card has expired."}}
	if _, err := SettleAutoReloadIntent(failed, f.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if paused := f.row(t); paused.PausedReason != model.AutoReloadPauseCardDeclined || paused.LastFailure != "expired_card" {
		t.Fatalf("webhook failure pause: %+v", paused)
	}
}
