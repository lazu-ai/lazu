package ledger

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func TestFinancialContinuityLossFencesAllMutationsIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	ctx := context.Background()
	input := testInput("accepted-before-loss", 100)
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	// No empty configuration can act as a development/runtime bypass.
	unbound := New(client, storage)
	if err := unbound.Reserve(ctx, testInput("unbound", 1)); !errors.Is(err, ErrContinuity) {
		t.Fatalf("unbound: %v", err)
	}
	client.Del(ctx, ContinuityKey)
	checks := []struct {
		name string
		run  func() error
	}{
		{"reserve", func() error { return engine.Reserve(ctx, testInput("after-loss", 1)) }},
		{"initialize", func() error { return engine.Initialize(ctx, input) }},
		{"settle", func() error { return engine.Settle(ctx, input, 80) }},
		{"release", func() error { return engine.Release(ctx, input) }},
		{"refund", func() error { return engine.Refund(ctx, input, 10, "refund-lost") }},
		{"credit", func() error { return engine.Credit(ctx, Credit{ID: 1, PayerID: 1, Sequence: 1, Amount: 500}) }},
		{"extend", func() error { return engine.Extend(ctx, input.RequestID, time.Now().Add(time.Hour)) }},
		{"split", func() error { _, _, err := engine.SplitReservation(ctx, input, "child-lost", 20); return err }},
		{"drain", func() error { _, err := engine.Drain(ctx, "old-worker"); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); !errors.Is(err, ErrContinuity) {
				t.Fatalf("mutation did not fail closed: %v", err)
			}
		})
	}
	if got := redisAvailable(t, client); got != 900 {
		t.Fatalf("lost continuity changed funds: %d", got)
	}
	engine.SpoolDir = t.TempDir()
	if err := engine.PersistSettlement(input, 80, "settle"); err != nil {
		t.Fatal(err)
	}
	// Recovery retains the financial identity but bumps admission generation.
	client.Set(ctx, ContinuityKey, "test-epoch:2", 0)
	if err := engine.ReplaySpool(ctx); !errors.Is(err, ErrContinuity) {
		t.Fatalf("old process resumed: %v", err)
	}
	recovered := New(client, storage)
	recovered.Continuity = "test-epoch:2"
	recovered.SpoolDir = engine.SpoolDir
	if err := recovered.ReplaySpool(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := recovered.Drain(ctx, "new-worker"); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 920 {
		t.Fatalf("spool replay charged twice or lost charge: %d", got)
	}
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if len(storage.applied) != 1 || storage.applied["settle:"+input.RequestID].Amount != 80 {
		t.Fatalf("changed financial identity: %+v", storage.applied)
	}
}

func TestColdConcurrentAdmissionWaitsForSingleRebuildIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	storage.snapshotStarted = make(chan struct{}, 1)
	storage.snapshotResume = make(chan struct{})
	engine := testEngine(client, storage)
	ctx := context.Background()
	results := make(chan error, 2)
	go func() { results <- engine.Reserve(ctx, testInput("cold-first", 100)) }()
	select {
	case <-storage.snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("cold snapshot did not begin")
	}
	go func() { results <- engine.Reserve(ctx, testInput("cold-second", 100)) }()
	// A short rebuild must not reject a concurrent first-use request.
	time.AfterFunc(30*time.Millisecond, func() { close(storage.snapshotResume) })
	for n := 0; n < 2; n++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cold admission exceeded bounded wait")
		}
	}
	if got := redisAvailable(t, client); got != 800 {
		t.Fatalf("concurrent cold admission lost hold: %d", got)
	}
	if err := client.Set(ctx, Prefix+"budget_fence", "unavailable-initializer", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := engine.Reserve(canceled, testInput("canceled-cold", 1)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cold retry ignored caller deadline: %v", err)
	}
}

type ledgerDispatchFailureHook struct {
	command string
	key     string
	failure error
	hits    int
}

