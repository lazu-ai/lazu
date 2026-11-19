package worker

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"gorm.io/gorm"
)

func upstreamWatchDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.RoutingAttemptOutcome{}, &model.Channel{}, &model.Log{}); err != nil {
		t.Fatal(err)
	}
	oldCore, oldTelemetry := model.CoreDB(), model.ConfiguredTelemetryDB()
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	t.Cleanup(func() {
		model.SetCoreDB(oldCore)
		model.SetTelemetryDB(oldTelemetry)
		raw, _ := db.DB()
		_ = raw.Close()
	})
	return db
}

func seedAttempt(t *testing.T, db *gorm.DB, id string, channel int, code string, status int, at time.Time) {
	t.Helper()
	row := model.RoutingAttemptOutcome{RequestTS: at.UnixMilli(), RequestID: id, OutcomeAt: at, ChannelID: channel, StatusCode: status, ErrorCode: code}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}

func TestRecentUpstreamAccountFailuresCountsPerChannelAndCode(t *testing.T) {
	db := upstreamWatchDB(t)
	now := time.Now()
	for i := 0; i < 4; i++ {
		seedAttempt(t, db, fmt.Sprintf("req_b%d", i), 65, "upstream_billing_failed", 402, now.Add(-time.Duration(i)*time.Minute))
	}
	seedAttempt(t, db, "req_old", 65, "upstream_billing_failed", 402, now.Add(-2*time.Hour))
	seedAttempt(t, db, "req_rl", 65, "upstream_rate_limited", 429, now)
	seedAttempt(t, db, "req_k", 9, "channel:invalid_key", 401, now)

	rows, err := model.RecentUpstreamAccountFailures(context.Background(), now.Add(-upstreamAccountWindow), []string{"upstream_billing_failed", "channel:invalid_key"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[fmt.Sprintf("%d/%s/%d", r.ChannelID, r.ErrorCode, r.StatusCode)] = r.Failures
	}
	if got["65/upstream_billing_failed/402"] != 4 || got["9/channel:invalid_key/401"] != 1 || len(got) != 2 {
		t.Fatalf("failures = %v", got)
	}
}

// A failure nothing retried is only in the error log; the watch reads it
// there, by the code stored as the row's reason.
func TestRecentUpstreamAccountFinalFailuresReadsErrorLog(t *testing.T) {
	db := upstreamWatchDB(t)
	now := time.Now()
	seed := func(id string, channel int, logType int, reason string, at time.Time) {
		t.Helper()
		row := model.Log{RequestTS: at.UnixMilli(), RequestID: id, CreatedAt: at.Unix(), Type: logType, ChannelId: channel, RejectionReason: reason}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		seed(fmt.Sprintf("req_f%d", i), 41, model.LogTypeError, "upstream_billing_failed", now.Add(-time.Duration(i)*time.Minute))
	}
	seed("req_f_old", 41, model.LogTypeError, "upstream_billing_failed", now.Add(-2*time.Hour))
	seed("req_f_rl", 41, model.LogTypeError, "upstream_rate_limited", now)
	seed("req_f_local", 0, model.LogTypeError, "upstream_billing_failed", now)
	seed("req_f_ok", 41, model.LogTypeConsume, "upstream_billing_failed", now)

	rows, err := model.RecentUpstreamAccountFinalFailures(context.Background(), now.Add(-upstreamAccountWindow), []string{"upstream_billing_failed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ChannelID != 41 || rows[0].ErrorCode != "upstream_billing_failed" || rows[0].Failures != 3 || rows[0].StatusCode != 0 {
		t.Fatalf("final failures = %+v", rows)
	}
	if rows[0].LastMs < now.Add(-time.Second).UnixMilli() {
		t.Fatalf("last failure = %d, want the newest row", rows[0].LastMs)
	}
}

// With Redis (CI sets REDIS_CONN_STRING; locally any reachable Redis in a
// spare database), a channel past the threshold is reported once, through
// the ops alert path, with its name; retried attempts and final failures
// count together.
func TestUpstreamAccountWatchAlertsOncePerChannel(t *testing.T) {
	conn := os.Getenv("REDIS_CONN_STRING")
	if conn == "" {
		t.Skip("REDIS_CONN_STRING is not set")
	}
	opt, err := redis.ParseURL(conn)
	if err != nil {
		t.Skipf("invalid REDIS_CONN_STRING: %v", err)
	}
	client := redis.NewClient(opt)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis unreachable: %v", err)
	}
	oldRDB, oldEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() { common.RDB, common.RedisEnabled = oldRDB, oldEnabled })
	for _, lease := range []string{"upstream-account:计费失败:65", "upstream-account:鉴权失败:9"} {
		_ = client.Del(context.Background(), hostedTaskLeasePrefix+lease).Err()
		t.Cleanup(func() { _ = client.Del(context.Background(), hostedTaskLeasePrefix+lease).Err() })
	}

	db := upstreamWatchDB(t)
	if err := db.Create(&model.Channel{Id: 65, Name: "Example Gemini", Group: "direct"}).Error; err != nil {
		t.Fatal(err)
	}
	setting := email_setting.GetEmailSetting()
	oldAdmin := setting.AdminEmail
	setting.AdminEmail = ""
	t.Cleanup(func() { setting.AdminEmail = oldAdmin })

	var alerts []common.OpsAlert
	common.RegisterOpsAlertSink(func(a common.OpsAlert) {
		if strings.HasPrefix(a.Subject, "[Lazu ops] 上游渠道") {
			alerts = append(alerts, a)
		}
	})

	now := time.Now()
	for i := 0; i < upstreamAccountThreshold; i++ {
		seedAttempt(t, db, fmt.Sprintf("req_bill%d", i), 65, "upstream_billing_failed", 402, now.Add(-time.Duration(i)*time.Minute))
	}
	seedAttempt(t, db, "req_key0", 9, "channel:invalid_key", 401, now)
	seedAttempt(t, db, "req_key1", 9, "upstream_auth_failed", 401, now)
	// The third key failure was nobody's retry: it is only in the error log.
	final := model.Log{RequestTS: now.UnixMilli(), RequestID: "req_key2", CreatedAt: now.Unix(), Type: model.LogTypeError, ChannelId: 9, RejectionReason: "upstream_auth_failed"}
	if err := db.Create(&final).Error; err != nil {
		t.Fatal(err)
	}

	task := NewUpstreamAccountWatchTask()
	if err := task.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := task.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 {
		t.Fatalf("alerts = %+v", alerts)
	}
	var a, key common.OpsAlert
	for _, alert := range alerts {
		if strings.Contains(alert.Subject, "计费失败") {
			a = alert
		} else {
			key = alert
		}
	}
	if key.Subject != "[Lazu ops] 上游渠道鉴权失败：渠道 #9" || !strings.Contains(key.Text, "近 15 分钟失败: 3 次") || !strings.Contains(key.Text, "上游状态码: 401") {
		t.Fatalf("key alert = %+v", key)
	}
	if a.Subject != "[Lazu ops] 上游渠道计费失败：Example Gemini" || !strings.Contains(a.Text, "近 15 分钟失败: 3 次") || !strings.Contains(a.Text, "线路: direct") {
		t.Fatalf("alert = %+v", a)
	}
}
