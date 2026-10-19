package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestStripeRefundReversesCreditOnceAndAllowsNegativeBalance(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "refund-owner", Quota: 2_000_000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	order := TopUp{PublicID: "topup-refund", TradeNo: "ref-refund", UserId: owner.Id, ProjectID: project.ID,
		StripeCustomerID: "cus_refund", StripePaymentIntentID: "pi_refund", StripeChargeID: "ch_refund",
		Status: common.TopUpStatusSuccess, PaidAmount: 10, CreditedMicrousd: 12_000_000}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	input := StripeChargeRefund{ChargeID: "ch_refund", PaymentIntentID: "pi_refund", CustomerID: "cus_refund", Currency: "usd", AmountCents: 1000, RefundedCents: 500}
	change, err := ApplyStripeChargeRefund(input)
	if err != nil || !change.Changed || change.ReversedCredit != 6_000_000 || change.Balance != -4_000_000 {
		t.Fatalf("partial refund: %+v %v", change, err)
	}
	for _, cents := range []int64{500, 300} {
		input.RefundedCents = cents
		if duplicate, err := ApplyStripeChargeRefund(input); err != nil || duplicate.Changed {
			t.Fatalf("duplicate/older refund: %+v %v", duplicate, err)
		}
	}
	input.RefundedCents = 1000
	change, err = ApplyStripeChargeRefund(input)
	if err != nil || change.ReversedCredit != 6_000_000 || change.Balance != -10_000_000 {
		t.Fatalf("full refund: %+v %v", change, err)
	}
	if err := AdjustUserQuotaWithLedger(AdjustUserQuotaWithLedgerInput{UserID: owner.Id, DeltaQuota: billing.MicroUSD(15_000_000), Kind: WalletLedgerKindTopup, RefType: "test", RefID: "next"}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&owner, owner.Id).Error; err != nil || owner.Quota != 5_000_000 {
		t.Fatalf("next top-up did not offset debt: %d %v", owner.Quota, err)
	}
	var count int64
	if err := db.Model(&WalletLedger{}).Where("kind = ?", WalletLedgerKindPaymentReversal).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("reversal entries: %d %v", count, err)
	}
}

func TestPaymentReconciliationForSoftDeletedOwner(t *testing.T) {
	for _, outcome := range []string{"won", "lost"} {
		t.Run(outcome, func(t *testing.T) {
			db := setupProjectTestDB(t, true)
			if err := db.AutoMigrate(&WalletLedger{}); err != nil {
				t.Fatal(err)
			}
			owner := User{Username: "deleted-payment-owner", Quota: 10_000_000}
			if err := db.Create(&owner).Error; err != nil {
				t.Fatal(err)
			}
			var project Project
			if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
				t.Fatal(err)
			}
			order := TopUp{PublicID: "topup-deleted-payment", TradeNo: "ref-deleted-payment", UserId: owner.Id, ProjectID: project.ID,
				StripeCustomerID: "cus_deleted_payment", StripePaymentIntentID: "pi_deleted_payment", StripeChargeID: "ch_deleted_payment",
				Status: common.TopUpStatusSuccess, PaidAmount: 10, CreditedMicrousd: 10_000_000}
			if err := db.Create(&order).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Delete(&owner).Error; err != nil {
				t.Fatal(err)
			}
			refund := StripeChargeRefund{ChargeID: order.StripeChargeID, PaymentIntentID: order.StripePaymentIntentID,
				CustomerID: "cus_deleted_payment", Currency: "usd", AmountCents: 1000, RefundedCents: 250}
			if change, err := ApplyStripeChargeRefund(refund); err != nil || change.ReversedCredit != 2_500_000 || change.Balance != 7_500_000 {
				t.Fatalf("refund after deletion: %+v %v", change, err)
			}
			if change, err := ApplyStripeChargeRefund(refund); err != nil || change.Changed {
				t.Fatalf("duplicate refund after deletion: %+v %v", change, err)
			}
			notice := StripeDisputeNotice{DisputeID: "dp_deleted_payment", ChargeID: order.StripeChargeID,
				PaymentIntentID: order.StripePaymentIntentID, CustomerID: "cus_deleted_payment", DisputeCents: 500, ChargeCents: 1000}
			if _, err := RecordStripeDispute(notice); err != nil {
				t.Fatal(err)
			}
			change, err := ResolveStripeDispute(order.PublicID, notice.DisputeID, outcome)
			if err != nil || !change.Changed {
				t.Fatalf("dispute %s after deletion: %+v %v", outcome, change, err)
			}
			if held, err := ProjectPaymentHeld(project.ID); err != nil || held {
				t.Fatalf("dispute hold remains: %t %v", held, err)
			}
			var saved User
			if err := db.Unscoped().First(&saved, owner.Id).Error; err != nil || !saved.DeletedAt.Valid {
				t.Fatalf("owner restored during reconciliation: %+v %v", saved, err)
			}
			want := billing.MicroUSD(7_500_000)
			if outcome == "lost" {
				want = 2_500_000
			}
			if saved.Quota != want || change.Balance != want {
				t.Fatalf("reconciled balance = %d, reported = %d, want %d", saved.Quota, change.Balance, want)
			}
		})
	}
}

