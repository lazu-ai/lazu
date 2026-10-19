package model

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
)

// A member budget counts the spend of its own actor and nothing else. An
// actorless row belongs to nobody: the project migration attributes every Key,
// so a row that still carries actor 0 must not be absorbed by the owner's
// allowance — that would charge one person for spend no one can attribute.
func TestProjectBudgetCountsOnlyItsOwnActorSpend(t *testing.T) {
	db := setupProjectTestDB(t, true)
	now := time.Now().UTC().Truncate(time.Second)
	owner, member, project, _ := shareProjectWithMember(t, db, "actor-spend", now)

	rows := []ProjectSpend{
		{EventKey: "owner-request", ProjectID: project.ID, PayerUserID: owner.Id, ActorUserID: owner.Id, TokenID: 4242, RequestID: "owner-request", AmountMicrousd: billing.MicroUSD(600), OccurredAt: now},
		{EventKey: "actorless", ProjectID: project.ID, PayerUserID: owner.Id, ActorUserID: 0, TokenID: 4243, RequestID: "actorless", AmountMicrousd: billing.MicroUSD(500), OccurredAt: now},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	start, end := spendBudgetPeriodWindow(SpendBudgetPeriodMonth, now)
	ownerBudget := SpendBudget{ProjectID: project.ID, Scope: SpendBudgetScopeUser, UserID: owner.Id, Period: SpendBudgetPeriodMonth, LimitMicrousd: 2000, Enabled: true}
	got, err := sumProjectSpendTx(db, ownerBudget, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if got != 600 {
		t.Fatalf("owner budget counted %d, want only its own actor's 600", got)
	}

	memberBudget := SpendBudget{ProjectID: project.ID, Scope: SpendBudgetScopeUser, UserID: member.Id, Period: SpendBudgetPeriodMonth, LimitMicrousd: 2000, Enabled: true}
	got, err = sumProjectSpendTx(db, memberBudget, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("a member absorbed another actor's spend: %d", got)
	}
}

// The same rule for in-flight holds: a budget sees the holds of its own actor,
// and an actorless hold is nobody's.
func TestProjectBudgetSeesOnlyItsOwnActorHolds(t *testing.T) {
	db := setupProjectTestDB(t, true)
	now := time.Now().UTC().Truncate(time.Second)
	owner, member, project, _ := shareProjectWithMember(t, db, "actor-hold", now)

	start, end := spendBudgetPeriodWindow(SpendBudgetPeriodMonth, now)
	holds := []SpendBudgetReservation{
		{
			ProjectID: project.ID, PayerUserID: owner.Id, ActorUserID: owner.Id, TokenID: 4243,
			RequestID: "owner-job", Period: SpendBudgetPeriodMonth,
			PeriodStart: start, PeriodEnd: end,
			ReservedMicrousd: billing.MicroUSD(700), CreatedAt: now,
			ExpiresAt: now.Add(24 * time.Hour).Unix(),
		},
		{
			ProjectID: project.ID, PayerUserID: owner.Id, ActorUserID: 0, TokenID: 4244,
			RequestID: "actorless-job", Period: SpendBudgetPeriodMonth,
			PeriodStart: start, PeriodEnd: end,
			ReservedMicrousd: billing.MicroUSD(900), CreatedAt: now,
			ExpiresAt: now.Add(24 * time.Hour).Unix(),
		},
	}
	for i := range holds {
		if err := db.Create(&holds[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	ownerBudget := SpendBudget{ProjectID: project.ID, Scope: SpendBudgetScopeUser, UserID: owner.Id, Period: SpendBudgetPeriodMonth, LimitMicrousd: 3000, Enabled: true}
	got, err := sumReservedSpendMicrousdForBudget(db, ownerBudget, start, end, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != 700 {
		t.Fatalf("owner budget saw %d of the in-flight holds, want only its own 700", got)
	}

	memberBudget := SpendBudget{ProjectID: project.ID, Scope: SpendBudgetScopeUser, UserID: member.Id, Period: SpendBudgetPeriodMonth, LimitMicrousd: 3000, Enabled: true}
	got, err = sumReservedSpendMicrousdForBudget(db, memberBudget, start, end, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("a member absorbed another actor's hold: %d", got)
	}
}

// SQLite stores UTC timestamps as text; a local-zone month boundary must not
// hide the first hours of a UTC billing month.
func TestProjectSpendUTCMonthBoundary(t *testing.T) {
	db := setupProjectTestDB(t, true)
	old := time.Local
	time.Local = time.FixedZone("UTC+8", 8*60*60)
	t.Cleanup(func() { time.Local = old })
	now := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	row := ProjectSpend{EventKey: "month-boundary", ProjectID: 77, ActorUserID: 1, PayerUserID: 1, AmountMicrousd: 600, OccurredAt: now}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	start, end := spendBudgetPeriodWindow(SpendBudgetPeriodMonth, now)
	got, err := sumProjectSpendTx(db, SpendBudget{ProjectID: 77, Scope: SpendBudgetScopeOrg}, start, end)
	if err != nil || got != 600 {
		t.Fatalf("UTC month spend = %v, %v", got, err)
	}
}
