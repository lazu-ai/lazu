package uploader

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/storage"
)

func TestUploadDataFolderWithOptions_RejectsConcurrentRun(t *testing.T) {
	atomic.StoreInt32(&uploadRunning, 1)
	defer atomic.StoreInt32(&uploadRunning, 0)

	if _, err := UploadDataFolderWithOptions(UploadOptions{}); err == nil {
		t.Fatal("expected concurrent upload to be rejected")
	}
}

func TestContainsToday(t *testing.T) {
	today := rotatingLogDate(time.Now())
	if !containsToday("model-responses-" + today + ".jsonl") {
		t.Fatal("expected today's filename to match")
	}
	if containsToday("model-responses-2001-01-01.jsonl") {
		t.Fatal("did not expect old filename to match")
	}
}

func TestIsTodayLogFilename(t *testing.T) {
	today := time.Now().Format("20060102")
	if !isTodayLogFilename("lazu-" + today + "010203.log") {
		t.Fatal("expected today's log filename to match")
	}
	if isTodayLogFilename("lazu-20000101010203.log") {
		t.Fatal("did not expect historical log filename to match")
	}
	if isTodayLogFilename("app-" + today + ".log") {
		t.Fatal("did not expect wrong prefix to match")
	}
}

func TestHumanSize(t *testing.T) {
	if got := humanSize(512); got != "512 B" {
		t.Fatalf("expected 512 B, got %s", got)
	}
	if got := humanSize(1024); got != "1.0 KiB" {
		t.Fatalf("expected 1.0 KiB, got %s", got)
	}
}

func TestCountLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.jsonl")
	content := "line-1\nline-2\nline-3\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	count, err := countLines(path)
	if err != nil {
		t.Fatalf("countLines returned error: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 lines, got %d", count)
	}
}

func TestUploadDataFolderWithOptions_SkipsAllCurrentDayJSONLShards(t *testing.T) {
	dataDir, exportDir, fake := configureUploadShardTest(t, nil)
	today := rotatingLogDate(time.Now())
	closedPath := filepath.Join(exportDir, "requests-"+today+".jsonl")
	currentPath := filepath.Join(exportDir, "requests-"+today+"-2.jsonl")
	writeOldFile(t, closedPath, []byte("{\"closed\":true}\n"))
	writeOldFile(t, currentPath, []byte("{\"current\":true}\n"))
	common.ModelLogPath = filepath.Join(exportDir, "requests.jsonl")
	common.ErrorLogPath = filepath.Join(exportDir, "errors.jsonl")
	common.AppConfig.Data.Dir = dataDir

	summary, err := UploadDataFolderWithOptions(UploadOptions{})
	if err != nil {
		t.Fatalf("UploadDataFolderWithOptions returned error: %v", err)
	}
	if len(summary.Failed) != 0 {
		t.Fatalf("expected no failed uploads, got %v", summary.Failed)
	}
	if len(summary.Uploaded) != 0 || len(fake.uploaded) != 0 {
		t.Fatalf("expected all current-day shards to be skipped, summary=%+v fake=%+v", summary.Uploaded, fake.uploaded)
	}
	if _, err := os.Stat(closedPath); err != nil {
		t.Fatalf("expected current-day shard to remain local, got %v", err)
	}
	if _, err := os.Stat(currentPath); err != nil {
		t.Fatalf("expected current writer shard to remain local, got %v", err)
	}
}

