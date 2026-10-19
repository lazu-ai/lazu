package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func setupMediaJobBillingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalDB := coreDB
	originalLogDB := telemetryDB
	originalLogConsumeEnabled := common.LogConsumeEnabled
	originalRedisEnabled := common.RedisEnabled
	originalPricingSnapshotEnabled := common.PricingSnapshotEnabled
	common.LogConsumeEnabled = true
	common.RedisEnabled = false
	common.PricingSnapshotEnabled = false
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	telemetryDB = db
	EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(
		&User{},
		&Token{},
		&Channel{},
		&MediaJob{},
		&ModelSellPrice{},
		&ProviderCostPrice{},
		&WalletLedger{},
		&Log{},
		&SpendBudget{},
		&SpendBudgetAlert{},
		&SpendBudgetReservation{},
		&SpendBudgetCharge{}, &ProjectSpend{},
		&SettlementFailure{},
		&LedgerLegacySettlementWaiver{},
	); err != nil {
		t.Fatalf("failed to migrate media job billing tables: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = originalDB
		telemetryDB = originalLogDB
		common.LogConsumeEnabled = originalLogConsumeEnabled
		common.RedisEnabled = originalRedisEnabled
		common.PricingSnapshotEnabled = originalPricingSnapshotEnabled
	})
	return db
}

func seedPricedVideoMediaJob(t *testing.T, db *gorm.DB) MediaJob {
	t.Helper()
	user := &User{Id: 1, Username: "media-billing-user", Status: common.UserStatusEnabled, Quota: billing.MicroUSD(1_000_000)}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	token := &Token{
		Id:          2,
		UserId:      user.Id,
		Key:         "mediajobbillingtoken",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: billing.MicroUSD(1_000_000),
		Group:       ChannelLaneDirect,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	channel := &Channel{Id: 3, Name: "video-channel", Type: 1, Key: "upstream", Status: common.ChannelStatusEnabled, Models: "veo-priced", Group: ChannelLaneDirect}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("failed to create channel: %v", err)
	}
	secondSellUSD := 0.02
	secondCostUSD := 0.01
	if err := db.Create(&ModelSellPrice{
		ModelName:      "veo-priced",
		ChannelGroup:   ChannelLaneDirect,
		SecondUSD:      &secondSellUSD,
		Active:         true,
		PricingVersion: 7,
		Source:         "test",
	}).Error; err != nil {
		t.Fatalf("failed to create sell price: %v", err)
	}
	if err := db.Create(&ProviderCostPrice{
		ModelName:      "veo-priced",
		Provider:       "gemini",
		ChannelID:      channel.Id,
		SecondUSD:      &secondCostUSD,
		PricingVersion: 7,
		Source:         "test",
	}).Error; err != nil {
		t.Fatalf("failed to create provider cost price: %v", err)
	}
	tokenID := token.Id
	channelID := channel.Id
	job := MediaJob{
		PublicID:        "mjob_billing",
		UserID:          user.Id,
		TokenID:         &tokenID,
		ModelName:       "veo-priced",
		Kind:            MediaJobKindVideo,
		Provider:        "gemini",
		ChannelID:       &channelID,
		Status:          MediaJobStatusRunning,
		RequestSnapshot: JSONValue([]byte(`{"prompt":"video","seconds":5,"size":"1280x720"}`)),
		SessionID:       "sess-media-billing",
		AgentID:         "codex",
		RequestID:       "req-media-billing",
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("failed to create media job: %v", err)
	}
	return job
}

func TestReserveMediaJobBillingPreventsDoubleSubmissionAndSettlesOnce(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	quote, err := quoteMediaJobBilling(job)
	if err != nil {
		t.Fatalf("quoteMediaJobBilling error: %v", err)
	}
	if quote.CustomerCharge <= 0 {
		t.Fatalf("expected positive video charge, got %d", quote.CustomerCharge)
	}
	if err := db.Model(&User{}).Where("id = ?", job.UserID).Update("quota", quote.CustomerCharge).Error; err != nil {
		t.Fatalf("set exact user quota: %v", err)
	}
	if err := db.Model(&Token{}).Where("id = ?", *job.TokenID).Updates(map[string]any{
		"remain_quota": quote.CustomerCharge,
		"used_quota":   0,
	}).Error; err != nil {
		t.Fatalf("set exact token quota: %v", err)
	}

	job.RequestID = "req-video-reservation-1"
	if _, err := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC()); err != nil {
		t.Fatalf("ReserveMediaJobBillingBeforeSubmit error: %v", err)
	}
	var persistedReservation MediaJob
	if err := db.First(&persistedReservation, job.ID).Error; err != nil {
		t.Fatalf("reload persisted reservation: %v", err)
	}
	if MediaJobBillingReservationAmount(persistedReservation) != quote.CustomerCharge ||
		mediaJobBillingReservationID(persistedReservation) == "" ||
		len(persistedReservation.CostSnapshot) == 0 {
		t.Fatalf("reservation was not atomically persisted on job: request=%s cost=%s", persistedReservation.RequestSnapshot, persistedReservation.CostSnapshot)
	}
	second := job
	second.RequestID = "req-video-reservation-2"
	second.RequestSnapshot = JSONValue([]byte(`{"prompt":"video","seconds":5,"size":"1280x720"}`))
	if _, err := ReserveMediaJobBillingBeforeSubmit(&second, time.Now().UTC()); err == nil {
		t.Fatal("expected second reservation to fail after the exact quota was atomically reserved")
	}

	if _, err := SettleMediaJobBilling(persistedReservation, time.Now().UTC()); err != nil {
		t.Fatalf("SettleMediaJobBilling error: %v", err)
	}
	var user User
	if err := db.Select("quota").First(&user, job.UserID).Error; err != nil {
		t.Fatalf("load user quota: %v", err)
	}
	if user.Quota != 0 {
		t.Fatalf("settlement must not double-charge reserved quota, got %d", user.Quota)
	}
	var token Token
	if err := db.Select("remain_quota", "used_quota").First(&token, *job.TokenID).Error; err != nil {
		t.Fatalf("load token quota: %v", err)
	}
	if token.RemainQuota != 0 || token.UsedQuota != quote.CustomerCharge {
		t.Fatalf("unexpected settled token quota: remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}
}

func TestReserveMediaJobBillingBlocksHeldProjectBeforeCharge(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	if err := db.AutoMigrate(&Project{}); err != nil {
		t.Fatal(err)
	}
	project := Project{ID: 7, OwnerUserID: job.UserID, Name: "Held video project", PaymentRiskHold: true}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	job.ProjectID = project.ID
	if _, err := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC()); !errors.Is(err, ErrProjectAccessSuspended) {
		t.Fatalf("held project admitted video charge: %v", err)
	}
	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).Count(&ledgerCount).Error; err != nil || ledgerCount != 0 {
		t.Fatalf("held video changed wallet: %d %v", ledgerCount, err)
	}
}

