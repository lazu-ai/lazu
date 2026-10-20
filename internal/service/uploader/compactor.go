package uploader

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/service/alerting"
	"github.com/lazu-ai/lazu/internal/storage"

	"github.com/robfig/cron/v3"
)

// JSONL captures in R2 live as uncompressed .jsonl for the first 30 days
// (debug-friendly: grep, jq, paste into an editor) and then get gzipped
// in place by this compactor. Cloudflare lifecycle handles the eventual
// 180-day delete; the compactor only touches the storage shape, not the
// retention.
const (
	compactionAgeDays  = 30
	compactionMinBytes = 100 * 1024 // skip files <100KB; gzip header overhead is not worth it
	compactionMaxFiles = 500        // safety cap per run; daily cron drains backlog over multiple runs
)

var (
	compactorCron    *cron.Cron
	compactorRunning int32 // 0 idle, 1 running
)

// compactorPrefixes lists the R2 prefixes the compactor scans.
var compactorPrefixes = []string{
	"archive/requests/",
	"archive/errors/",
}

// CompactorResult is the per-run summary surfaced to Feishu / logs.
type CompactorResult struct {
	StartedAt     time.Time
	Duration      time.Duration
	Scanned       int
	Compressed    int
	SkippedRecent int
	SkippedSmall  int
	SkippedGzip   int
	Failed        int
	Err           error
}

// StartCompactorScheduler kicks off the daily archive compaction cron.
// No-op when artifact_storage S3 is not configured (cannot read/write
// the archive).
func StartCompactorScheduler() {
	cfg := common.AppConfig
	if !cfg.ArtifactStorage.S3.IsConfigured() {
		common.SysLog("compactor scheduler disabled: artifact_storage.s3 not configured")
		return
	}
	loc, err := time.LoadLocation(cfg.Upload.Timezone)
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	if compactorCron != nil {
		compactorCron.Stop()
	}
	compactorCron = cron.New(cron.WithLocation(loc))
	// Daily 06:00 — runs after pg_dump (05:30) and the hourly upload
	// (every :00). Avoids contending with either for the same R2
	// connection burst.
	_, err = compactorCron.AddFunc("0 6 * * *", func() {
		res := RunCompactorOnce(context.Background())
		if res.Err != nil {
			common.SysError(fmt.Sprintf("compactor failed: %v", res.Err))
		} else {
			common.SysLog(fmt.Sprintf("compactor ok: scanned=%d compressed=%d skipped_recent=%d skipped_small=%d skipped_gzip=%d failed=%d duration=%s",
				res.Scanned, res.Compressed, res.SkippedRecent, res.SkippedSmall, res.SkippedGzip, res.Failed,
				res.Duration.Truncate(time.Millisecond)))
		}
		NotifyCompactorResult(res)
	})
	if err != nil {
		common.SysError("failed to schedule compactor: " + err.Error())
		return
	}
	compactorCron.Start()
}

// RunCompactorOnce performs one compaction pass. Exposed so an admin
// endpoint / restore drill could trigger an immediate compaction.
func RunCompactorOnce(ctx context.Context) CompactorResult {
	res := CompactorResult{StartedAt: time.Now()}
	if !atomic.CompareAndSwapInt32(&compactorRunning, 0, 1) {
		res.Err = fmt.Errorf("compactor already running")
		return res
	}
	defer atomic.StoreInt32(&compactorRunning, 0)

	client, err := storage.NewS3(common.AppConfig.ArtifactStorage.S3)
	if err != nil {
		res.Err = fmt.Errorf("init s3 client: %w", err)
		res.Duration = time.Since(res.StartedAt)
		return res
	}

	cutoff := time.Now().AddDate(0, 0, -compactionAgeDays)

	for _, prefix := range compactorPrefixes {
		objects, err := client.ListByPrefix(ctx, prefix)
		if err != nil {
			res.Err = fmt.Errorf("list %s: %w", prefix, err)
			res.Duration = time.Since(res.StartedAt)
			return res
		}
		for _, obj := range objects {
			if res.Scanned >= compactionMaxFiles {
				common.SysLog(fmt.Sprintf("compactor: hit per-run cap %d files, deferring rest to next run", compactionMaxFiles))
				res.Duration = time.Since(res.StartedAt)
				return res
			}
			res.Scanned++
			if strings.HasSuffix(obj.Key, ".gz") {
				res.SkippedGzip++
				continue
			}
			if obj.LastModified.After(cutoff) {
				res.SkippedRecent++
				continue
			}
			if obj.Size < compactionMinBytes {
				res.SkippedSmall++
				continue
			}
			if err := compactOne(ctx, client, obj.Key); err != nil {
				common.SysError(fmt.Sprintf("compactor: failed to compact %s: %v", obj.Key, err))
				res.Failed++
				continue
			}
			res.Compressed++
		}
	}
	res.Duration = time.Since(res.StartedAt)
	return res
}

