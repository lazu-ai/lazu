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

// onlyExpectDigestTasks limits the never-reported check to the given keys,
// so a test sees missing tasks only when it asks for them.
func onlyExpectDigestTasks(t *testing.T, keys ...string) {
	t.Helper()
	previous := digestTaskExpected
	expected := map[string]bool{}
	for _, key := range keys {
		expected[key] = true
	}
	digestTaskExpected = func(key string) bool { return expected[key] }
	t.Cleanup(func() { digestTaskExpected = previous })
}

func TestDailyDigestLeadsWithProblemsAndFoldsHealthyTasks(t *testing.T) {
	onlyExpectDigestTasks(t, "hourly-upload", "pg-dump", "relay-zero-token")
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	message := formatDailyDigest(now, []HealthSnapshot{
		{Key: "hourly-upload", Label: "R2 JSONL upload", Status: "ok", Summary: "上传 14 个文件", UpdatedAt: now.Add(-time.Hour).Unix()},
		{Key: "pg-dump", Label: "Database backup", Status: "failed", Summary: "错误：connection refused", UpdatedAt: now.Add(-30 * time.Minute).Unix()},
		{Key: "relay-zero-token", Label: "Zero-token billing monitor", Status: "unknown", Summary: "流量低", UpdatedAt: now.Unix()},
	}, nil, nil)
	want := `📊 Lazu 每日运维报告 · 2026-08-19
⚠️ 需要处理 1 项

❌ 数据库备份：失败
   30 分钟前更新 · 错误：connection refused

【定时任务】
✅ 正常 1 项：请求日志上传 R2
➖ 未检测 1 项：零 token 计费监控`
	if message != want {
		t.Fatalf("digest =\n%s\nwant\n%s", message, want)
	}
}

func TestDailyDigestMarksTasksThatStoppedReportingAsStale(t *testing.T) {
	onlyExpectDigestTasks(t, "hourly-upload", "pg-dump", "relay-zero-token")
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.FixedZone("CST", 8*60*60))
	message := formatDailyDigest(now, []HealthSnapshot{
		{Key: "pg-dump", Label: "Database backup", Status: "ok", Summary: "size=671.7 MiB", UpdatedAt: now.Add(-(291*time.Hour + 38*time.Minute)).Unix()},
		{Key: "hourly-upload", Label: "R2 JSONL upload", Status: "ok", Summary: "上传 3 个文件", UpdatedAt: now.Add(-10 * time.Minute).Unix()},
	}, nil, nil)
	for _, expected := range []string{
		"⚠️ 需要处理 1 项",
		"⏸️ 数据库备份：已停止运行\n   最后一次正常在 12 天 3 小时前（应每天运行一次）\n   上次结果：size=671.7 MiB",
		"✅ 正常 1 项：请求日志上传 R2",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("digest missing %q: %s", expected, message)
		}
	}
}

func TestDailyDigestHeadline(t *testing.T) {
	onlyExpectDigestTasks(t, "pg-dump", "relay-zero-token")
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	allOK := formatDailyDigest(now, []HealthSnapshot{
		{Key: "pg-dump", Label: "Database backup", Status: "ok", UpdatedAt: now.Add(-4 * time.Hour).Unix()},
		{Label: "Unregistered task", Status: "ok", UpdatedAt: now.Add(-30 * 24 * time.Hour).Unix()},
		{Key: "relay-zero-token", Label: "Zero-token billing monitor", Status: "unknown", UpdatedAt: now.Unix()},
	}, nil, nil)
	if !strings.Contains(allOK, "\n✅ 没有需要处理的事\n") || !strings.Contains(allOK, "✅ 正常 2 项：Unregistered task、数据库备份") {
		t.Fatalf("unknown and keyless tasks need no action: %s", allOK)
	}
	odd := formatDailyDigest(now, []HealthSnapshot{
		{Label: "Custom task", Status: "degraded", UpdatedAt: now.Unix()},
	}, nil, nil)
	if !strings.Contains(odd, "⚠️ 需要处理 1 项") || !strings.Contains(odd, "⚠️ Custom task：degraded") {
		t.Fatalf("unrecognized status should show verbatim as a problem: %s", odd)
	}
}

