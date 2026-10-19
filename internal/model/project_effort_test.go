package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestMemberPolicyOnlyEditPreservesBudgetHistoryAndNotices(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "policy-owner"}
	target := User{Username: "policy-target", Email: "target@example.com"}
	for _, u := range []*User{&owner, &target} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	member := ProjectMember{ProjectID: project.ID, UserID: target.Id, Role: ProjectRoleMember, Status: ProjectMemberActive}
	if err := db.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	cap := "medium"
	allowed := []string{"gpt-5.2"}
	updated, err := UpdateProjectMemberBudget(project.ID, owner.Id, target.Id, member.Version, nil, 80, "", ProjectMemberPolicy{EffortCap: &cap, AllowedModels: &allowed})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != member.Version+1 || updated.ReasoningEffortCap != cap {
		t.Fatalf("policy not versioned: %+v", updated)
	}
	for _, table := range []interface{}{&ProjectBudgetHistory{}, &ProjectNotification{}} {
		var count int64
		if err := db.Model(table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("policy-only edit wrote %T: %d", table, count)
		}
	}
	if _, err := UpdateProjectMemberBudget(project.ID, owner.Id, target.Id, member.Version, nil, 80, "", ProjectMemberPolicy{EffortCap: &cap}); err != ErrProjectVersionConflict {
		t.Fatalf("stale edit allowed: %v", err)
	}
	amount := billing.MicroUSD(100_000_000)
	if _, err := UpdateProjectMemberBudget(project.ID, owner.Id, target.Id, updated.Version, &amount, 80, ""); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&ProjectBudgetHistory{}).Count(&count)
	if count != 1 {
		t.Fatalf("real budget change history=%d", count)
	}
}

func TestUncappedMemberCountsKeyEffortClamps(t *testing.T) {
	db := setupProjectTestDB(t, true)
	oldLog := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = oldLog })
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	user := User{Username: "uncapped-effort-member"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", user.Id).First(&project)
	var member ProjectMember
	db.Where("project_id = ? AND user_id = ?", project.ID, user.Id).First(&member)
	now := time.Now().UTC()
	if err := db.Create(&Log{ProjectID: project.ID, UserId: user.Id, Type: LogTypeConsume, CreatedAt: now.Unix(), Other: `{"effort_clamped":true,"effort_cap_source":"key"}`}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := ReadProjectMemberView(project, member, now)
	if err != nil {
		t.Fatal(err)
	}
	if view.EffortDownedMonth != 1 {
		t.Fatalf("uncapped member key clamps=%d", view.EffortDownedMonth)
	}
}

func TestKeyEffortCapPersistsOnlyWithinOwningMemberCeiling(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner, member, project, token := shareProjectWithMember(t, db, "effort-ceiling", time.Now().UTC())
	if err := db.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", project.ID, member.Id).Update("reasoning_effort_cap", "low").Error; err != nil {
		t.Fatal(err)
	}
	// The viewer's policy differs: validation must use the target Key's owner.
	if err := db.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", project.ID, owner.Id).Update("reasoning_effort_cap", "minimal").Error; err != nil {
		t.Fatal(err)
	}
	invalid := Token{Name: "too high", Key: strings.Repeat("x", 48), ReasoningEffortCap: "high"}
	if err := CreateProjectPersonalToken(project.ID, member.Id, &invalid); !errors.Is(err, ErrProjectInvalidInput) {
		t.Fatalf("created over-cap Key: %v", err)
	}
	if invalid.Id != 0 {
		t.Fatal("rejected creation persisted")
	}
	for _, viewer := range []int{member.Id, owner.Id} {
		edit := token
		edit.ReasoningEffortCap = "high"
		if err := UpdateProjectToken(project.ID, viewer, &edit); !errors.Is(err, ErrProjectInvalidInput) {
			t.Fatalf("viewer %d saved over-cap Key: %v", viewer, err)
		}
	}
	edit := token
	edit.ReasoningEffortCap = "low"
	if err := UpdateProjectToken(project.ID, owner.Id, &edit); err != nil {
		t.Fatalf("manager used their own ceiling: %v", err)
	}
	var stored Token
	db.First(&stored, token.Id)
	if stored.ReasoningEffortCap != "low" {
		t.Fatal("valid cap not saved")
	}
	// Raising the member ceiling cannot resurrect the previously rejected high.
	db.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", project.ID, member.Id).Update("reasoning_effort_cap", "high")
	effective, _ := EffectiveEffortCap(stored.ReasoningEffortCap, "high")
	if effective != "low" {
		t.Fatalf("Key silently widened: %s", effective)
	}
	inherited := Token{Name: "inherit", Key: strings.Repeat("y", 48)}
	if err := CreateProjectPersonalToken(project.ID, member.Id, &inherited); err != nil {
		t.Fatalf("inherit rejected: %v", err)
	}
}

