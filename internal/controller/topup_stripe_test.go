package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"
	"gorm.io/gorm"
)

func setupStripeControllerTestDB(t *testing.T) func() {
	t.Helper()

	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)

	if err := db.AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatalf("failed to migrate topups: %v", err)
	}

	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	}
}

func newJSONContext(method string, body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, w
}

func decodeJSONMap(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}
	return payload
}

// assertConsoleError checks the console failure envelope: real status,
// success=false and the catalog code under error.code.
func assertConsoleError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	if w.Code != status {
		t.Fatalf("expected status %d, got %d: %s", status, w.Code, w.Body.String())
	}
	payload := decodeJSONMap(t, w.Body.Bytes())
	body, _ := payload["error"].(map[string]any)
	if payload["success"] != false || body == nil || body["code"] != code {
		t.Fatalf("expected error code %q, got %s", code, w.Body.String())
	}
	if payload["data"] != nil {
		t.Fatalf("a failure carries no data: %s", w.Body.String())
	}
	return body
}

func TestRequestStripeAmount_InvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, w := newJSONContext(http.MethodPost, "{invalid")

	RequestStripeAmount(c)

	body := assertConsoleError(t, w, http.StatusBadRequest, "invalid_request")
	if body["param"] != "amount" {
		t.Fatalf("expected param=amount, got %v", body["param"])
	}
}

func TestRequestStripeAmount_RejectsAmountBelowMin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, w := newJSONContext(http.MethodPost, `{"amount":0,"payment_method":"stripe"}`)

	RequestStripeAmount(c)

	assertConsoleError(t, w, http.StatusBadRequest, "invalid_amount")
}

func TestRequestStripePay_RejectsInvalidPaymentMethod(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, w := newJSONContext(http.MethodPost, `{"amount":10,"payment_method":"paypal"}`)

	RequestStripePay(c)

	body := assertConsoleError(t, w, http.StatusBadRequest, "invalid_request")
	if body["param"] != "payment_method" {
		t.Fatalf("expected param=payment_method, got %v", body["param"])
	}
}

// An out-of-range top-up is a validation failure carrying a code, not an HTTP
// 200 whose body has to be sniffed for a stray number. The floor is the
// operator's (option MinTopUpUSD) and the refusal names it.
func TestRequestStripePay_RejectsAmountOutOfRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := setting.StripeMinTopUp
	setting.StripeMinTopUp = 5
	t.Cleanup(func() { setting.StripeMinTopUp = previous })
	for _, body := range []string{`{"amount":0,"payment_method":"stripe"}`, `{"amount":4,"payment_method":"stripe"}`, `{"amount":10001,"payment_method":"stripe"}`} {
		c, w := newJSONContext(http.MethodPost, body)

		RequestStripePay(c)

		failure := assertConsoleError(t, w, http.StatusBadRequest, "invalid_amount")
		details, _ := failure["details"].(map[string]any)
		if details["min_amount"] != float64(5) || details["max_amount"] != float64(10000) {
			t.Fatalf("invalid_amount must carry the range for %s: %v", body, failure)
		}
	}
}

// Raising the floor in the console moves the gate; an amount the old floor
// admitted is refused once the option says so, and the floor never drops
// below $1 or above the ceiling however it was saved.
func TestRequestStripePay_FloorFollowsOption(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := setting.StripeMinTopUp
	t.Cleanup(func() { setting.StripeMinTopUp = previous })
	for _, tc := range []struct {
		saved  int
		amount string
		floor  float64
	}{
		{saved: 10, amount: "5", floor: 10},
		{saved: 0, amount: "0", floor: 1},
		{saved: 20000, amount: "9999", floor: 10000},
	} {
		setting.StripeMinTopUp = tc.saved
		c, w := newJSONContext(http.MethodPost, `{"amount":`+tc.amount+`,"payment_method":"stripe"}`)

		RequestStripePay(c)

		failure := assertConsoleError(t, w, http.StatusBadRequest, "invalid_amount")
		details, _ := failure["details"].(map[string]any)
		if details["min_amount"] != tc.floor {
			t.Fatalf("saved %d: expected floor %v, got %v", tc.saved, tc.floor, failure)
		}
	}
}

func TestStripeWebhook_InvalidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldSecret := setting.StripeWebhookSecret
	setting.StripeWebhookSecret = "whsec_test_secret"
	defer func() { setting.StripeWebhookSecret = oldSecret }()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/stripe/webhook", bytes.NewBufferString(`{"id":"evt_1"}`))
	req.Header.Set("Stripe-Signature", "invalid-signature")
	c.Request = req

	StripeWebhook(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestStripeWebhook_IgnoresUnknownEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "whsec_test_secret"
	oldSecret := setting.StripeWebhookSecret
	setting.StripeWebhookSecret = secret
	defer func() { setting.StripeWebhookSecret = oldSecret }()

	payload := []byte(`{
		"id":"evt_123",
		"object":"event",
		"type":"charge.succeeded",
		"data":{"object":{"id":"ch_123"}}
	}`)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   payload,
		Secret:    secret,
		Timestamp: time.Now(),
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/stripe/webhook", bytes.NewBuffer(signed.Payload))
	req.Header.Set("Stripe-Signature", signed.Header)
	c.Request = req

	StripeWebhook(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestGetStripePaymentMethod_Fallbacks(t *testing.T) {
	if got := getStripePaymentMethod(nil); got != PaymentMethodStripe {
		t.Fatalf("expected default payment method %q, got %q", PaymentMethodStripe, got)
	}

	checkoutSession := &stripe.CheckoutSession{
		PaymentMethodTypes: []string{"card"},
	}
	if got := getStripePaymentMethod(checkoutSession); got != "card" {
		t.Fatalf("expected first payment method type, got %q", got)
	}
}
