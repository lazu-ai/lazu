package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"gorm.io/gorm"
)

func TestIdentitySnapshotKeepsAuthIDsAndOmitsCredential(t *testing.T) {
	now := time.Now()
	key := "sk-" + strings.Repeat("a", 48)
	snapshot := &IdentitySnapshot{Token: &Token{Id: 42, UserId: 7, ProjectID: 12, Key: key, KeyDigest: TokenKeyDigest(key), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, RemovalOperationID: "removed-1"}, Actor: &UserBase{Id: 7, Status: common.UserStatusEnabled}, Payer: &UserBase{Id: 8, Status: common.UserStatusEnabled}, Principal: ProjectPrincipal{ProjectID: 12, PayerUserID: 8, ActorUserID: 7, TokenID: 42}, MemberStatus: ProjectMemberActive, ProjectUpgraded: true, CachedUntil: now.Add(time.Minute).UnixMilli()}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), key) {
		t.Fatal("serialized identity contains the clear credential")
	}
	var restored IdentitySnapshot
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Token.Id != 42 || restored.Token.UserId != 7 || restored.Token.ProjectID != 12 || restored.Token.RemovalOperationID != "removed-1" || restored.Token.Key != "" {
		t.Fatalf("identity lost authentication fields: %+v", restored.Token)
	}
	if err := restored.Validate(now); !errors.Is(err, ErrProjectAccessSuspended) {
		t.Fatalf("removal fence lost: %v", err)
	}
	restored.Token.RemovalOperationID = ""
	if err := restored.Validate(now); err != nil {
		t.Fatal(err)
	}
	restored.CredentialExpiresAt = now.Unix()
	if err := restored.Validate(now); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("expired alias accepted: %v", err)
	}
	restored.CredentialExpiresAt = 0
	restored.Token.ExpiredTime = now.Unix()
	if err := restored.Validate(now); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired key accepted: %v", err)
	}
}

func identityRedisTest(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled = true
	common.RDB = client
	t.Cleanup(func() {
		common.RedisEnabled = previousEnabled
		common.RDB = previousRDB
		_ = client.Close()
		identitySnapshotL1.clear()
	})
	return client
}

