package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestIdentitySnapshotPreservesPrivateBudgetProjectScope(t *testing.T) {
	budget := SpendBudget{ID: 401, ProjectID: 201, Scope: SpendBudgetScopeOrg, Period: SpendBudgetPeriodMonth, LimitMicrousd: 1000, Enabled: true}
	public, err := json.Marshal(budget)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "project_id") {
		t.Fatal("public spend-budget contract exposed its private project ID")
	}
	snapshot := IdentitySnapshot{Token: &Token{Id: 301}, Actor: &UserBase{Id: 101}, Payer: &UserBase{Id: 101}, Budgets: []SpendBudget{budget}}
	raw, err := json.Marshal(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var restored IdentitySnapshot
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Budgets) != 1 || restored.Budgets[0].ProjectID != budget.ProjectID {
		t.Fatalf("private identity projection lost project budget scope: %+v", restored.Budgets)
	}
	if got := LedgerBudgets(restored.Budgets, time.Now())[0].ProjectID; got != budget.ProjectID {
		t.Fatalf("admission budget changed project scope: %d", got)
	}
}

func TestIdentitySnapshotRejectsLegacyBudgetProjectionWithoutProjectScope(t *testing.T) {
	raw := []byte(`{"token":{},"actor":{},"payer":{},"budgets":[{"id":401,"scope":"org","period":"month","limit_microusd":1000}]}`)
	var restored IdentitySnapshot
	if err := json.Unmarshal(raw, &restored); !errors.Is(err, ErrIdentityStale) {
		t.Fatalf("legacy projection with ambiguous budget scope was admitted: %+v %v", restored.Budgets, err)
	}
}

func TestIdentitySnapshotLegacyDirectoryGenerationDoesNotFenceWarmCredential(t *testing.T) {
	raw := []byte(`{"token":{},"actor":{},"payer":{},"generations":[{"key":"gen:directory","value":"17"},{"key":"gen:policy","value":"18"},{"key":"gen:key:digest","value":"19"},{"key":"gen:user:101","value":"20"},{"key":"gen:project:201","value":"21"}],"directory_version":"17","cached_until":1791122400000}`)
	var snapshot IdentitySnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Generations) != 4 || snapshot.Generations[0].Key != "gen:policy" {
		t.Fatalf("old snapshot retains global warm directory fence or lost policy fence: %+v", snapshot.Generations)
	}
	for _, generation := range snapshot.Generations {
		if generation.Key == "gen:directory" {
			t.Fatal("old warm snapshot still observes directory creates")
		}
	}
	if snapshot.DirectoryVersion != "17" || snapshot.CachedUntil != 1791122400000 {
		t.Fatalf("cache compatibility changed discovery marker or expiry: %+v", snapshot)
	}
}