func TestDailyDigestShowsSections(t *testing.T) {
	onlyExpectDigestTasks(t, "pg-dump")
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	message := formatDailyDigest(now, []HealthSnapshot{
		{Key: "pg-dump", Label: "Database backup", Status: "ok", UpdatedAt: now.Add(-4 * time.Hour).Unix()},
	}, nil, []DigestSection{
		{Title: "昨日业务（10-19）", Lines: []string{"请求 12.4 万"}, Attention: []string{"昨日成功率 95.0%"}},
		{Title: "数据与容量", Lines: []string{"磁盘已用 41%"}},
	})
	want := `📊 Lazu 每日运维报告 · 2026-10-20
⚠️ 需要处理 1 项

⚠️ 昨日成功率 95.0%

【昨日业务（10-19）】
请求 12.4 万

【数据与容量】
磁盘已用 41%

【定时任务】
✅ 正常 1 项：数据库备份`
	if message != want {
		t.Fatalf("digest =\n%s\nwant\n%s", message, want)
	}
}

func TestDigestSectionFailureRaisesAttention(t *testing.T) {
	previous := digestSections
	digestSections = nil
	defer func() { digestSections = previous }()
	RegisterDigestSection("昨日业务", func(context.Context, time.Time) (DigestSection, error) {
		return DigestSection{}, errors.New("telemetry down")
	})
	RegisterDigestSection("数据与容量", func(context.Context, time.Time) (DigestSection, error) {
		var missing map[string]int
		missing["boom"]++
		return DigestSection{}, nil
	})
	sections := buildDigestSections(time.Date(2026, 10, 20, 1, 10, 0, 0, time.UTC))
	if len(sections) != 2 || sections[1].Title != "数据与容量" || len(sections[1].Attention) != 1 || !strings.Contains(sections[1].Attention[0], "panic") {
		t.Fatalf("a panicking section must not take the digest down: %+v", sections)
	}
	if len(sections) != 2 || sections[0].Title != "昨日业务" || len(sections[0].Attention) != 1 ||
		!strings.Contains(sections[0].Attention[0], "昨日业务数据读取失败：telemetry down") {
		t.Fatalf("a failed section should raise attention: %+v", sections)
	}
	onlyExpectDigestTasks(t)
	message := formatDailyDigest(time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC), nil, nil, sections)
	if !strings.Contains(message, "⚠️ 需要处理 2 项") || strings.Contains(message, "没有需要处理的事") {
		t.Fatalf("unreadable sections must not produce an all-clear headline: %s", message)
	}
}

func TestDailyDigestShowsOpenAlerts(t *testing.T) {
	onlyExpectDigestTasks(t, "hourly-upload", "pg-dump")
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	incidents := map[string]incidentState{
		// Health still reads ok, but the alert was never resolved.
		"hourly-upload": {Fingerprint: "storage-401", FirstSeen: now.Add(-50 * time.Hour).Unix(), LastSeen: now.Add(-20 * time.Minute).Unix(), Occurrences: 48, Title: "⚠️ Lazu hourly upload needs attention"},
		// No health record names this one, so the title must.
		"settlement-outbox": {Fingerprint: "stuck", FirstSeen: now.Add(-3 * time.Hour).Unix(), LastSeen: now.Unix(), Occurrences: 3, Title: "❌ settlement outbox stuck"},
	}
	message := formatDailyDigest(now, []HealthSnapshot{
		{Key: "hourly-upload", Label: "R2 JSONL upload", Status: "ok", Summary: "上传 2 个文件", UpdatedAt: now.Add(-10 * time.Minute).Unix()},
		{Key: "pg-dump", Label: "Database backup", Status: "ok", UpdatedAt: now.Add(-4 * time.Hour).Unix()},
	}, incidents, nil)
	for _, expected := range []string{
		"⚠️ 需要处理 2 项",
		"🔔 请求日志上传 R2：告警未恢复\n   告警已持续 2 天 2 小时，共触发 48 次，最近一次：20 分钟前\n   10 分钟前更新 · 上传 2 个文件",
		"🔔 settlement-outbox：告警未恢复\n   ❌ settlement outbox stuck\n   告警已持续 3 小时，共触发 3 次，最近一次：刚刚",
		"✅ 正常 1 项：数据库备份",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("digest missing %q: %s", expected, message)
		}
	}
}

func TestDailyDigestListsExpectedTasksThatNeverReported(t *testing.T) {
	onlyExpectDigestTasks(t, "pg-dump", "relay-zero-token")
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	message := formatDailyDigest(now, []HealthSnapshot{
		{Key: "pg-dump", Label: "Database backup", Status: "ok", UpdatedAt: now.Add(-4 * time.Hour).Unix()},
	}, map[string]incidentState{}, nil)
	for _, expected := range []string{
		"✅ 没有需要处理的事",
		"✅ 正常 1 项：数据库备份\n➖ 从未上报 1 项：零 token 计费监控",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("digest missing %q: %s", expected, message)
		}
	}
	if strings.Contains(message, "R2 旧归档压缩") {
		t.Fatalf("a task this deployment does not run must not be listed: %s", message)
	}
}