func TestUploadDataFolderWithOptions_SkipsCurrentDayShardBeforeUploadFailure(t *testing.T) {
	dataDir, exportDir, _ := configureUploadShardTest(t, errors.New("storage unavailable"))
	today := rotatingLogDate(time.Now())
	closedPath := filepath.Join(exportDir, "requests-"+today+".jsonl")
	currentPath := filepath.Join(exportDir, "requests-"+today+"-2.jsonl")
	writeOldFile(t, closedPath, []byte("{\"closed\":true}\n"))
	writeOldFile(t, currentPath, []byte("{\"current\":true}\n"))
	common.ModelLogPath = filepath.Join(exportDir, "requests.jsonl")
	common.ErrorLogPath = filepath.Join(exportDir, "errors.jsonl")
	common.AppConfig.Data.Dir = dataDir

	summary, err := UploadDataFolderWithOptions(UploadOptions{})
	if err != nil {
		t.Fatalf("UploadDataFolderWithOptions returned error: %v", err)
	}
	if len(summary.Uploaded) != 0 {
		t.Fatalf("expected no uploaded files, got %+v", summary.Uploaded)
	}
	if len(summary.Failed) != 0 {
		t.Fatalf("expected current-day shard to be skipped before upload, got failed=%v", summary.Failed)
	}
	if _, err := os.Stat(closedPath); err != nil {
		t.Fatalf("expected current-day shard to remain local, got %v", err)
	}
}

func TestUploadDataFolderWithOptions_SkipsCurrentDayJSONLWhenLogPathsUnset(t *testing.T) {
	dataDir, exportDir, fake := configureUploadShardTest(t, nil)
	today := rotatingLogDate(time.Now())
	currentPath := filepath.Join(exportDir, "requests-"+today+".jsonl")
	writeOldFile(t, currentPath, []byte("{\"current\":true}\n"))
	common.ModelLogPath = ""
	common.ErrorLogPath = ""
	common.AppConfig.Data.Dir = dataDir

	summary, err := UploadDataFolderWithOptions(UploadOptions{})
	if err != nil {
		t.Fatalf("UploadDataFolderWithOptions returned error: %v", err)
	}
	if len(summary.Uploaded) != 0 || len(fake.uploaded) != 0 {
		t.Fatalf("expected unset log paths to conservatively skip current-day JSONL, summary=%+v fake=%+v", summary.Uploaded, fake.uploaded)
	}
	if _, err := os.Stat(currentPath); err != nil {
		t.Fatalf("expected current-day JSONL to remain local, got %v", err)
	}
}

func TestUploadDataFolderWithOptions_LargeActiveCurrentShardIsNotAWarning(t *testing.T) {
	dataDir, exportDir, fake := configureUploadShardTest(t, nil)
	today := rotatingLogDate(time.Now())
	currentPath := filepath.Join(exportDir, "requests-"+today+".jsonl")
	writeOldFile(t, currentPath, []byte("{}\n"))
	if err := os.Truncate(currentPath, 128<<20); err != nil {
		t.Fatalf("failed to enlarge active shard: %v", err)
	}
	old := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(currentPath, old, old); err != nil {
		t.Fatalf("failed to backdate active shard: %v", err)
	}
	common.ModelLogPath = filepath.Join(exportDir, "requests.jsonl")
	common.ErrorLogPath = filepath.Join(exportDir, "errors.jsonl")
	common.AppConfig.Data.Dir = dataDir

	summary, err := UploadDataFolderWithOptions(UploadOptions{})
	if err != nil {
		t.Fatalf("UploadDataFolderWithOptions returned error: %v", err)
	}
	if len(summary.Uploaded) != 0 || len(fake.uploaded) != 0 {
		t.Fatalf("expected active shard to be skipped, summary=%+v fake=%+v", summary.Uploaded, fake.uploaded)
	}
	for _, warning := range summary.Warnings {
		if strings.Contains(warning, "active export JSONL") {
			t.Fatalf("expected normal writer rotation to stay quiet, got %v", summary.Warnings)
		}
	}
	if _, err := os.Stat(currentPath); err != nil {
		t.Fatalf("expected active shard to remain local, got %v", err)
	}
}

func TestUploadDataFolderWithOptions_SkipsVolumeOwnershipMarker(t *testing.T) {
	dataDir, _, fake := configureUploadShardTest(t, nil)
	markerPath := filepath.Join(dataDir, ".lazu-owned-v1")
	writeOldFile(t, markerPath, []byte("owned\n"))

	summary, err := UploadDataFolderWithOptions(UploadOptions{})
	if err != nil {
		t.Fatalf("UploadDataFolderWithOptions returned error: %v", err)
	}
	if len(summary.Uploaded) != 0 || len(summary.Failed) != 0 || len(fake.uploaded) != 0 {
		t.Fatalf("expected ownership marker to be ignored, summary=%+v fake=%+v", summary, fake.uploaded)
	}
}