func TestPartialDisputeReversesOnlyDisputedShareAndQueuesNotice(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	oldSecret := common.CryptoSecret
	common.CryptoSecret = "test-payment-risk"
	t.Cleanup(func() { common.CryptoSecret = oldSecret })
	owner := User{Username: "partial-dispute-owner", Quota: 12_000_000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	order := TopUp{PublicID: "topup-partial-dispute", TradeNo: "ref-partial-dispute", UserId: owner.Id, ProjectID: project.ID,
		StripeCustomerID: "cus_partial_dispute", StripePaymentIntentID: "pi_partial_dispute", StripeChargeID: "ch_partial_dispute",
		Status: common.TopUpStatusSuccess, PaidAmount: 10, CreditedMicrousd: 12_000_000}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	notice := StripeDisputeNotice{DisputeID: "dp_partial", ChargeID: order.StripeChargeID, PaymentIntentID: order.StripePaymentIntentID,
		CustomerID: "cus_partial_dispute", DisputeCents: 250, ChargeCents: 1000, AdminEmail: "admin@example.com", Reason: "fraudulent"}
	if _, err := RecordStripeDispute(notice); err != nil {
		t.Fatal(err)
	}
	change, err := ResolveStripeDispute(order.PublicID, notice.DisputeID, "lost", notice.AdminEmail)
	if err != nil || change.ReversedCredit != 3_000_000 || change.Balance != 9_000_000 {
		t.Fatalf("partial dispute: %+v %v", change, err)
	}
	if again, err := ResolveStripeDispute(order.PublicID, notice.DisputeID, "lost", notice.AdminEmail); err != nil || again.Changed {
		t.Fatalf("duplicate resolution: %+v %v", again, err)
	}
	refund, err := ApplyStripeChargeRefund(StripeChargeRefund{ChargeID: order.StripeChargeID, PaymentIntentID: order.StripePaymentIntentID,
		CustomerID: "cus_partial_dispute", Currency: "usd", AmountCents: 1000, RefundedCents: 400, AdminEmail: notice.AdminEmail})
	if err != nil || refund.ReversedCredit != 4_800_000 || refund.Balance != 4_200_000 {
		t.Fatalf("refund after partial dispute: %+v %v", refund, err)
	}
	var notices []ProjectNotification
	if err := db.Where("event_key LIKE ?", "payment-risk:%").Find(&notices).Error; err != nil || len(notices) != 3 {
		t.Fatalf("durable notices: %d %v", len(notices), err)
	}
	var opening ProjectNotification
	if err := db.Where("event_key = ?", "payment-risk:dispute-open:dp_partial").First(&opening).Error; err != nil {
		t.Fatal(err)
	}
	email, err := opening.Email()
	if err != nil || !strings.Contains(email.HTML, "fraudulent") || !strings.Contains(email.Subject, "[Lazu ops]") {
		t.Fatalf("dispute reason missing from email: %+v %v", email, err)
	}
}

func TestLegacyStripePaymentBindsVerifiedOrderForRefund(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "legacy-risk-owner", Quota: 5_000_000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	order := TopUp{PublicID: "topup-legacy-risk", TradeNo: "ref-legacy-risk", UserId: owner.Id, ProjectID: project.ID, Amount: 5,
		Status: common.TopUpStatusSuccess, InvoiceStatus: "legacy", PaidAmount: 5, CreditedMicrousd: 5_000_000}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	identity := LegacyStripePayment{Reference: order.TradeNo, SessionID: "cs_legacy_risk", PaymentIntentID: "pi_legacy_risk",
		ChargeID: "ch_legacy_risk", CustomerID: "cus_original", Currency: "usd", SubtotalCents: 500, AmountCents: 400}
	if err := BindLegacyStripePayment(identity); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&order, order.Id).Error; err != nil || paymentPaidCents(order) != 400 || order.PaidAmount != 4 {
		t.Fatalf("discounted charge amount was not persisted: %+v %v", order, err)
	}
	if err := BindLegacyStripePayment(identity); err != nil {
		t.Fatalf("duplicate binding: %v", err)
	}
	wrong := identity
	wrong.PaymentIntentID = "pi_wrong"
	if err := BindLegacyStripePayment(wrong); err == nil {
		t.Fatal("conflicting payment intent was accepted")
	}
	wrong = identity
	wrong.SubtotalCents = 600
	if err := BindLegacyStripePayment(wrong); err == nil {
		t.Fatal("checkout subtotal did not match the original order")
	}
	wrong = identity
	wrong.AmountCents = 300
	if err := BindLegacyStripePayment(wrong); err == nil {
		t.Fatal("bound charge amount changed on replay")
	}
	change, err := ApplyStripeChargeRefund(StripeChargeRefund{ChargeID: identity.ChargeID, PaymentIntentID: identity.PaymentIntentID,
		CustomerID: identity.CustomerID, Currency: identity.Currency, AmountCents: identity.AmountCents, RefundedCents: 400})
	if err != nil || change.ReversedCredit != 5_000_000 || change.Balance != 0 {
		t.Fatalf("legacy refund: %+v %v", change, err)
	}
}