func (hook *ledgerDispatchFailureHook) BeforeProcess(ctx context.Context, command redis.Cmder) (context.Context, error) {
	if command.Name() == hook.command && (hook.key == "" || len(command.Args()) > 1 && fmt.Sprint(command.Args()[1]) == hook.key) {
		hook.hits++
		return ctx, hook.failure
	}
	return ctx, nil
}
func (*ledgerDispatchFailureHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (*ledgerDispatchFailureHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (*ledgerDispatchFailureHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func assertCancellationKeptFunds(t *testing.T, client *redis.Client, input Input) {
	t.Helper()
	ctx := context.Background()
	if got := redisAvailable(t, client); got != 1000 {
		t.Fatalf("canceled operation changed wallet: %d", got)
	}
	if got := redisTokenRemaining(t, client); got != 1000 {
		t.Fatalf("canceled operation changed key remaining: %d", got)
	}
	if count, err := client.Exists(ctx, request(input.RequestID), fence(input.PayerID), Prefix+"budget_fence").Result(); err != nil || count != 0 {
		t.Fatalf("cancellation left an admitted request or initialization fence: count=%d err=%v", count, err)
	}
	if count, err := client.HLen(ctx, Prefix+"reservations").Result(); err != nil || count != 0 {
		t.Fatalf("cancellation created a reservation: count=%d err=%v", count, err)
	}
	if count, err := client.XLen(ctx, Prefix+"events").Result(); err != nil || count != 0 {
		t.Fatalf("cancellation emitted a financial event: count=%d err=%v", count, err)
	}
}

func TestLedgerRedisDispatchPreservesCancellationChainIntegration(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled} {
		for _, phase := range []struct {
			name, command, key string
			initialize         bool
		}{
			{"reserve_eval", "evalsha", "", false},
			{"continuity_get", "get", ContinuityKey, true},
			{"budget_fence", "set", Prefix + "budget_fence", true},
			{"payer_fence", "set", fence(1), true},
		} {
			t.Run(cause.Error()+"/"+phase.name, func(t *testing.T) {
				client := testRedis(t)
				engine := testEngine(client, newMemoryStorage(1000, 1000))
				if err := engine.Initialize(context.Background(), testInput("seed-cancellation-fixture", 1)); err != nil {
					t.Fatal(err)
				}
				// Fail exactly at dispatch; no timer, scheduler race or actual
				// network outage is needed to exercise the error wrapper.
				hook := &ledgerDispatchFailureHook{command: phase.command, key: phase.key, failure: fmt.Errorf("redis dispatch interrupted: %w", cause)}
				client.AddHook(hook)
				input := testInput("dispatch-cancellation-fixture", 100)
				var err error
				if phase.initialize {
					err = engine.Initialize(context.Background(), input)
				} else {
					err = engine.Reserve(context.Background(), input)
				}
				if hook.hits != 1 {
					t.Fatalf("did not exercise exactly one selected dispatch: %d", hook.hits)
				}
				if !errors.Is(err, ErrUnavailable) || !errors.Is(err, cause) {
					t.Errorf("dispatch lost financial/caller error classification: %v", err)
				}
				if phase.name == "continuity_get" && !errors.Is(err, ErrContinuity) {
					t.Errorf("continuity read failure lost its existing sentinel: %v", err)
				}
				assertCancellationKeptFunds(t, client, input)
			})
		}
	}
}

type ledgerSnapshotFailureStorage struct {
	*memoryStorage
	failure error
}

func (storage *ledgerSnapshotFailureStorage) Snapshot(context.Context, Input) (Snapshot, error) {
	return Snapshot{}, storage.failure
}

func TestColdInitializePreservesSnapshotCancellationIntegration(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled} {
		for _, unavailable := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/unavailable=%t", cause, unavailable), func(t *testing.T) {
				client := testRedis(t)
				storage := newMemoryStorage(1000, 1000)
				engine := testEngine(client, storage)
				if err := engine.Initialize(context.Background(), testInput("seed-snapshot-cancellation-fixture", 1)); err != nil {
					t.Fatal(err)
				}
				failure := fmt.Errorf("snapshot interrupted: %w", cause)
				if unavailable {
					failure = fmt.Errorf("%w: %w", ErrUnavailable, failure)
				}
				engine.Storage = &ledgerSnapshotFailureStorage{memoryStorage: storage, failure: failure}
				input := testInput("snapshot-cancellation-fixture", 100)
				err := engine.Initialize(context.Background(), input)
				if !errors.Is(err, cause) || errors.Is(err, ErrUnavailable) != unavailable {
					t.Errorf("snapshot failure changed its original classification: %v", err)
				}
				assertCancellationKeptFunds(t, client, input)
			})
		}
	}
}

func TestLedgerContinuityAbsentAndMismatchKeepSentinelIntegration(t *testing.T) {
	client := testRedis(t)
	ctx := context.Background()
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	if err := New(nil, storage).CheckContinuity(ctx); err != ErrContinuity {
		t.Fatalf("nil Redis changed continuity classification: %v", err)
	}
	if err := New(client, storage).CheckContinuity(ctx); err != ErrContinuity {
		t.Fatalf("unbound engine changed continuity classification: %v", err)
	}
	if err := client.Del(ctx, ContinuityKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.CheckContinuity(ctx); err != ErrContinuity {
		t.Fatalf("missing continuity changed sentinel: %v", err)
	}
	if err := client.Set(ctx, ContinuityKey, "different-epoch-fixture", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.CheckContinuity(ctx); err != ErrContinuity {
		t.Fatalf("mismatched continuity changed sentinel: %v", err)
	}
}