func TestRunPgDumpOnceRejectsConcurrentRun(t *testing.T) {
	atomic.StoreInt32(&pgDumpRunning, 1)
	defer atomic.StoreInt32(&pgDumpRunning, 0)

	res := RunPgDumpOnce(context.Background(), "postgres://user:pass@example.com/lazu")
	if res.Reason != "skipped: already running" {
		t.Fatalf("expected concurrent pg_dump to be skipped, got reason %q", res.Reason)
	}
	if res.Err != nil {
		t.Fatalf("expected no error for skipped pg_dump, got %v", res.Err)
	}
}

func TestShouldSendUploadEmailOnlyForActionableResults(t *testing.T) {
	if shouldSendUploadEmail(&UploadSummary{Failed: map[string]string{}}, nil) {
		t.Fatal("healthy upload should be included in the daily digest instead of sending an email")
	}
	if !shouldSendUploadEmail(&UploadSummary{Failed: map[string]string{"file": "failed"}}, nil) {
		t.Fatal("failed upload should send an email")
	}
	if !shouldSendUploadEmail(&UploadSummary{Warnings: []string{"disk usage high"}}, nil) {
		t.Fatal("warning upload should send an email")
	}
	if !shouldSendUploadEmail(nil, errors.New("upload failed")) {
		t.Fatal("top-level upload error should send an email")
	}
}

func TestScheduledUploadEmailFallsBackWhenFeishuIsDisabled(t *testing.T) {
	healthy := &UploadSummary{Failed: map[string]string{}}
	if !shouldSendScheduledUploadEmail(healthy, nil, false) {
		t.Fatal("healthy scheduled upload should retain its email heartbeat when Feishu is disabled")
	}
	if shouldSendScheduledUploadEmail(healthy, nil, true) {
		t.Fatal("healthy scheduled upload should use the daily digest when Feishu is configured")
	}
}

func configureUploadShardTest(t *testing.T, putErr error) (string, string, *fakeObjectStorage) {
	t.Helper()

	oldConfig := common.AppConfig
	oldModelLogPath := common.ModelLogPath
	oldErrorLogPath := common.ErrorLogPath
	oldNewStorageClient := newStorageClient
	atomic.StoreInt32(&uploadRunning, 0)
	t.Cleanup(func() {
		common.AppConfig = oldConfig
		common.ModelLogPath = oldModelLogPath
		common.ErrorLogPath = oldErrorLogPath
		newStorageClient = oldNewStorageClient
		atomic.StoreInt32(&uploadRunning, 0)
	})

	dataDir := t.TempDir()
	exportDir := filepath.Join(dataDir, "export")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		t.Fatalf("failed to create export dir: %v", err)
	}
	common.AppConfig.Data.Dir = dataDir
	common.AppConfig.Data.ExportSubDir = "export"
	common.AppConfig.Data.LogsSubDir = "logs"
	common.AppConfig.Upload.DeleteAfterVerify = true
	common.AppConfig.Upload.VerifyViaPublic = false
	common.AppConfig.Upload.PublicBaseURL = ""
	common.AppConfig.Upload.Email.Enable = false

	fake := &fakeObjectStorage{uploaded: map[string]string{}, putErr: putErr}
	newStorageClient = func(storage.S3Config) (storage.ObjectStorage, error) {
		return fake, nil
	}
	return dataDir, exportDir, fake
}

func writeOldFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
	old := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("failed to backdate %s: %v", path, err)
	}
}

type fakeObjectStorage struct {
	uploaded map[string]string
	putErr   error
}

func (f *fakeObjectStorage) Put(context.Context, string, io.Reader, string) error {
	return nil
}

func (f *fakeObjectStorage) PutFromFile(_ context.Context, key string, filePath string) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.uploaded[key] = filePath
	return nil
}

