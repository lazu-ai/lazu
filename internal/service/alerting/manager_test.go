package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
)

func TestMaintenanceIncidentBackoffAndFingerprintChange(t *testing.T) {
	start := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	event := Incident{Fingerprint: "storage-401", Priority: PriorityMaintenance}

	state, send, changed := nextIncidentState(incidentState{}, false, event, start, "initial")
	if !send || changed || state.Occurrences != 1 {
		t.Fatalf("unexpected initial decision: state=%+v send=%v changed=%v", state, send, changed)
	}
	state = markIncidentNotified(state, start)
	state, send, _ = nextIncidentState(state, true, event, start.Add(5*time.Hour), "early")
	if send {
		t.Fatal("unchanged maintenance incident should be suppressed before six hours")
	}
	state, send, _ = nextIncidentState(state, true, event, start.Add(6*time.Hour), "reminder-1")
	if !send || state.ReminderStep != 0 {
		t.Fatalf("expected six-hour reminder decision, got state=%+v send=%v", state, send)
	}
	state = markIncidentNotified(state, start.Add(6*time.Hour))
	state, send, _ = nextIncidentState(state, true, event, start.Add(23*time.Hour), "early-2")
	if send {
		t.Fatal("second maintenance reminder should wait until 24 hours from first alert")
	}
	state, send, _ = nextIncidentState(state, true, event, start.Add(24*time.Hour), "reminder-2")
	if !send || state.ReminderStep != 1 {
		t.Fatalf("expected 24-hour reminder decision, got state=%+v send=%v", state, send)
	}
	state = markIncidentNotified(state, start.Add(24*time.Hour))
	state, send, _ = nextIncidentState(state, true, event, start.Add(48*time.Hour), "exhausted")
	if send {
		t.Fatal("unchanged incidents should move to the daily digest after the 24-hour reminder")
	}

	changedEvent := event
	changedEvent.Fingerprint = "storage-403"
	state, send, changed = nextIncidentState(state, true, changedEvent, start.Add(25*time.Hour), "changed")
	if !send || !changed || state.Fingerprint != "storage-403" || state.Occurrences != 1 {
		t.Fatalf("changed cause should alert immediately, got state=%+v send=%v changed=%v", state, send, changed)
	}
}

func TestHighPriorityIncidentUsesProgressiveBackoff(t *testing.T) {
	start := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	event := Incident{Fingerprint: "zero-token", Priority: PriorityHigh}
	state, _, _ := nextIncidentState(incidentState{}, false, event, start, "initial")
	state = markIncidentNotified(state, start)

	state, send, _ := nextIncidentState(state, true, event, start.Add(59*time.Minute), "early")
	if send {
		t.Fatal("high-priority incident should wait one hour before the first reminder")
	}
	state, send, _ = nextIncidentState(state, true, event, start.Add(time.Hour), "reminder-1")
	if !send {
		t.Fatal("expected first high-priority reminder after one hour")
	}
	state = markIncidentNotified(state, start.Add(time.Hour))
	state, send, _ = nextIncidentState(state, true, event, start.Add(6*time.Hour), "reminder-2")
	if !send {
		t.Fatal("expected second high-priority reminder six hours after initial alert")
	}
	state = markIncidentNotified(state, start.Add(6*time.Hour))
	state, send, _ = nextIncidentState(state, true, event, start.Add(24*time.Hour), "reminder-3")
	if !send {
		t.Fatal("expected third high-priority reminder 24 hours after initial alert")
	}
	state = markIncidentNotified(state, start.Add(24*time.Hour))
	state, send, _ = nextIncidentState(state, true, event, start.Add(48*time.Hour), "exhausted")
	if send {
		t.Fatal("high-priority incident should move to the daily digest after 24 hours")
	}
}