func TestReserveFreeMediaJobBillingBlocksHeldProject(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	if err := db.AutoMigrate(&Project{}); err != nil {
		t.Fatal(err)
	}
	project := Project{ID: 7, OwnerUserID: job.UserID, Name: "Held free video project", PaymentRiskHold: true}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	job.ProjectID = project.ID
	if err := db.Model(&ModelSellPrice{}).Where("model_name = ?", job.ModelName).Update("second_usd", 0).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC()); !errors.Is(err, ErrProjectAccessSuspended) {
		t.Fatalf("held project admitted free video: %v", err)
	}
	if err := db.Model(&project).Update("payment_risk_hold", false).Error; err != nil {
		t.Fatal(err)
	}
	if result, err := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC()); err != nil || result.CustomerCharge != 0 {
		t.Fatalf("released project could not submit free video: %+v %v", result, err)
	}
}

func TestReserveMediaJobBillingRollsBackQuotaWhenJobSnapshotFails(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)

	const callbackName = "test:fail_atomic_media_job_reservation_snapshot"
	if err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "MediaJob" {
			_ = tx.AddError(errors.New("forced media job snapshot failure"))
		}
	}); err != nil {
		t.Fatalf("register media job update failure: %v", err)
	}
	_, reserveErr := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC())
	if err := db.Callback().Update().Remove(callbackName); err != nil {
		t.Fatalf("remove media job update failure: %v", err)
	}
	if reserveErr == nil {
		t.Fatal("expected reservation to fail when the durable job snapshot cannot be updated")
	}

	var user User
	if err := db.Select("quota").First(&user, job.UserID).Error; err != nil {
		t.Fatalf("reload user quota: %v", err)
	}
	if user.Quota != billing.MicroUSD(1_000_000) {
		t.Fatalf("user quota changed despite rolled-back reservation: %d", user.Quota)
	}
	var token Token
	if err := db.Select("remain_quota", "used_quota").First(&token, *job.TokenID).Error; err != nil {
		t.Fatalf("reload token quota: %v", err)
	}
	if token.RemainQuota != billing.MicroUSD(1_000_000) || token.UsedQuota != 0 {
		t.Fatalf("token quota changed despite rolled-back reservation: remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}
	var reservationLedgers int64
	if err := db.Model(&WalletLedger{}).
		Where("ref_type = ?", mediaJobBillingReservationRefType).
		Count(&reservationLedgers).Error; err != nil {
		t.Fatalf("count reservation ledgers: %v", err)
	}
	if reservationLedgers != 0 {
		t.Fatalf("reservation ledger committed without the durable job snapshot: %d", reservationLedgers)
	}
	var persisted MediaJob
	if err := db.First(&persisted, job.ID).Error; err != nil {
		t.Fatalf("reload media job: %v", err)
	}
	if MediaJobBillingReservationAmount(persisted) != 0 || mediaJobBillingReservationID(persisted) != "" {
		t.Fatalf("reservation metadata committed despite transaction rollback: %s", persisted.RequestSnapshot)
	}
}

func TestReserveMediaJobBillingCommitsBlockedBudgetAlertOnly(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	if err := db.Create(&SpendBudget{
		Scope:         SpendBudgetScopeToken,
		UserID:        job.UserID,
		TokenID:       *job.TokenID,
		Period:        SpendBudgetPeriodMonth,
		LimitMicrousd: billing.MicroUSD(1),
		Enforcement:   SpendBudgetEnforcementBlock,
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("create blocking spend budget: %v", err)
	}

	_, err := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC())
	if !errors.Is(err, ErrSpendBudgetExceeded) {
		t.Fatalf("expected spend-budget block, got %v", err)
	}
	var blockedAlerts int64
	if err := db.Model(&SpendBudgetAlert{}).
		Where("alert_type = ? AND request_id = ?", SpendBudgetAlertBlocked, job.RequestID).
		Count(&blockedAlerts).Error; err != nil {
		t.Fatalf("count blocked alerts: %v", err)
	}
	if blockedAlerts != 1 {
		t.Fatalf("expected one durable blocked-attempt alert, got %d", blockedAlerts)
	}
	var reservationLedgers int64
	if err := db.Model(&WalletLedger{}).
		Where("ref_type = ?", mediaJobBillingReservationRefType).
		Count(&reservationLedgers).Error; err != nil {
		t.Fatalf("count reservation ledgers: %v", err)
	}
	if reservationLedgers != 0 {
		t.Fatalf("blocked reservation wrote a wallet ledger: %d", reservationLedgers)
	}
	var persisted MediaJob
	if err := db.First(&persisted, job.ID).Error; err != nil {
		t.Fatalf("reload blocked media job: %v", err)
	}
	if MediaJobBillingReservationAmount(persisted) != 0 {
		t.Fatalf("blocked reservation mutated the durable job: %s", persisted.RequestSnapshot)
	}
}

func TestReservedMediaJobSettlesFromSnapshotAfterPriceRemoval(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	reserved, err := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC())
	if err != nil {
		t.Fatalf("ReserveMediaJobBillingBeforeSubmit error: %v", err)
	}
	if reserved.CustomerCharge <= 0 {
		t.Fatalf("expected positive reserved charge, got %d", reserved.CustomerCharge)
	}
	if err := db.Model(&MediaJob{}).Where("id = ?", job.ID).Updates(map[string]any{
		"request_snapshot": job.RequestSnapshot,
		"cost_snapshot":    job.CostSnapshot,
	}).Error; err != nil {
		t.Fatalf("persist reservation snapshot: %v", err)
	}
	if err := db.Where("model_name = ?", job.ModelName).Delete(&ModelSellPrice{}).Error; err != nil {
		t.Fatalf("remove sell price after submission: %v", err)
	}
	var persisted MediaJob
	if err := db.First(&persisted, job.ID).Error; err != nil {
		t.Fatalf("reload reserved media job: %v", err)
	}
	settled, err := SettleMediaJobBilling(persisted, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("reserved media job should settle without live pricing: %v", err)
	}
	if settled.CustomerCharge != reserved.CustomerCharge {
		t.Fatalf("settlement changed reserved quote: reserved=%d settled=%d", reserved.CustomerCharge, settled.CustomerCharge)
	}
}

