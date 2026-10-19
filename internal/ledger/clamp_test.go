package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func storedReservationAmount(t *testing.T, engine *Engine, requestID string) int64 {
	t.Helper()
	raw, err := engine.Redis.HGet(context.Background(), request(requestID), "input").Result()
	if err != nil {
		t.Fatal(err)
	}
	var input Input
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	return input.Amount
}

func requireRejection(t *testing.T, err error, code string) {
	t.Helper()
	var rejection *Rejection
	if !errors.As(err, &rejection) || rejection.Code != code {
		t.Fatalf("want %q rejection, got %v", code, err)
	}
}

// Production 2026-10-05: a Key with $2.36 left rejected every $2.56 worst-case
// estimate although each call settled near $0.19.
func TestClampToRemainingAdmitsPositiveKeyRemainderIntegration(t *testing.T) {
	const wallet, keyLeft, estimate, actual = 13_000_000, 2_360_000, 2_560_000, 190_000
	client := testRedis(t)
	engine := testEngine(client, newMemoryStorage(wallet, keyLeft))
	ctx := context.Background()

	strict := testInput("strict", estimate)
	_, err := engine.ReserveAdmitted(ctx, strict)
	requireRejection(t, err, "key")

	first := testInput("clamped", estimate)
	first.ClampToRemaining = true
	admitted, err := engine.ReserveAdmitted(ctx, first)
	if err != nil || admitted != keyLeft {
		t.Fatalf("admitted %d, %v; want the Key remainder %d", admitted, err, keyLeft)
	}
	if got := storedReservationAmount(t, engine, "clamped"); got != keyLeft {
		t.Fatalf("stored hold %d, want %d", got, keyLeft)
	}
	if redisTokenRemaining(t, client) != 0 || redisAvailable(t, client) != wallet-keyLeft {
		t.Fatal("clamped hold not reserved from Key and wallet")
	}
	first.Amount, first.ClampToRemaining = admitted, false
	if again, err := engine.ReserveAdmitted(ctx, first); err != nil || again != keyLeft {
		t.Fatalf("admitted reservation is not idempotent: %d %v", again, err)
	}
	if err := engine.Settle(ctx, first, actual); err != nil {
		t.Fatal(err)
	}
	if redisTokenRemaining(t, client) != keyLeft-actual || redisAvailable(t, client) != wallet-actual {
		t.Fatalf("settlement did not record the actual charge: key=%d wallet=%d", redisTokenRemaining(t, client), redisAvailable(t, client))
	}

	// The last call may overshoot; settlement records it and the Key then stops.
	last := testInput("last", estimate)
	last.ClampToRemaining = true
	admitted, err = engine.ReserveAdmitted(ctx, last)
	if err != nil || admitted != keyLeft-actual {
		t.Fatalf("second clamp admitted %d, %v", admitted, err)
	}
	last.Amount, last.ClampToRemaining = admitted, false
	if err := engine.Settle(ctx, last, 3_000_000); err != nil {
		t.Fatalf("overshooting settlement refused: %v", err)
	}
	if got := redisTokenRemaining(t, client); got != keyLeft-actual-3_000_000 {
		t.Fatalf("overshoot not recorded on the Key: %d", got)
	}
	exhausted := testInput("exhausted", estimate)
	exhausted.ClampToRemaining = true
	_, err = engine.ReserveAdmitted(ctx, exhausted)
	requireRejection(t, err, "key")
}

func TestClampToRemainingAdmitsPositiveWalletBalanceIntegration(t *testing.T) {
	const wallet, estimate = 2_000_000, 2_560_000
	client := testRedis(t)
	engine := testEngine(client, newMemoryStorage(wallet, 100_000_000))
	ctx := context.Background()

	_, err := engine.ReserveAdmitted(ctx, testInput("strict", estimate))
	requireRejection(t, err, "balance")

	clamped := testInput("clamped", estimate)
	clamped.ClampToRemaining = true
	admitted, err := engine.ReserveAdmitted(ctx, clamped)
	if err != nil || admitted != wallet {
		t.Fatalf("admitted %d, %v; want the wallet balance %d", admitted, err, wallet)
	}
	if redisAvailable(t, client) != 0 || redisTokenRemaining(t, client) != 100_000_000-wallet {
		t.Fatal("clamped hold not reserved from wallet and Key")
	}
	empty := testInput("empty", estimate)
	empty.ClampToRemaining = true
	_, err = engine.ReserveAdmitted(ctx, empty)
	requireRejection(t, err, "balance")
}
