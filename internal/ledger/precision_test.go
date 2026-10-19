package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func TestFinancialDecimalWireBackwardsCompatibility(t *testing.T) {
	const amount int64 = 623456789012345
	input := testInput("decimal-wire", amount)
	input.PricingVersion = 1791000000123456789
	input.Budgets = []Budget{{ID: 1, Limit: amount, Spent: 9007199254741017}}
	input.Rates = []RateLimit{{Key: "rate", Limit: amount, Cost: amount}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"amount":"623456789012345"`, `"spent":"9007199254741017"`, `"cost":"623456789012345"`, `"pricing_version":"1791000000123456789"`} {
		if !strings.Contains(string(raw), field) {
			t.Fatalf("exact decimal field missing %s: %s", field, raw)
		}
	}
	for _, legacy := range []string{
		`{"amount":623456789012345,"pricing_version":1791000000123456789,"budgets":[{"limit":623456789012345,"spent":9007199254741017}],"rates":[{"limit":623456789012345,"cost":623456789012345}]}`,
		`{"amount":6.23456789012345e14,"pricing_version":"1791000000123456789","budgets":[{"limit":6.23456789012345e14,"spent":"9007199254741017"}],"rates":[{"limit":"623456789012345","cost":6.23456789012345e14}]}`,
		string(raw),
	} {
		var decoded Input
		if err := json.Unmarshal([]byte(legacy), &decoded); err != nil || decoded.Amount != amount || decoded.PricingVersion != input.PricingVersion || decoded.Budgets[0].Spent != input.Budgets[0].Spent || decoded.Rates[0].Cost != amount {
			t.Fatalf("backwards compatible exact decode: %+v %v", decoded, err)
		}
	}
	for _, invalid := range []string{`{"amount":1.5}`, `{"amount":9.223372036854775808e18}`, `{"budgets":[{"spent":"9223372036854775808"}]}`, `{"rates":[{"cost":1e-3}]}`} {
		var decoded Input
		if err := json.Unmarshal([]byte(invalid), &decoded); err == nil {
			t.Fatalf("accepted fractional/overflow legacy amount %s", invalid)
		}
	}
}

func TestFinancialLargeDecimalSplitCrashReplayAndRebuildIntegration(t *testing.T) {
	client := testRedis(t)
	ctx := context.Background()
	const initial int64 = 9007199254741017 // odd aggregate above IEEE-754's exact range
	const priorSpent int64 = 9007199254741011
	const reserved int64 = 623456789012345
	const childAmount int64 = 223456789012345
	const parentActual int64 = 333456789012345
	const childActual int64 = 111456789012345
	const refund int64 = 111111111111111
	storage := newMemoryStorage(initial, initial)
	storage.snapshot.CreditSeq = initial
	engine := testEngine(client, storage)
	engine.HardLimit = math.MaxInt64
	input := testInput("decimal-parent", reserved)
	input.Budgets = []Budget{{ID: 81, Start: input.OccurredAt.Unix() - 3600, End: input.OccurredAt.Unix() + 3600, Limit: safeInteger}}
	budget := input.Budgets[0]
	storage.snapshot.Budgets[budget.Key()] = priorSpent
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	assertLargeCounters(t, client, budget, initial-reserved, priorSpent, reserved)
	parent, child, err := engine.SplitReservation(ctx, input, "decimal-child", childAmount)
	if err != nil || parent.Amount != reserved-childAmount || child.Amount != childAmount {
		t.Fatalf("split lost decimal facts: parent=%d child=%d %v", parent.Amount, child.Amount, err)
	}
	child.Rates = []RateLimit{{Key: "decimal-child-tpm", Decision: "tpm", Limit: safeInteger, Cost: childAmount, WindowSeconds: 3600}}
	if err := engine.Reserve(ctx, child); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(ctx, child); err != nil {
		t.Fatal(err)
	}
	rateKeys, _ := client.Keys(ctx, Prefix+"rate:decimal-child-tpm:*").Result()
	if len(rateKeys) != 1 {
		t.Fatalf("missing child rate counter %v", rateKeys)
	}
	if count, err := client.Get(ctx, rateKeys[0]).Int64(); err != nil || count != childAmount {
		t.Fatalf("adoption duplicated/rounded rate count=%d %v", count, err)
	}
	if err := engine.Settle(ctx, parent, parentActual); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, child, childActual); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refund(ctx, child, refund, "decimal-refund"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refund(ctx, child, refund, "decimal-refund"); err != nil {
		t.Fatal(err)
	}
	spent := parentActual + childActual - refund
	assertLargeCounters(t, client, budget, initial-spent, priorSpent+spent, 0)
	if exposure, err := client.Get(ctx, Prefix+"exposure").Int64(); err != nil || exposure != parentActual+childActual+refund {
		t.Fatalf("refund/settlement exposure=%d %v", exposure, err)
	}
	// Commit the first delivery, then crash before ACK. The next writer must
	// deduplicate exactly the same 15-digit financial fact.
	if err := client.XGroupCreateMkStream(ctx, Stream, Group, "0").Err(); err != nil {
		t.Fatal(err)
	}
	streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: Group, Consumer: "decimal-crashed", Streams: []string{Stream, ">"}, Count: 1, Block: time.Millisecond}).Result()
	if err != nil || len(streams) != 1 || len(streams[0].Messages) != 1 {
		t.Fatalf("read crash boundary: %+v %v", streams, err)
	}
	message := streams[0].Messages[0]
	var event Event
	if err := json.Unmarshal([]byte(message.Values["event"].(string)), &event); err != nil || event.Amount != parentActual {
		t.Fatalf("journal rounded first settlement %+v %v", event, err)
	}
	if err := storage.Apply(ctx, []Event{event}); err != nil {
		t.Fatal(err)
	}
	if err := client.Do(ctx, "XCLAIM", Stream, Group, "decimal-abandoned", 0, message.ID, "IDLE", 60000).Err(); err != nil {
		t.Fatal(err)
	}
	if n, err := engine.Drain(ctx, "decimal-recovered"); err != nil || n != 3 {
		t.Fatalf("replay count=%d %v", n, err)
	}
	if len(storage.applied) != 3 || storage.snapshot.Balance != initial-spent || storage.snapshot.Budgets[budget.Key()] != priorSpent+spent {
		t.Fatalf("permanent crash replay drift %+v", storage.snapshot)
	}
	if exposure, err := client.Get(ctx, Prefix+"exposure").Int64(); err != nil || exposure != 0 {
		t.Fatalf("ACK rounded remaining exposure=%d %v", exposure, err)
	}
	client.Del(ctx, wallet(1), token(2), budget.Key())
	if err := engine.Initialize(ctx, input); err != nil {
		t.Fatal(err)
	}
	assertLargeCounters(t, client, budget, initial-spent, priorSpent+spent, 0)
	credit := Credit{ID: 91, PayerID: 1, Sequence: initial + 1, Amount: reserved}
	if err := engine.Credit(ctx, credit); err != nil {
		t.Fatal(err)
	}
	if err := engine.Credit(ctx, credit); err != nil {
		t.Fatal(err)
	}
	if seq, err := client.HGet(ctx, wallet(1), "credit_seq").Int64(); err != nil || seq != initial+1 {
		t.Fatalf("large consecutive credit sequence=%d %v", seq, err)
	}
	if got := redisAvailable(t, client); got != initial-spent+reserved {
		t.Fatalf("large ordered credit wallet=%d", got)
	}
}

func assertLargeCounters(t *testing.T, client *redis.Client, budget Budget, balance, spent, reserved int64) {
	t.Helper()
	if wallet, key := redisAvailable(t, client), redisTokenRemaining(t, client); wallet != balance || key != balance {
		t.Fatalf("decimal balances wallet=%d token=%d want%d", wallet, key, balance)
	}
	for field, want := range map[string]int64{"spent": spent, "reserved": reserved} {
		if got, err := client.HGet(context.Background(), budget.Key(), field).Int64(); err != nil || got != want {
			t.Fatalf("decimal budget %s=%d want%d %v", field, got, want, err)
		}
	}
}

func TestFinancialLargeDecimalReapLateSettleAndSpoolIntegration(t *testing.T) {
	client := testRedis(t)
	ctx := context.Background()
	const initial int64 = 9007199254741017
	const amount int64 = 623456789012345
	storage := newMemoryStorage(initial, initial)
	engine := testEngine(client, storage)
	engine.HardLimit = math.MaxInt64
	engine.SpoolDir = t.TempDir()
	input := testInput("decimal-expired", amount)
	input.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != initial {
		t.Fatalf("large reap wallet=%d", got)
	}
	if err := engine.PersistSettlement(input, amount-17, "settle"); err != nil {
		t.Fatal(err)
	}
	restarted := testEngine(client, storage)
	restarted.SpoolDir = engine.SpoolDir
	if err := restarted.ReplaySpool(ctx); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ReplaySpool(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := restarted.Drain(ctx, "decimal-spool"); err != nil || n != 1 {
		t.Fatalf("spool drain=%d %v", n, err)
	}
	if got := redisAvailable(t, client); got != initial-amount+17 || storage.snapshot.Balance != got {
		t.Fatalf("large late/spooled settlement wallet=%d SQL=%d", got, storage.snapshot.Balance)
	}
}

func TestFinancialCounterOverflowFailsBeforeMutationIntegration(t *testing.T) {
	for _, counter := range []string{"budget", "exposure", "token", "success_rate"} {
		t.Run(counter, func(t *testing.T) {
			client := testRedis(t)
			ctx := context.Background()
			engine := testEngine(client, newMemoryStorage(1000, 1000))
			input := testInput("overflow-"+counter, 100)
			input.Budgets = []Budget{{ID: 89, Start: input.OccurredAt.Unix() - 3600, End: input.OccurredAt.Unix() + 3600, Limit: 1000}}
			input.Rates = []RateLimit{{Key: "overflow-success", Decision: "success", Limit: safeInteger, Cost: 1, WindowSeconds: 3600, OnSuccess: true}}
			if err := engine.Reserve(ctx, input); err != nil {
				t.Fatal(err)
			}
			switch counter {
			case "budget":
				client.HSet(ctx, input.Budgets[0].Key(), "spent", int64(math.MaxInt64))
			case "exposure":
				client.Set(ctx, Prefix+"exposure", int64(math.MaxInt64), 0)
			case "token":
				client.Set(ctx, token(2), int64(math.MaxInt64), 0)
			case "success_rate":
				client.Set(ctx, fmt.Sprintf("%srate:overflow-success:%d", Prefix, time.Now().UnixMilli()/3600000), int64(math.MaxInt64), 0)
			}
			walletBefore, _ := client.HGetAll(ctx, wallet(1)).Result()
			tokenBefore, _ := client.Get(ctx, token(2)).Result()
			budgetBefore, _ := client.HGetAll(ctx, input.Budgets[0].Key()).Result()
			var rejection *Rejection
			if err := engine.Settle(ctx, input, 1); !errors.As(err, &rejection) || rejection.Code != "money_range" {
				t.Fatalf("overflow result %v", err)
			}
			walletAfter, _ := client.HGetAll(ctx, wallet(1)).Result()
			tokenAfter, _ := client.Get(ctx, token(2)).Result()
			budgetAfter, _ := client.HGetAll(ctx, input.Budgets[0].Key()).Result()
			if fmt.Sprint(walletBefore) != fmt.Sprint(walletAfter) || tokenBefore != tokenAfter || fmt.Sprint(budgetBefore) != fmt.Sprint(budgetAfter) {
				t.Fatalf("partial counter mutation: wallet%v/%v token%s/%s budget%v/%v", walletBefore, walletAfter, tokenBefore, tokenAfter, budgetBefore, budgetAfter)
			}
			if state, _ := client.HGet(ctx, request(input.RequestID), "state").Result(); state != "reserved" {
				t.Fatalf("overflow consumed hold: %s", state)
			}
			if count, _ := client.XLen(ctx, Stream).Result(); count != 0 {
				t.Fatalf("overflow published financial event %d", count)
			}
		})
	}
}

func TestFinancialACKInvalidExposurePreservesPendingDeliveryIntegration(t *testing.T) {
	client := testRedis(t)
	ctx := context.Background()
	engine := testEngine(client, newMemoryStorage(1000, 1000))
	input := testInput("bad-exposure", 100)
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, input, 80); err != nil {
		t.Fatal(err)
	}
	client.Set(ctx, Prefix+"exposure", 79, 0)
	if _, err := engine.Drain(ctx, "bad-exposure-writer"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("invalid exposure ACK result %v", err)
	}
	if pending, err := client.XPending(ctx, Stream, Group).Result(); err != nil || pending.Count != 1 {
		t.Fatalf("invalid exposure erased pending %+v %v", pending, err)
	}
	if count, _ := client.ZCard(ctx, Prefix+"uncommitted").Result(); count != 1 {
		t.Fatal("invalid exposure erased journal identity")
	}
}

func TestFinancialLargeDecimalConcurrentBudgetTotalsIntegration(t *testing.T) {
	client := testRedis(t)
	ctx := context.Background()
	const initial int64 = 9007199254741017
	const amount int64 = 123456789012345
	const count = 32
	storage := newMemoryStorage(initial, initial)
	engine := testEngine(client, storage)
	engine.HardLimit = math.MaxInt64
	seed := testInput("decimal-concurrent-seed", 0)
	seed.Budgets = []Budget{{ID: 87, Start: seed.OccurredAt.Unix() - 3600, End: seed.OccurredAt.Unix() + 3600, Limit: safeInteger}}
	storage.snapshot.Budgets[seed.Budgets[0].Key()] = initial
	if err := engine.Initialize(ctx, seed); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			input := seed
			input.RequestID, input.Amount = fmt.Sprintf("decimal-concurrent-%d", index), amount
			if err := engine.Reserve(ctx, input); err != nil {
				t.Error(err)
				return
			}
			if err := engine.Settle(ctx, input, amount); err != nil {
				t.Error(err)
			}
		}(index)
	}
	group.Wait()
	assertLargeCounters(t, client, seed.Budgets[0], initial-count*amount, initial+count*amount, 0)
	if n, err := engine.Drain(ctx, "decimal-concurrent-writer"); err != nil || n != count {
		t.Fatalf("large concurrent drain=%d %v", n, err)
	}
	if storage.snapshot.Balance != initial-count*amount || storage.snapshot.Budgets[seed.Budgets[0].Key()] != initial+count*amount {
		t.Fatalf("concurrent permanent decimal drift %+v", storage.snapshot)
	}
}

func TestFinancialLargeDecimalLegacyStateAndExactLimitIntegration(t *testing.T) {
	client := testRedis(t)
	ctx := context.Background()
	const initial int64 = 9007199254741017
	const amount int64 = 623456789012345
	engine := testEngine(client, newMemoryStorage(initial, initial))
	engine.HardLimit = math.MaxInt64
	input := testInput("decimal-legacy-state", amount)
	input.Budgets = []Budget{{ID: 88, Start: input.OccurredAt.Unix() - 3600, End: input.OccurredAt.Unix() + 3600, Limit: amount, Block: true}}
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	// An already accepted record may still use the pre-migration numeric wire.
	raw, err := client.HGet(ctx, request(input.RequestID), "input").Result()
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.ReplaceAll(raw, fmt.Sprintf(`"%d"`, amount), fmt.Sprint(amount))
	client.HSet(ctx, request(input.RequestID), "input", legacy)
	client.HSet(ctx, Prefix+"reservations", input.RequestID, legacy)
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatalf("numeric legacy hold idempotency: %v", err)
	}
	next := input
	next.RequestID, next.Amount = "decimal-one-over-limit", 1
	var rejection *Rejection
	if err := engine.Reserve(ctx, next); !errors.As(err, &rejection) || rejection.BudgetID != 88 {
		t.Fatalf("one microdollar above 15-digit budget was admitted: %v", err)
	}
	if err := engine.Settle(ctx, input, amount); err != nil {
		t.Fatalf("numeric legacy hold settlement: %v", err)
	}
	assertLargeCounters(t, client, input.Budgets[0], initial-amount, amount, 0)
	raw, err = client.HGet(ctx, Prefix+"journal", input.RequestID).Result()
	if err != nil || !strings.Contains(raw, `"amount":"623456789012345"`) {
		t.Fatalf("legacy hold re-encoded imprecisely %s %v", raw, err)
	}
}