func TestCarryMediaJobBillingReservationRecomputesWinningProviderCost(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	source := seedPricedVideoMediaJob(t, db)
	reserved, err := ReserveMediaJobBillingBeforeSubmit(&source, time.Now().UTC())
	if err != nil {
		t.Fatalf("ReserveMediaJobBillingBeforeSubmit error: %v", err)
	}

	winningChannel := &Channel{
		Id:     4,
		Name:   "video-retry-winner",
		Type:   1,
		Key:    "winning-upstream",
		Status: common.ChannelStatusEnabled,
		Models: "veo-priced",
		Group:  ChannelLaneDirect,
	}
	if err := db.Create(winningChannel).Error; err != nil {
		t.Fatalf("create winning channel: %v", err)
	}
	winningSecondCostUSD := 0.004
	if err := db.Create(&ProviderCostPrice{
		ModelName:      "veo-priced",
		Provider:       "gemini",
		ChannelID:      winningChannel.Id,
		SecondUSD:      &winningSecondCostUSD,
		PricingVersion: 8,
		Source:         "test",
	}).Error; err != nil {
		t.Fatalf("create winning provider price: %v", err)
	}

	winningChannelID := winningChannel.Id
	target := source
	target.ID = 0
	target.ChannelID = &winningChannelID
	if err := CarryMediaJobBillingReservation(&target, &source); err != nil {
		t.Fatalf("CarryMediaJobBillingReservation error: %v", err)
	}
	carried := mediaJobBillingResultFromReservation(target, MediaJobBillingReservationAmount(target))
	if carried.CustomerCharge != reserved.CustomerCharge {
		t.Fatalf("customer reservation changed: before=%d after=%d", reserved.CustomerCharge, carried.CustomerCharge)
	}
	if carried.ProviderCost == reserved.ProviderCost {
		t.Fatalf("provider cost was not recomputed: source=%d target=%d", reserved.ProviderCost, carried.ProviderCost)
	}
	if carried.ProviderCost != billing.USD(winningSecondCostUSD*5) {
		t.Fatalf("unexpected winning provider cost: %d", carried.ProviderCost)
	}
	if carried.UsageFact.ChannelID != winningChannel.Id {
		t.Fatalf("provider usage remained on failed channel: %+v", carried.UsageFact)
	}
}

func TestRefundMediaJobBillingReservationIsIdempotent(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	if err := db.Create(&SpendBudget{
		Scope:         SpendBudgetScopeToken,
		UserID:        job.UserID,
		TokenID:       *job.TokenID,
		Period:        SpendBudgetPeriodMonth,
		LimitMicrousd: billing.MicroUSD(200_000),
		Enforcement:   SpendBudgetEnforcementBlock,
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("failed to create spend budget: %v", err)
	}
	job.RequestID = "req-video-refund"
	if _, err := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC()); err != nil {
		t.Fatalf("ReserveMediaJobBillingBeforeSubmit error: %v", err)
	}
	var reservationCount int64
	if err := db.Model(&SpendBudgetReservation{}).Where("request_id = ?", job.RequestID).Count(&reservationCount).Error; err != nil {
		t.Fatalf("count spend budget reservation: %v", err)
	}
	if reservationCount != 1 {
		t.Fatalf("expected one spend budget reservation before refund, got %d", reservationCount)
	}
	if err := RefundMediaJobBillingReservation(job, time.Now().UTC()); err != nil {
		t.Fatalf("RefundMediaJobBillingReservation error: %v", err)
	}
	if err := RefundMediaJobBillingReservation(job, time.Now().UTC()); err != nil {
		t.Fatalf("second RefundMediaJobBillingReservation error: %v", err)
	}
	var user User
	if err := db.Select("quota").First(&user, job.UserID).Error; err != nil {
		t.Fatalf("load user quota: %v", err)
	}
	if user.Quota != 1_000_000 {
		t.Fatalf("refunded user quota=%d", user.Quota)
	}
	var token Token
	if err := db.Select("remain_quota", "used_quota").First(&token, *job.TokenID).Error; err != nil {
		t.Fatalf("load token quota: %v", err)
	}
	if token.RemainQuota != 1_000_000 || token.UsedQuota != 0 {
		t.Fatalf("unexpected refunded token quota: remain=%d used=%d", token.RemainQuota, token.UsedQuota)
	}
	if err := db.Model(&SpendBudgetReservation{}).Where("request_id = ?", job.RequestID).Count(&reservationCount).Error; err != nil {
		t.Fatalf("count spend budget reservation after refund: %v", err)
	}
	if reservationCount != 0 {
		t.Fatalf("expected refund to release spend budget reservation, got %d", reservationCount)
	}
}

func TestRefundMediaJobBillingReservationRecoversMissingSnapshotFromLedger(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	quote, err := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC())
	if err != nil {
		t.Fatalf("ReserveMediaJobBillingBeforeSubmit error: %v", err)
	}
	legacySnapshot := JSONValue([]byte(`{"prompt":"video","seconds":5,"size":"1280x720"}`))
	if err := db.Model(&MediaJob{}).Where("id = ?", job.ID).Update("request_snapshot", legacySnapshot).Error; err != nil {
		t.Fatalf("simulate missing durable reservation snapshot: %v", err)
	}
	var missingSnapshot MediaJob
	if err := db.First(&missingSnapshot, job.ID).Error; err != nil {
		t.Fatalf("reload media job with missing snapshot: %v", err)
	}
	if MediaJobBillingReservationAmount(missingSnapshot) != 0 {
		t.Fatalf("test setup still has reservation snapshot: %s", missingSnapshot.RequestSnapshot)
	}
	if err := RefundMediaJobBillingReservation(missingSnapshot, time.Now().UTC()); err != nil {
		t.Fatalf("RefundMediaJobBillingReservation recovered from ledger error: %v", err)
	}
	var user User
	if err := db.Select("quota").First(&user, job.UserID).Error; err != nil {
		t.Fatalf("load user quota: %v", err)
	}
	if user.Quota != 1_000_000 {
		t.Fatalf("expected recovered refund to restore user quota, got %d", user.Quota)
	}
	var token Token
	if err := db.Select("remain_quota", "used_quota").First(&token, *job.TokenID).Error; err != nil {
		t.Fatalf("load token quota: %v", err)
	}
	if token.RemainQuota != 1_000_000 || token.UsedQuota != 0 {
		t.Fatalf("expected recovered refund to restore token quota after %d charge, remain=%d used=%d", quote.CustomerCharge, token.RemainQuota, token.UsedQuota)
	}
	var recovered MediaJob
	if err := db.First(&recovered, job.ID).Error; err != nil {
		t.Fatalf("reload recovered media job: %v", err)
	}
	if MediaJobBillingReservationAmount(recovered) != quote.CustomerCharge || mediaJobBillingReservationID(recovered) == "" {
		t.Fatalf("expected recovery to persist reservation back onto job, request=%s", recovered.RequestSnapshot)
	}
}

func TestRefundMediaJobBillingReservationDurablyRecordsRetry(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	job.RequestID = "req-video-refund-retry"
	if _, err := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC()); err != nil {
		t.Fatalf("ReserveMediaJobBillingBeforeSubmit error: %v", err)
	}
	const callbackName = "test:fail_media_job_refund_ledger"
	if err := db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "WalletLedger" {
			_ = tx.AddError(errors.New("forced refund ledger failure"))
		}
	}); err != nil {
		t.Fatalf("register refund failure callback: %v", err)
	}
	defer db.Callback().Create().Remove(callbackName)

	if err := RefundMediaJobBillingReservationDurably(job, time.Now().UTC()); err == nil {
		t.Fatal("expected refund failure to be returned")
	}
	var failure SettlementFailure
	if err := db.Where("request_id = ?", job.RequestID).First(&failure).Error; err != nil {
		t.Fatalf("load durable refund retry: %v", err)
	}
	if failure.Status != SettlementFailureStatusOpen ||
		failure.RefType != mediaJobBillingRefundRefType ||
		failure.QuotaDelta != -MediaJobBillingReservationAmount(job) {
		t.Fatalf("unexpected durable refund retry: %+v", failure)
	}
}

