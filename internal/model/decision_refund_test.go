package model

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestDecisionRefundIsIdempotentAndReversesOriginalPeriods(t *testing.T) {
	db, logs := newLedgerTestDBs(t)
	EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&DecisionGenerationRefund{}, &SpendBudgetCharge{}, &ProjectSpend{}, &SettlementOutbox{}); err != nil {
		t.Fatal(err)
	}
	user := User{Id: 9201, Username: "decision-payer", Quota: 1000}
	token := Token{Id: 9201, UserId: 9201, Key: "fixture", RemainQuota: 1000, UsedQuota: 100, Status: common.TokenStatusEnabled}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC)
	charge := SpendBudgetCharge{RequestID: "gen-refund", ProjectID: 42, TokenID: token.Id, AmountMicrousd: 100, PeriodStart: when.Unix(), PeriodEnd: when.Add(time.Minute).Unix()}
	if err := db.Create(&charge).Error; err != nil {
		t.Fatal(err)
	}
	spend := ProjectSpend{EventKey: "gen-refund-event", RequestID: "gen-refund", ProjectID: 42, TokenID: token.Id, ActorUserID: 11, PayerUserID: user.Id, AmountMicrousd: 100, OccurredAt: when}
	if err := db.Create(&spend).Error; err != nil {
		t.Fatal(err)
	}
	receipt := common.InternalChargeReceipt{RequestID: "gen-refund", UserID: user.Id, TokenID: token.Id, ProjectID: 42, ActorUserID: 11, Model: "gpt-6-luna", ChargedMicrousd: 100, SettlementUnix: when.Unix()}
	for i := 0; i < 3; i++ {
		if err := RecordDecisionGenerationRefund(receipt, "bad questions"); err != nil {
			t.Fatal(err)
		}
	}
	db.First(&user, user.Id)
	db.First(&token, token.Id)
	db.First(&charge, charge.ID)
	db.First(&spend, spend.ID)
	if user.Quota != 1100 || token.RemainQuota != 1100 || token.UsedQuota != 0 {
		t.Fatalf("wallet/key refund duplicated or missing: user=%d key=%d used=%d", user.Quota, token.RemainQuota, token.UsedQuota)
	}
	if charge.AmountMicrousd != 0 || spend.AmountMicrousd != 0 || !spend.OccurredAt.Equal(when) {
		t.Fatal("original-period spend was not reversed")
	}
	var count int64
	logs.Model(&Log{}).Where("request_id = ? AND type = ?", receipt.RequestID, LogTypeRefund).Count(&count)
	if count != 1 {
		t.Fatalf("refund log count=%d", count)
	}
	var ledger WalletLedger
	if err := db.Where("ref_type = ? AND ref_id = ?", "decision_generation_refund", receipt.RequestID).First(&ledger).Error; err != nil {
		t.Fatal(err)
	}
	if ledger.DeltaMicrousd != billing.MicroUSD(100) {
		t.Fatal("wrong refund ledger amount")
	}
}

func TestDecisionRefundResumesAfterLogDatabaseFailure(t *testing.T) {
	db, logs := newLedgerTestDBs(t)
	EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&DecisionGenerationRefund{}); err != nil {
		t.Fatal(err)
	}
	u := User{Id: 9202, Username: "refund-recovery", Quota: 1000}
	key := Token{Id: 9202, UserId: u.Id, Key: "recovery-fixture", RemainQuota: 1000, UsedQuota: 70, Status: common.TokenStatusEnabled}
	db.Create(&u)
	db.Create(&key)
	receipt := common.InternalChargeReceipt{RequestID: "gen-recover", UserID: u.Id, TokenID: key.Id, ChargedMicrousd: 70}
	telemetryDB = nil
	if err := RecordDecisionGenerationRefund(receipt, "bad questions"); err == nil {
		t.Fatal("missing log database should leave a durable retry")
	}
	telemetryDB = logs
	if err := ReconcileDecisionGenerationRefunds(); err != nil {
		t.Fatal(err)
	}
	db.First(&u, u.Id)
	if u.Quota != 1070 {
		t.Fatalf("recovery duplicated wallet refund: %d", u.Quota)
	}
	var row DecisionGenerationRefund
	db.Where("request_id = ?", receipt.RequestID).First(&row)
	if row.Status != "done" {
		t.Fatalf("refund still pending: %s", row.Status)
	}
}

func TestDecisionRefundWaitsForLegacySettlementAndUsesFinalAmount(t *testing.T) {
	db, logs := newLedgerTestDBs(t)
	EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&DecisionGenerationRefund{}, &SettlementFailure{}); err != nil {
		t.Fatal(err)
	}
	user := User{Id: 9203, Username: "refund-delayed-settlement", Quota: 1000}
	key := Token{Id: 9203, UserId: user.Id, Key: "delayed-fixture", RemainQuota: 1000, UsedQuota: 100, Status: common.TokenStatusEnabled}
	db.Create(&user)
	db.Create(&key)
	failure := SettlementFailure{RequestID: "gen-delayed", UserID: user.Id, TokenID: key.Id, QuotaDelta: 50, PreConsumed: 100, Status: SettlementFailureStatusOpen}
	db.Create(&failure)
	receipt := common.InternalChargeReceipt{RequestID: failure.RequestID, UserID: user.Id, TokenID: key.Id, ChargedMicrousd: 100}
	if err := RecordDecisionGenerationRefund(receipt, "invalid questions"); err == nil {
		t.Fatal("refunded before pending settlement")
	}
	db.First(&user, user.Id)
	if user.Quota != 1000 {
		t.Fatal("wallet changed before settlement")
	}
	if err := AdjustRelayQuotaWithLedger(AdjustRelayQuotaWithLedgerInput{AcceptedSettlement: true, UserID: user.Id, TokenID: key.Id, QuotaDelta: 50, RefType: "relay_settlement", RefID: failure.RequestID}); err != nil {
		t.Fatal(err)
	}
	db.Model(&failure).Update("status", SettlementFailureStatusResolved)
	if err := ApplyDecisionGenerationRefund(failure.RequestID); err != nil {
		t.Fatal(err)
	}
	db.First(&user, user.Id)
	db.First(&key, key.Id)
	if user.Quota != 1100 || key.RemainQuota != 1100 || key.UsedQuota != 0 {
		t.Fatalf("incorrect final refund user=%d key=%d used=%d", user.Quota, key.RemainQuota, key.UsedQuota)
	}
	var log Log
	if err := logs.Where("request_id = ? AND type = ?", failure.RequestID, LogTypeRefund).First(&log).Error; err != nil {
		t.Fatal(err)
	}
	if log.AmountMicrousd != -150 {
		t.Fatalf("refund log amount=%d", log.AmountMicrousd)
	}
}
