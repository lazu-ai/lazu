package worker

import (
	"context"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

func TestSettlementOutboxRecordIsIdempotent(t *testing.T) {
	db := withSettlementFailureWorkerTestDB(t, "settlement_outbox_idempotent")
	previousOutbox := common.SettlementOutboxEnabled
	common.SettlementOutboxEnabled = true
	t.Cleanup(func() {
		common.SettlementOutboxEnabled = previousOutbox
	})

	row := model.SettlementOutbox{
		RequestID:      "request-outbox",
		UserID:         1,
		QuotaDelta:     100,
		RefType:        "relay_settlement",
		RefID:          "settle-outbox",
		PricingVersion: 12,
		Status:         model.SettlementFailureStatusOpen,
		Metadata:       "{}",
	}
	first, err := model.RecordSettlementOutbox(row)
	if err != nil {
		t.Fatalf("record first outbox failed: %v", err)
	}
	second, err := model.RecordSettlementOutbox(row)
	if err != nil {
		t.Fatalf("record second outbox failed: %v", err)
	}
	if first == nil || second == nil || first.ID != second.ID {
		t.Fatalf("expected idempotent outbox IDs, got first=%+v second=%+v", first, second)
	}
	var count int64
	if err := db.Model(&model.SettlementOutbox{}).Count(&count).Error; err != nil {
		t.Fatalf("count outbox failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one outbox row, got %d", count)
	}
}

func TestSettlementOutboxReconcilerResolvesSuccessfulReplay(t *testing.T) {
	db := withSettlementFailureWorkerTestDB(t, "settlement_outbox_reconcile_success")
	if err := db.Create(&model.User{Id: 1, Username: "outbox-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if _, err := model.RecordSettlementOutbox(model.SettlementOutbox{
		RequestID:      "request-outbox-replay",
		UserID:         1,
		TokenID:        2,
		TokenKey:       "token-key",
		QuotaDelta:     100,
		RefType:        "relay_settlement",
		RefID:          "settle-outbox-replay",
		PricingVersion: 12,
		Status:         model.SettlementFailureStatusOpen,
		Metadata:       "{}",
	}); err != nil {
		t.Fatalf("failed to create settlement outbox: %v", err)
	}

	stats, err := ReconcileSettlementOutboxOnce(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("reconcile outbox failed: %v", err)
	}
	if stats.Claimed != 1 || stats.Resolved != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	var user model.User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if user.Quota != 900 {
		t.Fatalf("expected user quota 900, got %d", user.Quota)
	}
	var outbox model.SettlementOutbox
	if err := db.First(&outbox, 1).Error; err != nil {
		t.Fatalf("failed to load outbox: %v", err)
	}
	if outbox.Status != model.SettlementFailureStatusResolved || outbox.ResolvedAt == nil {
		t.Fatalf("expected resolved outbox, got %+v", outbox)
	}
}

func TestSettlementOutboxReconcilerResolvesExistingLedgerWithoutDoubleCharge(t *testing.T) {
	db := withSettlementFailureWorkerTestDB(t, "settlement_outbox_existing_ledger")
	if err := db.Create(&model.User{Id: 1, Username: "outbox-existing", Quota: 900}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.WalletLedger{
		UserID:         1,
		DeltaMicrousd:  -100,
		Kind:           model.WalletLedgerKindSpend,
		RefType:        "relay_settlement",
		RefID:          "settle-existing-ledger",
		PricingVersion: 12,
	}).Error; err != nil {
		t.Fatalf("failed to create ledger: %v", err)
	}
	if _, err := model.RecordSettlementOutbox(model.SettlementOutbox{
		RequestID:      "request-existing-ledger",
		UserID:         1,
		QuotaDelta:     100,
		RefType:        "relay_settlement",
		RefID:          "settle-existing-ledger",
		PricingVersion: 12,
		Status:         model.SettlementFailureStatusOpen,
		Metadata:       "{}",
	}); err != nil {
		t.Fatalf("failed to create settlement outbox: %v", err)
	}

	stats, err := ReconcileSettlementOutboxOnce(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("reconcile outbox failed: %v", err)
	}
	if stats.Resolved != 1 {
		t.Fatalf("expected existing ledger to resolve, got %+v", stats)
	}
	var user model.User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if user.Quota != 900 {
		t.Fatalf("expected user quota unchanged at 900, got %d", user.Quota)
	}
}

func TestSettlementOutboxReconcilerRefundsUserWhenTokenMissing(t *testing.T) {
	db := withSettlementFailureWorkerTestDB(t, "settlement_outbox_refund_missing_token")
	if err := db.Create(&model.User{Id: 1, Username: "outbox-refund-user", Quota: 900}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if _, err := model.RecordSettlementOutbox(model.SettlementOutbox{
		RequestID:      "request-outbox-refund",
		UserID:         1,
		TokenID:        404,
		TokenKey:       "missing-token",
		QuotaDelta:     -100,
		RefType:        "relay_settlement",
		RefID:          "settle-outbox-refund",
		PricingVersion: 12,
		Status:         model.SettlementFailureStatusOpen,
		Metadata:       "{}",
	}); err != nil {
		t.Fatalf("failed to create settlement outbox: %v", err)
	}

	stats, err := ReconcileSettlementOutboxOnce(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("reconcile outbox failed: %v", err)
	}
	if stats.Resolved != 1 {
		t.Fatalf("expected refund to resolve without token, got %+v", stats)
	}
	var user model.User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if user.Quota != 1_000 {
		t.Fatalf("expected user quota refunded to 1000, got %d", user.Quota)
	}
	var ledger model.WalletLedger
	if err := db.Where("user_id = ? AND ref_id = ?", 1, "settle-outbox-refund").First(&ledger).Error; err != nil {
		t.Fatalf("failed to load refund ledger: %v", err)
	}
	if ledger.Kind != model.WalletLedgerKindRefund || ledger.DeltaMicrousd != 100 {
		t.Fatalf("expected refund ledger +100, got %+v", ledger)
	}
}