func TestDailyDigestSkipsSnapshotsOfDisabledTasks(t *testing.T) {
	onlyExpectDigestTasks(t, "pg-dump")
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	old := now.Add(-30 * 24 * time.Hour).Unix()
	message := formatDailyDigest(now, []HealthSnapshot{
		{Key: "pg-dump", Label: "Database backup", Status: "ok", UpdatedAt: now.Add(-4 * time.Hour).Unix()},
		// Upload and compactor were switched off after they last reported.
		{Key: "hourly-upload", Label: "R2 JSONL upload", Status: "ok", UpdatedAt: old},
		{Key: "archive-compactor", Label: "R2 archive compactor", Status: "ok", UpdatedAt: old},
	}, map[string]incidentState{
		"archive-compactor": {Fingerprint: "x", FirstSeen: old, LastSeen: old, Occurrences: 1, Title: "❌ compactor FAILED"},
	}, nil)
	if strings.Contains(message, "已停止运行") || strings.Contains(message, "请求日志上传") {
		t.Fatalf("a disabled task must not be reported as stopped: %s", message)
	}
	if !strings.Contains(message, "🔔 R2 旧归档压缩：告警未恢复") {
		t.Fatalf("an open alert for a disabled task is still listed: %s", message)
	}
}

func TestZeroTokenMonitorIsExpectedOnlyWithASeparateWorker(t *testing.T) {
	t.Setenv("PROCESS_ROLE", "all-in-one")
	if digestTaskExpected("relay-zero-token") {
		t.Fatal("an all-in-one API never schedules the zero-token monitor")
	}
	t.Setenv("PROCESS_ROLE", "api")
	if !digestTaskExpected("relay-zero-token") {
		t.Fatal("a split deployment runs the monitor in its worker")
	}
}

func TestUploadCadenceFollowsConfiguredSchedule(t *testing.T) {
	previous := common.AppConfig.Upload.Schedule
	defer func() { common.AppConfig.Upload.Schedule = previous }()
	onlyExpectDigestTasks(t, "hourly-upload")
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	snapshots := []HealthSnapshot{{Key: "hourly-upload", Label: "R2 JSONL upload", Status: "ok", UpdatedAt: now.Add(-5*time.Hour - 10*time.Minute).Unix()}}

	common.AppConfig.Upload.Schedule = "0 4 * * *"
	if message := formatDailyDigest(now, snapshots, nil, nil); strings.Contains(message, "已停止运行") {
		t.Fatalf("a daily upload five hours after its run is not stale: %s", message)
	}
	common.AppConfig.Upload.Schedule = "0 * * * *"
	if message := formatDailyDigest(now, snapshots, nil, nil); !strings.Contains(message, "应每小时运行一次") {
		t.Fatalf("an hourly upload five hours late is stale: %s", message)
	}
	common.AppConfig.Upload.Schedule = "not a cron spec"
	if cadence := digestTaskCadence("hourly-upload", digestTasks["hourly-upload"], now); cadence != time.Hour {
		t.Fatalf("an unparsable schedule falls back to hourly, got %s", cadence)
	}
	if cadence, ok := cronCadence("0 9,21 * * *", now); !ok || cadence != 12*time.Hour {
		t.Fatalf("cronCadence(0 9,21) = %s %v", cadence, ok)
	}
}

func TestLoadOpenIncidentsUsesLocalCacheOnlyWithoutRedis(t *testing.T) {
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = false, nil
	localMu.Lock()
	previousLocal := localIncidents
	localIncidents = map[string]incidentState{"pg-dump": {Fingerprint: "x", Occurrences: 2}}
	localMu.Unlock()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		localMu.Lock()
		localIncidents = previousLocal
		localMu.Unlock()
	})
	open, err := loadOpenIncidents()
	if err != nil || open["pg-dump"].Occurrences != 2 {
		t.Fatalf("without Redis the local cache is the source: open=%+v err=%v", open, err)
	}
}

