package ledger

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSplitReservationTransfersHoldAndAdoptsRatesOnceIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	ctx := context.Background()
	parent := testInput("decision-parent", 300)
	parent.Budgets = []Budget{{ID: 99, ProjectID: parent.ProjectID, Scope: "org", Start: parent.OccurredAt.Add(-time.Hour).Unix(), End: parent.OccurredAt.Add(time.Hour).Unix(), Limit: 300, Block: true}}
	rpm := RateLimit{Key: "token_rpm:2", Decision: "token_rpm_exceeded", Limit: 2, Cost: 1, WindowSeconds: 3600}
	parent.Rates = []RateLimit{rpm}
	if err := engine.Reserve(ctx, parent); err != nil {
		t.Fatal(err)
	}
	native, child, err := engine.SplitReservation(ctx, parent, "decision-child", 100)
	if err != nil {
		t.Fatal(err)
	}
	if native.Amount != 200 || child.Amount != 100 || child.ExpiresAt <= time.Now().UnixMilli() {
		t.Fatalf("invalid transferred inputs: %+v %+v", native, child)
	}
	if redisAvailable(t, client) != 700 || redisTokenRemaining(t, client) != 700 {
		t.Fatal("split changed total wallet or key hold")
	}
	reserved, _ := client.HGet(ctx, parent.Budgets[0].Key(), "reserved").Int64()
	if reserved != 300 {
		t.Fatalf("split changed budget hold: %d", reserved)
	}
	if again, _, err := engine.SplitReservation(ctx, parent, child.RequestID, child.Amount); err != nil || again.Amount != native.Amount {
		t.Fatalf("split retry transferred funds again: %+v %v", again, err)
	}
	child.Rates = []RateLimit{rpm, {Key: "decision_qgen:token:2", Decision: "token_rpm_exceeded", Limit: 2, Cost: 1, WindowSeconds: 60}}
	if err := client.Del(ctx, request(child.RequestID)).Err(); err != nil {
		t.Fatal(err)
	}
	if again, _, err := engine.SplitReservation(ctx, parent, child.RequestID, child.Amount); err != nil || again.Amount != native.Amount {
		t.Fatalf("missing child state made a split retry transfer twice: %+v %v", again, err)
	}
	if err := client.Del(ctx, request(child.RequestID)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(ctx, child); err != nil {
		t.Fatalf("child could not adopt its prepaid hold: %v", err)
	}
	if err := engine.Reserve(ctx, child); err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, request(child.RequestID)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(ctx, child); err != nil {
		t.Fatal(err)
	}
	if redisAvailable(t, client) != 700 {
		t.Fatal("child adoption reserved the same funds twice")
	}
	serverTime, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	rateKey := fmt.Sprintf("%srate:%s:%d", Prefix, rpm.Key, serverTime.Unix()/rpm.WindowSeconds)
	if n, err := client.Get(ctx, rateKey).Int64(); err != nil || n != 2 {
		t.Fatalf("child rate adoption did not occur exactly once: %d %v", n, err)
	}
	if err := engine.Settle(ctx, child, 80); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, native, 250); err != nil {
		t.Fatal(err)
	}
	if err := engine.Release(ctx, native); err != nil {
		t.Fatal(err)
	}
	if err := engine.Release(ctx, child); err != nil {
		t.Fatal(err)
	}
	spent, _ := client.HGet(ctx, parent.Budgets[0].Key(), "spent").Int64()
	reserved, _ = client.HGet(ctx, parent.Budgets[0].Key(), "reserved").Int64()
	if redisAvailable(t, client) != 670 || spent != 330 || reserved != 0 {
		t.Fatalf("two-stage actual settlement lost money: wallet=%d spent=%d reserved=%d", redisAvailable(t, client), spent, reserved)
	}
	if err := engine.Reserve(ctx, child); err == nil {
		t.Fatal("completed child request was re-admitted")
	}
}

func TestBudgetColdRebuildFencesOtherPayersIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	storage.snapshot.TokenRemaining[4] = 1000
	engine := testEngine(client, storage)
	ctx := context.Background()
	first := testInput("budget-rebuild-first", 100)
	second := first
	second.RequestID = "fixture-budget-rebuild-other"
	second.PayerID, second.TokenID = 5, 4
	if err := engine.Reserve(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(ctx, second); err != nil {
		t.Fatal(err)
	}
	storage.snapshotStarted, storage.snapshotResume = make(chan struct{}, 1), make(chan struct{})
	newBudget := first
	newBudget.Budgets = []Budget{{ID: 123, Scope: "org", Start: first.OccurredAt.Add(-time.Hour).Unix(), End: first.OccurredAt.Add(time.Hour).Unix(), Limit: 1000, Block: true}}
	finished := make(chan error, 1)
	go func() { finished <- engine.Initialize(ctx, newBudget) }()
	select {
	case <-storage.snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("budget snapshot did not start")
	}
	if err := engine.Settle(ctx, second, 80); !errors.Is(err, ErrRebuilding) {
		t.Fatalf("another payer settled while shared budget baseline was being read: %v", err)
	}
	second.RequestID = "budget-rebuild-other-new"
	if err := engine.Reserve(ctx, second); !errors.Is(err, ErrRebuilding) {
		t.Fatalf("another payer entered while shared budget baseline was being read: %v", err)
	}
	close(storage.snapshotResume)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, first, 80); err != nil {
		t.Fatal(err)
	}
	spent, _ := client.HGet(ctx, newBudget.Budgets[0].Key(), "spent").Int64()
	if spent != 80 {
		t.Fatalf("old in-flight request did not update newly active org budget: %d", spent)
	}
}

func TestFinancialWorkerBuffersActualBatchIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	ctx := context.Background()
	first := testInput("batch-first", 10)
	if err := engine.Reserve(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, first, 10); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	done := make(chan error, 1)
	go func() {
		n, err := engine.Drain(ctx, "batching")
		if err == nil && n != 3 {
			err = fmt.Errorf("financial batch contained %d events, want 3", n)
		}
		done <- err
	}()
	for index := 0; index < 2; index++ {
		time.Sleep(50 * time.Millisecond)
		input := testInput(fmt.Sprintf("batch-later-%d", index), 10)
		if err := engine.Reserve(ctx, input); err != nil {
			t.Fatal(err)
		}
		if err := engine.Settle(ctx, input, 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < 900*time.Millisecond || storage.applyCalls != 1 {
		t.Fatalf("worker did not accumulate its flush interval: duration=%s SQL batches=%d", time.Since(started), storage.applyCalls)
	}
}

func TestExpiredReservationRecoversMissingRequestStateIntegration(t *testing.T) {
	client := testRedis(t)
	engine := testEngine(client, newMemoryStorage(1000, 1000))
	ctx := context.Background()
	input := testInput("expired-without-request-hash", 100)
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, request(input.RequestID)).Err(); err != nil {
		t.Fatal(err)
	}
	engine.Now = func() time.Time { return time.Now().Add(time.Hour) }
	if err := engine.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	if redisAvailable(t, client) != 1000 || redisTokenRemaining(t, client) != 1000 {
		t.Fatal("lost request state trapped the expired hold")
	}
	if exists, err := client.HExists(ctx, Prefix+"reservations", input.RequestID).Result(); err != nil || exists {
		t.Fatalf("recovered expired reservation was not removed: %v %v", exists, err)
	}
	if err := engine.Settle(ctx, input, 80); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, input, 80); err != nil {
		t.Fatal(err)
	}
	if redisAvailable(t, client) != 920 {
		t.Fatal("late settlement after recovered expiry was lost or duplicated")
	}
}