func TestPendingLegacyStripePaymentBindsBeforeWalletCredit(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&WalletLedger{}, &Log{}); err != nil {
		t.Fatal(err)
	}
	previousLogDB := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = previousLogDB })
	owner := User{Username: "pending-legacy-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	order := TopUp{PublicID: "topup-pending-legacy", TradeNo: "ref-pending-legacy", UserId: owner.Id, ProjectID: project.ID,
		Amount: 5, Status: common.TopUpStatusPending, InvoiceStatus: "legacy", CreditedMicrousd: 5_000_000}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	identity := LegacyStripePayment{Reference: order.TradeNo, SessionID: "cs_pending_legacy", PaymentIntentID: "pi_pending_legacy",
		ChargeID: "ch_pending_legacy", CustomerID: "cus_pending_legacy", Currency: "usd", SubtotalCents: 500, AmountCents: 400}
	wrong := identity
	wrong.SubtotalCents = 600
	if err := BindLegacyStripePayment(wrong); err == nil {
		t.Fatal("unverified payment was bound")
	}
	if err := db.First(&owner, owner.Id).Error; err != nil || owner.Quota != 0 {
		t.Fatalf("wallet credited before Stripe binding: %d %v", owner.Quota, err)
	}
	if err := BindLegacyStripePayment(identity); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Recharge(order.TradeNo, identity.CustomerID, "127.0.0.1", "card"); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&order, order.Id).Error; err != nil || order.Status != common.TopUpStatusSuccess || order.StripePaymentIntentID != identity.PaymentIntentID || paymentPaidCents(order) != 400 {
		t.Fatalf("legacy payment lost verified binding or discount: %+v %v", order, err)
	}
	if err := db.First(&owner, owner.Id).Error; err != nil || owner.Quota != 5_000_000 {
		t.Fatalf("legacy wallet credit: %d %v", owner.Quota, err)
	}
}