func TestIncidentDeliveryReservationSuppressesConcurrentReporter(t *testing.T) {
	start := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	event := Incident{Fingerprint: "storage-401", Priority: PriorityMaintenance}
	state, send, _ := nextIncidentState(incidentState{}, false, event, start, "owner-one")
	if !send || state.DeliveryToken != "owner-one" {
		t.Fatalf("expected first reporter to reserve delivery, state=%+v send=%v", state, send)
	}
	state, send, _ = nextIncidentState(state, true, event, start.Add(time.Second), "owner-two")
	if send || state.DeliveryToken != "owner-one" {
		t.Fatalf("concurrent reporter must not take active reservation, state=%+v send=%v", state, send)
	}
	state, send, _ = nextIncidentState(state, true, event, start.Add(deliveryLeaseTTL+time.Second), "owner-two")
	if !send || state.DeliveryToken != "owner-two" {
		t.Fatalf("expired reservation should be retryable, state=%+v send=%v", state, send)
	}
}

func TestDailyDigestFormatsOneCompactSummary(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	message := formatDailyDigest(now, []HealthSnapshot{
		{Key: "hourly-upload", Label: "R2 JSONL upload", Status: "ok", Summary: "上传 14 个文件", UpdatedAt: now.Add(-time.Hour).Unix()},
		{Key: "pg-dump", Label: "Database backup", Status: "failed", Summary: "错误：connection refused", UpdatedAt: now.Add(-30 * time.Minute).Unix()},
	})
	for _, expected := range []string{
		"Lazu 每日运维报告 · 2026-08-19",
		"⚠️ 失败 1 项，正常 1 项",
		"❌ 数据库备份：失败\n   30 分钟前更新 · 错误：connection refused",
		"✅ 请求日志上传 R2：正常\n   1 小时前更新 · 上传 14 个文件",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("digest missing %q: %s", expected, message)
		}
	}
	if strings.Index(message, "数据库备份") > strings.Index(message, "请求日志上传") {
		t.Fatalf("failed task should be listed before ok task: %s", message)
	}
}

func TestDailyDigestMarksTasksThatStoppedReportingAsStale(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.FixedZone("CST", 8*60*60))
	message := formatDailyDigest(now, []HealthSnapshot{
		{Key: "pg-dump", Label: "Database backup", Status: "ok", Summary: "size=671.7 MiB", UpdatedAt: now.Add(-(291*time.Hour + 38*time.Minute)).Unix()},
		{Key: "hourly-upload", Label: "R2 JSONL upload", Status: "ok", Summary: "上传 3 个文件", UpdatedAt: now.Add(-10 * time.Minute).Unix()},
		{Key: "relay-zero-token", Label: "Zero-token billing monitor", Status: "unknown", Summary: "流量低", UpdatedAt: now.Unix()},
	})
	for _, expected := range []string{
		"⚠️ 已停止运行 1 项，未检测 1 项，正常 1 项",
		"⏸️ 数据库备份：已停止运行\n   最后一次正常在 12 天 3 小时前（应每天运行一次）\n   上次结果：size=671.7 MiB",
		"➖ 零 token 计费监控：未检测\n   刚刚更新 · 流量低",
		"✅ 请求日志上传 R2：正常\n   10 分钟前更新",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("digest missing %q: %s", expected, message)
		}
	}
}

func TestDailyDigestHeadline(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	allOK := formatDailyDigest(now, []HealthSnapshot{
		{Key: "pg-dump", Label: "Database backup", Status: "ok", UpdatedAt: now.Add(-4 * time.Hour).Unix()},
		{Label: "Unregistered task", Status: "ok", UpdatedAt: now.Add(-30 * 24 * time.Hour).Unix()},
	})
	if !strings.Contains(allOK, "\n✅ 全部 2 项正常\n") || !strings.Contains(allOK, "✅ Unregistered task：正常") {
		t.Fatalf("expected all-ok headline; tasks without a cadence keep their label and never go stale: %s", allOK)
	}
	quiet := formatDailyDigest(now, []HealthSnapshot{
		{Key: "relay-zero-token", Label: "Zero-token billing monitor", Status: "unknown", UpdatedAt: now.Unix()},
	})
	if !strings.Contains(quiet, "\n➖ 未检测 1 项\n") {
		t.Fatalf("unknown alone should not raise a warning: %s", quiet)
	}
	odd := formatDailyDigest(now, []HealthSnapshot{
		{Label: "Custom task", Status: "degraded", UpdatedAt: now.Unix()},
	})
	if !strings.Contains(odd, "⚠️ degraded 1 项") || !strings.Contains(odd, "⚠️ Custom task：degraded") {
		t.Fatalf("unrecognized status should show verbatim as a warning: %s", odd)
	}
}

