package service

import (
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

func TestPostConsumeQuotaPersistsSettlementFailure(t *testing.T) {
	previousDB := model.CoreDB()
	previousRedis := common.RedisEnabled
	db, err := gorm.Open(gormsqlite.Open("file:settlement_failure?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	common.RedisEnabled = false
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		common.RedisEnabled = previousRedis
	})
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.WalletLedger{}, &model.SettlementFailure{}, &model.SettlementOutbox{}); err != nil {
		t.Fatalf("failed to migrate tables: %v", err)
	}
	if err := db.Create(&model.User{Id: 1, Username: "settlement-user", Quota: 10}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 100}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	err = PostConsumeQuota(&relaycommon.RelayInfo{
		RequestID:       "request-1",
		UserId:          1,
		TokenId:         2,
		TokenKey:        "token-key",
		TokenUnlimited:  true,
		IsPlayground:    true,
		OriginModelName: "gpt-test",
		UsingGroup:      model.ChannelLaneDirect,
		PriceData: types.PriceData{
			PricingVersion: 12,
		},
	}, 20, 0, false)
	if err == nil {
		t.Fatalf("expected settlement to fail")
	}

	var failure model.SettlementFailure
	if err := db.Where("request_id = ?", "request-1").First(&failure).Error; err != nil {
		t.Fatalf("failed to load settlement failure: %v", err)
	}
	if failure.UserID != 1 || failure.TokenID != 2 || failure.QuotaDelta != 20 || failure.PricingVersion != 12 {
		t.Fatalf("unexpected settlement failure row: %+v", failure)
	}
	if failure.TokenKey != "token-key" || !failure.TokenUnlimited || !failure.IsPlayground {
		t.Fatalf("expected replay fields to be persisted, got %+v", failure)
	}
	if failure.Status != model.SettlementFailureStatusOpen {
		t.Fatalf("expected open settlement failure, got %q", failure.Status)
	}
}

func TestPreCallReservationFailureDoesNotPersistSettlementFailure(t *testing.T) {
	previousDB := model.CoreDB()
	previousRedis := common.RedisEnabled
	db, err := gorm.Open(gormsqlite.Open("file:settlement_pre_call_skip?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	common.RedisEnabled = false
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		common.RedisEnabled = previousRedis
	})
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.WalletLedger{}, &model.SettlementFailure{}, &model.SettlementOutbox{}); err != nil {
		t.Fatalf("failed to migrate tables: %v", err)
	}
	if err := db.Create(&model.User{Id: 1, Username: "reservation-user", Quota: 100}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	err = postConsumeQuota(&relaycommon.RelayInfo{
		RequestID:       "pre-call-request",
		UserId:          1,
		IsPlayground:    true,
		OriginModelName: "gpt-test",
		UsingGroup:      model.ChannelLaneDirect,
		PriceData: types.PriceData{
			PricingVersion: 12,
		},
	}, 200, 0, false, false)
	if err == nil {
		t.Fatalf("expected pre-call reservation to fail")
	}

	var failureCount int64
	if err := db.Model(&model.SettlementFailure{}).Count(&failureCount).Error; err != nil {
		t.Fatalf("failed to count settlement failures: %v", err)
	}
	if failureCount != 0 {
		t.Fatalf("expected no settlement failure for failed pre-call reservation, got %d", failureCount)
	}
}

func TestPostConsumeQuotaOutboxResolvedAfterSuccessfulSettlement(t *testing.T) {
	previousDB := model.CoreDB()
	previousRedis := common.RedisEnabled
	previousOutbox := common.SettlementOutboxEnabled
	db, err := gorm.Open(gormsqlite.Open("file:settlement_outbox_success?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	common.RedisEnabled = false
	common.SettlementOutboxEnabled = true
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		common.RedisEnabled = previousRedis
		common.SettlementOutboxEnabled = previousOutbox
	})
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.WalletLedger{}, &model.SettlementFailure{}, &model.SettlementOutbox{}); err != nil {
		t.Fatalf("failed to migrate tables: %v", err)
	}
	if err := db.Create(&model.User{Id: 1, Username: "outbox-user", Quota: 100}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: 100}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	err = PostConsumeQuota(&relaycommon.RelayInfo{
		RequestID:       "request-outbox-success",
		UserId:          1,
		TokenId:         2,
		TokenKey:        "token-key",
		OriginModelName: "gpt-test",
		UsingGroup:      model.ChannelLaneDirect,
		PriceData: types.PriceData{
			PricingVersion: 12,
		},
	}, 20, 0, false)
	if err != nil {
		t.Fatalf("expected settlement to succeed: %v", err)
	}

	var outbox model.SettlementOutbox
	if err := db.Where("request_id = ?", "request-outbox-success").First(&outbox).Error; err != nil {
		t.Fatalf("failed to load settlement outbox: %v", err)
	}
	if outbox.Status != model.SettlementFailureStatusResolved || outbox.ResolvedAt == nil {
		t.Fatalf("expected resolved settlement outbox, got %+v", outbox)
	}
	var failureCount int64
	if err := db.Model(&model.SettlementFailure{}).Count(&failureCount).Error; err != nil {
		t.Fatalf("failed to count failures: %v", err)
	}
	if failureCount != 0 {
		t.Fatalf("expected no settlement failure, got %d", failureCount)
	}
}