func TestPaymentDocumentAlertsAdvancePastPriorBatch(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "alert-batch-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 201; i++ {
		order := TopUp{PublicID: fmt.Sprintf("topup-alert-batch-%03d", i), TradeNo: fmt.Sprintf("ref-alert-batch-%03d", i), UserId: owner.Id, ProjectID: project.ID,
			Status: common.TopUpStatusSuccess, InvoiceStatus: "failed", CompleteTime: now.Add(-2 * time.Hour).Unix()}
		if err := db.Create(&order).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := QueuePaymentDocumentAlerts(now, "admin@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&ProjectNotification{}).Where("event_key LIKE ?", "payment-alert:topup-alert-batch-%").Count(&count).Error; err != nil || count != 201 {
		t.Fatalf("alert batch did not advance: %d %v", count, err)
	}
}

func TestPaymentDocumentAlertsDeduplicateByReason(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "alert-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	order := TopUp{PublicID: "topup-alert", TradeNo: "ref-alert", UserId: owner.Id, ProjectID: project.ID,
		Status: common.TopUpStatusSuccess, InvoiceStatus: "failed", StripeInvoiceID: "in_alert", CompleteTime: now.Add(-2 * time.Hour).Unix()}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := QueueProjectInvoiceFailureAlert(order.TradeNo, "admin@example.com", "customer_tax_location_invalid: missing country"); err != nil {
			t.Fatal(err)
		}
		if err := QueuePaymentDocumentAlerts(now, "admin@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&ProjectNotification{}).Where("event_key = ?", "payment-alert:topup-alert:invoice_finalization_failed").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("invoice alert count: %d %v", count, err)
	}
	var alert ProjectNotification
	if err := db.Where("event_key = ?", "payment-alert:topup-alert:invoice_finalization_failed").First(&alert).Error; err != nil {
		t.Fatal(err)
	}
	email, err := alert.Email()
	if err != nil || !strings.Contains(email.HTML, "customer_tax_location_invalid: missing country") {
		t.Fatalf("Stripe finalization cause missing from alert: %+v %v", email, err)
	}
	order.PublicID, order.TradeNo, order.InvoiceStatus = "topup-receipt-alert", "ref-receipt-alert", "ready"
	order.StripeInvoicePDF, order.PaidAmount = "https://pay.stripe.com/invoice/test/pdf", 5
	order.Id = 0
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := QueuePaymentDocumentAlerts(now, "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&ProjectNotification{}).Where("event_key = ?", "payment-alert:topup-receipt-alert:receipt_missing_after_one_hour").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("receipt alert count: %d %v", count, err)
	}
	firstFailedAt := now.Add(-2 * time.Hour).Unix()
	failedEmail := ProjectNotification{ProjectID: project.ID, EventKey: "payment:topup-alert:1", Payload: "test", Status: "pending", Attempts: 1, FirstFailedAt: &firstFailedAt, CreatedAt: now.Add(-2 * time.Hour).Unix()}
	if err := db.Create(&failedEmail).Error; err != nil {
		t.Fatal(err)
	}
	if err := QueuePaymentDocumentAlerts(now, "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&ProjectNotification{}).Where("event_key = ?", "payment-alert:topup-alert:document_email_failed").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failed email alert count: %d %v", count, err)
	}
}

