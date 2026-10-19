package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func setupSpendBudgetServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.CoreDB()
	previousLogDB := model.ConfiguredTelemetryDB()
	previousRedis := common.RedisEnabled
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	common.RedisEnabled = false
	model.EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.WalletLedger{},
		&model.SpendBudget{},
		&model.SpendBudgetAlert{},
		&model.SpendBudgetReservation{},
		&model.SpendBudgetCharge{}, &model.ProjectSpend{},
		&model.Log{},
	); err != nil {
		t.Fatalf("failed to migrate spend budget service tables: %v", err)
	}
	if err := db.AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		waitTokenUsageAlerts()
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		model.SetTelemetryDB(previousLogDB)
		common.RedisEnabled = previousRedis
	})
	return db
}

func TestPreConsumeQuotaBlocksWhenSpendBudgetWouldBeExceeded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.Create(&model.User{Id: 1, Username: "budget-user", Quota: billing.MicroUSD(10_000)}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: billing.MicroUSD(10_000)}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if err := db.Create(&model.SpendBudget{
		Scope:               model.SpendBudgetScopeToken,
		TokenID:             2,
		Period:              model.SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(500),
		WarningThresholdPct: 80,
		Enforcement:         model.SpendBudgetEnforcementBlock,
		Enabled:             true,
	}).Error; err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}

	c, _ := gin.CreateTestContext(nil)
	c.Set("token_quota", int64(10_000))
	err := PreConsumeQuota(c, billing.MicroUSD(600), &relaycommon.RelayInfo{
		RequestID:       "req-service-block",
		UserId:          1,
		TokenId:         2,
		TokenKey:        "token-key",
		SessionID:       "sess-service-block",
		AgentID:         "codex",
		OriginModelName: "gpt-test",
		UsingGroup:      model.ChannelLaneDirect,
		PriceData: types.PriceData{
			PricingVersion: 1,
		},
	})
	if err == nil {
		t.Fatalf("expected spend budget block error")
	}
	if err.GetErrorCode() != types.ErrorCodeInsufficientQuota || err.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected insufficient quota 429, got code=%s status=%d error=%v", err.GetErrorCode(), err.StatusCode, err)
	}

	var user model.User
	if loadErr := db.Select("quota").First(&user, 1).Error; loadErr != nil {
		t.Fatalf("failed to load user: %v", loadErr)
	}
	if user.Quota != billing.MicroUSD(10_000) {
		t.Fatalf("budget block must not debit user quota, got %d", user.Quota)
	}
	var alert model.SpendBudgetAlert
	if loadErr := db.Where("alert_type = ?", model.SpendBudgetAlertBlocked).First(&alert).Error; loadErr != nil {
		t.Fatalf("failed to load blocked alert: %v", loadErr)
	}
	if alert.SessionID != "sess-service-block" || alert.RequestID != "req-service-block" || alert.TokenID != 2 {
		t.Fatalf("blocked alert lost attribution: %+v", alert)
	}
}

func TestReturnPreConsumedQuotaReleasesSpendBudgetReservation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.Create(&model.User{Id: 1, Username: "budget-user", Quota: billing.MicroUSD(10_000)}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: billing.MicroUSD(10_000)}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if err := db.Create(&model.SpendBudget{
		Scope:               model.SpendBudgetScopeToken,
		TokenID:             2,
		Period:              model.SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(1_000),
		WarningThresholdPct: 80,
		Enforcement:         model.SpendBudgetEnforcementBlock,
		Enabled:             true,
	}).Error; err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}

	relayInfo := &relaycommon.RelayInfo{
		RequestID:       "req-service-release",
		UserId:          1,
		TokenId:         2,
		TokenKey:        "token-key",
		SessionID:       "sess-service-release",
		AgentID:         "codex",
		OriginModelName: "gpt-test",
		UsingGroup:      model.ChannelLaneDirect,
	}
	if err := model.CheckSpendBudgetBeforeCharge(spendBudgetChargeInputFromRelay(relayInfo, billing.MicroUSD(400))); err != nil {
		t.Fatalf("failed to reserve spend budget: %v", err)
	}
	var count int64
	if err := db.Model(&model.SpendBudgetReservation{}).Where("request_id = ?", relayInfo.RequestID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count reservation: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one spend budget reservation, got %d", count)
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ReturnPreConsumedQuota(c, relayInfo)

	if err := db.Model(&model.SpendBudgetReservation{}).Where("request_id = ?", relayInfo.RequestID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count reservation after return: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected spend budget reservation to be released, got %d", count)
	}
}

