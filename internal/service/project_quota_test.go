package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestProjectQuotaUsesPayerAndPreservesMemberBudgetAttribution(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.AutoMigrate(&model.SettlementFailure{}, &model.SettlementOutbox{}, &model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []model.User{{Id: 10, Username: "payer", Quota: 100}, {Id: 20, Username: "actor", Quota: 3}} {
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	token := model.Token{Id: 30, UserId: 20, ProjectID: 1, Key: "project-member-key", UnlimitedQuota: true}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	budget := model.SpendBudget{ProjectID: 1, Scope: model.SpendBudgetScopeUser, Period: model.SpendBudgetPeriodMonth, UserID: 20, LimitMicrousd: 1000, Enabled: true, Enforcement: model.SpendBudgetEnforcementBlock}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	info := &relaycommon.RelayInfo{ProjectID: 1, PayerUserID: 10, ActorUserID: 20, UserId: 20, TokenId: 30, TokenKey: "project-member-key", TokenUnlimited: true, RequestID: "project-charge", BudgetSettlementAt: time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC)}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if err := PreConsumeQuota(c, 20, info); err != nil {
		t.Fatalf("payer could fund request despite actor balance: %v", err)
	}
	// The actual charge was 12: refund 8 from the 20 pre-consumed amount.
	if err := PostConsumeQuota(info, -8, 20, false); err != nil {
		t.Fatal(err)
	}
	var payer, actor model.User
	db.First(&payer, 10)
	db.First(&actor, 20)
	if payer.Quota != 88 || actor.Quota != 3 {
		t.Fatalf("wrong wallet: payer=%d actor=%d", payer.Quota, actor.Quota)
	}
	if info.UserId != 20 || info.ActorID() != 20 {
		t.Fatal("settlement changed request identity")
	}
	var charge model.SpendBudgetCharge
	if err := db.Where("request_id = ?", info.RequestID).First(&charge).Error; err != nil {
		t.Fatal(err)
	}
	if charge.PayerUserID != 10 || charge.ActorUserID != 20 || charge.UserID != 20 || charge.ProjectID != 1 || charge.AmountMicrousd != billing.MicroUSD(12) {
		t.Fatalf("budget snapshot: %+v", charge)
	}
	if charge.PeriodStart != time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Unix() || !info.BudgetSettlementAt.Equal(time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC)) {
		t.Fatalf("settlement moved the admission month: %+v at %v", charge, info.BudgetSettlementAt)
	}
	var reservations int64
	db.Model(&model.SpendBudgetReservation{}).Where("request_id = ?", info.RequestID).Count(&reservations)
	if reservations != 0 {
		t.Fatal("budget reservation not released")
	}
	var wrongLedgers int64
	db.Model(&model.WalletLedger{}).Where("user_id = ?", 20).Count(&wrongLedgers)
	if wrongLedgers != 0 {
		t.Fatal("actor got a wallet ledger entry")
	}
}

func TestProjectRealtimeSegmentsCountSeparatelyAndStopAtCap(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.AutoMigrate(&model.SettlementFailure{}, &model.SettlementOutbox{}, &model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	payer := model.User{Username: "realtime-payer", Quota: 100}
	if err := db.Create(&payer).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", payer.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	token := model.Token{UserId: payer.Id, ProjectID: project.ID, Key: "realtime-project-key", UnlimitedQuota: true}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	budget := model.SpendBudget{ProjectID: project.ID, Scope: model.SpendBudgetScopeOrg, Period: model.SpendBudgetPeriodMonth, LimitMicrousd: 25, Enabled: true, Enforcement: model.SpendBudgetEnforcementBlock}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	info := &relaycommon.RelayInfo{ProjectID: project.ID, PayerUserID: payer.Id, ActorUserID: payer.Id, UserId: payer.Id, TokenId: token.Id, TokenKey: token.Key, TokenUnlimited: true, RequestID: "one-websocket"}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	for i := 0; i < 2; i++ {
		if err := consumeProjectRealtimeSegment(c, info, 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := consumeProjectRealtimeSegment(c, info, 10); err == nil {
		t.Fatal("third segment bypassed project cap")
	}
	if info.RequestID != "one-websocket" {
		t.Fatal("session identity changed")
	}
	if err := db.First(&payer, payer.Id).Error; err != nil {
		t.Fatal(err)
	}
	if payer.Quota != 80 {
		t.Fatalf("wallet %d", payer.Quota)
	}
	var count int64
	db.Model(&model.ProjectSpend{}).Count(&count)
	if count != 2 {
		t.Fatalf("settled segments %d", count)
	}
}

func TestProjectAcceptedRequestRefundSurvivesKeyDeletion(t *testing.T) {
	db := setupSpendBudgetServiceTestDB(t)
	if err := db.AutoMigrate(&model.SettlementFailure{}, &model.SettlementOutbox{}, &model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatal(err)
	}
	payer := model.User{Username: "deleted-key-payer", Quota: 100}
	if err := db.Create(&payer).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	db.Where("owner_user_id = ?", payer.Id).First(&project)
	token := model.Token{UserId: payer.Id, ProjectID: project.ID, Key: "refund-deleted-key", RemainQuota: 100}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	info := &relaycommon.RelayInfo{ProjectID: project.ID, PayerUserID: payer.Id, ActorUserID: payer.Id, UserId: payer.Id, TokenId: token.Id, TokenKey: token.Key, RequestID: "accepted-before-delete"}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if err := PreConsumeQuota(c, 20, info); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&token).Error; err != nil {
		t.Fatal(err)
	}
	if err := PostConsumeQuota(info, -8, 20, false); err != nil {
		t.Fatalf("accepted refund blocked by deletion: %v", err)
	}
	if err := db.First(&payer, payer.Id).Error; err != nil {
		t.Fatal(err)
	}
	if payer.Quota != 88 {
		t.Fatalf("payer quota %d", payer.Quota)
	}
	if err := db.Unscoped().First(&token, token.Id).Error; err != nil {
		t.Fatal(err)
	}
	if !token.DeletedAt.Valid || token.RemainQuota != 88 || token.UsedQuota != 12 {
		t.Fatalf("refund resurrected or miscounted Key: deleted=%v remaining=%d used=%d", token.DeletedAt.Valid, token.RemainQuota, token.UsedQuota)
	}
	if err := model.PreConsumeQuotaWithLedger(model.PreConsumeQuotaWithLedgerInput{UserID: payer.Id, TokenID: token.Id, TokenKey: token.Key, Quota: 1, RefType: "new-request", RefID: "after-delete"}); err == nil {
		t.Fatal("settlement exception leaked into new admission")
	}
}
