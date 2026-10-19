package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const promotionConfigOptionKey = "PromotionConfig"

func setupAutoReloadWebhookTest(t *testing.T, stripeHandler http.HandlerFunc) (*gorm.DB, model.User, model.Project) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	originalSchedule := scheduleProjectNotificationWake
	scheduleProjectNotificationWake = func(int64) {}
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	previousDB, previousRedis, previousSecret, previousKey, previousHook := model.CoreDB(), common.RedisEnabled, common.CryptoSecret, setting.StripeApiSecret, setting.StripeWebhookSecret
	common.RedisEnabled = false
	common.CryptoSecret = "auto-reload-webhook-test" // secret-scan: allow -- test fixture
	setting.StripeApiSecret, setting.StripeWebhookSecret = "sk_test", "whsec_auto_reload"
	model.SetCoreDB(db)
	model.EnsureColumnNamesInitialized()
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
	server := httptest.NewServer(stripeHandler)
	oldBackend := stripe.GetBackend(stripe.APIBackend)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0)}))
	t.Cleanup(func() {
		stripe.SetBackend(stripe.APIBackend, oldBackend)
		server.Close()
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		common.RedisEnabled, common.CryptoSecret, setting.StripeApiSecret, setting.StripeWebhookSecret = previousRedis, previousSecret, previousKey, previousHook
		scheduleProjectNotificationWake = originalSchedule
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
	owner := model.User{Username: "hook-" + name, Email: "owner@example.com", Quota: 1_000_000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Update("stripe_customer_id", "cus_hook").Error; err != nil {
		t.Fatal(err)
	}
	project.StripeCustomerID = "cus_hook"
	return db, owner, project
}

func sendStripeEvent(t *testing.T, payload string) int {
	t.Helper()
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: []byte(payload), Secret: setting.StripeWebhookSecret, Timestamp: time.Now()})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/stripe/webhook", bytes.NewBuffer(signed.Payload))
	req.Header.Set("Stripe-Signature", signed.Header)
	c.Request = req
	StripeWebhook(c)
	return w.Code
}

