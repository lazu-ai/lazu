package model

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func waitRefreshWaiters(t *testing.T, coordinator *identityRefreshCoordinator, digest string, count int) *identityRefreshFlight {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		flight := coordinator.flights[digest]
		ready := flight != nil && flight.waiters == count
		coordinator.mu.Unlock()
		if ready {
			return flight
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("refresh did not collect %d waiters", count)
	return nil
}

func TestIdentityRefreshCoordinatorCancellationCapacityAndNoNegativeCache(t *testing.T) {
	coordinator := newIdentityRefreshCoordinator(1)
	entered, release := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > identityTransactionTimeout {
			t.Error("shared read lost its bounded deadline")
		}
		close(entered)
		select {
		case <-release:
			return "serialized", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { _, err := coordinator.do(ctx, "same", load); first <- err }()
	<-entered
	go func() {
		raw, err := coordinator.do(context.Background(), "same", load)
		if raw != "serialized" && err == nil {
			err = errors.New("shared bytes lost")
		}
		second <- err
	}()
	waitRefreshWaiters(t, coordinator, "same", 2)
	if _, err := coordinator.do(context.Background(), "other", load); !errors.Is(err, errIdentityRefreshCapacity) {
		t.Fatalf("unbounded refreshes admitted: %v", err)
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatalf("one canceled waiter canceled a different request: %v", err)
	}
	var reads atomic.Int32
	failure := errors.New("SQL unavailable")
	for index := 0; index < 2; index++ {
		_, err := coordinator.do(context.Background(), "failed", func(context.Context) (string, error) { reads.Add(1); return "", failure })
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	if reads.Load() != 2 {
		t.Fatal("negative result was retained")
	}
}

func TestIdentityRefreshAllCanceledStopsReadAndAllowsFreshRead(t *testing.T) {
	coordinator := newIdentityRefreshCoordinator(1)
	ctx, cancel := context.WithCancel(context.Background())
	entered, stopped, result := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := coordinator.do(ctx, "same", func(readCtx context.Context) (string, error) {
			close(entered)
			<-readCtx.Done()
			close(stopped)
			return "", readCtx.Err()
		})
		result <- err
	}()
	<-entered
	flight := waitRefreshWaiters(t, coordinator, "same", 1)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("abandoned SQL read was not canceled")
	}
	<-flight.done
	raw, err := coordinator.do(context.Background(), "same", func(context.Context) (string, error) { return "new", nil })
	if err != nil || raw != "new" {
		t.Fatalf("fresh caller inherited canceled flight: %q %v", raw, err)
	}
}

func refreshPostgresFixture(t *testing.T) (*gorm.DB, *redis.Client, string, *identityRefreshCoordinator) {
	t.Helper()
	db, _ := postgresTelemetryFixture(t)
	if err := db.AutoMigrate(&Token{}, &Project{}, &ProjectMember{}, &SpendBudget{}, &TokenCredential{}); err != nil {
		t.Fatal(err)
	}
	key, err := common.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	seed := db.Session(&gorm.Session{SkipHooks: true})
	for _, row := range []interface{}{
		&User{Id: 101, Username: "refresh-owner", Status: common.UserStatusEnabled},
		&Project{ID: 201, OwnerUserID: 101, Name: "refresh-project"},
		&ProjectMember{ProjectID: 201, UserID: 101, Status: ProjectMemberActive, Role: ProjectRoleOwner},
		&Token{Id: 301, UserId: 101, ProjectID: 201, KeyDigest: TokenKeyDigest(key), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, ModelLimits: "model-a"},
		&SpendBudget{ID: 401, ProjectID: 201, Scope: SpendBudgetScopeToken, TokenID: 301, Enabled: true, LimitMicrousd: 1000},
	} {
		if err := seed.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	client := identityRedisTest(t)
	previous := identitySnapshotRefreshes
	coordinator := newIdentityRefreshCoordinator(1024)
	identitySnapshotRefreshes = coordinator
	identitySnapshotL1.clear()
	t.Cleanup(func() {
		coordinator.mu.Lock()
		flights := make([]*identityRefreshFlight, 0, len(coordinator.flights))
		for _, flight := range coordinator.flights {
			flights = append(flights, flight)
		}
		coordinator.mu.Unlock()
		for _, flight := range flights {
			flight.cancel()
			<-flight.done
		}
		identitySnapshotRefreshes = previous
		_ = client.Del(context.Background(), "identity:"+TokenKeyDigest(key)).Err()
		identitySnapshotL1.clear()
	})
	return db, client, key, coordinator
}

func refreshSQLBarrier(t *testing.T, db *gorm.DB) (chan struct{}, chan struct{}, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	entered, release := make(chan struct{}, 1), make(chan struct{})
	joins, budgets := new(atomic.Int32), new(atomic.Int32)
	if err := db.Callback().Row().Before("gorm:row").Register("test:refresh-join", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*Token); !ok || len(tx.Statement.Joins) == 0 {
			return
		}
		joins.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-tx.Statement.Context.Done():
			tx.AddError(tx.Statement.Context.Err())
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Query().Before("gorm:query").Register("test:refresh-budget", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "SpendBudget" {
			budgets.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Row().Remove("test:refresh-join")
		_ = db.Callback().Query().Remove("test:refresh-budget")
	})
	return entered, release, joins, budgets
}

type refreshResult struct {
	snapshot *IdentitySnapshot
	err      error
}

type refreshCommandCounter struct{ commands atomic.Int32 }

func (hook *refreshCommandCounter) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	hook.commands.Add(1)
	return ctx, nil
}
func (*refreshCommandCounter) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (hook *refreshCommandCounter) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	hook.commands.Add(1)
	return ctx, nil
}
func (*refreshCommandCounter) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func TestPostgresIdentityRefresh32ShareOnlyOneJoinAndBudgetRead(t *testing.T) {
	db, client, key, coordinator := refreshPostgresFixture(t)
	entered, release, joins, budgets := refreshSQLBarrier(t, db)
	results := make(chan refreshResult, 32)
	for index := 0; index < 32; index++ {
		go func() {
			snapshot, err := ResolveIdentitySnapshot(context.Background(), key)
			results <- refreshResult{snapshot, err}
		}()
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SQL join did not start")
	}
	queryEntered := time.Now()
	waitRefreshWaiters(t, coordinator, TokenKeyDigest(key), 32)
	time.Sleep(75 * time.Millisecond)
	close(release)
	var snapshots []*IdentitySnapshot
	for index := 0; index < 32; index++ {
		row := <-results
		if row.err != nil {
			t.Fatal(row.err)
		}
		if row.snapshot.CachedUntil > queryEntered.Add(identitySnapshotTTL).UnixMilli() {
			t.Fatal("waiter extended absolute SQL-start TTL")
		}
		if row.snapshot.Token.Key != key || len(row.snapshot.Budgets) != 1 {
			t.Fatal("projection lost credential/budget data")
		}
		snapshots = append(snapshots, row.snapshot)
	}
	if joins.Load() != 1 || budgets.Load() != 1 {
		t.Fatalf("32 cold callers used %d joins and %d budget reads", joins.Load(), budgets.Load())
	}
	snapshots[0].Token.ModelLimits = "changed"
	snapshots[0].Budgets[0].LimitMicrousd = 99
	snapshots[0].Actor.Username = "changed"
	snapshots[0].Generations[0].Value = "changed"
	if snapshots[1].Token.ModelLimits == "changed" || snapshots[1].Budgets[0].LimitMicrousd != 1000 || snapshots[1].Actor.Username == "changed" || snapshots[1].Generations[0].Value == "changed" {
		t.Fatal("waiters share mutable snapshot state")
	}
	if ttl := client.PTTL(context.Background(), "identity:"+TokenKeyDigest(key)).Val(); ttl >= identitySnapshotTTL-50*time.Millisecond {
		t.Fatalf("publish reset TTL after delayed SQL: %v", ttl)
	}
	commandCounter := new(refreshCommandCounter)
	client.AddHook(commandCounter)
	if _, err := ResolveIdentitySnapshot(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if commandCounter.commands.Load() != 1 {
		t.Fatalf("warm L1 hit used %d Redis dispatches instead of one generation check", commandCounter.commands.Load())
	}
	if joins.Load() != 1 || budgets.Load() != 1 {
		t.Fatal("warm request did not bypass SQL refresh")
	}
}

func TestPostgresIdentityRefreshExpiredProjectionCoalesces(t *testing.T) {
	db, client, key, coordinator := refreshPostgresFixture(t)
	snapshot, err := ResolveIdentitySnapshot(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	// A serialized projection remains stale even if Redis's key TTL or the
	// local cache entry lasts longer than its absolute authorization deadline.
	snapshot.CachedUntil = time.Now().Add(-time.Millisecond).UnixMilli()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	identitySnapshotL1.set(TokenKeyDigest(key), string(raw), time.Now())
	if err := client.Set(context.Background(), "identity:"+TokenKeyDigest(key), raw, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	entered, release, joins, budgets := refreshSQLBarrier(t, db)
	results := make(chan refreshResult, 32)
	for index := 0; index < 32; index++ {
		go func() {
			current, err := ResolveIdentitySnapshot(context.Background(), key)
			results <- refreshResult{current, err}
		}()
	}
	<-entered
	waitRefreshWaiters(t, coordinator, TokenKeyDigest(key), 32)
	close(release)
	for index := 0; index < 32; index++ {
		row := <-results
		if row.err != nil || row.snapshot.CachedUntil <= snapshot.CachedUntil {
			t.Fatalf("expired cohort did not refresh: %v", row.err)
		}
	}
	if joins.Load() != 1 || budgets.Load() != 1 {
		t.Fatalf("32 expired callers used %d joins and %d budget reads", joins.Load(), budgets.Load())
	}
}

func TestPostgresIdentityRefreshCancellationDoesNotAffectOtherWaiters(t *testing.T) {
	db, _, key, coordinator := refreshPostgresFixture(t)
	entered, release, joins, budgets := refreshSQLBarrier(t, db)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := ResolveIdentitySnapshot(ctx, key); first <- err }()
	<-entered
	others := make(chan error, 31)
	for index := 0; index < 31; index++ {
		go func() { _, err := ResolveIdentitySnapshot(context.Background(), key); others <- err }()
	}
	waitRefreshWaiters(t, coordinator, TokenKeyDigest(key), 32)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled leader returned %v", err)
	}
	waitRefreshWaiters(t, coordinator, TokenKeyDigest(key), 31)
	close(release)
	for index := 0; index < 31; index++ {
		if err := <-others; err != nil {
			t.Fatalf("canceled leader terminated another waiter: %v", err)
		}
	}
	if joins.Load() != 1 || budgets.Load() != 1 {
		t.Fatalf("cancellation repeated SQL: joins=%d budgets=%d", joins.Load(), budgets.Load())
	}
}

func TestPostgresIdentityRefreshAllCanceledStopsSQL(t *testing.T) {
	db, client, key, coordinator := refreshPostgresFixture(t)
	entered, release, joins, budgets := refreshSQLBarrier(t, db)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := ResolveIdentitySnapshot(ctx, key); result <- err }()
	<-entered
	flight := waitRefreshWaiters(t, coordinator, TokenKeyDigest(key), 1)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-flight.done:
	case <-time.After(time.Second):
		t.Fatal("shared SQL context did not stop after all waiters canceled")
	}
	if budgets.Load() != 0 || client.Exists(context.Background(), "identity:"+TokenKeyDigest(key)).Val() != 0 {
		t.Fatal("abandoned query continued or published a projection")
	}
	close(release)
	if _, err := ResolveIdentitySnapshot(context.Background(), key); err != nil {
		t.Fatalf("later caller inherited canceled SQL: %v", err)
	}
	if joins.Load() != 2 || budgets.Load() != 1 {
		t.Fatalf("fresh SQL counts joins=%d budgets=%d", joins.Load(), budgets.Load())
	}
}

