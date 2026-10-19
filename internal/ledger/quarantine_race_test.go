package ledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type sharedQuarantineStorage struct {
	*memoryStorage
	mu           sync.Mutex
	arrivals     int
	bothReached  chan struct{}
	firstDurable chan struct{}
}

func (s *sharedQuarantineStorage) Settled(ctx context.Context, id, kind string) (bool, error) {
	s.mu.Lock()
	s.arrivals++
	arrival := s.arrivals
	if arrival == 2 {
		close(s.bothReached)
	}
	s.mu.Unlock()
	select {
	case <-s.bothReached:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	if arrival == 2 {
		select {
		case <-s.firstDurable:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return false, fmt.Errorf("%w: fixture deleted payer", ErrPermanentOperation)
}

func TestPR380SharedSpoolQuarantineRaceRetainsProofWithoutDiskFailureIntegration(t *testing.T) {
	client := testRedis(t)
	storage := &sharedQuarantineStorage{memoryStorage: newMemoryStorage(1000, 1000), bothReached: make(chan struct{}), firstDurable: make(chan struct{})}
	first, second := testEngine(client, storage), testEngine(client, storage)
	first.SpoolDir = t.TempDir()
	second.SpoolDir = first.SpoolDir
	_, raw := writeReviewSpool(t, first, "000", testInput("review-shared-permanent", 100), 73)
	var failures, notices atomic.Int64
	var once sync.Once
	for _, engine := range []*Engine{first, second} {
		engine.SpoolFailure = func(error) { failures.Add(1) }
		engine.SpoolQuarantine = func(record QuarantinedOperation) {
			if retained, err := os.ReadFile(record.File); err != nil || !bytes.Equal(raw, retained) {
				t.Errorf("concurrent isolation lost immutable raw: %v", err)
			}
			if _, err := os.Stat(record.File + ".reason"); err != nil {
				t.Errorf("notification before durable metadata: %v", err)
			}
			notices.Add(1)
			once.Do(func() { close(storage.firstDurable) })
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := make(chan error, 2)
	// Both read the same active source before the permanent lookup result.
	// The second reaches Link only after the first's durable source unlink.
	go func() { results <- first.ReplaySpool(ctx) }()
	go func() { results <- second.ReplaySpool(ctx) }()
	for count := 0; count < 2; count++ {
		if err := <-results; err != nil {
			t.Errorf("durable competing quarantine caused replay failure: %v", err)
		}
	}
	if failures.Load() != 0 || notices.Load() != 2 {
		t.Fatalf("shared isolation falsely latched durability or lost completion: failures=%d notices=%d", failures.Load(), notices.Load())
	}
	usage, err := first.SpoolUsage()
	if err != nil || usage.Files != 1 || usage.QuarantinedFiles != 1 || usage.UnresolvedExposure != 73 || usage.UnknownExposureFiles != 0 {
		t.Fatalf("shared isolation lost or duplicated monetary evidence: %+v %v", usage, err)
	}
	if count, err := client.Exists(ctx, wallet(1), request("review-shared-permanent")).Result(); err != nil || count != 0 {
		t.Fatalf("permanent classification mutated money: %d %v", count, err)
	}
}

func TestPR380MissingBothQuarantineCopiesRemainsDiskFailure(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = t.TempDir()
	path, raw := writeReviewSpool(t, engine, "000", testInput("review-lost-evidence", 100), 73)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	err := engine.quarantineSpool(path, raw, nil, ErrPermanentOperation)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing both durable copies reported isolation success: %v", err)
	}
	failures := 0
	engine.SpoolFailure = func(error) { failures++ }
	engine.recordSpoolFailure(err)
	if failures != 1 {
		t.Fatal("actual missing financial evidence did not latch disk failure")
	}
	if _, err := os.Stat(filepath.Join(engine.SpoolDir, "quarantine", "000.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invented durable evidence from RAM: %v", err)
	}
}
