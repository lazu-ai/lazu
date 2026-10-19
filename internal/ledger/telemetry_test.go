package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestSettlementHonorsTelemetryBoundIntegration(t *testing.T) {
	for _, maximum := range []int64{3, 100_005} {
		t.Run(fmt.Sprint(maximum), func(t *testing.T) {
			client := testRedis(t)
			engine := testEngine(client, newMemoryStorage(1000, 1000))
			engine.TelemetryStream = Prefix + "test:configured-bound"
			engine.TelemetryMaxLength = maximum
			engine.TelemetryEncode = func(Event) (string, error) { return `{}`, nil }
			ctx := context.Background()
			if err := client.Eval(ctx, `for i=1,tonumber(ARGV[1]) do redis.call('XADD',KEYS[1],'*','type','fixture','payload','{}') end;return 1`, []string{engine.TelemetryStream}, maximum).Err(); err != nil {
				t.Fatal(err)
			}
			input := testInput("configured-bound", 100)
			if err := engine.Reserve(ctx, input); err != nil {
				t.Fatal(err)
			}
			if err := engine.SettleWithTelemetry(ctx, input, 80, `{}`); err != nil {
				t.Fatal(err)
			}
			if count, err := client.XLen(ctx, engine.TelemetryStream).Result(); err != nil || count != maximum {
				t.Fatalf("configured maximum=%d actual=%d err=%v", maximum, count, err)
			}
			if dropped, err := client.Get(ctx, engine.TelemetryStream+":dropped").Int64(); err != nil || dropped != 2 {
				t.Fatalf("both atomic producers must account for trims: dropped=%d err=%v", dropped, err)
			}
			rows, err := client.XRevRangeN(ctx, engine.TelemetryStream, "+", "-", 2).Result()
			if err != nil || len(rows) != 2 || rows[0].Values["type"] != "relay_result" || rows[1].Values["type"] != "financial_settlement" {
				t.Fatalf("missing atomic events: %+v %v", rows, err)
			}
			if err := engine.SettleWithTelemetry(ctx, input, 80, `{}`); err != nil {
				t.Fatal(err)
			}
			if dropped, _ := client.Get(ctx, engine.TelemetryStream+":dropped").Int64(); dropped != 2 || redisAvailable(t, client) != 920 {
				t.Fatal("retry duplicated settlement or trimming")
			}
		})
	}
}

func TestFullTelemetrySettlementAndSpoolReplayIntegration(t *testing.T) {
	client := testRedis(t)
	engine := testEngine(client, newMemoryStorage(1000, 1000))
	engine.TelemetryStream = Prefix + "test:full-telemetry"
	engine.TelemetryEncode = func(event Event) (string, error) {
		raw, err := json.Marshal(event)
		return string(raw), err
	}
	ctx := context.Background()
	envelope := `{"consume":{"request_id":"full-facts","amount_microusd":9007199254740993},"attempts":[{"provider_attempted":true}]}`
	input := testInput("full-facts", 100)
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := engine.SettleWithTelemetry(ctx, input, 80, envelope); err != nil {
			t.Fatal(err)
		}
	}
	assertEnvelope := func(wantCount int) {
		t.Helper()
		rows, err := client.XRange(ctx, engine.TelemetryStream, "-", "+").Result()
		if err != nil || len(rows) != wantCount {
			t.Fatalf("telemetry rows=%d want=%d err=%v rows=%+v", len(rows), wantCount, err, rows)
		}
		row := rows[len(rows)-1]
		if row.Values["type"] != "relay_result" || row.Values["payload"] != envelope {
			t.Fatalf("complete envelope changed: %+v", row.Values)
		}
	}
	assertEnvelope(2) // one minimum financial receipt and one complete envelope
	if count, _ := client.XLen(ctx, Stream).Result(); count != 1 {
		t.Fatalf("duplicate settlement appended %d financial events", count)
	}
	engine.SpoolDir = t.TempDir()
	input = testInput("spooled-facts", 100)
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := engine.PersistSettlementWithTelemetry(input, 60, "settle", envelope); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(engine.SpoolDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("durable spool entries=%v err=%v", entries, err)
	}
	if err := engine.ReplaySpool(ctx); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(4)
	rows, err := client.XRange(ctx, Stream, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if strings.Contains(row.Values["event"].(string), "telemetry_envelope") {
			t.Fatal("complete telemetry leaked into permanent financial journal")
		}
	}
	if balance := redisAvailable(t, client); balance != 860 {
		t.Fatalf("financial debit duplicated on telemetry replay: %d", balance)
	}
}

