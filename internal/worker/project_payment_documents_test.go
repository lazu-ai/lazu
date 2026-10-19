package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/client"
	"gorm.io/gorm"
)

func TestDocumentReconciliationKeepsStripeFinalizationError(t *testing.T) {
	oldDB, oldSecret, oldRedis := model.CoreDB(), common.CryptoSecret, common.RedisEnabled
	oldAdminEmail := email_setting.GetEmailSetting().AdminEmail
	db, err := gorm.Open(gormsqlite.Open("file:document_reconcile_failure?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	model.EnsureColumnNamesInitialized()
	common.CryptoSecret = "test-only"
	common.RedisEnabled = false
	email_setting.GetEmailSetting().AdminEmail = "admin@example.com"
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(oldDB)
		common.CryptoSecret = oldSecret
		common.RedisEnabled = oldRedis
		email_setting.GetEmailSetting().AdminEmail = oldAdminEmail
	})
	if err := db.AutoMigrate(&model.User{}, &model.Project{}, &model.ProjectMember{}, &model.TopUp{}, &model.ProjectNotification{}); err != nil {
		t.Fatal(err)
	}
	owner := model.User{Username: "failed-invoice-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Update("stripe_customer_id", "cus_failed_invoice").Error; err != nil {
		t.Fatal(err)
	}
	order := model.TopUp{PublicID: "topup-failed-invoice", ProjectID: project.ID, UserId: owner.Id,
		StripeCustomerID: "cus_failed_invoice", StripeCheckoutSessionID: "cs_failed_invoice", TradeNo: "ref_failed_invoice",
		Status: common.TopUpStatusSuccess, InvoiceStatus: "pending", CompleteTime: time.Now().Unix()}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/checkout/sessions/cs_failed_invoice":
			fmt.Fprint(w, `{"id":"cs_failed_invoice","client_reference_id":"ref_failed_invoice","customer":"cus_failed_invoice","payment_status":"paid","invoice":"in_failed_invoice"}`)
		case "/v1/invoices/in_failed_invoice":
			fmt.Fprint(w, `{"id":"in_failed_invoice","customer":"cus_failed_invoice","metadata":{"lazu_top_up_reference":"ref_failed_invoice"},"status":"draft","last_finalization_error":{"code":"customer_tax_location_invalid","message":"missing country"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	api := &client.API{}
	api.Init("sk_test", stripe.NewBackendsWithConfig(&stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client()}))
	for range 2 {
		if err := reconcileProjectPaymentDocument(context.Background(), api, order); err == nil || !strings.Contains(err.Error(), "missing country") {
			t.Fatalf("missing Stripe failure detail: %v", err)
		}
	}
	if err := db.First(&order, order.Id).Error; err != nil || order.InvoiceStatus != "failed" || order.StripeInvoiceID != "in_failed_invoice" {
		t.Fatalf("invoice failure was not persisted: %+v %v", order, err)
	}
	var notices []model.ProjectNotification
	if err := db.Where("event_key = ?", "payment-alert:topup-failed-invoice:invoice_finalization_failed").Find(&notices).Error; err != nil || len(notices) != 1 {
		t.Fatalf("failure alert count: %d %v", len(notices), err)
	}
	email, err := notices[0].Email()
	if err != nil || !strings.Contains(email.HTML, "customer_tax_location_invalid: missing country") {
		t.Fatalf("failure alert lost Stripe detail: %+v %v", email, err)
	}
}

func TestDocumentReconciliationAdvancesPastFailedOrders(t *testing.T) {
	oldDB, oldSecret := model.CoreDB(), setting.StripeApiSecret
	db, err := gorm.Open(gormsqlite.Open("file:project_document_retry_queue?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	if err := db.AutoMigrate(&model.TopUp{}, &model.ProjectNotification{}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour).Unix()
	for i := range 51 {
		order := model.TopUp{ProjectID: 1, PublicID: fmt.Sprintf("top_retry_%02d", i), TradeNo: fmt.Sprintf("ref_retry_%02d", i),
			StripeCheckoutSessionID: fmt.Sprintf("cs_retry_%02d", i), Status: common.TopUpStatusSuccess, InvoiceStatus: "failed", CompleteTime: base + int64(i)}
		if err := db.Create(&order).Error; err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"invoice unavailable"}}`)
	}))
	oldBackend := stripe.GetBackend(stripe.APIBackend)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client()}))
	setting.StripeApiSecret = "sk_test"
	t.Cleanup(func() {
		server.Close()
		stripe.SetBackend(stripe.APIBackend, oldBackend)
		setting.StripeApiSecret = oldSecret
		model.SetCoreDB(oldDB)
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	if err := ReconcileProjectPaymentDocuments(context.Background(), time.Now()); err == nil {
		t.Fatal("expected Stripe failures for the first batch")
	}
	remaining, err := model.ListProjectTopUpsMissingDocuments(50, base-1)
	if err != nil || len(remaining) != 50 {
		t.Fatalf("read next reconciliation batch: %d %v", len(remaining), err)
	}
	if remaining[0].PublicID != "top_retry_50" {
		t.Fatalf("newer order blocked by failed invoices: %s", remaining[0].PublicID)
	}
}

func TestProjectPaymentDocumentReconciliationRestoresBothLinksOnce(t *testing.T) {
	oldDB, oldSecret, oldRedis := model.CoreDB(), common.CryptoSecret, common.RedisEnabled
	db, err := gorm.Open(gormsqlite.Open("file:project_document_reconcile?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	model.EnsureColumnNamesInitialized()
	common.CryptoSecret = "test-only"
	common.RedisEnabled = false
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(oldDB)
		common.CryptoSecret = oldSecret
		common.RedisEnabled = oldRedis
	})
	if err := db.AutoMigrate(&model.User{}, &model.Project{}, &model.ProjectMember{}, &model.TopUp{}, &model.ProjectNotification{}); err != nil {
		t.Fatal(err)
	}
	owner := model.User{Username: "document-owner", Email: "owner@example.com"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Update("stripe_customer_id", "cus_test").Error; err != nil {
		t.Fatal(err)
	}
	order := model.TopUp{ProjectID: project.ID, UserId: owner.Id, PaidByUserID: owner.Id, StripeCustomerID: "cus_test", StripeCheckoutSessionID: "cs_test",
		StripePaymentIntentID: "pi_test", TradeNo: "ref_test", Amount: 5, PaidAmount: 5, Status: common.TopUpStatusSuccess,
		InvoiceStatus: "pending", CompleteTime: time.Now().Unix(), StripeReturnBase: "https://lazu.ai"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	stripeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/checkout/sessions/cs_test":
			fmt.Fprint(w, `{"id":"cs_test","client_reference_id":"ref_test","customer":"cus_test","payment_status":"paid","invoice":"in_test","payment_intent":"pi_test"}`)
		case "/v1/invoices/in_test":
			fmt.Fprint(w, `{"id":"in_test","customer":"cus_test","metadata":{"lazu_top_up_reference":"ref_test"},"status":"paid","hosted_invoice_url":"https://invoice.stripe.com/i/test","invoice_pdf":"https://pay.stripe.com/invoice/test/pdf","number":"INV-TEST","customer_name":"Example Company"}`)
		case "/v1/payment_intents/pi_test":
			fmt.Fprint(w, `{"id":"pi_test","customer":"cus_test","metadata":{"lazu_top_up_reference":"ref_test"},"latest_charge":{"id":"ch_test","customer":"cus_test","payment_intent":"pi_test","receipt_url":"https://pay.stripe.com/receipts/test","payment_method_details":{"type":"alipay"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer stripeServer.Close()
	api := &client.API{}
	api.Init("sk_test", stripe.NewBackendsWithConfig(&stripe.BackendConfig{URL: stripe.String(stripeServer.URL), HTTPClient: stripeServer.Client()}))
	for range 2 {
		if err := reconcileProjectPaymentDocument(context.Background(), api, order); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.First(&order, order.Id).Error; err != nil {
		t.Fatal(err)
	}
	if order.InvoiceStatus != "ready" || order.StripeReceiptURL == "" || order.PaymentMethod != "alipay" {
		t.Fatalf("documents not restored: %+v", order)
	}
	var notices []model.ProjectNotification
	if err := db.Where("event_key LIKE ?", "payment:%").Find(&notices).Error; err != nil || len(notices) != 1 {
		t.Fatalf("payment notices %d: %v", len(notices), err)
	}
	email, err := notices[0].Email()
	if err != nil || !strings.Contains(email.HTML, "document=invoice_page") || !strings.Contains(email.HTML, "INV-TEST") || !strings.Contains(email.HTML, "Alipay") {
		t.Fatalf("reconciled payment email is incomplete: %v", err)
	}
	pending, err := model.ListProjectTopUpsMissingDocuments(50, time.Now().Add(-time.Hour).Unix())
	if err != nil || len(pending) != 0 {
		t.Fatalf("reconciled top-up still pending: %d %v", len(pending), err)
	}
	// A completed order whose events never queued a payment e-mail still gets
	// exactly one.
	backfill := model.TopUp{ProjectID: project.ID, UserId: owner.Id, PaidByUserID: owner.Id, StripeCustomerID: "cus_test",
		StripeInvoiceID: "in_previous", StripeInvoiceNumber: "INV-PREVIOUS", StripeInvoicePDF: "https://pay.stripe.com/invoice/previous/pdf",
		StripePaymentIntentID: "pi_previous", StripeReceiptURL: "https://pay.stripe.com/receipts/previous", TradeNo: "ref_previous",
		Amount: 5, PaidAmount: 5, Status: common.TopUpStatusSuccess, InvoiceStatus: "ready", CompleteTime: time.Now().Unix()}
	if err := db.Create(&backfill).Error; err != nil {
		t.Fatal(err)
	}
	oldReturnBase := setting.StripeReturnBase
	setting.StripeReturnBase = "https://lazu.ai"
	t.Cleanup(func() { setting.StripeReturnBase = oldReturnBase })
	t.Setenv("FRONTEND_BASE_URL", "")
	for range 2 {
		if err := ReconcileProjectPaymentDocuments(context.Background(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var backfillNotices []model.ProjectNotification
	if err := db.Where("event_key = ?", fmt.Sprintf("payment:%s:%d", backfill.PublicID, owner.Id)).Find(&backfillNotices).Error; err != nil || len(backfillNotices) != 1 {
		t.Fatalf("completed order invoice email: %d %v", len(backfillNotices), err)
	}
	backfillEmail, err := backfillNotices[0].Email()
	if err != nil || !strings.Contains(backfillEmail.HTML, "https://lazu.ai/payment/document?project_id=") || !strings.Contains(backfillEmail.HTML, "top_up_id="+backfill.PublicID) {
		t.Fatalf("backfilled invoice email lacks absolute document URL: %v", err)
	}
	payer := model.User{Username: "document-payer", Email: "payer@example.com"}
	if err := db.Create(&payer).Error; err != nil {
		t.Fatal(err)
	}
	partial := backfill
	partial.Id = 0
	partial.PublicID = ""
	partial.PaidByUserID = payer.Id
	partial.TradeNo = "ref_partial"
	if err := db.Create(&partial).Error; err != nil {
		t.Fatal(err)
	}
	// The owner already received the pre-merge invoice mail; only the payer
	// is missing one.
	ownerEvent := fmt.Sprintf("invoice:%s:%d", partial.PublicID, owner.Id)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return model.QueueProjectEmailTx(tx, project.ID, owner.Id, ownerEvent, model.ProjectEmail{Receiver: owner.Email, Subject: "Existing owner invoice"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileProjectPaymentDocuments(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	var payerNotices []model.ProjectNotification
	if err := db.Where("event_key = ?", fmt.Sprintf("payment:%s:%d", partial.PublicID, payer.Id)).Find(&payerNotices).Error; err != nil || len(payerNotices) != 1 {
		t.Fatalf("missing payer payment email was not backfilled: %d %v", len(payerNotices), err)
	}
	var ownerRepeats int64
	if err := db.Model(&model.ProjectNotification{}).Where("event_key = ?", fmt.Sprintf("payment:%s:%d", partial.PublicID, owner.Id)).Count(&ownerRepeats).Error; err != nil || ownerRepeats != 0 {
		t.Fatalf("owner with a pre-merge invoice mail got another: %d %v", ownerRepeats, err)
	}
	free := backfill
	free.Id = 0
	free.PublicID = ""
	free.TradeNo = "ref_free_previous"
	free.PaidAmount = 0
	free.StripeReceiptURL = ""
	free.StripePaymentIntentID = ""
	if err := db.Create(&free).Error; err != nil {
		t.Fatal(err)
	}
	if err := ReconcileProjectPaymentDocuments(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	var freeNotice model.ProjectNotification
	if err := db.Where("event_key = ?", fmt.Sprintf("payment:%s:%d", free.PublicID, owner.Id)).First(&freeNotice).Error; err != nil {
		t.Fatalf("zero-dollar payment email was not backfilled: %v", err)
	}
	freeEmail, err := freeNotice.Email()
	if err != nil || strings.Contains(freeEmail.HTML, "document=receipt") {
		t.Fatalf("zero-dollar backfill should omit receipt: %v", err)
	}
}