func TestRefundMediaJobBillingReservationRejectsUnverifiedSnapshot(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	job.RequestSnapshot = JSONValue(`{"_billing_reserved_quota_microusd":900000,"_billing_reservation_id":"attacker-selected"}`)

	err := RefundMediaJobBillingReservationDurably(job, time.Now().UTC())
	if !errors.Is(err, ErrMediaJobBillingReservationUnverified) {
		t.Fatalf("expected unverified reservation error, got %v", err)
	}
	var user User
	if err := db.First(&user, job.UserID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.Quota != 1_000_000 {
		t.Fatalf("unverified reservation credited user quota: %d", user.Quota)
	}
	var refundCount int64
	if err := db.Model(&WalletLedger{}).
		Where("ref_type = ?", mediaJobBillingRefundRefType).
		Count(&refundCount).Error; err != nil {
		t.Fatalf("count refund ledger: %v", err)
	}
	if refundCount != 0 {
		t.Fatalf("unverified reservation created %d refund ledger rows", refundCount)
	}
	var failureCount int64
	if err := db.Model(&SettlementFailure{}).Count(&failureCount).Error; err != nil {
		t.Fatalf("count settlement failures: %v", err)
	}
	if failureCount != 0 {
		t.Fatalf("unverified reservation created a retryable refund: %d", failureCount)
	}
}

func TestSettleMediaJobBillingRejectsUnverifiedSnapshot(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	job.RequestSnapshot = JSONValue(`{"seconds":5,"_billing_reserved_quota_microusd":100000,"_billing_reservation_id":"attacker-selected"}`)

	_, err := SettleMediaJobBilling(job, time.Now().UTC())
	if !errors.Is(err, ErrMediaJobBillingReservationUnverified) {
		t.Fatalf("expected unverified reservation error, got %v", err)
	}
	var user User
	if err := db.First(&user, job.UserID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.Quota != 1_000_000 {
		t.Fatalf("unverified reservation changed user quota: %d", user.Quota)
	}
	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).
		Where("ref_type = ?", MediaJobRefType).
		Count(&ledgerCount).Error; err != nil {
		t.Fatalf("count settlement ledgers: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("unverified reservation created %d settlement ledgers", ledgerCount)
	}
}

func TestSettleMediaJobBillingRejectsRefundedReservation(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	if _, err := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC()); err != nil {
		t.Fatalf("reserve media billing: %v", err)
	}
	if err := RefundMediaJobBillingReservation(job, time.Now().UTC()); err != nil {
		t.Fatalf("refund media billing: %v", err)
	}

	if _, err := SettleMediaJobBilling(job, time.Now().UTC()); !errors.Is(err, ErrMediaJobBillingReservationUnverified) {
		t.Fatalf("expected refunded reservation to be rejected, got %v", err)
	}
	var user User
	if err := db.First(&user, job.UserID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.Quota != 1_000_000 {
		t.Fatalf("refunded reservation changed user quota during settlement: %d", user.Quota)
	}
}

func TestSettleMediaJobBillingChargesLedgerAndLogsUsageFact(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	result, err := SettleMediaJobBilling(job, time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SettleMediaJobBilling error: %v", err)
	}
	if result.CustomerCharge != billing.MicroUSD(100_000) || result.ProviderCost != billing.MicroUSD(50_000) {
		t.Fatalf("unexpected quote: customer=%d provider=%d", result.CustomerCharge, result.ProviderCost)
	}
	var ledger WalletLedger
	if err := db.Where("ref_type = ? AND ref_id = ?", MediaJobRefType, job.PublicID).First(&ledger).Error; err != nil {
		t.Fatalf("expected wallet ledger: %v", err)
	}
	if ledger.DeltaMicrousd != billing.MicroUSD(-100_000) {
		t.Fatalf("unexpected ledger delta: %d", ledger.DeltaMicrousd)
	}
	var log Log
	if err := db.Where("request_id = ?", job.RequestID).First(&log).Error; err != nil {
		t.Fatalf("expected consume log: %v", err)
	}
	fact, ok := usagepkg.ParseFact(log.UsageFact)
	if !ok {
		t.Fatalf("expected usage fact to parse: %s", log.UsageFact)
	}
	if fact.MediaUnits == nil || fact.MediaUnits.VideoSeconds != 5 || fact.MediaUnits.ResolutionTier != "1280x720" {
		t.Fatalf("unexpected media units: %+v", fact.MediaUnits)
	}
	if dimensionQuantity(fact, usagepkg.DimensionVideoSeconds) != 5 || dimensionQuantity(fact, usagepkg.DimensionSecond) != 5 {
		t.Fatalf("expected video and billing second dimensions, got %+v", fact.Dimensions)
	}
	if fact.CustomerCostUSD == nil || *fact.CustomerCostUSD != 0.10 || fact.ProviderCostUSD == nil || *fact.ProviderCostUSD != 0.05 {
		t.Fatalf("unexpected cost fields: %+v", fact)
	}
	var user User
	if err := db.First(&user, job.UserID).Error; err != nil {
		t.Fatalf("failed to reload user: %v", err)
	}
	if user.UsedQuota != billing.MicroUSD(100_000) {
		t.Fatalf("expected media settlement to update user used quota, got used=%d", user.UsedQuota)
	}
}

func TestSettleMediaJobBillingRejectsMissingSellPrice(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	if err := db.Where("model_name = ?", job.ModelName).Delete(&ModelSellPrice{}).Error; err != nil {
		t.Fatalf("failed to remove sell price: %v", err)
	}

	_, err := SettleMediaJobBilling(job, time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrMediaJobPriceNotConfigured) {
		t.Fatalf("expected missing price error, got %v", err)
	}
	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).Where("ref_type = ? AND ref_id = ?", MediaJobRefType, job.PublicID).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("failed to count wallet ledgers: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("expected no wallet ledger for unpriced media job, got %d", ledgerCount)
	}
	var stored MediaJob
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatalf("failed to reload job: %v", err)
	}
	if stored.BillingSettledAt != 0 {
		t.Fatalf("expected unpriced job to remain unsettled, got %d", stored.BillingSettledAt)
	}
}

func TestSettleMediaJobBillingRejectsTokenOnlySellPrice(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	inputUSD := 0.10
	if err := db.Model(&ModelSellPrice{}).
		Where("model_name = ? AND channel_group = ?", job.ModelName, ChannelLaneDirect).
		Updates(map[string]any{
			"input_per_mtok": &inputUSD,
			"request_usd":    nil,
			"second_usd":     nil,
		}).Error; err != nil {
		t.Fatalf("failed to update sell price: %v", err)
	}

	_, err := SettleMediaJobBilling(job, time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrMediaJobPriceNotConfigured) {
		t.Fatalf("expected token-only price to be rejected, got %v", err)
	}
	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).Where("ref_type = ? AND ref_id = ?", MediaJobRefType, job.PublicID).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("failed to count wallet ledgers: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("expected no wallet ledger for token-only media price, got %d", ledgerCount)
	}
	var stored MediaJob
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatalf("failed to reload job: %v", err)
	}
	if stored.BillingSettledAt != 0 {
		t.Fatalf("expected token-only priced job to remain unsettled, got %d", stored.BillingSettledAt)
	}
}