func TestPR380SettlementQueueFaultDoesNotMutateMoneyIntegration(t *testing.T) {
	for _, target := range []string{Stream, Prefix + "test:telemetry", Prefix + "test:telemetry:dropped", Prefix + "journal", Prefix + "uncommitted", Prefix + "expiry"} {
		t.Run(target, func(t *testing.T) {
			client := testRedis(t)
			engine := testEngine(client, newMemoryStorage(1000, 1000))
			engine.TelemetryStream = Prefix + "test:telemetry"
			engine.TelemetryEncode = func(Event) (string, error) { return `{}`, nil }
			engine.SpoolDir = t.TempDir()
			ctx := context.Background()
			input := testInput("queue-fault", 100)
			if err := engine.Reserve(ctx, input); err != nil {
				t.Fatal(err)
			}
			client.Del(ctx, target)
			if err := client.Set(ctx, target, "wrong-type", 0).Err(); err != nil {
				t.Fatal(err)
			}
			if err := engine.SettleWithTelemetry(ctx, input, 80, `{}`); err == nil {
				t.Fatal("expected queue fault")
			}
			if got := redisAvailable(t, client); got != 900 {
				t.Errorf("failed settlement mutated wallet: %d", got)
			}
			if got, _ := client.Get(ctx, token(2)).Int64(); got != 900 {
				t.Errorf("failed settlement mutated token: %d", got)
			}
			if got, _ := client.HGet(ctx, request(input.RequestID), "state").Result(); got != "reserved" {
				t.Errorf("failed settlement terminal state: %s", got)
			}
			if err := engine.PersistSettlementWithTelemetry(input, 80, "settle", `{}`); err != nil {
				t.Fatal(err)
			}
			client.Del(ctx, target)
			if err := engine.ReplaySpool(ctx); err != nil {
				t.Fatal(err)
			}
			if got := redisAvailable(t, client); got != 920 {
				t.Errorf("replay duplicated delta: %d", got)
			}
			if n, _ := client.XLen(ctx, Stream).Result(); n != 1 {
				t.Errorf("ledger events=%d", n)
			}
		})
	}
}

func TestPR380ExhaustedStreamRejectsBeforeMoneyIntegration(t *testing.T) {
	for _, stream := range []string{Stream, Prefix + "test:exhausted-telemetry"} {
		t.Run(stream, func(t *testing.T) {
			client := testRedis(t)
			engine := testEngine(client, newMemoryStorage(1000, 1000))
			ctx := context.Background()
			engine.TelemetryStream = Prefix + "test:exhausted-telemetry"
			input := historicalReviewInput("exhausted-stream")
			if err := engine.Reserve(ctx, input); err != nil {
				t.Fatal(err)
			}
			if err := client.Do(ctx, "XADD", stream, "18446744073709551615-18446744073709551615", "event", "fixture").Err(); err != nil {
				t.Fatal(err)
			}
			if err := engine.SettleWithTelemetry(ctx, input, 80, `{}`); err == nil {
				t.Fatal("accepted exhausted stream")
			}
			if got := redisAvailable(t, client); got != 900 {
				t.Fatalf("failed queue append changed wallet: %d", got)
			}
			if got, _ := client.HGet(ctx, input.Budgets[0].Key(), "reserved").Int64(); got != 100 {
				t.Fatalf("failed queue append changed budget: %d", got)
			}
			client.Del(ctx, stream)
			if err := engine.SettleWithTelemetry(ctx, input, 80, `{}`); err != nil {
				t.Fatal(err)
			}
			if got := redisAvailable(t, client); got != 920 {
				t.Fatalf("retry changed wallet twice: %d", got)
			}
		})
	}
}
