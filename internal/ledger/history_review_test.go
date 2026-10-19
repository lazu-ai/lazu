package ledger

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func historicalReviewInput(id string) Input {
	at := time.Now().UTC().Add(-30 * 24 * time.Hour)
	input := testInput(id, 100)
	input.OccurredAt = at
	input.Budgets = []Budget{{ID: 91, ProjectID: input.ProjectID, Scope: "org", Start: at.Add(-time.Hour).Unix(), End: at.Add(time.Hour).Unix(), Limit: 1000, Block: true}}
	return input
}

func assertHistoricalBudgetRetained(t *testing.T, engine *Engine, input Input, spent int64) {
	t.Helper()
	ctx := context.Background()
	key := input.Budgets[0].Key()
	if got, err := engine.Redis.HGet(ctx, key, "spent").Int64(); err != nil || got != spent {
		t.Fatalf("historical immutable period spent=%d want=%d err=%v", got, spent, err)
	}
	if ttl, err := engine.Redis.TTL(ctx, key).Result(); err != nil || ttl < 6*24*time.Hour || ttl > 7*24*time.Hour {
		t.Fatalf("reconstructed historical counter expires immediately or is unbounded: ttl=%s err=%v", ttl, err)
	}
}

func TestPR380HistoricalRefundAfterCounterExpiryIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(920, 920)
	input := historicalReviewInput("review-historical-original")
	storage.snapshot.Budgets[input.Budgets[0].Key()] = 80
	engine := testEngine(client, storage)
	ctx := context.Background()
	for index := 0; index < 2; index++ {
		if err := engine.Refund(ctx, input, 20, "review-historical-refund"); err != nil {
			t.Fatalf("refund of immutable period older than retention failed: %v", err)
		}
	}
	assertHistoricalBudgetRetained(t, engine, input, 60)
	if got := redisAvailable(t, client); got != 940 {
		t.Fatalf("historical refund lost or duplicated: wallet=%d", got)
	}
	if n, err := engine.Drain(ctx, "historical-refund-writer"); err != nil || n != 1 {
		t.Fatalf("historical refund permanent write n=%d err=%v", n, err)
	}
	if storage.snapshot.Balance != 940 || storage.snapshot.Budgets[input.Budgets[0].Key()] != 60 {
		t.Fatalf("permanent historical facts changed period: %+v", storage.snapshot)
	}
}

func TestPR380HistoricalSpoolReplayAfterCounterExpiryIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	engine.SpoolDir = t.TempDir()
	input := historicalReviewInput("review-historical-spool")
	if err := engine.PersistSettlement(input, 80, "settle"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for index := 0; index < 2; index++ {
		if err := engine.ReplaySpool(ctx); err != nil {
			t.Fatalf("delayed immutable spool failed: %v", err)
		}
	}
	assertHistoricalBudgetRetained(t, engine, input, 80)
	if got := redisAvailable(t, client); got != 920 {
		t.Fatalf("spool settlement lost or duplicated: wallet=%d", got)
	}
	if usage, err := engine.SpoolUsage(); err != nil || usage.Files != 0 {
		t.Fatalf("accepted historical spool did not drain: %+v %v", usage, err)
	}
	if n, err := engine.Drain(ctx, "historical-spool-writer"); err != nil || n != 1 {
		t.Fatalf("historical spool permanent write n=%d err=%v", n, err)
	}
	if storage.snapshot.Balance != 920 || storage.snapshot.Budgets[input.Budgets[0].Key()] != 80 {
		t.Fatalf("permanent historical facts changed period: %+v", storage.snapshot)
	}
}