func TestReturnPreConsumedQuotaSurvivesCanceledRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.Create(&model.User{Id: 1, Username: "canceled-user", Quota: billing.MicroUSD(9_900)}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{
		Id: 2, UserId: 1, Key: "canceled-token", RemainQuota: billing.MicroUSD(9_900), UsedQuota: billing.MicroUSD(100),
	}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	reqCtx, cancel := context.WithCancel(req.Context())
	cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req.WithContext(reqCtx)
	relayInfo := &relaycommon.RelayInfo{
		RequestID: "req-canceled-refund", UserId: 1, TokenId: 2, TokenKey: "canceled-token",
		OriginModelName: "gpt-test", UsingGroup: model.ChannelLaneDirect,
		FinalPreConsumedQuota: billing.MicroUSD(100),
		PriceData:             types.PriceData{PricingVersion: 1},
	}

	ReturnPreConsumedQuota(c, relayInfo)
	deadline := time.Now().Add(2 * time.Second)
	for {
		var user model.User
		var token model.Token
		if err := db.Select("quota").First(&user, 1).Error; err != nil {
			t.Fatalf("load refunded user: %v", err)
		}
		if err := db.Select("remain_quota", "used_quota").First(&token, 2).Error; err != nil {
			t.Fatalf("load refunded token: %v", err)
		}
		if user.Quota == billing.MicroUSD(10_000) && token.RemainQuota == billing.MicroUSD(10_000) && token.UsedQuota == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("refund did not settle after cancellation: user=%d token_remain=%d token_used=%d", user.Quota, token.RemainQuota, token.UsedQuota)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReturnSearchPreConsumedQuotaReleasesTrustedSpendBudgetReservation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.Create(&model.User{Id: 1, Username: "search-budget-user", Quota: billing.MicroUSD(10_000)}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: billing.MicroUSD(10_000)}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if err := db.Create(&model.SpendBudget{
		Scope:               model.SpendBudgetScopeToken,
		TokenID:             2,
		Period:              model.SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(1_000),
		WarningThresholdPct: 80,
		Enforcement:         model.SpendBudgetEnforcementBlock,
		Enabled:             true,
	}).Error; err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}

	relayInfo := &relaycommon.RelayInfo{
		RequestID:       "req-search-trusted-release",
		UserId:          1,
		TokenId:         2,
		TokenKey:        "token-key",
		SessionID:       "sess-search-trusted-release",
		AgentID:         "codex",
		OriginModelName: "web_search",
		UsingGroup:      model.ChannelLaneDirect,
	}
	if err := model.CheckSpendBudgetBeforeCharge(spendBudgetChargeInputFromRelay(relayInfo, billing.MicroUSD(400))); err != nil {
		t.Fatalf("failed to reserve spend budget: %v", err)
	}

	var count int64
	if err := db.Model(&model.SpendBudgetReservation{}).Where("request_id = ?", relayInfo.RequestID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count reservation: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one spend budget reservation, got %d", count)
	}

	returnSearchPreConsumedQuota(relayInfo)

	if err := db.Model(&model.SpendBudgetReservation{}).Where("request_id = ?", relayInfo.RequestID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count reservation after search return: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected trusted search reservation to be released, got %d", count)
	}
}

