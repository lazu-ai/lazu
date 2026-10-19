package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/stripe/stripe-go/v81"
)

func TestLegacyCheckoutRetriesBindingBeforeCreditingWallet(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.TopUp{}, &model.Log{}); err != nil {
		t.Fatal(err)
	}
	previousLogDB := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(previousLogDB) })
	owner := model.User{Username: "legacy-webhook-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	order := model.TopUp{PublicID: "topup-legacy-retry", TradeNo: "ref-legacy-retry", UserId: owner.Id, ProjectID: project.ID,
		Amount: 5, Status: common.TopUpStatusPending, InvoiceStatus: "legacy", StripePromotionCodes: true, CreditedMicrousd: 5_000_000}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	failBinding := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/payment_intents/pi_legacy_retry" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if failBinding {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":{"type":"api_error","message":"temporary"}}`)
			return
		}
		fmt.Fprint(w, `{"id":"pi_legacy_retry","customer":"cus_legacy_retry","latest_charge":{"id":"ch_legacy_retry","customer":"cus_legacy_retry","payment_intent":"pi_legacy_retry","amount":400,"currency":"usd"}}`)
	}))
	defer server.Close()
	oldBackend, oldKey := stripe.GetBackend(stripe.APIBackend), setting.StripeApiSecret
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client()}))
	setting.StripeApiSecret = "sk_test"
	t.Cleanup(func() {
		stripe.SetBackend(stripe.APIBackend, oldBackend)
		setting.StripeApiSecret = oldKey
	})
	var event stripe.Event
	if err := json.Unmarshal([]byte(`{"id":"evt_legacy_retry","type":"checkout.session.completed","data":{"object":{"id":"cs_legacy_retry","client_reference_id":"ref-legacy-retry","customer":"cus_legacy_retry","status":"complete","payment_status":"paid","amount_subtotal":500,"amount_total":400,"currency":"usd","payment_intent":"pi_legacy_retry","payment_method_types":["card"]}}}`), &event); err != nil {
		t.Fatal(err)
	}
	send := func() int {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/stripe/webhook", nil)
		sessionCompleted(c, event)
		return response.Code
	}
	if code := send(); code != http.StatusServiceUnavailable {
		t.Fatalf("transient Stripe failure returned %d", code)
	}
	if err := db.First(&order, order.Id).Error; err != nil || order.Status != common.TopUpStatusPending {
		t.Fatalf("unbound payment was committed: %+v %v", order, err)
	}
	if err := db.First(&owner, owner.Id).Error; err != nil || owner.Quota != 0 {
		t.Fatalf("unbound payment credited wallet: %d %v", owner.Quota, err)
	}
	failBinding = false
	if code := send(); code != http.StatusOK {
		t.Fatalf("retry did not complete: %d", code)
	}
	if err := db.First(&order, order.Id).Error; err != nil || order.Status != common.TopUpStatusSuccess || order.StripePaymentIntentID != "pi_legacy_retry" || order.PaidAmount != 4 {
		t.Fatalf("retry lost binding or discounted amount: %+v %v", order, err)
	}
	if err := db.First(&owner, owner.Id).Error; err != nil || owner.Quota != 5_000_000 {
		t.Fatalf("retry wallet credit: %d %v", owner.Quota, err)
	}
}

func TestUnrelatedStripeRefundAndDisputeAreAcknowledged(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/checkout/sessions":
			if r.URL.Query().Get("payment_intent") != "pi_foreign" {
				t.Errorf("unexpected Checkout lookup: %s", r.URL.String())
			}
			fmt.Fprint(w, `{"object":"list","data":[],"has_more":false}`)
		case "/v1/payment_intents/pi_foreign":
			fmt.Fprint(w, `{"id":"pi_foreign","metadata":{}}`)
		case "/v1/charges/ch_foreign":
			fmt.Fprint(w, `{"id":"ch_foreign","payment_intent":"pi_foreign","customer":"cus_foreign","amount":500,"currency":"usd"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	oldBackend, oldKey := stripe.GetBackend(stripe.APIBackend), setting.StripeApiSecret
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client()}))
	setting.StripeApiSecret = "sk_test"
	t.Cleanup(func() {
		stripe.SetBackend(stripe.APIBackend, oldBackend)
		setting.StripeApiSecret = oldKey
	})
	for _, test := range []struct {
		name    string
		handler func(*gin.Context, stripe.Event)
		object  string
	}{
		{"refund", projectRefundWebhook, `{"id":"ch_foreign","payment_intent":"pi_foreign","customer":"cus_foreign","amount":500,"amount_refunded":500,"currency":"usd"}`},
		{"dispute", projectDisputeWebhook, `{"id":"dp_foreign","charge":"ch_foreign","amount":500,"reason":"fraudulent"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/user/stripe/webhook", nil)
			test.handler(c, stripe.Event{Data: &stripe.EventData{Raw: json.RawMessage(test.object)}})
			if response.Code != http.StatusOK || c.IsAborted() {
				t.Fatalf("unrelated Stripe charge was not acknowledged: %d", response.Code)
			}
		})
	}
}
