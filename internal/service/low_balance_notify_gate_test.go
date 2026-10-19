package service

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
)

var lowBalanceTestMu sync.Mutex

func withLowBalanceTestGlobals(t *testing.T) {
	t.Helper()
	lowBalanceTestMu.Lock()

	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	previousNow := lowBalanceNow
	lowBalanceStoreMu.Lock()
	previousStore := lowBalanceNotifyStore
	lowBalanceNotifyStore = make(map[string]lowBalanceNotifyState)
	lowBalanceStoreMu.Unlock()

	lowBalanceSendFailMu.Lock()
	previousFails := lowBalanceSendFails
	lowBalanceSendFails = make(map[int]lowBalanceSendFailRecord)
	lowBalanceSendFailMu.Unlock()

	common.RedisEnabled = false
	common.RDB = nil

	t.Cleanup(func() {
		stopLowBalanceMemoryCleanup()
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
		lowBalanceNow = previousNow
		lowBalanceStoreMu.Lock()
		lowBalanceNotifyStore = previousStore
		lowBalanceStoreMu.Unlock()
		lowBalanceSendFailMu.Lock()
		lowBalanceSendFails = previousFails
		lowBalanceSendFailMu.Unlock()
		lowBalanceTestMu.Unlock()
	})
}

func TestLowBalanceRemindDays(t *testing.T) {
	withLowBalanceTestGlobals(t)

	if got := lowBalanceRemindDays(nil); len(got) != 5 || got[0] != 1 || got[4] != 30 {
		t.Fatalf("unexpected default remind days: %v", got)
	}

	cfg := &email_setting.EmailSetting{LowBalanceRemindDays: []int{30, 7, 7, 3}}
	got := lowBalanceRemindDays(cfg)
	want := []int{1, 3, 7, 30}
	if len(got) != len(want) {
		t.Fatalf("unexpected remind days length: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected remind days at %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestLowBalanceNotifyStateTTL(t *testing.T) {
	withLowBalanceTestGlobals(t)

	if got := lowBalanceNotifyStateTTL([]int{1, 3, 7, 15, 30}); got != 90*24*time.Hour {
		t.Fatalf("unexpected ttl for default schedule: %s", got)
	}
	if got := lowBalanceNotifyStateTTL([]int{1, 3, 7, 30, 120}); got != 180*24*time.Hour {
		t.Fatalf("unexpected ttl for extended schedule: %s", got)
	}
}

func TestShouldSendLowBalanceEmailBackoffSchedule(t *testing.T) {
	withLowBalanceTestGlobals(t)

	now := time.Unix(1700000000, 0)
	start := now
	lowBalanceNow = func() time.Time { return now }
	userID := 2026
	days := []int{1, 3, 7, 15, 30}

	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("day 1 notification should be sent")
	}
	if shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("same day repeated notification should be blocked")
	}

	now = start.Add(24 * time.Hour)
	if shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("day 2 should be blocked")
	}

	now = start.Add(48 * time.Hour)
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("day 3 notification should be sent")
	}

	now = start.Add(6 * 24 * time.Hour)
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("day 7 notification should be sent")
	}

	now = start.Add(14 * 24 * time.Hour)
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("day 15 notification should be sent")
	}

	now = start.Add(29 * 24 * time.Hour)
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("day 30 notification should be sent")
	}

	now = start.Add(35 * 24 * time.Hour)
	if shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("after day 30, notifications should stop for current low-balance episode")
	}
}

func TestResetLowBalanceEmailCooldown(t *testing.T) {
	withLowBalanceTestGlobals(t)

	now := time.Unix(1700000000, 0)
	lowBalanceNow = func() time.Time { return now }
	userID := 4096
	days := []int{1, 3, 7, 15, 30}

	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("first low-balance alert should be allowed")
	}
	if shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("same-day repeated low-balance alert should be blocked")
	}

	resetLowBalanceEmailCooldown(userID)
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("low-balance alert should be allowed immediately after reset")
	}
}

func TestShouldSendLowBalanceEmailRedisAtomicIfAvailable(t *testing.T) {
	withLowBalanceTestGlobals(t)

	redisConn := os.Getenv("REDIS_CONN_STRING")
	if redisConn == "" {
		t.Skip("REDIS_CONN_STRING is not set, skipping redis integration test")
	}

	opt, err := redis.ParseURL(redisConn)
	if err != nil {
		t.Skipf("invalid REDIS_CONN_STRING, skipping: %v", err)
	}

	client := redis.NewClient(opt)
	t.Cleanup(func() {
		_ = client.Close()
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis is unreachable, skipping: %v", err)
	}

	common.RedisEnabled = true
	common.RDB = client

	now := time.Unix(1700000000, 0)
	lowBalanceNow = func() time.Time { return now }
	userID := int(time.Now().UnixNano()%1_000_000_000 + 1)
	days := []int{1, 3, 7, 15, 30}

	const workers = 32
	results := make(chan bool, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- shouldSendLowBalanceEmail(userID, days)
		}()
	}
	wg.Wait()
	close(results)

	sentCount := 0
	for sent := range results {
		if sent {
			sentCount++
		}
	}
	if sentCount != 1 {
		t.Fatalf("expected only one sender in concurrent day-1 check, got %d", sentCount)
	}

	resetLowBalanceEmailCooldown(userID)
}

