package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func withSettlementFailureWorkerTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()

	previousDB := model.CoreDB()
	previousRedisEnabled := common.RedisEnabled
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	model.EnsureColumnNamesInitialized()
	common.RedisEnabled = false
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.WalletLedger{}, &model.SettlementFailure{}, &model.SettlementOutbox{}, &model.LedgerLegacySettlementWaiver{}, &model.RedisLedgerOperation{}); err != nil {
		t.Fatalf("failed to migrate tables: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		common.RedisEnabled = previousRedisEnabled
	})
	return db
}

func TestSettlementFailureReconcilerResolvesSuccessfulReplay(t *testing.T) {
	db := withSettlementFailureWorkerTestDB(t, "settlement_reconcile_success")
	if err := db.Create(&model.User{Id: 1, Username: "settlement-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if err := db.Create(&model.SettlementFailure{
		RequestID:      "request-1",
		UserID:         1,
		TokenID:        2,
		TokenKey:       "token-key",
		QuotaDelta:     100,
		RefType:        "relay_settlement",
		RefID:          "settle-1",
		PricingVersion: 12,
		Status:         model.SettlementFailureStatusOpen,
		Metadata:       "{}",
	}).Error; err != nil {
		t.Fatalf("failed to create settlement failure: %v", err)
	}

	stats, err := ReconcileSettlementFailuresOnce(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
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
	var token model.Token
	if err := db.Select("remain_quota", "used_quota").First(&token, 2).Error; err != nil {
		t.Fatalf("failed to load token: %v", err)
	}
	if token.RemainQuota != 900 || token.UsedQuota != 100 {
		t.Fatalf("expected token remain=900 used=100, got remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}
	var failure model.SettlementFailure
	if err := db.First(&failure, 1).Error; err != nil {
		t.Fatalf("failed to load failure: %v", err)
	}
	if failure.Status != model.SettlementFailureStatusResolved || failure.Attempts != 1 || failure.ResolvedAt == nil {
		t.Fatalf("expected resolved failure with one attempt, got %+v", failure)
	}
}

func TestSettlementFailureReconcilerResolvesExistingLedgerWithoutDoubleCharge(t *testing.T) {
	db := withSettlementFailureWorkerTestDB(t, "settlement_reconcile_existing_ledger")
	if err := db.Create(&model.User{Id: 1, Username: "settlement-user", Quota: 50}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 50}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if err := db.Create(&model.WalletLedger{
		UserID:         1,
		DeltaMicrousd:  -100,
		Kind:           model.WalletLedgerKindSpend,
		RefType:        "relay_settlement",
		RefID:          "settle-existing",
		PricingVersion: 12,
	}).Error; err != nil {
		t.Fatalf("failed to create existing ledger: %v", err)
	}
	if err := db.Create(&model.SettlementFailure{
		RequestID:      "request-existing",
		UserID:         1,
		TokenID:        2,
		TokenKey:       "token-key",
		QuotaDelta:     100,
		RefType:        "relay_settlement",
		RefID:          "settle-existing",
		PricingVersion: 12,
		Status:         model.SettlementFailureStatusOpen,
		Metadata:       "{}",
	}).Error; err != nil {
		t.Fatalf("failed to create settlement failure: %v", err)
	}

	stats, err := ReconcileSettlementFailuresOnce(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if stats.Resolved != 1 {
		t.Fatalf("expected existing ledger to resolve, got %+v", stats)
	}
	var user model.User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("failed to load user: %v", err)
	}
	if user.Quota != 50 {
		t.Fatalf("expected user quota unchanged at 50, got %d", user.Quota)
	}
	var token model.Token
	if err := db.Select("remain_quota", "used_quota").First(&token, 2).Error; err != nil {
		t.Fatalf("failed to load token: %v", err)
	}
	if token.RemainQuota != 50 || token.UsedQuota != 0 {
		t.Fatalf("expected token unchanged, got remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}
}

func TestSettlementFailureReconcilerPreservesPlaygroundToken(t *testing.T) {
	db := withSettlementFailureWorkerTestDB(t, "settlement_reconcile_playground")
	if err := db.Create(&model.User{Id: 1, Username: "playground-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 500}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if err := db.Create(&model.SettlementFailure{
		RequestID:      "request-playground",
		UserID:         1,
		IsPlayground:   true,
		QuotaDelta:     100,
		RefType:        "relay_settlement",
		RefID:          "settle-playground",
		PricingVersion: 12,
		Status:         model.SettlementFailureStatusOpen,
		Metadata:       "{}",
	}).Error; err != nil {
		t.Fatalf("failed to create settlement failure: %v", err)
	}

	stats, err := ReconcileSettlementFailuresOnce(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if stats.Resolved != 1 {
		t.Fatalf("expected playground settlement to resolve, got %+v", stats)
	}
	var token model.Token
	if err := db.Select("remain_quota", "used_quota").First(&token, 2).Error; err != nil {
		t.Fatalf("failed to load token: %v", err)
	}
	if token.RemainQuota != 500 || token.UsedQuota != 0 {
		t.Fatalf("expected playground token unchanged, got remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}
}

func TestSettlementFailureReconcilerUsesTokenKeyWhenIDMissing(t *testing.T) {
	db := withSettlementFailureWorkerTestDB(t, "settlement_reconcile_token_key")
	if err := db.Create(&model.User{Id: 1, Username: "token-key-user", Quota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if err := db.Create(&model.SettlementFailure{
		RequestID:      "request-token-key",
		UserID:         1,
		TokenKey:       "token-key",
		QuotaDelta:     100,
		RefType:        "relay_settlement",
		RefID:          "settle-token-key",
		PricingVersion: 12,
		Status:         model.SettlementFailureStatusOpen,
		Metadata:       "{}",
	}).Error; err != nil {
		t.Fatalf("failed to create settlement failure: %v", err)
	}

	stats, err := ReconcileSettlementFailuresOnce(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if stats.Resolved != 1 {
		t.Fatalf("expected token-key settlement to resolve, got %+v", stats)
	}
	var token model.Token
	if err := db.Select("remain_quota", "used_quota").First(&token, 2).Error; err != nil {
		t.Fatalf("failed to load token: %v", err)
	}
	if token.RemainQuota != 900 || token.UsedQuota != 100 {
		t.Fatalf("expected token-key replay to debit token, got remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}
}

func TestSettlementFailureReconcilerManualReviewOnInsufficientQuota(t *testing.T) {
	db := withSettlementFailureWorkerTestDB(t, "settlement_reconcile_insufficient")
	if err := db.Create(&model.User{Id: 1, Username: "poor-user", Quota: 10}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 1_000}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if err := db.Create(&model.SettlementFailure{
		RequestID:      "request-insufficient",
		UserID:         1,
		TokenID:        2,
		TokenKey:       "token-key",
		QuotaDelta:     100,
		RefType:        "relay_settlement",
		RefID:          "settle-insufficient",
		PricingVersion: 12,
		Status:         model.SettlementFailureStatusOpen,
		Metadata:       "{}",
	}).Error; err != nil {
		t.Fatalf("failed to create settlement failure: %v", err)
	}

	stats, err := ReconcileSettlementFailuresOnce(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if stats.ManualReview != 1 {
		t.Fatalf("expected manual review, got %+v", stats)
	}
	var failure model.SettlementFailure
	if err := db.First(&failure, 1).Error; err != nil {
		t.Fatalf("failed to load failure: %v", err)
	}
	if failure.Status != model.SettlementFailureStatusManualReview || failure.Attempts != 1 {
		t.Fatalf("expected manual review with one attempt, got %+v", failure)
	}
	var ledgerCount int64
	if err := db.Model(&model.WalletLedger{}).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("failed to count ledgers: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("expected no ledger rows for insufficient quota, got %d", ledgerCount)
	}
}

func TestSettlementFailureReconcilerRefundsUserWhenTokenMissing(t *testing.T) {
	db := withSettlementFailureWorkerTestDB(t, "settlement_reconcile_refund_missing_token")
	if err := db.Create(&model.User{Id: 1, Username: "refund-user", Quota: 900}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.SettlementFailure{
		RequestID:      "request-refund",
		UserID:         1,
		TokenID:        404,
		TokenKey:       "missing-token",
		QuotaDelta:     -100,
		RefType:        "relay_settlement",
		RefID:          "settle-refund",
		PricingVersion: 12,
		Status:         model.SettlementFailureStatusOpen,
		Metadata:       "{}",
	}).Error; err != nil {
		t.Fatalf("failed to create settlement failure: %v", err)
	}

	stats, err := ReconcileSettlementFailuresOnce(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
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
	if err := db.Where("user_id = ? AND ref_id = ?", 1, "settle-refund").First(&ledger).Error; err != nil {
		t.Fatalf("failed to load refund ledger: %v", err)
	}
	if ledger.Kind != model.WalletLedgerKindRefund || ledger.DeltaMicrousd != 100 {
		t.Fatalf("expected refund ledger +100, got %+v", ledger)
	}
}