func TestRelayPreconsumeChecksProjectHoldInDebitTransaction(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "held-relay-owner", Quota: 1_000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Update("payment_risk_hold", true).Error; err != nil {
		t.Fatal(err)
	}
	input := PreConsumeQuotaWithLedgerInput{UserID: owner.Id, ProjectID: project.ID, IsPlayground: true, Quota: 100, RefType: "relay_preconsume", RefID: "held-relay"}
	if err := PreConsumeQuotaWithLedger(input); !errors.Is(err, ErrProjectAccessSuspended) {
		t.Fatalf("held project was charged: %v", err)
	}
	if err := db.First(&owner, owner.Id).Error; err != nil || owner.Quota != 1_000 {
		t.Fatalf("held debit changed owner quota: %d %v", owner.Quota, err)
	}
	var count int64
	if err := db.Model(&WalletLedger{}).Where("ref_id = ?", input.RefID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("held debit wrote ledger: %d %v", count, err)
	}
	if err := db.Model(&project).Update("payment_risk_hold", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := PreConsumeQuotaWithLedger(input); err != nil {
		t.Fatalf("released project could not debit: %v", err)
	}
}

func TestZeroCostProjectAdmissionChecksPaymentHold(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "free-admission-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Update("payment_risk_hold", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := CheckProjectPaymentAdmission(project.ID); !errors.Is(err, ErrProjectAccessSuspended) {
		t.Fatalf("held project admitted free request: %v", err)
	}
	if err := db.Model(&project).Update("payment_risk_hold", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := CheckProjectPaymentAdmission(project.ID); err != nil {
		t.Fatalf("released project rejected free request: %v", err)
	}
}

func TestStripeDisputeHoldsProjectAndDoesNotDoubleReverseRefund(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "dispute-owner", Quota: 1_000_000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	order := TopUp{PublicID: "topup-dispute", TradeNo: "ref-dispute", UserId: owner.Id, ProjectID: project.ID,
		StripeCustomerID: "cus_dispute", StripePaymentIntentID: "pi_dispute", StripeChargeID: "ch_dispute",
		Status: common.TopUpStatusSuccess, PaidAmount: 10, CreditedMicrousd: 10_000_000}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	refund := StripeChargeRefund{ChargeID: "ch_dispute", PaymentIntentID: "pi_dispute", CustomerID: "cus_dispute", Currency: "usd", AmountCents: 1000, RefundedCents: 400}
	if _, err := ApplyStripeChargeRefund(refund); err != nil {
		t.Fatal(err)
	}
	notice := StripeDisputeNotice{DisputeID: "dp_test", ChargeID: "ch_dispute", PaymentIntentID: "pi_dispute", CustomerID: "cus_dispute", DisputeCents: 1000, ChargeCents: 1000}
	if change, err := RecordStripeDispute(notice); err != nil || !change.Changed {
		t.Fatalf("dispute: %+v %v", change, err)
	}
	if held, err := ProjectPaymentHeld(project.ID); err != nil || !held {
		t.Fatalf("project should be held: %t %v", held, err)
	}
	if again, err := RecordStripeDispute(notice); err != nil || again.Changed {
		t.Fatalf("duplicate dispute: %+v %v", again, err)
	}
	change, err := ResolveStripeDispute(order.PublicID, notice.DisputeID, "lost")
	if err != nil || change.ReversedCredit != 6_000_000 || change.Balance != -9_000_000 {
		t.Fatalf("lost dispute: %+v %v", change, err)
	}
	if held, err := ProjectPaymentHeld(project.ID); err != nil || held {
		t.Fatalf("project hold not cleared: %t %v", held, err)
	}
	refund.RefundedCents = 1000
	if change, err := ApplyStripeChargeRefund(refund); err != nil || change.ReversedCredit != 0 {
		t.Fatalf("refund after loss double reversed: %+v %v", change, err)
	}
}
