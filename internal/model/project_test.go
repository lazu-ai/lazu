package model

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupProjectTestDB(t *testing.T, withProjects bool) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	oldDB := coreDB
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	coreDB = db
	t.Cleanup(func() { coreDB = oldDB; common.RedisEnabled = oldRedis; sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := db.AutoMigrate(&User{}, &Token{}, &TopUp{}, &SpendBudget{}, &SpendBudgetAlert{}, &SpendBudgetReservation{}, &SpendBudgetCharge{}, &ProjectSpend{}, &MediaJob{}, &File{}); err != nil {
		t.Fatal(err)
	}
	if withProjects {
		migrateProjectTestTables(t, db)
	}
	// Payment tests here assert exact credits; an extension that adds to a
	// top-up switches itself off here.
	for _, setup := range testDBSetupHooks {
		setup(t)
	}
	return db
}
func migrateProjectTestTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&Project{}, &ProjectMember{}, &ProjectInvite{}, &BudgetRequest{}, &ProjectRemoval{}, &ProjectRemovalKey{}, &ProjectBudgetHistory{}, &ProjectNotification{}, &TokenCredential{}, &TokenRotation{}, &TokenAuditLog{}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectSignupCreatesOneDefaultAndTokenInheritsIt(t *testing.T) {
	db := setupProjectTestDB(t, true)
	user := User{Username: "owner", Quota: 12345}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var p Project
	if err := db.Where("owner_user_id = ?", user.Id).First(&p).Error; err != nil {
		t.Fatal(err)
	}
	if p.Name != "owner" || p.StripeCustomerID != "" || p.UpgradedAt != nil {
		t.Fatalf("unexpected default: %+v", p)
	}
	settings, err := p.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.DefaultMemberBudgetMicrousd == nil || *settings.DefaultMemberBudgetMicrousd != 100_000_000 || settings.WarningPct != 80 {
		t.Fatalf("defaults: %+v", settings)
	}
	token := Token{UserId: user.Id, Key: strings.Repeat("a", 48), Name: "existing"}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	if token.ProjectID != p.ID {
		t.Fatal("token did not inherit default project")
	}
	for i := 0; i < 2; i++ {
		if _, err := ensureDefaultProjectTx(db, &user); err != nil {
			t.Fatal(err)
		}
	}
	var n int64
	db.Model(&Project{}).Count(&n)
	if n != 1 {
		t.Fatalf("duplicate projects: %d", n)
	}
	db.Model(&ProjectMember{}).Count(&n)
	if n != 1 {
		t.Fatalf("duplicate membership: %d", n)
	}
	var saved User
	db.First(&saved, user.Id)
	if saved.Quota != 12345 {
		t.Fatal("creating project changed wallet")
	}
}

func TestProjectMigrationPreservesBudgetAndSharedAttribution(t *testing.T) {
	db := setupProjectTestDB(t, false)
	owner := User{Username: "legacy", Quota: 456}
	other := User{Username: "second", Quota: 789}
	for _, u := range []*User{&owner, &other} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	token := Token{UserId: owner.Id, Key: strings.Repeat("b", 48)}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	budgets := []SpendBudget{{Scope: SpendBudgetScopeOrg, LimitMicrousd: 5000, Enabled: true}, {Scope: SpendBudgetScopeUser, UserID: owner.Id, LimitMicrousd: 1000, Enabled: true}, {Scope: SpendBudgetScopeToken, TokenID: token.Id, LimitMicrousd: 100, Enabled: true}}
	for i := range budgets {
		if err := db.Create(&budgets[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	charge := SpendBudgetCharge{BudgetID: budgets[1].ID, UserID: owner.Id, TokenID: token.Id, AmountMicrousd: 90, RequestID: "original-charge"}
	reservation := SpendBudgetReservation{BudgetID: budgets[1].ID, UserID: owner.Id, TokenID: token.Id, ReservedMicrousd: 10, RequestID: "original-reservation"}
	topup := TopUp{UserId: owner.Id, TradeNo: "original-payment", Amount: 100}
	job := MediaJob{PublicID: "job_legacy", UserID: owner.Id, TokenID: &token.Id, Status: MediaJobStatusRunning}
	file := File{UserID: owner.Id, TokenID: &token.Id, Filename: "old.bin", Purpose: "assistants", StorageKey: "legacy"}
	for _, row := range []interface{}{&charge, &reservation, &topup, &job, &file} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	migrateProjectTestTables(t, db)
	if err := migrateTeamProjects(db); err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	if project.ID == 0 {
		t.Fatal("no default")
	}
	db.First(&token, token.Id)
	if token.ProjectID != project.ID {
		t.Fatal("token attribution lost")
	}
	for i := range budgets {
		db.First(&budgets[i], budgets[i].ID)
		want := project.ID
		if i == 0 {
			want = 0
		}
		if budgets[i].ProjectID != want {
			t.Fatalf("budget %d wrong project", i)
		}
	}
	db.First(&charge, charge.ID)
	if charge.ProjectID != project.ID || charge.AmountMicrousd != 90 || charge.RequestID != "original-charge" {
		t.Fatalf("charge changed: %+v", charge)
	}
	db.First(&reservation, reservation.ID)
	if reservation.ProjectID != project.ID || reservation.ReservedMicrousd != 10 {
		t.Fatal("reservation lost")
	}
	db.First(&topup, topup.Id)
	if topup.ProjectID != project.ID || topup.PaidByUserID != owner.Id || topup.StripeCustomerID != "" {
		t.Fatal("topup attribution")
	}
	db.First(&job, job.ID)
	if job.ProjectID != project.ID || job.ActorUserID != owner.Id || job.PayerUserID != owner.Id {
		t.Fatal("async job attribution")
	}
	if err := db.Where("id = ?", file.ID).First(&file).Error; err != nil {
		t.Fatal(err)
	}
	if file.ProjectID != project.ID || file.ActorUserID != owner.Id {
		t.Fatal("file attribution")
	}
	// Simulate later shared ownership. Startup must not reinterpret a member's
	// shared credential as belonging to that member's own default project.
	if err := db.Model(&token).Update("user_id", other.Id).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&project).Update("name", "shared-name")
	if err := migrateTeamProjects(db); err != nil {
		t.Fatal(err)
	}
	db.First(&token, token.Id)
	if token.ProjectID != project.ID {
		t.Fatal("second startup moved shared key")
	}
	db.First(&project, project.ID)
	if project.Name != "shared-name" {
		t.Fatal("second startup reset shared settings")
	}
	var saved User
	db.First(&saved, owner.Id)
	if saved.Quota != 456 {
		t.Fatal("migration moved money")
	}
	var n int64
	db.Model(&Project{}).Count(&n)
	if n != 2 {
		t.Fatalf("not idempotent: %d projects", n)
	}
}

func TestProjectPermissionMatrix(t *testing.T) {
	settings := DefaultProjectSettings()
	roles := []string{ProjectRoleOwner, ProjectRoleAdmin, ProjectRoleMember}
	for _, viewerRole := range roles {
		for _, targetRole := range roles {
			viewer := ProjectMember{ProjectID: 1, UserID: 10, Role: viewerRole, Status: ProjectMemberActive}
			target := ProjectMember{ProjectID: 1, UserID: 20, Role: targetRole, Status: ProjectMemberActive}
			expected := viewerRole == ProjectRoleOwner && targetRole != ProjectRoleOwner || viewerRole == ProjectRoleAdmin && targetRole == ProjectRoleMember
			if got := CanProjectAction(viewer, &target, ProjectManageMember, settings); got != expected {
				t.Errorf("%s manages %s: %v", viewerRole, targetRole, got)
			}
			if got := CanProjectAction(viewer, &target, ProjectInviteMember, settings); got != expected {
				t.Errorf("%s invites %s: %v", viewerRole, targetRole, got)
			}
			target.ProjectID = 2
			if CanProjectAction(viewer, &target, ProjectManagePersonalKey, settings) {
				t.Fatal("cross-project secret access")
			}
		}
	}
	admin := ProjectMember{ProjectID: 1, UserID: 10, Role: ProjectRoleAdmin, Status: ProjectMemberActive}
	if CanProjectAction(admin, nil, ProjectManageBilling, settings) {
		t.Fatal("admin got billing portal")
	}
	if CanProjectAction(admin, &admin, ProjectManageMember, settings) {
		t.Fatal("admin raised own budget")
	}
	member := ProjectMember{ProjectID: 1, UserID: 20, Role: ProjectRoleMember, Status: ProjectMemberActive}
	if !CanProjectAction(member, &member, ProjectManagePersonalKey, settings) {
		t.Fatal("member cannot manage own Key")
	}
	member.Status = ProjectMemberSuspended
	if CanProjectAction(member, &member, ProjectManagePersonalKey, settings) {
		t.Fatal("suspended member manages own Key")
	}
	settings.WhoCanTopup = "owner"
	if CanProjectAction(admin, nil, ProjectTopUp, settings) {
		t.Fatal("owner-only topup bypass")
	}
}

func TestProjectSettingsRejectInvalidValues(t *testing.T) {
	for _, amount := range []billing.MicroUSD{-1, 9_007_199_254_740_992} {
		s := DefaultProjectSettings()
		s.ProjectCapMicrousd = &amount
		if s.Validate() == nil {
			t.Fatalf("accepted %d", amount)
		}
	}
	s := DefaultProjectSettings()
	s.DefaultMemberBudgetMicrousd = nil
	if s.Validate() != nil {
		t.Fatal("unlimited budget rejected")
	}
	zero := billing.MicroUSD(0)
	s.ProjectCapMicrousd = &zero
	if s.Validate() != nil {
		t.Fatal("blocking zero budget rejected")
	}
}

func TestProjectBudgetsIsolateActorAndKeepPlatformCap(t *testing.T) {
	db := setupSpendBudgetTestDB(t)
	if err := db.AutoMigrate(&Project{}, &ProjectSpend{}); err != nil {
		t.Fatal(err)
	}
	rows := []SpendBudget{
		{Scope: SpendBudgetScopeOrg, ProjectID: 0, LimitMicrousd: 10000, Enabled: true, Enforcement: SpendBudgetEnforcementBlock},
		{Scope: SpendBudgetScopeOrg, ProjectID: 1, LimitMicrousd: 1000, Enabled: true, Enforcement: SpendBudgetEnforcementBlock},
		{Scope: SpendBudgetScopeUser, ProjectID: 1, UserID: 20, LimitMicrousd: 100, Enabled: true, Enforcement: SpendBudgetEnforcementBlock},
		{Scope: SpendBudgetScopeUser, ProjectID: 2, UserID: 20, LimitMicrousd: 1, Enabled: true, Enforcement: SpendBudgetEnforcementBlock},
		{Scope: SpendBudgetScopeToken, ProjectID: 1, TokenID: 30, LimitMicrousd: 500, Enabled: true, Enforcement: SpendBudgetEnforcementBlock},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	input := SpendBudgetChargeInput{ProjectID: 1, PayerUserID: 10, ActorUserID: 20, TokenID: 30, RequestID: "member", DeltaMicrousd: 90}
	if err := CheckSpendBudgetBeforeCharge(input); err != nil {
		t.Fatalf("cross-project budget leaked: %v", err)
	}
	if err := RecordSpendBudgetCharge(input); err != nil {
		t.Fatal(err)
	}
	var charge SpendBudgetCharge
	if err := db.Where("budget_id = ?", rows[2].ID).First(&charge).Error; err != nil {
		t.Fatal(err)
	}
	if charge.UserID != 20 || charge.ActorUserID != 20 || charge.PayerUserID != 10 || charge.ProjectID != 1 {
		t.Fatalf("charge attribution: %+v", charge)
	}
	input.RequestID = "blocked"
	input.DeltaMicrousd = 11
	if err := CheckSpendBudgetBeforeCharge(input); err == nil {
		t.Fatal("member cap not enforced")
	}
	// A charge without a resolved actor matches no member budget.
	input.ActorUserID = 0
	input.RequestID = "actorless"
	input.DeltaMicrousd = 200
	if err := CheckSpendBudgetBeforeCharge(input); err != nil {
		t.Fatalf("actorless request charged to a member: %v", err)
	}
	if err := RecordSpendBudgetCharge(input); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&SpendBudgetCharge{}).Where("request_id = ? AND scope = ?", "actorless", SpendBudgetScopeUser).Count(&count)
	if count != 0 {
		t.Fatal("actorless request has personal budget charges")
	}
	if err := db.Model(&rows[0]).Update("limit_microusd", 290).Error; err != nil {
		t.Fatal(err)
	}
	input.RequestID = "global-block"
	input.DeltaMicrousd = 1
	if err := CheckSpendBudgetBeforeCharge(input); err == nil {
		t.Fatal("platform cap no longer applies")
	}
}

func TestProjectRemovalStopsKeysImmediatelyAndUndoRespectsLaterRevocations(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "owner"}
	member := User{Username: "member"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	now := time.Now().UTC().Truncate(time.Second)
	sharedAt := now.Unix()
	project.UpgradedAt = &sharedAt
	if err := db.Save(&project).Error; err != nil {
		t.Fatal(err)
	}
	membership := ProjectMember{ProjectID: project.ID, UserID: member.Id, Role: ProjectRoleMember, Status: ProjectMemberActive}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}
	tokens := []Token{
		{UserId: member.Id, ProjectID: project.ID, Key: strings.Repeat("c", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true},
		{UserId: member.Id, ProjectID: project.ID, Key: strings.Repeat("d", 48), Status: common.TokenStatusDisabled, ExpiredTime: -1, UnlimitedQuota: true},
		{UserId: member.Id, ProjectID: project.ID, Key: strings.Repeat("e", 48), Status: common.TokenStatusEnabled, ExpiredTime: now.Add(time.Hour).Unix(), UnlimitedQuota: true},
	}
	for i := range tokens {
		if err := db.Create(&tokens[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	receipt, err := RemoveProjectMember(project.ID, owner.Id, member.Id, now)
	if err != nil {
		t.Fatal(err)
	}
	// Removal stops every Key at once: there is no grace period. A Key the member
	// had already disabled reports its own state instead of blaming the project.
	for i := range tokens {
		var want error = ErrProjectAccessSuspended
		if tokens[i].Status == common.TokenStatusDisabled {
			want = ErrTokenAccessInactive
		}
		identity, err := ResolveProjectPrincipal(db, tokens[i].Id, now.Add(time.Minute))
		if !errors.Is(err, want) {
			t.Fatalf("removed member key %d still usable: %v", i, err)
		}
		if identity.PayerUserID != owner.Id || identity.ActorUserID != member.Id {
			t.Fatalf("removal changed charging identity: %+v", identity)
		}
	}
	// Even disabling and re-enabling manually after removal is a new operation;
	// undo may not overwrite it or remove the original removal fence.
	db.First(&tokens[0], tokens[0].Id)
	tokens[0].Status = common.TokenStatusDisabled
	if err := tokens[0].Update(); err != nil {
		t.Fatal(err)
	}
	tokens[0].Status = common.TokenStatusEnabled
	if err := tokens[0].Update(); err != nil {
		t.Fatal(err)
	}
	result, err := UndoProjectRemoval(project.ID, owner.Id, member.Id, receipt.PublicID, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RestoredKeyIDs) != 1 || result.RestoredKeyIDs[0] != tokens[2].PublicID || len(result.SkippedKeyIDs) != 2 {
		t.Fatalf("undo overwrote unrelated state: %+v", result)
	}
	if replay, err := UndoProjectRemoval(project.ID, owner.Id, member.Id, receipt.PublicID, now.Add(3*time.Minute)); err != nil || !reflect.DeepEqual(result, replay) {
		t.Fatalf("undo replay changed original receipt: %+v %v", replay, err)
	}
	if _, err := ResolveProjectPrincipal(db, tokens[0].Id, now.Add(3*time.Minute)); !errors.Is(err, ErrProjectAccessSuspended) {
		t.Fatal("undo revived manually changed credential")
	}
	if _, err := ResolveProjectPrincipal(db, tokens[2].Id, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("eligible key not restored: %v", err)
	}
}

// A row written before the service-Key machinery was retired keeps working as
// an ordinary Key. A legacy row with user_id 0 is attributed to the project owner.
// There is one kind of Key and tokens.user_id names its owner. The project
// migration attributes every row written before 2026-09-10, so nothing
// downstream may hand an ownerless row to the project owner: a row that still
// carries user_id 0 is not an identity, whatever is_service says about it.
func TestOwnerlessKeyRowsAreNeverHandedToTheProjectOwner(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var p Project
	db.Where("owner_user_id = ?", owner.Id).First(&p)
	now := time.Now().UTC()
	at := now.Unix()
	p.UpgradedAt = &at
	if err := db.Save(&p).Error; err != nil {
		t.Fatal(err)
	}
	ordinary := Token{UserId: owner.Id, ProjectID: p.ID, UnlimitedQuota: true, Key: strings.Repeat("f", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1}
	if err := db.Create(&ordinary).Error; err != nil {
		t.Fatal(err)
	}
	principal, err := ResolveProjectPrincipal(db, ordinary.Id, now)
	if err != nil {
		t.Fatalf("an owned Key was rejected: %v", err)
	}
	if principal.ActorUserID != owner.Id || principal.PayerUserID != owner.Id {
		t.Fatalf("owner identity %+v", principal)
	}
	// An ownerless row never inherits the owner.
	for _, name := range []string{"malformed"} {
		row := Token{UserId: owner.Id, ProjectID: p.ID, UnlimitedQuota: true, Key: strings.Repeat(name[:1], 48), Status: common.TokenStatusEnabled, ExpiredTime: -1}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&Token{}).Where("id = ?", row.Id).Update("user_id", 0).Error; err != nil {
			t.Fatal(err)
		}
		if got, err := ResolveProjectPrincipal(db, row.Id, now); err == nil {
			t.Fatalf("%s ownerless row was handed to the owner: %+v", name, got)
		}
	}
	// The owner can be removed from nothing, so use a plain suspension instead.
	if err := db.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", p.ID, owner.Id).Update("status", ProjectMemberSuspended).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveProjectPrincipal(db, ordinary.Id, now); !errors.Is(err, ErrProjectAccessSuspended) {
		t.Fatal("suspended owner key still active")
	}
}

func TestProjectResourcesIsolateMembers(t *testing.T) {
	db := setupProjectTestDB(t, true)
	personal := ProjectPrincipal{ProjectID: 10, PayerUserID: 1, ActorUserID: 2, TokenID: 30}
	fixtures := []struct {
		id                    string
		project, actor, token int
	}{
		{"file_member", 10, 2, 30}, {"file_other_project", 11, 2, 30},
		{"file_actorless", 10, 0, 31},
		{"file_other_member", 10, 3, 33},
	}
	for _, f := range fixtures {
		tokenID := f.token
		row := File{ID: f.id, ProjectID: f.project, PayerUserID: 1, ActorUserID: f.actor, UserID: f.actor, TokenID: &tokenID}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		job := MediaJob{PublicID: "job_" + f.id, ProjectID: f.project, PayerUserID: 1, ActorUserID: f.actor, UserID: f.actor, TokenID: &tokenID, Kind: MediaJobKindVideo, ModelName: "test"}
		if err := db.Create(&job).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, want string
		principal  ProjectPrincipal
	}{
		{"member", "file_member", personal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := ListFilesByOwner(tc.principal.ActorUserID, "", 100, "desc", tc.principal)
			if err != nil || len(rows) != 1 || rows[0].ID != tc.want {
				t.Fatalf("files=%+v err=%v", rows, err)
			}
			jobs, err := ListMediaJobsByUser(tc.principal.ActorUserID, "", "", 100, tc.principal)
			if err != nil || len(jobs) != 1 || jobs[0].PublicID != "job_"+tc.want {
				t.Fatalf("jobs=%+v err=%v", jobs, err)
			}
			for _, f := range fixtures {
				if f.id == tc.want {
					continue
				}
				if _, err := GetFileByIDAndOwner(f.id, tc.principal.ActorUserID, tc.principal); !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("cross-resource access %s: %v", f.id, err)
				}
			}
		})
	}
	rows, err := ListFilesByOwner(0, "", 100, "desc")
	if err != nil || len(rows) != 0 {
		t.Fatalf("actor 0 without principal got resources: %+v %v", rows, err)
	}
	// An actorless principal that carries no token id is not an identity and
	// owns nothing.
	anonymous := ProjectPrincipal{ProjectID: 10, PayerUserID: 1, ActorUserID: 0}
	rows, err = ListFilesByOwner(0, "", 100, "desc", anonymous)
	if err != nil || len(rows) != 0 {
		t.Fatalf("actor 0 principal without a token got resources: %+v %v", rows, err)
	}
	// Actor 0 is not an identity even with a token id, and an actorless row is
	// nobody's: no principal, and no Key, reaches it.
	actorless := ProjectPrincipal{ProjectID: 10, PayerUserID: 1, ActorUserID: 0, TokenID: 31}
	rows, err = ListFilesByOwner(0, "", 100, "desc", actorless)
	if err != nil || len(rows) != 0 {
		t.Fatalf("actor 0 with a token id got resources: %+v %v", rows, err)
	}
	for _, id := range []string{"file_member", "file_other_member", "file_other_project", "file_actorless"} {
		if _, err := GetFileByIDAndOwner(id, 0, actorless); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("actorless principal reached %s: %v", id, err)
		}
	}
	// The Key that wrote the actorless row cannot reach it either, now that its
	// own principal names a real person.
	owned := ProjectPrincipal{ProjectID: 10, PayerUserID: 1, ActorUserID: 2, TokenID: 31}
	if _, err := GetFileByIDAndOwner("file_actorless", 2, owned); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("an attributed principal reached an actorless row: %v", err)
	}
}

// The locked rule: a Key may access resources of its own logical Key only.
// Holding two Keys must not let either one read the other's files and jobs, nor
// reach console uploads that never carried a token id. A console principal has
// no token id and keeps reading everything the member itself produced.
func TestProjectResourceScopeIsolatesSiblingKeysOfOneMember(t *testing.T) {
	db := setupProjectTestDB(t, true)
	keyA, keyB := 41, 42
	fixtures := []struct {
		id    string
		token *int
	}{{"file_key_a", &keyA}, {"file_key_b", &keyB}, {"file_console", nil}}
	for _, f := range fixtures {
		row := File{ID: f.id, ProjectID: 12, PayerUserID: 1, ActorUserID: 5, UserID: 5, TokenID: f.token}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		job := MediaJob{PublicID: "job_" + f.id, ProjectID: 12, PayerUserID: 1, ActorUserID: 5, UserID: 5, TokenID: f.token, Kind: MediaJobKindVideo, ModelName: "test"}
		if err := db.Create(&job).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Rotation reuses the tokens row, so the token id is the logical Key: Key A
	// sees its own history and nothing else.
	principalA := ProjectPrincipal{ProjectID: 12, PayerUserID: 1, ActorUserID: 5, TokenID: keyA}
	rows, err := ListFilesByOwner(5, "", 100, "desc", principalA)
	if err != nil || len(rows) != 1 || rows[0].ID != "file_key_a" {
		t.Fatalf("Key A saw sibling resources: %+v %v", rows, err)
	}
	jobs, err := ListMediaJobsByUser(5, "", "", 100, principalA)
	if err != nil || len(jobs) != 1 || jobs[0].PublicID != "job_file_key_a" {
		t.Fatalf("Key A saw sibling jobs: %+v %v", jobs, err)
	}
	for _, id := range []string{"file_key_b", "file_console"} {
		if _, err := GetFileByIDAndOwner(id, 5, principalA); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("Key A reached %s: %v", id, err)
		}
	}
	if _, err := GetMediaJobByPublicIDAndUser("job_file_key_b", 5, principalA); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("Key A reached another Key's job: %v", err)
	}
	// The console carries no token id: the member still reads its own rows,
	// including the upload no Key ever produced.
	console := ProjectPrincipal{ProjectID: 12, PayerUserID: 1, ActorUserID: 5}
	rows, err = ListFilesByOwner(5, "", 100, "desc", console)
	if err != nil || len(rows) != 3 {
		t.Fatalf("console read lost the member's own rows: %+v %v", rows, err)
	}
	if _, err := GetFileByIDAndOwner("file_console", 5, console); err != nil {
		t.Fatalf("console read lost its own upload: %v", err)
	}
}

