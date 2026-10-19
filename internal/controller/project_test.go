package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/stripe/stripe-go/v81/webhook"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"github.com/stripe/stripe-go/v81"
)

func TestProjectDocumentResolverRefreshesStripeURLAndChecksAccess(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	owner := model.User{Username: "document-owner", Email: "owner@example.com"}
	payer := model.User{Username: "document-payer", Email: "payer@example.com"}
	bystander := model.User{Username: "document-bystander"}
	outsider := model.User{Username: "document-outsider"}
	for _, user := range []*model.User{&owner, &payer, &bystander, &outsider} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Updates(map[string]any{"stripe_customer_id": "cus_document", "upgraded_at": time.Now().Unix()}).Error; err != nil {
		t.Fatal(err)
	}
	for _, userID := range []int{payer.Id, bystander.Id} {
		if err := db.Create(&model.ProjectMember{ProjectID: project.ID, UserID: userID, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive}).Error; err != nil {
			t.Fatal(err)
		}
	}
	order := model.TopUp{ProjectID: project.ID, UserId: owner.Id, PaidByUserID: payer.Id, StripeCustomerID: "cus_document", StripePaymentIntentID: "pi_document", StripeInvoiceID: "in_document", StripeInvoicePDF: "https://pay.stripe.com/invoice/old/pdf", StripeInvoiceURL: "https://invoice.stripe.com/i/old", StripeReceiptURL: "https://pay.stripe.com/receipts/old", TradeNo: "ref_document", Amount: 5, PaidAmount: 5, Status: common.TopUpStatusSuccess, InvoiceStatus: "ready"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	currentInvoiceURL := "https://pay.stripe.com/invoice/current/pdf"
	stripeUnavailable := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if stripeUnavailable {
			http.Error(w, "Stripe temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/invoices/in_document":
			fmt.Fprintf(w, `{"id":"in_document","customer":"cus_document","metadata":{"lazu_top_up_reference":"ref_document"},"status":"paid","invoice_pdf":%q,"hosted_invoice_url":"https://invoice.stripe.com/i/current"}`, currentInvoiceURL)
		case "/v1/payment_intents/pi_document":
			fmt.Fprint(w, `{"id":"pi_document","customer":"cus_document","metadata":{"lazu_top_up_reference":"ref_document"},"latest_charge":{"id":"ch_document","customer":"cus_document","payment_intent":"pi_document","receipt_url":"https://pay.stripe.com/receipts/current"}}`)
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
	request := func(userID int, kind string) *httptest.ResponseRecorder {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("id", userID); c.Set("role", common.RoleCommonUser) })
		router.GET("/api/project/:id/topups/:topup_id/document", GetProjectTopUpDocument)
		record := httptest.NewRecorder()
		router.ServeHTTP(record, httptest.NewRequest(http.MethodGet, "/api/project/"+project.PublicID+"/topups/"+order.PublicID+"/document?document="+kind, nil))
		return record
	}
	if got := request(outsider.Id, "invoice"); got.Code != http.StatusNotFound {
		t.Fatalf("outsider document access: %d %s", got.Code, got.Body.String())
	}
	if got := request(bystander.Id, "invoice"); got.Code != http.StatusForbidden {
		t.Fatalf("nonpayer member document access: %d %s", got.Code, got.Body.String())
	}
	if got := request(owner.Id, "invoice"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), currentInvoiceURL) || strings.Contains(got.Body.String(), "/old/") {
		t.Fatalf("fresh invoice: %d %s", got.Code, got.Body.String())
	}
	if got := request(payer.Id, "invoice"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), currentInvoiceURL) {
		t.Fatalf("demoted payer invoice: %d %s", got.Code, got.Body.String())
	}
	if got := request(payer.Id, "receipt"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "receipts/current") {
		t.Fatalf("demoted payer receipt: %d %s", got.Code, got.Body.String())
	}
	if got := request(payer.Id, "invoice_page"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "invoice.stripe.com/i/current") {
		t.Fatalf("hosted invoice page: %d %s", got.Code, got.Body.String())
	}
	for _, enabled := range []string{"false", "true"} {
		t.Setenv("PROJECTS_ENABLED", enabled)
		t.Setenv("PROJECTS_ALLOWED_USERS", fmt.Sprint(outsider.Id))
		for _, userID := range []int{owner.Id, payer.Id} {
			for _, kind := range []string{"invoice", "invoice_page", "receipt"} {
				if got := request(userID, kind); got.Code != http.StatusOK {
					t.Fatalf("document blocked by feature gate: user=%d enabled=%s kind=%s: %d %s", userID, enabled, kind, got.Code, got.Body.String())
				}
			}
		}
		if got := request(bystander.Id, "invoice_page"); got.Code != http.StatusForbidden {
			t.Fatalf("nonpayer member bypassed authorization: %d", got.Code)
		}
	}
	t.Setenv("PROJECTS_ENABLED", "true")
	t.Setenv("PROJECTS_ALLOWED_USERS", "")
	if err := db.Where("project_id = ? AND user_id = ?", project.ID, payer.Id).Delete(&model.ProjectMember{}).Error; err != nil {
		t.Fatal(err)
	}
	if got := request(payer.Id, "invoice"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), currentInvoiceURL) {
		t.Fatalf("historical payer invoice after membership removal: %d %s", got.Code, got.Body.String())
	}
	if got := request(payer.Id, "receipt"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "receipts/current") {
		t.Fatalf("historical payer receipt after membership removal: %d %s", got.Code, got.Body.String())
	}
	if got := request(outsider.Id, "invoice"); got.Code != http.StatusNotFound {
		t.Fatalf("outsider opened payer document after membership removal: %d %s", got.Code, got.Body.String())
	}
	stripeUnavailable = true
	if got := request(owner.Id, "invoice"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "invoice/old/pdf") {
		t.Fatalf("stored invoice unavailable during Stripe outage: %d %s", got.Code, got.Body.String())
	}
	if got := request(payer.Id, "receipt"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "receipts/old") {
		t.Fatalf("stored receipt unavailable during Stripe outage: %d %s", got.Code, got.Body.String())
	}
	if got := request(owner.Id, "invoice_page"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "invoice.stripe.com/i/old") {
		t.Fatalf("stored hosted invoice page during Stripe outage: %d %s", got.Code, got.Body.String())
	}
	stripeUnavailable = false
	currentInvoiceURL = "https://example.com/unsafe"
	if got := request(owner.Id, "invoice"); got.Code != http.StatusNotFound {
		t.Fatalf("unsafe invoice URL: %d %s", got.Code, got.Body.String())
	}
	if err := db.Model(&order).Update("stripe_invoice_pdf", "https://example.com/unsafe").Error; err != nil {
		t.Fatal(err)
	}
	stripeUnavailable = true
	if got := request(owner.Id, "invoice"); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsafe stored invoice URL: %d %s", got.Code, got.Body.String())
	}
	if err := db.Model(&order).Update("stripe_invoice_pdf", "https://pay.stripe.com/invoice/old/pdf").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("project_id = ? AND user_id = ?", project.ID, owner.Id).Delete(&model.ProjectMember{}).Error; err != nil {
		t.Fatal(err)
	}
	stripeUnavailable = false
	currentInvoiceURL = "https://pay.stripe.com/invoice/current/pdf"
	if got := request(owner.Id, "invoice"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "invoice/current/pdf") {
		t.Fatalf("owner with historical missing membership cannot open invoice: %d %s", got.Code, got.Body.String())
	}
	if got := request(outsider.Id, "invoice"); got.Code != http.StatusNotFound {
		t.Fatalf("outsider opened owner document without membership: %d %s", got.Code, got.Body.String())
	}
}

func TestOwnPaymentRecordAllowsOwnerAndFormerPayerOnly(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	owner := model.User{Username: "wallet-owner"}
	payer := model.User{Username: "wallet-payer"}
	other := model.User{Username: "wallet-other"}
	for _, user := range []*model.User{&owner, &payer, &other} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	order := model.TopUp{PublicID: "top_wallet_record", ProjectID: project.ID, UserId: owner.Id, PaidByUserID: payer.Id,
		StripeInvoiceID: "in_wallet", StripeInvoicePDF: "https://pay.stripe.com/invoice/private/pdf",
		StripePaymentIntentID: "pi_wallet", StripeReceiptURL: "https://pay.stripe.com/receipts/private",
		TradeNo: "ref_wallet_record", Amount: 5, PaidAmount: 5, Status: common.TopUpStatusSuccess, InvoiceStatus: "ready"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	request := func(userID int) *httptest.ResponseRecorder {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("id", userID) })
		router.GET("/api/user/payments/:topup_id", GetOwnPaymentRecord)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/user/payments/"+order.PublicID, nil))
		return response
	}
	if got := request(owner.Id); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), project.PublicID) || !strings.Contains(got.Body.String(), `"project_shared":false`) || !strings.Contains(got.Body.String(), `"project_name":""`) {
		t.Fatalf("owner record: %d %s", got.Code, got.Body.String())
	}
	if got := request(payer.Id); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"receipt_ready":true`) || strings.Contains(got.Body.String(), "pay.stripe.com") {
		t.Fatalf("former payer record: %d %s", got.Code, got.Body.String())
	}
	if got := request(other.Id); got.Code != http.StatusNotFound || strings.Contains(got.Body.String(), order.PublicID) {
		t.Fatalf("unrelated account record: %d %s", got.Code, got.Body.String())
	}
}

func TestPersonalDocumentResolverWorksWithProjectsDisabled(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	owner := model.User{Username: "personal-document-owner", Email: "owner@example.com"}
	other := model.User{Username: "personal-document-other"}
	for _, user := range []*model.User{&owner, &other} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Update("stripe_customer_id", "cus_personal").Error; err != nil {
		t.Fatal(err)
	}
	order := model.TopUp{ProjectID: project.ID, UserId: owner.Id, PaidByUserID: owner.Id, StripeCustomerID: "cus_personal", StripeInvoiceID: "in_personal", TradeNo: "ref_personal", Amount: 5, PaidAmount: 5, Status: common.TopUpStatusSuccess, InvoiceStatus: "ready"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"in_personal","customer":"cus_personal","metadata":{"lazu_top_up_reference":"ref_personal"},"status":"paid","invoice_pdf":"https://pay.stripe.com/invoice/personal/pdf"}`)
	}))
	defer server.Close()
	oldBackend, oldKey := stripe.GetBackend(stripe.APIBackend), setting.StripeApiSecret
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client()}))
	setting.StripeApiSecret = "sk_test"
	t.Cleanup(func() {
		stripe.SetBackend(stripe.APIBackend, oldBackend)
		setting.StripeApiSecret = oldKey
	})
	t.Setenv("PROJECTS_ENABLED", "false")
	t.Setenv("PROJECTS_ALLOWED_USERS", "")
	request := func(userID int, query string) *httptest.ResponseRecorder {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("id", userID) })
		router.GET("/api/project/:id/topups/:topup_id/document", GetProjectTopUpDocument)
		result := httptest.NewRecorder()
		router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/api/project/"+project.PublicID+"/topups/"+order.PublicID+"/document?document=invoice"+query, nil))
		return result
	}
	if got := request(owner.Id, ""); got.Code != http.StatusOK {
		t.Fatalf("personal owner cannot open invoice with projects disabled: %d %s", got.Code, got.Body.String())
	}
	if got := request(other.Id, ""); got.Code != http.StatusNotFound {
		t.Fatalf("other user opened invoice: %d %s", got.Code, got.Body.String())
	}
	if got := request(owner.Id, "&project_id=prj_other"); got.Code != http.StatusBadRequest {
		t.Fatalf("mismatched project selector: %d %s", got.Code, got.Body.String())
	}
}