func TestPR380HistoricalRefundSpoolReplayAfterCounterExpiryIntegration(t *testing.T) {
	client := testRedis(t)
	storage := newMemoryStorage(920, 920)
	input := historicalReviewInput("review-historical-original")
	storage.snapshot.Budgets[input.Budgets[0].Key()] = 80
	input.ReferenceRequestID = input.RequestID
	input.RequestID = "review-historical-spooled-refund"
	engine := testEngine(client, storage)
	engine.SpoolDir = t.TempDir()
	if err := engine.PersistSettlement(input, -20, "refund"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for index := 0; index < 2; index++ {
		if err := engine.ReplaySpool(ctx); err != nil {
			t.Fatalf("delayed historical refund spool failed: %v", err)
		}
	}
	assertHistoricalBudgetRetained(t, engine, input, 60)
	if got := redisAvailable(t, client); got != 940 {
		t.Fatalf("historical spooled refund lost or duplicated: %d", got)
	}
	if n, err := engine.Drain(ctx, "historical-spool-refund-writer"); err != nil || n != 1 || storage.snapshot.Balance != 940 || storage.snapshot.Budgets[input.Budgets[0].Key()] != 60 {
		t.Fatalf("historical spooled refund committed wrong facts: n=%d err=%v snapshot=%+v", n, err, storage.snapshot)
	}
}

func TestPR380PaidZeroEstimateRequiresPositiveWalletIntegration(t *testing.T) {
	for _, balance := range []int64{0, -1, 1} {
		t.Run(fmt.Sprint(balance), func(t *testing.T) {
			client := testRedis(t)
			engine := testEngine(client, newMemoryStorage(balance, 1000))
			input := testInput("review-zero-paid", 0)
			input.Budgets = []Budget{{ID: 92, Start: time.Now().Unix() - 3600, End: time.Now().Unix() + 3600, Limit: 1000, Block: true}}
			input.Rates = []RateLimit{{Key: "review-zero-paid", Decision: "rpm", Limit: 10, Cost: 1, WindowSeconds: 3600}}
			ctx := context.Background()
			err := engine.Reserve(ctx, input)
			if balance > 0 {
				if err != nil {
					t.Fatalf("positive funded wallet rejected zero estimate: %v", err)
				}
				return
			}
			var rejection *Rejection
			if !errors.As(err, &rejection) || rejection.Code != "balance" {
				t.Fatalf("unfunded paid zero estimate admitted: balance=%d err=%v", balance, err)
			}
			if got := redisAvailable(t, client); got != balance {
				t.Fatal("rejection changed balance")
			}
			if exists, err := client.Exists(ctx, request(input.RequestID)).Result(); err != nil || exists != 0 {
				t.Fatalf("rejection created request hold: %d %v", exists, err)
			}
			if reserved, err := client.HGet(ctx, input.Budgets[0].Key(), "reserved").Int64(); err != nil || reserved != 0 {
				t.Fatalf("rejection charged budget: %d %v", reserved, err)
			}
			if keys, err := client.Keys(ctx, Prefix+"rate:*").Result(); err != nil || len(keys) != 0 {
				t.Fatalf("rejection consumed rate capacity: %+v %v", keys, err)
			}
		})
	}
}

func TestPR380OnlyExplicitFreeZeroEstimateCanUseZeroWalletIntegration(t *testing.T) {
	for _, balance := range []int64{0, -1} {
		t.Run(fmt.Sprint(balance), func(t *testing.T) {
			client := testRedis(t)
			engine := testEngine(client, newMemoryStorage(balance, 1000))
			ctx := context.Background()
			input := testInput("review-explicit-free", 0)
			input.AllowZeroBalance = true
			if err := engine.Reserve(ctx, input); err != nil {
				if balance < 0 {
					var rejected *Rejection
					if !errors.As(err, &rejected) || rejected.Code != "balance" {
						t.Fatalf("wrong negative wallet rejection: %v", err)
					}
					return
				}
				t.Fatalf("configured free request rejected at zero balance: %v", err)
			}
			if balance < 0 {
				t.Fatal("free policy admitted a negative wallet")
			}
			changed := input
			changed.AllowZeroBalance = false
			var rejected *Rejection
			if err := engine.Reserve(ctx, changed); !errors.As(err, &rejected) || rejected.Code != "identity_conflict" {
				t.Fatalf("stable identity accepted changed free policy: %v", err)
			}
			paid := input
			paid.RequestID, paid.Amount = "review-free-flag-paid", 1
			if err := engine.Reserve(ctx, paid); !errors.As(err, &rejected) || rejected.Code != "balance" {
				t.Fatalf("free flag bypassed positive charge: %v", err)
			}
		})
	}
}