func TestPostConsumeQuotaRecordsFullPreConsumedSpendBudgetCharge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.Create(&model.User{Id: 1, Username: "budget-user", Quota: billing.MicroUSD(10_000)}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: billing.MicroUSD(10_000)}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if err := db.Create(&model.SpendBudget{
		Scope:               model.SpendBudgetScopeToken,
		TokenID:             2,
		Period:              model.SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(1_000),
		WarningThresholdPct: 80,
		Enforcement:         model.SpendBudgetEnforcementBlock,
		Enabled:             true,
	}).Error; err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}

	c, _ := gin.CreateTestContext(nil)
	c.Set("token_quota", int64(10_000))
	relayInfo := &relaycommon.RelayInfo{
		RequestID:       "req-service-full-charge",
		UserId:          1,
		TokenId:         2,
		TokenKey:        "token-key",
		SessionID:       "sess-service-full-charge",
		AgentID:         "codex",
		OriginModelName: "gpt-test",
		UsingGroup:      model.ChannelLaneDirect,
		PriceData: types.PriceData{
			PricingVersion: 1,
		},
	}
	if err := PreConsumeQuota(c, billing.MicroUSD(500), relayInfo); err != nil {
		t.Fatalf("PreConsumeQuota returned error: %v", err)
	}
	var alertsBefore int64
	if err := db.Model(&model.SpendBudgetAlert{}).Count(&alertsBefore).Error; err != nil {
		t.Fatalf("failed to count pre-settlement alerts: %v", err)
	}
	if alertsBefore != 0 {
		t.Fatalf("expected pre-consume admission to avoid threshold alerts, got %d", alertsBefore)
	}

	if err := PostConsumeQuota(relayInfo, billing.MicroUSD(600), relayInfo.FinalPreConsumedQuota, false); err != nil {
		t.Fatalf("PostConsumeQuota returned error: %v", err)
	}
	var reservationCount int64
	if err := db.Model(&model.SpendBudgetReservation{}).Where("request_id = ?", relayInfo.RequestID).Count(&reservationCount).Error; err != nil {
		t.Fatalf("failed to count reservation after settlement: %v", err)
	}
	if reservationCount != 0 {
		t.Fatalf("expected settlement to release spend budget reservation, got %d", reservationCount)
	}

	var exceeded model.SpendBudgetAlert
	if err := db.Where("alert_type = ?", model.SpendBudgetAlertExceeded).First(&exceeded).Error; err != nil {
		t.Fatalf("expected exceeded alert for full settled spend: %v", err)
	}
	if exceeded.SpentMicrousd != billing.MicroUSD(1_100) || exceeded.DeltaMicrousd != billing.MicroUSD(1_100) {
		t.Fatalf("expected alert to use full 1100 settled charge, got %+v", exceeded)
	}
}

func TestPostConsumeQuotaRecordsExactPreConsumedSpendBudgetCharge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.Create(&model.User{Id: 1, Username: "budget-user", Quota: billing.MicroUSD(10_000)}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "token-key", RemainQuota: billing.MicroUSD(10_000)}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if err := db.Create(&model.SpendBudget{
		Scope:               model.SpendBudgetScopeToken,
		TokenID:             2,
		Period:              model.SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(1_000),
		WarningThresholdPct: 80,
		Enforcement:         model.SpendBudgetEnforcementBlock,
		Enabled:             true,
	}).Error; err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}

	c, _ := gin.CreateTestContext(nil)
	c.Set("token_quota", int64(10_000))
	relayInfo := &relaycommon.RelayInfo{
		RequestID:       "req-service-exact-charge",
		UserId:          1,
		TokenId:         2,
		TokenKey:        "token-key",
		SessionID:       "sess-service-exact-charge",
		AgentID:         "codex",
		OriginModelName: "gpt-test",
		UsingGroup:      model.ChannelLaneDirect,
		PriceData: types.PriceData{
			PricingVersion: 1,
		},
	}
	if err := PreConsumeQuota(c, billing.MicroUSD(900), relayInfo); err != nil {
		t.Fatalf("PreConsumeQuota returned error: %v", err)
	}
	if err := PostConsumeQuota(relayInfo, 0, relayInfo.FinalPreConsumedQuota, false); err != nil {
		t.Fatalf("PostConsumeQuota returned error: %v", err)
	}
	var reservationCount int64
	if err := db.Model(&model.SpendBudgetReservation{}).Where("request_id = ?", relayInfo.RequestID).Count(&reservationCount).Error; err != nil {
		t.Fatalf("failed to count reservation after exact settlement: %v", err)
	}
	if reservationCount != 0 {
		t.Fatalf("expected exact settlement to release spend budget reservation, got %d", reservationCount)
	}

	var threshold model.SpendBudgetAlert
	if err := db.Where("alert_type = ? AND threshold_pct = ?", model.SpendBudgetAlertThreshold, 80).First(&threshold).Error; err != nil {
		t.Fatalf("expected threshold alert for exact pre-consumed spend: %v", err)
	}
	if threshold.SpentMicrousd != billing.MicroUSD(900) || threshold.DeltaMicrousd != billing.MicroUSD(900) {
		t.Fatalf("expected alert to use exact 900 settled charge, got %+v", threshold)
	}
}

