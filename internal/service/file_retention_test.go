package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestStartFileRetentionIsNotMasterGated(t *testing.T) {
	previousMasterNode := common.IsMasterNode
	previousRunner := fileRetentionRunner
	common.IsMasterNode = false
	fileRetentionOnce = sync.Once{}
	started := make(chan struct{})
	fileRetentionRunner = func() {
		close(started)
	}
	t.Cleanup(func() {
		common.IsMasterNode = previousMasterNode
		fileRetentionRunner = previousRunner
		fileRetentionOnce = sync.Once{}
	})

	StartFileRetention()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected file retention to start for an API replica")
	}
}

func TestParseRotatingDailyFilenameTime(t *testing.T) {
	got, ok := parseRequestsLogTime("requests-2026-02-01.jsonl")
	if !ok {
		t.Fatal("expected requests log filename to be parsed")
	}
	expectedDay := time.Date(2026, 2, 1, 23, 59, 59, int(time.Second-time.Nanosecond), time.Local)
	if !got.Equal(expectedDay) {
		t.Fatalf("unexpected parsed time, got=%v want=%v", got, expectedDay)
	}

	got, ok = parseErrorsLogTime("errors-2026-02-01-3.jsonl")
	if !ok {
		t.Fatal("expected errors log filename with index to be parsed")
	}
	if !got.Equal(expectedDay) {
		t.Fatalf("unexpected parsed time with index, got=%v want=%v", got, expectedDay)
	}

	if _, ok = parseRequestsLogTime("requests-2026-02-01-bad.jsonl"); ok {
		t.Fatal("expected invalid requests log filename to fail parsing")
	}

	// Legacy filenames should still parse so retention can sweep up the
	// pre-rename artifacts.
	if _, ok = parseLegacyModelResponseLogTime("model-responses-2026-02-01.jsonl"); !ok {
		t.Fatal("expected legacy model-responses filename to be parsed")
	}
	if _, ok = parseLegacyErrorLogTime("error-logs-2026-02-01.jsonl"); !ok {
		t.Fatal("expected legacy error-logs filename to be parsed")
	}

	customGot, customOK := parseRotatingDailyFilenameTime("my-model-2026-02-03-2.txt", "my-model", ".txt")
	if !customOK {
		t.Fatal("expected custom rotating filename to be parsed")
	}
	customExpected := time.Date(2026, 2, 3, 23, 59, 59, int(time.Second-time.Nanosecond), time.Local)
	if !customGot.Equal(customExpected) {
		t.Fatalf("unexpected custom parsed time, got=%v want=%v", customGot, customExpected)
	}
}

func TestParseAppLogTime(t *testing.T) {
	got, ok := parseAppLogTime("lazu-20260304112233.log")
	if !ok {
		t.Fatal("expected app log filename to be parsed")
	}
	expected := time.Date(2026, 3, 4, 11, 22, 33, 0, time.Local)
	if !got.Equal(expected) {
		t.Fatalf("unexpected app log parsed time, got=%v want=%v", got, expected)
	}

	got, ok = parseAppLogTime("lazu-20260304112233-2.log")
	if !ok {
		t.Fatal("expected app log filename with suffix to be parsed")
	}
	if !got.Equal(expected) {
		t.Fatalf("unexpected app log parsed time with suffix, got=%v want=%v", got, expected)
	}

	if _, ok = parseAppLogTime("lazu-20260304.log"); ok {
		t.Fatal("expected short app log filename to fail parsing")
	}
}

func TestCleanupFilesByTargets(t *testing.T) {
	tmpDir := t.TempDir()
	exportDir := filepath.Join(tmpDir, "export")
	logsDir := filepath.Join(tmpDir, "logs")

	now := time.Now()
	oldDay := now.AddDate(0, 0, -45)
	recentDay := now.AddDate(0, 0, -5)

	oldByName := filepath.Join(exportDir, fmt.Sprintf("requests-%s.jsonl", oldDay.Format("2006-01-02")))
	recentByName := filepath.Join(exportDir, fmt.Sprintf("requests-%s.jsonl", recentDay.Format("2006-01-02")))
	oldByModTime := filepath.Join(exportDir, "requests-invalid.jsonl")
	oldAppLog := filepath.Join(logsDir, fmt.Sprintf("lazu-%s.log", oldDay.Format("20060102150405")))
	recentAppLog := filepath.Join(logsDir, fmt.Sprintf("lazu-%s.log", recentDay.Format("20060102150405")))

	mustCreateTestFile(t, oldByName, now)
	mustCreateTestFile(t, recentByName, now)
	mustCreateTestFile(t, oldByModTime, oldDay)
	mustCreateTestFile(t, oldAppLog, now)
	mustCreateTestFile(t, recentAppLog, now)

	targets := []fileRetentionTarget{
		{
			globPattern:        filepath.Join(exportDir, "requests-*.jsonl"),
			parseReferenceTime: parseRequestsLogTime,
			retentionDays:      30,
		},
		{
			globPattern:        filepath.Join(logsDir, "lazu-*.log"),
			parseReferenceTime: parseAppLogTime,
			retentionDays:      30,
		},
	}

	stats := cleanupFilesByTargets(now, targets)

	if stats.scanned != 5 {
		t.Fatalf("unexpected scanned files count, got=%d want=%d", stats.scanned, 5)
	}
	if stats.deleted != 3 {
		t.Fatalf("unexpected deleted files count, got=%d want=%d", stats.deleted, 3)
	}
	if stats.failed != 0 {
		t.Fatalf("unexpected failed count, got=%d want=%d", stats.failed, 0)
	}

	assertFileExists(t, recentByName)
	assertFileExists(t, recentAppLog)

	assertFileNotExists(t, oldByName)
	assertFileNotExists(t, oldByModTime)
	assertFileNotExists(t, oldAppLog)
}