func TestCheckoutFirstEmailIncludesCardMethod(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	var scheduled []int64
	scheduleProjectNotificationWake = func(at int64) { scheduled = append(scheduled, at) }
	oldWake := wakeProjectNotifications
	wakeProjectNotifications = func() {}
	t.Cleanup(func() { wakeProjectNotifications = oldWake })

	owner := model.User{Username: "card-payer", Email: "card@example.com"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Update("stripe_customer_id", "cus_card").Error; err != nil {
		t.Fatal(err)
	}
	order, err := model.BeginProjectTopUp(project.ID, owner.Id, "checkout-first", 5, model.ProjectCheckoutConfig{ReturnBase: "https://lazu.ai"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/payment_intents/pi_card" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"id":"pi_card","customer":"cus_card","metadata":{"lazu_top_up_reference":%q},"latest_charge":{"id":"ch_card","customer":"cus_card","payment_intent":"pi_card","receipt_url":"https://pay.stripe.com/receipts/card","payment_method_details":{"type":"card","card":{"brand":"visa","last4":"4242"}}}}`, order.TradeNo)
	}))
	defer server.Close()
	oldBackend, oldKey := stripe.GetBackend(stripe.APIBackend), setting.StripeApiSecret
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client()}))
	setting.StripeApiSecret = "sk_test"
	t.Cleanup(func() {
		stripe.SetBackend(stripe.APIBackend, oldBackend)
		setting.StripeApiSecret = oldKey
	})
	var session stripe.CheckoutSession
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"id":"cs_card","client_reference_id":%q,"customer":"cus_card","payment_intent":"pi_card","amount_total":500,"currency":"usd"}`, order.TradeNo)), &session); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/stripe/webhook", nil)
	completeProjectStripeSession(c, session)
	if recorder.Code != http.StatusOK {
		t.Fatalf("checkout completion: %d", recorder.Code)
	}
	if err := db.First(&order, order.Id).Error; err != nil || order.PaymentMethod != "Visa ···· 4242" || order.StripeReceiptURL == "" {
		t.Fatalf("checkout-first method: %v %+v", err, order)
	}
	var notice model.ProjectNotification
	if err := db.Where("event_key = ?", fmt.Sprintf("payment:%s:%d", order.PublicID, owner.Id)).First(&notice).Error; err != nil {
		t.Fatal(err)
	}
	email, err := notice.Email()
	if err != nil || !strings.Contains(email.HTML, "Visa ···· 4242") {
		t.Fatalf("checkout-first email method: %v", err)
	}
	if len(scheduled) != 1 || scheduled[0] != notice.AvailableAt || notice.AvailableAt < order.CompleteTime+60 {
		t.Fatalf("fallback wake does not match durable deadline: scheduled=%v available=%d credited=%d", scheduled, notice.AvailableAt, order.CompleteTime)
	}
	completeProjectStripeSession(c, session)
	if len(scheduled) != 1 {
		t.Fatalf("duplicate checkout scheduled another wake: %v", scheduled)
	}

}

func TestProjectAPIRolesFeatureGateAndVersionFence(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.ProjectInvite{}, &model.Log{}); err != nil {
		t.Fatal(err)
	}
	oldLogDB := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLogDB) })
	users := []model.User{
		{Id: 11, Username: "owner", Quota: 500, Role: common.RoleCommonUser},
		{Id: 12, Username: "admin", Role: common.RoleRootUser},
		{Id: 13, Username: "member", Role: common.RoleRootUser},
		{Id: 14, Username: "outsider", Role: common.RoleRootUser},
	}
	for i := range users {
		if err := db.Create(&users[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", 11).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	for _, m := range []model.ProjectMember{
		{ProjectID: project.ID, UserID: 12, Role: model.ProjectRoleAdmin, Status: model.ProjectMemberActive},
		{ProjectID: project.ID, UserID: 13, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive},
	} {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := func(userID int, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("id", userID); c.Set("role", common.RoleRootUser) })
		routes := router.Group("/api/project/:id", middleware.ProjectConsoleContext())
		routes.GET("", GetProject)
		routes.PUT("", UpdateProject)
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	path := "/api/project/" + project.PublicID
	t.Setenv("PROJECTS_ENABLED", "false")
	t.Setenv("PROJECTS_ALLOWED_USERS", "")
	if got := request(11, "GET", path, ""); got.Code != http.StatusForbidden {
		t.Fatalf("feature disabled: %d %s", got.Code, got.Body.String())
	}
	t.Setenv("PROJECTS_ENABLED", "true")
	if got := request(14, "GET", path, ""); got.Code != http.StatusNotFound {
		t.Fatalf("platform role crossed project: %d %s", got.Code, got.Body.String())
	}
	for _, actor := range []int{12, 13} {
		if got := request(actor, "PUT", path, `{"name":"stolen","expected_version":1}`); got.Code != http.StatusForbidden {
			t.Fatalf("non-owner edited settings: %d %s", got.Code, got.Body.String())
		}
	}
	for _, actor := range []int{11, 12, 13} {
		got := request(actor, "GET", path, "")
		if got.Code != http.StatusOK {
			t.Fatalf("read: %d %s", got.Code, got.Body.String())
		}
		var body struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(got.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		_, hasSettings := body.Data["settings"]
		if hasSettings != (actor != 13) {
			t.Fatalf("member settings visibility: %s", got.Body.String())
		}
	}
	if got := request(11, "PUT", path, `{"name":"renamed","expected_version":1}`); got.Code != 200 {
		t.Fatalf("update: %d %s", got.Code, got.Body.String())
	}
	if got := request(11, "PUT", path, `{"name":"stale","expected_version":1}`); got.Code != 409 {
		t.Fatalf("lost update: %d %s", got.Code, got.Body.String())
	}
	if got := request(11, "GET", path+"?project_id=another", ""); got.Code != 400 {
		t.Fatalf("mismatched project: %d", got.Code)
	}
	if err := db.Model(&model.ProjectMember{}).Where("project_id = ? AND user_id = ?", project.ID, 12).Update("status", model.ProjectMemberSuspended).Error; err != nil {
		t.Fatal(err)
	}
	got := request(12, "GET", path, "")
	if got.Code != 200 || strings.Contains(got.Body.String(), `"allocation"`) {
		t.Fatalf("suspended full view: %d %s", got.Code, got.Body.String())
	}
	projects, defaultID, err := model.ListUserProjects(12)
	if err != nil || len(projects) != 2 || defaultID == project.PublicID {
		t.Fatalf("owner's joined-project switcher: %v %s %v", projects, defaultID, err)
	}
}

func TestProjectAttentionAggregatesBeforePagingAndRejectsNonManagers(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.ProjectInvite{}, &model.BudgetRequest{}, &model.Log{}); err != nil {
		t.Fatal(err)
	}
	oldLogDB := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLogDB) })
	t.Setenv("PROJECTS_ENABLED", "true")
	owner := model.User{Id: 21, Username: "attention-owner"}
	member := model.User{Id: 22, Username: "attention-member"}
	outsider := model.User{Id: 23, Username: "attention-outsider"}
	for _, user := range []*model.User{&owner, &member, &outsider} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project, other model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("owner_user_id = ?", outsider.Id).First(&other).Error; err != nil {
		t.Fatal(err)
	}
	membership := model.ProjectMember{ProjectID: project.ID, UserID: member.Id, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}
	token := model.Token{Id: 24, UserId: member.Id, ProjectID: project.ID, Key: "attention-token", Name: "Personal"}
	ownerKey := model.Token{Id: 25, UserId: owner.Id, ProjectID: project.ID, Key: "attention-owner-key", Name: "Owner"}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ownerKey).Error; err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Unix()
	for i := 0; i < 60; i++ {
		actor, key, reason := member.Id, token.Id, "key_budget_exceeded"
		if i >= 15 && i < 30 {
			reason = "member_budget_exceeded"
		}
		if i >= 30 && i < 45 {
			actor, key, reason = owner.Id, ownerKey.Id, "project_budget_exceeded"
		}
		if i >= 45 {
			reason = "platform_budget_exceeded"
		}
		if err := db.Create(&model.Log{ProjectID: project.ID, PayerUserID: owner.Id, UserId: actor, TokenId: key, Type: model.LogTypeError, RejectionReason: reason, CreatedAt: created}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.Log{ProjectID: other.ID, UserId: member.Id, TokenId: token.Id, Type: model.LogTypeError, CreatedAt: created}).Error; err != nil {
		t.Fatal(err)
	}
	request := func(actor int, page string) *httptest.ResponseRecorder {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("id", actor); c.Set("role", common.RoleRootUser) })
		router.GET("/api/project/:id/attention", middleware.ProjectConsoleContext(), GetProjectAttention)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/project/"+project.PublicID+"/attention?page="+page+"&page_size=1", nil))
		return response
	}
	seen := map[string]bool{}
	for _, page := range []string{"1", "2", "3", "4"} {
		response := request(owner.Id, page)
		if response.Code != 200 {
			t.Fatalf("attention status=%d %s", response.Code, response.Body.String())
		}
		var body struct {
			Data struct {
				Rejected   int64 `json:"rejected_count_today"`
				Rejections struct {
					Items []projectAttentionRow `json:"items"`
					Total int64                 `json:"total"`
				} `json:"rejections"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Data.Rejected != 60 || body.Data.Rejections.Total != 4 || len(body.Data.Rejections.Items) != 1 || body.Data.Rejections.Items[0].Count != 15 {
			t.Fatalf("page totals: %s", response.Body.String())
		}
		row := body.Data.Rejections.Items[0]
		if seen[row.Reason] {
			t.Fatal("unstable group pagination")
		}
		seen[row.Reason] = true
		if row.Reason == "project_budget_exceeded" && (row.Actor == nil || row.Token == nil || row.Token.Name != "Owner") {
			t.Fatalf("rejection actor was reattributed: %+v", row)
		}
	}
	if !seen["platform_budget_exceeded"] {
		t.Fatal("attention lost platform budget reason")
	}
	if got := request(member.Id, "1"); got.Code != 403 {
		t.Fatalf("member read full-project attention: %d", got.Code)
	}
	if err := db.Model(&model.ProjectMember{}).Where("project_id = ? AND user_id = ?", project.ID, owner.Id).Update("status", model.ProjectMemberSuspended).Error; err != nil {
		t.Fatal(err)
	}
	if got := request(owner.Id, "1"); got.Code != 403 {
		t.Fatalf("suspended manager read full-project attention: %d", got.Code)
	}
}