func TestPruneLowBalanceMemoryState(t *testing.T) {
	withLowBalanceTestGlobals(t)

	now := time.Unix(1700000000, 0)
	oldStart := now.Add(-(time.Duration(lowBalanceMemoryRetentionDays) + 1) * 24 * time.Hour).Unix()
	newStart := now.Add(-7 * 24 * time.Hour).Unix()

	lowBalanceStoreMu.Lock()
	lowBalanceNotifyStore["old"] = lowBalanceNotifyState{EpisodeStartUnix: oldStart, LastStep: 1}
	lowBalanceNotifyStore["new"] = lowBalanceNotifyState{EpisodeStartUnix: newStart, LastStep: 1}
	lowBalanceStoreMu.Unlock()

	pruneLowBalanceMemoryState(now)

	lowBalanceStoreMu.Lock()
	_, hasOld := lowBalanceNotifyStore["old"]
	_, hasNew := lowBalanceNotifyStore["new"]
	lowBalanceStoreMu.Unlock()
	if hasOld {
		t.Fatal("expected old memory state to be pruned")
	}
	if !hasNew {
		t.Fatal("expected recent memory state to be kept")
	}
}

func TestResetLowBalanceEmailCooldownClearsMemoryOnRedisError(t *testing.T) {
	withLowBalanceTestGlobals(t)

	client := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:0",
		DialTimeout:  20 * time.Millisecond,
		ReadTimeout:  20 * time.Millisecond,
		WriteTimeout: 20 * time.Millisecond,
	})
	t.Cleanup(func() {
		_ = client.Close()
	})
	common.RedisEnabled = true
	common.RDB = client

	key := lowBalanceNotifyKey(9527)
	lowBalanceStoreMu.Lock()
	lowBalanceNotifyStore[key] = lowBalanceNotifyState{
		EpisodeStartUnix: 1700000000,
		LastStep:         2,
	}
	lowBalanceStoreMu.Unlock()

	resetLowBalanceEmailCooldown(9527)

	lowBalanceStoreMu.Lock()
	_, exists := lowBalanceNotifyStore[key]
	lowBalanceStoreMu.Unlock()
	if exists {
		t.Fatal("expected memory state to be cleared even when redis delete fails")
	}
}

func TestSendFailureExponentialBackoff(t *testing.T) {
	withLowBalanceTestGlobals(t)

	now := time.Unix(1700000000, 0)
	lowBalanceNow = func() time.Time { return now }
	userID := 7788
	days := []int{1, 3, 7, 15, 30}

	// First notification should be sent.
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("first notification should be sent")
	}

	// Simulate send failure.
	handleLowBalanceEmailSendFailure(userID)

	// During cooldown (1h for first failure), should NOT retry.
	now = now.Add(30 * time.Minute)
	if shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("should be in cooldown after first failure")
	}

	// After 1h cooldown, retry should be allowed.
	now = now.Add(31 * time.Minute)
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("should be allowed to retry after 1h cooldown")
	}

	// Second failure → 2h cooldown.
	handleLowBalanceEmailSendFailure(userID)
	now = now.Add(time.Hour)
	if shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("should be in 2h cooldown after second failure")
	}
	now = now.Add(time.Hour + time.Second)
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("should be allowed to retry after 2h cooldown")
	}

	// Third failure → 4h cooldown.
	handleLowBalanceEmailSendFailure(userID)
	now = now.Add(3 * time.Hour)
	if shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("should be in 4h cooldown after third failure")
	}
	now = now.Add(time.Hour + time.Second)
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("should be allowed to retry after 4h cooldown")
	}
}

func TestSendFailureClearedOnSuccess(t *testing.T) {
	withLowBalanceTestGlobals(t)

	now := time.Unix(1700000000, 0)
	lowBalanceNow = func() time.Time { return now }
	userID := 5566
	days := []int{1, 3, 7, 15, 30}

	// Accumulate failures.
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("initial send should be allowed")
	}
	handleLowBalanceEmailSendFailure(userID)
	now = now.Add(2 * time.Hour)
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("retry should be allowed after cooldown")
	}
	handleLowBalanceEmailSendFailure(userID)

	// Simulate successful send → clears failure state.
	clearLowBalanceSendFailure(userID)

	// Should be immediately sendable (no cooldown).
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("should be allowed immediately after clearing failure state")
	}
}

func TestSendFailureClearedOnEpisodeReset(t *testing.T) {
	withLowBalanceTestGlobals(t)

	now := time.Unix(1700000000, 0)
	lowBalanceNow = func() time.Time { return now }
	userID := 3344
	days := []int{1, 3, 7, 15, 30}

	// Build up many consecutive failures.
	for i := 0; i < 10; i++ {
		now = now.Add(24 * time.Hour)
		shouldSendLowBalanceEmail(userID, days)
		handleLowBalanceEmailSendFailure(userID)
	}

	// Cooldown should be at max (16h) but still active.
	now = now.Add(15 * time.Hour)
	if shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("should still be in max cooldown")
	}

	// Simulate episode reset (user topped up).
	resetLowBalanceEmailCooldown(userID)
	clearLowBalanceSendFailure(userID)

	// Should get a fresh start.
	if !shouldSendLowBalanceEmail(userID, days) {
		t.Fatal("should be allowed after episode reset")
	}
}
