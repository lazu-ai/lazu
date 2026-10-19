package model

import (
	"strconv"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func setupBudgetNoticeProject(t *testing.T) (*gorm.DB, Project, User, User, User) {
	t.Helper()
	db := setupProjectTestDB(t, true)
	t.Setenv("PROJECTS_ENABLED", "true")
	old := common.CryptoSecret
	common.CryptoSecret = "budget-notice-test"
	t.Cleanup(func() { common.CryptoSecret = old })
	owner := User{Username: "notice-owner", Email: "owner@example.com"}
	admin := User{Username: "notice-admin", Email: "admin@example.com"}
	member := User{Username: "notice-member", Email: "grace@example.com"}
	for _, user := range []*User{&owner, &admin, &member} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	upgraded := time.Now().Unix()
	project.UpgradedAt = &upgraded
	if err := db.Save(&project).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []ProjectMember{
		{ProjectID: project.ID, UserID: admin.Id, Role: ProjectRoleAdmin, Status: ProjectMemberActive},
		{ProjectID: project.ID, UserID: member.Id, Role: ProjectRoleMember, Status: ProjectMemberActive},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, project, owner, admin, member
}

func budgetNoticeRecipients(t *testing.T, db *gorm.DB, project Project, prefix string) map[int]int {
	t.Helper()
	var rows []ProjectNotification
	if err := db.Where("project_id = ? AND event_key LIKE ?", project.ID, prefix+"%").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	result := map[int]int{}
	for _, row := range rows {
		result[row.RecipientUserID]++
	}
	return result
}

func memberBudget(t *testing.T, db *gorm.DB, project Project, userID int, dollars int64) SpendBudget {
	t.Helper()
	amount := billing.MicroUSD(dollars * 1_000_000)
	if err := setProjectBudgetTx(db, project.ID, userID, 0, &amount, 80); err != nil {
		t.Fatal(err)
	}
	scope := SpendBudgetScopeOrg
	if userID > 0 {
		scope = SpendBudgetScopeUser
	}
	var budget SpendBudget
	if err := db.Where("project_id = ? AND scope = ? AND user_id = ?", project.ID, scope, userID).First(&budget).Error; err != nil {
		t.Fatal(err)
	}
	return budget
}

func TestProjectBudgetNoticesReachTheRightPeopleOnce(t *testing.T) {
	db, project, owner, admin, member := setupBudgetNoticeProject(t)
	budget := memberBudget(t, db, project, member.Id, 100)
	start, end := spendBudgetPeriodWindow(SpendBudgetPeriodMonth, time.Now())

	// The warning line goes to the member alone.
	if err := queueProjectBudgetNoticesTx(db, budget, projectBudgetWarn, start, end, 80_000_000); err != nil {
		t.Fatal(err)
	}
	if got := budgetNoticeRecipients(t, db, project, "budget:warn:"); len(got) != 1 || got[member.Id] != 1 {
		t.Fatalf("warning recipients: %v", got)
	}

	// Used up: the member, the owner and the admin, each once, however often
	// the crossing or a refusal reports it.
	for i := 0; i < 2; i++ {
		if err := queueProjectBudgetNoticesTx(db, budget, projectBudgetOut, start, end, 99_900_000); err != nil {
			t.Fatal(err)
		}
	}
	got := budgetNoticeRecipients(t, db, project, "budget:out:")
	if len(got) != 3 || got[member.Id] != 1 || got[owner.Id] != 1 || got[admin.Id] != 1 {
		t.Fatalf("used-up recipients: %v", got)
	}

	// A raised budget is a new limit, so its lines count again.
	raised := memberBudget(t, db, project, member.Id, 200)
	if err := queueProjectBudgetNoticesTx(db, raised, projectBudgetWarn, start, end, 160_000_000); err != nil {
		t.Fatal(err)
	}
	if got := budgetNoticeRecipients(t, db, project, "budget:warn:"); got[member.Id] != 2 {
		t.Fatalf("raised budget did not warn again: %v", got)
	}

	// An admin's own budget is the owner's business only.
	adminBudget := memberBudget(t, db, project, admin.Id, 50)
	if err := queueProjectBudgetNoticesTx(db, adminBudget, projectBudgetOut, start, end, 50_000_000); err != nil {
		t.Fatal(err)
	}
	var adminRows []ProjectNotification
	db.Where("event_key LIKE ?", "budget:out:"+strconv.Itoa(int(adminBudget.ID))+":%").Find(&adminRows)
	if len(adminRows) != 2 {
		t.Fatalf("admin used-up notices: %d", len(adminRows))
	}
	for _, row := range adminRows {
		if row.RecipientUserID != admin.Id && row.RecipientUserID != owner.Id {
			t.Fatalf("admin budget told %d", row.RecipientUserID)
		}
	}

	// The project cap is the owner's alone.
	cap := memberBudget(t, db, project, 0, 1000)
	if err := queueProjectBudgetNoticesTx(db, cap, projectBudgetWarn, start, end, 800_000_000); err != nil {
		t.Fatal(err)
	}
	var capRows []ProjectNotification
	db.Where("event_key LIKE ?", "budget:warn:"+strconv.Itoa(int(cap.ID))+":%").Find(&capRows)
	if len(capRows) != 1 || capRows[0].RecipientUserID != owner.Id {
		t.Fatalf("project cap notices: %+v", capRows)
	}
	email, err := capRows[0].Email()
	if err != nil || email.Receiver != owner.Email {
		t.Fatalf("project cap mail: %+v %v", email, err)
	}
}

func TestProjectBudgetNoticesSkipUnsharedProjects(t *testing.T) {
	db, project, _, _, member := setupBudgetNoticeProject(t)
	budget := memberBudget(t, db, project, member.Id, 100)
	db.Model(&project).Update("upgraded_at", nil)
	start, end := spendBudgetPeriodWindow(SpendBudgetPeriodMonth, time.Now())
	if err := queueProjectBudgetNoticesTx(db, budget, projectBudgetOut, start, end, 100_000_000); err != nil {
		t.Fatal(err)
	}
	if got := budgetNoticeRecipients(t, db, project, "budget:"); len(got) != 0 {
		t.Fatalf("unshared project mailed: %v", got)
	}
}

func TestProjectBudgetThresholdCrossingQueuesNotice(t *testing.T) {
	db, project, _, _, member := setupBudgetNoticeProject(t)
	budget := memberBudget(t, db, project, member.Id, 100)
	start, end := spendBudgetPeriodWindow(SpendBudgetPeriodMonth, time.Now())
	input := SpendBudgetChargeInput{ProjectID: project.ID, ActorUserID: member.Id, UserID: member.Id, DeltaMicrousd: 10_000_000, OccurredAt: time.Now()}
	if err := recordSpendBudgetThresholdAlerts(db, budget, input, start, end, 75_000_000, 85_000_000); err != nil {
		t.Fatal(err)
	}
	if got := budgetNoticeRecipients(t, db, project, "budget:warn:"); got[member.Id] != 1 {
		t.Fatalf("crossing did not warn: %v", got)
	}
}

func TestReadProjectNearBudget(t *testing.T) {
	db, project, _, admin, member := setupBudgetNoticeProject(t)
	memberBudget(t, db, project, member.Id, 100)
	memberBudget(t, db, project, admin.Id, 100)
	now := time.Now().UTC()
	for i, row := range []ProjectSpend{
		{EventKey: "near-1", ProjectID: project.ID, ActorUserID: member.Id, AmountMicrousd: 91_000_000, OccurredAt: now},
		{EventKey: "near-2", ProjectID: project.ID, ActorUserID: admin.Id, AmountMicrousd: 40_000_000, OccurredAt: now},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("spend %d: %v", i, err)
		}
	}
	near, err := ReadProjectNearBudget(project, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(near) != 1 || near[0].UserID != member.Id || near[0].SpentMicrousd != 91_000_000 {
		t.Fatalf("near budget: %+v", near)
	}
}

func TestProjectInviteFollowUpsAndSignupPass(t *testing.T) {
	db, project, owner, _, _ := setupBudgetNoticeProject(t)
	invitee := User{Username: "follow-up", Email: "newcomer@example.com", VerifiedEmail: "newcomer@example.com"}
	if err := db.Create(&invitee).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	invite, err := CreateProjectInvite(project.ID, owner.Id, CreateProjectInviteInput{Email: invitee.Email, Role: ProjectRoleMember, BaseURL: "https://console.example"}, now)
	if err != nil {
		t.Fatal(err)
	}
	var reminder, expired ProjectNotification
	if err := db.Where("event_key = ?", projectInviteReminderKey(invite.TokenDigest)).First(&reminder).Error; err != nil {
		t.Fatalf("no reminder: %v", err)
	}
	if err := db.Where("event_key = ?", projectInviteExpiredKey(invite.TokenDigest)).First(&expired).Error; err != nil {
		t.Fatalf("no expiry notice: %v", err)
	}
	deadline := invite.EffectiveExpiresAt()
	if reminder.AvailableAt != deadline-int64(projectInviteReminderLead/time.Second) || expired.AvailableAt != deadline || expired.RecipientUserID != owner.Id {
		t.Fatalf("follow-up timing: reminder=%d expired=%d/%d deadline=%d", reminder.AvailableAt, expired.AvailableAt, expired.RecipientUserID, deadline)
	}

	raw := invitationRawFromQueue(t, db, invite.TokenDigest)
	if !ProjectInviteOpensSignup(raw, "NEWCOMER@example.com", now) || ProjectInviteOpensSignup(raw, "someone@example.com", now) {
		t.Fatal("signup pass ignored the invited address")
	}
	if ProjectInviteOpensSignup(raw, invitee.Email, now.Add(ProjectInviteValidity+time.Minute)) {
		t.Fatal("expired invitation still opens signup")
	}

	if _, err := AcceptProjectInvite(raw, invitee.Id, now.Add(time.Minute), "https://console.example"); err != nil {
		t.Fatal(err)
	}
	for _, row := range []*ProjectNotification{&reminder, &expired} {
		db.First(row, row.ID)
		if row.Status != "cancelled" || row.Payload != "" {
			t.Fatalf("follow-up kept after acceptance: %+v", row)
		}
	}
	if ProjectInviteOpensSignup(raw, invitee.Email, now) {
		t.Fatal("accepted invitation still opens signup")
	}
}

func TestProjectBudgetNoticeFailureNeverBlocksTheAlert(t *testing.T) {
	db, project, _, _, member := setupBudgetNoticeProject(t)
	budget := memberBudget(t, db, project, member.Id, 100)
	common.CryptoSecret = "" // the notice cannot be sealed
	start, end := spendBudgetPeriodWindow(SpendBudgetPeriodMonth, time.Now())
	input := SpendBudgetChargeInput{ProjectID: project.ID, ActorUserID: member.Id, UserID: member.Id, DeltaMicrousd: 30_000_000, OccurredAt: time.Now()}
	if err := recordSpendBudgetThresholdAlerts(db, budget, input, start, end, 75_000_000, 105_000_000); err != nil {
		t.Fatalf("a mail failure reached the financial writer: %v", err)
	}
	var alerts int64
	db.Model(&SpendBudgetAlert{}).Where("budget_id = ?", budget.ID).Count(&alerts)
	if alerts != 2 {
		t.Fatalf("alerts recorded: %d", alerts)
	}
	if got := budgetNoticeRecipients(t, db, project, "budget:"); len(got) != 0 {
		t.Fatalf("half-written notices: %v", got)
	}
}

func TestProjectInviteResendCancelsOldFollowUps(t *testing.T) {
	db, project, owner, _, _ := setupBudgetNoticeProject(t)
	now := time.Now().UTC()
	invite, err := CreateProjectInvite(project.ID, owner.Id, CreateProjectInviteInput{Email: "later@example.com", Role: ProjectRoleMember, BaseURL: "https://console.example"}, now)
	if err != nil {
		t.Fatal(err)
	}
	resent, err := ChangeProjectInvite(project.ID, owner.Id, invite.PublicID, true, "https://console.example", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{projectInviteReminderKey(invite.TokenDigest), projectInviteExpiredKey(invite.TokenDigest)} {
		var row ProjectNotification
		if err := db.Where("event_key = ?", key).First(&row).Error; err != nil || row.Status != "cancelled" {
			t.Fatalf("old follow-up %s still live: %+v %v", key, row, err)
		}
	}
	for _, key := range []string{projectInviteReminderKey(resent.TokenDigest), projectInviteExpiredKey(resent.TokenDigest)} {
		var row ProjectNotification
		if err := db.Where("event_key = ?", key).First(&row).Error; err != nil || row.Status != "pending" {
			t.Fatalf("new follow-up %s missing: %+v %v", key, row, err)
		}
	}
}