func TestProjectCheckoutOwnerAndAdminBillingBoundaries(t *testing.T) {
	project := model.Project{ID: 1, PublicID: "prj_checkout", OwnerUserID: 11, StripeCustomerID: "cus_project"}
	personal := false
	order := model.TopUp{StripeProjectName: "Original project", StripeProjectShared: &personal, PublicID: "top_checkout", TradeNo: "ref_checkout", PaidByUserID: 12, Amount: 25, StripePriceID: "price_original", StripeReturnBase: "https://example.com", StripePromotionCodes: true}
	admin := projectCheckoutParams(project, order, "cus_project", "")
	if *admin.Customer != "cus_project" || *admin.CustomerUpdate.Name != "never" || *admin.CustomerUpdate.Address != "never" || admin.TaxIDCollection != nil {
		t.Fatal("admin checkout can overwrite saved project billing details")
	}
	if admin.InvoiceCreation == nil || !*admin.InvoiceCreation.Enabled || admin.InvoiceCreation.InvoiceData.Metadata["lazu_top_up_reference"] != "ref_checkout" {
		t.Fatal("invoice cannot be correlated independently of checkout callback")
	}
	if admin.PaymentIntentData.Metadata["lazu_top_up_reference"] != "ref_checkout" {
		t.Fatal("receipt cannot be correlated before payment callback")
	}
	if admin.PaymentMethodTypes != nil {
		t.Fatal("checkout overrides the payment methods enabled in Stripe")
	}
	if got := *admin.InvoiceCreation.InvoiceData.Description; got != "Lazu API credit" {
		t.Fatalf("personal invoice exposes the default project name: %s", got)
	}
	if got := *admin.SuccessURL; got != "https://example.com/payment/success?project_id=prj_checkout&top_up_id=top_checkout" {
		t.Fatalf("wrong checkout return URL: %s", got)
	}
	if got := *admin.CancelURL; got != "https://example.com/console/wallet?project_id=prj_checkout" {
		t.Fatalf("cancelled checkout should return to wallet: %s", got)
	}
	if *admin.LineItems[0].Price != "price_original" || *admin.LineItems[0].Quantity != 25 || admin.IdempotencyKey == nil {
		t.Fatal("checkout retry lost original order terms")
	}
	project.Name = "Renamed after timeout"
	if replay := projectCheckoutParams(project, order, "cus_project", ""); *replay.InvoiceCreation.InvoiceData.Description != *admin.InvoiceCreation.InvoiceData.Description {
		t.Fatal("rename changed Stripe idempotency parameters")
	}
	at := time.Now().Unix()
	project.UpgradedAt = &at
	if replay := projectCheckoutParams(project, order, "cus_project", ""); *replay.InvoiceCreation.InvoiceData.Description != *admin.InvoiceCreation.InvoiceData.Description {
		t.Fatal("project upgrade changed Stripe idempotency parameters")
	}
	order.PaidByUserID = 11
	shared := true
	order.StripeProjectShared = &shared
	owner := projectCheckoutParams(project, order, "cus_project", "")
	if *owner.CustomerUpdate.Name != "auto" || *owner.CustomerUpdate.Address != "auto" || owner.TaxIDCollection == nil || !*owner.TaxIDCollection.Enabled {
		t.Fatal("owner cannot set project billing details")
	}
	if got := *owner.InvoiceCreation.InvoiceData.Description; got != "Lazu API credit for Original project" {
		t.Fatalf("shared project invoice lost the project name: %s", got)
	}
}

func TestProjectPaymentReturnBaseUsesConfiguredConsole(t *testing.T) {
	previous := setting.StripeReturnBase
	setting.StripeReturnBase = "https://lazu.ai"
	t.Cleanup(func() { setting.StripeReturnBase = previous })
	t.Setenv("FRONTEND_BASE_URL", "")
	if got := projectPaymentReturnBase(); got != "https://lazu.ai" {
		t.Fatalf("checkout return base = %q", got)
	}
	t.Setenv("FRONTEND_BASE_URL", "https://other.example/")
	if got := projectPaymentReturnBase(); got != "https://other.example" {
		t.Fatalf("frontend override = %q", got)
	}
}

func TestProjectStripeSignedWebhooksPreservePaymentAndInvoiceOrder(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	// This test replays webhooks and asserts the exact credit; an extension
	// that adds to a top-up switches itself off here.
	for _, setup := range exactCreditTestSetups {
		setup(t)
	}
	if err := db.AutoMigrate(&model.Log{}, &model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })
	oldSecret := setting.StripeWebhookSecret
	setting.StripeWebhookSecret = "whsec_project_fixture" // secret-scan: allow -- deterministic local webhook test fixture
	t.Cleanup(func() { setting.StripeWebhookSecret = oldSecret })
	oldWake := wakeProjectNotifications
	wakeProjectNotifications = func() {}
	t.Cleanup(func() { wakeProjectNotifications = oldWake })
	owner := model.User{Username: "stripe-http-owner", Email: "owner@example.com"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	// The Stripe customer lives on the project (schema v15 dropped users.stripe_customer).
	if err := db.Model(&project).Update("stripe_customer_id", "cus_project_http").Error; err != nil {
		t.Fatal(err)
	}
	order, err := model.BeginProjectTopUp(project.ID, owner.Id, "http-payment", 10)
	if err != nil {
		t.Fatal(err)
	}
	send := func(kind string, object gin.H) int {
		t.Helper()
		payload, err := json.Marshal(gin.H{"id": "evt_project_fixture", "object": "event", "type": kind, "data": gin.H{"object": object}})
		if err != nil {
			t.Fatal(err)
		}
		signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: setting.StripeWebhookSecret, Timestamp: time.Now()})
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest("POST", "/api/user/stripe/webhook", bytes.NewReader(signed.Payload))
		c.Request.Header.Set("Stripe-Signature", signed.Header)
		StripeWebhook(c)
		return response.Code
	}
	checkout := gin.H{"id": "cs_http", "client_reference_id": order.TradeNo, "customer": "cus_project_http", "status": "complete", "payment_status": "unpaid", "amount_total": 1000, "currency": "usd", "payment_intent": "pi_http"}
	if code := send("checkout.session.completed", checkout); code != 200 {
		t.Fatalf("unpaid callback %d", code)
	}
	var saved model.User
	db.First(&saved, owner.Id)
	if saved.Quota != 0 {
		t.Fatal("unpaid checkout credited wallet")
	}
	invoice := gin.H{"id": "in_http", "customer": "cus_project_http", "number": "INV-HTTP", "status": "open", "hosted_invoice_url": "https://invoice.stripe.com/i/http", "invoice_pdf": "https://pay.stripe.com/invoice/http/pdf", "customer_name": "Project Company", "metadata": gin.H{"lazu_top_up_reference": order.TradeNo}}
	if code := send("invoice.finalized", invoice); code != 200 {
		t.Fatalf("invoice-first %d", code)
	}
	if err := db.First(&order, order.Id).Error; err != nil || order.InvoiceStatus != "pending" {
		t.Fatalf("finalized but unpaid invoice became ready: %v %+v", err, order)
	}
	charge := gin.H{"id": "ch_http", "customer": "cus_project_http", "payment_intent": "pi_http", "receipt_url": "https://pay.stripe.com/receipts/http", "payment_method_details": gin.H{"type": "alipay"}, "metadata": gin.H{"lazu_top_up_reference": order.TradeNo}}
	if code := send("charge.succeeded", charge); code != 200 {
		t.Fatalf("receipt-first %d", code)
	}
	checkout["payment_status"] = "paid"
	for i := 0; i < 2; i++ {
		if code := send("checkout.session.completed", checkout); code != 200 {
			t.Fatalf("paid callback %d", code)
		}
	}
	invoice["status"] = "paid"
	if code := send("invoice.paid", invoice); code != 200 {
		t.Fatalf("paid invoice callback %d", code)
	}
	if code := send("checkout.session.expired", gin.H{"client_reference_id": order.TradeNo, "status": "expired"}); code != 200 {
		t.Fatalf("late expiry %d", code)
	}
	if err := db.First(&saved, owner.Id).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Quota != 10_000_000 {
		t.Fatalf("replayed credit %d", saved.Quota)
	}
	if err := db.First(&order, order.Id).Error; err != nil {
		t.Fatal(err)
	}
	if order.InvoiceStatus != "ready" || order.Status != common.TopUpStatusSuccess || order.StripeReceiptURL != "https://pay.stripe.com/receipts/http" || order.PaymentMethod != "alipay" {
		t.Fatalf("lost independent state %+v", order)
	}
	var paymentNotices []model.ProjectNotification
	if err := db.Where("event_key LIKE ?", "payment:"+order.PublicID+":%").Find(&paymentNotices).Error; err != nil || len(paymentNotices) != 1 {
		t.Fatalf("expected one payment email after both links arrived, count=%d err=%v", len(paymentNotices), err)
	}
	if email, err := paymentNotices[0].Email(); err != nil || !strings.Contains(email.HTML, "document=invoice_page") || paymentNotices[0].AvailableAt > time.Now().Unix() {
		t.Fatalf("payment email lacks the invoice once documents arrived: %v", err)
	}
	charge["amount"] = 1000
	charge["currency"] = "usd"
	charge["amount_refunded"] = 500
	for i := 0; i < 2; i++ {
		if code := send("charge.refunded", charge); code != 200 {
			t.Fatalf("refund callback %d", code)
		}
	}
	if err := db.First(&saved, owner.Id).Error; err != nil || saved.Quota != 5_000_000 {
		t.Fatalf("duplicate refund changed quota: %d %v", saved.Quota, err)
	}
	if err := db.Migrator().RenameTable("top_ups", "hidden_top_ups"); err != nil {
		t.Fatal(err)
	}
	if code := send("checkout.session.completed", checkout); code != 503 {
		t.Fatalf("database failure was acknowledged: %d", code)
	}
}

