package service

import (
	"context"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestPaymentEmailAlertClockStartsAtFirstFailedDelivery(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.AutoMigrate(&model.TopUp{}, &model.ProjectNotification{}); err != nil {
		t.Fatal(err)
	}
	previousSecret := common.CryptoSecret
	common.CryptoSecret = "test-notification-clock" // secret-scan: allow -- deterministic test-only encryption key
	t.Cleanup(func() { common.CryptoSecret = previousSecret })
	now := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	order := model.TopUp{ProjectID: 1, PublicID: "topup-delivery-clock", TradeNo: "ref-delivery-clock", Status: common.TopUpStatusSuccess,
		InvoiceStatus: "ready", StripeInvoicePDF: "https://pay.stripe.com/invoice/test/pdf", StripeReceiptURL: "https://pay.stripe.com/receipts/test", PaidAmount: 5, CompleteTime: now.Add(-2 * time.Hour).Unix()}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	key := "invoice:" + order.PublicID + ":1"
	if err := model.QueueProjectEmailTx(db, 1, 1, key, model.ProjectEmail{Subject: "Invoice", HTML: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ProjectNotification{}).Where("event_key = ?", key).Update("created_at", now.Add(-2*time.Hour).Unix()).Error; err != nil {
		t.Fatal(err)
	}
	for _, attemptAt := range []time.Time{now, now.Add(2 * time.Minute)} {
		if err := DeliverProjectNotifications(context.Background(), attemptAt); err != nil {
			t.Fatal(err)
		}
		var notice model.ProjectNotification
		if err := db.Where("event_key = ?", key).First(&notice).Error; err != nil {
			t.Fatal(err)
		}
		if notice.FirstFailedAt == nil || *notice.FirstFailedAt != now.Unix() {
			t.Fatalf("first failure timestamp changed after retry: %+v", notice.FirstFailedAt)
		}
	}
	if err := model.QueuePaymentDocumentAlerts(now.Add(59*time.Minute), "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	var count int64
	alertKey := "payment-alert:" + order.PublicID + ":document_email_failed"
	if err := db.Model(&model.ProjectNotification{}).Where("event_key = ?", alertKey).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("alert sent before an hour of failures: %d %v", count, err)
	}
	if err := model.QueuePaymentDocumentAlerts(now.Add(61*time.Minute), "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ProjectNotification{}).Where("event_key = ?", alertKey).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("sustained failure was not alerted: %d %v", count, err)
	}
}

func TestNotificationDeliversPayloadUpdatedBetweenSelectionAndClaim(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.AutoMigrate(&model.ProjectNotification{}); err != nil {
		t.Fatal(err)
	}
	oldSecret := common.CryptoSecret
	common.CryptoSecret = "test-notification-race" // secret-scan: allow -- deterministic test-only encryption key
	t.Cleanup(func() { common.CryptoSecret = oldSecret })
	const key = "payment:race:1"
	if err := model.QueueProjectEmailTx(db, 1, 1, key, model.ProjectEmail{Receiver: "payer@example.com", Subject: "Ready", HTML: "View invoice"}); err != nil {
		t.Fatal(err)
	}
	var ready model.ProjectNotification
	if err := db.Where("event_key = ?", key).First(&ready).Error; err != nil {
		t.Fatal(err)
	}
	readyPayload := ready.Payload
	// Candidate carries invalid stale content. Replace with the ready payload
	// immediately after selection, as an invoice transaction would do.
	if err := db.Model(&ready).Update("payload", "stale-fallback").Error; err != nil {
		t.Fatal(err)
	}
	replaced := false
	if err := db.Callback().Query().After("gorm:query").Register("test:replace-selected-email", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*[]model.ProjectNotification); !ok || replaced {
			return
		}
		replaced = true
		if err := db.Model(&model.ProjectNotification{}).Where("id = ?", ready.ID).Update("payload", readyPayload).Error; err != nil {
			tx.AddError(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Query().Remove("test:replace-selected-email") })
	sends := 0
	send := func(_ model.ProjectNotification, subject, receiver, body string) error {
		sends++
		if subject != "Ready" || receiver != "payer@example.com" || body != "View invoice" {
			t.Fatalf("sent stale content: %q %q", subject, body)
		}
		return nil
	}
	now := time.Now().Add(time.Minute)
	for i := 0; i < 2; i++ {
		if err := deliverProjectNotifications(context.Background(), now, send); err != nil {
			t.Fatal(err)
		}
	}
	if !replaced || sends != 1 {
		t.Fatalf("replaced=%v sends=%d", replaced, sends)
	}
}

func TestFallbackEmailDispatchAtPersistedDeadline(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.AutoMigrate(&model.ProjectNotification{}); err != nil {
		t.Fatal(err)
	}
	previousSecret := common.CryptoSecret
	common.CryptoSecret = "test-fallback-deadline" // secret-scan: allow -- deterministic test-only encryption key
	t.Cleanup(func() { common.CryptoSecret = previousSecret })
	const key = "payment:due:1"
	if err := model.QueueProjectEmailTx(db, 1, 1, key, model.ProjectEmail{Subject: "Payment received", Receiver: "payer@example.com", HTML: "Fallback"}); err != nil {
		t.Fatal(err)
	}
	creditedAt := time.Now().UTC().Truncate(time.Second)
	due := creditedAt.Add(time.Minute)
	if err := db.Model(&model.ProjectNotification{}).Where("event_key = ?", key).Update("available_at", due.Unix()).Error; err != nil {
		t.Fatal(err)
	}
	sends := 0
	send := func(_ model.ProjectNotification, subject, recipient, body string) error { sends++; return nil }
	for _, at := range []time.Time{creditedAt, due.Add(-time.Second)} {
		if err := deliverProjectNotifications(context.Background(), at, send); err != nil {
			t.Fatal(err)
		}
		if sends != 0 {
			t.Fatal("fallback sent before grace period")
		}
	}
	for _, at := range []time.Time{due, due.Add(time.Minute)} {
		if err := deliverProjectNotifications(context.Background(), at, send); err != nil {
			t.Fatal(err)
		}
		if sends != 1 {
			t.Fatalf("fallback not sent exactly once at deadline: %d", sends)
		}
	}
}