func TestIdentityConcurrentCreatesKeepUnrelatedWarmKeyWithoutSQLPostgres(t *testing.T) {
	db, client, key, _ := refreshPostgresFixture(t)
	if err := db.AutoMigrate(&EphemeralCredential{}); err != nil {
		t.Fatal(err)
	}
	seed := db.Session(&gorm.Session{SkipHooks: true})
	for _, row := range []interface{}{
		&User{Id: 102, PublicID: common.GeneratePublicID("usr"), Username: "create-other-owner", Status: common.UserStatusEnabled},
		&Project{ID: 202, PublicID: common.GeneratePublicID("prj"), OwnerUserID: 102, Name: "create-other-project"},
		&ProjectMember{ProjectID: 202, UserID: 102, Status: ProjectMemberActive, Role: ProjectRoleOwner},
		&Token{Id: 302, PublicID: common.GeneratePublicID("tok"), UserId: 102, ProjectID: 202, Key: common.GenerateRequestID(), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true},
	} {
		if err := seed.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	previousEngine := gatewayLedger.Load()
	gatewayLedger.Store(ledger.New(client, redisLedgerStorage{}))
	t.Cleanup(func() { gatewayLedger.Store(previousEngine) })
	if err := InstallIdentityMutationFence(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	warm, err := ResolveIdentitySnapshot(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	entered, sqlRelease, commitRelease := make(chan struct{}, 3), make(chan struct{}), make(chan struct{})
	if err := db.Callback().Create().After("identity:begin").Before("gorm:create").Register("test:create-barrier", func(tx *gorm.DB) {
		if tx.Statement.Table != "users" && tx.Statement.Table != "tokens" && tx.Statement.Table != "ephemeral_credentials" {
			return
		}
		entered <- struct{}{}
		select {
		case <-sqlRelease:
		case <-tx.Statement.Context.Done():
			tx.AddError(tx.Statement.Context.Err())
		}
	}); err != nil {
		t.Fatal(err)
	}
	var joins atomic.Int32
	if err := db.Callback().Row().Before("gorm:row").Register("test:create-warm-sql", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*Token); ok && len(tx.Statement.Joins) > 0 {
			joins.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove("test:create-barrier")
		_ = db.Callback().Row().Remove("test:create-warm-sql")
	})
	created, results := make(chan struct{}, 3), make(chan error, 3)
	rows := []interface{}{
		&User{PublicID: common.GeneratePublicID("usr"), Username: "create-new-owner", Status: common.UserStatusEnabled},
		&Token{PublicID: common.GeneratePublicID("tok"), UserId: 102, ProjectID: 202, Key: common.GenerateRequestID(), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true},
		&EphemeralCredential{UserID: 102, TokenID: 302, CredentialDigest: common.GenerateRequestID(), ExpiresAt: time.Now().Add(time.Hour).Unix()},
	}
	for _, row := range rows {
		go func(row interface{}) {
			results <- db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(row).Error; err != nil {
					return err
				}
				created <- struct{}{}
				<-commitRelease
				return nil
			})
		}(row)
	}
	// Always release and collect writers even if an assertion below fails.
	finished := false
	defer func() {
		if finished {
			return
		}
		close(sqlRelease)
		close(commitRelease)
		for range rows {
			if err := <-results; err != nil {
				t.Error(err)
			}
		}
	}()
	for range rows {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("create did not reach its identity fence")
		}
	}
	assertWarm := func(stage string) {
		t.Helper()
		for i := 0; i < 3; i++ {
			snapshot, err := ResolveIdentitySnapshot(ctx, key)
			if err != nil || snapshot == nil || snapshot.Token.Id != warm.Token.Id {
				t.Fatalf("%s: unrelated warm credential failed during concurrent creates: %v", stage, err)
			}
		}
		if joins.Load() != 0 {
			t.Fatalf("%s: unrelated warm credential performed %d authoritative SQL reads", stage, joins.Load())
		}
	}
	assertWarm("before create SQL")
	// Release SQL without closing the channel here: the deferred release must
	// remain safe even when the first assertion exposes the original defect.
	for range rows {
		sqlRelease <- struct{}{}
	}
	for range rows {
		select {
		case <-created:
		case <-time.After(5 * time.Second):
			t.Fatal("create SQL did not finish")
		}
	}
	assertWarm("before outer commit")
	newScopes := []string{
		fmt.Sprintf("user:%d", rows[0].(*User).Id),
		fmt.Sprintf("token:%d", rows[1].(*Token).Id),
		fmt.Sprintf("ephemeral:%d", rows[2].(*EphemeralCredential).ID),
	}
	for _, scope := range newScopes {
		if _, err := readIdentityGenerations(ctx, []string{scope}, true); !errors.Is(err, ErrIdentityUpdating) {
			t.Fatalf("new object's assigned identity was not fenced before commit: scope=%s err=%v", scope, err)
		}
	}
	close(sqlRelease)
	close(commitRelease)
	for range rows {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	finished = true
	assertWarm("after outer commit")
	for _, scope := range newScopes {
		if _, err := readIdentityGenerations(ctx, []string{scope}, false); err != nil {
			t.Fatalf("new object's fence did not finish on commit: scope=%s err=%v", scope, err)
		}
	}
	// The narrower create protocol must preserve ordinary mutation safety.
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	if err := tx.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", 201, 101).Update("status", ProjectMemberSuspended).Error; err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := CheckIdentityGenerations(ctx, warm.Generations); !errors.Is(err, ErrIdentityUpdating) {
		_ = tx.Rollback()
		t.Fatalf("existing member mutation lost its pending fence: %v", err)
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	if err := CheckIdentityGenerations(ctx, warm.Generations); !errors.Is(err, ErrIdentityStale) {
		t.Fatalf("existing member mutation lost its completion fence: %v", err)
	}
}

func TestIdentityUnresolvedCreateUpsertRemainsFailClosedPostgres(t *testing.T) {
	db, client, key, _ := refreshPostgresFixture(t)
	previousEngine := gatewayLedger.Load()
	gatewayLedger.Store(ledger.New(client, redisLedgerStorage{}))
	t.Cleanup(func() { gatewayLedger.Store(previousEngine) })
	if err := InstallIdentityMutationFence(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	warm, err := ResolveIdentitySnapshot(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	user := User{PublicID: common.GeneratePublicID("usr"), Username: "refresh-owner", Status: common.UserStatusDisabled}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	if err := tx.Session(&gorm.Session{SkipHooks: true}).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "username"}},
		DoUpdates: clause.AssignmentColumns([]string{"status"}),
	}).Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := CheckIdentityGenerations(ctx, warm.Generations); !errors.Is(err, ErrIdentityUpdating) {
		t.Fatalf("unresolved upsert bypassed the pending policy fence: %v", err)
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	if err := CheckIdentityGenerations(ctx, warm.Generations); !errors.Is(err, ErrIdentityStale) {
		t.Fatalf("unresolved upsert rollback lost its completion fence: %v", err)
	}
}