func TestProjectBillingPortalCannotManagePaymentsOrSubscriptions(t *testing.T) {
	params := projectBillingPortalConfiguration()
	if params.Features == nil || !*params.Features.CustomerUpdate.Enabled || !*params.Features.InvoiceHistory.Enabled {
		t.Fatal("missing billing features")
	}
	if *params.Features.PaymentMethodUpdate.Enabled || *params.Features.SubscriptionUpdate.Enabled || *params.Features.SubscriptionCancel.Enabled || *params.LoginPage.Enabled {
		t.Fatal("portal exposed unrelated payment permissions")
	}
	allowed := params.Features.CustomerUpdate.AllowedUpdates
	if len(allowed) != 3 || *allowed[0] != "name" || *allowed[1] != "address" || *allowed[2] != "tax_id" {
		t.Fatal("unexpected customer update permissions")
	}
}

func TestProjectPersonalKeyCreationAcceptsProjectClientShape(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.TokenAuditLog{}, &model.SpendBudgetAlert{}, &model.Log{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })
	t.Setenv("PROJECTS_ENABLED", "true")
	user := model.User{Username: "key-client", Quota: 50000000}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	db.Where("owner_user_id = ?", user.Id).First(&project)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", user.Id) })
	router.POST("/api/token/", AddToken)
	raw, _ := json.Marshal(gin.H{"project_id": project.PublicID, "name": "cli", "model_limits": []string{"gpt-4o"}, "cap_microusd": 1000000})
	request := httptest.NewRequest("POST", "/api/token/", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	var token model.Token
	if err := db.Where("project_id = ?", project.ID).First(&token).Error; err != nil {
		t.Fatal(err)
	}
	if token.ExpiredTime != -1 || !token.UnlimitedQuota || !token.ModelLimitsEnabled || token.ModelLimits != "gpt-4o" {
		t.Fatalf("wrong default/limits: %+v", token)
	}
	var budget model.SpendBudget
	if err := db.Where("project_id = ? AND token_id = ?", project.ID, token.Id).First(&budget).Error; err != nil {
		t.Fatal(err)
	}
	if budget.LimitMicrousd != 1000000 || !budget.Enabled {
		t.Fatal("creation lost monthly cap")
	}
	var body struct {
		Data struct {
			Token map[string]json.RawMessage `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Token) == 0 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing metadata or secret cache protection")
	}
	if err := db.Model(&token).Updates(map[string]interface{}{"rate_limits_enabled": true, "rpm_limit": 7, "allow_ips": "127.0.0.1", "ip_whitelist_enabled": true}).Error; err != nil {
		t.Fatal(err)
	}
	router.PUT("/api/token/", UpdateToken)
	raw, _ = json.Marshal(gin.H{"id": token.PublicID, "project_id": project.PublicID, "expected_version": token.ProjectVersion, "name": "renamed", "model_limits": []string{"gpt-4o-mini"}, "cap_microusd": 2000000})
	request = httptest.NewRequest("PUT", "/api/token/?policy_only=true", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("edit: %d %s", response.Code, response.Body.String())
	}
	// Replaying the same draft must conflict rather than overwrite a newer edit.
	request = httptest.NewRequest("PUT", "/api/token/?policy_only=true", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	stale := httptest.NewRecorder()
	router.ServeHTTP(stale, request)
	if !strings.Contains(stale.Body.String(), `"code":"version_conflict"`) {
		t.Fatalf("stale edit accepted: %s", stale.Body.String())
	}
	if err := db.First(&token, token.Id).Error; err != nil {
		t.Fatal(err)
	}
	if token.Name != "renamed" || token.ModelLimits != "gpt-4o-mini" || !token.ModelLimitsEnabled || token.ExpiredTime != -1 || !token.UnlimitedQuota || !token.RateLimitsEnabled || token.RPMLimit != 7 || token.AllowIps == nil || *token.AllowIps != "127.0.0.1" || token.IPWhitelistEnabled == nil || !*token.IPWhitelistEnabled {
		t.Fatalf("policy edit changed unrelated fields: %+v", token)
	}
	if err := db.First(&budget, budget.ID).Error; err != nil {
		t.Fatal(err)
	}
	if budget.LimitMicrousd != 2000000 {
		t.Fatal("monthly cap was not updated")
	}

	// Team creation uses the personal form policies with a separate monthly cap.
	expiry := time.Now().Add(30 * 24 * time.Hour).Unix()
	raw, _ = json.Marshal(gin.H{"project_id": project.PublicID, "name": "shared-form", "model_limits": []string{"gpt-4o"}, "model_limits_enabled": true, "cap_microusd": 20000000, "unlimited_quota": true, "remain_quota": 0, "expired_time": expiry, "rate_limits_enabled": true, "rpm_limit": 7, "ip_whitelist_enabled": true, "allow_ips": "127.0.0.1", "note": "team creation"})
	request = httptest.NewRequest("POST", "/api/token/", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("shared form create: %s", response.Body.String())
	}
	var advanced model.Token
	if err := db.Where("name = ?", "shared-form").First(&advanced).Error; err != nil {
		t.Fatal(err)
	}
	if advanced.ProjectID != project.ID || advanced.UserId != user.Id || advanced.ExpiredTime != expiry || !advanced.UnlimitedQuota || advanced.RemainQuota != 0 || !advanced.RateLimitsEnabled || advanced.RPMLimit != 7 || advanced.AllowIps == nil || *advanced.AllowIps != "127.0.0.1" || advanced.Note != "team creation" {
		t.Fatalf("shared form lost policy or scope: %+v", advanced)
	}
	var monthly model.SpendBudget
	if err := db.Where("project_id = ? AND token_id = ?", project.ID, advanced.Id).First(&monthly).Error; err != nil {
		t.Fatal(err)
	}
	if monthly.LimitMicrousd != 20000000 {
		t.Fatal("shared form lost monthly cap")
	}

	// The full team editor must retain the separate monthly cap while updating advanced policies.
	raw, _ = json.Marshal(gin.H{"id": advanced.PublicID, "project_id": project.PublicID, "expected_version": advanced.ProjectVersion, "name": "edited-full", "model_limits": []string{}, "model_limits_enabled": true, "model_scope_vendors": []int{17}, "cap_microusd": 30000000, "period_cap": nil, "unlimited_quota": true, "expired_time": expiry + 86400, "rate_limits_enabled": true, "rpm_limit": 12, "usage_alert_percent": 90, "ip_whitelist_enabled": true, "allow_ips": "10.0.0.1", "search_limits_enabled": true, "search_providers": []string{"tavily"}, "note": "edited policy"})
	request = httptest.NewRequest("PUT", "/api/token/", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("full edit: %s", response.Body.String())
	}
	if err := db.First(&advanced, advanced.Id).Error; err != nil {
		t.Fatal(err)
	}
	if advanced.ExpiredTime != expiry+86400 || advanced.RPMLimit != 12 || advanced.UsageAlertPercent != 90 || advanced.Note != "edited policy" || !advanced.ModelLimitsEnabled || len(advanced.ModelScopeVendors) != 1 || !advanced.SearchLimitsEnabled || advanced.AllowIps == nil || *advanced.AllowIps != "10.0.0.1" {
		t.Fatalf("full edit lost policy: %+v", advanced)
	}
	if err := db.First(&monthly, monthly.ID).Error; err != nil {
		t.Fatal("full editor deleted monthly cap:", err)
	}
	if monthly.LimitMicrousd != 30000000 {
		t.Fatal("full editor did not update monthly cap")
	}

	for _, enabled := range []bool{true, false} {
		name := "vendor-scope-off"
		if enabled {
			name = "vendor-scope-on"
		}
		raw, _ = json.Marshal(gin.H{"project_id": project.PublicID, "name": name, "model_limits": []string{}, "model_limits_enabled": enabled, "model_scope_vendors": []int{17}})
		request = httptest.NewRequest("POST", "/api/token/", bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		response = httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"success":true`) {
			t.Fatalf("vendor-only scope create: %s", response.Body.String())
		}
		var scoped model.Token
		if err := db.Where("name = ?", name).First(&scoped).Error; err != nil {
			t.Fatal(err)
		}
		if scoped.ModelLimitsEnabled != enabled || scoped.ModelLimits != "" || len(scoped.ModelScopeVendors) != 1 || scoped.ModelScopeVendors[0] != 17 {
			t.Fatalf("vendor-only scope changed: %+v", scoped)
		}
		if enabled {
			raw, _ = json.Marshal(gin.H{"id": scoped.PublicID, "project_id": project.PublicID, "expected_version": scoped.ProjectVersion, "name": "vendor-renamed", "model_limits": []string{}, "cap_microusd": nil})
			request = httptest.NewRequest("PUT", "/api/token/?policy_only=true", bytes.NewReader(raw))
			request.Header.Set("Content-Type", "application/json")
			response = httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if !strings.Contains(response.Body.String(), `"success":true`) {
				t.Fatalf("vendor-only scope edit: %s", response.Body.String())
			}
			if err := db.First(&scoped, scoped.Id).Error; err != nil {
				t.Fatal(err)
			}
			if !scoped.ModelLimitsEnabled || len(scoped.ModelScopeVendors) != 1 {
				t.Fatal("project policy edit cleared vendor scope")
			}
		}
	}

}

