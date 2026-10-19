package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

type memoryStorage struct {
	mu              sync.Mutex
	snapshot        Snapshot
	applied         map[string]Event
	applyCalls      int
	settledCalls    int
	failAfterCommit bool
	reject          map[string]bool
	credits         []Credit
	creditDone      map[uint]bool
	snapshotStarted chan struct{}
	snapshotResume  chan struct{}
}

func newMemoryStorage(balance, remaining int64) *memoryStorage {
	return &memoryStorage{snapshot: Snapshot{Balance: balance, TokenRemaining: map[int]int64{2: remaining}, Budgets: map[string]int64{}, BudgetReserved: map[string]int64{}}, applied: map[string]Event{}, creditDone: map[uint]bool{}}
}
func (s *memoryStorage) Snapshot(ctx context.Context, input Input) (Snapshot, error) {
	s.mu.Lock()
	copy := Snapshot{Balance: s.snapshot.Balance, CreditSeq: s.snapshot.CreditSeq, TokenRemaining: map[int]int64{}, Budgets: map[string]int64{}, BudgetReserved: map[string]int64{}}
	for key, value := range s.snapshot.TokenRemaining {
		copy.TokenRemaining[key] = value
	}
	for key, value := range s.snapshot.Budgets {
		copy.Budgets[key] = value
	}
	for _, budget := range input.Budgets {
		if _, ok := copy.Budgets[budget.Key()]; !ok {
			copy.Budgets[budget.Key()] = 0
		}
	}
	started, resume := s.snapshotStarted, s.snapshotResume
	s.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if resume != nil {
		select {
		case <-resume:
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
	return copy, nil
}
func (s *memoryStorage) Settled(_ context.Context, id, kind string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settledCalls++
	_, ok := s.applied[kind+":"+id]
	return ok, nil
}

func TestFixedRateBucketsShareAtomicFinancialAdmissionIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	current := time.Now()
	engine.Now = func() time.Time { return current }
	ctx := context.Background()
	rpm := RateLimit{Key: "token_rpm:2", Decision: "token_rpm_exceeded", Limit: 2, Cost: 1, WindowSeconds: 60}
	tpm := RateLimit{Key: "account_tpm:1", Decision: "account_tpm_exceeded", Limit: 100, Cost: 60, WindowSeconds: 60}
	bucketKey := func(rate RateLimit) string {
		return fmt.Sprintf("%srate:%s:%d", Prefix, rate.Key, current.UnixMilli()/(rate.WindowSeconds*1000))
	}
	first := testInput("rate-first", 100)
	first.Rates = []RateLimit{rpm, tpm}
	if err := engine.Reserve(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(ctx, first); err != nil {
		t.Fatal(err)
	}
	rejected := testInput("rate-rejected", 100)
	rejected.Rates = first.Rates
	var limit *Rejection
	if err := engine.Reserve(ctx, rejected); !errors.As(err, &limit) || limit.RateDecision != tpm.Decision || limit.RateLimit != 100 || limit.RetryAfterSeconds <= 0 || limit.RetryAfterSeconds > 60 {
		t.Fatalf("missing account TPM admission decision: %+v %v", limit, err)
	}
	if count, err := client.Get(ctx, bucketKey(rpm)).Int64(); err != nil || count != 1 {
		t.Fatalf("TPM rejection or duplicate admission spent RPM: %d %v", count, err)
	}
	if redisAvailable(t, client) != 900 {
		t.Fatal("TPM rejection spent funds")
	}
	insufficient := testInput("rate-insufficient", 2000)
	insufficient.Rates = []RateLimit{rpm}
	if err := engine.Reserve(ctx, insufficient); err == nil {
		t.Fatal("unfunded request admitted")
	}
	if count, _ := client.Get(ctx, bucketKey(rpm)).Int64(); count != 1 {
		t.Fatal("financial rejection spent a rate bucket")
	}
	if ttl, err := client.PTTL(ctx, bucketKey(rpm)).Result(); err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("fixed bucket does not expire at window boundary: %v %v", ttl, err)
	}
	// Expire the current fixed bucket without sleeping for a whole minute.
	if err := client.Del(ctx, bucketKey(rpm), bucketKey(tpm)).Err(); err != nil {
		t.Fatal(err)
	}
	newWindow := testInput("rate-next-window", 100)
	newWindow.Rates = first.Rates
	if err := engine.Reserve(ctx, newWindow); err != nil {
		t.Fatalf("fixed window did not reset: %v", err)
	}
	if count, _ := client.Get(ctx, bucketKey(rpm)).Int64(); count != 1 {
		t.Fatal("new window counter did not start at one")
	}
}

func TestConcurrentRateBucketsAndSuccessSettlementIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(10000, 10000)
	engine := testEngine(client, storage)
	ctx := context.Background()
	seed := testInput("rate-seed", 1)
	if err := engine.Initialize(ctx, seed); err != nil {
		t.Fatal(err)
	}
	rate := RateLimit{Key: "token_rpm:2", Decision: "token_rpm_exceeded", Limit: 10, Cost: 1, WindowSeconds: 3600}
	var admitted atomic.Int32
	var group sync.WaitGroup
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			input := testInput(fmt.Sprintf("rate-concurrent-%d", index), 1)
			input.Rates = []RateLimit{rate}
			if err := engine.Reserve(ctx, input); err == nil {
				admitted.Add(1)
			} else {
				var rejection *Rejection
				if !errors.As(err, &rejection) || rejection.RateDecision != rate.Decision {
					t.Errorf("unexpected concurrent rejection: %v", err)
				}
			}
		}(index)
	}
	group.Wait()
	if admitted.Load() != 10 || redisAvailable(t, client) != 9990 {
		t.Fatalf("concurrent rate cap drift: admitted=%d", admitted.Load())
	}
	success := RateLimit{Key: "success_rpm:1", Decision: "success_rpm_exceeded", Limit: 1, Cost: 1, WindowSeconds: 3600, OnSuccess: true}
	failed := testInput("success-release", 10)
	failed.Rates = []RateLimit{success}
	if err := engine.Reserve(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if err := engine.Release(ctx, failed); err != nil {
		t.Fatal(err)
	}
	finished := testInput("success-settle", 10)
	finished.Rates = []RateLimit{success}
	if err := engine.Reserve(ctx, finished); err != nil {
		t.Fatalf("release spent success capacity: %v", err)
	}
	if err := engine.Settle(ctx, finished, 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, finished, 0); err != nil {
		t.Fatal(err)
	}
	next := testInput("success-after", 10)
	next.Rates = []RateLimit{success}
	var rejection *Rejection
	if err := engine.Reserve(ctx, next); !errors.As(err, &rejection) || rejection.RateDecision != success.Decision {
		t.Fatalf("successful zero-cost settlement did not consume success capacity: %v", err)
	}
}