func TestCleanupFilesByTargetsDeduplicatesMatches(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "model-responses-2025-01-01.jsonl")
	mustCreateTestFile(t, path, time.Now())

	// Both targets share the same glob so the same path appears twice;
	// the dedupe set should still only scan/delete once.
	targets := []fileRetentionTarget{
		{
			globPattern:        filepath.Join(tmpDir, "model-responses-*.jsonl"),
			parseReferenceTime: nil,
			retentionDays:      1, // forces cutoff in the past so file is "old"
		},
		{
			globPattern:        filepath.Join(tmpDir, "model-responses-*.jsonl"),
			parseReferenceTime: nil,
			retentionDays:      1,
		},
	}
	// Now is +1h ahead of the test file mtime → file is "old" relative
	// to the 1-day retention window.
	now := time.Now().Add(48 * time.Hour)
	stats := cleanupFilesByTargets(now, targets)

	if stats.scanned != 1 {
		t.Fatalf("unexpected scanned count with duplicate targets, got=%d want=1", stats.scanned)
	}
	if stats.deleted != 1 {
		t.Fatalf("unexpected deleted count with duplicate targets, got=%d want=1", stats.deleted)
	}
	if stats.failed != 0 {
		t.Fatalf("unexpected failed count with duplicate targets, got=%d want=0", stats.failed)
	}
}

func TestJSONLRetentionLeavesSevenDayCredentialRecoveryWindow(t *testing.T) {
	tmpDir := t.TempDir()
	now := time.Now()
	recoverable := filepath.Join(tmpDir, "requests-recoverable.jsonl")
	expired := filepath.Join(tmpDir, "requests-expired.jsonl")
	mustCreateTestFile(t, recoverable, now.Add(-2*24*time.Hour))
	mustCreateTestFile(t, expired, now.Add(-8*24*time.Hour))

	stats := cleanupFilesByTargets(now, []fileRetentionTarget{{
		globPattern:   filepath.Join(tmpDir, "requests-*.jsonl"),
		retentionDays: jsonlRetentionDays,
	}})

	if stats.deleted != 1 || stats.failed != 0 {
		t.Fatalf("unexpected retention stats: %+v", stats)
	}
	assertFileExists(t, recoverable)
	assertFileNotExists(t, expired)
}

func TestDefaultFileRetentionTargetsHonorConfiguredPaths(t *testing.T) {
	oldModelPath := common.ModelLogPath
	oldErrorPath := common.ErrorLogPath
	oldLogDir := common.LogDir
	oldDataDir := common.AppConfig.Data.Dir
	oldExportSub := common.AppConfig.Data.ExportSubDir
	oldLogsSub := common.AppConfig.Data.LogsSubDir

	customLogDir := filepath.Join(t.TempDir(), "custom-logs")
	common.ModelLogPath = "/tmp/custom-retention/model-events.jsonl"
	common.ErrorLogPath = "/tmp/custom-retention/error-events.jsonl"
	common.LogDir = &customLogDir
	common.AppConfig.Data.Dir = "/tmp/retention-data"
	common.AppConfig.Data.ExportSubDir = "export"
	common.AppConfig.Data.LogsSubDir = "logs"

	t.Cleanup(func() {
		common.ModelLogPath = oldModelPath
		common.ErrorLogPath = oldErrorPath
		common.LogDir = oldLogDir
		common.AppConfig.Data.Dir = oldDataDir
		common.AppConfig.Data.ExportSubDir = oldExportSub
		common.AppConfig.Data.LogsSubDir = oldLogsSub
	})

	targets := defaultFileRetentionTargets()
	patternSet := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		patternSet[target.globPattern] = struct{}{}
	}

	if _, ok := patternSet["/tmp/custom-retention/model-events-*.jsonl"]; !ok {
		t.Fatalf("expected model log target for configured path, got patterns=%v", patternSet)
	}
	if _, ok := patternSet["/tmp/custom-retention/error-events-*.jsonl"]; !ok {
		t.Fatalf("expected error log target for configured path, got patterns=%v", patternSet)
	}
	if _, ok := patternSet[filepath.Join(customLogDir, "lazu-*.log")]; !ok {
		t.Fatalf("expected app log target to use configured log dir, got patterns=%v", patternSet)
	}
}

func mustCreateTestFile(t *testing.T, path string, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create parent directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to write test file %s: %v", path, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("failed to set mtime for %s: %v", path, err)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist %s: %v", path, err)
	}
}

func assertFileNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted %s, stat err=%v", path, err)
	}
}