func TestProjectMemberChangesQueueOnceAndRollbackOnNotificationFailure(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "notice-owner"}
	target := User{Username: "notice-target", Email: "target@example.com"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	member := ProjectMember{ProjectID: project.ID, UserID: target.Id, Role: ProjectRoleMember, Status: ProjectMemberActive}
	if err := db.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := UpdateProjectMemberRoleOrStatus(project.ID, owner.Id, target.Id, ProjectRoleAdmin, ""); err != nil {
			t.Fatal(err)
		}
	}
	var notices []ProjectNotification
	if err := db.Find(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 {
		t.Fatalf("duplicate notification events: %d", len(notices))
	}
	mail, err := notices[0].Email()
	if err != nil || mail.Receiver != target.Email || strings.Contains(notices[0].Payload, target.Email) {
		t.Fatalf("encrypted notification: %v", err)
	}
	if err := db.Callback().Create().Before("gorm:create").Register("project_notification_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "project_notifications" {
			tx.AddError(errors.New("queue unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateProjectMemberRoleOrStatus(project.ID, owner.Id, target.Id, "", ProjectMemberSuspended); err == nil {
		t.Fatal("membership committed without reliable notification")
	}
	if err := db.First(&member, member.ID).Error; err != nil {
		t.Fatal(err)
	}
	if member.Status != ProjectMemberActive || member.Version != 2 {
		t.Fatalf("failed transaction changed member: %+v", member)
	}
}

func TestProjectNotificationDeduplicatesAndEncryptsInvitationLink(t *testing.T) {
	db := setupProjectTestDB(t, true)
	email := ProjectEmail{Receiver: "grace@example.com", Subject: "Invitation", HTML: "https://console.example/invite/secret-one-time-token"}
	for i := 0; i < 2; i++ {
		if err := QueueProjectEmailTx(db, 1, 2, "invite-event", email); err != nil {
			t.Fatal(err)
		}
	}
	var notices []ProjectNotification
	if err := db.Find(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || strings.Contains(notices[0].Payload, "secret-one-time-token") {
		t.Fatal("invitation plaintext persisted or event duplicated")
	}
	decoded, err := notices[0].Email()
	if err != nil || decoded != email {
		t.Fatalf("decode mismatch: %v", err)
	}
	notices[0].EventKey = "another-event"
	if _, err := notices[0].Email(); err == nil {
		t.Fatal("encrypted payload can be transplanted between events")
	}
}

func invitationRawFromQueue(t *testing.T, db *gorm.DB, digest string) string {
	t.Helper()
	var notice ProjectNotification
	if err := db.Where("event_key = ?", "invite:"+digest).First(&notice).Error; err != nil {
		t.Fatal(err)
	}
	email, err := notice.Email()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(email.HTML, "/invite/")
	if len(parts) != 2 || len(parts[1]) < 48 {
		t.Fatal("invitation link missing")
	}
	return parts[1][:48]
}

func TestProjectInvitationSnapshotsResendAndVerifiedAcceptance(t *testing.T) {
	db := setupProjectTestDB(t, true)
	t.Setenv("PROJECTS_ENABLED", "true")
	owner := User{Username: "invite-owner"}
	member := User{Username: "invite-member", Email: "grace@example.com"}
	admin := User{Username: "invite-admin", Email: "admin@example.com"}
	for _, user := range []*User{&owner, &member, &admin} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	adminMembership := ProjectMember{ProjectID: project.ID, UserID: admin.Id, Role: ProjectRoleAdmin, Status: ProjectMemberActive}
	if err := db.Create(&adminMembership).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := CreateProjectInvite(project.ID, admin.Id, CreateProjectInviteInput{Email: member.Email, Role: ProjectRoleAdmin, BaseURL: "https://console.example"}, now); !errors.Is(err, ErrProjectForbidden) {
		t.Fatalf("admin invited admin: %v", err)
	}
	invite, err := CreateProjectInvite(project.ID, owner.Id, CreateProjectInviteInput{Email: member.Email, Role: ProjectRoleMember, BaseURL: "https://console.example"}, now)
	if err != nil {
		t.Fatal(err)
	}
	raw := invitationRawFromQueue(t, db, invite.TokenDigest)
	if invite.BudgetMicrousd == nil || *invite.BudgetMicrousd != 100_000_000 || invite.WarningPct != 80 {
		t.Fatalf("wrong invitation snapshot: %+v", invite)
	}
	if _, err := CreateProjectInvite(project.ID, owner.Id, CreateProjectInviteInput{Email: member.Email, Role: ProjectRoleMember}, now); !errors.Is(err, ErrProjectAlreadyInvited) {
		t.Fatalf("duplicate pending invitation: %v", err)
	}
	settings := DefaultProjectSettings()
	changed := billing.MicroUSD(200_000_000)
	settings.DefaultMemberBudgetMicrousd = &changed
	settings.WarningPct = 90
	settings.DefaultReasoningEffortCap = "low"
	if err := db.First(&project, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateProjectSettings(project.ID, owner.Id, project.Version, nil, &settings); err != nil {
		t.Fatal(err)
	}
	var rate ProjectInviteRateError
	if _, err := ChangeProjectInvite(project.ID, owner.Id, invite.PublicID, true, "https://console.example", now.Add(30*time.Second)); !errors.As(err, &rate) || rate.RetryAfter != 30 {
		t.Fatalf("resend throttle: %v", err)
	}
	resent, err := ChangeProjectInvite(project.ID, owner.Id, invite.PublicID, true, "https://console.example", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LookupProjectInvite(raw); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("old link still valid: %v", err)
	}
	raw = invitationRawFromQueue(t, db, resent.TokenDigest)
	if *resent.BudgetMicrousd != 100_000_000 || resent.WarningPct != 80 {
		t.Fatal("resend changed original budget snapshot")
	}
	if _, err := AcceptProjectInvite(raw, admin.Id, now.Add(2*time.Minute), "https://console.example"); !errors.Is(err, ErrProjectEmailMismatch) {
		t.Fatalf("wrong mailbox accepted: %v", err)
	}
	if _, err := AcceptProjectInvite(raw, member.Id, now.Add(2*time.Minute), "https://console.example"); !errors.Is(err, ErrProjectEmailUnverified) {
		t.Fatalf("unverified mailbox accepted: %v", err)
	}
	if err := db.Model(&member).Update("verified_email", member.Email).Error; err != nil {
		t.Fatal(err)
	}
	accepted, err := AcceptProjectInvite(raw, member.Id, now.Add(2*time.Minute), "https://console.example")
	if err != nil {
		t.Fatal(err)
	}
	if accepted.ReasoningEffortCap != "" {
		t.Fatalf("pending invitation inherited changed default: %s", accepted.ReasoningEffortCap)
	}
	var joinedNotice ProjectNotification
	if err := db.Where("event_key = ?", "accepted:"+invite.PublicID).First(&joinedNotice).Error; err != nil {
		t.Fatal(err)
	}
	joinedEmail, err := joinedNotice.Email()
	if err != nil {
		t.Fatal(err)
	}
	if joinedEmail.Receiver != member.Email || !strings.Contains(joinedEmail.HTML, "$100.00") || !strings.Contains(joinedEmail.HTML, "project_id="+project.PublicID) || !strings.Contains(joinedEmail.Subject, project.Name) {
		t.Fatal("joined mail lost recipient, invitation budget snapshot or project destination")
	}
	repeated, err := AcceptProjectInvite(raw, member.Id, now.Add(3*time.Minute), "https://console.example")
	if err != nil || repeated.ID != accepted.ID || repeated.Version != accepted.Version {
		t.Fatalf("accept replay changed membership: %+v %v", repeated, err)
	}
	budget, warning, err := readMemberBudgetTx(db, project.ID, member.Id)
	if err != nil || budget == nil || *budget != 100_000_000 || warning != 80 {
		t.Fatalf("accepted budget changed: %v %d %v", budget, warning, err)
	}
	var historyCount int64
	if err := db.Model(&ProjectBudgetHistory{}).Where("project_id = ? AND user_id = ?", project.ID, member.Id).Count(&historyCount).Error; err != nil {
		t.Fatal(err)
	}
	if historyCount != 1 {
		t.Fatalf("repeated acceptance changed budget history: %d", historyCount)
	}
	t.Setenv("PROJECTS_ENABLED", "false")
	if _, err := AcceptProjectInvite(raw, member.Id, now.Add(3*time.Minute), "https://console.example"); !errors.Is(err, ErrProjectFeatureUnavailable) {
		t.Fatalf("disabled feature can be bypassed: %v", err)
	}
}

func TestVerifiedEmailDoesNotFollowProfileEmailChanges(t *testing.T) {
	user := User{Email: "grace@example.com", VerifiedEmail: "grace@example.com"}
	if !user.HasVerifiedEmail() {
		t.Fatal("verified mailbox rejected")
	}
	user.Email = "different@example.com"
	if user.HasVerifiedEmail() {
		t.Fatal("changed mailbox inherited verification")
	}
}

func TestProjectBudgetRequestFinalizationIsIdempotentAndRoleBounded(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "request-owner"}
	admin := User{Username: "request-admin"}
	member := User{Username: "request-member", Email: "grace@example.com"}
	for _, user := range []*User{&owner, &admin, &member} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	for _, membership := range []ProjectMember{
		{ProjectID: project.ID, UserID: admin.Id, Role: ProjectRoleAdmin, Status: ProjectMemberActive},
		{ProjectID: project.ID, UserID: member.Id, Role: ProjectRoleMember, Status: ProjectMemberActive},
	} {
		if err := db.Create(&membership).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CreateProjectBudgetRequest(project.ID, admin.Id, 200, "raise"); !errors.Is(err, ErrProjectForbidden) {
		t.Fatalf("admin submitted own budget request: %v", err)
	}
	request, err := CreateProjectBudgetRequest(project.ID, member.Id, 200, "raise")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProjectBudgetRequest(project.ID, member.Id, 300, "another"); !errors.Is(err, ErrProjectRequestPending) {
		t.Fatalf("duplicate pending request: %v", err)
	}
	amount := billing.MicroUSD(150)
	for i := 0; i < 2; i++ {
		if _, err := HandleProjectBudgetRequest(project.ID, admin.Id, request.PublicID, "approved", &amount, "", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := HandleProjectBudgetRequest(project.ID, admin.Id, request.PublicID, "rejected", nil, "No longer approved", time.Now().UTC()); !errors.Is(err, ErrProjectStateConflict) {
		t.Fatalf("overwrote approved request: %v", err)
	}
	if _, err := HandleProjectBudgetRequest(project.ID, member.Id, request.PublicID, "withdrawn", nil, "", time.Now().UTC()); !errors.Is(err, ErrProjectStateConflict) {
		t.Fatalf("withdrew finalized request: %v", err)
	}
	if err := db.First(&request, request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if request.ApprovedBudgetMicrousd == nil || *request.ApprovedBudgetMicrousd != amount {
		t.Fatal("approval history lost the actual amount")
	}
	cap, _, err := readMemberBudgetTx(db, project.ID, member.Id)
	if err != nil || cap == nil || *cap != amount {
		t.Fatalf("approved cap: %v %v", cap, err)
	}
	var historyCount, noticeCount int64
	db.Model(&ProjectBudgetHistory{}).Count(&historyCount)
	db.Model(&ProjectNotification{}).Count(&noticeCount)
	if historyCount != 1 || noticeCount != 1 {
		t.Fatalf("duplicate effects history=%d notices=%d", historyCount, noticeCount)
	}
	second, err := CreateProjectBudgetRequest(project.ID, member.Id, 300, "raise")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateProjectMemberRoleOrStatus(project.ID, owner.Id, member.Id, ProjectRoleAdmin, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := HandleProjectBudgetRequest(project.ID, admin.Id, second.PublicID, "approved", nil, "", time.Now().UTC()); !errors.Is(err, ErrProjectForbidden) {
		t.Fatalf("request bypassed target promotion: %v", err)
	}
}

func TestProjectTokenRotationKeepsLogicalIdentityBudgetAndExpiry(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "rotation-owner"}
	member := User{Username: "rotation-member", Email: "grace@example.com"}
	for _, user := range []*User{&owner, &member} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := now.Unix()
	project.UpgradedAt = &at
	if err := db.Save(&project).Error; err != nil {
		t.Fatal(err)
	}
	membership := ProjectMember{ProjectID: project.ID, UserID: member.Id, Role: ProjectRoleMember, Status: ProjectMemberActive}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}
	key, err := common.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	token := Token{ProjectID: project.ID, UserId: member.Id, Key: key, Name: "rotate", RemainQuota: 300, ExpiredTime: now.Add(time.Hour).Unix(), Status: common.TokenStatusEnabled}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	cap := billing.MicroUSD(500)
	if err := setProjectBudgetTx(db, project.ID, 0, token.Id, &cap, 80); err != nil {
		t.Fatal(err)
	}
	if err := RecordSpendBudgetCharge(SpendBudgetChargeInput{ProjectID: project.ID, PayerUserID: owner.Id, ActorUserID: member.Id, TokenID: token.Id, RequestID: "before-rotation", DeltaMicrousd: 25, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	first, err := RotateProjectToken(project.ID, owner.Id, token.Id, "rotation-operation", "24h", now)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := RotateProjectToken(project.ID, owner.Id, token.Id, "rotation-operation", "24h", now.Add(time.Second))
	if err != nil || repeat != first {
		t.Fatalf("rotation replay: %+v %v", repeat, err)
	}
	if first.Key == key || first.OldExpiresAt != token.ExpiredTime {
		t.Fatalf("rotation expiry: %+v", first)
	}
	old, err := ResolveTokenCredential(token.Id, key, now.Add(time.Minute))
	if err != nil || old.Id != token.Id {
		t.Fatalf("overlap lost logical identity: %v", err)
	}
	if _, err := ResolveTokenCredential(token.Id, key, now.Add(2*time.Hour)); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("old credential exceeded original expiry: %v", err)
	}
	current, err := ResolveTokenCredential(token.Id, first.Key, now.Add(time.Minute))
	if err != nil || current.Id != token.Id || current.UserId != member.Id || current.RemainQuota != 300 {
		t.Fatalf("rotation reset identity or quota: %+v %v", current, err)
	}
	var charges []SpendBudgetCharge
	if err := db.Find(&charges).Error; err != nil {
		t.Fatal(err)
	}
	if len(charges) != 1 || charges[0].AmountMicrousd != 25 {
		t.Fatal("rotation reset budget consumption")
	}
	var credentials int64
	db.Model(&TokenCredential{}).Count(&credentials)
	if credentials != 1 {
		t.Fatalf("replay produced extra credentials: %d", credentials)
	}
	if _, err := RotateProjectToken(project.ID, owner.Id, token.Id, "rotation-operation", "now", now); !errors.Is(err, ErrProjectStateConflict) {
		t.Fatalf("replay changed overlap policy: %v", err)
	}
}

func TestProjectKeyLimitCountsDisabledAndExpired(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "limit-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < ProjectKeyLimit; i++ {
		key, err := common.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		token := Token{Name: fmt.Sprintf("personal-%d", i), Key: key, UnlimitedQuota: true}
		if err := CreateProjectPersonalToken(project.ID, owner.Id, &token); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := db.Model(&token).Update("status", common.TokenStatusDisabled).Error; err != nil {
				t.Fatal(err)
			}
		}
		if i == 1 {
			if err := db.Model(&token).Update("expired_time", time.Now().Add(-time.Hour).Unix()).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	key, err := common.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateProjectPersonalToken(project.ID, owner.Id, &Token{Name: "eleventh", Key: key}); !errors.Is(err, ErrProjectKeyLimit) {
		t.Fatalf("key limit bypassed: %v", err)
	}
	var count int64
	db.Model(&Token{}).Where("project_id = ?", project.ID).Count(&count)
	if count != ProjectKeyLimit {
		t.Fatalf("unexpected token count: %d", count)
	}
}

// A retired service row carries user_id 0 and is attributed to the project
// owner, so it must occupy one of the owner's slots. Counting only rows whose
// user_id matches would let nine ordinary Keys plus one legacy row admit an
// eleventh.
func TestProjectBatchDeleteRejectsForeignKeyAtomically(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner, foreign := User{Username: "batch-owner"}, User{Username: "batch-foreign"}
	for _, u := range []*User{&owner, &foreign} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	ownKey, foreignKey := Token{UserId: owner.Id, Name: "own", Key: "batch-own-key"}, Token{UserId: foreign.Id, Name: "foreign", Key: "batch-foreign-key"}
	for _, key := range []*Token{&ownKey, &foreignKey} {
		if err := db.Create(key).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := DeleteProjectTokens(project.ID, owner.Id, []int{ownKey.Id, foreignKey.Id}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-project batch: %v", err)
	}
	var remaining int64
	db.Model(&Token{}).Count(&remaining)
	if remaining != 2 {
		t.Fatal("partially deleted batch")
	}
	deleted, err := DeleteProjectTokens(project.ID, owner.Id, []int{ownKey.Id, ownKey.Id})
	if err != nil || len(deleted) != 1 {
		t.Fatalf("own batch: %+v %v", deleted, err)
	}
}

func TestProjectCredentialLegacyFormsAndRotationRevocation(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "credential-forms"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	key := Token{UserId: owner.Id, Key: "legacy-short-test-key", ExpiredTime: -1, UnlimitedQuota: true, Status: common.TokenStatusEnabled}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, raw := range []string{key.Key, "sk-" + key.Key, common.APITokenPrefix + key.Key} {
		if _, err := ResolveTokenCredential(key.Id, raw, now); err != nil {
			t.Fatalf("legacy form: %v", err)
		}
	}
	rotated, err := RotateProjectToken(key.ProjectID, owner.Id, key.Id, "legacy-rotation", "now", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{key.Key, "sk-" + key.Key, common.APITokenPrefix + key.Key} {
		if _, err := ResolveTokenCredential(key.Id, raw, now); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("revoked legacy alias: %v", err)
		}
	}
	if _, err := ResolveTokenCredential(key.Id, rotated.Key, now); err != nil {
		t.Fatal(err)
	}
}

func TestProjectBudgetsCountSpendBeforeLimitAndWhileUnlimited(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "midmonth-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	now := time.Now().UTC()
	input := SpendBudgetChargeInput{ProjectID: project.ID, PayerUserID: owner.Id, ActorUserID: owner.Id, TokenID: 23, RequestID: "before-cap", DeltaMicrousd: 70, OccurredAt: now}
	if err := RecordSpendBudgetCharge(input); err != nil {
		t.Fatal(err)
	}
	cap := billing.MicroUSD(100)
	if err := setProjectBudgetTx(db, project.ID, owner.Id, 0, &cap, 80); err != nil {
		t.Fatal(err)
	}
	input.RequestID = "over-cap"
	input.DeltaMicrousd = 31
	if err := CheckSpendBudgetBeforeCharge(input); err == nil {
		t.Fatal("new cap forgot earlier consumption")
	}
	if err := setProjectBudgetTx(db, project.ID, owner.Id, 0, nil, 80); err != nil {
		t.Fatal(err)
	}
	input.RequestID = "unlimited-spend"
	input.DeltaMicrousd = 20
	if err := RecordSpendBudgetCharge(input); err != nil {
		t.Fatal(err)
	}
	if err := setProjectBudgetTx(db, project.ID, owner.Id, 0, &cap, 80); err != nil {
		t.Fatal(err)
	}
	input.RequestID = "after-reenable"
	input.DeltaMicrousd = 11
	if err := CheckSpendBudgetBeforeCharge(input); err == nil {
		t.Fatal("re-enabled cap forgot unlimited consumption")
	}
}

func TestProjectNewBudgetIncludesInFlightReservations(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "inflight-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	input := SpendBudgetChargeInput{ProjectID: project.ID, PayerUserID: owner.Id, ActorUserID: owner.Id, TokenID: 42, RequestID: "inflight-before-cap", DeltaMicrousd: 75, OccurredAt: time.Now().UTC()}
	if err := CheckSpendBudgetBeforeCharge(input); err != nil {
		t.Fatal(err)
	}
	cap := billing.MicroUSD(100)
	if err := setProjectBudgetTx(db, project.ID, 0, 0, &cap, 80); err != nil {
		t.Fatal(err)
	}
	input.RequestID = "new-admission"
	input.DeltaMicrousd = 26
	if err := CheckSpendBudgetBeforeCharge(input); err == nil {
		t.Fatal("new cap ignored in-flight reservation")
	}
}

func TestProjectSpendMigrationPreservesLegacyAndReplaysOnce(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&ProjectDataMigration{}, &Log{}); err != nil {
		t.Fatal(err)
	}
	oldLog := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = oldLog })
	user := User{Username: "migration-spend"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", user.Id).First(&project)
	now := time.Now().UTC()
	cap := billing.MicroUSD(1000)
	if err := setProjectBudgetTx(db, project.ID, user.Id, 0, &cap, 80); err != nil {
		t.Fatal(err)
	}
	var budget SpendBudget
	db.Where("project_id = ? AND user_id = ?", project.ID, user.Id).First(&budget)
	charge := SpendBudgetCharge{ProjectID: project.ID, PayerUserID: user.Id, ActorUserID: user.Id, BudgetID: budget.ID, UserID: user.Id, RequestID: "already-recorded", AmountMicrousd: 40, CreatedAt: now}
	if err := db.Create(&charge).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []Log{{Type: LogTypeConsume, UserId: user.Id, RequestID: "already-recorded", AmountMicrousd: 40, CreatedAt: now.Unix()}, {Type: LogTypeConsume, UserId: user.Id, RequestID: "before-budget-existed", AmountMicrousd: 25, CreatedAt: now.Unix()}} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := MigrateProjectSpendHistory(); err != nil {
			t.Fatal(err)
		}
	}
	used, err := ReadSpendBudgetUsed(budget, now)
	if err != nil || used != 65 {
		t.Fatalf("backfill counted twice or lost spend: %d %v", used, err)
	}
	if err := setProjectBudgetTx(db, project.ID, 0, 0, &cap, 80); err != nil {
		t.Fatal(err)
	}
	var projectBudget SpendBudget
	db.Where("project_id = ? AND scope = ?", project.ID, SpendBudgetScopeOrg).First(&projectBudget)
	used, err = ReadSpendBudgetUsed(projectBudget, now)
	if err != nil || used != 65 {
		t.Fatalf("new cap lost pre-budget history: %d %v", used, err)
	}
	var count int64
	db.Model(&ProjectSpend{}).Count(&count)
	if count != 2 {
		t.Fatalf("event count %d", count)
	}
}

func TestProjectPaymentAndInvoiceAreIndependentIdempotentStates(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	oldLog := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = oldLog })
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "billing-owner", Email: "owner@example.com", Quota: 25}
	admin := User{Username: "billing-admin", Email: "admin@example.com", Quota: 77}
	for _, user := range []*User{&owner, &admin} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	setTestProjectStripeCustomer(t, db, project.ID, "cus_project")
	originalProjectName := project.Name
	at := time.Now().Unix()
	db.Model(&project).Update("upgraded_at", at)
	if err := db.Create(&ProjectMember{ProjectID: project.ID, UserID: admin.Id, Role: ProjectRoleAdmin, Status: ProjectMemberActive}).Error; err != nil {
		t.Fatal(err)
	}
	for index, invoiceFirst := range []bool{true, false} {
		operation := fmt.Sprintf("payment-%d", index)
		order, err := BeginProjectTopUp(project.ID, admin.Id, operation, 10)
		if err != nil {
			t.Fatal(err)
		}
		if order.StripeProjectShared == nil || !*order.StripeProjectShared || order.StripeProjectName != project.Name {
			t.Fatalf("checkout project identity was not snapshotted: %+v", order)
		}
		replay, err := BeginProjectTopUp(project.ID, admin.Id, operation, 10)
		if err != nil || replay.Id != order.Id {
			t.Fatalf("order replay %v", err)
		}
		if _, err := BeginProjectTopUp(project.ID, admin.Id, operation, 11); !errors.Is(err, ErrProjectStateConflict) {
			t.Fatalf("different amount replay: %v", err)
		}
		if index == 0 {
			if err := db.Model(&project).Update("name", "Renamed after checkout").Error; err != nil {
				t.Fatal(err)
			}
		}
		invoice := ProjectInvoiceUpdate{Reference: order.TradeNo, CustomerID: "cus_project", InvoiceID: fmt.Sprintf("in_%d", index), Number: "INV-1", HostedURL: "https://invoice.stripe.com/i/test", PDFURL: "https://pay.stripe.com/invoice/test/pdf", Ready: true, BillTo: JSONValue(`{"name":"Project Company"}`)}
		paymentMails := func() []ProjectNotification {
			var rows []ProjectNotification
			if err := db.Where("event_key LIKE ?", "payment:"+order.PublicID+":%").Order("event_key").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			return rows
		}
		if invoiceFirst {
			if err := UpdateProjectInvoice(invoice); err != nil {
				t.Fatal(err)
			}
			if rows := paymentMails(); len(rows) != 0 {
				t.Fatal("payment email was queued before payment")
			}
		}
		receipt := ProjectPaymentReceipt{Reference: order.TradeNo, CustomerID: "cus_project", SessionID: fmt.Sprintf("cs_%d", index), PaymentIntentID: fmt.Sprintf("pi_%d", index), PaidMicrousd: 10_000_000, Currency: "USD", Method: "stripe"}
		creditedAt := time.Now().Unix()
		paid, changed, err := CompleteProjectTopUp(receipt)
		if err != nil || !changed || paid.Status != common.TopUpStatusSuccess {
			t.Fatalf("credit %v changed=%v", err, changed)
		}
		if !invoiceFirst {
			if err := UpdateProjectInvoice(invoice); err != nil {
				t.Fatal(err)
			}
		}
		// Credited, receipt still missing: one waiting e-mail per recipient,
		// due a minute later and without the invoice link.
		waiting := paymentMails()
		if len(waiting) != 2 {
			t.Fatalf("waiting payment emails %d", len(waiting))
		}
		for _, row := range waiting {
			email, err := row.Email()
			if err != nil || row.AvailableAt < creditedAt+int64(paymentEmailFallbackDelay/time.Second) || strings.Contains(email.HTML, "View invoice") || !strings.Contains(email.HTML, "Top-up ID") {
				t.Fatalf("fallback payment email is wrong: available=%d %v", row.AvailableAt, err)
			}
		}
		if err := UpdateProjectPaymentReceipt(order.TradeNo, "cus_project", fmt.Sprintf("pi_%d", index), fmt.Sprintf("ch_%d", index), "https://pay.stripe.com/receipts/test", "alipay"); err != nil {
			t.Fatal(err)
		}
		// Documents complete inside the minute: the same rows now carry the
		// invoice link and are due immediately.
		ready := paymentMails()
		if len(ready) != 2 {
			t.Fatalf("payment emails after documents %d", len(ready))
		}
		for _, row := range ready {
			email, err := row.Email()
			if err != nil || row.AvailableAt > time.Now().Unix() || !strings.Contains(email.HTML, "document=invoice_page") || !strings.Contains(email.HTML, "View invoice") || !strings.Contains(email.HTML, "INV-1") ||
				!strings.Contains(email.HTML, "Paid by") || !strings.Contains(email.HTML, "billing-admin") || !strings.Contains(email.HTML, "your Lazu account") ||
				strings.Contains(email.HTML, "Renamed after checkout") || strings.Contains(email.HTML, project.Name) || strings.Contains(email.HTML, "document=receipt") {
				t.Fatalf("payment email after documents is wrong: %v", err)
			}
			if email.Subject != "$10.00 added to your Lazu account" {
				t.Fatalf("payment email subject %q", email.Subject)
			}
		}
		if index == 0 {
			if err := db.Model(&project).Update("name", originalProjectName).Error; err != nil {
				t.Fatal(err)
			}
			project.Name = originalProjectName
		}
		if _, changed, err := CompleteProjectTopUp(receipt); err != nil || changed {
			t.Fatalf("duplicate credit %v changed=%v", err, changed)
		}
		if err := ExpireProjectTopUp(order.TradeNo, false); err != nil {
			t.Fatal(err)
		}
		invoice.Ready = false
		invoice.Failed = true
		if err := UpdateProjectInvoice(invoice); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&order, order.Id).Error; err != nil {
			t.Fatal(err)
		}
		if order.InvoiceStatus != "ready" || order.Status != common.TopUpStatusSuccess || order.PaidByUserID != admin.Id || order.UserId != owner.Id {
			t.Fatalf("regressed states/identity %+v", order)
		}
	}
	var payer, operator User
	db.First(&payer, owner.Id)
	db.First(&operator, admin.Id)
	if payer.Quota != 20_000_025 || operator.Quota != 77 {
		t.Fatalf("wrong wallet/customer: payer=%d admin=%d", payer.Quota, operator.Quota)
	}
	var notices int64
	db.Model(&ProjectNotification{}).Count(&notices)
	if notices != 4 {
		t.Fatalf("one payment email per recipient per order, got %d", notices)
	}
}

func TestPaymentEmailIsNeverSentTwice(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "once-owner", Email: "once@example.com"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	setTestProjectStripeCustomer(t, db, project.ID, "cus_once")
	order, err := BeginProjectTopUp(project.ID, owner.Id, "once", 5, ProjectCheckoutConfig{ReturnBase: "https://lazu.ai"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := CompleteProjectTopUp(ProjectPaymentReceipt{Reference: order.TradeNo, CustomerID: "cus_once", SessionID: "cs_once", PaymentIntentID: "pi_once", PaidMicrousd: 5_000_000, Currency: "USD", Method: "wechat_pay"}); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("payment:%s:%d", order.PublicID, owner.Id)
	var fallback ProjectNotification
	if err := db.Where("event_key = ?", key).First(&fallback).Error; err != nil {
		t.Fatal(err)
	}
	// The minute passed and the fallback went out.
	if err := db.Model(&fallback).Updates(map[string]interface{}{"status": "sent"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := UpdateProjectInvoice(ProjectInvoiceUpdate{Reference: order.TradeNo, CustomerID: "cus_once", InvoiceID: "in_once", Number: "INV-ONCE", HostedURL: "https://invoice.stripe.com/i/once", PDFURL: "https://pay.stripe.com/invoice/once/pdf", Ready: true}); err != nil {
		t.Fatal(err)
	}
	if err := UpdateProjectPaymentReceipt(order.TradeNo, "cus_once", "pi_once", "ch_once", "https://pay.stripe.com/receipts/once", "wechat_pay"); err != nil {
		t.Fatal(err)
	}
	var rows []ProjectNotification
	if err := db.Where("event_key LIKE ?", "payment:"+order.PublicID+":%").Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].Payload != fallback.Payload || rows[0].Status != "sent" {
		t.Fatalf("a sent payment email was replaced or duplicated: %d %v", len(rows), err)
	}
	email, err := rows[0].Email()
	if err != nil || !strings.Contains(email.HTML, "WeChat Pay") || !strings.Contains(email.HTML, "$5.00") {
		t.Fatalf("fallback payment email content: %v", err)
	}
	missing, err := ListProjectTopUpsMissingDocumentEmails(50, time.Now().Add(-time.Hour).Unix())
	if err != nil || len(missing) != 0 {
		t.Fatalf("sent fallback must count as the payment email: %d %v", len(missing), err)
	}
}

func TestPersonalTopUpEmailNamesTheAccountAndLinksToThePayment(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "grace-1", Email: "grace@example.com"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	setTestProjectStripeCustomer(t, db, project.ID, "cus_personal")
	order, err := BeginProjectTopUp(project.ID, owner.Id, "personal-email", 5, ProjectCheckoutConfig{ReturnBase: "https://lazu.ai"})
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := CompleteProjectTopUp(ProjectPaymentReceipt{Reference: order.TradeNo, CustomerID: "cus_personal", SessionID: "cs_personal", PaidMicrousd: 5_000_000, Currency: "USD", Method: "card"}); err != nil || !changed {
		t.Fatalf("complete personal top-up: changed=%v err=%v", changed, err)
	}
	var notice ProjectNotification
	if err := db.Where("event_key = ?", "payment:"+order.PublicID+":"+fmt.Sprint(owner.Id)).First(&notice).Error; err != nil {
		t.Fatal(err)
	}
	email, err := notice.Email()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(email.Subject, "grace-1") || strings.Contains(email.HTML, "to grace-1") || !strings.Contains(email.HTML, "your Lazu account") || !strings.Contains(email.HTML, "/console/wallet?project_id="+project.PublicID) || !strings.Contains(email.HTML, "top_up_id="+order.PublicID) || !strings.Contains(email.HTML, "lazu-email-logo-paper.png") {
		t.Fatalf("personal email has wrong beneficiary or payment link: %s %s", email.Subject, email.HTML)
	}
}

func TestFullyDiscountedTopUpSendsInvoiceWithoutReceipt(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "discount-owner", Email: "discount@example.com"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	setTestProjectStripeCustomer(t, db, project.ID, "cus_discount")
	order, err := BeginProjectTopUp(project.ID, owner.Id, "free-promotion", 5, ProjectCheckoutConfig{PromotionCodes: true, ReturnBase: "https://lazu.ai"})
	if err != nil {
		t.Fatal(err)
	}
	if paid, changed, err := CompleteProjectTopUp(ProjectPaymentReceipt{Reference: order.TradeNo, CustomerID: "cus_discount", SessionID: "cs_discount", PaidMicrousd: 0, Currency: "USD"}); err != nil || !changed || paid.PaymentMethod != "promotion" {
		t.Fatalf("fully discounted credit: changed=%v err=%v", changed, err)
	}
	if err := UpdateProjectInvoice(ProjectInvoiceUpdate{Reference: order.TradeNo, CustomerID: "cus_discount", InvoiceID: "in_discount", Number: "INV-FREE", HostedURL: "https://invoice.stripe.com/i/free", PDFURL: "https://pay.stripe.com/invoice/free/pdf", Ready: true}); err != nil {
		t.Fatal(err)
	}
	var notice ProjectNotification
	if err := db.Where("event_key LIKE ?", "payment:"+order.PublicID+":%").First(&notice).Error; err != nil {
		t.Fatal(err)
	}
	email, err := notice.Email()
	if err != nil || !strings.Contains(email.HTML, "document=invoice_page") || strings.Contains(email.HTML, "document=receipt") || notice.AvailableAt > time.Now().Unix() {
		t.Fatalf("discounted payment email has wrong documents: %v", err)
	}
	missing, err := ListProjectTopUpsMissingDocuments(50, time.Now().Add(-time.Hour).Unix())
	if err != nil || len(missing) != 0 {
		t.Fatalf("discounted order needs nonexistent receipt: %d %v", len(missing), err)
	}
}

func TestProjectPaymentMethodFormatsCard(t *testing.T) {
	if got := ProjectPaymentMethod("card", "visa", "4242"); got != "Visa ···· 4242" {
		t.Fatalf("card method = %q", got)
	}
	if got := paymentEmailMethod(ProjectPaymentMethod("card", "visa", "4242")); got != "Visa ···· 4242" {
		t.Fatalf("card email method = %q", got)
	}
	for input, want := range map[string]string{"paypal": "PayPal", "cashapp": "Cash App", "us_bank_account": "US bank account", "revolut_pay": "Revolut Pay", "stripe": ""} {
		if got := paymentEmailMethod(input); got != want {
			t.Errorf("payment method %q = %q, want %q", input, got, want)
		}
	}
}

func TestMissingPaymentDocumentsPrioritizeOldestOrders(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "document-queue-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour).Unix()
	for i := range 52 {
		invoiceStatus := "pending"
		if i < 50 {
			invoiceStatus = "failed"
		}
		order := TopUp{ProjectID: project.ID, UserId: owner.Id, PaidByUserID: owner.Id, TradeNo: fmt.Sprintf("ref_queue_%02d", i),
			StripeCheckoutSessionID: fmt.Sprintf("cs_queue_%02d", i), Amount: 5, PaidAmount: 5, Status: common.TopUpStatusSuccess,
			InvoiceStatus: invoiceStatus, CompleteTime: base + int64(i)}
		if err := db.Create(&order).Error; err != nil {
			t.Fatal(err)
		}
	}
	orders, err := ListProjectTopUpsMissingDocuments(50, base-1)
	if err != nil || len(orders) != 50 {
		t.Fatalf("first reconciliation batch: %d %v", len(orders), err)
	}
	if orders[0].TradeNo != "ref_queue_00" || orders[49].TradeNo != "ref_queue_49" {
		t.Fatalf("old orders displaced by newer payments: first=%s last=%s", orders[0].TradeNo, orders[49].TradeNo)
	}
	for _, order := range orders {
		if err := MarkProjectTopUpDocumentChecked(order.Id, base+100); err != nil {
			t.Fatal(err)
		}
	}
	orders, err = ListProjectTopUpsMissingDocuments(50, base-1)
	if err != nil || len(orders) != 50 {
		t.Fatalf("next reconciliation batch: %d %v", len(orders), err)
	}
	if orders[0].TradeNo != "ref_queue_50" || orders[1].TradeNo != "ref_queue_51" {
		t.Fatalf("failed old invoices starved newer payments: first=%s second=%s", orders[0].TradeNo, orders[1].TradeNo)
	}
}

func TestProjectLogAccessSeparatesProjectsAndMembers(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	oldLog := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = oldLog })
	users := []User{{Username: "log-owner"}, {Username: "log-member"}, {Username: "log-other"}}
	for i := range users {
		if err := db.Create(&users[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	var shared, personal Project
	db.Where("owner_user_id = ?", users[0].Id).First(&shared)
	db.Where("owner_user_id = ?", users[1].Id).First(&personal)
	at := time.Now().Unix()
	db.Model(&shared).Update("upgraded_at", at)
	for _, u := range users[1:] {
		if err := db.Create(&ProjectMember{ProjectID: shared.ID, UserID: u.Id, Role: ProjectRoleMember, Status: ProjectMemberActive}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []Log{
		{ProjectID: shared.ID, UserId: users[1].Id, RequestID: "shared-personal", Type: LogTypeConsume},
		{ProjectID: personal.ID, UserId: users[1].Id, RequestID: "other-project", Type: LogTypeConsume},
		{ProjectID: shared.ID, UserId: users[2].Id, RequestID: "other-member", Type: LogTypeConsume},
		// The owner's project-less row from before schema 37, as the backfill
		// left it.
		{ProjectID: shared.ID, UserId: users[0].Id, RequestID: "legacy-owner", Type: LogTypeConsume},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	count := func(userID int) int64 {
		t.Helper()
		var member ProjectMember
		db.Where("project_id = ? AND user_id = ?", shared.ID, userID).First(&member)
		access, err := NewProjectLogAccess(shared, member)
		if err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := ProjectLogsQuery(access, ProjectLogFilter{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		return count
	}
	// The owner reads the whole project, including the backfilled legacy row;
	// each member reads only their own attribution.
	if count(users[0].Id) != 3 || count(users[1].Id) != 1 || count(users[2].Id) != 1 {
		t.Fatal("cross-project or cross-member attribution leaked")
	}
	if err := db.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", shared.ID, users[1].Id).Update("status", ProjectMemberSuspended).Error; err != nil {
		t.Fatal(err)
	}
	if count(users[1].Id) != 1 {
		t.Fatal("suspension changed a member's own history")
	}
	if err := db.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", shared.ID, users[1].Id).Update("status", ProjectMemberRemoved).Error; err != nil {
		t.Fatal(err)
	}
	var removed ProjectMember
	db.Where("project_id = ? AND user_id = ?", shared.ID, users[1].Id).First(&removed)
	if _, err := NewProjectLogAccess(shared, removed); !errors.Is(err, ErrProjectForbidden) {
		t.Fatal("removed member can still read project history")
	}
}

func TestProjectAccountDeletionProtectsSuspendedMembersAndPendingSettlement(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&SettlementOutbox{}, &SettlementFailure{}); err != nil {
		t.Fatal(err)
	}
	owner, member := User{Username: "delete-project-owner"}, User{Username: "delete-project-member"}
	for _, u := range []*User{&owner, &member} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	membership := ProjectMember{ProjectID: project.ID, UserID: member.Id, Role: ProjectRoleMember, Status: ProjectMemberSuspended}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteSelfUser(owner.Id); !errors.Is(err, ErrProjectDeletionDependency) {
		t.Fatalf("owner stranded suspended member: %v", err)
	}
	if err := db.Model(&membership).Update("status", ProjectMemberRemoved).Error; err != nil {
		t.Fatal(err)
	}
	pending := SettlementFailure{UserID: owner.Id, ActorUserID: member.Id, ProjectID: project.ID, Status: SettlementFailureStatusOpen, RequestID: "pending-delete-refund"}
	if err := db.Create(&pending).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteSelfUser(owner.Id); !errors.Is(err, ErrProjectDeletionDependency) {
		t.Fatalf("owner lost pending refund: %v", err)
	}
	if _, err := DeleteSelfUser(member.Id); !errors.Is(err, ErrProjectDeletionDependency) {
		t.Fatalf("actor lost pending settlement: %v", err)
	}
	if err := db.Model(&pending).Update("status", SettlementFailureStatusResolved).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteSelfUser(member.Id); err != nil {
		t.Fatal(err)
	}
	var historic User
	if err := db.Unscoped().First(&historic, member.Id).Error; err != nil || !historic.DeletedAt.Valid {
		t.Fatal("soft deletion lost historical actor")
	}
}

func TestProjectSpendMigrationRejectsAmbiguousChargesAndStopsAtInitialBoundary(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&ProjectDataMigration{}, &Log{}); err != nil {
		t.Fatal(err)
	}
	oldLog := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = oldLog })
	user := User{Username: "ambiguous-ledger"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", user.Id).First(&project)
	charge := SpendBudgetCharge{ProjectID: project.ID, ActorUserID: user.Id, PayerUserID: user.Id, AmountMicrousd: 10, CreatedAt: time.Now().UTC()}
	if err := db.Create(&charge).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateProjectSpendHistory(); err == nil {
		t.Fatal("ambiguous history silently migrated")
	}
	if err := db.Model(&charge).Update("request_id", "reconciled-request").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateProjectSpendHistory(); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Log{ProjectID: project.ID, PayerUserID: user.Id, UserId: user.Id, Type: LogTypeConsume, RequestID: "new-realtime-summary", AmountMicrousd: 10, CreatedAt: time.Now().Unix()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateProjectSpendHistory(); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&ProjectSpend{}).Count(&count)
	if count != 1 {
		t.Fatalf("migration imported post-migration logs: %d", count)
	}
	// An old instance may still write a legacy-format log during a rolling
	// upgrade. A later bounded pass must import it without replaying new logs.
	if err := db.Create(&Log{UserId: user.Id, Type: LogTypeConsume, RequestID: "late-old-instance", AmountMicrousd: 5, CreatedAt: time.Now().Unix()}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := MigrateProjectSpendHistory(); err != nil {
			t.Fatal(err)
		}
	}
	var amount billing.MicroUSD
	db.Model(&ProjectSpend{}).Select("COALESCE(SUM(amount_microusd),0)").Scan(&amount)
	if amount != 15 {
		t.Fatalf("rolling upgrade lost or duplicated spend: %d", amount)
	}
}

func TestProjectBudgetDecisionRaceHasOneWinner(t *testing.T) {
	db := setupProjectTestDB(t, true)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	owner := User{Username: "race-owner"}
	member := User{Username: "race-member", Email: "race@example.com"}
	for _, user := range []*User{&owner, &member} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	if err := db.Create(&ProjectMember{ProjectID: project.ID, UserID: member.Id, Role: ProjectRoleMember, Status: ProjectMemberActive}).Error; err != nil {
		t.Fatal(err)
	}
	request, err := CreateProjectBudgetRequest(project.ID, member.Id, 200, "raise budget")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 3)
	amount := billing.MicroUSD(250)
	for _, action := range []string{"approved", "rejected", "withdrawn"} {
		go func(action string) {
			<-start
			actor := owner.Id
			if action == "withdrawn" {
				actor = member.Id
			}
			_, err := HandleProjectBudgetRequest(project.ID, actor, request.PublicID, action, &amount, "declined request", time.Now().UTC())
			results <- err
		}(action)
	}
	close(start)
	wins := 0
	for i := 0; i < 3; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrProjectStateConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("decision winners %d", wins)
	}
	if err := db.First(&request, request.ID).Error; err != nil {
		t.Fatal(err)
	}
	var histories, notices int64
	db.Model(&ProjectBudgetHistory{}).Count(&histories)
	db.Model(&ProjectNotification{}).Count(&notices)
	if request.Status == "approved" && histories != 1 || request.Status != "approved" && histories != 0 {
		t.Fatalf("unexpected budget writes: %s %d", request.Status, histories)
	}
	if request.Status == "withdrawn" && notices != 0 || request.Status != "withdrawn" && notices != 1 {
		t.Fatalf("unexpected notices: %s %d", request.Status, notices)
	}
}

func TestProjectInviteAcceptanceAndRevocationRace(t *testing.T) {
	db := setupProjectTestDB(t, true)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Setenv("PROJECTS_ENABLED", "true")
	owner := User{Username: "invite-race-owner"}
	member := User{Username: "invite-race-member", Email: "race@example.com", VerifiedEmail: "race@example.com"}
	for _, user := range []*User{&owner, &member} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	invite, err := CreateProjectInvite(project.ID, owner.Id, CreateProjectInviteInput{Email: member.Email, Role: ProjectRoleMember, BaseURL: "https://example.com"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	raw := invitationRawFromQueue(t, db, invite.TokenDigest)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := AcceptProjectInvite(raw, member.Id, time.Now().UTC(), "https://console.example")
		results <- err
	}()
	go func() {
		<-start
		_, err := ChangeProjectInvite(project.ID, owner.Id, invite.PublicID, false, "", time.Now().UTC())
		results <- err
	}()
	close(start)
	wins := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrProjectInviteRevoked) && !errors.Is(err, ErrProjectStateConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("race winners %d", wins)
	}
	db.First(&invite, invite.ID)
	var members int64
	db.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", project.ID, member.Id).Count(&members)
	if invite.Status == "accepted" && members != 1 || invite.Status == "revoked" && members != 0 {
		t.Fatalf("inconsistent invite/member: %s %d", invite.Status, members)
	}
}

func TestProjectMigrationPreservesDeletedOwnerAndDetectsOrphans(t *testing.T) {
	db := setupProjectTestDB(t, true)
	user := User{Username: "deleted-owner"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteSelfUser(user.Id); err != nil {
		t.Fatal(err)
	}
	if err := migrateTeamProjects(db); err != nil {
		t.Fatalf("deleted account blocked restart: %v", err)
	}
	var count int64
	db.Model(&ProjectMember{}).Where("user_id = ? AND status = ?", user.Id, ProjectMemberRemoved).Count(&count)
	if count != 1 {
		t.Fatal("migration revived deleted owner")
	}
	token := Token{UserId: 9999, Key: "orphan-credential", ProjectID: 9999}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateTeamProjects(db); err == nil {
		t.Fatal("orphan passed migration validation")
	}
}

func TestProjectReceiptRequiresMatchingLogicalKeyAndProject(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	oldLog := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = oldLog })
	holder := User{Username: "receipt-holder"}
	if err := db.Create(&holder).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", holder.Id).First(&project)
	row := Log{Type: LogTypeConsume, ProjectID: project.ID, PayerUserID: holder.Id, UserId: holder.Id, TokenId: 42, RequestID: "key-receipt", AmountMicrousd: 7}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	principal := ProjectPrincipal{ProjectID: project.ID, PayerUserID: holder.Id, ActorUserID: holder.Id, TokenID: 42}
	if _, err := GetConsumeLogByRequestIDForTokenContext(context.Background(), holder.Id, 42, row.RequestID, principal); err != nil {
		t.Fatal(err)
	}
	other := principal
	other.ActorUserID = holder.Id + 1000
	if _, err := GetConsumeLogByRequestIDForTokenContext(context.Background(), other.ActorUserID, 42, row.RequestID, other); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-actor receipt: %v", err)
	}
	actorless := principal
	actorless.ActorUserID = 0
	if _, err := GetConsumeLogByRequestIDForTokenContext(context.Background(), 0, 42, row.RequestID, actorless); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("actor zero got a receipt")
	}
}