func TestFormatDigestAge(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{40 * time.Second, "0 分钟"},
		{59 * time.Minute, "59 分钟"},
		{2 * time.Hour, "2 小时"},
		{2*time.Hour + 28*time.Minute, "2 小时 28 分钟"},
		{24 * time.Hour, "1 天"},
		{291*time.Hour + 38*time.Minute, "12 天 3 小时"},
	} {
		if got := formatDigestAge(tc.in); got != tc.want {
			t.Fatalf("formatDigestAge(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPersistHealthSnapshotRetriesTransientRedisFailure(t *testing.T) {
	attempts := 0
	var delays []time.Duration
	err := persistHealthSnapshotWithRetry(func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary Redis failure")
		}
		return nil
	}, func(delay time.Duration) {
		delays = append(delays, delay)
	})
	if err != nil || attempts != 3 {
		t.Fatalf("expected third write to succeed, attempts=%d err=%v", attempts, err)
	}
	if len(delays) != 2 || delays[0] != 100*time.Millisecond || delays[1] != 200*time.Millisecond {
		t.Fatalf("unexpected retry delays: %v", delays)
	}
}

func TestReportFallsBackLocallyAndSendsOneRecovery(t *testing.T) {
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	previousNow, previousPost := nowFunc, postText
	common.RedisEnabled, common.RDB = false, nil
	localMu.Lock()
	localIncidents = map[string]incidentState{}
	localResolved = map[string]bool{}
	localMu.Unlock()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		nowFunc, postText = previousNow, previousPost
		localMu.Lock()
		localIncidents = map[string]incidentState{}
		localResolved = map[string]bool{}
		localMu.Unlock()
	})

	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	var messages []string
	postText = func(message string) error { messages = append(messages, message); return nil }
	event := Incident{
		Key:         "r2-upload",
		Fingerprint: "storage-401",
		Message:     "upload failed",
		Priority:    PriorityMaintenance,
	}

	Report(event)
	Report(event)
	if len(messages) != 1 {
		t.Fatalf("expected duplicate alert to be suppressed, got %v", messages)
	}
	now = now.Add(6 * time.Hour)
	Report(event)
	Resolve(event.Key, "upload recovered")
	Resolve(event.Key, "upload recovered")
	if len(messages) != 3 {
		t.Fatalf("expected initial, reminder, and one recovery message, got %v", messages)
	}
	if !strings.Contains(messages[1], "reminder: unchanged") || !strings.Contains(messages[2], "occurrences=3") {
		t.Fatalf("unexpected reminder or recovery details: %v", messages)
	}
}

func TestReportRetriesWithoutAdvancingBackoffAfterDeliveryFailure(t *testing.T) {
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	previousNow, previousPost := nowFunc, postText
	common.RedisEnabled, common.RDB = false, nil
	localMu.Lock()
	localIncidents = map[string]incidentState{}
	localResolved = map[string]bool{}
	localMu.Unlock()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		nowFunc, postText = previousNow, previousPost
		localMu.Lock()
		localIncidents = map[string]incidentState{}
		localResolved = map[string]bool{}
		localMu.Unlock()
	})

	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	attempts := 0
	postText = func(string) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary webhook failure")
		}
		return nil
	}
	event := Incident{Key: "delivery", Fingerprint: "same-cause", Message: "failed"}

	Report(event)
	state, ok := peekLocalIncident(event.Key)
	if !ok || state.LastNotified != 0 {
		t.Fatalf("failed delivery must remain immediately retryable, state=%+v", state)
	}
	Report(event)
	state, _ = peekLocalIncident(event.Key)
	if attempts != 2 || state.LastNotified != now.Unix() {
		t.Fatalf("successful retry should commit notification state, attempts=%d state=%+v", attempts, state)
	}
}

