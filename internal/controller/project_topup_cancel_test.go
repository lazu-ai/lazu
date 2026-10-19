package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/stripe/stripe-go/v81"
)

// A pending order carries its checkout page back to the wallet and can be
// withdrawn by someone who may top up; Stripe's session is expired first, and
// a session Stripe reports as already complete is never marked canceled.
func TestCancelProjectTopUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	owner := model.User{Username: "cancel-owner", Email: "cancel-owner@example.com"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	member := model.User{Username: "cancel-member", Email: "cancel-member@example.com"}
	if err := db.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Update("stripe_customer_id", "cus_cancel").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ProjectMember{ProjectID: project.ID, UserID: member.Id, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive}).Error; err != nil {
		t.Fatal(err)
	}
	order, err := model.BeginProjectTopUp(project.ID, owner.Id, "cancel-me", 20, model.ProjectCheckoutConfig{ReturnBase: "https://lazu.ai"})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.AttachProjectCheckout(order.Id, "cus_cancel", "cs_cancel", "https://checkout.stripe.com/c/pay/cs_cancel"); err != nil {
		t.Fatal(err)
	}
	paid, err := model.BeginProjectTopUp(project.ID, owner.Id, "already-paid", 10, model.ProjectCheckoutConfig{ReturnBase: "https://lazu.ai"})
	if err != nil {
		t.Fatal(err)
	}
	if err := model.AttachProjectCheckout(paid.Id, "cus_cancel", "cs_paid", "https://checkout.stripe.com/c/pay/cs_paid"); err != nil {
		t.Fatal(err)
	}

	expired := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions/cs_cancel/expire":
			expired++
			fmt.Fprint(w, `{"id":"cs_cancel","object":"checkout.session","status":"expired"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions/cs_paid/expire":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"You cannot expire a Checkout Session that is already complete."}}`)
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
	t.Setenv("PROJECTS_ENABLED", "true")

	request := func(userID int, method, path string) *httptest.ResponseRecorder {
		t.Helper()
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("id", userID); c.Set("role", common.RoleCommonUser) })
		routes := router.Group("/api/project/:id", middleware.ProjectConsoleContext())
		routes.GET("/topups", ListProjectTopUps)
		routes.POST("/topups/:topup_id/cancel", CancelProjectTopUp)
		result := httptest.NewRecorder()
		router.ServeHTTP(result, httptest.NewRequest(method, "/api/project/"+project.PublicID+path, nil))
		return result
	}
	rowsOf := func(body []byte) map[string]map[string]any {
		t.Helper()
		var payload struct {
			Data struct {
				Items []map[string]any `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		rows := map[string]map[string]any{}
		for _, item := range payload.Data.Items {
			rows[item["id"].(string)] = item
		}
		return rows
	}

	// Pending rows carry the checkout page and its deadline; nothing else does.
	list := request(owner.Id, http.MethodGet, "/topups")
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	rows := rowsOf(list.Body.Bytes())
	if rows[order.PublicID]["pay_link"] != "https://checkout.stripe.com/c/pay/cs_cancel" || rows[order.PublicID]["pay_by"] != float64(order.CreateTime+24*3600) {
		t.Fatalf("pending row must carry pay_link and pay_by: %v", rows[order.PublicID])
	}

	// A member who may not top up may not withdraw an order either.
	if got := request(member.Id, http.MethodPost, "/topups/"+order.PublicID+"/cancel"); got.Code != http.StatusForbidden {
		t.Fatalf("member cancel: %d %s", got.Code, got.Body.String())
	}

	got := request(owner.Id, http.MethodPost, "/topups/"+order.PublicID+"/cancel")
	if got.Code != http.StatusOK || expired != 1 || !strings.Contains(got.Body.String(), `"payment_status":"canceled"`) || strings.Contains(got.Body.String(), "pay_link") {
		t.Fatalf("cancel: %d expired=%d %s", got.Code, expired, got.Body.String())
	}
	var stored model.TopUp
	if err := db.First(&stored, order.Id).Error; err != nil || stored.Status != common.TopUpStatusCanceled {
		t.Fatalf("stored status after cancel: %q %v", stored.Status, err)
	}
	// Cancelling twice is a conflict, not a second success.
	if got := request(owner.Id, http.MethodPost, "/topups/"+order.PublicID+"/cancel"); got.Code != http.StatusConflict {
		t.Fatalf("second cancel: %d %s", got.Code, got.Body.String())
	}

	// Stripe says the session already completed: the order stays pending for
	// the webhook to credit, and the caller hears a conflict.
	if got := request(owner.Id, http.MethodPost, "/topups/"+paid.PublicID+"/cancel"); got.Code != http.StatusConflict {
		t.Fatalf("cancel of a completed session: %d %s", got.Code, got.Body.String())
	}
	var paidStored model.TopUp
	if err := db.First(&paidStored, paid.Id).Error; err != nil || paidStored.Status != common.TopUpStatusPending {
		t.Fatalf("completed session must stay pending: %q %v", paidStored.Status, err)
	}

	// A late payment on a cancelled order is still credited.
	if _, _, err := model.CompleteProjectTopUp(model.ProjectPaymentReceipt{Reference: order.TradeNo, CustomerID: "cus_cancel", SessionID: "cs_cancel", PaymentIntentID: "pi_late", PaidMicrousd: 20_000_000, Currency: "usd"}); err != nil {
		t.Fatalf("late payment on a cancelled order must credit: %v", err)
	}
}