func TestProjectLegacyDefaultKeyLimitDoesNotLeakIntoSharedProject(t *testing.T) {
	db := setupProjectTestDB(t, true)
	user := User{Username: "legacy-key-count"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", user.Id).First(&project)
	for i := 0; i < ProjectKeyLimit+1; i++ {
		token := Token{Name: fmt.Sprintf("old-%d", i), Key: fmt.Sprintf("legacy-limit-%d", i)}
		if err := CreateLegacyDefaultProjectPersonalToken(project.ID, user.Id, &token); err != nil {
			t.Fatal(err)
		}
	}
	token := Token{Name: "project-limit", Key: "project-limit"}
	if err := CreateProjectPersonalToken(project.ID, user.Id, &token); !errors.Is(err, ErrProjectKeyLimit) {
		t.Fatalf("enabled project limit missing: %v", err)
	}
	if err := db.Model(&project).Update("upgraded_at", time.Now().Unix()).Error; err != nil {
		t.Fatal(err)
	}
	if err := CreateLegacyDefaultProjectPersonalToken(project.ID, user.Id, &token); !errors.Is(err, ErrProjectForbidden) {
		t.Fatalf("legacy route bypassed shared project limit: %v", err)
	}
}

func TestProjectRootRestoreOnlyReactivatesOwnedMembership(t *testing.T) {
	db := setupProjectTestDB(t, true)
	resetRootConfig(t)
	user := User{Username: "restore-configured-root", Status: common.UserStatusEnabled}
	other := User{Username: "unrelated-project"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	var owned, joined Project
	db.Where("owner_user_id = ?", user.Id).First(&owned)
	db.Where("owner_user_id = ?", other.Id).First(&joined)
	member := ProjectMember{ProjectID: joined.ID, UserID: user.Id, Role: ProjectRoleMember, Status: ProjectMemberRemoved}
	if err := db.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", owned.ID, user.Id).Updates(map[string]interface{}{"status": ProjectMemberRemoved, "removal_operation_id": "original-removal", "removed_at": int64(1)}).Error; err != nil {
		t.Fatal(err)
	}
	token := Token{UserId: user.Id, ProjectID: owned.ID, Key: "revoked-root-key"}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&token).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROOT_USERNAME", user.Username)
	t.Setenv("ROOT_PASSWORD", "root-test-password")
	if err := SyncRootUserFromConfig(); err != nil {
		t.Fatal(err)
	}
	var restored User
	if err := db.First(&restored, user.Id).Error; err != nil {
		t.Fatal(err)
	}
	if restored.Role != common.RoleRootUser || restored.Status != common.UserStatusEnabled {
		t.Fatalf("root not restored: %+v", restored)
	}
	var ownerMember ProjectMember
	db.Where("project_id = ? AND user_id = ?", owned.ID, user.Id).First(&ownerMember)
	if ownerMember.Status != ProjectMemberActive || ownerMember.RemovedAt != nil || ownerMember.RemovalOperationID != "" {
		t.Fatalf("owner not restored: %+v", ownerMember)
	}
	db.First(&member, member.ID)
	if member.Status != ProjectMemberRemoved {
		t.Fatal("joined membership revived")
	}
	var count int64
	db.Model(&Token{}).Where("id = ?", token.Id).Count(&count)
	if count != 0 {
		t.Fatal("revoked key revived")
	}
	version := ownerMember.Version
	if err := SyncRootUserFromConfig(); err != nil {
		t.Fatal(err)
	}
	db.First(&ownerMember, ownerMember.ID)
	if ownerMember.Version != version {
		t.Fatal("bootstrap replay changed membership")
	}
}