// A setup-mode Checkout Session never reaches the payment path: it has no
// order to credit. It saves the card and turns auto-reload on.
func TestStripeWebhook_SetupSessionSavesAutoReloadCard(t *testing.T) {
	var projectPublicID string
	db, _, project := setupAutoReloadWebhookTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/setup_intents/seti_hook":
			fmt.Fprintf(w, `{"id":"seti_hook","object":"setup_intent","status":"succeeded","customer":"cus_hook","usage":"off_session",
				"metadata":{"lazu_project_id":%q,"lazu_auto_reload":"setup"},
				"payment_method":{"id":"pm_hook","object":"payment_method","type":"card","customer":"cus_hook","card":{"brand":"visa","last4":"4242"}}}`, projectPublicID)
		default:
			http.NotFound(w, r)
		}
	})
	projectPublicID = project.PublicID
	if _, err := model.BeginProjectAutoReloadSetup(project.ID, model.ProjectAutoReloadSettings{ThresholdUSD: 10, AmountUSD: 50, MonthlyCapUSD: 300}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := model.AttachProjectAutoReloadSetupSession(project.ID, "cs_setup"); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"id":"evt_setup","object":"event","type":"checkout.session.completed","data":{"object":{"id":"cs_setup","object":"checkout.session","mode":"setup","status":"complete","customer":"cus_hook","setup_intent":"seti_hook","client_reference_id":"auto-reload:%s","metadata":{"lazu_project_id":%q,"lazu_auto_reload":"setup"}}}}`, project.PublicID, project.PublicID)
	for range 2 {
		if code := sendStripeEvent(t, payload); code != http.StatusOK {
			t.Fatalf("setup session webhook: %d", code)
		}
	}
	var row model.ProjectAutoReload
	if err := db.Where("project_id = ?", project.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !row.Enabled || row.StripePaymentMethodID != "pm_hook" || row.CardLabel() != "Visa ···· 4242" || row.SetupSessionID != "" {
		t.Fatalf("card not saved: %+v", row)
	}
	var orders int64
	db.Model(&model.TopUp{}).Count(&orders)
	if orders != 0 {
		t.Fatal("a setup session must not create an order")
	}
}

// payment_intent.succeeded settles a reload the worker did not finish, once;
// payment_intent.payment_failed pauses reloads. Checkout's intents, which
// carry the same reference key without the auto-reload mark, are left to
// their session event.
func TestStripeWebhook_PaymentIntentSettlesAutoReload(t *testing.T) {
	var reference string
	db, owner, project := setupAutoReloadWebhookTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/payment_intents/pi_hook":
			fmt.Fprintf(w, `{"id":"pi_hook","object":"payment_intent","status":"succeeded","amount":5000,"currency":"usd","customer":"cus_hook",
				"metadata":{"lazu_top_up_reference":%q,"lazu_auto_reload":"charge"},
				"latest_charge":{"id":"ch_hook","object":"charge","payment_intent":"pi_hook","receipt_url":"https://pay.stripe.com/receipts/hook","payment_method_details":{"type":"card","card":{"brand":"visa","last4":"4242"}}}}`, reference)
		case "/v1/payment_intents/pi_hook_failed":
			fmt.Fprintf(w, `{"id":"pi_hook_failed","object":"payment_intent","status":"requires_payment_method","amount":5000,"currency":"usd","customer":"cus_hook",
				"metadata":{"lazu_top_up_reference":%q,"lazu_auto_reload":"charge"},
				"last_payment_error":{"type":"card_error","code":"card_declined","decline_code":"do_not_honor","message":"Your card was declined."}}`, reference)
		default:
			http.NotFound(w, r)
		}
	})
	if _, err := model.BeginProjectAutoReloadSetup(project.ID, model.ProjectAutoReloadSettings{ThresholdUSD: 10, AmountUSD: 50, MonthlyCapUSD: 300}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := model.CompleteProjectAutoReloadSetup(model.ProjectAutoReloadCard{ProjectID: project.ID, CustomerID: "cus_hook", PaymentMethodID: "pm_hook", Brand: "visa", Last4: "4242"}); err != nil {
		t.Fatal(err)
	}
	order, err := model.BeginProjectAutoReloadTopUp(project.ID, 50, "https://lazu.test", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	reference = order.TradeNo
	// Checkout's intent: same reference key, no mark, no fetch, no change.
	if code := sendStripeEvent(t, fmt.Sprintf(`{"id":"evt_pi_checkout","object":"event","type":"payment_intent.succeeded","data":{"object":{"id":"pi_checkout","object":"payment_intent","status":"succeeded","metadata":{"lazu_top_up_reference":%q}}}}`, reference)); code != http.StatusOK {
		t.Fatalf("checkout intent event: %d", code)
	}
	var pending model.TopUp
	db.First(&pending, order.Id)
	if pending.Status != common.TopUpStatusPending {
		t.Fatalf("checkout intent settled an auto-reload order: %s", pending.Status)
	}
	succeeded := fmt.Sprintf(`{"id":"evt_pi_ok","object":"event","type":"payment_intent.succeeded","data":{"object":{"id":"pi_hook","object":"payment_intent","status":"succeeded","amount":5000,"currency":"usd","customer":"cus_hook","metadata":{"lazu_top_up_reference":%q,"lazu_auto_reload":"charge"}}}}`, reference)
	for range 2 {
		if code := sendStripeEvent(t, succeeded); code != http.StatusOK {
			t.Fatalf("intent webhook: %d", code)
		}
	}
	var settled model.TopUp
	db.First(&settled, order.Id)
	if settled.Status != common.TopUpStatusSuccess || settled.StripePaymentIntentID != "pi_hook" || settled.StripeChargeID != "ch_hook" || settled.PaymentMethod != "Visa ···· 4242" {
		t.Fatalf("settled order: %+v", settled)
	}
	var paid model.User
	db.First(&paid, owner.Id)
	if paid.Quota != 1_000_000+50_000_000 {
		t.Fatalf("credited once: %d", paid.Quota)
	}
	// A later reload declined: the failed event pauses reloads and mails once.
	second, err := model.BeginProjectAutoReloadTopUp(project.ID, 50, "https://lazu.test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reference = second.TradeNo
	failed := fmt.Sprintf(`{"id":"evt_pi_failed","object":"event","type":"payment_intent.payment_failed","data":{"object":{"id":"pi_hook_failed","object":"payment_intent","status":"requires_payment_method","customer":"cus_hook","metadata":{"lazu_top_up_reference":%q,"lazu_auto_reload":"charge"}}}}`, reference)
	for range 2 {
		if code := sendStripeEvent(t, failed); code != http.StatusOK {
			t.Fatalf("failed intent webhook: %d", code)
		}
	}
	var row model.ProjectAutoReload
	db.Where("project_id = ?", project.ID).First(&row)
	if row.PausedReason != model.AutoReloadPauseCardDeclined || row.LastFailure != "do_not_honor" {
		t.Fatalf("pause after failed intent: %+v", row)
	}
	var mails int64
	db.Model(&model.ProjectNotification{}).Where("event_key LIKE ?", "auto-reload-failed:%").Count(&mails)
	if mails != 1 {
		t.Fatalf("decline mail queued once, got %d", mails)
	}
	if paid.Quota != 1_000_000+50_000_000 {
		t.Fatalf("a failed intent must not move the wallet: %d", paid.Quota)
	}
}