func (f *fakeObjectStorage) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeObjectStorage) Head(_ context.Context, key string) (storage.ObjectMeta, error) {
	_, ok := f.uploaded[key]
	return storage.ObjectMeta{Exists: ok}, nil
}

func (f *fakeObjectStorage) Delete(context.Context, string) error {
	return nil
}

func (f *fakeObjectStorage) ListByPrefix(context.Context, string) ([]storage.ObjectInfo, error) {
	return nil, nil
}

func TestDatabaseNameFromDSN(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "extracts path database",
			dsn:  "postgres://user:pass@example.com:5432/lazu?sslmode=require",
			want: "lazu",
		},
		{
			name: "defaults empty path",
			dsn:  "postgresql://user:pass@example.com",
			want: "lazu",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := databaseNameFromDSN(tc.dsn)
			if err != nil {
				t.Fatalf("databaseNameFromDSN returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("expected short string unchanged, got %q", got)
	}
	if got := truncate("abcdefgh", 3); got != "abc…" {
		t.Fatalf("expected truncated string with ellipsis, got %q", got)
	}
}

func TestRemoteKeyForUpload(t *testing.T) {
	cases := []struct {
		name       string
		relative   string
		isJSONL    bool
		wantPrefix string
		wantSuffix string
	}{
		// New filename shapes (post-rename).
		{
			name:       "requests jsonl lands under archive/requests with yyyy-mm",
			relative:   "export/requests-2026-05-24.jsonl",
			isJSONL:    true,
			wantPrefix: "archive/requests/2026-05/",
			wantSuffix: "requests-2026-05-24.jsonl",
		},
		{
			name:       "errors jsonl lands under archive/errors with yyyy-mm",
			relative:   "export/errors-2026-12-31.jsonl",
			isJSONL:    true,
			wantPrefix: "archive/errors/2026-12/",
			wantSuffix: "errors-2026-12-31.jsonl",
		},
		// Legacy filename shapes still get routed cleanly so a deploy
		// across the rename window does not orphan in-flight files.
		{
			name:       "legacy model-responses-* still routes to archive/requests",
			relative:   "export/model-responses-2026-05-24.jsonl",
			isJSONL:    true,
			wantPrefix: "archive/requests/2026-05/",
			wantSuffix: "model-responses-2026-05-24.jsonl",
		},
		{
			name:       "legacy error-logs-* still routes to archive/errors",
			relative:   "export/error-logs-2026-05-24.jsonl",
			isJSONL:    true,
			wantPrefix: "archive/errors/2026-05/",
			wantSuffix: "error-logs-2026-05-24.jsonl",
		},
		{
			name:       "unrecognized jsonl name falls back to archive/misc",
			relative:   "export/something-else-2026-05-24.jsonl",
			isJSONL:    true,
			wantPrefix: "archive/misc/2026-05/",
			wantSuffix: "something-else-2026-05-24.jsonl",
		},
		{
			name:       "non-jsonl files keep their original key",
			relative:   "logs/lazu-20260524023854.log",
			isJSONL:    false,
			wantPrefix: "logs/",
			wantSuffix: "lazu-20260524023854.log",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := remoteKeyForUpload(tc.relative, tc.isJSONL)
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("prefix: got %q want prefix %q", got, tc.wantPrefix)
			}
			if !strings.HasSuffix(got, tc.wantSuffix) {
				t.Errorf("suffix: got %q want suffix %q", got, tc.wantSuffix)
			}
		})
	}
}

func TestHeadPublicWithSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.URL.Path, "/") == "exists.txt" {
			w.Header().Set("Content-Length", "12")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	previousClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = previousClient }()

	exists, size := headPublicWithSize(server.URL, "exists.txt")
	if !exists || size != 12 {
		t.Fatalf("expected exists=true size=12, got exists=%v size=%d", exists, size)
	}

	exists, size = headPublicWithSize(server.URL, "missing.txt")
	if exists || size != 0 {
		t.Fatalf("expected missing file to return exists=false size=0, got exists=%v size=%d", exists, size)
	}
}