func TestProjectTokenKeywordSearchKeepsOwnershipScope(t *testing.T) {
	EnsureColumnNamesInitialized()
	db := setupProjectTestDB(t, true)
	user := User{Username: "search-owner"}
	other := User{Username: "search-other"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	var project, second Project
	db.Where("owner_user_id = ?", user.Id).First(&project)
	db.Where("owner_user_id = ?", other.Id).First(&second)
	own := Token{UserId: user.Id, ProjectID: project.ID, Name: "own", Key: "search-own-abc123"}
	for _, token := range []*Token{&own, {UserId: other.Id, ProjectID: project.ID, Name: "private", Key: "search-private-abc123"}, {UserId: user.Id, ProjectID: second.ID, Name: "other project", Key: "search-other-abc123"}} {
		if err := db.Create(token).Error; err != nil {
			t.Fatal(err)
		}
	}
	var found []Token
	query := ProjectVisibleTokensQuery(db, project, ProjectMember{UserID: user.Id, Role: ProjectRoleOwner, Status: ProjectMemberActive}, false)
	if err := FilterTokenSearch(query, "abc123", "").Find(&found).Error; err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Id != own.Id {
		t.Fatalf("keyword leaked or missed keys: %+v", found)
	}
}

// A retired service row carries user_id 0 and belongs to the project owner. The
// member-detail list and the Key limit already count it as the owner's, so the
// Key list, search and options projections have to agree or the console shows
// fewer Keys than the limit charges for.
func TestProjectVisibleTokensQueryMatchesTheKeyLimitScope(t *testing.T) {
	EnsureColumnNamesInitialized()
	db := setupProjectTestDB(t, true)
	owner := User{Username: "legacy-key-owner"}
	member := User{Username: "legacy-key-member"}
	for _, user := range []*User{&owner, &member} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ProjectMember{ProjectID: project.ID, UserID: member.Id, Role: ProjectRoleMember, Status: ProjectMemberActive}).Error; err != nil {
		t.Fatal(err)
	}
	first := Token{UserId: owner.Id, ProjectID: project.ID, Name: "owner-a", Key: "project-owner-key-0"}
	second := Token{UserId: owner.Id, ProjectID: project.ID, Name: "owner-b", Key: "project-owner-key-1"}
	theirs := Token{UserId: member.Id, ProjectID: project.ID, Name: "member", Key: "project-member-key-2"}
	// An ownerless leftover belongs to nobody: the migration attributes every
	// real row, so one that still carries user_id 0 must not surface anywhere.
	orphan := Token{UserId: owner.Id, ProjectID: project.ID, Name: "orphan", Key: "project-orphan-key-3"}
	for _, token := range []*Token{&first, &second, &theirs, &orphan} {
		if err := db.Create(token).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&Token{}).Where("id = ?", orphan.Id).Update("user_id", 0).Error; err != nil {
		t.Fatal(err)
	}
	ownerMember := ProjectMember{ProjectID: project.ID, UserID: owner.Id, Role: ProjectRoleOwner, Status: ProjectMemberActive}
	var visible []Token
	if err := ProjectVisibleTokensQuery(db, project, ownerMember, false).Order("id ASC").Find(&visible).Error; err != nil {
		t.Fatal(err)
	}
	if len(visible) != 2 || visible[0].Id != first.Id || visible[1].Id != second.Id {
		t.Fatalf("owner list is wrong: %+v", visible)
	}
	// The projection the limit and the member detail already use.
	var counted int64
	if err := ProjectMemberTokenScope(excludeInternalTokens(db.Model(&Token{})).Where("project_id = ?", project.ID), owner.Id).Count(&counted).Error; err != nil {
		t.Fatal(err)
	}
	if counted != int64(len(visible)) {
		t.Fatalf("list and limit disagree: list=%d limit=%d", len(visible), counted)
	}
	var theirView []Token
	if err := ProjectVisibleTokensQuery(db, project, ProjectMember{ProjectID: project.ID, UserID: member.Id, Role: ProjectRoleMember, Status: ProjectMemberActive}, false).Find(&theirView).Error; err != nil {
		t.Fatal(err)
	}
	if len(theirView) != 1 || theirView[0].Id != theirs.Id {
		t.Fatalf("member saw an unattributed Key: %+v", theirView)
	}
}