func TestSettleMediaJobBillingRejectsSecondPricedVideoWithoutDuration(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	job.RequestSnapshot = JSONValue([]byte(`{"prompt":"video","artifact_url":"https://example.com/artifact.mp4"}`))
	if err := db.Model(&MediaJob{}).Where("id = ?", job.ID).Update("request_snapshot", job.RequestSnapshot).Error; err != nil {
		t.Fatalf("failed to update request snapshot: %v", err)
	}

	_, err := SettleMediaJobBilling(job, time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrMediaJobDurationRequired) {
		t.Fatalf("expected duration required error, got %v", err)
	}
	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).Where("ref_type = ? AND ref_id = ?", MediaJobRefType, job.PublicID).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("failed to count wallet ledgers: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("expected no wallet ledger for duration-less second-priced media job, got %d", ledgerCount)
	}
	var stored MediaJob
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatalf("failed to reload job: %v", err)
	}
	if stored.BillingSettledAt != 0 {
		t.Fatalf("expected duration-less job to remain unsettled, got %d", stored.BillingSettledAt)
	}
}

func TestSettleMediaJobBillingSettlesExplicitFreeMediaJob(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	free := 0.0
	if err := db.Model(&ModelSellPrice{}).
		Where("model_name = ? AND channel_group = ?", job.ModelName, ChannelLaneDirect).
		Updates(map[string]any{
			"request_usd": &free,
			"second_usd":  nil,
		}).Error; err != nil {
		t.Fatalf("failed to update sell price: %v", err)
	}

	result, err := SettleMediaJobBilling(job, time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SettleMediaJobBilling error: %v", err)
	}
	if !result.CustomerPriceConfigured || result.CustomerCharge != 0 {
		t.Fatalf("expected explicit free customer price, got configured=%t charge=%d", result.CustomerPriceConfigured, result.CustomerCharge)
	}
	var stored MediaJob
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatalf("failed to reload job: %v", err)
	}
	if stored.BillingSettledAt == 0 {
		t.Fatal("expected free media job to be marked settled")
	}
	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).Where("ref_type = ? AND ref_id = ?", MediaJobRefType, job.PublicID).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("failed to count ledgers: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("expected no wallet ledger for free media job, got %d", ledgerCount)
	}
	var log Log
	if err := db.Where("request_id = ?", job.RequestID).First(&log).Error; err != nil {
		t.Fatalf("expected zero-cost consume log: %v", err)
	}
	if log.AmountMicrousd != 0 {
		t.Fatalf("expected zero-cost consume log, got %d", log.AmountMicrousd)
	}
	var user User
	if err := db.First(&user, job.UserID).Error; err != nil {
		t.Fatalf("failed to reload user: %v", err)
	}
	if user.UsedQuota != 0 {
		t.Fatalf("expected free media job not to add used quota, got used=%d", user.UsedQuota)
	}
}

func TestSettleMediaJobBillingChargesRequestPricedVideoWithDuration(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	requestUSD := 0.03
	if err := db.Model(&ModelSellPrice{}).
		Where("model_name = ? AND channel_group = ?", job.ModelName, ChannelLaneDirect).
		Updates(map[string]any{
			"request_usd": &requestUSD,
			"second_usd":  nil,
		}).Error; err != nil {
		t.Fatalf("failed to update sell price: %v", err)
	}

	result, err := SettleMediaJobBilling(job, time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SettleMediaJobBilling error: %v", err)
	}
	if result.CustomerCharge != billing.MicroUSD(30_000) {
		t.Fatalf("expected request-priced video charge 30000 microusd, got %d", result.CustomerCharge)
	}
	if dimensionQuantity(result.UsageFact, usagepkg.DimensionRequest) != 1 {
		t.Fatalf("expected request dimension for request-priced video, got %+v", result.UsageFact.Dimensions)
	}
	if dimensionQuantity(result.UsageFact, usagepkg.DimensionSecond) != 5 {
		t.Fatalf("expected video second telemetry to remain, got %+v", result.UsageFact.Dimensions)
	}
}

func TestSettleMediaJobBillingUsesSnapshotFinalGroup(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	if err := db.Model(&Channel{}).Where("id = ?", *job.ChannelID).Update("group", "direct,cheap").Error; err != nil {
		t.Fatalf("failed to update channel group: %v", err)
	}
	cheapSecondUSD := 0.005
	if err := db.Create(&ModelSellPrice{
		ModelName:      job.ModelName,
		ChannelGroup:   ChannelLaneAgent,
		SecondUSD:      &cheapSecondUSD,
		Active:         true,
		PricingVersion: 8,
		Source:         "test",
	}).Error; err != nil {
		t.Fatalf("failed to create cheap sell price: %v", err)
	}
	job.RequestSnapshot = JSONValue([]byte(`{"prompt":"video","seconds":5,"size":"1280x720","final_group":"cheap"}`))
	if err := db.Model(&MediaJob{}).Where("id = ?", job.ID).Update("request_snapshot", job.RequestSnapshot).Error; err != nil {
		t.Fatalf("failed to update request snapshot: %v", err)
	}

	result, err := SettleMediaJobBilling(job, time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("SettleMediaJobBilling error: %v", err)
	}
	if result.ChannelGroup != ChannelLaneAgent {
		t.Fatalf("expected agent lane billing, got %q", result.ChannelGroup)
	}
	if result.CustomerCharge != billing.MicroUSD(25_000) {
		t.Fatalf("expected agent lane charge 25000 microusd, got %d", result.CustomerCharge)
	}
	var log Log
	if err := db.Where("request_id = ?", job.RequestID).First(&log).Error; err != nil {
		t.Fatalf("expected consume log: %v", err)
	}
	if log.Group != ChannelLaneAgent {
		t.Fatalf("expected consume log group cheap, got %q", log.Group)
	}
}