func TestNextIncidentStateKeepsLatestTitle(t *testing.T) {
	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	event := Incident{Fingerprint: "storage-401", Priority: PriorityMaintenance, Message: "⚠️ first line\ndetail"}
	state, _, _ := nextIncidentState(incidentState{}, false, event, start, "one")
	if state.Title != "⚠️ first line" {
		t.Fatalf("title = %q", state.Title)
	}
	event.Message = "⚠️ newer line\ndetail"
	state, _, _ = nextIncidentState(state, true, event, start.Add(time.Minute), "two")
	if state.Title != "⚠️ newer line" {
		t.Fatalf("continuing incident should keep the latest title, got %q", state.Title)
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

// alertingTestRedis returns a flushed client for ALERTING_TEST_REDIS_URL and
// installs it as the package Redis with clean local state for the test.
func alertingTestRedis(t *testing.T) *redis.Client {
	t.Helper()
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
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = true, client
	localMu.Lock()
	previousLocal, previousDay := localIncidents, localDigestDay
	localIncidents, localDigestDay = map[string]incidentState{}, ""
	localMu.Unlock()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		localMu.Lock()
		localIncidents, localDigestDay = previousLocal, previousDay
		localMu.Unlock()
		_ = client.Close()
	})
	return client
}

func TestLoadOpenIncidentsTrustsRedisOverLocalCache(t *testing.T) {
	client := alertingTestRedis(t)
	localMu.Lock()
	localIncidents = map[string]incidentState{
		// Resolved by another process: gone from Redis.
		"pg-dump": {Fingerprint: "x", FirstSeen: 100, LastSeen: 200, Occurrences: 5},
		// Resolved elsewhere, then recurred with the same fingerprint.
		"hourly-upload": {Fingerprint: "y", FirstSeen: 100, LastSeen: 200, Occurrences: 9},
	}
	localMu.Unlock()
	recurrence := `{"fingerprint":"y","first_seen":500,"last_seen":600,"occurrences":2}`
	if err := client.Set(context.Background(), incidentKeyPrefix+"hourly-upload", recurrence, 0).Err(); err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	open, err := loadOpenIncidents()
	if err != nil {
		t.Fatalf("loadOpenIncidents: %v", err)
	}
	if _, ok := open["pg-dump"]; ok {
		t.Fatalf("an incident resolved in Redis came back from the local cache: %+v", open)
	}
	if got := open["hourly-upload"]; got.FirstSeen != 500 || got.Occurrences != 2 {
		t.Fatalf("a recurrence must not inherit the resolved lifecycle: %+v", got)
	}
}

func TestDigestClaimSurvivesRedisOutageAfterDelivery(t *testing.T) {
	alertingTestRedis(t)
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	if !claimDigest(now) {
		t.Fatal("first claim of the day should succeed")
	}
	// Redis goes away before the 09:25 retry tick.
	down := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	defer down.Close()
	common.RDB = down
	if claimDigest(now.Add(15 * time.Minute)) {
		t.Fatal("a retry tick must not send again after a successful Redis-backed claim")
	}
}

func TestDigestClaimIsShortUntilDelivered(t *testing.T) {
	client := alertingTestRedis(t)
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	key := digestClaimKey("2026-10-20")
	if !claimDigest(now) {
		t.Fatal("first claim of the day should succeed")
	}
	// If the attempt fails and its release never reaches Redis, the hold must
	// lapse before the 09:25 retry.
	if ttl := client.TTL(context.Background(), key).Val(); ttl <= 0 || ttl > digestSendingTTL || ttl >= 15*time.Minute {
		t.Fatalf("sending claim should expire before the next retry tick, ttl=%s", ttl)
	}
	markDigestDelivered(now)
	if ttl := client.TTL(context.Background(), key).Val(); ttl < 24*time.Hour {
		t.Fatalf("a delivered digest should hold the day, ttl=%s", ttl)
	}
}

// failFirstCommands makes the first n Redis commands fail, as a brief outage.
type failFirstCommands struct{ remaining int }

func (h *failFirstCommands) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	if h.remaining > 0 {
		h.remaining--
		return ctx, errors.New("redis briefly unavailable")
	}
	return ctx, nil
}
func (h *failFirstCommands) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (h *failFirstCommands) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (h *failFirstCommands) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

func TestDigestDeliveryRecordRetriesThroughABriefOutage(t *testing.T) {
	client := alertingTestRedis(t)
	previousSleep := digestRetrySleep
	var slept []time.Duration
	digestRetrySleep = func(d time.Duration) { slept = append(slept, d) }
	t.Cleanup(func() { digestRetrySleep = previousSleep })
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	if !claimDigest(now) {
		t.Fatal("first claim of the day should succeed")
	}
	client.AddHook(&failFirstCommands{remaining: 2})
	markDigestDelivered(now)
	if got := client.Get(context.Background(), digestClaimKey("2026-10-20")).Val(); got != "sent" {
		t.Fatalf("delivery should be recorded once Redis is back, got %q after sleeping %v", got, slept)
	}
	if len(slept) != 2 {
		t.Fatalf("expected two retries, slept %v", slept)
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
