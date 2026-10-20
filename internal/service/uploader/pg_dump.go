package uploader

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/storage"

	"github.com/robfig/cron/v3"
)

// pgDumpCron is a dedicated cron instance so its lifecycle is independent
// from the file-upload scheduler (different schedule, different failure
// surface, different metric).
var pgDumpCron *cron.Cron
var pgDumpRunning int32 // 0 idle, 1 running

// PgDumpResult captures the outcome of a single dump job for downstream
// notification / logging hooks.
type PgDumpResult struct {
	Key        string
	SizeBytes  int64
	StartedAt  time.Time
	Duration   time.Duration
	Err        error
	DumpStderr string // first ~1KB of pg_dump stderr, useful when Err != nil
	Reason     string // "disabled" / "skipped: already running" / "" when ran
}

// StartPgDumpScheduler kicks off the daily pg_dump → R2 job. Runs at 05:30
// in the configured upload timezone (reuses Upload.Timezone for consistency).
// No-op when artifact_storage S3 is not configured (cannot write the dump
// anywhere).
func StartPgDumpScheduler() {
	cfg := common.AppConfig
	if !cfg.ArtifactStorage.S3.IsConfigured() {
		common.SysLog("pg_dump scheduler disabled: artifact_storage.s3 not configured")
		return
	}
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(cfg.Database.URL)
	}
	if dsn == "" {
		common.SysLog("pg_dump scheduler disabled: DATABASE_URL not set")
		return
	}
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		common.SysLog("pg_dump scheduler disabled: not a postgres DSN")
		return
	}

	loc, err := time.LoadLocation(cfg.Upload.Timezone)
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	if pgDumpCron != nil {
		pgDumpCron.Stop()
	}
	pgDumpCron = cron.New(cron.WithLocation(loc))
	// Daily 05:30 — separate from the hourly upload job (runs on the
	// hour) so they do not compete for the same R2 connection burst.
	// 05:30 also sits after the hourly job's :00 push, giving any
	// in-flight JSONL upload time to finish before the dump starts.
	_, err = pgDumpCron.AddFunc("30 5 * * *", func() {
		res := RunPgDumpOnce(context.Background(), dsn)
		if res.Err != nil {
			common.SysError(fmt.Sprintf("pg_dump job failed: %v (stderr: %s)", res.Err, res.DumpStderr))
		} else {
			common.SysLog(fmt.Sprintf("pg_dump job ok: key=%s size=%s duration=%s",
				res.Key, humanSize(res.SizeBytes), res.Duration.Truncate(time.Millisecond)))
		}
		NotifyPgDumpResult(res)
	})
	if err != nil {
		common.SysError("failed to schedule pg_dump job: " + err.Error())
		return
	}
	pgDumpCron.Start()
}

const pgDumpAlreadyRunning = "skipped: already running"

// RunPgDumpOnce performs one pg_dump and uploads the gzipped output to R2.
// Exposed so admin endpoints / restore drills can trigger an immediate dump.
func RunPgDumpOnce(ctx context.Context, dsn string) PgDumpResult {
	res := PgDumpResult{StartedAt: time.Now()}
	if !atomic.CompareAndSwapInt32(&pgDumpRunning, 0, 1) {
		res.Reason = pgDumpAlreadyRunning
		return res
	}
	defer atomic.StoreInt32(&pgDumpRunning, 0)

	dbName, err := databaseNameFromDSN(dsn)
	if err != nil {
		res.Err = fmt.Errorf("parse dsn: %w", err)
		res.Duration = time.Since(res.StartedAt)
		return res
	}

	// pg_dump writes to stdout. Stream stdout through gzip into a temp file
	// rather than memory: the compressed dump is hundreds of MiB, and holding
	// it in a growing buffer can exceed a 1 GiB jobs container. The temp dir
	// stays outside the data dir, which the JSONL upload walks.
	dumpFile, err := os.CreateTemp("", "lazu-pg-dump-*.sql.gz")
	if err != nil {
		res.Err = fmt.Errorf("create dump temp file: %w", err)
		res.Duration = time.Since(res.StartedAt)
		return res
	}
	defer func() {
		_ = dumpFile.Close()
		_ = os.Remove(dumpFile.Name())
	}()
	gw := gzip.NewWriter(dumpFile)
	var dumpErr bytes.Buffer

	// --no-owner + --no-acl keep the dump portable across role names
	// (handy for restoring into a fresh local PG / Neon). --clean lets a
	// restore drop existing objects first.
	cmd := exec.CommandContext(ctx, "pg_dump",
		"--dbname="+dsn,
		"--no-owner",
		"--no-acl",
		"--clean",
		"--if-exists",
		"--format=plain",
	)
	cmd.Stdout = gw
	cmd.Stderr = &dumpErr
	if err := cmd.Run(); err != nil {
		res.Err = fmt.Errorf("pg_dump exec: %w", err)
		res.DumpStderr = truncate(dumpErr.String(), 1024)
		res.Duration = time.Since(res.StartedAt)
		return res
	}
	if err := gw.Close(); err != nil {
		res.Err = fmt.Errorf("close gzip writer: %w", err)
		res.Duration = time.Since(res.StartedAt)
		return res
	}

	// Upload to artifact_storage S3 (R2) — same client setup as user-file
	// storage. Key shape: pg-dump/<yyyy-mm>/<db>-YYYY-MM-DD_HHMM.sql.gz
	// in the configured upload timezone. The year-month subfolder makes
	// the listing browsable when 365 daily dumps accumulate and matches
	// the archive/responses/ layout for visual symmetry. Cloudflare's
	// lifecycle rule on the bare "pg-dump/" prefix still applies.
	loc, _ := time.LoadLocation(common.AppConfig.Upload.Timezone)
	if loc == nil {
		loc = time.UTC
	}
	localStart := res.StartedAt.In(loc)
	stamp := localStart.Format("2006-01-02_1504")
	yearMonth := localStart.Format("2006-01")
	key := fmt.Sprintf("pg-dump/%s/%s-%s.sql.gz", yearMonth, dbName, stamp)

	client, err := storage.NewS3(common.AppConfig.ArtifactStorage.S3)
	if err != nil {
		res.Err = fmt.Errorf("init s3 client: %w", err)
		res.Duration = time.Since(res.StartedAt)
		return res
	}

	size, err := dumpFile.Seek(0, io.SeekEnd)
	if err == nil {
		_, err = dumpFile.Seek(0, io.SeekStart)
	}
	if err != nil {
		res.Err = fmt.Errorf("rewind dump temp file: %w", err)
		res.Duration = time.Since(res.StartedAt)
		return res
	}
	res.SizeBytes = size
	if err := client.Put(ctx, key, dumpFile, "application/gzip"); err != nil {
		res.Err = fmt.Errorf("upload %s: %w", key, err)
		res.Duration = time.Since(res.StartedAt)
		return res
	}
	res.Key = key
	res.Duration = time.Since(res.StartedAt)
	return res
}

// databaseNameFromDSN extracts the db name from a postgres URL.
// Falls back to "lazu" when path parsing yields an empty segment.
func databaseNameFromDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	name := strings.Trim(u.Path, "/")
	if name == "" {
		name = "lazu"
	}
	// strip query suffix accidentally captured
	if idx := strings.Index(name, "?"); idx > 0 {
		name = name[:idx]
	}
	return name, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