func TestProjectUsageSummaryIsScopedToOriginatingKey(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })
	for _, row := range []model.Log{
		{ProjectID: 10, UserId: 3, TokenId: 30, Type: model.LogTypeConsume, AmountMicrousd: 7, CreatedAt: 100},
		{ProjectID: 10, UserId: 3, TokenId: 31, Type: model.LogTypeConsume, AmountMicrousd: 100, CreatedAt: 100},
		{ProjectID: 20, UserId: 3, TokenId: 30, Type: model.LogTypeConsume, AmountMicrousd: 200, CreatedAt: 100},
		{ProjectID: 10, UserId: 2, TokenId: 32, Type: model.LogTypeConsume, AmountMicrousd: 9, CreatedAt: 100},
		{ProjectID: 20, UserId: 2, TokenId: 33, Type: model.LogTypeConsume, AmountMicrousd: 300, CreatedAt: 100},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, fixture := range []struct{ actor, token, want int }{{3, 30, 7}, {2, 32, 9}} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("GET", "/api/usage/summary?since=0&until=200&granularity=hour", nil)
		c.Set("id", fixture.actor)
		c.Set("token_id", fixture.token)
		c.Set("project_id", 10)
		c.Set("payer_user_id", 1)
		GetUsageSummary(c)
		var response struct {
			TotalMicrousd int               `json:"total_microusd"`
			TotalRequests int               `json:"total_requests"`
			Series        []json.RawMessage `json:"series"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != 200 || response.TotalMicrousd != fixture.want || response.TotalRequests != 1 || len(response.Series) != 1 {
			t.Fatalf("wrong scope actor=%d response=%d %s", fixture.actor, recorder.Code, recorder.Body.String())
		}
	}
}

func TestProjectReviewLegacyTopupSurvivesRollout(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROJECTS_ENABLED", "true")
	oldKey, oldPrice := setting.StripeApiSecret, setting.StripePriceId
	setting.StripeApiSecret = "disabled-for-test"
	setting.StripePriceId = "price_test"
	t.Cleanup(func() { setting.StripeApiSecret = oldKey; setting.StripePriceId = oldPrice })
	user := model.User{Username: "legacy-topup-rollout"}
	db.Create(&user)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("id", user.Id)
	c.Request = httptest.NewRequest("POST", "/api/user/stripe/pay", nil)
	requestProjectStripePay(c, &StripePayRequest{Amount: 10})
	var count int64
	db.Model(&model.TopUp{}).Where("user_id = ?", user.Id).Count(&count)
	if count != 1 {
		t.Fatal("legacy rollout topup rejected before order creation")
	}
}

func TestProjectReviewHistoricalWalletUsesPaymentCompletionRange(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	t.Setenv("PROJECTS_ENABLED", "true")
	if err := db.AutoMigrate(&model.TopUp{}, &model.Log{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })
	owner := model.User{Username: "historical-wallet", Quota: 100000000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	from, to := time.Now().Add(-30*24*time.Hour).Unix(), time.Now().Add(-24*time.Hour).Unix()
	for i, at := range []int64{from - 1, from, from + 1, to - 1, to, to + 1} {
		if err := db.Create(&model.TopUp{UserId: owner.Id, ProjectID: project.ID, TradeNo: fmt.Sprintf("period-order-%d", i), Status: common.TopUpStatusSuccess, CreateTime: from - 100, CompleteTime: at, CreditedMicrousd: 23000000}).Error; err != nil {
			t.Fatal(err)
		}
		// Writers store UTC instants; SQLite compares their timestamp strings.
		if err := db.Create(&model.ProjectSpend{ProjectID: project.ID, PayerUserID: owner.Id, ActorUserID: owner.Id, RequestID: fmt.Sprintf("period-spend-%d", i), AmountMicrousd: 12000000, EventKey: fmt.Sprintf("period-%d", i), OccurredAt: time.Unix(at, 0).UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", owner.Id) })
	router.GET("/api/project/:id/wallet", middleware.ProjectConsoleContext(), GetProjectWallet)
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/project/%s/wallet?start_timestamp=%d&end_timestamp=%d", project.PublicID, from, to), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	// Only from, from+1 and to-1 belong to the half-open historical window.
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"period_spend_microusd":36000000`) || !strings.Contains(rec.Body.String(), `"period_topup_microusd":69000000`) {
		t.Fatalf("wrong historical window: %s", rec.Body.String())
	}
}

func TestProjectReviewWalletLedgerUsesPayerAndRejectsSuspendedAdmin(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	t.Setenv("PROJECTS_ENABLED", "true")
	if err := db.AutoMigrate(&model.WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner, admin := model.User{Username: "ledger-project-owner"}, model.User{Username: "ledger-project-admin"}
	db.Create(&owner)
	db.Create(&admin)
	var project model.Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	membership := model.ProjectMember{ProjectID: project.ID, UserID: admin.Id, Role: model.ProjectRoleAdmin, Status: model.ProjectMemberActive}
	db.Create(&membership)
	at := time.Now().UTC().Add(-time.Hour)
	db.Create(&model.WalletLedger{UserID: owner.Id, DeltaMicrousd: 2000000, Kind: model.WalletLedgerKindTopup, CreatedAt: at})
	db.Create(&model.WalletLedger{UserID: admin.Id, DeltaMicrousd: 9000000, Kind: model.WalletLedgerKindTopup, CreatedAt: at})
	for _, suspended := range []bool{false, true} {
		if suspended {
			db.Model(&membership).Update("status", model.ProjectMemberSuspended)
		}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Set("id", admin.Id)
		c.Request = httptest.NewRequest("GET", fmt.Sprintf("/?project_id=%s&start_timestamp=%d&end_timestamp=%d&timezone=UTC", project.PublicID, at.Add(-time.Minute).Unix(), at.Add(time.Minute).Unix()), nil)
		GetUserWalletLedger(c)
		if suspended {
			if rec.Code != 403 {
				t.Fatalf("suspended ledger: %d %s", rec.Code, rec.Body.String())
			}
		} else if !strings.Contains(rec.Body.String(), `"credit_microusd":2000000`) || strings.Contains(rec.Body.String(), `9000000`) {
			t.Fatalf("wrong payer ledger: %s", rec.Body.String())
		}
	}
}

func TestSharedOwnerLegacyProjectRolloutGate(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	owner := model.User{Id: 501, Username: "rollout-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	resolve := func() error {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/api/token/1", nil)
		c.Set("id", owner.Id)
		_, err := middleware.ResolveConsoleProject(c, "")
		return err
	}
	t.Setenv("PROJECTS_ENABLED", "false")
	if err := resolve(); err != nil {
		t.Fatalf("unshared legacy access blocked: %v", err)
	}
	if err := db.Model(&project).Update("upgraded_at", time.Now().Unix()).Error; err != nil {
		t.Fatal(err)
	}
	if err := resolve(); err != model.ErrProjectFeatureUnavailable {
		t.Fatalf("shared owner bypassed rollout: %v", err)
	}
	t.Setenv("PROJECTS_ENABLED", "false")
	t.Setenv("PROJECTS_ALLOWED_USERS", owner.PublicID)
	if err := resolve(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROJECTS_ALLOWED_USERS", "usr_someone_else")
	if err := resolve(); err != model.ErrProjectFeatureUnavailable {
		t.Fatalf("removed allowlist bypass: %v", err)
	}
}

// The sign-up Key is created through the project-aware path, so it belongs to
// the new account's default project and is refused outright when that
// membership is not active. A raw insert would have produced a live Key either
// way, relying on a model hook to guess the project.
func TestDefaultSignupKeyIsCreatedInsideTheDefaultProject(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	previous := constant.GenerateDefaultToken
	constant.GenerateDefaultToken = true
	t.Cleanup(func() { constant.GenerateDefaultToken = previous })
	user := model.User{Username: "signup-key"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", user.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := initializeNewUserFeatures(&user); err != nil {
		t.Fatal(err)
	}
	var token model.Token
	if err := db.Where("user_id = ?", user.Id).First(&token).Error; err != nil {
		t.Fatalf("registration produced no Key: %v", err)
	}
	if token.ProjectID != project.ID || token.Status != common.TokenStatusEnabled {
		t.Fatalf("sign-up Key is not a live project Key: %+v", token)
	}
	if _, err := model.ResolveProjectPrincipal(model.CoreDB(), token.Id, time.Now().UTC()); err != nil {
		t.Fatalf("sign-up Key cannot bill anybody: %v", err)
	}
	// An account whose owner membership is not active gets no Key at all: the
	// project path checks membership, a direct insert never did.
	blocked := model.User{Username: "signup-key-suspended"}
	if err := db.Create(&blocked).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ProjectMember{}).Where("user_id = ?", blocked.Id).Update("status", model.ProjectMemberSuspended).Error; err != nil {
		t.Fatal(err)
	}
	if err := initializeNewUserFeatures(&blocked); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.Token{}).Where("user_id = ?", blocked.Id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("suspended membership still received %d Keys", count)
	}
}