func TestPostgresIdentityRefreshWaitersRejectMutationAndSQLFailure(t *testing.T) {
	for _, mode := range []string{"mutation", "SQL failure"} {
		t.Run(mode, func(t *testing.T) {
			db, client, key, coordinator := refreshPostgresFixture(t)
			entered, release, joins, _ := refreshSQLBarrier(t, db)
			if mode == "SQL failure" {
				if err := db.Callback().Row().Before("gorm:row").After("test:refresh-join").Register("test:refresh-error", func(tx *gorm.DB) {
					if _, ok := tx.Statement.Model.(*Token); ok && len(tx.Statement.Joins) > 0 {
						tx.AddError(errors.New("synthetic SQL unavailable"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			results := make(chan error, 32)
			for index := 0; index < 32; index++ {
				go func() { _, err := ResolveIdentitySnapshot(context.Background(), key); results <- err }()
			}
			<-entered
			waitRefreshWaiters(t, coordinator, TokenKeyDigest(key), 32)
			var mutation *identityMutation
			if mode == "mutation" {
				var err error
				mutation, err = beginIdentityMutation(context.Background(), []string{"key:" + TokenKeyDigest(key)})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = mutation.finish() }()
				time.Sleep(210 * time.Millisecond)
			}
			close(release)
			for index := 0; index < 32; index++ {
				if err := <-results; err == nil {
					t.Fatal("cold waiter admitted during mutation/SQL failure")
				}
			}
			if joins.Load() != 1 {
				t.Fatalf("shared failure repeated SQL %d times", joins.Load())
			}
			if mode == "SQL failure" {
				if err := db.Callback().Row().Remove("test:refresh-error"); err != nil {
					t.Fatal(err)
				}
				if _, err := ResolveIdentitySnapshot(context.Background(), key); err != nil {
					t.Fatalf("negative SQL failure cached: %v", err)
				}
			}
			if exists := client.Exists(context.Background(), "identity:"+TokenKeyDigest(key)).Val(); mode == "mutation" && exists != 0 {
				t.Fatal("pending mutation projection published")
			}
		})
	}
}

type refreshCheckDelay struct{ delay time.Duration }

func (hook refreshCheckDelay) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	args := cmd.Args()
	if cmd.Name() == "evalsha" && len(args) > 3 && args[1] == identityVersionsScript.Hash() && args[len(args)-2] == "check" {
		timer := time.NewTimer(hook.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx, ctx.Err()
		}
	}
	return ctx, nil
}
func (refreshCheckDelay) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (refreshCheckDelay) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (refreshCheckDelay) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

func TestPostgresIdentityProjectionFreshExpiryAndPendingChecks(t *testing.T) {
	_, client, key, _ := refreshPostgresFixture(t)
	snapshot, err := ResolveIdentitySnapshot(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.CachedUntil = time.Now().Add(20 * time.Millisecond).UnixMilli()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	client.AddHook(refreshCheckDelay{delay: 50 * time.Millisecond})
	if _, err := decodeIdentityProjection(context.Background(), key, string(raw)); !errors.Is(err, ErrIdentityStale) {
		t.Fatalf("cached deadline expired during generation check but admitted: %v", err)
	}
	snapshot.CachedUntil = time.Now().Add(time.Minute).UnixMilli()
	snapshot.CredentialExpiresAt = time.Now().Unix()
	raw, _ = json.Marshal(snapshot)
	if _, err := decodeIdentityProjection(context.Background(), key, string(raw)); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("expired alias admitted by waiter: %v", err)
	}
	snapshot.CredentialExpiresAt = 0
	raw, _ = json.Marshal(snapshot)
	mutation, err := beginIdentityMutation(context.Background(), []string{"key:" + TokenKeyDigest(key)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mutation.finish() }()
	if _, err := decodeIdentityProjection(context.Background(), key, string(raw)); !errors.Is(err, ErrIdentityUpdating) {
		t.Fatalf("waiter ignored pending fence: %v", err)
	}
	if strings.Contains(string(raw), key) {
		t.Fatal("shared projection contains credential")
	}
}
