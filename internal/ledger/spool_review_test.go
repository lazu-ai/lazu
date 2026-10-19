package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestPR380SpoolConflictDoesNotSignalDiskFailure(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = t.TempDir()
	failures := 0
	engine.SpoolFailure = func(error) { failures++ }
	input := testInput("review-spool-conflict", 100)
	if err := engine.PersistSettlement(input, 70, "settle"); err != nil {
		t.Fatal(err)
	}
	_, original, _ := readOnlySpoolEvent(t, engine)
	if err := engine.PersistSettlement(input, 71, "settle"); !errors.Is(err, ErrSpoolConflict) {
		t.Fatal("conflicting amount accepted")
	}
	_, retained, _ := readOnlySpoolEvent(t, engine)
	if failures != 0 || !bytes.Equal(retained, original) {
		t.Fatalf("logical conflict triggered global disk failure or erased evidence: failures=%d", failures)
	}
}

func TestPR380PermanentReplayClassificationRequiresTypedProof(t *testing.T) {
	for _, code := range []string{"identity_conflict", "terminal", "money_range"} {
		if !permanentReplayError(fmt.Errorf("wrapped: %w", &Rejection{Code: code})) {
			t.Fatalf("typed permanent rejection %s remained ordered poison", code)
		}
	}
	for _, err := range []error{ErrUnavailable, ErrContinuity, ErrStale, ErrRebuilding, errors.New("money_range"), &Rejection{Code: "unknown"}, &Rejection{Code: "cold"}} {
		if permanentReplayError(err) {
			t.Fatalf("availability/unproven error was isolated: %v", err)
		}
	}
	if !permanentReplayError(fmt.Errorf("malformed finance: %w", ErrPermanentOperation)) {
		t.Fatal("storage's typed invalid financial event lost its permanent classification")
	}
}

func TestPR380CorruptSpoolDoesNotStarveRecoverableTail(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = t.TempDir()
	bad := filepath.Join(engine.SpoolDir, "000-invalid.json")
	payload := []byte(`{"broken"`)
	if err := os.WriteFile(bad, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.PersistSettlement(testInput("review-recoverable-tail", 100), 70, "settle"); err != nil {
		t.Fatal(err)
	}
	// No Redis means the recoverable tail must stay in place. Seeing its
	// transient failure proves the permanent poison did not stop iteration.
	if err := engine.ReplaySpool(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("poison blocked recoverable tail: %v", err)
	}
	if _, err := os.Stat(bad); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("poison remained in active queue: %v", err)
	}
	retained, err := os.ReadFile(filepath.Join(engine.SpoolDir, "quarantine", "000-invalid.json"))
	if err != nil || !bytes.Equal(retained, payload) {
		t.Fatalf("quarantine lost immutable evidence: %q %v", retained, err)
	}
}