func TestIdentityConcurrentMutationsAndMissingGenerationIntegration(t *testing.T) {
	client := identityRedisTest(t)
	ctx := context.Background()
	scope := "test:" + common.GeneratePublicID("idtest")
	before, err := readIdentityGenerations(ctx, []string{scope}, true)
	if err != nil {
		t.Fatal(err)
	}
	first, err := beginIdentityMutation(ctx, []string{scope})
	if err != nil {
		t.Fatal(err)
	}
	second, err := beginIdentityMutation(ctx, []string{scope})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.finish(); err != nil {
		t.Fatal(err)
	}
	if err := CheckIdentityGenerations(ctx, before); !errors.Is(err, ErrIdentityUpdating) {
		t.Fatalf("first writer cleared another writer's pending operation: %v", err)
	}
	if err := second.finish(); err != nil {
		t.Fatal(err)
	}
	if err := CheckIdentityGenerations(ctx, before); !errors.Is(err, ErrIdentityStale) {
		t.Fatalf("old identity admitted after mutation: %v", err)
	}
	fresh, err := readIdentityGenerations(ctx, []string{scope}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, fresh[0].Key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := CheckIdentityGenerations(ctx, fresh); !errors.Is(err, ErrIdentityStale) {
		t.Fatalf("missing generation accepted: %v", err)
	}
	rebuilt, err := readIdentityGenerations(ctx, []string{scope}, true)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt[0].Value == fresh[0].Value {
		t.Fatal("Redis rebuild reused a generation")
	}
	_ = client.Del(ctx, identityGenerationKeys(rebuilt)...).Err()
}

func TestIdentityPendingBoundedWaitAndRedisFailureIntegration(t *testing.T) {
	client := identityRedisTest(t)
	ctx := context.Background()
	scope := "test:" + common.GeneratePublicID("idwait")
	generations, err := readIdentityGenerations(ctx, []string{scope}, true)
	if err != nil {
		t.Fatal(err)
	}
	key := common.APITokenPrefix + strings.Repeat("w", 48)
	snapshot := &IdentitySnapshot{Token: &Token{Id: 42, UserId: 7, ProjectID: 12, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}, Actor: &UserBase{Id: 7, Status: common.UserStatusEnabled}, Payer: &UserBase{Id: 7, Status: common.UserStatusEnabled}, Principal: ProjectPrincipal{ProjectID: 12, PayerUserID: 7, ActorUserID: 7, TokenID: 42}, MemberStatus: ProjectMemberActive, Generations: generations, CachedUntil: time.Now().Add(time.Minute).UnixMilli()}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	identitySnapshotL1.set(TokenKeyDigest(key), string(raw), time.Now())
	mutation, err := beginIdentityMutation(ctx, []string{scope})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := ResolveIdentitySnapshot(ctx, key); !errors.Is(err, ErrIdentityUpdating) {
		t.Fatalf("pending identity admitted: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 190*time.Millisecond || elapsed > time.Second {
		t.Fatalf("pending wait not bounded near 200ms: %v", elapsed)
	}
	if err := mutation.finish(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	// With the shared cache unavailable, the held L1 entry cannot authorize a
	// request and cannot fall back to SQL without checking revocation state.
	if value, err := ResolveIdentitySnapshot(ctx, key); err == nil || value != nil {
		t.Fatalf("L1 admitted during Redis failure: %+v %v", value, err)
	}
}

type identityDeadlinePool struct {
	gorm.ConnPool
	ctx context.Context
}

func (p *identityDeadlinePool) BeginTx(ctx context.Context, _ *sql.TxOptions) (gorm.ConnPool, error) {
	p.ctx = ctx
	return &identityDeadlineTransaction{}, nil
}

type identityDeadlineTransaction struct{ gorm.ConnPool }

func (*identityDeadlineTransaction) Commit() error   { return nil }
func (*identityDeadlineTransaction) Rollback() error { return nil }

func TestIdentityTransactionDeadlineAlwaysShorterThanPendingLease(t *testing.T) {
	underlying := &identityDeadlinePool{}
	pool := &identityConnPool{ConnPool: underlying}
	tx, err := pool.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline, ok := underlying.ctx.Deadline()
	if !ok {
		t.Fatal("SQL transaction has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining > identityTransactionTimeout || remaining >= identityPendingTTL {
		t.Fatalf("transaction can outlive pending lease: %v", remaining)
	}
	if err := tx.(gorm.TxCommitter).Commit(); err != nil {
		t.Fatal(err)
	}
	if underlying.ctx.Err() != context.Canceled {
		t.Fatal("commit did not cancel transaction deadline")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := pool.BeginTx(ctx, nil); err != nil {
		t.Fatal(err)
	}
	shorter, _ := underlying.ctx.Deadline()
	if time.Until(shorter) > time.Second {
		t.Fatal("wrapper widened caller deadline")
	}
}

func TestIdentityLedgerSkipKeepsReusableTransactionAndMarker(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "identity-skip-owner", Status: common.UserStatusEnabled}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		tx = SkipIdentityCounterRefresh(tx)
		if !identityCounterRefreshSkipped(tx.Model(&User{})) {
			return errors.New("skip marker lost on fresh User statement")
		}
		if err := tx.Model(&User{}).Where("id = ?", owner.Id).Update("quota", 100).Error; err != nil {
			return err
		}
		// Reusing the base after a User update must create WalletLedger with its
		// own schema; a mutable Set chain previously panicked on EventID.
		row := WalletLedger{EventID: "skip-regression", UserID: owner.Id, DeltaMicrousd: 100, Kind: "credit", RefType: "test", RefID: "skip", CreatedAt: time.Now()}
		if !identityCounterRefreshSkipped(tx.Model(&WalletLedger{})) {
			return errors.New("skip marker lost on second fresh statement")
		}
		return tx.Create(&row).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&WalletLedger{}).Where("event_id = ?", "skip-regression").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("wallet record lost to stale model chain: %d", count)
	}
}

// Preserve SQLite SQL behavior while enabling the same runtime protocol as
// PostgreSQL. Schema creation and fixtures happen before replacing its name.
type identityTestDialector struct{ gorm.Dialector }

func (identityTestDialector) Name() string { return "postgres" }

func TestIdentitySnapshotJoinReloadAndCommitFenceIntegration(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "identity-owner", Status: common.UserStatusEnabled}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	key := common.APITokenPrefix + strings.Repeat("i", 48)
	token := Token{UserId: owner.Id, ProjectID: project.ID, Key: key, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	client := identityRedisTest(t)
	previousEngine := gatewayLedger.Load()
	gatewayLedger.Store(ledger.New(client, redisLedgerStorage{}))
	t.Cleanup(func() { gatewayLedger.Store(previousEngine) })
	db.Dialector = identityTestDialector{db.Dialector}
	if err := InstallIdentityMutationFence(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snapshot, err := ResolveIdentitySnapshot(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Token.Id != token.Id || snapshot.Payer.Id != owner.Id || snapshot.Actor.Id != owner.Id || snapshot.Principal.ProjectID != project.ID {
		t.Fatalf("bad joined snapshot: %+v", snapshot)
	}
	if snapshot.Payer.Quota != 0 || snapshot.Token.Key != key {
		t.Fatal("snapshot mixed accounting state or credential")
	}
	firstVersion := snapshot.DirectoryVersion
	for _, update := range []struct {
		model  interface{}
		values map[string]interface{}
	}{
		{&User{}, map[string]interface{}{"first_request_at": 10, "last_request_at": 20, "used_quota": 3}},
		{&Token{}, map[string]interface{}{"last_used_at": 20, "used_quota": 3}},
	} {
		id := owner.Id
		if _, ok := update.model.(*Token); ok {
			id = token.Id
		}
		if err := db.Model(update.model).Where("id = ?", id).Updates(update.values).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckIdentityGenerations(ctx, snapshot.Generations); err != nil {
		t.Fatalf("observational usage stamp invalidated an unchanged identity: %v", err)
	}
	// Expiry must be checked even on L1 and Redis hits without a write event.
	snapshot.CredentialExpiresAt = time.Now().Add(-time.Second).Unix()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	identitySnapshotL1.set(TokenKeyDigest(key), string(raw), time.Now())
	if _, err := ResolveIdentitySnapshot(ctx, key); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("L1 alias expiry accepted: %v", err)
	}
	identitySnapshotL1.clear()
	if err := client.Set(ctx, "identity:"+TokenKeyDigest(key), string(raw), time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveIdentitySnapshot(ctx, key); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("Redis alias expiry accepted: %v", err)
	}
	if err := client.Del(ctx, "identity:"+TokenKeyDigest(key)).Err(); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	if err := tx.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", project.ID, owner.Id).Update("status", ProjectMemberSuspended).Error; err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := CheckIdentityGenerations(ctx, snapshot.Generations); !errors.Is(err, ErrIdentityUpdating) {
		_ = tx.Rollback()
		t.Fatalf("pending cleared before outer commit: %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	reloaded, err := ResolveIdentitySnapshot(ctx, key)
	if !errors.Is(err, ErrProjectAccessSuspended) {
		t.Fatalf("member suspension not immediate: %v", err)
	}
	if reloaded == nil || reloaded.DirectoryVersion == firstVersion {
		t.Fatal("snapshot was not reloaded after commit")
	}
	// A rollback also advances the fence but retains the committed state.
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ProjectMember{}).Where("project_id = ? AND user_id = ?", project.ID, owner.Id).Update("status", ProjectMemberActive).Error; err != nil {
			return err
		}
		return errors.New("test rollback")
	})
	if err == nil {
		t.Fatal("transaction unexpectedly committed")
	}
	if _, err := ResolveIdentitySnapshot(ctx, key); !errors.Is(err, ErrProjectAccessSuspended) {
		t.Fatalf("rollback restored unauthorized state: %v", err)
	}
	currentSnapshot, _ := ResolveIdentitySnapshot(ctx, key)
	identityCounterOutbox.RLock()
	previousOutbox := identityCounterOutbox.write
	identityCounterOutbox.RUnlock()
	t.Cleanup(func() { SetIdentityCounterRefreshOutbox(previousOutbox) })
	refreshes := 0
	SetIdentityCounterRefreshOutbox(func(tx *gorm.DB, mutation IdentityCounterMutation) error {
		if mutation.Table != "users" || mutation.UserID != owner.Id {
			return errors.New("wrong administrator refresh identity")
		}
		refreshes++
		return nil
	})
	admin := db.Begin()
	if err := admin.Model(&User{}).Where("id = ?", owner.Id).Updates(map[string]interface{}{"quota": 50, "last_request_at": 30}).Error; err != nil {
		_ = admin.Rollback()
		t.Fatal(err)
	}
	if err := CheckIdentityGenerations(ctx, currentSnapshot.Generations); !errors.Is(err, ErrIdentityUpdating) {
		_ = admin.Rollback()
		t.Fatalf("administrator quota update was treated as an observation: %v", err)
	}
	if err := admin.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 {
		t.Fatalf("administrator baseline refresh outbox count: %d", refreshes)
	}
	if err := CheckIdentityGenerations(ctx, currentSnapshot.Generations); !errors.Is(err, ErrIdentityStale) {
		t.Fatalf("administrator quota commit did not invalidate hot baseline: %v", err)
	}
	var rawSQLDeadline time.Duration
	if err := db.Callback().Raw().After("identity:begin").Before("gorm:raw").Register("test:raw-deadline", func(tx *gorm.DB) {
		deadline, ok := tx.Statement.Context.Deadline()
		if !ok {
			t.Error("raw identity mutation SQL has no deadline")
			return
		}
		rawSQLDeadline = time.Until(deadline)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE tokens SET status = ? WHERE id = ?", common.TokenStatusDisabled, token.Id).Error; err != nil {
		t.Fatal(err)
	}
	if rawSQLDeadline <= 0 || rawSQLDeadline > identityTransactionTimeout || rawSQLDeadline >= identityPendingTTL {
		t.Fatalf("raw SQL can outlive mutation registration: %v", rawSQLDeadline)
	}
	_ = client.Del(ctx, "identity:"+TokenKeyDigest(key)).Err()
}

func TestIdentityVerifiedCredentialsRemainFencedAndUncachedIntegration(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&EphemeralCredential{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "identity-trusted", Status: common.UserStatusEnabled}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	scope := "extension"
	token := Token{UserId: owner.Id, ProjectID: project.ID, InternalScope: &scope, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	credential := EphemeralCredential{UserID: owner.Id, TokenID: token.Id, CredentialDigest: "identity-ephemeral", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
	client := identityRedisTest(t)
	previousEngine := gatewayLedger.Load()
	gatewayLedger.Store(ledger.New(client, redisLedgerStorage{}))
	t.Cleanup(func() { gatewayLedger.Store(previousEngine) })
	db.Dialector = identityTestDialector{db.Dialector}
	if err := InstallIdentityMutationFence(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snapshot, err := ResolveVerifiedIdentitySnapshot(ctx, token.Id, credential.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Token.IsInternal() || snapshot.CredentialExpiresAt != credential.ExpiresAt {
		t.Fatalf("trusted snapshot lost ownership or clock fence: %+v", snapshot)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var restored IdentitySnapshot
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(time.Now()); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("trusted internal authorization leaked into ordinary key cache: %v", err)
	}
	if err := db.Model(&EphemeralCredential{}).Where("id = ?", credential.ID).Update("last_used_at", time.Now().Unix()).Error; err != nil {
		t.Fatal(err)
	}
	if err := CheckIdentityGenerations(ctx, snapshot.Generations); err != nil {
		t.Fatalf("ephemeral usage stamp invalidated identity: %v", err)
	}
	if err := db.Model(&EphemeralCredential{}).Where("id = ?", credential.ID).Update("revoked_at", time.Now().Unix()).Error; err != nil {
		t.Fatal(err)
	}
	if err := CheckIdentityGenerations(ctx, snapshot.Generations); !errors.Is(err, ErrIdentityStale) {
		t.Fatalf("ephemeral revocation did not fence admitted snapshot: %v", err)
	}
	if _, err := ResolveVerifiedIdentitySnapshot(ctx, token.Id, credential.ID); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("revoked uncommon credential accepted: %v", err)
	}
	playground, err := ResolvePlaygroundIdentitySnapshot(ctx, owner.Id)
	if err != nil {
		t.Fatal(err)
	}
	if playground.Token.Id != 0 || !playground.Token.UnlimitedQuota || playground.Principal.PayerUserID != owner.Id || playground.Principal.ActorUserID != owner.Id {
		t.Fatalf("playground lost authenticated ownership: %+v", playground)
	}
	encoded, err = json.Marshal(playground)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(time.Now()); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("playground private authorization leaked into ordinary key cache: %v", err)
	}
}