func TestProjectSuspendedSummaryAndCustomerLogRedaction(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "summary-owner", Quota: 99000000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	member := ProjectMember{ProjectID: project.ID, UserID: owner.Id, Role: ProjectRoleOwner, Status: ProjectMemberSuspended}
	summary, err := ReadProjectSummary(project, member)
	if err != nil {
		t.Fatal(err)
	}
	if summary.BalanceMicrousd != nil {
		t.Fatal("suspended membership can read payer balance")
	}
	member.Status = ProjectMemberActive
	summary, err = ReadProjectSummary(project, member)
	if err != nil || summary.BalanceMicrousd == nil || *summary.BalanceMicrousd != owner.Quota {
		t.Fatalf("active summary: %+v %v", summary, err)
	}
	source := &Log{ProjectID: project.ID, UserId: owner.Id, ChannelId: 42, ChannelName: "private supplier", UsageFact: `{"provider_family":"private-provider","provider_cost_usd":0.4,"gross_margin_usd":0.2,"channel_id":42,"raw_usage":{"secret":"supplier-data"}}`, Other: `{"provider_cost_usd":0.4,"gross_margin_usd":0.2,"channel_id":42,"prompt_tokens":12,"usage_fact":{"provider_cost_usd":0.4,"customer_cost_usd":0.6},"attempts":[{"upstream_model_name":"private-route","duration":2}]}`}
	views, err := ProjectLogViews(project, []*Log{source})
	if err != nil {
		t.Fatal(err)
	}
	if source.UsageFact == "" || source.ChannelId != 42 {
		t.Fatal("customer projection mutated the platform accounting record")
	}
	if len(views) != 1 || views[0].ChannelId != 0 || views[0].UsageFact != "" || strings.Contains(views[0].Other, "provider_cost") || strings.Contains(views[0].Other, "gross_margin") || strings.Contains(views[0].Other, "channel_id") || strings.Contains(views[0].Other, "private-route") || !strings.Contains(views[0].Other, "customer_cost_usd") {
		t.Fatalf("customer log: %+v", views)
	}
}