func TestPR380SpoolUsageIncludesUnresolvedQuarantine(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = t.TempDir()
	if err := engine.PersistSettlement(testInput("review-usage", 100), 70, "settle"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(engine.SpoolDir, "quarantine"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bad-a.json", "bad-b.json"} {
		if err := os.WriteFile(filepath.Join(engine.SpoolDir, "quarantine", name), []byte("unresolved financial evidence"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := engine.SpoolUsage()
	if err != nil || usage.Files != 3 || usage.UnresolvedExposure != 70 || usage.UnknownExposureFiles != 2 || usage.QuarantinedFiles != 2 {
		t.Fatalf("unresolved quarantines vanished from admission usage: %+v %v", usage, err)
	}
}

type reviewReplayStorage struct {
	*memoryStorage
	settledErrors map[string]error
	lookedUp      []string
}

func (s *reviewReplayStorage) Settled(ctx context.Context, id, kind string) (bool, error) {
	s.lookedUp = append(s.lookedUp, id)
	if err := s.settledErrors[id]; err != nil {
		return false, err
	}
	return s.memoryStorage.Settled(ctx, id, kind)
}

func writeReviewSpool(t *testing.T, engine *Engine, name string, input Input, amount int64) (string, []byte) {
	return writeReviewFinancialSpool(t, engine, name, input, amount, "settle")
}

func writeReviewFinancialSpool(t *testing.T, engine *Engine, name string, input Input, amount int64, kind string) (string, []byte) {
	t.Helper()
	event := Event{Input: normalize(input), OperationID: input.RequestID, Kind: kind}
	event.Amount = amount
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(engine.SpoolDir, name+".json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path, raw
}

func TestPR380MoneyRangeSpoolQuarantinesAndContinuesIntegration(t *testing.T) {
	for _, subject := range []string{"wallet_refund", "budget_spend"} {
		t.Run(subject, func(t *testing.T) {
			client := testRedis(t)
			storage := newMemoryStorage(1000, 1000)
			engine := testEngine(client, storage)
			engine.SpoolDir = t.TempDir()
			input := testInput("review-money-range", 100)
			amount, kind := int64(10), "settle"
			if subject == "wallet_refund" {
				storage.snapshot.Balance = math.MaxInt64 - 5
				amount, kind = -10, "refund"
			} else {
				input.Budgets = []Budget{{ID: 99, ProjectID: input.ProjectID, Scope: "org", Start: input.OccurredAt.Unix() - 3600, End: input.OccurredAt.Unix() + 3600, Limit: 1000}}
				storage.snapshot.Budgets[input.Budgets[0].Key()] = math.MaxInt64 - 5
			}
			path, raw := writeReviewFinancialSpool(t, engine, "000", input, amount, kind)
			tail := testInput("review-after-money-range", 100)
			tail.ProjectID = 4 // A different scope has no overflowing project cap.
			writeReviewSpool(t, engine, "999", tail, 20)
			var records []QuarantinedOperation
			engine.SpoolQuarantine = func(record QuarantinedOperation) { records = append(records, record) }
			failures := 0
			engine.SpoolFailure = func(error) { failures++ }
			ctx := context.Background()
			if err := engine.ReplaySpool(ctx); err != nil {
				t.Fatalf("safe single amount produced an aggregate money_range poison: %v", err)
			}
			if len(records) != 1 || records[0].Reason != "money_range" || records[0].Amount != amount || !records[0].AmountKnown || failures != 0 {
				t.Fatalf("money-range not durably classified: %+v diskFailures=%d", records, failures)
			}
			if retained, err := os.ReadFile(records[0].File); err != nil || !bytes.Equal(retained, raw) {
				t.Fatalf("money-range facts erased: %v", err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("money-range stayed in active queue: %v", err)
			}
			if wallet := redisAvailable(t, client); wallet != storage.snapshot.Balance-20 {
				t.Fatalf("failed preflight partially changed wallet or starved tail: %d", wallet)
			}
			if subject == "budget_spend" {
				if spent, err := client.HGet(ctx, input.Budgets[0].Key(), "spent").Int64(); err != nil || spent != math.MaxInt64-5 {
					t.Fatalf("failed preflight mutated protected budget: %d %v", spent, err)
				}
			}
			if usage, err := engine.SpoolUsage(); err != nil || usage.Files != 1 || usage.QuarantinedFiles != 1 || usage.UnresolvedExposure != 10 || usage.UnknownExposureFiles != 0 {
				t.Fatalf("money-range lost unresolved exposure: %+v %v", usage, err)
			}
			if n, err := engine.Drain(ctx, "review-money-range-writer"); err != nil || n != 1 || len(storage.applied) != 1 {
				t.Fatalf("valid tail did not commit once: n=%d err=%v", n, err)
			}
		})
	}
}

func TestPR380PermanentSpoolFailureQuarantinesAndContinuesIntegration(t *testing.T) {
	client := testRedis(t)
	storage := &reviewReplayStorage{memoryStorage: newMemoryStorage(1000, 1000), settledErrors: map[string]error{"review-permanent": fmt.Errorf("%w: deleted payer", ErrPermanentOperation)}}
	engine := testEngine(client, storage)
	engine.SpoolDir = t.TempDir()
	ctx := context.Background()
	first, original := writeReviewSpool(t, engine, "000", testInput("review-permanent", 100), 73)
	writeReviewSpool(t, engine, "999", testInput("review-valid-tail", 100), 25)
	var notified []QuarantinedOperation
	engine.SpoolQuarantine = func(record QuarantinedOperation) {
		if raw, err := os.ReadFile(record.File); err != nil || !bytes.Equal(raw, original) {
			t.Fatalf("notification before immutable payload durability: %v", err)
		}
		if _, err := os.Stat(record.File + ".reason"); err != nil {
			t.Fatalf("notification before durable reason: %v", err)
		}
		notified = append(notified, record)
	}
	failures := 0
	engine.SpoolFailure = func(error) { failures++ }
	if err := engine.ReplaySpool(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("permanent poison remained active: %v", err)
	}
	if len(notified) != 1 || !notified[0].AmountKnown || notified[0].Amount != 73 || failures != 0 {
		t.Fatalf("isolation lost amount or latched unrelated admission: records=%+v failures=%d", notified, failures)
	}
	if got := redisAvailable(t, client); got != 975 {
		t.Fatalf("permanent poison starved or debited tail: wallet=%d", got)
	}
	if exposure, err := client.Get(ctx, Prefix+"exposure").Int64(); err != nil || exposure != 25 {
		t.Fatalf("quarantine ACKed/reduced unresolved Redis money: %d %v", exposure, err)
	}
	if usage, err := engine.SpoolUsage(); err != nil || usage.Files != 1 || usage.UnresolvedExposure != 73 || usage.UnknownExposureFiles != 0 {
		t.Fatalf("quarantine money vanished from unresolved disk exposure: %+v %v", usage, err)
	}
	if n, err := engine.Drain(ctx, "review-permanent-writer"); err != nil || n != 1 || len(storage.applied) != 1 {
		t.Fatalf("valid tail did not commit once: n=%d err=%v rows=%d", n, err, len(storage.applied))
	}
}

func TestPR380TransientSpoolFailurePreservesOrderIntegration(t *testing.T) {
	client := testRedis(t)
	transient := errors.New("fixture SQL transport unavailable")
	storage := &reviewReplayStorage{memoryStorage: newMemoryStorage(1000, 1000), settledErrors: map[string]error{"review-transient": transient}}
	engine := testEngine(client, storage)
	engine.SpoolDir = t.TempDir()
	ctx := context.Background()
	first, original := writeReviewSpool(t, engine, "000", testInput("review-transient", 100), 30)
	tail, tailOriginal := writeReviewSpool(t, engine, "999", testInput("review-ordered-tail", 100), 20)
	if err := engine.ReplaySpool(ctx); !errors.Is(err, transient) {
		t.Fatalf("transient error misclassified: %v", err)
	}
	for path, expected := range map[string][]byte{first: original, tail: tailOriginal} {
		if raw, err := os.ReadFile(path); err != nil || !bytes.Equal(raw, expected) {
			t.Fatalf("transient replay changed ordered financial evidence: %s %v", path, err)
		}
	}
	if len(storage.lookedUp) != 1 || storage.lookedUp[0] != "review-transient" {
		t.Fatalf("transient failure processed later records out of order: %+v", storage.lookedUp)
	}
	if usage, err := engine.SpoolUsage(); err != nil || usage.Files != 2 || usage.QuarantinedFiles != 0 || usage.UnresolvedExposure != 50 {
		t.Fatalf("transient evidence disappeared: %+v %v", usage, err)
	}
	delete(storage.settledErrors, "review-transient")
	if err := engine.ReplaySpool(ctx); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 950 {
		t.Fatalf("retry money lost or duplicated: %d", got)
	}
	if n, err := engine.Drain(ctx, "review-ordered-writer"); err != nil || n != 2 || len(storage.applied) != 2 {
		t.Fatalf("ordered retries failed once-only commit: n=%d err=%v", n, err)
	}
}

func TestPR380IdentityConflictQuarantineRetainsOriginalExposureIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	engine.SpoolDir = t.TempDir()
	ctx := context.Background()
	original := testInput("review-conflicted-original", 100)
	if err := engine.Reserve(ctx, original); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, original, 80); err != nil {
		t.Fatal(err)
	}
	conflict := original
	conflict.PayerID++
	writeReviewSpool(t, engine, "000", conflict, 73)
	writeReviewSpool(t, engine, "999", testInput("review-after-identity-conflict", 100), 25)
	if err := engine.ReplaySpool(ctx); err != nil {
		t.Fatal(err)
	}
	if exposure, err := client.Get(ctx, Prefix+"exposure").Int64(); err != nil || exposure != 105 {
		t.Fatalf("isolation ACKed original unresolved money: %d %v", exposure, err)
	}
	if held, err := client.ZScore(ctx, Prefix+"uncommitted", original.RequestID).Result(); err != nil || held <= 0 {
		t.Fatalf("isolation forgot original unresolved identity: %g %v", held, err)
	}
	if got := redisAvailable(t, client); got != 895 {
		t.Fatalf("identity-conflicting replay changed money: %d", got)
	}
	if usage, err := engine.SpoolUsage(); err != nil || usage.Files != 1 || usage.QuarantinedFiles != 1 || usage.UnresolvedExposure != 73 {
		t.Fatalf("conflicting facts lost isolation/exposure: %+v %v", usage, err)
	}
	if n, err := engine.Drain(ctx, "review-conflict-writer"); err != nil || n != 2 || len(storage.applied) != 2 || storage.snapshot.Balance != 895 {
		t.Fatalf("original/tail permanent facts not exactly once: n=%d err=%v snapshot=%+v", n, err, storage.snapshot)
	}
}

func TestPR380InterruptedQuarantineKeepsEvidenceAndExposure(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = t.TempDir()
	input := testInput("review-interrupted", 100)
	if err := engine.PersistSettlement(input, 73, "settle"); err != nil {
		t.Fatal(err)
	}
	path, raw, _ := readOnlySpoolEvent(t, engine)
	directory := filepath.Join(engine.SpoolDir, "quarantine")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, filepath.Base(path))
	// Simulate a crash after linking payload but before writing reason/unlink.
	if err := os.Link(path, target); err != nil {
		t.Fatal(err)
	}
	if usage, err := engine.SpoolUsage(); err != nil || usage.Files != 2 || usage.UnresolvedExposure != 73 {
		t.Fatalf("interrupted isolation lost evidence or double-counted money: %+v %v", usage, err)
	}
	if err := engine.ReplaySpool(context.Background()); err != nil {
		t.Fatalf("interrupted quarantine retried unresolved money: %v", err)
	}
	if retained, err := os.ReadFile(target); err != nil || !bytes.Equal(raw, retained) {
		t.Fatalf("quarantine payload changed: %v", err)
	}
	if err := engine.PersistSettlement(input, 73, "settle"); !errors.Is(err, ErrPermanentOperation) {
		t.Fatalf("retry resurrected quarantined intent: %v", err)
	}
	if err := engine.PersistSettlement(input, 74, "settle"); !errors.Is(err, ErrSpoolConflict) {
		t.Fatalf("retry overwrote quarantined facts: %v", err)
	}
	if usage, err := engine.SpoolUsage(); err != nil || usage.Files != 1 || usage.QuarantinedFiles != 1 || usage.UnresolvedExposure != 73 || usage.QuarantinedBytes <= int64(len(raw)) {
		t.Fatalf("interrupted isolation was not durable: %+v %v", usage, err)
	}
}

func TestPR380QuarantineIOFailureLatchesAndRetainsSource(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = t.TempDir()
	path := filepath.Join(engine.SpoolDir, "000.json")
	raw := []byte(`{"broken"`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engine.SpoolDir, "quarantine"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	failures := 0
	engine.SpoolFailure = func(error) { failures++ }
	if err := engine.ReplaySpool(context.Background()); err == nil || failures != 1 {
		t.Fatalf("real isolation I/O failure did not latch: %v failures=%d", err, failures)
	}
	if retained, err := os.ReadFile(path); err != nil || !bytes.Equal(retained, raw) {
		t.Fatalf("failed isolation erased source: %v", err)
	}
}

func TestPR380NegativePrincipalSpoolQuarantinesAsUnknown(t *testing.T) {
	for _, subject := range []string{"actor", "project", "token"} {
		t.Run(subject, func(t *testing.T) {
			engine := New(nil, nil)
			engine.SpoolDir = t.TempDir()
			input := testInput("review-invalid-principal", 100)
			switch subject {
			case "actor":
				input.ActorID = -1
			case "project":
				input.ProjectID = -1
			case "token":
				input.TokenID = -1
			}
			_, raw := writeReviewSpool(t, engine, "000", input, 30)
			var notified []QuarantinedOperation
			engine.SpoolQuarantine = func(record QuarantinedOperation) { notified = append(notified, record) }
			failures := 0
			engine.SpoolFailure = func(error) { failures++ }
			if err := engine.ReplaySpool(context.Background()); err != nil {
				t.Fatalf("JSON-valid invalid %s identity attempted monetary replay: %v", subject, err)
			}
			if len(notified) != 1 || notified[0].AmountKnown || failures != 0 {
				t.Fatalf("invalid principal guessed valid exposure or disk failure: %+v failures=%d", notified, failures)
			}
			if retained, err := os.ReadFile(notified[0].File); err != nil || !bytes.Equal(retained, raw) {
				t.Fatalf("invalid principal evidence erased: %v", err)
			}
			if usage, err := engine.SpoolUsage(); err != nil || usage.Files != 1 || usage.UnresolvedExposure != 0 || usage.UnknownExposureFiles != 1 {
				t.Fatalf("invalid principal treated as known/empty exposure: %+v %v", usage, err)
			}
			if err := engine.PersistSettlement(input, 30, "settle"); !errors.Is(err, ErrPermanentOperation) || failures != 0 {
				t.Fatalf("accepted new malformed financial identity or latched disk: %v failures=%d", err, failures)
			}
		})
	}
	// Existing internal/legacy contexts have unspecified actor/project/key;
	// preserve that contract and the trusted no-key playground representation.
	input := testInput("review-compatible-principal", 100)
	input.ActorID, input.ProjectID, input.TokenID = 0, 0, 0
	for _, unlimited := range []bool{false, true} {
		input.Unlimited = unlimited
		if !validSpoolEvent(Event{Input: input, OperationID: input.RequestID, Kind: "settle"}) {
			t.Fatal("zero-ID legacy/playground compatibility changed")
		}
	}
}
