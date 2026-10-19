package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/ledger"
)

type spoolAdmissionStorage struct {
	mu      sync.Mutex
	applied map[string]ledger.Event
	poison  string
}

func (s *spoolAdmissionStorage) Snapshot(context.Context, ledger.Input) (ledger.Snapshot, error) {
	return ledger.Snapshot{Balance: 1000}, nil
}

func (s *spoolAdmissionStorage) Settled(_ context.Context, id, kind string) (bool, error) {
	if id == s.poison {
		return false, fmt.Errorf("%w: retained unsupported financial intent", ledger.ErrPermanentOperation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, found := s.applied[kind+":"+id]
	return found, nil
}

func (s *spoolAdmissionStorage) Apply(_ context.Context, events []ledger.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range events {
		s.applied[event.Kind+":"+event.OperationID] = event
	}
	return nil
}

func (*spoolAdmissionStorage) PendingCredits(context.Context, int) ([]ledger.Credit, error) {
	return nil, nil
}

func (*spoolAdmissionStorage) CreditApplied(context.Context, uint) error { return nil }

func spoolAdmissionFixture(t *testing.T) (*ledger.Engine, *spoolAdmissionStorage) {
	t.Helper()
	endpoint := os.Getenv("TEST_LEDGER_ADAPTER_REDIS_URL")
	if endpoint == "" {
		t.Skip("isolated TEST_LEDGER_ADAPTER_REDIS_URL required")
	}
	options, err := redis.ParseURL(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	clear := func() {
		keys, err := client.Keys(ctx, ledger.Prefix+"*").Result()
		if err != nil {
			t.Error(err)
			return
		}
		if len(keys) > 0 {
			if err := client.Del(ctx, keys...).Err(); err != nil {
				t.Error(err)
			}
		}
	}
	clear()
	t.Cleanup(func() { clear(); _ = client.Close() })
	continuity := "spool-admission-fixture:1"
	if err := client.Set(ctx, ledger.ContinuityKey, continuity, 0).Err(); err != nil {
		t.Fatal(err)
	}
	storage := &spoolAdmissionStorage{applied: map[string]ledger.Event{}}
	engine := ledger.New(client, storage)
	engine.Continuity = continuity
	engine.SpoolDir = t.TempDir()
	return engine, storage
}

func spoolAdmissionInput(id string) ledger.Input {
	return ledger.Input{RequestID: id, PayerID: 1, ActorID: 1, Unlimited: true, Amount: 10, OccurredAt: time.Now().UTC()}
}

func writeSpoolAdmissionEvent(t *testing.T, engine *ledger.Engine, name, id string, amount int64) {
	t.Helper()
	event := ledger.Event{Input: spoolAdmissionInput(id), OperationID: id, Kind: "settle"}
	event.Amount = amount
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engine.SpoolDir, name+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPR380UnknownSpoolExposureBlocksAdmissionAfterInventory(t *testing.T) {
	engine, _ := spoolAdmissionFixture(t)
	quarantine := filepath.Join(engine.SpoolDir, "quarantine")
	if err := os.Mkdir(quarantine, 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"broken"`)
	path := filepath.Join(quarantine, "unknown.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	check, _ := bindLedgerSpoolAdmission(engine)
	failures := 0
	engine.SpoolFailure = func(error) { failures++ }
	check() // Startup and each one-second inventory use this same gate.
	if err := engine.Reserve(context.Background(), spoolAdmissionInput("unknown-inventory")); !errors.Is(err, ledger.ErrUnavailable) {
		t.Fatalf("unknown financial exposure admitted new paid request: %v", err)
	}
	if failures != 0 {
		t.Fatal("unknown logical evidence invoked disk-failure latch")
	}
	if retained, err := os.ReadFile(path); err != nil || string(retained) != string(raw) {
		t.Fatalf("admission check removed unknown evidence: %v", err)
	}
}

func TestPR380UnknownQuarantineFencesAdmissionButDeliveryContinues(t *testing.T) {
	engine, storage := spoolAdmissionFixture(t)
	check, quarantined := bindLedgerSpoolAdmission(engine)
	check()
	failures := 0
	engine.SpoolFailure = func(error) { failures++ }
	engine.SpoolQuarantine = quarantined
	ctx := context.Background()
	accepted := spoolAdmissionInput("accepted-before-unknown")
	if err := engine.Reserve(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engine.SpoolDir, "000-unknown.json"), []byte(`{"broken"`), 0600); err != nil {
		t.Fatal(err)
	}
	writeSpoolAdmissionEvent(t, engine, "999-valid", "valid-tail", 20)
	if err := engine.ReplaySpool(ctx); err != nil {
		t.Fatalf("unknown quarantine starved valid financial replay: %v", err)
	}
	// No periodic inventory has run: the durable quarantine callback must fence
	// immediately, while settlement of already accepted work stays available.
	if err := engine.Reserve(ctx, spoolAdmissionInput("after-unknown-callback")); !errors.Is(err, ledger.ErrUnavailable) {
		t.Errorf("unknown quarantine did not immediately fence admission: %v", err)
	}
	if err := engine.Settle(ctx, accepted, 25); err != nil {
		t.Fatalf("paused admission blocked accepted settlement: %v", err)
	}
	if count, err := engine.Drain(ctx, "spool-admission-writer"); err != nil || count != 2 {
		t.Fatalf("paused admission blocked durable delivery: count=%d err=%v", count, err)
	}
	storage.mu.Lock()
	acceptedAmount := storage.applied["settle:"+accepted.RequestID].Amount
	tailAmount := storage.applied["settle:valid-tail"].Amount
	storage.mu.Unlock()
	if acceptedAmount != 25 || tailAmount != 20 || failures != 0 {
		t.Fatalf("delivery lost facts or misclassified I/O: accepted=%d tail=%d diskFailures=%d", acceptedAmount, tailAmount, failures)
	}
	check()
	if err := engine.Reserve(ctx, spoolAdmissionInput("after-unknown-inventory")); !errors.Is(err, ledger.ErrUnavailable) {
		t.Fatalf("inventory cleared unresolved unknown fence: %v", err)
	}
	// This is a fixture's explicit operator resolution, never runtime eviction.
	path := filepath.Join(engine.SpoolDir, "quarantine", "000-unknown.json")
	for _, file := range []string{path, path + ".reason"} {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	}
	check()
	if err := engine.Reserve(ctx, spoolAdmissionInput("after-explicit-resolution")); err != nil {
		t.Fatalf("logical unknown evidence left a permanent durability latch: %v", err)
	}
}

func TestPR380KnownQuarantineUsesMonetaryAdmissionThreshold(t *testing.T) {
	for _, amount := range []int64{73, 100_000_000} {
		t.Run(fmt.Sprint(amount), func(t *testing.T) {
			engine, storage := spoolAdmissionFixture(t)
			check, quarantined := bindLedgerSpoolAdmission(engine)
			engine.SpoolQuarantine = quarantined
			check()
			storage.poison = "known-quarantine"
			writeSpoolAdmissionEvent(t, engine, "000-known", storage.poison, amount)
			if err := engine.ReplaySpool(context.Background()); err != nil {
				t.Fatal(err)
			}
			check()
			err := engine.Reserve(context.Background(), spoolAdmissionInput("after-known-quarantine"))
			if amount == 73 && err != nil {
				t.Fatalf("known amount below cap unnecessarily paused admission: %v", err)
			}
			if amount == 100_000_000 {
				var rejection *ledger.Rejection
				if !errors.As(err, &rejection) || rejection.Code != "backlog" {
					t.Fatalf("known quarantine at cap escaped monetary admission: %v", err)
				}
			}
			usage, usageErr := engine.SpoolUsage()
			if usageErr != nil || usage.UnresolvedExposure != amount || usage.UnknownExposureFiles != 0 {
				t.Fatalf("quarantine financial exposure changed: %+v %v", usage, usageErr)
			}
		})
	}
}