func TestProjectHardDeleteRetainsBudgetDependents(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "budget-delete-owner", Quota: 0}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	budget := SpendBudget{ProjectID: project.ID, Scope: SpendBudgetScopeToken, TokenID: 9, Enabled: false, LimitMicrousd: 100}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	if err := HardDeleteUserById(owner.Id); !errors.Is(err, ErrProjectDeletionDependency) {
		t.Fatalf("hard deletion ignored budget dependency: %v", err)
	}
	if err := db.First(&project, project.ID).Error; err != nil {
		t.Fatal("project deleted while budget remains")
	}
}

func TestProjectReviewDissolutionRetiresRequestsAndMemberKeys(t *testing.T) {
	db := setupProjectTestDB(t, true)
	oldSecret := common.CryptoSecret
	common.CryptoSecret = "test"
	t.Cleanup(func() { common.CryptoSecret = oldSecret })
	owner, member := User{Username: "dissolve-owner"}, User{Username: "dissolve-member", Email: "member@example.test"}
	db.Create(&owner)
	db.Create(&member)
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	at := time.Now().Unix()
	db.Model(&project).Update("upgraded_at", at)
	db.Create(&ProjectMember{ProjectID: project.ID, UserID: member.Id, Role: ProjectRoleMember, Status: ProjectMemberActive})
	key := Token{ProjectID: project.ID, UserId: member.Id, Key: "dissolve-member-key", Status: common.TokenStatusEnabled, ExpiredTime: -1}
	db.Create(&key)
	request := BudgetRequest{ProjectID: project.ID, UserID: member.Id, Status: "pending"}
	db.Create(&request)
	notice := ProjectNotification{ProjectID: project.ID, EventKey: "invite:dissolve", Status: "pending", Payload: "encrypted"}
	db.Create(&notice)
	if err := DissolveProject(project.ID, owner.Id, project.Name); err != nil {
		t.Fatal(err)
	}
	db.First(&request, request.ID)
	db.First(&key, key.Id)
	db.First(&notice, notice.ID)
	if request.Status != "rejected" || request.HandledAt == nil {
		t.Fatalf("obsolete request: %+v", request)
	}
	if key.Status != common.TokenStatusDisabled || key.RemovalOperationID != "dissolved" {
		t.Fatalf("member key survived dissolution: %+v", key)
	}
	if notice.Status != "cancelled" || notice.Payload != "" {
		t.Fatal("dissolved invitation is deliverable")
	}
	if err := db.Delete(&key).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteSelfUser(member.Id); err != nil {
		t.Fatalf("former member deletion: %v", err)
	}
}

func TestProjectReviewHardDeletePristineOwnerWithRotationTable(t *testing.T) {
	initCol()
	db := setupProjectTestDB(t, true)
	user := User{Username: "pristine-delete"}
	db.Create(&user)
	if err := HardDeleteUserById(user.Id); err != nil {
		t.Fatalf("pristine owner deletion: %v", err)
	}
}

func TestProjectReviewInviteOutboxCancellation(t *testing.T) {
	db := setupProjectTestDB(t, true)
	t.Setenv("PROJECTS_ENABLED", "true")
	old := common.CryptoSecret
	common.CryptoSecret = "review-invite-test"
	t.Cleanup(func() { common.CryptoSecret = old })
	owner := User{Username: "outbox-owner"}
	db.Create(&owner)
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	for _, resend := range []bool{false, true} {
		digest := fmt.Sprintf("old-%v", resend)
		invite := ProjectInvite{ProjectID: project.ID, Email: "invite@example.test", Role: ProjectRoleMember, Status: "pending", TokenDigest: digest, CreatedAt: time.Now().Add(-2 * time.Minute).Unix()}
		db.Create(&invite)
		mail := ProjectNotification{ProjectID: project.ID, EventKey: "invite:" + digest, Payload: "secret", Status: "pending"}
		db.Create(&mail)
		if _, err := ChangeProjectInvite(project.ID, owner.Id, invite.PublicID, resend, "https://example.test", time.Now()); err != nil {
			t.Fatal(err)
		}
		db.First(&mail, mail.ID)
		if mail.Status != "cancelled" || mail.Payload != "" {
			t.Fatalf("old mail retained: %+v", mail)
		}
		if resend {
			var count int64
			db.Model(&ProjectNotification{}).Where("project_id = ? AND status = ? AND event_key NOT LIKE ?", project.ID, "pending", "invite:reminder:%").Count(&count)
			if count != 1 {
				t.Fatalf("replacement count %d", count)
			}
		}
	}
}

func TestProjectReviewSchemaRejectsMissingBudgetAttribution(t *testing.T) {
	for _, table := range []string{"spend_budget_alerts", "spend_budget_reservations", "spend_budget_charges"} {
		for _, column := range []string{"project_id", "payer_user_id", "actor_user_id"} {
			t.Run(table+"/"+column, func(t *testing.T) {
				db := setupProjectTestDB(t, true)
				if err := db.AutoMigrate(&ProjectDataMigration{}, &SettlementOutbox{}, &SettlementFailure{}); err != nil {
					t.Fatal(err)
				}
				if err := validateTeamProjectSchema(db, schemaVersionPrimary); err != nil {
					t.Fatalf("complete schema: %v", err)
				}
				if column == "project_id" {
					if err := db.Exec("DROP INDEX idx_" + table + "_project_id").Error; err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Exec("ALTER TABLE " + table + " DROP COLUMN " + column).Error; err != nil {
					t.Fatal(err)
				}
				if err := validateTeamProjectSchema(db, schemaVersionPrimary); err == nil || !strings.Contains(err.Error(), column) {
					t.Fatalf("missing attribution accepted: %v", err)
				}
			})
		}
	}
}

func TestProjectMemberActivityUsesSuccessfulLogs(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	old := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = old })
	owner := User{Username: "activity-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	member := ProjectMember{ProjectID: project.ID, UserID: owner.Id, Role: ProjectRoleOwner, Status: ProjectMemberActive}
	key := Token{ProjectID: project.ID, UserId: owner.Id, Key: "activity-key", LastUsedAt: 999999}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	view, err := ReadProjectMemberView(project, member, now)
	if err != nil || view.LastActiveAt != nil {
		t.Fatalf("unused member active: %+v %v", view, err)
	}
	for _, row := range []Log{{ProjectID: project.ID, UserId: owner.Id, TokenId: key.Id, Type: LogTypeConsume, CreatedAt: 100}, {ProjectID: project.ID, UserId: owner.Id, TokenId: key.Id, Type: LogTypeError, CreatedAt: 200}, {ProjectID: project.ID + 10, UserId: owner.Id, TokenId: key.Id, Type: LogTypeConsume, CreatedAt: 300}, {ProjectID: project.ID, UserId: 0, TokenId: key.Id + 1, Type: LogTypeConsume, CreatedAt: 400}} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	view, err = ReadProjectMemberView(project, member, now)
	if err != nil || view.LastActiveAt == nil || *view.LastActiveAt != 100 {
		t.Fatalf("wrong activity: %+v %v", view, err)
	}
}