// compactOne downloads an object, gzips it, uploads as <key>.gz, and
// deletes the original. R2 has no server-side compression so the round
// trip is unavoidable.
func compactOne(ctx context.Context, client storage.ObjectStorage, key string) error {
	rc, err := client.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	defer rc.Close()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := io.Copy(gw, rc); err != nil {
		_ = gw.Close()
		return fmt.Errorf("gzip: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("gzip close: %w", err)
	}

	gzKey := key + ".gz"
	if err := client.Put(ctx, gzKey, &buf, "application/gzip"); err != nil {
		return fmt.Errorf("put %s: %w", gzKey, err)
	}
	if err := client.Delete(ctx, key); err != nil {
		// Failed delete leaves both .jsonl and .jsonl.gz in R2 — not a
		// data-loss bug, just a temporary duplicate. Next compactor run
		// skips the .gz and retries the .jsonl delete.
		return fmt.Errorf("delete original %s after upload of %s: %w", key, gzKey, err)
	}
	return nil
}

// NotifyCompactorResult records health and emits a managed alert for both
// in-process cron and hosted one-shot worker executions.
func NotifyCompactorResult(res CompactorResult) {
	if res.Err != nil {
		summary := fmt.Sprintf("scanned=%d compressed=%d failed=%d", res.Scanned, res.Compressed, res.Failed)
		alerting.RecordHealth("archive-compactor", "R2 archive compactor", "failed",
			fmt.Sprintf("错误：%v；%s", res.Err, compactorHealthSummary(res)))
		alerting.Report(alerting.Incident{
			Key:         "archive-compactor",
			Fingerprint: compactorErrorFingerprint(res.Err),
			Priority:    alerting.PriorityMaintenance,
			Message: fmt.Sprintf("❌ Lazu compactor FAILED\nerror: %v\n%s",
				res.Err, summary),
		})
		return
	}
	if res.Failed > 0 {
		summary := fmt.Sprintf("scanned=%d compressed=%d failed=%d", res.Scanned, res.Compressed, res.Failed)
		alerting.RecordHealth("archive-compactor", "R2 archive compactor", "failed", compactorHealthSummary(res))
		alerting.Report(alerting.Incident{
			Key:         "archive-compactor",
			Fingerprint: "compactor-object-failures",
			Priority:    alerting.PriorityMaintenance,
			Message: fmt.Sprintf("⚠️ Lazu compactor finished with failures\n%s\nduration=%s",
				summary, res.Duration.Truncate(time.Millisecond)),
		})
		return
	}
	alerting.RecordHealth("archive-compactor", "R2 archive compactor", "ok", compactorHealthSummary(res))
	alerting.Resolve("archive-compactor", "✅ Lazu archive compactor recovered")
}

func compactorHealthSummary(res CompactorResult) string {
	return fmt.Sprintf("检查 %d 个文件，压缩 %d 个，失败 %d 个", res.Scanned, res.Compressed, res.Failed)
}

func compactorErrorFingerprint(err error) string {
	if err == nil {
		return "compactor-run-error"
	}
	return "compactor-run-error:" + normalizeUploadIssue(err.Error())
}