func TestSettleMediaJobBillingIsIdempotent(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if _, err := SettleMediaJobBilling(job, now); err != nil {
		t.Fatalf("first SettleMediaJobBilling error: %v", err)
	}
	if _, err := SettleMediaJobBilling(job, now.Add(time.Minute)); err != nil {
		t.Fatalf("second SettleMediaJobBilling error: %v", err)
	}

	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).Where("ref_type = ? AND ref_id = ?", MediaJobRefType, job.PublicID).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("failed to count ledgers: %v", err)
	}
	if ledgerCount != 1 {
		t.Fatalf("expected one ledger row after repeated settlement, got %d", ledgerCount)
	}
	var logCount int64
	if err := db.Model(&Log{}).Where("request_id = ? AND type = ?", job.RequestID, LogTypeConsume).Count(&logCount).Error; err != nil {
		t.Fatalf("failed to count consume logs: %v", err)
	}
	if logCount != 1 {
		t.Fatalf("expected one consume log after repeated settlement, got %d", logCount)
	}
	var loaded MediaJob
	if err := db.First(&loaded, job.ID).Error; err != nil {
		t.Fatalf("failed to reload media job: %v", err)
	}
	if loaded.BillingSettledAt == 0 {
		t.Fatal("expected media job billing_settled_at to be set")
	}
	var user User
	if err := db.First(&user, job.UserID).Error; err != nil {
		t.Fatalf("failed to reload user: %v", err)
	}
	if user.UsedQuota != billing.MicroUSD(100_000) {
		t.Fatalf("expected repeated settlement not to double count user usage, got used=%d", user.UsedQuota)
	}
}

func TestSettleMediaJobBillingKeepsIdempotencyAfterPriceRemoval(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if _, err := SettleMediaJobBilling(job, now); err != nil {
		t.Fatalf("first SettleMediaJobBilling error: %v", err)
	}
	if err := db.Where("model_name = ?", job.ModelName).Delete(&ModelSellPrice{}).Error; err != nil {
		t.Fatalf("failed to remove sell price: %v", err)
	}
	if _, err := SettleMediaJobBilling(job, now.Add(time.Minute)); err != nil {
		t.Fatalf("settled media job should remain idempotent after price removal, got %v", err)
	}
	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).Where("ref_type = ? AND ref_id = ?", MediaJobRefType, job.PublicID).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("failed to count ledgers: %v", err)
	}
	if ledgerCount != 1 {
		t.Fatalf("expected one ledger row after repeated settlement, got %d", ledgerCount)
	}
}

func TestSettleMediaJobBillingMarksExistingLedgerAsSettled(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&WalletLedger{
		UserID:         job.UserID,
		DeltaMicrousd:  billing.MicroUSD(-100_000),
		Kind:           WalletLedgerKindSpend,
		RefType:        MediaJobRefType,
		RefID:          job.PublicID,
		PricingVersion: 7,
		CreatedAt:      now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("failed to seed existing ledger: %v", err)
	}

	if _, err := SettleMediaJobBilling(job, now); err != nil {
		t.Fatalf("SettleMediaJobBilling should treat existing ledger as settled, got %v", err)
	}

	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).Where("ref_type = ? AND ref_id = ?", MediaJobRefType, job.PublicID).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("failed to count ledgers: %v", err)
	}
	if ledgerCount != 1 {
		t.Fatalf("expected existing ledger to be reused, got %d rows", ledgerCount)
	}
	var loaded MediaJob
	if err := db.First(&loaded, job.ID).Error; err != nil {
		t.Fatalf("failed to reload media job: %v", err)
	}
	if loaded.BillingSettledAt == 0 {
		t.Fatal("expected media job to be marked settled after existing ledger")
	}
}

func TestSettleMediaJobBillingHonorsSpendBudget(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	if err := db.Create(&SpendBudget{
		Scope:         SpendBudgetScopeToken,
		UserID:        job.UserID,
		TokenID:       *job.TokenID,
		Period:        SpendBudgetPeriodMonth,
		LimitMicrousd: billing.MicroUSD(50_000),
		Enforcement:   SpendBudgetEnforcementBlock,
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("failed to create spend budget: %v", err)
	}
	_, err := SettleMediaJobBilling(job, time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrSpendBudgetExceeded) {
		t.Fatalf("expected spend budget error, got %v", err)
	}
	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).Where("ref_type = ?", MediaJobRefType).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("failed to count ledger rows: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("expected no ledger charge when budget blocks, got %d", ledgerCount)
	}
}

func TestReservedMediaJobSettlementSurvivesBudgetReduction(t *testing.T) {
	for _, scope := range []string{"platform", "project", "member", "key"} {
		t.Run(scope, func(t *testing.T) {
			db := setupMediaJobBillingTestDB(t)
			job := seedPricedVideoMediaJob(t, db)
			if err := db.AutoMigrate(&Project{}); err != nil {
				t.Fatal(err)
			}
			project := Project{ID: 7, Name: "Settlement", OwnerUserID: job.UserID, PublicID: "prj-settlement"}
			if err := db.Create(&project).Error; err != nil {
				t.Fatal(err)
			}
			job.ProjectID = project.ID
			if err := db.Model(&job).Update("project_id", job.ProjectID).Error; err != nil {
				t.Fatal(err)
			}
			budget := SpendBudget{ProjectID: project.ID, Scope: SpendBudgetScopeOrg, Period: SpendBudgetPeriodMonth, LimitMicrousd: 1_000_000, Enforcement: SpendBudgetEnforcementBlock, Enabled: true}
			switch scope {
			case "platform":
				budget.ProjectID = 0
			case "member":
				budget.Scope, budget.UserID = SpendBudgetScopeUser, job.UserID
			case "key":
				budget.Scope, budget.TokenID = SpendBudgetScopeToken, *job.TokenID
			}
			if err := db.Create(&budget).Error; err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
			if _, err := ReserveMediaJobBillingBeforeSubmit(&job, now.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			reserved := MediaJobBillingReservationAmount(job)
			if reserved <= 50_000 {
				t.Fatalf("expected reservation above reduced cap: %d", reserved)
			}
			if err := db.Model(&budget).Update("limit_microusd", 50_000).Error; err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err := SettleMediaJobBilling(job, now); err != nil {
					t.Fatalf("accepted settlement attempt %d: %v", i, err)
				}
			}
			var user User
			if err := db.First(&user, job.UserID).Error; err != nil {
				t.Fatal(err)
			}
			if user.Quota != 1_000_000-reserved {
				t.Fatalf("settlement double charged wallet: %d", user.Quota)
			}
			var token Token
			if err := db.First(&token, *job.TokenID).Error; err != nil {
				t.Fatal(err)
			}
			if token.RemainQuota != 1_000_000-reserved || token.UsedQuota != reserved {
				t.Fatalf("settlement double charged key: %+v", token)
			}
			var count int64
			if err := db.Model(&SpendBudgetCharge{}).Where("budget_id = ?", budget.ID).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("expected one budget charge, got %d", count)
			}
			if err := db.Model(&SpendBudgetReservation{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("reservation not released: %d", count)
			}
			if err := db.Model(&ProjectSpend{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("expected one project spend, got %d", count)
			}
			var settled MediaJob
			if err := db.First(&settled, job.ID).Error; err != nil {
				t.Fatal(err)
			}
			if settled.BillingSettledAt == 0 {
				t.Fatal("job not settled")
			}
			err := CheckSpendBudgetBeforeCharge(SpendBudgetChargeInput{ProjectID: project.ID, UserID: job.UserID, ActorUserID: job.UserID, TokenID: *job.TokenID, RequestID: "new-request", DeltaMicrousd: 1, OccurredAt: now})
			if !errors.Is(err, ErrSpendBudgetExceeded) {
				t.Fatalf("new admission bypassed reduced cap: %v", err)
			}
		})
	}
}

func TestSettleMediaJobBillingRollsBackWhenSpendBudgetChargeFails(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	if err := db.Create(&SpendBudget{
		Scope:         SpendBudgetScopeToken,
		UserID:        job.UserID,
		TokenID:       *job.TokenID,
		Period:        SpendBudgetPeriodMonth,
		LimitMicrousd: billing.MicroUSD(200_000),
		Enforcement:   SpendBudgetEnforcementBlock,
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("failed to create spend budget: %v", err)
	}
	const callbackName = "test:fail_media_job_spend_budget_charge"
	if err := db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "SpendBudgetCharge" {
			_ = tx.AddError(errors.New("forced spend budget charge failure"))
		}
	}); err != nil {
		t.Fatalf("register spend budget charge failure callback: %v", err)
	}
	defer db.Callback().Create().Remove(callbackName)

	if _, err := SettleMediaJobBilling(job, time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("expected settlement to fail when spend budget charge cannot be recorded")
	}
	var ledgerCount int64
	if err := db.Model(&WalletLedger{}).Where("ref_type = ?", MediaJobRefType).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("failed to count media ledgers: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("wallet settlement committed without spend budget charge: %d", ledgerCount)
	}
	var stored MediaJob
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatalf("reload media job: %v", err)
	}
	if stored.BillingSettledAt != 0 {
		t.Fatalf("media job was marked settled despite charge failure: %+v", stored)
	}
}

