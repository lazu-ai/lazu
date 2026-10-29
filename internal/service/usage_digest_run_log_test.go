package service

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

// captureSysLog routes SysLog and SysWarn/SysError output to buffers at info
// level for one test.
func captureSysLog(t *testing.T) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	oldOut, oldErr, oldLevel := gin.DefaultWriter, gin.DefaultErrorWriter, common.LogLevel
	var out, errOut bytes.Buffer
	gin.DefaultWriter, gin.DefaultErrorWriter, common.LogLevel = &out, &errOut, common.LogLevelInfo
	t.Cleanup(func() { gin.DefaultWriter, gin.DefaultErrorWriter, common.LogLevel = oldOut, oldErr, oldLevel })
	return &out, &errOut
}

func TestUsageDigestRunLogsOneSummaryPerPeriod(t *testing.T) {
	core := setupSpendBudgetServiceTestDB(t)
	if err := core.AutoMigrate(&model.Ability{}, &model.Channel{}); err != nil {
		t.Fatal(err)
	}
	telemetry, err := gorm.Open(gormsqlite.Open("file:digest-run-log?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetTelemetryDB(telemetry)
	t.Cleanup(func() { raw, _ := telemetry.DB(); _ = raw.Close() })
	if err := telemetry.AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}
	user := model.User{Id: 7, Username: "digest-run-log", Email: "reader@example.test", Status: common.UserStatusEnabled, HasToppedUp: true, Setting: `{"timezone":"UTC","language":"en"}`}
	if err := core.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	oldOptions, oldEnabled := common.OptionMap, common.UsageDigestEnabled
	// The rollup is complete and the minimum is far above the reader's (zero)
	// spend, so the one due digest is claimed as skipped and nothing is sent.
	common.OptionMap = map[string]string{model.DigestRollupSinceOption: "1", DigestMinSpendOption: "1000"}
	common.UsageDigestEnabled = true
	t.Cleanup(func() { common.OptionMap, common.UsageDigestEnabled = oldOptions, oldEnabled })
	out, errOut := captureSysLog(t)

	// A Monday, half an hour into the weekly window; the monthly window
	// (the 1st plus two days of grace) is long past.
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	if err := RunUsageDigests(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	want := "usage digests weekly 2026-W41: sent 0, skipped 1 (below_minimum 1)"
	if got := out.String(); strings.Count(got, want) != 1 || strings.Contains(got, "monthly") {
		t.Fatalf("first run log:\n%s\nwant one line %q", got, want)
	}
	if got := errOut.String(); strings.Contains(got, "usage digests") {
		t.Fatalf("a complete run raised a warning or error: %s", got)
	}

	// The next run finds the period claimed and has nothing to report.
	out.Reset()
	if err := RunUsageDigests(context.Background(), now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Contains(got, "usage digests") {
		t.Fatalf("second run logged a summary for an already claimed period:\n%s", got)
	}
	var count int64
	if err := telemetry.Model(&model.MailDelivery{}).Where("status = ? AND error = ?", model.MailStatusSkipped, digestSkipBelowMinimum).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("skipped rows=%d err=%v", count, err)
	}
}

func TestUsageDigestRunSaysOnceWhyItDidNotRun(t *testing.T) {
	oldEnabled := common.UsageDigestEnabled
	common.UsageDigestEnabled = false
	digestRunSkipLogged = sync.Once{}
	t.Cleanup(func() { common.UsageDigestEnabled = oldEnabled; digestRunSkipLogged = sync.Once{} })
	out, errOut := captureSysLog(t)

	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	for range 3 {
		if err := RunUsageDigests(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	want := "usage digests: run skipped, usage digests are disabled"
	if got := errOut.String(); strings.Count(got, want) != 1 {
		t.Fatalf("disabled notice:\n%s\nwant exactly one %q", got, want)
	}
	if got := out.String(); strings.Contains(got, "usage digests") {
		t.Fatalf("a skipped run logged a summary: %s", got)
	}
}