// Every project-scoped Key endpoint speaks the console's contract: a status
// string, model limits as an array, the month's spend, unix timestamps, whether
// the secret can still be produced and whether a removal stopped the Key.
func TestProjectKeyPayloadFollowsTheContract(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}, &model.ProjectInvite{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })
	t.Setenv("PROJECTS_ENABLED", "true")
	owner := model.User{Username: "payload-owner", Quota: 50000000}
	member := model.User{Username: "payload-member", Email: "payload-member@example.test"}
	for _, user := range []*model.User{&owner, &member} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := now.Unix()
	if err := db.Model(&project).Update("upgraded_at", at).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ProjectMember{ProjectID: project.ID, UserID: member.Id, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive}).Error; err != nil {
		t.Fatal(err)
	}
	live := model.Token{ProjectID: project.ID, UserId: member.Id, Name: "live", Key: strings.Repeat("p", 48), Status: common.TokenStatusEnabled,
		ExpiredTime: -1, UnlimitedQuota: true, ModelLimitsEnabled: true, ModelLimits: "gpt-4o,claude-sonnet", CreatedTime: 1700000000, LastUsedAt: 1700000500}
	stopped := model.Token{ProjectID: project.ID, UserId: member.Id, Name: "stopped", Key: strings.Repeat("q", 48), Status: common.TokenStatusEnabled,
		ExpiredTime: -1, UnlimitedQuota: true, CreatedTime: 1700000001}
	for _, token := range []*model.Token{&live, &stopped} {
		if err := db.Create(token).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&stopped).Updates(map[string]interface{}{"removal_operation_id": "rmv_test", "key_digest": nil, "key": "digest:gone"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ProjectSpend{EventKey: "payload-spend", ProjectID: project.ID, ActorUserID: member.Id, PayerUserID: owner.Id, TokenID: live.Id, RequestID: "payload-request", AmountMicrousd: 4321, OccurredAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Log{ProjectID: project.ID, UserId: member.Id, TokenId: live.Id, Type: model.LogTypeConsume, CreatedAt: 1700000500}).Error; err != nil {
		t.Fatal(err)
	}
	// The project list is a manager view; the legacy list only ever shows the
	// reader their own Keys, so it is read as the member who owns them.
	actor := owner.Id
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", actor) })
	router.GET("/api/project/:id/tokens", middleware.ProjectConsoleContext(), ListProjectTokens)
	router.GET("/api/token/", GetAllTokens)
	type payload struct {
		ID                 string   `json:"id"`
		Status             string   `json:"status"`
		ModelLimits        []string `json:"model_limits"`
		MonthSpendMicrousd int64    `json:"month_spend_microusd"`
		CreatedAt          int64    `json:"created_at"`
		LastUsedAt         *int64   `json:"last_used_at"`
		SecretAvailable    bool     `json:"secret_available"`
		StoppedByRemoval   bool     `json:"stopped_by_removal"`
	}
	read := func(path string) map[string]payload {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		var body struct {
			Data struct {
				Items []payload `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		result := map[string]payload{}
		for _, item := range body.Data.Items {
			result[item.ID] = item
		}
		if len(result) != 2 {
			t.Fatalf("%s returned %d keys: %s", path, len(result), response.Body.String())
		}
		return result
	}
	for _, path := range []string{"/api/project/" + project.PublicID + "/tokens", "/api/token/?project_id=" + project.PublicID} {
		if strings.HasPrefix(path, "/api/token/") {
			actor = member.Id
		}
		rows := read(path)
		got := rows[live.PublicID]
		if got.Status != "enabled" {
			t.Fatalf("%s status: %+v", path, got)
		}
		if !reflect.DeepEqual(got.ModelLimits, []string{"gpt-4o", "claude-sonnet"}) {
			t.Fatalf("%s model limits: %+v", path, got)
		}
		if got.MonthSpendMicrousd != 4321 {
			t.Fatalf("%s month spend: %+v", path, got)
		}
		if got.CreatedAt != 1700000000 || got.LastUsedAt == nil || *got.LastUsedAt != 1700000500 {
			t.Fatalf("%s timestamps: %+v", path, got)
		}
		if !got.SecretAvailable || got.StoppedByRemoval {
			t.Fatalf("%s live key flags: %+v", path, got)
		}
		dead := rows[stopped.PublicID]
		if dead.Status != "disabled" || !dead.StoppedByRemoval {
			t.Fatalf("%s stopped key state: %+v", path, dead)
		}
		if dead.SecretAvailable || dead.LastUsedAt != nil || dead.MonthSpendMicrousd != 0 {
			t.Fatalf("%s stopped key details: %+v", path, dead)
		}
		if len(dead.ModelLimits) != 0 {
			t.Fatalf("%s empty model limits are not an array: %+v", path, dead)
		}
	}
	// The legacy `p` and the contract's `page` both page the same list.
	for _, query := range []string{"p=2", "page=2"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/token/?project_id="+project.PublicID+"&page_size=1&"+query, nil))
		var body struct {
			Data struct {
				Page  int       `json:"page"`
				Total int       `json:"total"`
				Items []payload `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || body.Data.Page != 2 || body.Data.Total != 2 || len(body.Data.Items) != 1 {
			t.Fatalf("%s did not page: %d %s", query, response.Code, response.Body.String())
		}
		if body.Data.Items[0].ID != live.PublicID {
			t.Fatalf("%s returned the wrong page: %s", query, response.Body.String())
		}
	}
}

// A budget row nobody changed has no changer. An empty UserRef would render as
// a nameless person in the history.
func TestProjectBudgetHistoryReportsAnAbsentChangerAsNull(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}, &model.ProjectInvite{}, &model.BudgetRequest{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })
	t.Setenv("PROJECTS_ENABLED", "true")
	owner := model.User{Username: "history-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	amount := billing.MicroUSD(500000)
	for _, row := range []model.ProjectBudgetHistory{
		{ProjectID: project.ID, UserID: owner.Id, ChangedBy: 0, NewBudgetMicrousd: &amount, Reason: "invite snapshot", CreatedAt: 20},
		{ProjectID: project.ID, UserID: owner.Id, ChangedBy: owner.Id, NewBudgetMicrousd: &amount, Reason: "owner edit", CreatedAt: 10},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", owner.Id) })
	router.GET("/api/project/:id/members/:uid/budget-history", middleware.ProjectConsoleContext(), GetProjectBudgetHistory)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/api/project/"+project.PublicID+"/members/"+owner.PublicID+"/budget-history", nil))
	if response.Code != 200 {
		t.Fatalf("history: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Data struct {
			Items []struct {
				Reason    string                `json:"reason"`
				ChangedBy *model.ProjectUserRef `json:"changed_by"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Items) != 2 {
		t.Fatalf("history rows: %s", response.Body.String())
	}
	if body.Data.Items[0].ChangedBy != nil {
		t.Fatalf("absent changer rendered as a person: %s", response.Body.String())
	}
	if body.Data.Items[1].ChangedBy == nil || body.Data.Items[1].ChangedBy.ID != owner.PublicID {
		t.Fatalf("real changer lost: %s", response.Body.String())
	}
}

// An idempotent replay of an already-paid order is a success in both halves of
// the legacy envelope; clients read `message` before they read `data`.
func TestProjectStripeReplayOfPaidOrderReportsSuccess(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROJECTS_ENABLED", "true")
	oldKey, oldPrice := setting.StripeApiSecret, setting.StripePriceId
	setting.StripeApiSecret = "disabled-for-test"
	setting.StripePriceId = "price_test"
	t.Cleanup(func() { setting.StripeApiSecret = oldKey; setting.StripePriceId = oldPrice })
	user := model.User{Username: "stripe-replay"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	call := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", user.Id)
		c.Request = httptest.NewRequest("POST", "/api/user/stripe/pay", nil)
		c.Request.Header.Set("Idempotency-Key", "replay-operation")
		requestProjectStripePay(c, &StripePayRequest{Amount: 10})
		return recorder
	}
	call()
	var order model.TopUp
	if err := db.Where("user_id = ?", user.Id).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&order).Update("status", common.TopUpStatusSuccess).Error; err != nil {
		t.Fatal(err)
	}
	replay := call()
	if replay.Code != 200 {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			PaymentStatus string `json:"payment_status"`
			TopUpID       string `json:"top_up_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success || body.Data.PaymentStatus != "paid" || body.Data.TopUpID != order.PublicID {
		t.Fatalf("paid replay does not read as a success: %s", replay.Body.String())
	}
}

// The member detail's key_count and keys[] are the same projection the Key
// limit charges against, so they have to agree with it: a Key belongs to the
// member named by tokens.user_id and to nobody else, and an ownerless leftover
// is neither shown nor counted for anyone.
func TestProjectMemberDetailCountsOnlyTheMembersOwnKeys(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}, &model.ProjectInvite{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })
	t.Setenv("PROJECTS_ENABLED", "true")
	owner := model.User{Username: "legacy-detail-owner", Quota: 50000000}
	other := model.User{Username: "legacy-detail-member", Email: "legacy-detail-member@example.test"}
	for _, user := range []*model.User{&owner, &other} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Update("upgraded_at", time.Now().UTC().Unix()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ProjectMember{ProjectID: project.ID, UserID: other.Id, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive}).Error; err != nil {
		t.Fatal(err)
	}
	ordinary := model.Token{ProjectID: project.ID, UserId: owner.Id, Name: "ordinary", Key: strings.Repeat("a", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	legacy := model.Token{ProjectID: project.ID, UserId: owner.Id, Name: "second-owner-key", Key: strings.Repeat("b", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	foreign := model.Token{ProjectID: project.ID, UserId: other.Id, Name: "member-key", Key: strings.Repeat("c", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	for _, token := range []*model.Token{&ordinary, &legacy, &foreign} {
		if err := db.Create(token).Error; err != nil {
			t.Fatal(err)
		}
	}
	// An ownerless leftover is nobody's: presenting it as the owner's would
	// advertise mutations that all fail, and it must not be counted against the
	// owner either.
	orphan := model.Token{ProjectID: project.ID, UserId: owner.Id, Name: "orphan", Key: strings.Repeat("d", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	if err := db.Create(&orphan).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Token{}).Where("id = ?", orphan.Id).Update("user_id", 0).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", owner.Id) })
	router.GET("/api/project/:id/members/:uid", middleware.ProjectConsoleContext(), GetProjectMember)
	read := func(publicID string) (int64, map[string]bool) {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/project/"+project.PublicID+"/members/"+publicID, nil))
		if response.Code != 200 {
			t.Fatalf("member detail: %d %s", response.Code, response.Body.String())
		}
		var body struct {
			Data struct {
				KeyCount int64 `json:"key_count"`
				Keys     []struct {
					ID string `json:"id"`
				} `json:"keys"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, key := range body.Data.Keys {
			ids[key.ID] = true
		}
		return body.Data.KeyCount, ids
	}
	count, ids := read(owner.PublicID)
	if count != 2 {
		t.Fatalf("owner key_count is wrong: %d", count)
	}
	if ids[orphan.PublicID] {
		t.Fatalf("an ownerless Key was shown as the owner's: %v", ids)
	}
	if !ids[legacy.PublicID] || !ids[ordinary.PublicID] || len(ids) != 2 {
		t.Fatalf("owner keys[] wrong: %v", ids)
	}
	// A member sees its own Key and inherits nothing.
	count, ids = read(other.PublicID)
	if count != 1 || !ids[foreign.PublicID] || len(ids) != 1 {
		t.Fatalf("member detail is wrong: %d %v", count, ids)
	}
}

// With the Key limit no longer a small product rule, the member detail pages
// its keys[] instead of returning every Key at once; key_count stays the total
// so a reader knows how many pages there are.
func TestProjectMemberDetailPagesKeys(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}, &model.ProjectInvite{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })
	t.Setenv("PROJECTS_ENABLED", "true")
	owner := model.User{Username: "paged-detail-owner", Quota: 50000000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	total := projectMemberKeysPageSize + 7
	for i := 0; i < total; i++ {
		token := model.Token{ProjectID: project.ID, UserId: owner.Id, Name: fmt.Sprintf("k%d", i), Key: fmt.Sprintf("%048d", i+1), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
		if err := db.Create(&token).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", owner.Id) })
	router.GET("/api/project/:id/members/:uid", middleware.ProjectConsoleContext(), GetProjectMember)
	read := func(query string) (int, int64, map[string]bool) {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/project/"+project.PublicID+"/members/"+owner.PublicID+query, nil))
		if response.Code != 200 {
			return response.Code, 0, nil
		}
		var body struct {
			Data struct {
				KeyCount int64 `json:"key_count"`
				Keys     []struct {
					ID string `json:"id"`
				} `json:"keys"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, key := range body.Data.Keys {
			ids[key.ID] = true
		}
		return response.Code, body.Data.KeyCount, ids
	}
	_, count, first := read("")
	if count != int64(total) || len(first) != projectMemberKeysPageSize {
		t.Fatalf("first page: key_count=%d keys=%d", count, len(first))
	}
	_, _, second := read("?keys_page=2")
	if len(second) != 7 {
		t.Fatalf("second page has %d keys", len(second))
	}
	for id := range second {
		if first[id] {
			t.Fatalf("key %s appears on both pages", id)
		}
	}
	if code, _, _ := read("?keys_page=0"); code == 200 {
		t.Fatal("keys_page=0 was accepted")
	}
}

// projectTokenPayload backs /api/token/ list, search, detail, create and
// update, so replacing the numeric `status` with the contract string changed an
// established field's type on all five. status_code carries the number on every
// one of them (and on /api/token/inspect, which keeps the numeric `status`), so
// a reader that wants the number never has to guess the shape.
func TestTokenPayloadsAlwaysCarryNumericStatusCode(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}, &model.TokenAuditLog{}, &model.SpendBudgetAlert{}, &model.ProjectInvite{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })
	t.Setenv("PROJECTS_ENABLED", "true")
	user := model.User{Username: "status-code-owner", Quota: 50000000}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", user.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	disabled := model.Token{ProjectID: project.ID, UserId: user.Id, Name: "disabled", Key: strings.Repeat("s", 48),
		Status: common.TokenStatusDisabled, ExpiredTime: -1, UnlimitedQuota: true}
	if err := db.Create(&disabled).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", user.Id) })
	router.GET("/api/token/", GetAllTokens)
	router.GET("/api/token/search", SearchTokens)
	router.GET("/api/token/:id", GetToken)
	router.POST("/api/token/", AddToken)
	router.PUT("/api/token/", UpdateToken)
	// status must be the contract string and status_code the stored number on
	// every projection that carries a status.
	check := func(label string, payload map[string]json.RawMessage) {
		t.Helper()
		if len(payload) == 0 {
			t.Fatalf("%s returned no key payload", label)
		}
		raw, ok := payload["status"]
		if !ok {
			t.Fatalf("%s dropped status", label)
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			t.Fatalf("%s status is not the contract string: %s", label, raw)
		}
		code, ok := payload["status_code"]
		if !ok {
			t.Fatalf("%s emits status without status_code", label)
		}
		var number int
		if err := json.Unmarshal(code, &number); err != nil || number < common.TokenStatusEnabled {
			t.Fatalf("%s status_code is not the stored number: %s %v", label, code, err)
		}
	}
	call := func(method, path string, body any) json.RawMessage {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(raw)
		} else {
			reader = bytes.NewReader(nil)
		}
		request := httptest.NewRequest(method, path, reader)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"success":true`) {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		var body2 struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body2); err != nil {
			t.Fatal(err)
		}
		return body2.Data
	}
	suffix := "project_id=" + project.PublicID
	var listed struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(call("GET", "/api/token/?"+suffix, nil), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 {
		t.Fatalf("list returned %d keys", len(listed.Items))
	}
	check("list", listed.Items[0])
	var searched []map[string]json.RawMessage
	if err := json.Unmarshal(call("GET", "/api/token/search?"+suffix, nil), &searched); err != nil {
		t.Fatal(err)
	}
	if len(searched) != 1 {
		t.Fatalf("search returned %d keys", len(searched))
	}
	check("search", searched[0])
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(call("GET", "/api/token/"+disabled.PublicID+"?"+suffix, nil), &detail); err != nil {
		t.Fatal(err)
	}
	check("detail", detail)
	var created struct {
		Token map[string]json.RawMessage `json:"token"`
	}
	if err := json.Unmarshal(call("POST", "/api/token/", gin.H{"project_id": project.PublicID, "name": "fresh"}), &created); err != nil {
		t.Fatal(err)
	}
	check("create", created.Token)
	var fresh model.Token
	if err := db.Where("project_id = ? AND name = ?", project.ID, "fresh").First(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	var updated map[string]json.RawMessage
	if err := json.Unmarshal(call("PUT", "/api/token/?policy_only=true", gin.H{"id": fresh.PublicID, "project_id": project.PublicID, "expected_version": fresh.ProjectVersion, "name": "renamed", "model_limits": []string{"gpt-4o"}, "cap_microusd": nil}), &updated); err != nil {
		t.Fatal(err)
	}
	check("update", updated)
}

// A legacy invitation stored a seven-day expires_at. Accept and preview already
// clamp it to the three-day rule, so the summaries have to clamp it too:
// otherwise a day-four link is rejected at the door while the project header and
// the attention panel keep counting it as pending until day seven.
func TestPendingInviteSummariesUseTheEffectiveDeadline(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.ProjectInvite{}, &model.BudgetRequest{}, &model.Log{}); err != nil {
		t.Fatal(err)
	}
	oldLogDB := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLogDB) })
	t.Setenv("PROJECTS_ENABLED", "true")
	owner := model.User{Id: 41, Username: "invite-summary-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	day := int64(24 * 3600)
	resent := now.Unix() - day
	invites := []model.ProjectInvite{
		// Written by an older binary: issued four days ago, stored deadline in
		// three more days. Its effective deadline passed yesterday.
		{ProjectID: project.ID, Email: "legacy@example.com", Role: model.ProjectRoleMember, TokenDigest: "digest-legacy", Status: "pending", CreatedAt: now.Unix() - 4*day, ExpiresAt: now.Unix() + 3*day},
		// Issued today under the three-day rule.
		{ProjectID: project.ID, Email: "fresh@example.com", Role: model.ProjectRoleMember, TokenDigest: "digest-fresh", Status: "pending", CreatedAt: now.Unix(), ExpiresAt: now.Unix() + 3*day},
		// A resend restarts the window, so a stale row resent yesterday counts.
		{ProjectID: project.ID, Email: "resent@example.com", Role: model.ProjectRoleMember, TokenDigest: "digest-resent", Status: "pending", CreatedAt: now.Unix() - 10*day, ResentAt: &resent, ExpiresAt: now.Unix() + 6*day},
	}
	for i := range invites {
		if err := db.Create(&invites[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	// The row-level clamp the accept and preview paths apply.
	if status := invites[0].EffectiveStatus(now); status != "expired" {
		t.Fatalf("legacy invite is already rejected on accept, status=%s", status)
	}
	request := func(path string) *httptest.ResponseRecorder {
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("id", owner.Id); c.Set("role", common.RoleRootUser) })
		routes := router.Group("/api/project/:id", middleware.ProjectConsoleContext())
		routes.GET("", GetProject)
		routes.GET("/attention", GetProjectAttention)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/project/"+project.PublicID+path, nil))
		return response
	}
	details := request("")
	if details.Code != http.StatusOK {
		t.Fatalf("project details: %d %s", details.Code, details.Body.String())
	}
	var detailBody struct {
		Data struct {
			PendingInvites int64 `json:"pending_invites"`
		} `json:"data"`
	}
	if err := json.Unmarshal(details.Body.Bytes(), &detailBody); err != nil {
		t.Fatal(err)
	}
	if detailBody.Data.PendingInvites != 2 {
		t.Fatalf("project header counted a link accept already rejects: %s", details.Body.String())
	}
	attention := request("/attention")
	if attention.Code != http.StatusOK {
		t.Fatalf("attention: %d %s", attention.Code, attention.Body.String())
	}
	var attentionBody struct {
		Data struct {
			PendingInvites int64 `json:"pending_invites_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(attention.Body.Bytes(), &attentionBody); err != nil {
		t.Fatal(err)
	}
	if attentionBody.Data.PendingInvites != 2 {
		t.Fatalf("attention counted a link accept already rejects: %s", attention.Body.String())
	}
}

// A project-attributed relay rejection must land in the logs table exactly
// once, with the contract code in rejection_reason and both the model and the
// actor resolved — the console's attention aggregate, the rejection chips and
// the result=rejected&reason= filter all read those columns.
func TestProjectRelayRejectionIsLoggedOnceWithReasonModelAndActor(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}); err != nil {
		t.Fatal(err)
	}
	oldLog := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLog) })

	actor := model.User{Username: "removed-member"}
	if err := db.Create(&actor).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", actor.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}

	newRequest := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		c.Set("id", actor.Id)
		c.Set("token_name", "cli")
		c.Set("token_id", 7)
		// Deliberately no "username" and no "original_model": a budget
		// rejection fires before the user cache and channel selection run.
		common.SetContextKey(c, constant.ContextKeyRequestModel, "gpt-4o")
		common.SetContextKey(c, constant.ContextKeyProjectID, project.ID)
		common.SetContextKey(c, constant.ContextKeyPayerUserID, actor.Id)
		return c
	}

	rejection := types.NewErrorWithStatusCode(fmt.Errorf("member budget exhausted"),
		types.ErrorCodeMemberBudgetExceeded, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())

	c := newRequest()
	writeRelayError(c, types.RelayFormatOpenAI, nil, rejection)
	recordRelayRejection(c, rejection)

	var rows []model.Log
	if err := db.Where("project_id = ?", project.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly one rejection row, got %d", len(rows))
	}
	row := rows[0]
	if row.RejectionReason != string(types.ErrorCodeMemberBudgetExceeded) {
		t.Fatalf("rejection_reason = %q, want the contract code", row.RejectionReason)
	}
	if row.ModelName != "gpt-4o" {
		t.Fatalf("model_name = %q, want gpt-4o", row.ModelName)
	}
	if row.Username != actor.Username {
		t.Fatalf("username = %q, want %q", row.Username, actor.Username)
	}
	if row.UserId != actor.Id || row.Type != model.LogTypeError {
		t.Fatalf("unexpected attribution: %+v", row)
	}

	// Replaying the defer on the same request must not double-log.
	recordRelayRejection(c, rejection)
	var count int64
	db.Model(&model.Log{}).Where("project_id = ?", project.ID).Count(&count)
	if count != 1 {
		t.Fatalf("rejection logged %d times on one request", count)
	}

	// A channel error is written by processChannelError, which then sets
	// error_logged_to_file; the relay defer must stay out of its way.
	channelCtx := newRequest()
	channelErr := types.NewError(fmt.Errorf("upstream exploded"), types.ErrorCodeBadResponse)
	model.RecordErrorLog(channelCtx, actor.Id, 42, "gpt-4o", "cli", "upstream exploded", 7, 0, false, "", map[string]interface{}{"error_code": string(types.ErrorCodeBadResponse)})
	channelCtx.Set("error_logged_to_file", true)
	recordRelayRejection(channelCtx, channelErr)
	db.Model(&model.Log{}).Where("project_id = ? AND channel_id = ?", project.ID, 42).Count(&count)
	if count != 1 {
		t.Fatalf("channel error logged %d times", count)
	}
}

// The portal must distinguish "this project never paid" from a real conflict.
func TestProjectStripePortalReportsMissingBillingCustomer(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	t.Setenv("PROJECTS_ENABLED", "true")
	owner := model.User{Username: "portal-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", owner.Id) })
	router.GET("/api/user/self/stripe/portal", ProjectStripePortal)
	request := httptest.NewRequest("GET", "/api/user/self/stripe/portal?project_id="+project.PublicID, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"no_billing_customer"`) || !strings.Contains(response.Body.String(), `"lazu":"LZ-7110"`) {
		t.Fatalf("portal without a Stripe customer: %d %s", response.Code, response.Body.String())
	}
}

func TestOwnPaymentRecordUsesCheckoutModeAndReceiptReadiness(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	owner := model.User{Username: "snapshot-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	order := model.TopUp{PublicID: "top_snapshot_record", ProjectID: project.ID, UserId: owner.Id, PaidByUserID: owner.Id,
		StripeProjectName: "personal-handle", StripePaymentIntentID: "pi_snapshot", TradeNo: "ref_snapshot_record",
		Amount: 5, PaidAmount: 5, Status: common.TopUpStatusSuccess, InvoiceStatus: "ready"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", owner.Id) })
	router.GET("/api/user/payments/:topup_id", GetOwnPaymentRecord)
	read := func() struct {
		ProjectShared bool   `json:"project_shared"`
		ProjectName   string `json:"project_name"`
		ReceiptReady  bool   `json:"receipt_ready"`
	} {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/user/payments/"+order.PublicID, nil))
		var body struct {
			Data struct {
				ProjectShared bool   `json:"project_shared"`
				ProjectName   string `json:"project_name"`
				ReceiptReady  bool   `json:"receipt_ready"`
			} `json:"data"`
		}
		if response.Code != http.StatusOK {
			t.Fatalf("payment record: %d %s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Data
	}
	personal, shared := false, true
	for _, tc := range []struct {
		name          string
		snapshot      *bool
		projectShared bool
		snapshotName  string
		wantShared    bool
		wantName      string
	}{
		{"personal before upgrade", &personal, false, "personal-handle", false, ""},
		{"personal after upgrade", &personal, true, "personal-handle", false, ""},
		{"shared preserves checkout name", &shared, true, "Original team", true, "Original team"},
		{"shared uses checkout mode", &shared, false, "Original team", true, "Original team"},
		{"legacy personal", nil, false, "", false, ""},
		{"legacy shared", nil, true, "", true, "Renamed team"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var upgradedAt *int64
			if tc.projectShared {
				now := time.Now().Unix()
				upgradedAt = &now
			}
			if err := db.Model(&project).Updates(map[string]any{"upgraded_at": upgradedAt, "name": "Renamed team"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&order).Updates(map[string]any{"stripe_project_shared": tc.snapshot, "stripe_project_name": tc.snapshotName}).Error; err != nil {
				t.Fatal(err)
			}
			got := read()
			if got.ProjectShared != tc.wantShared || got.ProjectName != tc.wantName {
				t.Fatalf("payment mode drifted: %+v", got)
			}
		})
	}
	for _, tc := range []struct {
		name, intent, receipt, status string
		paid                          float64
		ready                         bool
	}{
		{"intent without receipt", "pi_snapshot", "", common.TopUpStatusSuccess, 5, false},
		{"receipt arrives", "pi_snapshot", "https://pay.stripe.com/receipts/snapshot", common.TopUpStatusSuccess, 5, true},
		{"receipt without resolver identity", "", "https://pay.stripe.com/receipts/snapshot", common.TopUpStatusSuccess, 5, false},
		{"unsafe receipt", "pi_snapshot", "https://example.com/receipt", common.TopUpStatusSuccess, 5, false},
		{"unpaid order", "pi_snapshot", "https://pay.stripe.com/receipts/snapshot", common.TopUpStatusPending, 5, false},
		{"free promotion", "pi_snapshot", "https://pay.stripe.com/receipts/snapshot", common.TopUpStatusSuccess, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := db.Model(&order).Updates(map[string]any{"stripe_payment_intent_id": tc.intent, "stripe_receipt_url": tc.receipt, "status": tc.status, "paid_amount": tc.paid}).Error; err != nil {
				t.Fatal(err)
			}
			if got := read(); got.ReceiptReady != tc.ready {
				t.Fatalf("incorrect receipt readiness: %+v", got)
			}
		})
	}
}