func TestIdentityCachedProjectBudgetsUseIndependentSpendAndRejectPostgres(t *testing.T) {
	db, client, key, _ := refreshPostgresFixture(t)
	if err := db.AutoMigrate(&ProjectSpend{}, &SpendBudgetCharge{}, &RedisLedgerOperation{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&User{}).Where("id = ?", 101).Update("quota", 10000).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&SpendBudget{}, 401).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	seed := db.Session(&gorm.Session{SkipHooks: true})
	for _, row := range []interface{}{
		&User{Id: 102, PublicID: common.GeneratePublicID("usr"), Username: "budget-other-owner", Status: common.UserStatusEnabled, Quota: 10000},
		&Project{ID: 202, PublicID: common.GeneratePublicID("prj"), OwnerUserID: 102, Name: "budget-other-project"},
		&ProjectMember{ProjectID: 202, UserID: 102, Status: ProjectMemberActive, Role: ProjectRoleOwner},
		&SpendBudget{ID: 411, ProjectID: 201, Scope: SpendBudgetScopeOrg, Period: SpendBudgetPeriodMonth, LimitMicrousd: 1000, Enforcement: SpendBudgetEnforcementBlock, Enabled: true},
		&SpendBudget{ID: 412, ProjectID: 202, Scope: SpendBudgetScopeOrg, Period: SpendBudgetPeriodMonth, LimitMicrousd: 1000, Enforcement: SpendBudgetEnforcementBlock, Enabled: true},
		&ProjectSpend{EventKey: "identity-scope-project-one", ProjectID: 201, ActorUserID: 101, PayerUserID: 101, TokenID: 301, RequestID: "prior-one", AmountMicrousd: 900, OccurredAt: now},
		&ProjectSpend{EventKey: "identity-scope-project-two", ProjectID: 202, ActorUserID: 102, PayerUserID: 102, TokenID: 302, RequestID: "prior-two", AmountMicrousd: 100, OccurredAt: now},
	} {
		if err := seed.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	clearFinancial := func() {
		keys, err := client.Keys(ctx, ledger.Prefix+"*").Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) > 0 {
			if err := client.Del(ctx, keys...).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	clearFinancial()
	t.Cleanup(clearFinancial)
	snapshot, err := ResolveIdentitySnapshot(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Budgets) != 1 {
		t.Fatalf("project-one snapshot has unexpected budgets: %+v", snapshot.Budgets)
	}
	otherKey, err := common.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Create(&Token{Id: 302, PublicID: common.GeneratePublicID("tok"), ProjectID: 202, UserId: 102, Key: otherKey, KeyDigest: TokenKeyDigest(otherKey), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}).Error; err != nil {
		t.Fatal(err)
	}
	other, err := ResolveIdentitySnapshot(ctx, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Del(ctx, "identity:"+TokenKeyDigest(otherKey)).Err() })
	engine := ledger.New(client, redisLedgerStorage{})
	engine.Continuity = common.GenerateRequestID()
	if err := client.Set(ctx, ledger.ContinuityKey, engine.Continuity, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, identity := range []*IdentitySnapshot{snapshot, other} {
		input := ledger.Input{RequestID: fmt.Sprintf("identity-budget-%d", index), PayerID: identity.Principal.PayerUserID, ActorID: identity.Principal.ActorUserID, ProjectID: identity.Principal.ProjectID, TokenID: identity.Token.Id, Unlimited: true, Amount: 200, OccurredAt: now, Budgets: LedgerBudgets(identity.Budgets, now)}
		state, err := (redisLedgerStorage{}).Snapshot(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		want := int64(900)
		if index == 1 {
			want = 100
		}
		if len(input.Budgets) != 1 || state.Budgets[input.Budgets[0].Key()] != want {
			t.Fatalf("project %d identity roundtrip read wrong spend: budgets=%+v state=%+v want=%d", identity.Principal.ProjectID, input.Budgets, state.Budgets, want)
		}
		// Exercise Redis's real budget admission after initializing only the
		// financial continuity marker for this isolated fixture.
		err = engine.Reserve(ctx, input)
		var rejection *ledger.Rejection
		if index == 0 {
			if !errors.As(err, &rejection) || rejection.BudgetID != identity.Budgets[0].ID {
				t.Fatalf("project-one over-budget request admitted: %v", err)
			}
		} else if err != nil {
			t.Fatalf("project-two budget was charged for project-one spend: %v", err)
		}
	}
}