func TestPreConsumeQuotaDistinguishesPlatformBudgetFromWallet(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.AutoMigrate(&model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{Id: 1, Username: "platform-budget", Quota: 10_000}).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", 1).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, Key: "platform-budget-key", RemainQuota: 10_000}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SpendBudget{Scope: model.SpendBudgetScopeOrg, Period: model.SpendBudgetPeriodMonth, LimitMicrousd: 500, Enforcement: model.SpendBudgetEnforcementBlock, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	err := PreConsumeQuota(c, 600, &relaycommon.RelayInfo{ProjectID: project.ID, UserId: 1, ActorUserID: 1, PayerUserID: 1, TokenId: 2, RequestID: "platform-block"})
	if err == nil || err.GetErrorCode() != types.ErrorCodePlatformBudgetExceeded || err.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("platform budget misclassified: %v", err)
	}
	common.SetContextKey(c, constant.ContextKeyProjectID, project.ID)
	common.SetContextKey(c, constant.ContextKeyPayerUserID, 1)
	common.SetContextKey(c, constant.ContextKeyRequestErrorCode, string(err.GetErrorCode()))
	model.RecordErrorLog(c, 1, 0, "test", "key", "blocked", 2, 0, false, "", nil)
	var rejection model.Log
	if loadErr := db.First(&rejection).Error; loadErr != nil {
		t.Fatal(loadErr)
	}
	if rejection.RejectionReason != "platform_budget_exceeded" || rejection.ProjectID != project.ID {
		t.Fatalf("rejection log lost platform reason: %+v", rejection)
	}
	var user model.User
	if loadErr := db.First(&user, 1).Error; loadErr != nil {
		t.Fatal(loadErr)
	}
	if user.Quota != 10_000 {
		t.Fatalf("budget rejection debited wallet: %d", user.Quota)
	}
}

func TestPreConsumeQuotaBlocksPaymentRiskHold(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.AutoMigrate(&model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	owner := model.User{Username: "held-project-owner", Quota: 10_000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&project).Update("payment_risk_hold", true).Error; err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	err := PreConsumeQuota(c, 100, &relaycommon.RelayInfo{ProjectID: project.ID, UserId: owner.Id, PayerUserID: owner.Id})
	if err == nil || err.GetErrorCode() != types.ErrorCodeProjectAccessSuspended || err.StatusCode != http.StatusForbidden {
		t.Fatalf("payment risk hold did not block request: %v", err)
	}
}

// A project member's request is billed to the project's payer, so the
// out-of-quota notification goes to the payer's settings and address, not the
// member's.
func TestMemberQuotaNotificationUsesPayer(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	payer := model.User{Id: 51, Username: "notify-payer", Email: "payer@example.test", Setting: `{"notify_type":"webhook","webhook_url":"https://example.test/notify","quota_warning_threshold":123}`}
	if err := db.Create(&payer).Error; err != nil {
		t.Fatal(err)
	}
	info := &relaycommon.RelayInfo{ProjectID: 7, PayerUserID: 51, UserId: 52, ActorUserID: 52}
	settings, email, err := quotaNotifyRecipient(info)
	if err != nil || settings.NotifyType != dto.NotifyTypeWebhook || settings.WebhookUrl != "https://example.test/notify" || settings.QuotaWarningThreshold != 123 || email != payer.Email {
		t.Fatalf("payer notification lost: %+v %s %v", settings, email, err)
	}
	if err := db.Model(&payer).Update("setting", "{}").Error; err != nil {
		t.Fatal(err)
	}
	settings, email, err = quotaNotifyRecipient(info)
	if err != nil || email != payer.Email || settings.NotifyType != "" {
		t.Fatalf("fallback email lost: %s %v", email, err)
	}
}
