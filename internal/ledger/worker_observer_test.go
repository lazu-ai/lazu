package ledger

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLedgerObserverSerialReportsAndJoinsCancellation(t *testing.T) {
	engine := &Engine{}
	ctx, cancel := context.WithCancel(context.Background())
	started, nextStarted := make(chan struct{}), make(chan struct{})
	release, finish := make(chan struct{}), make(chan struct{})
	cancelled := make(chan struct{})
	reported := make(chan error, 4)
	var calls, active atomic.Int32
	observerError := errors.New("observer fixture error")
	engine.Observer = func(ctx context.Context) error {
		if active.Add(1) != 1 {
			t.Error("observer callbacks overlapped")
		}
		defer active.Add(-1)
		switch calls.Add(1) {
		case 1:
			close(started)
			<-release
			return observerError
		case 2:
			close(nextStarted)
			<-ctx.Done()
			close(cancelled)
			<-finish
			return ctx.Err()
		default:
			t.Error("observer started after cancellation")
			return nil
		}
	}
	stop := engine.startObserver(ctx, func(err error) { reported <- err }, 5*time.Millisecond)
	var releaseOnce, finishOnce sync.Once
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		finishOnce.Do(func() { close(finish) })
		stop()
	})
	waitObserverSignal(t, started, time.Second, "first observer invocation")
	// Several ticker periods pass while the first invocation is held. They
	// cannot start more scans or build an unbounded work queue.
	time.Sleep(40 * time.Millisecond)
	if calls.Load() != 1 || active.Load() != 1 {
		t.Fatalf("observer was not single-flight: calls=%d active=%d", calls.Load(), active.Load())
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-reported:
		if !errors.Is(err, observerError) {
			t.Fatalf("observer error was not reported: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("observer error was not reported")
	}
	waitObserverSignal(t, nextStarted, time.Second, "next observer invocation")
	cancel()
	waitObserverSignal(t, cancelled, time.Second, "observer context cancellation")
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("observer stop returned before the active callback exited")
	case <-time.After(30 * time.Millisecond):
	}
	finishOnce.Do(func() { close(finish) })
	waitObserverSignal(t, stopped, time.Second, "observer join")
	if calls.Load() != 2 || active.Load() != 0 {
		t.Fatalf("observer lifecycle leaked work: calls=%d active=%d", calls.Load(), active.Load())
	}
	select {
	case err := <-reported:
		t.Fatalf("shutdown cancellation was reported as a scan failure: %v", err)
	default:
	}
}

func TestLedgerRunBlockedObserverDoesNotBlockSettlementIntegration(t *testing.T) {
	client := dedicatedScriptRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	ctx, cancel := context.WithCancel(context.Background())
	started, cancelled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	engine.Observer = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-finish
		return ctx.Err()
	}
	errorsReported := make(chan error, 4)
	done := make(chan struct{})
	go func() { engine.Run(ctx, "observer-lifecycle", func(err error) { errorsReported <- err }); close(done) }()
	var finishOnce sync.Once
	t.Cleanup(func() {
		cancel()
		finishOnce.Do(func() { close(finish) })
		waitObserverSignal(t, done, 3*time.Second, "financial worker cleanup")
	})
	waitObserverSignal(t, started, 12*time.Second, "production observer tick")
	input := testInput("settle-while-observer-blocked", 100)
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, input, 70); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		storage.mu.Lock()
		_, committed := storage.applied["settle:"+input.RequestID]
		balance := storage.snapshot.Balance
		storage.mu.Unlock()
		if committed {
			if balance != 930 {
				t.Fatalf("committed settlement has wrong balance: %d", balance)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("blocked observer prevented financial commit")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	waitObserverSignal(t, cancelled, time.Second, "Run observer cancellation")
	select {
	case <-done:
		t.Fatal("Run exited without joining its observer")
	case <-time.After(30 * time.Millisecond):
	}
	finishOnce.Do(func() { close(finish) })
	waitObserverSignal(t, done, 3*time.Second, "Run observer join")
	for {
		select {
		case err := <-errorsReported:
			// Existing maintenance callbacks can observe shutdown cancellation.
			if !errors.Is(err, context.Canceled) && !(errors.Is(err, ErrUnavailable) && strings.HasSuffix(err.Error(), context.Canceled.Error())) {
				t.Fatalf("financial worker reported unexpected error: %v", err)
			}
		default:
			return
		}
	}
}

func waitObserverSignal(t *testing.T, signal <-chan struct{}, limit time.Duration, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(limit):
		t.Fatalf("timed out waiting for %s", name)
	}
}