// shareProjectWithMember is the fixture behind the removal/undo regressions: a
// shared project, one active member and one enabled Key belonging to them.
func shareProjectWithMember(t *testing.T, db *gorm.DB, prefix string, now time.Time) (User, User, Project, Token) {
	t.Helper()
	owner := User{Username: prefix + "-owner", Email: prefix + "-owner@example.test"}
	member := User{Username: prefix + "-member", Email: prefix + "-member@example.test"}
	for _, user := range []*User{&owner, &member} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	at := now.Unix()
	if err := db.Model(&project).Update("upgraded_at", at).Error; err != nil {
		t.Fatal(err)
	}
	project.UpgradedAt = &at
	if err := db.Create(&ProjectMember{ProjectID: project.ID, UserID: member.Id, Role: ProjectRoleMember, Status: ProjectMemberActive}).Error; err != nil {
		t.Fatal(err)
	}
	key, err := common.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	token := Token{ProjectID: project.ID, UserId: member.Id, Name: prefix + "-key", Key: key, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	return owner, member, project, token
}

// Dissolving a project ends every outstanding undo window. Without clearing the
// members' handles an old removal receipt could be replayed once the project is
// shared again and put that member back in.
func TestProjectDissolveClearsRemovalUndoHandles(t *testing.T) {
	db := setupProjectTestDB(t, true)
	oldSecret := common.CryptoSecret
	common.CryptoSecret = "test"
	t.Cleanup(func() { common.CryptoSecret = oldSecret })
	t.Setenv("PROJECTS_ENABLED", "true")
	now := time.Now().UTC().Truncate(time.Second)
	owner, member, project, token := shareProjectWithMember(t, db, "dissolve-undo", now)
	receipt, err := RemoveProjectMember(project.ID, owner.Id, member.Id, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := DissolveProject(project.ID, owner.Id, project.Name); err != nil {
		t.Fatal(err)
	}
	var removed ProjectMember
	if err := db.Where("project_id = ? AND user_id = ?", project.ID, member.Id).First(&removed).Error; err != nil {
		t.Fatal(err)
	}
	if removed.RemovalOperationID != "" {
		t.Fatalf("dissolution left an undo handle: %+v", removed)
	}
	// Sharing again is exactly what an invitation does; the old receipt must not
	// come back to life with it.
	if _, err := CreateProjectInvite(project.ID, owner.Id, CreateProjectInviteInput{Email: "rejoiner@example.test", Role: ProjectRoleMember, BaseURL: "https://console.example"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&project, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if project.UpgradedAt == nil {
		t.Fatal("invitation did not re-share the project")
	}
	if _, err := UndoProjectRemoval(project.ID, owner.Id, member.Id, receipt.PublicID, now.Add(2*time.Minute)); !errors.Is(err, ErrProjectStateConflict) {
		t.Fatalf("stale receipt undone into a re-shared project: %v", err)
	}
	if err := db.Where("project_id = ? AND user_id = ?", project.ID, member.Id).First(&removed).Error; err != nil {
		t.Fatal(err)
	}
	if removed.Status != ProjectMemberRemoved {
		t.Fatalf("membership resurrected: %+v", removed)
	}
	if _, err := ResolveProjectPrincipal(db, token.Id, now.Add(2*time.Minute)); err == nil {
		t.Fatal("dissolved member's key came back")
	}
}

// A re-invited member keeps their old Keys stopped for good. Those Keys must not
// eat into the ten they are allowed, and their state has to be legible.
func TestProjectRemovalStoppedKeysStillConsumeTheKeyLimit(t *testing.T) {
	db := setupProjectTestDB(t, true)
	oldSecret := common.CryptoSecret
	common.CryptoSecret = "test"
	t.Cleanup(func() { common.CryptoSecret = oldSecret })
	now := time.Now().UTC().Truncate(time.Second)
	owner, member, project, _ := shareProjectWithMember(t, db, "fenced-limit", now)
	for i := 1; i < ProjectKeyLimit; i++ {
		key, err := common.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		token := Token{Name: fmt.Sprintf("fenced-%d", i), Key: key, UnlimitedQuota: true}
		if err := CreateProjectPersonalToken(project.ID, member.Id, &token); err != nil {
			t.Fatal(err)
		}
	}
	key, err := common.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateProjectPersonalToken(project.ID, member.Id, &Token{Name: "eleventh", Key: key, UnlimitedQuota: true}); !errors.Is(err, ErrProjectKeyLimit) {
		t.Fatalf("key limit bypassed: %v", err)
	}
	if _, err := RemoveProjectMember(project.ID, owner.Id, member.Id, now); err != nil {
		t.Fatal(err)
	}
	// Re-inviting clears the member's fence but never their Keys': those stay
	// stopped, and they keep consuming the allowance until they are deleted.
	if err := db.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", project.ID, member.Id).
		Updates(map[string]interface{}{"status": ProjectMemberActive, "removal_operation_id": "", "removed_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	var stopped []Token
	if err := db.Where("project_id = ? AND user_id = ?", project.ID, member.Id).Find(&stopped).Error; err != nil {
		t.Fatal(err)
	}
	if len(stopped) != ProjectKeyLimit {
		t.Fatalf("fixture lost keys: %d", len(stopped))
	}
	for _, token := range stopped {
		if !token.StoppedByRemoval() {
			t.Fatalf("key escaped the removal fence: %+v", token)
		}
		if _, err := ResolveProjectPrincipal(db, token.Id, now.Add(time.Minute)); !errors.Is(err, ErrProjectAccessSuspended) {
			t.Fatalf("stopped key revived on re-join: %v", err)
		}
	}
	fresh, err := common.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	// A remove/re-invite cycle must not hand out a fresh allowance.
	if err := CreateProjectPersonalToken(project.ID, member.Id, &Token{Name: "after-rejoin", Key: fresh, UnlimitedQuota: true}); !errors.Is(err, ErrProjectKeyLimit) {
		t.Fatalf("re-invite reset the key limit: %v", err)
	}
	// A re-invite restores the membership but never the Key, so a stopped Key
	// stays read-and-delete only. Rotating one would mint a secret that could
	// never authenticate.
	for _, action := range []ProjectAction{ProjectManagePersonalKey} {
		if _, err := CheckProjectTokenAction(coreDB, project.ID, owner.Id, stopped[0].Id, action); !errors.Is(err, ErrProjectStateConflict) {
			t.Fatalf("stopped key still mutable after re-invite (%v): %v", action, err)
		}
	}
	if _, err := CheckProjectTokenAction(coreDB, project.ID, owner.Id, stopped[0].Id, ProjectDeletePersonalKey); err != nil {
		t.Fatalf("stopped key is not deletable after re-invite: %v", err)
	}
	// Deleting a stopped Key is how the allowance is freed; nothing revives it.
	if _, err := DeleteProjectToken(project.ID, member.Id, stopped[0].Id); err != nil {
		t.Fatalf("owner cannot clear a stopped key: %v", err)
	}
	replacement := Token{Name: "after-delete", Key: fresh, UnlimitedQuota: true}
	if err := CreateProjectPersonalToken(project.ID, member.Id, &replacement); err != nil {
		t.Fatalf("deleting a stopped key did not free the allowance: %v", err)
	}
}

// A member who disabled their own Key, or a Key that simply ran out of calendar,
// gets the ordinary token-state answer. project_access_suspended means the
// membership or project is suspended and tells the holder to find an admin.
func TestProjectPrincipalSeparatesKeyStateFromProjectSuspension(t *testing.T) {
	db := setupProjectTestDB(t, true)
	now := time.Now().UTC().Truncate(time.Second)
	_, member, project, token := shareProjectWithMember(t, db, "key-state", now)
	if _, err := ResolveProjectPrincipal(db, token.Id, now); err != nil {
		t.Fatalf("healthy key rejected: %v", err)
	}
	for name, update := range map[string]map[string]interface{}{
		"disabled":   {"status": common.TokenStatusDisabled},
		"expired":    {"status": common.TokenStatusEnabled, "expired_time": now.Add(-time.Hour).Unix()},
		"no project": {"status": common.TokenStatusEnabled, "expired_time": -1, "project_id": 0},
	} {
		if err := db.Model(&Token{}).Where("id = ?", token.Id).Updates(update).Error; err != nil {
			t.Fatal(err)
		}
		_, err := ResolveProjectPrincipal(db, token.Id, now)
		if !errors.Is(err, ErrTokenAccessInactive) {
			t.Fatalf("%s key reported as a project suspension: %v", name, err)
		}
		if errors.Is(err, ErrProjectAccessSuspended) {
			t.Fatalf("%s key still carries the suspension reason", name)
		}
	}
	// The membership state keeps its own, distinguishable reason.
	if err := db.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{"status": common.TokenStatusEnabled, "expired_time": -1, "project_id": project.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", project.ID, member.Id).Update("status", ProjectMemberSuspended).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveProjectPrincipal(db, token.Id, now); !errors.Is(err, ErrProjectAccessSuspended) {
		t.Fatalf("suspended membership lost its reason: %v", err)
	}
}

func TestProjectPaymentHoldPreservesKeyStateErrors(t *testing.T) {
	db := setupProjectTestDB(t, true)
	now := time.Now().UTC().Truncate(time.Second)
	_, _, project, token := shareProjectWithMember(t, db, "held-key-state", now)
	if err := db.Model(&project).Update("payment_risk_hold", true).Error; err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		fields map[string]any
		want   error
	}{
		{"disabled", map[string]any{"status": common.TokenStatusDisabled, "expired_time": -1, "unlimited_quota": true}, ErrTokenAccessInactive},
		{"expired", map[string]any{"status": common.TokenStatusEnabled, "expired_time": now.Add(-time.Hour).Unix(), "unlimited_quota": true}, ErrTokenAccessInactive},
		{"exhausted", map[string]any{"status": common.TokenStatusExhausted, "expired_time": -1, "unlimited_quota": false, "remain_quota": 0}, ErrTokenBudgetExhausted},
		{"empty budget", map[string]any{"status": common.TokenStatusEnabled, "expired_time": -1, "unlimited_quota": false, "remain_quota": 0}, ErrTokenBudgetExhausted},
		{"active", map[string]any{"status": common.TokenStatusEnabled, "expired_time": -1, "unlimited_quota": true}, ErrProjectAccessSuspended},
	}
	for _, tc := range cases {
		if err := db.Model(&token).Updates(tc.fields).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveProjectPrincipal(db, token.Id, now); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

// A manager must not edit, rotate or reveal a Key whose owner is no longer an
// active member: rotation bumps the project version and silently breaks the undo.
func TestProjectKeyActionsRejectedWhileOwnerIsNotActive(t *testing.T) {
	db := setupProjectTestDB(t, true)
	oldSecret := common.CryptoSecret
	common.CryptoSecret = "test"
	t.Cleanup(func() { common.CryptoSecret = oldSecret })
	now := time.Now().UTC().Truncate(time.Second)
	owner, member, project, token := shareProjectWithMember(t, db, "inactive-owner", now)
	spare, err := common.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	disposable := Token{ProjectID: project.ID, UserId: member.Id, Name: "disposable", Key: spare, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	if err := db.Create(&disposable).Error; err != nil {
		t.Fatal(err)
	}
	receipt, err := RemoveProjectMember(project.ID, owner.Id, member.Id, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.First(&token, token.Id).Error; err != nil {
		t.Fatal(err)
	}
	before := token.ProjectVersion
	edit := token
	edit.Name = "renamed by a manager"
	if err := UpdateProjectToken(project.ID, owner.Id, &edit); !errors.Is(err, ErrProjectStateConflict) {
		t.Fatalf("edited a removed member's key: %v", err)
	}
	if _, err := RotateProjectToken(project.ID, owner.Id, token.Id, "undo-window-rotation", "24h", now.Add(time.Minute)); !errors.Is(err, ErrProjectStateConflict) {
		t.Fatalf("rotated a removed member's key: %v", err)
	}
	if _, _, err := RevealProjectToken(project.ID, owner.Id, token.Id); !errors.Is(err, ErrProjectStateConflict) {
		t.Fatalf("revealed a removed member's key: %v", err)
	}
	if err := db.First(&token, token.Id).Error; err != nil {
		t.Fatal(err)
	}
	if token.ProjectVersion != before || token.Name == "renamed by a manager" {
		t.Fatalf("refused action still changed the key: %+v", token)
	}
	// Deletion stays open: the contract says a deletion wins over a later undo.
	if _, err := DeleteProjectToken(project.ID, owner.Id, disposable.Id); err != nil {
		t.Fatalf("deletion refused inside the undo window: %v", err)
	}
	// The undo is still intact, which is the point of refusing those actions.
	result, err := UndoProjectRemoval(project.ID, owner.Id, member.Id, receipt.PublicID, now.Add(2*time.Minute))
	if err != nil || len(result.RestoredKeyIDs) != 1 || result.RestoredKeyIDs[0] != token.PublicID {
		t.Fatalf("undo broken by manager actions: %+v %v", result, err)
	}
	if len(result.SkippedKeyIDs) != 1 || result.SkippedKeyIDs[0] != disposable.PublicID {
		t.Fatalf("undo revived a deleted key: %+v", result)
	}
}

// Rotation with a 24-hour overlap must actually keep the old credential alive on
// a row written before Keys were stored as digests.
func TestProjectRotationOverlapCoversLegacyPlaintextRows(t *testing.T) {
	db := setupProjectTestDB(t, true)
	oldSecret := common.CryptoSecret
	common.CryptoSecret = "test"
	t.Cleanup(func() { common.CryptoSecret = oldSecret })
	owner := User{Username: "legacy-rotation-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	raw, err := common.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	token := Token{ProjectID: project.ID, UserId: owner.Id, Name: "legacy", Key: raw, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	// Pre-digest rows keep the plaintext in `key` and have no digest at all.
	if err := db.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{"key_digest": nil, "key_prefix": nil, "key_suffix": nil}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := RotateProjectToken(project.ID, owner.Id, token.Id, "legacy-24h-rotation", "24h", now)
	if err != nil {
		t.Fatal(err)
	}
	if result.OldExpiresAt != now.Add(24*time.Hour).Unix() {
		t.Fatalf("reported window is not the requested 24 hours: %+v", result)
	}
	if _, err := ResolveTokenCredential(token.Id, raw, now.Add(time.Hour)); err != nil {
		t.Fatalf("promised overlap did not survive the rotation: %v", err)
	}
	if _, err := ResolveTokenCredential(token.Id, raw, now.Add(25*time.Hour)); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("old credential outlived its window: %v", err)
	}
	if _, err := ResolveTokenCredential(token.Id, result.Key, now.Add(time.Hour)); err != nil {
		t.Fatalf("new credential rejected: %v", err)
	}
	// A digest-only row cannot keep its old secret alive, so it must say so
	// rather than promise a window it will not honour.
	if err := db.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{"key": "digest:unrecoverable", "key_digest": nil}).Error; err != nil {
		t.Fatal(err)
	}
	honest, err := RotateProjectToken(project.ID, owner.Id, token.Id, "digest-only-rotation", "24h", now)
	if err != nil {
		t.Fatal(err)
	}
	if honest.OldExpiresAt != now.Unix() {
		t.Fatalf("digest-only row promised an overlap it cannot keep: %+v", honest)
	}
}

// A hold has to outlive the request that took it, and a settlement arriving
// after the hold expired must still count exactly once.
func TestSpendBudgetReservationCoversLongRequestsAndFinalizesOnce(t *testing.T) {
	db := setupProjectTestDB(t, true)
	now := time.Now().UTC().Truncate(time.Second)
	owner, member, project, token := shareProjectWithMember(t, db, "long-request", now)
	limit := billing.MicroUSD(1000)
	if err := setProjectBudgetTx(db, project.ID, member.Id, 0, &limit, 80); err != nil {
		t.Fatal(err)
	}
	charge := SpendBudgetChargeInput{ProjectID: project.ID, PayerUserID: owner.Id, ActorUserID: member.Id, TokenID: token.Id, RequestID: "long-media-job", DeltaMicrousd: 800, OccurredAt: now, LongRunning: true}
	if err := CheckSpendBudgetBeforeCharge(charge); err != nil {
		t.Fatal(err)
	}
	var reservation SpendBudgetReservation
	if err := db.Where("request_id = ? AND budget_id > 0", charge.RequestID).First(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	// The longest admitted media job is hours long; the hold must still be there.
	if reservation.ExpiresAt <= now.Add(16*time.Hour).Unix() {
		t.Fatalf("hold expires before the longest allowed job: %+v", reservation)
	}
	// An ordinary relay request gets the short hold instead: if its process dies
	// between reserving and pre-consuming, expiry is the only thing that frees
	// the payer, so one interrupted chat must not block a Key for a day.
	relay := charge
	relay.RequestID, relay.DeltaMicrousd, relay.LongRunning = "ordinary-relay", 1, false
	if err := CheckSpendBudgetBeforeCharge(relay); err != nil {
		t.Fatal(err)
	}
	var relayHold SpendBudgetReservation
	if err := db.Where("request_id = ? AND budget_id > 0", relay.RequestID).First(&relayHold).Error; err != nil {
		t.Fatal(err)
	}
	if relayHold.ExpiresAt > now.Add(2*time.Hour).Unix() {
		t.Fatalf("a crashed relay request would hold the budget for hours: %+v", relayHold)
	}
	if err := db.Where("request_id = ?", relay.RequestID).Delete(&SpendBudgetReservation{}).Error; err != nil {
		t.Fatal(err)
	}
	concurrent := charge
	concurrent.RequestID, concurrent.DeltaMicrousd = "concurrent", 500
	if err := CheckSpendBudgetBeforeCharge(concurrent); !errors.Is(err, ErrSpendBudgetExceeded) {
		t.Fatalf("in-flight hold ignored by a concurrent request: %v", err)
	}
	// Even so, a settlement that arrives after the hold is gone counts once.
	if err := db.Where("request_id = ?", charge.RequestID).Delete(&SpendBudgetReservation{}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := FinalizeProjectSpendBudget(charge); err != nil {
			t.Fatalf("settlement %d: %v", i, err)
		}
	}
	var budget SpendBudget
	if err := db.Where("project_id = ? AND scope = ? AND user_id = ?", project.ID, SpendBudgetScopeUser, member.Id).First(&budget).Error; err != nil {
		t.Fatal(err)
	}
	start, end := spendBudgetPeriodWindow(budget.Period, now)
	spent, err := sumSpendMicrousdForBudgetWithDB(db, budget, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if spent != 800 {
		t.Fatalf("settlement without a reservation counted %d, want 800", spent)
	}
	var reservations int64
	if err := db.Model(&SpendBudgetReservation{}).Where("request_id = ?", charge.RequestID).Count(&reservations).Error; err != nil {
		t.Fatal(err)
	}
	if reservations != 0 {
		t.Fatalf("settlement left %d holds behind", reservations)
	}
}

// Invitations written under the old seven-day rule keep a seven-day expires_at.
// Trusting the stored value would leave those links usable on days four to
// seven after the three-day rule shipped, so the deadline is clamped wherever a
// pending invitation's validity is judged.
func TestProjectInviteClampsLegacySevenDayDeadline(t *testing.T) {
	db := setupProjectTestDB(t, true)
	t.Setenv("PROJECTS_ENABLED", "true")
	owner := User{Username: "clamp-owner"}
	member := User{Username: "clamp-member", Email: "clamp-member@example.test", VerifiedEmail: "clamp-member@example.test"}
	for _, user := range []*User{&owner, &member} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	invite, err := CreateProjectInvite(project.ID, owner.Id, CreateProjectInviteInput{Email: member.Email, Role: ProjectRoleMember, BaseURL: "https://console.example"}, now)
	if err != nil {
		t.Fatal(err)
	}
	raw := invitationRawFromQueue(t, db, invite.TokenDigest)
	// Rewrite the row the way the seven-day rule stored it.
	if err := db.Model(&ProjectInvite{}).Where("id = ?", invite.ID).Update("expires_at", now.Add(7*24*time.Hour).Unix()).Error; err != nil {
		t.Fatal(err)
	}
	stale, err := LookupProjectInvite(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := stale.EffectiveExpiresAt(); got != now.Add(ProjectInviteValidity).Unix() {
		t.Fatalf("stored seven-day deadline was not clamped: %d", got)
	}
	day4 := now.Add(4 * 24 * time.Hour)
	if got := stale.EffectiveStatus(day4); got != "expired" {
		t.Fatalf("day four still reports %q", got)
	}
	if _, err := AcceptProjectInvite(raw, member.Id, day4, "https://console.example"); !errors.Is(err, ErrProjectInviteExpired) {
		t.Fatalf("seven-day link accepted on day four: %v", err)
	}
	// The stale row must also stop blocking a fresh invitation to the same
	// mailbox once its real window has closed.
	replacement, err := CreateProjectInvite(project.ID, owner.Id, CreateProjectInviteInput{Email: member.Email, Role: ProjectRoleMember, BaseURL: "https://console.example"}, day4)
	if err != nil {
		t.Fatalf("stale invitation blocked a new one: %v", err)
	}
	// A resend restarts the window, so the clamp runs from the resend and not
	// from the original creation.
	resent, err := ChangeProjectInvite(project.ID, owner.Id, replacement.PublicID, true, "https://console.example", day4.Add(2*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&ProjectInvite{}).Where("id = ?", resent.ID).Update("expires_at", day4.Add(9*24*time.Hour).Unix()).Error; err != nil {
		t.Fatal(err)
	}
	var reread ProjectInvite
	if err := db.First(&reread, resent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got := reread.EffectiveStatus(day4.Add(3 * 24 * time.Hour)); got != "pending" {
		t.Fatalf("resent invitation expired against its original creation: %q", got)
	}
}

// A Key's usage view is bounded by the pair (token id, actor). Another
// person's rows on the same Key id stay out, and so does a row that carries no
// actor at all: the project migration attributes every Key, so an actorless
// log row belongs to nobody rather than to whoever holds the credential now.
func TestProjectKeyUsageIsBoundedByItsOwnActor(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	oldLog := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = oldLog })
	owner := User{Username: "actor-zero-owner"}
	stranger := User{Username: "actor-zero-stranger"}
	for _, user := range []*User{&owner, &stranger} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	const keyID = 4242
	rows := []Log{
		{Type: LogTypeConsume, ProjectID: project.ID, PayerUserID: owner.Id, UserId: 0, TokenId: keyID, RequestID: "actorless-receipt", AmountMicrousd: 5, CreatedAt: 1700000000},
		{Type: LogTypeConsume, ProjectID: project.ID, PayerUserID: owner.Id, UserId: owner.Id, TokenId: keyID, RequestID: "current-receipt", AmountMicrousd: 7, CreatedAt: 1700000100},
		// Another person's row on the same Key id stays out.
		{Type: LogTypeConsume, ProjectID: project.ID, PayerUserID: owner.Id, UserId: stranger.Id, TokenId: keyID, RequestID: "stranger-receipt", AmountMicrousd: 99, CreatedAt: 1700000200},
		// So does an actorless row from a different Key.
		{Type: LogTypeConsume, ProjectID: project.ID, PayerUserID: owner.Id, UserId: 0, TokenId: keyID + 1, RequestID: "other-key-receipt", AmountMicrousd: 11, CreatedAt: 1700000300},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	principal := ProjectPrincipal{ProjectID: project.ID, PayerUserID: owner.Id, ActorUserID: owner.Id, TokenID: keyID}
	summary, err := GetProjectKeyConsumeSummary(principal, 1600000000, 1800000000)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RequestCount != 1 || summary.AmountMicrousd != 7 {
		t.Fatalf("summary is not bounded by its own actor: %+v", summary)
	}
	series, err := GetProjectKeyUsageSeries(principal, 1600000000, 1800000000, "day")
	if err != nil {
		t.Fatal(err)
	}
	var seriesRequests int64
	var seriesAmount billing.MicroUSD
	for _, point := range series {
		seriesRequests += point.RequestCount
		seriesAmount += point.AmountMicrousd
	}
	if seriesRequests != 1 || seriesAmount != 7 {
		t.Fatalf("series is not bounded by its own actor: %d %d", seriesRequests, seriesAmount)
	}
	if _, err := GetConsumeLogByRequestIDForTokenContext(context.Background(), owner.Id, keyID, "current-receipt", principal); err != nil {
		t.Fatalf("own receipt is unreachable: %v", err)
	}
	if _, err := GetConsumeLogByRequestIDForTokenContext(context.Background(), owner.Id, keyID, "actorless-receipt", principal); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("an actorless receipt was handed to the credential holder: %v", err)
	}
	if _, err := GetConsumeLogByRequestIDForTokenContext(context.Background(), owner.Id, keyID, "stranger-receipt", principal); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("another actor's receipt leaked: %v", err)
	}
}

func TestProjectInviteRolloutChecksRecipientBeforeSending(t *testing.T) {
	db := setupProjectTestDB(t, true)
	t.Setenv("PROJECTS_ENABLED", "false")
	owner := User{Username: "rollout-owner"}
	member := User{Username: "rollout-member", Email: "rollout@example.test", VerifiedEmail: "rollout@example.test"}
	for _, user := range []*User{&owner, &member} {
		if err := db.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PROJECTS_ALLOWED_USERS", owner.PublicID)
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, email := range []string{member.Email, "not-registered@example.test"} {
		_, err := CreateProjectInvite(project.ID, owner.Id, CreateProjectInviteInput{Email: email, Role: ProjectRoleMember}, now)
		if !errors.Is(err, ErrProjectInviteeUnavailable) {
			t.Fatalf("recipient %s: %v", email, err)
		}
	}
	var count int64
	if err := db.Model(&ProjectInvite{}).Where("project_id = ?", project.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unexpected invitations: %d, %v", count, err)
	}
	if err := db.Model(&ProjectNotification{}).Where("project_id = ?", project.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unexpected notifications: %d, %v", count, err)
	}
	if err := db.First(&project, project.ID).Error; err != nil || project.UpgradedAt != nil {
		t.Fatalf("blocked invite upgraded project: %v", err)
	}
	t.Setenv("PROJECTS_ALLOWED_USERS", owner.PublicID+","+member.PublicID)
	invite, err := CreateProjectInvite(project.ID, owner.Id, CreateProjectInviteInput{Email: member.Email, Role: ProjectRoleMember, BaseURL: "https://console.example"}, now)
	if err != nil {
		t.Fatal(err)
	}
	raw := invitationRawFromQueue(t, db, invite.TokenDigest)
	t.Setenv("PROJECTS_ALLOWED_USERS", owner.PublicID)
	if _, err := ChangeProjectInvite(project.ID, owner.Id, invite.PublicID, true, "https://console.example", now.Add(time.Minute)); !errors.Is(err, ErrProjectInviteeUnavailable) {
		t.Fatalf("resend bypassed rollout: %v", err)
	}
	if _, err := AcceptProjectInvite(raw, member.Id, now.Add(time.Minute), "https://console.example"); !errors.Is(err, ErrProjectFeatureUnavailable) {
		t.Fatalf("accept bypassed rollout: %v", err)
	}
	t.Setenv("PROJECTS_ALLOWED_USERS", owner.PublicID+","+member.PublicID)
	if _, err := AcceptProjectInvite(raw, member.Id, now.Add(time.Minute), "https://console.example"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROJECTS_ENABLED", "true")
	if _, err := CreateProjectInvite(project.ID, owner.Id, CreateProjectInviteInput{Email: "new-signup@example.test", Role: ProjectRoleMember, BaseURL: "https://console.example"}, now); err != nil {
		t.Fatalf("global rollout must allow new users: %v", err)
	}
}

func TestProjectInviteRolloutRejectsAmbiguousEmail(t *testing.T) {
	for _, allowedFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("allowed_first_%t", allowedFirst), func(t *testing.T) {
			db := setupProjectTestDB(t, true)
			t.Setenv("PROJECTS_ENABLED", "false")
			owner := User{Username: "ambiguous-owner"}
			first := User{Username: "ambiguous-first", Email: "Duplicate@example.test"}
			second := User{Username: "ambiguous-second", Email: "duplicate@example.test"}
			for _, user := range []*User{&owner, &first, &second} {
				if err := db.Create(user).Error; err != nil {
					t.Fatal(err)
				}
			}
			allowed, duplicate := &second, &first
			if allowedFirst {
				allowed, duplicate = &first, &second
			}
			t.Setenv("PROJECTS_ALLOWED_USERS", owner.PublicID+","+allowed.PublicID)
			var project Project
			if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			input := CreateProjectInviteInput{Email: " DUPLICATE@example.test ", Role: ProjectRoleMember, BaseURL: "https://console.example"}
			if _, err := CreateProjectInvite(project.ID, owner.Id, input, now); !errors.Is(err, ErrProjectInviteeUnavailable) {
				t.Fatalf("ambiguous recipient was not rejected: %v", err)
			}
			for _, table := range []interface{}{&ProjectInvite{}, &ProjectNotification{}} {
				var count int64
				if err := db.Model(table).Where("project_id = ?", project.ID).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("blocked create produced records: %d, %v", count, err)
				}
			}
			if err := db.First(&project, project.ID).Error; err != nil || project.UpgradedAt != nil {
				t.Fatalf("blocked create upgraded project: %v", err)
			}
			// Soft-deleted rows do not make an otherwise unique address ambiguous.
			if err := db.Delete(duplicate).Error; err != nil {
				t.Fatal(err)
			}
			invite, err := CreateProjectInvite(project.ID, owner.Id, input, now)
			if err != nil {
				t.Fatalf("unique live recipient rejected: %v", err)
			}
			// Restoring the duplicate must block a resend before rotating its token or mail.
			if err := db.Unscoped().Model(duplicate).Update("deleted_at", nil).Error; err != nil {
				t.Fatal(err)
			}
			var before int64
			if err := db.Model(&ProjectNotification{}).Where("project_id = ?", project.ID).Count(&before).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := ChangeProjectInvite(project.ID, owner.Id, invite.PublicID, true, "https://console.example", now.Add(time.Minute)); !errors.Is(err, ErrProjectInviteeUnavailable) {
				t.Fatalf("ambiguous resend was not rejected: %v", err)
			}
			var after int64
			if err := db.Model(&ProjectNotification{}).Where("project_id = ?", project.ID).Count(&after).Error; err != nil || after != before {
				t.Fatalf("blocked resend changed notification count: %d -> %d, %v", before, after, err)
			}
			var stored ProjectInvite
			if err := db.First(&stored, invite.ID).Error; err != nil {
				t.Fatal(err)
			}
			if stored.TokenDigest != invite.TokenDigest || stored.ResentAt != nil {
				t.Fatal("blocked resend mutated invitation")
			}
			// Global rollout remains independent of recipient account lookup.
			t.Setenv("PROJECTS_ENABLED", "true")
			if _, err := ChangeProjectInvite(project.ID, owner.Id, invite.PublicID, true, "https://console.example", now.Add(time.Minute)); err != nil {
				t.Fatalf("global rollout resend rejected: %v", err)
			}
		})
	}
}

func TestPaymentBalanceSnapshotPreservesZeroWhenDocumentsArrive(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "zero-snapshot", Email: "zero@example.com", Quota: -5_000_000}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	setTestProjectStripeCustomer(t, db, project.ID, "cus_zero")
	order, err := BeginProjectTopUp(project.ID, owner.Id, "zero-snapshot", 5, ProjectCheckoutConfig{ReturnBase: "https://lazu.ai"})
	if err != nil {
		t.Fatal(err)
	}
	paid, _, err := CompleteProjectTopUp(ProjectPaymentReceipt{Reference: order.TradeNo, CustomerID: "cus_zero", SessionID: "cs_zero", PaymentIntentID: "pi_zero", PaidMicrousd: 5_000_000, Currency: "USD", Method: "card", ReceiptURL: "https://pay.stripe.com/receipts/zero"})
	if err != nil {
		t.Fatal(err)
	}
	if paid.BalanceAfterMicrousd == nil || *paid.BalanceAfterMicrousd != 0 {
		t.Fatalf("missing zero snapshot: %+v", paid.BalanceAfterMicrousd)
	}
	if err := db.Model(&owner).Update("quota", 99_000_000).Error; err != nil {
		t.Fatal(err)
	}
	if err := UpdateProjectInvoice(ProjectInvoiceUpdate{Reference: order.TradeNo, CustomerID: "cus_zero", InvoiceID: "in_zero", Number: "INV-ZERO", HostedURL: "https://invoice.stripe.com/i/zero", PDFURL: "https://pay.stripe.com/invoice/zero/pdf", Ready: true}); err != nil {
		t.Fatal(err)
	}
	var notice ProjectNotification
	if err := db.Where("event_key = ?", fmt.Sprintf("payment:%s:%d", order.PublicID, owner.Id)).First(&notice).Error; err != nil {
		t.Fatal(err)
	}
	email, err := notice.Email()
	if err != nil || !strings.Contains(email.HTML, "$0.00") || strings.Contains(email.HTML, "$99.00") || !strings.Contains(email.HTML, "View invoice") {
		t.Fatalf("snapshot changed during upgrade: %v", err)
	}
}

func TestPaymentSchemaRequiresBalanceSnapshot(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&ProjectDataMigration{}, &SettlementOutbox{}, &SettlementFailure{}); err != nil {
		t.Fatal(err)
	}
	if err := validateTeamProjectSchema(db, schemaVersionPrimary); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE top_ups DROP COLUMN balance_after_microusd").Error; err != nil {
		t.Fatal(err)
	}
	if err := validateTeamProjectSchema(db, schemaVersionPrimary); err == nil || !strings.Contains(err.Error(), "balance_after_microusd") {
		t.Fatalf("missing snapshot accepted: %v", err)
	}
	if err := db.AutoMigrate(&TopUp{}); err != nil {
		t.Fatal(err)
	}
	if err := validateTeamProjectSchema(db, schemaVersionPrimary); err != nil {
		t.Fatal(err)
	}
}

// setTestProjectStripeCustomer binds a Stripe customer to a project. Since
// schema version 15 the customer lives only on the project; a new default
// project no longer inherits one from the owner's user row.
func setTestProjectStripeCustomer(t *testing.T, db *gorm.DB, projectID int, customerID string) {
	t.Helper()
	if err := db.Model(&Project{}).Where("id = ?", projectID).Update("stripe_customer_id", customerID).Error; err != nil {
		t.Fatal(err)
	}
}