func TestSettlementJournalDedupePrecisionAndAtomicTelemetryIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	engine.TelemetryStream = Prefix + "test-telemetry"
	engine.TelemetryEncode = func(event Event) (string, error) {
		value, err := json.Marshal(event)
		return string(value), err
	}
	ctx := context.Background()
	input := testInput("journal-dedupe", 100)
	input.PricingVersion = 1791016000123456789
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, input, 80); err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, request(input.RequestID)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, input, 80); err != nil {
		t.Fatal(err)
	}
	if redisAvailable(t, client) != 920 || storage.settledCalls != 0 {
		t.Fatalf("journal recovery charged twice or performed SQL: balance=%d queries=%d", redisAvailable(t, client), storage.settledCalls)
	}
	for _, stream := range []string{Stream, engine.TelemetryStream} {
		rows, err := client.XRange(ctx, stream, "-", "+").Result()
		if err != nil || len(rows) != 1 {
			t.Fatalf("financial/telemetry publish not deduplicated: %s rows=%d %v", stream, len(rows), err)
		}
		field := "event"
		if stream == engine.TelemetryStream {
			field = "payload"
			if rows[0].Values["type"] != "financial_settlement" {
				t.Fatal("telemetry event type missing")
			}
		}
		var event Event
		if err := json.Unmarshal([]byte(rows[0].Values[field].(string)), &event); err != nil {
			t.Fatal(err)
		}
		if event.PricingVersion != input.PricingVersion {
			t.Fatalf("Lua changed pricing version precision: %d", event.PricingVersion)
		}
	}
	release := testInput("receipt-release", 100)
	if err := engine.Reserve(ctx, release); err != nil {
		t.Fatal(err)
	}
	if err := engine.Release(ctx, release); err != nil {
		t.Fatal(err)
	}
	if count, _ := client.XLen(ctx, Stream).Result(); count != 1 {
		t.Fatal("release wrote a financial debit")
	}
	if count, _ := client.XLen(ctx, engine.TelemetryStream).Result(); count != 2 {
		t.Fatal("release telemetry receipt was not published")
	}
}
func (s *memoryStorage) Apply(_ context.Context, events []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyCalls++
	for _, event := range events {
		if s.reject[event.OperationID] {
			return errors.New("injected failing financial event")
		}
	}
	for _, event := range events {
		id := event.Kind + ":" + event.OperationID
		if _, ok := s.applied[id]; ok {
			continue
		}
		s.applied[id] = event
		s.snapshot.Balance -= event.Amount
		if !event.Unlimited {
			s.snapshot.TokenRemaining[event.TokenID] -= event.Amount
		}
		for _, budget := range event.Budgets {
			s.snapshot.Budgets[budget.Key()] += event.Amount
		}
	}
	if s.failAfterCommit {
		s.failAfterCommit = false
		return errors.New("injected failure after committed SQL")
	}
	return nil
}
func (s *memoryStorage) PendingCredits(_ context.Context, _ int) ([]Credit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []Credit
	for _, credit := range s.credits {
		if !s.creditDone[credit.ID] {
			rows = append(rows, credit)
		}
	}
	return rows, nil
}
func (s *memoryStorage) CreditApplied(_ context.Context, id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creditDone[id] = true
	return nil
}

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("TEST_LEDGER_REDIS_URL")
	if url == "" {
		t.Skip("TEST_LEDGER_REDIS_URL must name an isolated test Redis database")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	clear := func() {
		var cursor uint64
		var collected []string
		for {
			keys, next, err := client.Scan(context.Background(), cursor, Prefix+"*", 500).Result()
			if err != nil {
				t.Error(err)
				return
			}
			collected = append(collected, keys...)
			cursor = next
			if cursor == 0 {
				break
			}
		}
		if len(collected) > 0 {
			if err := client.Del(context.Background(), collected...).Err(); err != nil {
				t.Error(err)
			}
		}
	}
	clear()
	if err := client.Set(context.Background(), ContinuityKey, "test-epoch:1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clear(); _ = client.Close() })
	return client
}

func testInput(id string, amount int64) Input {
	return Input{RequestID: id, PayerID: 1, ActorID: 1, ProjectID: 3, TokenID: 2, Amount: amount, OccurredAt: time.Now().UTC()}
}
func redisAvailable(t *testing.T, client *redis.Client) int64 {
	t.Helper()
	value, err := client.HGet(context.Background(), wallet(1), "avail").Int64()
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func redisTokenRemaining(t *testing.T, client *redis.Client) int64 {
	t.Helper()
	value, err := client.Get(context.Background(), token(2)).Int64()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// Production 2026-10-08: every "payer ledger rebuilding" failure since PR #380
// was the operator maintenance key held through a deploy quiesce, reported as
// a per-payer fence. The pause must be distinguishable, and a never-initialized
// payer must still be initialized inline and admitted once the pause ends.
func TestMaintenancePauseIsNotPayerRebuildAndColdPayerAdmitsAfterIntegration(t *testing.T) {
	client := testRedis(t)
	ctx := context.Background()
	engine := testEngine(client, newMemoryStorage(1000, 1000))
	if err := client.Set(ctx, Prefix+"maintenance", "deploy-quiesce", 0).Err(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err := engine.Reserve(ctx, testInput("during-pause", 10))
	if !errors.Is(err, ErrAdmissionPaused) || errors.Is(err, ErrRebuilding) {
		t.Fatalf("maintenance pause surfaced as %v; want ErrAdmissionPaused", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("maintenance pause held the request for %s", elapsed)
	}
	if exists, _ := client.Exists(ctx, wallet(1)).Result(); exists != 0 {
		t.Fatal("paused admission initialized a wallet")
	}
	if err := client.Del(ctx, Prefix+"maintenance").Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(ctx, testInput("first-after-pause", 10)); err != nil {
		t.Fatalf("cold payer was not initialized and admitted: %v", err)
	}
	if got := redisAvailable(t, client); got != 990 {
		t.Fatalf("cold payer avail=%d want 990", got)
	}
}

func TestConcurrentBudgetAndWalletAdmissionIntegration(t *testing.T) {
	for _, scenario := range []struct {
		name                      string
		balance, remaining, limit int64
		want                      int64
	}{{"wallet", 100, 1000, 1000, 10}, {"key", 1000, 100, 1000, 10}, {"budget", 1000, 1000, 100, 10}} {
		t.Run(scenario.name, func(t *testing.T) {
			client := testRedis(t)
			storage := newMemoryStorage(scenario.balance, scenario.remaining)
			engine := testEngine(client, storage)
			base := testInput("initialize", 0)
			budget := Budget{ID: 1, Start: time.Now().Unix() - 3600, End: time.Now().Unix() + 3600, Limit: scenario.limit, Block: true}
			base.Budgets = []Budget{budget}
			if err := engine.Initialize(context.Background(), base); err != nil {
				t.Fatal(err)
			}
			var accepted atomic.Int64
			var rejected atomic.Int64
			var invalid atomic.Int64
			var wg sync.WaitGroup
			const concurrentRequests = 200
			start := make(chan struct{})
			for n := 0; n < concurrentRequests; n++ {
				wg.Add(1)
				go func(n int) {
					defer wg.Done()
					<-start
					input := base
					input.RequestID = fmt.Sprintf("concurrent-%d", n)
					input.Amount = 10
					if err := engine.Reserve(context.Background(), input); err == nil {
						accepted.Add(1)
					} else {
						var rejection *Rejection
						if !errors.As(err, &rejection) {
							invalid.Add(1)
						} else {
							rejected.Add(1)
						}
					}
				}(n)
			}
			close(start)
			wg.Wait()
			if invalid.Load() != 0 || accepted.Load() != scenario.want || accepted.Load()+rejected.Load() != concurrentRequests {
				t.Fatalf("accepted=%d rejected=%d invalid=%d want=%d of %d", accepted.Load(), rejected.Load(), invalid.Load(), scenario.want, concurrentRequests)
			}
			totalReserved := accepted.Load() * 10
			if got := redisAvailable(t, client); got != scenario.balance-totalReserved {
				t.Fatalf("wallet=%d", got)
			}
			if got := redisTokenRemaining(t, client); got != scenario.remaining-totalReserved {
				t.Fatalf("key=%d", got)
			}
			reserved, err := client.HGet(context.Background(), budget.Key(), "reserved").Int64()
			if err != nil {
				t.Fatal(err)
			}
			if reserved != totalReserved {
				t.Fatalf("reserved=%d", reserved)
			}
			if spent, err := client.HGet(context.Background(), budget.Key(), "spent").Int64(); err != nil || spent != 0 {
				t.Fatalf("admission changed settled budget spend=%d err=%v", spent, err)
			}
		})
	}
}

func TestReserveReleaseAndLateSettlementIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	input := testInput("expired-media-job", 100)
	budget := Budget{ID: 1, Start: time.Now().Unix() - 3600, End: time.Now().Unix() + 3600, Limit: 1000, Block: true}
	input.Budgets = []Budget{budget}
	input.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	if err := engine.Reserve(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 900 {
		t.Fatalf("duplicate reserve wallet=%d", got)
	}
	if err := engine.Reap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 1000 {
		t.Fatalf("expiry didn't release: %d", got)
	}
	if err := engine.Release(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(context.Background(), input, 120); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(context.Background(), input, 120); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 880 {
		t.Fatalf("late/duplicate settlement wallet=%d", got)
	}
	if got := redisTokenRemaining(t, client); got != 880 {
		t.Fatalf("late/duplicate settlement key=%d", got)
	}
	if got, err := client.HGet(context.Background(), budget.Key(), "reserved").Int64(); err != nil || got != 0 {
		t.Fatalf("budget reserved=%d err=%v", got, err)
	}
	if got, err := client.HGet(context.Background(), budget.Key(), "spent").Int64(); err != nil || got != 120 {
		t.Fatalf("budget spent=%d err=%v", got, err)
	}
	if n, err := engine.Drain(context.Background(), "late-settle-writer"); err != nil || n != 1 {
		t.Fatalf("drain=%d %v", n, err)
	}
	if len(storage.applied) != 1 || storage.snapshot.Balance != 880 {
		t.Fatalf("permanent settlement mismatch: %+v", storage.snapshot)
	}
	// Evict request state after durable delivery. Permanent operation identity
	// must prevent a new debit even after the Redis dedupe retention is gone.
	if err := client.Del(context.Background(), request(input.RequestID)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(context.Background(), input, 120); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 880 {
		t.Fatalf("durable dedupe wallet=%d", got)
	}
}

func TestCommittedBeforeACKCrashReplayIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	input := testInput("ack-crash", 100)
	if err := engine.Reserve(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(context.Background(), input, 80); err != nil {
		t.Fatal(err)
	}
	if err := client.XGroupCreateMkStream(context.Background(), Stream, Group, "0").Err(); err != nil {
		t.Fatal(err)
	}
	read, err := client.XReadGroup(context.Background(), &redis.XReadGroupArgs{Group: Group, Consumer: "crashed-writer", Streams: []string{Stream, ">"}, Count: 1, Block: time.Millisecond}).Result()
	if err != nil || len(read) != 1 || len(read[0].Messages) != 1 {
		t.Fatalf("crash fixture read=%+v %v", read, err)
	}
	var committed Event
	if err := json.Unmarshal([]byte(read[0].Messages[0].Values["event"].(string)), &committed); err != nil {
		t.Fatal(err)
	}
	committed.StreamID = read[0].Messages[0].ID
	if err := storage.Apply(context.Background(), []Event{committed}); err != nil {
		t.Fatal(err)
	}
	// Inject the crash boundary here: SQL has committed, XACK has not run.
	pending, err := client.XPendingExt(context.Background(), &redis.XPendingExtArgs{Stream: Stream, Group: Group, Start: "-", End: "+", Count: 10}).Result()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%+v %v", pending, err)
	}
	// Set idle age directly so the recovery test does not wait five seconds.
	if err := client.Do(context.Background(), "XCLAIM", Stream, Group, "abandoned", 0, pending[0].ID, "IDLE", 60_000).Err(); err != nil {
		t.Fatal(err)
	}
	if n, err := engine.Drain(context.Background(), "recovered-writer"); err != nil || n != 1 {
		t.Fatalf("replay=%d %v", n, err)
	}
	if len(storage.applied) != 1 || storage.applyCalls != 2 || storage.snapshot.Balance != 920 {
		t.Fatalf("permanent duplicate charge: %+v calls=%d", storage.snapshot, storage.applyCalls)
	}
	if got := redisAvailable(t, client); got != 920 {
		t.Fatalf("replay refunded or debited wallet=%d", got)
	}
	if exposure, err := client.Get(context.Background(), Prefix+"exposure").Int64(); err != nil || exposure != 0 {
		t.Fatalf("exposure=%d %v", exposure, err)
	}
}

func TestCreditSequenceAndConcurrentRebuildIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1100, 1000)
	storage.snapshot.CreditSeq = 1
	engine := testEngine(client, storage)
	input := testInput("rebuild", 0)
	storage.snapshotStarted = make(chan struct{}, 1)
	storage.snapshotResume = make(chan struct{})
	finished := make(chan error, 1)
	go func() { finished <- engine.Initialize(context.Background(), input) }()
	<-storage.snapshotStarted
	credit := Credit{ID: 1, PayerID: 1, Sequence: 1, Amount: 100}
	if err := engine.Credit(context.Background(), credit); !errors.Is(err, ErrRebuilding) {
		t.Fatalf("credit crossed rebuild fence: %v", err)
	}
	if err := engine.Reserve(context.Background(), testInput("during-rebuild", 10)); !errors.Is(err, ErrRebuilding) {
		t.Fatalf("admission crossed rebuild fence: %v", err)
	}
	close(storage.snapshotResume)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := engine.Credit(context.Background(), credit); err != nil {
		t.Fatal(err)
	}
	if err := engine.Credit(context.Background(), credit); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 1100 {
		t.Fatalf("credit included in snapshot credited again: %d", got)
	}
	if err := engine.Credit(context.Background(), Credit{ID: 3, PayerID: 1, Sequence: 3, Amount: 10}); !errors.Is(err, ErrRebuilding) {
		t.Fatalf("out-of-order credit accepted: %v", err)
	}
	if err := engine.Credit(context.Background(), Credit{ID: 2, PayerID: 1, Sequence: 2, Amount: 10}); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 1110 {
		t.Fatalf("ordered credit wallet=%d", got)
	}
	// Cold initialization can never replace the existing wallet with an older
	// snapshot after a credit has already been applied.
	storage.snapshotResume = nil
	storage.snapshotStarted = nil
	if err := engine.Initialize(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 1110 {
		t.Fatalf("stale snapshot overwrote credit: %d", got)
	}
}

func TestSettlementSpoolDurableIdentityAndReplayIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	engine.SpoolDir = t.TempDir()
	input := testInput("spool-replay", 100)
	if err := engine.Reserve(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := engine.PersistSettlement(input, 80, "settle"); err != nil {
		t.Fatal(err)
	}
	if err := engine.PersistSettlement(input, 80, "settle"); err != nil {
		t.Fatalf("identical spool retry: %v", err)
	}
	if err := engine.PersistSettlement(input, 90, "settle"); err == nil {
		t.Fatal("conflicting settlement overwrote durable spool")
	}
	entries, err := os.ReadDir(engine.SpoolDir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry.Name())
		}
	}
	if len(files) != 1 {
		t.Fatalf("spool entries=%v", files)
	}
	path := filepath.Join(engine.SpoolDir, files[0])
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("spool permissions=%o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Event
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Amount != 80 || persisted.OperationID != input.RequestID {
		t.Fatalf("wrong durable settlement: %+v", persisted)
	}
	if err := engine.ReplaySpool(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("acknowledged spool not removed: %v", err)
	}
	if got := redisAvailable(t, client); got != 920 {
		t.Fatalf("spool settlement wallet=%d", got)
	}
	if err := engine.ReplaySpool(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 920 {
		t.Fatalf("second replay wallet=%d", got)
	}
}

func TestTerminalReserveAndZeroSettlementPermanentDedupeIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	input := testInput("zero-cost", 100)
	if err := engine.Reserve(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(context.Background(), input, 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(context.Background(), input); err == nil {
		t.Fatal("terminal request was admitted a second time")
	}
	if n, err := engine.Drain(context.Background(), "zero-writer"); err != nil || n != 1 {
		t.Fatalf("zero settlement drain=%d %v", n, err)
	}
	if len(storage.applied) != 1 {
		t.Fatalf("zero settlement not permanently deduplicated: %v", storage.applied)
	}
	before, err := client.XLen(context.Background(), Stream).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(context.Background(), request(input.RequestID)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(context.Background(), input, 0); err != nil {
		t.Fatal(err)
	}
	after, err := client.XLen(context.Background(), Stream).Result()
	if err != nil {
		t.Fatal(err)
	}
	if after != before || redisAvailable(t, client) != 1000 {
		t.Fatal("unknown-state zero settlement was re-published")
	}
	// A released request also cannot be admitted as new work with its old ID.
	released := testInput("released-terminal", 100)
	if err := engine.Reserve(context.Background(), released); err != nil {
		t.Fatal(err)
	}
	if err := engine.Release(context.Background(), released); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(context.Background(), released); err == nil {
		t.Fatal("released request was admitted a second time")
	}
}

func TestRefundReplayedOnceAndOriginalBudgetPeriodIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	input := testInput("budget-rollover", 100)
	oldBudget := Budget{ID: 1, Start: time.Now().Unix() - 3600, End: time.Now().Unix() + 3600, Limit: 1000, Block: true}
	input.Budgets = []Budget{oldBudget}
	if err := engine.Reserve(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	late := input
	late.Budgets = []Budget{{ID: 1, Start: oldBudget.Start + 7200, End: oldBudget.End + 7200, Limit: 1000, Block: true}}
	if err := engine.Settle(context.Background(), late, 80); err != nil {
		t.Fatal(err)
	}
	if spent, err := client.HGet(context.Background(), oldBudget.Key(), "spent").Int64(); err != nil || spent != 80 {
		t.Fatalf("settlement changed budget period: %d %v", spent, err)
	}
	if exists, err := client.Exists(context.Background(), late.Budgets[0].Key()).Result(); err != nil || exists != 0 {
		t.Fatalf("late budget created: %d %v", exists, err)
	}
	if err := engine.Refund(context.Background(), input, 20, "refund-one"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refund(context.Background(), input, 20, "refund-one"); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 940 {
		t.Fatalf("duplicate refund balance=%d", got)
	}
	if exposure, err := client.Get(context.Background(), Prefix+"exposure").Int64(); err != nil || exposure != 100 {
		t.Fatalf("refund must add absolute uncommitted exposure: %d %v", exposure, err)
	}
	if n, err := engine.Drain(context.Background(), "refund-writer"); err != nil || n != 2 {
		t.Fatalf("refund drain=%d %v", n, err)
	}
	if len(storage.applied) != 2 || storage.snapshot.Balance != 940 {
		t.Fatalf("refund permanent balance=%d rows=%d", storage.snapshot.Balance, len(storage.applied))
	}
}

func TestGenerationAndPendingAdmissionFailClosedIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	ctx := context.Background()
	key, pending := "gen:ledger-test:scope", "pending:ledger-test:scope"
	t.Cleanup(func() { _ = client.Del(ctx, key, pending).Err() })
	if err := client.Del(ctx, key, pending).Err(); err != nil {
		t.Fatal(err)
	}
	input := testInput("gen-fail-closed", 10)
	input.Generations = []Generation{{Key: key, PendingKey: pending, Value: "1"}}
	if err := engine.Reserve(ctx, input); !errors.Is(err, ErrStale) {
		t.Fatalf("missing generation admitted: %v", err)
	}
	if err := client.Set(ctx, key, "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(ctx, pending, &redis.Z{Score: float64(time.Now().Add(time.Minute).UnixMilli()), Member: "write-a"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(ctx, input); !errors.Is(err, ErrRebuilding) {
		t.Fatalf("pending generation admitted: %v", err)
	}
	if err := client.Del(ctx, pending).Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	closed := redis.NewClient(client.Options())
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	engine.Redis = closed
	input.RequestID = "redis-down"
	if err := engine.Reserve(ctx, input); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable Redis admitted: %v", err)
	}
}

func TestDeadLetterContinuesAndRecoveryDeduplicatesIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	current := time.Now()
	engine.Now = func() time.Time { return current }
	storage.reject = map[string]bool{"failing-event": true}
	for _, item := range []struct {
		id     string
		actual int64
	}{{"failing-event", 80}, {"successful-event", 50}} {
		input := testInput(item.id, 100)
		if err := engine.Reserve(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		if err := engine.Settle(context.Background(), input, item.actual); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := engine.Drain(context.Background(), "partial-writer"); err != nil || n != 1 {
		t.Fatalf("failed event prevented successful later event: n=%d %v", n, err)
	}
	if len(storage.applied) != 1 || storage.snapshot.Balance != 950 {
		t.Fatalf("successful event not committed: %+v", storage.applied)
	}
	if exposure, err := client.Get(context.Background(), Prefix+"exposure").Int64(); err != nil || exposure != 80 {
		t.Fatalf("dead event lost exposure: %d %v", exposure, err)
	}
	if count, err := client.XLen(context.Background(), DeadStream).Result(); err != nil || count != 1 {
		t.Fatalf("dead event not durable: %d %v", count, err)
	}
	if err := engine.RetryDead(context.Background(), "still-failing"); err == nil {
		t.Fatal("persistent dead event failure hidden")
	}
	delete(storage.reject, "failing-event")
	pending, err := client.XPendingExt(context.Background(), &redis.XPendingExtArgs{Stream: DeadStream, Group: DeadGroup, Start: "-", End: "+", Count: 10}).Result()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending dead event=%+v %v", pending, err)
	}
	if err := client.Do(context.Background(), "XCLAIM", DeadStream, DeadGroup, "abandoned", 0, pending[0].ID, "IDLE", 60_000).Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.RetryDead(context.Background(), "recovered-dead"); err != nil {
		t.Fatal(err)
	}
	if len(storage.applied) != 2 || storage.snapshot.Balance != 870 || redisAvailable(t, client) != 870 {
		t.Fatalf("dead replay charged/refunded twice: %+v", storage.snapshot)
	}
	if exposure, err := client.Get(context.Background(), Prefix+"exposure").Int64(); err != nil || exposure != 0 {
		t.Fatalf("replayed dead exposure=%d %v", exposure, err)
	}
	if err := engine.RetryDead(context.Background(), "second-replay"); err != nil {
		t.Fatal(err)
	}
	if len(storage.applied) != 2 || storage.snapshot.Balance != 870 {
		t.Fatal("dead replay not idempotent")
	}
	current = current.Add(11 * time.Minute)
	if err := engine.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, stream := range []string{Stream, DeadStream} {
		if count, err := client.XLen(context.Background(), stream).Result(); err != nil || count != 0 {
			t.Fatalf("completed dead replay leaked %s entry: %d %v", stream, count, err)
		}
	}
}

func TestJournalCollectionRetainsUncommittedEventsIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	current := time.Now()
	engine.Now = func() time.Time { return current }
	committed := testInput("collect-committed", 100)
	if err := engine.Reserve(context.Background(), committed); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(context.Background(), committed, 80); err != nil {
		t.Fatal(err)
	}
	if n, err := engine.Drain(context.Background(), "gc-writer"); err != nil || n != 1 {
		t.Fatalf("drain=%d %v", n, err)
	}
	uncommitted := testInput("collect-uncommitted", 100)
	if err := engine.Reserve(context.Background(), uncommitted); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(context.Background(), uncommitted, 50); err != nil {
		t.Fatal(err)
	}
	current = current.Add(9 * time.Minute)
	if err := engine.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count, err := client.HLen(context.Background(), Prefix+"journal").Result(); err != nil || count != 2 {
		t.Fatalf("journal collected before delay: %d %v", count, err)
	}
	current = current.Add(2 * time.Minute)
	if err := engine.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count, err := client.HLen(context.Background(), Prefix+"journal").Result(); err != nil || count != 1 {
		t.Fatalf("only acked journal should collect: %d %v", count, err)
	}
	if exists, err := client.HExists(context.Background(), Prefix+"journal", uncommitted.RequestID).Result(); err != nil || !exists {
		t.Fatalf("uncommitted journal lost: %v %v", exists, err)
	}
	if count, err := client.XLen(context.Background(), Stream).Result(); err != nil || count != 1 {
		t.Fatalf("uncommitted stream lost: %d %v", count, err)
	}
	if exposure, err := client.Get(context.Background(), Prefix+"exposure").Int64(); err != nil || exposure != 50 {
		t.Fatalf("GC changed pending financial exposure: %d %v", exposure, err)
	}
}

func TestBudgetPeriodDefinitionChangeDoesNotCollideIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	start := time.Now().Unix()
	daily := Budget{ID: 1, Start: start, End: start + 86400, Limit: 100, Block: true}
	monthly := Budget{ID: 1, Start: start, End: start + 30*86400, Limit: 100, Block: true}
	if daily.Key() == monthly.Key() {
		t.Fatal("day/month definitions sharing period start collide")
	}
	dayInput := testInput("before-period-change", 80)
	dayInput.Budgets = []Budget{daily}
	monthInput := testInput("after-period-change", 80)
	monthInput.Budgets = []Budget{monthly}
	if err := engine.Reserve(context.Background(), dayInput); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(context.Background(), monthInput); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(context.Background(), dayInput, 80); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(context.Background(), monthInput, 80); err != nil {
		t.Fatal(err)
	}
	for _, budget := range []Budget{daily, monthly} {
		if got, err := client.HGet(context.Background(), budget.Key(), "spent").Int64(); err != nil || got != 80 {
			t.Fatalf("budget period merged: %d %v", got, err)
		}
	}
}

func testEngine(client *redis.Client, storage Storage) *Engine {
	e := New(client, storage)
	e.Continuity = "test-epoch:1"
	return e
}
