package ledger

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// SCRIPT FLUSH affects every logical database, so these tests create their own
// Redis process over a temporary Unix socket rather than touching a shared
// TEST_LEDGER_REDIS_URL server.
func dedicatedScriptRedis(t *testing.T) *redis.Client {
	t.Helper()
	path, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server required for isolated script-cache integration")
	}
	dir, err := os.MkdirTemp("", "ledger-script-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "redis.sock")
	command := exec.Command(path, "--port", "0", "--unixsocket", socket, "--save", "", "--appendonly", "no", "--logfile", filepath.Join(dir, "redis.log"))
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close(); _ = command.Process.Kill(); _ = command.Wait() })
	deadline := time.Now().Add(3 * time.Second)
	for client.Ping(context.Background()).Err() != nil {
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(dir, "redis.log"))
			t.Fatalf("isolated Redis did not become ready: %s", log)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := client.Set(context.Background(), ContinuityKey, "test-epoch:1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	return client
}

type scriptCommandHook struct {
	mu       sync.Mutex
	commands []string
}

func (h *scriptCommandHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	h.mu.Lock()
	h.commands = append(h.commands, cmd.Name())
	h.mu.Unlock()
	return ctx, nil
}
func (*scriptCommandHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (*scriptCommandHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (*scriptCommandHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }
func (h *scriptCommandHook) take() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := h.commands
	h.commands = nil
	return result
}

func TestGuardedScriptCacheWarmFlushAndContinuityIntegration(t *testing.T) {
	client := dedicatedScriptRedis(t)
	hook := &scriptCommandHook{}
	client.AddHook(hook)
	engine := testEngine(client, newMemoryStorage(1000, 1000))
	ctx := context.Background()
	script := `return redis.call('INCR', KEYS[1])`
	run := func(want int64, commands []string) {
		t.Helper()
		got, err := engine.evalRaw(ctx, script, []string{Prefix + "cache-test"})
		if err != nil || got != want {
			t.Fatalf("result=%v want=%d err=%v", got, want, err)
		}
		if observed := hook.take(); !reflect.DeepEqual(observed, commands) {
			t.Fatalf("dispatches=%v want=%v", observed, commands)
		}
	}
	if err := client.ScriptFlush(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	hook.take()
	run(1, []string{"evalsha", "eval"})
	run(2, []string{"evalsha"})
	if err := client.ScriptFlush(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	hook.take()
	run(3, []string{"evalsha", "eval"})
	run(4, []string{"evalsha"})
	if err := client.Del(ctx, ContinuityKey).Err(); err != nil {
		t.Fatal(err)
	}
	hook.take()
	if _, err := engine.evalRaw(ctx, script, []string{Prefix + "cache-test"}); !errors.Is(err, ErrContinuity) {
		t.Fatalf("missing epoch did not fence warm script: %v", err)
	}
	if got := hook.take(); !reflect.DeepEqual(got, []string{"evalsha"}) {
		t.Fatalf("warm missing guard dispatches=%v", got)
	}
	if err := client.Set(ctx, ContinuityKey, "test-epoch:2", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.evalRaw(ctx, script, []string{Prefix + "cache-test"}); !errors.Is(err, ErrContinuity) {
		t.Fatalf("changed epoch admitted old process: %v", err)
	}
	// Even reusing an Engine in a recovery test must bind its newly cached
	// script to the new continuity rather than silently keeping the old guard.
	engine.Continuity = "test-epoch:2"
	hook.take()
	run(5, []string{"evalsha", "eval"})
	run(6, []string{"evalsha"})
	engine.Continuity = ""
	if _, err := engine.evalRaw(ctx, script, []string{Prefix + "cache-test"}); !errors.Is(err, ErrContinuity) {
		t.Fatalf("empty epoch admitted: %v", err)
	}
	if got := hook.take(); len(got) != 0 {
		t.Fatalf("empty epoch sent commands: %v", got)
	}
	if got, err := client.Get(ctx, Prefix+"cache-test").Int64(); err != nil || got != 6 {
		t.Fatalf("guard or NOSCRIPT repeated mutation: %d %v", got, err)
	}
}

func TestSettlementScriptCacheFlushPreservesMoneyAndEnvelopeIntegration(t *testing.T) {
	client := dedicatedScriptRedis(t)
	engine := testEngine(client, newMemoryStorage(1000, 1000))
	engine.TelemetryStream = Prefix + "cache-test:telemetry"
	engine.TelemetryEncode = func(Event) (string, error) { return `{"amount_microusd":80}`, nil }
	ctx := context.Background()
	input := testInput("script-cache-settlement", 100)
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := engine.SettleWithTelemetry(ctx, input, 80, `{"request_id":"script-cache-settlement"}`); err != nil {
		t.Fatal(err)
	}
	if err := client.ScriptFlush(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if err := engine.SettleWithTelemetry(ctx, input, 80, `{"request_id":"script-cache-settlement"}`); err != nil {
		t.Fatal(err)
	}
	if got := redisAvailable(t, client); got != 920 {
		t.Fatalf("NOSCRIPT duplicate settlement changed balance: %d", got)
	}
	if got, err := client.XLen(ctx, Stream).Result(); err != nil || got != 1 {
		t.Fatalf("financial events=%d err=%v", got, err)
	}
	if got, err := client.XLen(ctx, engine.TelemetryStream).Result(); err != nil || got != 2 {
		t.Fatalf("receipt/envelope duplicated or lost: %d err=%v", got, err)
	}
	hook := &scriptCommandHook{}
	client.AddHook(hook)
	warm := testInput("warm-normal-request", 100)
	// Load the reserve script after SCRIPT FLUSH before counting warm traffic.
	if err := engine.Reserve(ctx, testInput("warm-primer", 0)); err != nil {
		t.Fatal(err)
	}
	hook.take()
	if err := engine.Reserve(ctx, warm); err != nil {
		t.Fatal(err)
	}
	if err := engine.SettleWithTelemetry(ctx, warm, 80, `{"request_id":"warm-normal-request"}`); err != nil {
		t.Fatal(err)
	}
	if got := hook.take(); !reflect.DeepEqual(got, []string{"evalsha", "evalsha"}) {
		t.Fatalf("warm reserve+settle should retain two ledger dispatches (identity is the third): %v", got)
	}
}