func TestResolveSuppressesRecoveryWhenFailureWasNeverDelivered(t *testing.T) {
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	previousNow, previousPost := nowFunc, postText
	common.RedisEnabled, common.RDB = false, nil
	localMu.Lock()
	localIncidents = map[string]incidentState{}
	localResolved = map[string]bool{}
	localMu.Unlock()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		nowFunc, postText = previousNow, previousPost
		localMu.Lock()
		localIncidents = map[string]incidentState{}
		localResolved = map[string]bool{}
		localMu.Unlock()
	})

	nowFunc = func() time.Time { return time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC) }
	attempts := 0
	postText = func(string) error {
		attempts++
		return errors.New("webhook rejected")
	}
	event := Incident{Key: "undelivered", Fingerprint: "query", Message: "failed"}
	Report(event)
	Resolve(event.Key, "recovered")
	if attempts != 1 {
		t.Fatalf("undelivered incident must not emit recovery, attempts=%d", attempts)
	}
	if _, exists := peekLocalIncident(event.Key); exists {
		t.Fatal("resolved undelivered incident should still be deleted")
	}
}

func TestRedisStateSurvivesProcessLocalReset(t *testing.T) {
	redisURL := os.Getenv("ALERTING_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("ALERTING_TEST_REDIS_URL is not set")
	}
	if !strings.Contains(redisURL, "127.0.0.1") && !strings.Contains(redisURL, "localhost") {
		t.Fatal("ALERTING_TEST_REDIS_URL must target a loopback Redis instance")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	client := redis.NewClient(options)
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush test Redis: %v", err)
	}
	defer client.Close()

	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	previousNow, previousPost := nowFunc, postText
	common.RedisEnabled, common.RDB = true, client
	localMu.Lock()
	localIncidents = map[string]incidentState{}
	localResolved = map[string]bool{}
	localMu.Unlock()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		nowFunc, postText = previousNow, previousPost
		localMu.Lock()
		localIncidents = map[string]incidentState{}
		localResolved = map[string]bool{}
		localMu.Unlock()
	})

	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	var messages []string
	postText = func(message string) error { messages = append(messages, message); return nil }
	event := Incident{Key: "persistent", Fingerprint: "same-cause", Message: "failed"}
	Report(event)

	// Simulate a process restart by dropping only the process-local shadow.
	localMu.Lock()
	localIncidents = map[string]incidentState{}
	localMu.Unlock()
	Report(event)
	if len(messages) != 1 {
		t.Fatalf("Redis-backed state should suppress after local reset, got %v", messages)
	}

	now = now.Add(6 * time.Hour)
	Report(event)
	Resolve(event.Key, "recovered")
	if len(messages) != 3 {
		t.Fatalf("expected persisted reminder and recovery, got %v", messages)
	}
}

func TestResolveReconcilesLocalFallbackAfterRedisRecovers(t *testing.T) {
	redisURL := os.Getenv("ALERTING_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("ALERTING_TEST_REDIS_URL is not set")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	client := redis.NewClient(options)
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush test Redis: %v", err)
	}
	defer client.Close()

	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	previousNow, previousPost := nowFunc, postText
	common.RedisEnabled, common.RDB = false, nil
	localMu.Lock()
	localIncidents = map[string]incidentState{}
	localResolved = map[string]bool{}
	localMu.Unlock()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		nowFunc, postText = previousNow, previousPost
		localMu.Lock()
		localIncidents = map[string]incidentState{}
		localResolved = map[string]bool{}
		localMu.Unlock()
	})

	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	var messages []string
	postText = func(message string) error { messages = append(messages, message); return nil }
	Report(Incident{Key: "fallback", Fingerprint: "network", Message: "failed"})

	common.RedisEnabled, common.RDB = true, client
	now = now.Add(time.Hour)
	Resolve("fallback", "recovered")
	Resolve("fallback", "recovered")
	if len(messages) != 2 || !strings.Contains(messages[1], "recovered") {
		t.Fatalf("expected one recovery from local fallback after Redis recovered, got %v", messages)
	}
}

