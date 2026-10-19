package worker

import (
	"context"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
)

func TestProjectSettlementReplayKeepsPayerActorAndOriginalMonth(t *testing.T) {
	db := withSettlementFailureWorkerTestDB(t, "project_settlement_month")
	if err := db.AutoMigrate(&model.SpendBudget{}, &model.SpendBudgetAlert{}, &model.SpendBudgetCharge{}, &model.ProjectSpend{}, &model.Project{}, &model.ProjectMember{}, &model.SpendBudgetReservation{}); err != nil {
		t.Fatal(err)
	}
	for _, user := range []model.User{{Id: 1, Username: "payer", Quota: 900}, {Id: 2, Username: "actor", Quota: 7}} {
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
	}
	token := model.Token{Id: 3, UserId: 2, ProjectID: 10, Key: "project-replay-key", RemainQuota: 900}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	budget := model.SpendBudget{ProjectID: 10, Scope: model.SpendBudgetScopeUser, UserID: 2, LimitMicrousd: 1000, Enabled: true, Period: model.SpendBudgetPeriodMonth, Enforcement: model.SpendBudgetEnforcementBlock}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	acceptedAt := time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	if err := model.CheckSpendBudgetBeforeCharge(model.SpendBudgetChargeInput{
		ProjectID: 10, PayerUserID: 1, ActorUserID: 2, TokenID: 3, RequestID: "project-replay", DeltaMicrousd: 100, OccurredAt: acceptedAt,
	}); err != nil {
		t.Fatal(err)
	}
	row := model.SettlementFailure{
		ProjectID: 10, ActorUserID: 2, UserID: 1, TokenID: 3, TokenKey: "project-replay-key",
		RequestID: "project-replay", QuotaDelta: -40, PreConsumed: 100,
		RefType: "relay_settlement", RefID: "project-replay-ref", CreatedAt: acceptedAt,
		Metadata: `{"budget_occurred_at":"2026-09-30T23:59:59Z"}`,
	}
	if err := model.RecordSettlementFailure(row); err != nil {
		t.Fatal(err)
	}
	now := acceptedAt.Add(2 * time.Hour)
	stats, err := ReconcileSettlementFailuresOnce(context.Background(), now)
	if err != nil || stats.Resolved != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	// A second durable retry record for the same wallet event must also finish
	// the budget without a second charge or a different month's attribution.
	if err := model.RecordSettlementFailure(row); err != nil {
		t.Fatal(err)
	}
	stats, err = ReconcileSettlementFailuresOnce(context.Background(), now.Add(time.Hour))
	if err != nil || stats.Resolved != 1 {
		t.Fatalf("second replay stats=%+v err=%v", stats, err)
	}
	var payer, actor model.User
	db.First(&payer, 1)
	db.First(&actor, 2)
	if payer.Quota != 940 || actor.Quota != 7 {
		t.Fatalf("wallets payer=%d actor=%d", payer.Quota, actor.Quota)
	}
	var charges []model.SpendBudgetCharge
	if err := db.Find(&charges).Error; err != nil {
		t.Fatal(err)
	}
	september := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Unix()
	if len(charges) != 1 || charges[0].AmountMicrousd != 60 || charges[0].PeriodStart != september || charges[0].ActorUserID != 2 || charges[0].PayerUserID != 1 {
		t.Fatalf("charges=%+v", charges)
	}
	var reserved int64
	db.Model(&model.SpendBudgetReservation{}).Count(&reserved)
	if reserved != 0 {
		t.Fatalf("remaining reservations=%d", reserved)
	}
}