func TestLowerMemberCeilingReconcilesExistingKeyCaps(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner, user, project, token := shareProjectWithMember(t, db, "lower-existing", time.Now().UTC())
	if err := db.Model(&token).Update("reasoning_effort_cap", "high").Error; err != nil {
		t.Fatal(err)
	}
	var member ProjectMember
	if err := db.Where("project_id = ? AND user_id = ?", project.ID, user.Id).First(&member).Error; err != nil {
		t.Fatal(err)
	}
	cap := "low"
	updated, err := UpdateProjectMemberBudget(project.ID, owner.Id, user.Id, member.Version, nil, 80, "", ProjectMemberPolicy{EffortCap: &cap})
	if err != nil {
		t.Fatal(err)
	}
	var stored Token
	if err := db.First(&stored, token.Id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ReasoningEffortCap != "low" || stored.ProjectVersion != token.ProjectVersion+1 {
		t.Fatalf("key not reconciled: %+v", stored)
	}
	stale := token
	stale.ReasoningEffortCap = "low"
	if err := UpdateProjectToken(project.ID, user.Id, &stale); !errors.Is(err, ErrProjectVersionConflict) {
		t.Fatalf("stale key edit allowed: %v", err)
	}
	cap = "high"
	if _, err := UpdateProjectMemberBudget(project.ID, owner.Id, user.Id, updated.Version, nil, 80, "", ProjectMemberPolicy{EffortCap: &cap}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&stored, token.Id).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ReasoningEffortCap != "low" {
		t.Fatal("raising member silently widened key")
	}
}

func TestLoweredLogFilterMatchesMonthAndTokenCount(t *testing.T) {
	db := setupProjectTestDB(t, true)
	previousLog := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = previousLog })
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	owner, user, project, token := shareProjectWithMember(t, db, "lowered-logs", time.Now().UTC())
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	rows := []Log{
		{ProjectID: project.ID, UserId: user.Id, TokenId: token.Id, Type: LogTypeConsume, CreatedAt: start, Other: `{"effort_clamped":true}`},
		{ProjectID: project.ID, UserId: user.Id, TokenId: token.Id, Type: LogTypeConsume, CreatedAt: start - 1, Other: `{"effort_clamped":true}`},
		{ProjectID: project.ID, UserId: user.Id, TokenId: token.Id, Type: LogTypeConsume, CreatedAt: start, Other: `{"effort_clamped":false}`},
		{ProjectID: project.ID, UserId: user.Id, TokenId: token.Id, Type: LogTypeError, CreatedAt: start, Other: `{"effort_clamped":true}`},
		{ProjectID: project.ID, UserId: user.Id, TokenId: token.Id + 1, Type: LogTypeConsume, CreatedAt: start, Other: `{"effort_clamped":true}`},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	access := ProjectLogAccess{Project: project, Viewer: ProjectMember{ProjectID: project.ID, UserID: owner.Id, Role: ProjectRoleOwner, Status: ProjectMemberActive}, TokenID: token.Id}
	var got []Log
	if err := ProjectLogsQuery(access, ProjectLogFilter{Start: start, End: now.Unix(), EffortClamped: true}).Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Id != rows[0].Id {
		t.Fatalf("wrong lowered log set: %+v", got)
	}
}