func TestRedisReservationAllowsOnlyOneConcurrentDelivery(t *testing.T) {
	redisURL := os.Getenv("ALERTING_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("ALERTING_TEST_REDIS_URL is not set")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	client := redis.NewClient(options)
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush test Redis: %v", err)
	}
	defer client.Close()

	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	previousNow, previousPost := nowFunc, postText
	common.RedisEnabled, common.RDB = true, client
	localMu.Lock()
	localIncidents = map[string]incidentState{}
	localResolved = map[string]bool{}
	localMu.Unlock()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		nowFunc, postText = previousNow, previousPost
		localMu.Lock()
		localIncidents = map[string]incidentState{}
		localResolved = map[string]bool{}
		localMu.Unlock()
	})

	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	started := make(chan struct{})
	release := make(chan struct{})
	var attempts atomic.Int32
	postText = func(string) error {
		if attempts.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil
	}
	event := Incident{Key: "concurrent", Fingerprint: "same", Message: "failed"}
	done := make(chan struct{})
	go func() {
		Report(event)
		close(done)
	}()
	<-started
	Report(event)
	close(release)
	<-done

	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected one delivery attempt for concurrent reporters, got %d", got)
	}
	state, exists, err := loadRedisIncidentByKey(event.Key)
	if err != nil || !exists || state.LastNotified == 0 || state.DeliveryToken != "" {
		t.Fatalf("unexpected committed Redis state: state=%+v exists=%v err=%v", state, exists, err)
	}
}

func TestRedisReservationAllowsOnlyOneConcurrentRecovery(t *testing.T) {
	redisURL := os.Getenv("ALERTING_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("ALERTING_TEST_REDIS_URL is not set")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	client := redis.NewClient(options)
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush test Redis: %v", err)
	}
	defer client.Close()

	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	previousNow, previousPost := nowFunc, postText
	common.RedisEnabled, common.RDB = true, client
	localMu.Lock()
	localIncidents = map[string]incidentState{}
	localResolved = map[string]bool{}
	localMu.Unlock()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		nowFunc, postText = previousNow, previousPost
		localMu.Lock()
		localIncidents = map[string]incidentState{}
		localResolved = map[string]bool{}
		localMu.Unlock()
	})

	now := time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	seed := incidentState{
		Fingerprint:  "same",
		FirstSeen:    now.Add(-time.Hour).Unix(),
		LastSeen:     now.Add(-time.Minute).Unix(),
		LastNotified: now.Add(-time.Hour).Unix(),
		Occurrences:  3,
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	if err := client.Set(context.Background(), incidentKeyPrefix+"concurrent-recovery", raw, incidentTTL).Err(); err != nil {
		t.Fatalf("seed Redis incident: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var attempts atomic.Int32
	postText = func(string) error {
		if attempts.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil
	}
	done := make(chan struct{})
	go func() {
		Resolve("concurrent-recovery", "recovered")
		close(done)
	}()
	<-started
	Resolve("concurrent-recovery", "recovered")
	close(release)
	<-done

	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected one recovery delivery for concurrent resolvers, got %d", got)
	}
	if _, exists, err := loadRedisIncidentByKey("concurrent-recovery"); err != nil || exists {
		t.Fatalf("expected recovered incident to be deleted, exists=%v err=%v", exists, err)
	}
}