func TestCheckMediaJobBillingBeforeArtifactFetchReservesBudget(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	if err := db.Create(&SpendBudget{
		Scope:         SpendBudgetScopeToken,
		UserID:        job.UserID,
		TokenID:       *job.TokenID,
		Period:        SpendBudgetPeriodMonth,
		LimitMicrousd: billing.MicroUSD(200_000),
		Enforcement:   SpendBudgetEnforcementBlock,
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("failed to create spend budget: %v", err)
	}
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := CheckMediaJobBillingBeforeArtifactFetch(job, now); err != nil {
		t.Fatalf("CheckMediaJobBillingBeforeArtifactFetch error: %v", err)
	}
	var reservation SpendBudgetReservation
	if err := db.Where("request_id = ?", job.RequestID).First(&reservation).Error; err != nil {
		t.Fatalf("expected media precheck reservation: %v", err)
	}
	if reservation.ReservedMicrousd != billing.MicroUSD(100_000) {
		t.Fatalf("expected reservation for quoted charge, got %+v", reservation)
	}
	if _, err := SettleMediaJobBilling(job, now.Add(time.Minute)); err != nil {
		t.Fatalf("SettleMediaJobBilling error: %v", err)
	}
	var reservationCount int64
	if err := db.Model(&SpendBudgetReservation{}).Where("request_id = ?", job.RequestID).Count(&reservationCount).Error; err != nil {
		t.Fatalf("failed to count reservation after settlement: %v", err)
	}
	if reservationCount != 0 {
		t.Fatalf("expected settlement to release media reservation, got %d", reservationCount)
	}
}

func TestMediaJobUsageFactUsesJobSource(t *testing.T) {
	fact := mediaJobUsageFact(MediaJob{
		PublicID:        "mjob_extension_usage",
		UserID:          1,
		Source:          testExtensionMediaSource,
		ModelName:       "imagen-priced",
		Kind:            MediaJobKindImage,
		RequestSnapshot: JSONValue([]byte(`{"n":2,"size":"1024x1024"}`)),
	}, 0)
	if fact.Source != testExtensionMediaSource {
		t.Fatalf("expected the extension source in usage fact, got %q", fact.Source)
	}
	if fact.MediaUnits == nil || fact.MediaUnits.ImageCount != 2 {
		t.Fatalf("unexpected media units: %+v", fact.MediaUnits)
	}
}

func TestMediaJobUsageFactReadsNestedParameters(t *testing.T) {
	fact := mediaJobUsageFact(MediaJob{
		PublicID:        "mjob_extension_video_usage",
		UserID:          1,
		Source:          testExtensionMediaSource,
		ModelName:       "veo-priced",
		Kind:            MediaJobKindVideo,
		RequestSnapshot: JSONValue([]byte(`{"prompt":"video","parameters":{"seconds":6,"size":"1280x720","quality":"hd"}}`)),
	}, 0)
	if fact.MediaUnits == nil {
		t.Fatal("expected media units")
	}
	if fact.MediaUnits.VideoSeconds != 6 {
		t.Fatalf("expected nested seconds to become video seconds, got %+v", fact.MediaUnits)
	}
	if fact.MediaUnits.ResolutionTier != "hd" {
		t.Fatalf("expected nested quality to be used for resolution tier, got %+v", fact.MediaUnits)
	}
	if dimensionQuantity(fact, usagepkg.DimensionSecond) != 6 {
		t.Fatalf("expected billing second dimension from nested parameters, got %+v", fact.Dimensions)
	}
}

func TestMediaJobUsageFactReadsEffectiveGeminiVideoParameters(t *testing.T) {
	fact := mediaJobUsageFact(MediaJob{
		PublicID:        "mjob_gemini_video_usage",
		UserID:          1,
		ModelName:       "veo-priced",
		Kind:            MediaJobKindVideo,
		RequestSnapshot: JSONValue([]byte(`{"parameters":{"durationSeconds":8,"aspectRatio":"16:9","resolution":"1080p"}}`)),
	}, 0)
	if fact.MediaUnits == nil {
		t.Fatal("expected media units")
	}
	if fact.MediaUnits.VideoSeconds != 8 {
		t.Fatalf("expected effective Gemini duration, got %+v", fact.MediaUnits)
	}
	if fact.MediaUnits.ResolutionTier != "1080p" {
		t.Fatalf("expected effective Gemini resolution, got %+v", fact.MediaUnits)
	}
}

func TestProjectMediaReservationKeepsAdmissionMonth(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	admitted := time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC)
	job.RequestSnapshot = JSONValue([]byte(`{"seconds":5,"_billing_budget_occurred_at":"2020-01-01T00:00:00Z"}`))
	if !mediaJobBudgetOccurredAt(job, admitted).Equal(admitted) {
		t.Fatal("unreserved request supplied accounting time")
	}
	budget := SpendBudget{Scope: SpendBudgetScopeToken, UserID: job.UserID, TokenID: *job.TokenID, Period: SpendBudgetPeriodMonth, LimitMicrousd: 1_000_000, Enabled: true, Enforcement: SpendBudgetEnforcementBlock}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveMediaJobBillingBeforeSubmit(&job, admitted); err != nil {
		t.Fatal(err)
	}
	var persisted MediaJob
	if err := db.First(&persisted, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !mediaJobBudgetOccurredAt(persisted, time.Time{}).Equal(admitted) {
		t.Fatal("admission timestamp was not persisted")
	}
	if _, err := SettleMediaJobBilling(persisted, admitted.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var charge SpendBudgetCharge
	if err := db.Where("budget_id = ?", budget.ID).First(&charge).Error; err != nil {
		t.Fatal(err)
	}
	if charge.PeriodStart != time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("media charged completion month: %+v", charge)
	}
	var count int64
	db.Model(&SpendBudgetReservation{}).Where("request_id = ?", mediaJobSpendBudgetRequestID(job)).Count(&count)
	if count != 0 {
		t.Fatal("reservation remained after settlement")
	}
	target := job
	target.RequestSnapshot = JSONValue([]byte(`{"prompt":"video","seconds":5,"size":"1280x720"}`))
	if err := CarryMediaJobBillingReservation(&target, &persisted); err != nil {
		t.Fatal(err)
	}
	if !mediaJobBudgetOccurredAt(target, time.Time{}).Equal(admitted) {
		t.Fatal("handoff lost admission timestamp")
	}
}

// The hold a video job takes at admission is the one that has to survive: the
// job can sit pending for hours, and if that hold lapses another request can
// spend the budget it was admitted against. Mark it the same way as the later
// artifact and settlement holds.
func TestReserveMediaJobBillingTakesALongRunningHold(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	if err := db.AutoMigrate(&Project{}, &ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	job := seedPricedVideoMediaJob(t, db)
	// A hold only exists where a budget does, so give the payer a project one.
	// Creating the user already provisioned their default project.
	var project Project
	if err := db.Where("owner_user_id = ?", job.UserID).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	limit := billing.MicroUSD(10_000_000)
	if err := db.Create(&SpendBudget{ProjectID: project.ID, Scope: SpendBudgetScopeUser, UserID: job.UserID, Period: SpendBudgetPeriodMonth, LimitMicrousd: limit, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&MediaJob{}).Where("id = ?", job.ID).Update("project_id", project.ID).Error; err != nil {
		t.Fatal(err)
	}
	job.ProjectID = project.ID
	job.RequestID = "req-video-admission-hold"
	now := time.Now().UTC()
	if _, err := ReserveMediaJobBillingBeforeSubmit(&job, now); err != nil {
		t.Fatalf("ReserveMediaJobBillingBeforeSubmit error: %v", err)
	}
	var holds []SpendBudgetReservation
	if err := db.Where("request_id = ?", mediaJobSpendBudgetRequestID(job)).Find(&holds).Error; err != nil {
		t.Fatal(err)
	}
	if len(holds) == 0 {
		t.Fatal("admission took no budget hold")
	}
	floor := now.Add(16 * time.Hour).Unix()
	for _, hold := range holds {
		if hold.ExpiresAt <= floor {
			t.Fatalf("admission hold expires before the job can finish: %+v", hold)
		}
	}
}

// A settled job must not leave its hold standing. Releasing it after the
// transaction commits means a process that dies in between, or a cleanup query
// that fails, leaves the cap counting the job twice for the hold's whole
// lifetime — a day, now that a job's hold is sized for a job.
func TestMediaJobSettlementReleasesItsHoldInTheSameTransaction(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	if err := db.AutoMigrate(&Project{}, &ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	job := seedPricedVideoMediaJob(t, db)
	var project Project
	if err := db.Where("owner_user_id = ?", job.UserID).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	limit := billing.MicroUSD(10_000_000)
	if err := db.Create(&SpendBudget{ProjectID: project.ID, Scope: SpendBudgetScopeUser, UserID: job.UserID, Period: SpendBudgetPeriodMonth, LimitMicrousd: limit, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&MediaJob{}).Where("id = ?", job.ID).Update("project_id", project.ID).Error; err != nil {
		t.Fatal(err)
	}
	job.ProjectID = project.ID
	job.RequestID = "req-video-settlement-release"
	now := time.Now().UTC()
	if _, err := ReserveMediaJobBillingBeforeSubmit(&job, now); err != nil {
		t.Fatalf("ReserveMediaJobBillingBeforeSubmit error: %v", err)
	}
	var held int64
	if err := db.Model(&SpendBudgetReservation{}).Where("request_id = ?", mediaJobSpendBudgetRequestID(job)).Count(&held).Error; err != nil {
		t.Fatal(err)
	}
	if held == 0 {
		t.Fatal("admission took no hold to release")
	}
	// Settle inside a transaction of our own and inspect before the deferred
	// post-commit cleanup could ever run.
	if _, err := SettleMediaJobBilling(job, now); err != nil {
		t.Fatalf("SettleMediaJobBilling error: %v", err)
	}
	var remaining int64
	if err := db.Model(&SpendBudgetReservation{}).Where("request_id = ?", mediaJobSpendBudgetRequestID(job)).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("settlement left %d hold(s) for the cap to double count", remaining)
	}
}

func TestReservedVideoJobRepricesOnProviderReportedFacts(t *testing.T) {
	db := setupMediaJobBillingTestDB(t)
	job := seedPricedVideoMediaJob(t, db)
	reserved, err := ReserveMediaJobBillingBeforeSubmit(&job, time.Now().UTC())
	if err != nil {
		t.Fatalf("ReserveMediaJobBillingBeforeSubmit error: %v", err)
	}
	if reserved.CustomerCharge != billing.USD(0.10) {
		t.Fatalf("five seconds at $0.02 should hold $0.10, got %d", reserved.CustomerCharge)
	}
	// The provider delivered a ten-second clip and said so.
	snapshot, err := SnapshotWithVideoFacts(job.RequestSnapshot, MediaJobProviderFactsSnapshotKey, dto.VideoFacts{
		Seconds: 10, Resolution: "720p", FPS: 24, Reported: true,
	})
	if err != nil {
		t.Fatalf("SnapshotWithVideoFacts error: %v", err)
	}
	if err := db.Model(&MediaJob{}).Where("id = ?", job.ID).Updates(map[string]any{
		"request_snapshot": snapshot,
		"cost_snapshot":    job.CostSnapshot,
	}).Error; err != nil {
		t.Fatalf("persist reported facts: %v", err)
	}
	var persisted MediaJob
	if err := db.First(&persisted, job.ID).Error; err != nil {
		t.Fatalf("reload media job: %v", err)
	}
	settled, err := SettleMediaJobBilling(persisted, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("SettleMediaJobBilling error: %v", err)
	}
	if settled.CustomerCharge != billing.USD(0.20) {
		t.Fatalf("settlement should follow the reported ten seconds: got %d", settled.CustomerCharge)
	}
	if settled.UsageFact.MediaUnits == nil || settled.UsageFact.MediaUnits.VideoSeconds != 10 {
		t.Fatalf("usage fact should carry the reported duration: %+v", settled.UsageFact.MediaUnits)
	}
	var user User
	if err := db.First(&user, job.UserID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.Quota != billing.MicroUSD(1_000_000)-billing.USD(0.20) {
		t.Fatalf("the hold plus the delta should leave the user charged $0.20: quota=%d", user.Quota)
	}
	public := persisted.ToDTO(false)
	if public.Usage == nil || public.Usage.Seconds != 10 || public.Usage.Resolution != "720p" {
		t.Fatalf("public job usage should expose the reported facts: %+v", public.Usage)
	}
}
