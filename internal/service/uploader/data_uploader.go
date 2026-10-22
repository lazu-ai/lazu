package uploader

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/storage"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/robfig/cron/v3"
)

const uploadDiskWarnPercent = 85.0

var rotatingLogLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// remoteKeyForUpload maps a local relative path to the R2 key for archival.
// JSONL captures split into two top-level prefixes by content type, then
// nested by year-month so 180 days of hourly uploads stay browsable:
//
//	requests-2026-05-24.jsonl   →  archive/requests/2026-05/requests-2026-05-24.jsonl
//	errors-2026-05-24.jsonl     →  archive/errors/2026-05/errors-2026-05-24.jsonl
//
// We upload as plain .jsonl now (no gzip). The daily compactor in
// internal/service/uploader/compactor.go gzips files older than 30 days
// in place; Cloudflare lifecycle rules on archive/requests/ and
// archive/errors/ expire everything at 180 days.
//
// Legacy filenames from before the rename (model-responses-* /
// error-logs-*) are tolerated so the first upload after a deploy still
// archives any in-flight files; they get mapped to the new prefixes.
func remoteKeyForUpload(relativePath string, isJSONL bool) string {
	if !isJSONL {
		return relativePath
	}
	base := filepath.Base(relativePath)
	prefix := "archive/misc"
	switch {
	case strings.HasPrefix(base, "requests-"),
		strings.HasPrefix(base, "model-responses-"):
		prefix = "archive/requests"
	case strings.HasPrefix(base, "errors-"),
		strings.HasPrefix(base, "error-logs-"):
		prefix = "archive/errors"
	}
	yearMonth := dateBucketFromFilename(base)
	return fmt.Sprintf("%s/%s/%s", prefix, yearMonth, base)
}

// dateBucketFromFilename extracts a YYYY-MM prefix from a daily-rotated
// filename like "model-responses-2026-05-24.jsonl". Falls back to the
// current month when the filename does not embed a date.
var dateInFilename = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})`)

func dateBucketFromFilename(base string) string {
	if m := dateInFilename.FindStringSubmatch(base); len(m) == 4 {
		return m[1] + "-" + m[2]
	}
	return time.Now().Format("2006-01")
}

// uploadFileGzipped streams a local file through gzip into the object store.
// JSONL compresses ~10:1, so this both saves bandwidth and slashes R2 storage
// cost on the archive prefix.
func uploadFileGzipped(ctx context.Context, client storage.ObjectStorage, key string, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := io.Copy(gw, f); err != nil {
		_ = gw.Close()
		return fmt.Errorf("gzip %s: %w", localPath, err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("close gzip writer: %w", err)
	}
	return client.Put(ctx, key, &buf, "application/gzip")
}

var cr *cron.Cron
var uploadRunning int32 // 0 idle, 1 running

func tryStartUpload() bool  { return atomic.CompareAndSwapInt32(&uploadRunning, 0, 1) }
func endUpload()            { atomic.StoreInt32(&uploadRunning, 0) }
func IsUploadRunning() bool { return atomic.LoadInt32(&uploadRunning) == 1 }

// UploadOptions 上传选项
type UploadOptions struct {
	ForceReupload bool // 强制重新上传（即使远端已存在）
	IncludeToday  bool // 包含今天的文件
	IncludeRecent bool // 包含最近 10 分钟内修改的文件
}

type UploadedFile struct {
	Key          string
	LocalPath    string
	SizeBytes    int64
	IsJSONL      bool
	JSONLRecords int64
}

type UploadSummary struct {
	Uploaded []UploadedFile
	Failed   map[string]string // key -> error
	Warnings []string          // operator-visible warnings; uploads may still succeed
}

// StartUploadScheduler starts the data archive job according to YAML config.
func StartUploadScheduler() {
	cfg := common.AppConfig
	if !cfg.Upload.Enable {
		return
	}
	loc, err := time.LoadLocation(cfg.Upload.Timezone)
	if err != nil {
		common.SysError(fmt.Sprintf("failed to load timezone %s: %v", cfg.Upload.Timezone, err))
		loc = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	if cr != nil {
		cr.Stop()
	}
	cr = cron.New(cron.WithLocation(loc))
	_, err = cr.AddFunc(cfg.Upload.Schedule, func() {
		start := time.Now()
		common.SysLog("object upload job started (cron)")
		summary, err := UploadDataFolderOnce()
		dur := time.Since(start)
		if err != nil {
			common.SysError("object upload job error: " + err.Error())
		}
		uploaded := 0
		failed := 0
		if summary != nil {
			uploaded = len(summary.Uploaded)
			failed = len(summary.Failed)
		}
		common.SysLog(fmt.Sprintf("object upload job finished in %s: uploaded=%d failed=%d", dur.Truncate(time.Millisecond), uploaded, failed))
		// Healthy scheduled runs are represented in the daily operations digest.
		if e := SendScheduledUploadEmail(summary, err); e != nil {
			common.SysError("failed to send upload email: " + e.Error())
		}
		// Feishu — failure-only by default to avoid hourly noise.
		notifyUploadResult(summary, err)
	})
	if err != nil {
		common.SysError("failed to schedule object upload job: " + err.Error())
		return
	}
	if _, err = cr.AddFunc("30 4 * * *", runArchiveSweep); err != nil {
		common.SysError("failed to schedule archive retention: " + err.Error())
	}
	cr.Start()
}

// UploadDataFolderOnce walks the data directory and uploads files not present remotely.
// Uses default options (skip today's files, skip recent files, skip if already present).
func UploadDataFolderOnce() (*UploadSummary, error) {
	return UploadDataFolderWithOptions(UploadOptions{})
}

// UploadDataFolderWithOptions walks data dir and uploads files with custom options.
func UploadDataFolderWithOptions(opts UploadOptions) (*UploadSummary, error) {
	if !tryStartUpload() {
		return nil, fmt.Errorf("upload already running")
	}
	defer endUpload()
	cfg := common.AppConfig
	dataDir := common.GetDataDir()
	if dataDir == "" {
		dataDir = "./data"
	}
	common.SysLog(fmt.Sprintf("Scanning data directory for object upload: %s (force=%v, includeToday=%v, includeRecent=%v)",
		dataDir, opts.ForceReupload, opts.IncludeToday, opts.IncludeRecent))
	client, err := newStorageClient(cfg.Upload.S3)
	if err != nil {
		return nil, err
	}
	summary := &UploadSummary{Failed: map[string]string{}}
	recordWarning := func(msg string) {
		summary.Warnings = append(summary.Warnings, msg)
		common.SysError("object upload warning: " + msg)
	}
	diskInfo := common.GetDiskSpaceInfo()
	if diskInfo.Total > 0 && diskInfo.UsedPercent >= uploadDiskWarnPercent {
		recordWarning(fmt.Sprintf("disk usage high on %s: %.1f%% used (%s free)", diskInfo.Path, diskInfo.UsedPercent, humanSize(int64(diskInfo.Free))))
	}
	now := time.Now()

	// Precompute export relative prefix
	exportRel, _ := filepath.Rel(dataDir, common.GetExportDir())
	exportRel = filepath.ToSlash(exportRel)
	if exportRel != "" && !strings.HasSuffix(exportRel, "/") {
		exportRel += "/"
	}

	// Walk files
	walkErr := filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return nil
		}
		relSlashed := filepath.ToSlash(rel)
		// Runtime volume metadata is not backup data.
		if relSlashed == ".lazu-owned-v1" {
			return nil
		}
		isJSONL := strings.HasSuffix(strings.ToLower(path), ".jsonl")
		isExportJSONL := exportRel != "" && strings.HasPrefix(relSlashed, exportRel) && isJSONL
		// Skip very recent files to avoid racing with writers (10 minutes)
		// Unless IncludeRecent is true
		if !opts.IncludeRecent && time.Since(info.ModTime()) < 10*time.Minute {
			return nil
		}
		// Never upload local config files mounted under data/config
		// to avoid leaking secrets.
		if strings.HasPrefix(relSlashed, "config/") {
			return nil
		}
		// Treat all current-day rotating export shards as active. The writer
		// can reopen a lower suffix after restart, so the uploader must not
		// infer the active shard from the highest suffix on disk.
		if !opts.IncludeToday {
			if isExportJSONL && containsDate(relSlashed, now) {
				if isCurrentRotatingDailyFile(path, now) {
					return nil
				}
			}
			// Skip today's app log file by filename pattern lazu-YYYYMMDD*.log
			logsRel, _ := filepath.Rel(dataDir, common.GetLogsDir())
			logsRel = filepath.ToSlash(logsRel)
			if logsRel != "" && strings.HasPrefix(filepath.ToSlash(rel), logsRel+"/") {
				base := filepath.Base(relSlashed)
				if isTodayLogFilename(base) {
					return nil
				}
			}
		}

		// App logs (lazu-*.log) stay local-only — too noisy to archive
		// and easy to retrieve via `docker logs` while still on disk.
		logsRel, _ := filepath.Rel(dataDir, common.GetLogsDir())
		logsRel = filepath.ToSlash(logsRel)
		if logsRel != "" && strings.HasPrefix(relSlashed, logsRel+"/") {
			return nil
		}

		key := remoteKeyForUpload(relSlashed, isJSONL)
		localSize := info.Size()

		// Check if already exists in remote (unless ForceReupload is true).
		// For JSONL we re-upload only if its line count > already-archived
		// count, since the gzipped sizes do not directly correlate to lines.
		if !opts.ForceReupload {
			exists := false
			var remoteSize int64

			// Try public HEAD first
			if ok, size := headPublicWithSize(cfg.Upload.PublicBaseURL, key); ok {
				exists = true
				remoteSize = size
			}
			if !exists {
				// Fallback to signed HEAD in case bucket is private
				if meta, err := client.Head(context.Background(), key); err == nil && meta.Exists {
					exists = true
					remoteSize = meta.ContentLength
				}
			}

			if exists && !isJSONL && localSize <= remoteSize {
				return nil
			}
			if exists && !isJSONL && localSize > remoteSize {
				common.SysLog(fmt.Sprintf("re-uploading %s: local size %s > remote size %s",
					key, humanSize(localSize), humanSize(remoteSize)))
			}
		}

		// Prepare stats
		uf := UploadedFile{Key: key, LocalPath: path, SizeBytes: localSize}
		if isJSONL {
			uf.IsJSONL = true
			if n, e2 := countLines(path); e2 == nil {
				uf.JSONLRecords = n
			}
		}
		// Upload as plain bytes regardless of file type. JSONL stays
		// uncompressed for the first 30 days so recent debugging is
		// grep-friendly; the daily compactor (compactor.go) rewrites
		// older files as .gz in place.
		if err := client.PutFromFile(context.Background(), key, path); err != nil {
			common.SysError(fmt.Sprintf("upload failed for %s: %v; keeping local file for retry", key, err))
			summary.Failed[key] = err.Error()
			return nil
		}
		if uf.IsJSONL {
			common.SysLog(fmt.Sprintf("uploaded: %s (%s, %d lines)", key, humanSize(uf.SizeBytes), uf.JSONLRecords))
		} else {
			common.SysLog(fmt.Sprintf("uploaded: %s (%s)", key, humanSize(uf.SizeBytes)))
		}
		summary.Uploaded = append(summary.Uploaded, uf)
		// Verify via public URL if enabled
		verified := false
		if cfg.Upload.VerifyViaPublic {
			if ok, _ := headPublic(cfg.Upload.PublicBaseURL, key); ok {
				verified = true
			}
		}
		if !verified {
			if meta, err := client.Head(context.Background(), key); err == nil && meta.Exists {
				verified = true
			}
		}
		if !verified {
			common.SysError("verification failed for " + key + "; keeping local file for retry")
			summary.Failed[key] = "verification failed"
			return nil
		}
		// Delete local after verify
		// 重要：如果是手动上传今天的文件，不要删除本地文件
		// 这样自动任务可以在一天结束时重新上传完整的文件
		shouldDelete := cfg.Upload.DeleteAfterVerify
		if opts.IncludeToday && containsToday(rel) {
			shouldDelete = false
			common.SysLog(fmt.Sprintf("keeping today's file locally for later sync: %s", key))
		}
		if shouldDelete {
			_ = os.Remove(path)
		}
		return nil
	})
	if walkErr != nil {
		return summary, walkErr
	}
	return summary, nil
}

func containsToday(name string) bool {
	return containsDate(name, time.Now().In(rotatingLogLocation))
}

func containsDate(name string, now time.Time) bool {
	return strings.Contains(name, rotatingLogDate(now))
}

func rotatingLogDate(now time.Time) string {
	return now.In(rotatingLogLocation).Format("2006-01-02")
}

func isCurrentRotatingDailyFile(path string, now time.Time) bool {
	if strings.TrimSpace(common.ModelLogPath) == "" || strings.TrimSpace(common.ErrorLogPath) == "" {
		return containsDate(filepath.Base(path), now)
	}
	for _, basePath := range []string{common.ModelLogPath, common.ErrorLogPath} {
		if isRotatingDailyPathForDate(path, basePath, now) {
			return true
		}
	}
	return false
}

func isRotatingDailyPathForDate(path string, basePath string, now time.Time) bool {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" {
		return false
	}
	dir := filepath.Dir(basePath)
	base := filepath.Base(basePath)
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext)
	if prefix == "" {
		return false
	}
	if !samePath(filepath.Dir(path), dir) {
		return false
	}
	_, ok := rotatingDailySequence(filepath.Base(path), prefix, ext, rotatingLogDate(now))
	return ok
}

func rotatingDailySequence(base, prefix, ext, date string) (int, bool) {
	if ext != "" {
		if !strings.HasSuffix(base, ext) {
			return 0, false
		}
		base = strings.TrimSuffix(base, ext)
	}
	stem := prefix + "-" + date
	if base == stem {
		return 1, true
	}
	if !strings.HasPrefix(base, stem+"-") {
		return 0, false
	}
	seq, err := strconv.Atoi(strings.TrimPrefix(base, stem+"-"))
	if err != nil || seq < 2 {
		return 0, false
	}
	return seq, true
}

func samePath(a, b string) bool {
	aAbs, err := filepath.Abs(a)
	if err == nil {
		a = aAbs
	}
	bAbs, err := filepath.Abs(b)
	if err == nil {
		b = bAbs
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// PendingFile 表示一个待上传的文件
type PendingFile struct {
	Key           string `json:"key"`
	LocalPath     string `json:"local_path"`
	SizeBytes     int64  `json:"size_bytes"`
	SizeHuman     string `json:"size_human"`
	RemoteSizeStr string `json:"remote_size,omitempty"` // 远端文件大小
	ModTime       string `json:"mod_time"`
	IsJSONL       bool   `json:"is_jsonl"`
	Category      string `json:"category"`    // export, logs, other
	Status        string `json:"status"`      // pending, uploaded, skipped, needs_update
	SkipReason    string `json:"skip_reason"` // 跳过原因
}

// ListPendingFiles 列出 data 目录下所有可上传的文件
func ListPendingFiles() ([]PendingFile, error) {
	cfg := common.AppConfig
	dataDir := common.GetDataDir()
	if dataDir == "" {
		dataDir = "./data"
	}

	// 转换为绝对路径以避免路径比较问题
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		common.SysError(fmt.Sprintf("ListPendingFiles: failed to get absolute path for %s: %v", dataDir, err))
		absDataDir = dataDir
	}

	common.SysLog(fmt.Sprintf("ListPendingFiles: scanning directory %s (abs: %s)", dataDir, absDataDir))

	// Check if directory exists
	if _, err := os.Stat(absDataDir); os.IsNotExist(err) {
		common.SysLog(fmt.Sprintf("ListPendingFiles: data directory does not exist: %s", absDataDir))
		return []PendingFile{}, nil
	}

	files := make([]PendingFile, 0) // 使用 make 确保返回 [] 而不是 null

	// Precompute relative prefixes using absolute paths
	exportDir, _ := filepath.Abs(common.GetExportDir())
	logsDir, _ := filepath.Abs(common.GetLogsDir())

	exportRel, _ := filepath.Rel(absDataDir, exportDir)
	exportRel = filepath.ToSlash(exportRel)
	if exportRel != "" && !strings.HasSuffix(exportRel, "/") {
		exportRel += "/"
	}

	logsRel, _ := filepath.Rel(absDataDir, logsDir)
	logsRel = filepath.ToSlash(logsRel)
	if logsRel != "" && !strings.HasSuffix(logsRel, "/") {
		logsRel += "/"
	}

	common.SysLog(fmt.Sprintf("ListPendingFiles: dataDir=%s exportDir=%s logsDir=%s exportRel=%s logsRel=%s",
		absDataDir, exportDir, logsDir, exportRel, logsRel))

	fileCount := 0
	walkErr := filepath.Walk(absDataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			common.SysLog(fmt.Sprintf("ListPendingFiles: walk error at %s: %v", path, err))
			return err
		}
		if info.IsDir() {
			return nil
		}
		fileCount++
		rel, err := filepath.Rel(absDataDir, path)
		if err != nil {
			return nil
		}
		relSlashed := filepath.ToSlash(rel)
		if relSlashed == ".lazu-owned-v1" {
			return nil
		}

		// Skip config files (never show these)
		if strings.HasPrefix(relSlashed, "config/") {
			return nil
		}

		localSize := info.Size()
		pf := PendingFile{
			Key:       relSlashed,
			LocalPath: path,
			SizeBytes: localSize,
			SizeHuman: humanSize(localSize),
			ModTime:   info.ModTime().Format("2006-01-02 15:04:05"),
			IsJSONL:   strings.HasSuffix(strings.ToLower(path), ".jsonl"),
			Category:  "other",
			Status:    "pending",
		}

		// Determine category
		if exportRel != "" && strings.HasPrefix(relSlashed, exportRel) {
			pf.Category = "export"
		}
		if logsRel != "" && strings.HasPrefix(relSlashed, logsRel) {
			pf.Category = "logs"
		}

		// Check skip reasons (for display purposes, actual upload still skips these by default)
		if time.Since(info.ModTime()) < 10*time.Minute {
			pf.Status = "skipped"
			pf.SkipReason = "正在写入 (修改时间 < 10分钟)"
		} else if pf.Category == "export" && pf.IsJSONL && containsToday(rel) && isCurrentRotatingDailyFile(path, time.Now()) {
			pf.Status = "skipped"
			pf.SkipReason = "当前写入分片"
		} else if pf.Category == "logs" && isTodayLogFilename(filepath.Base(relSlashed)) {
			pf.Status = "skipped"
			pf.SkipReason = "今天的日志"
		} else {
			// Check if the object already exists and compare sizes.
			if cfg.Upload.PublicBaseURL != "" {
				if exists, remoteSize := headPublicWithSize(cfg.Upload.PublicBaseURL, relSlashed); exists {
					pf.RemoteSizeStr = humanSize(remoteSize)
					if localSize > remoteSize {
						pf.Status = "needs_update"
						pf.SkipReason = fmt.Sprintf("本地文件更大 (%s > %s)", humanSize(localSize), humanSize(remoteSize))
					} else {
						pf.Status = "uploaded"
						pf.SkipReason = "已上传到对象存储"
					}
				}
			}
		}

		files = append(files, pf)
		return nil
	})

	common.SysLog(fmt.Sprintf("ListPendingFiles: walked %d files, returning %d files", fileCount, len(files)))

	if walkErr != nil {
		return nil, walkErr
	}
	return files, nil
}

func isTodayLogFilename(base string) bool {
	// expect: lazu-YYYYMMDDHHMMSS.log or similar
	if !strings.HasPrefix(base, "lazu-") || !strings.HasSuffix(base, ".log") {
		return false
	}
	// Extract YYYYMMDD from base
	s := strings.TrimPrefix(base, "lazu-")
	if len(s) < 8 {
		return false
	}
	ymd := s[:8]
	today := time.Now().Format("20060102")
	return ymd == today
}

var newStorageClient = storage.NewClient

var httpClient = &http.Client{Timeout: 10 * time.Second}

func headPublic(baseURL, key string) (bool, int) {
	if baseURL == "" {
		return false, 0
	}
	full := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(key, "/")
	req, _ := http.NewRequest(http.MethodHead, full, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, 0
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300, resp.StatusCode
}

// headPublicWithSize returns (exists, contentLength) for a public URL
func headPublicWithSize(baseURL, key string) (bool, int64) {
	if baseURL == "" {
		return false, 0
	}
	full := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(key, "/")
	req, _ := http.NewRequest(http.MethodHead, full, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, resp.ContentLength
	}
	return false, 0
}

func countLines(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var count int64
	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		buf, err := r.ReadBytes('\n')
		if len(buf) > 0 {
			count += int64(bytes.Count(buf, []byte{'\n'}))
		}
		if err != nil {
			if err.Error() == "EOF" || err == io.EOF {
				break
			}
			break
		}
	}
	return count, nil
}

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// SingleFileResult 单文件上传结果
type SingleFileResult struct {
	Key       string
	SizeHuman string
}

// UploadSingleFile uploads one file to object storage.
func UploadSingleFile(key string) (*SingleFileResult, error) {
	cfg := common.AppConfig
	dataDir := common.GetDataDir()
	if dataDir == "" {
		dataDir = "./data"
	}

	// 转换为绝对路径
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %v", err)
	}

	// 构建完整文件路径
	localPath := filepath.Join(absDataDir, filepath.FromSlash(key))

	// 检查文件是否存在
	info, err := os.Stat(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, types.Err(types.ErrorCodeUploadFileNotFound, types.D{"field": "key", "key": key})
		}
		return nil, fmt.Errorf("stat data file %s: %w", key, err)
	}

	if info.IsDir() {
		return nil, types.Err(types.ErrorCodeUploadKeyNotAllowed, types.D{"field": "key", "key": key, "reason": "it is a directory"})
	}

	// 安全检查：不允许上传 config 目录下的文件
	if strings.HasPrefix(key, "config/") {
		return nil, types.Err(types.ErrorCodeUploadKeyNotAllowed, types.D{"field": "key", "key": key, "reason": "configuration files are never uploaded"})
	}

	// 创建存储客户端
	client, err := newStorageClient(cfg.Upload.S3)
	if err != nil {
		return nil, types.WrapErr(fmt.Errorf("create storage client: %w", err), types.ErrorCodeStorageNotConfigured)
	}

	// 上传文件
	common.SysLog(fmt.Sprintf("uploading single file: %s (%s)", key, humanSize(info.Size())))
	if err := client.PutFromFile(context.Background(), key, localPath); err != nil {
		return nil, types.WrapErr(fmt.Errorf("upload %s: %w", key, err), types.ErrorCodeUploadFailed, types.D{"key": key})
	}

	// 验证上传
	verified := false
	if cfg.Upload.VerifyViaPublic && cfg.Upload.PublicBaseURL != "" {
		if ok, _ := headPublic(cfg.Upload.PublicBaseURL, key); ok {
			verified = true
		}
	}
	if !verified {
		if meta, err := client.Head(context.Background(), key); err == nil && meta.Exists {
			verified = true
		}
	}
	if !verified {
		return nil, types.WrapErr(fmt.Errorf("uploaded object %s could not be verified", key), types.ErrorCodeUploadFailed, types.D{"key": key})
	}

	common.SysLog(fmt.Sprintf("single file uploaded successfully: %s (%s)", key, humanSize(info.Size())))

	return &SingleFileResult{
		Key:       key,
		SizeHuman: humanSize(info.Size()),
	}, nil
}

// SendScheduledUploadEmail suppresses healthy cron success emails only when
// Feishu is configured to deliver the replacement daily operations digest.
func SendScheduledUploadEmail(summary *UploadSummary, runErr error) error {
	if !shouldSendScheduledUploadEmail(summary, runErr, common.FeishuBotWebhook() != "") {
		return nil
	}
	return SendUploadEmail(summary, runErr)
}

// SendUploadEmail sends an upload summary email. Manual uploads use this
// directly so their explicitly requested success notification is preserved.
func SendUploadEmail(summary *UploadSummary, runErr error) error {
	cfg := common.AppConfig
	if !cfg.Upload.Email.Enable {
		return nil
	}
	to := cfg.Upload.Email.To
	if to == "" {
		return nil
	}
	status, severity := "成功", "NOTICE"
	if runErr != nil {
		status, severity = "失败", "ALERT"
	} else if summary != nil && (len(summary.Warnings) > 0 || len(summary.Failed) > 0) {
		status, severity = "有警告", "WARN"
	}
	uploaded, failed := 0, 0
	if summary != nil {
		uploaded, failed = len(summary.Uploaded), len(summary.Failed)
	}
	title := "R2 备份上传" + status
	blocks := []common.MailBlock{{Facts: []common.MailFact{
		{Label: "执行时间", Value: time.Now().Format("2006-01-02 15:04:05")},
		{Label: "成功", Value: fmt.Sprintf("%d", uploaded)},
		{Label: "失败", Value: fmt.Sprintf("%d", failed)},
	}}}
	if runErr != nil {
		blocks = append(blocks, common.MailBlock{Snippet: runErr.Error()})
	}
	if summary != nil && len(summary.Warnings) > 0 {
		table := &common.MailTable{Head: []string{"告警"}}
		for _, warning := range summary.Warnings {
			table.Rows = append(table.Rows, []string{warning})
		}
		blocks = append(blocks, common.MailBlock{Table: table})
	}
	if summary != nil && len(summary.Failed) > 0 {
		table := &common.MailTable{Head: []string{"分片", "错误"}}
		for key, reason := range summary.Failed {
			table.Rows = append(table.Rows, []string{key, reason})
		}
		blocks = append(blocks, common.MailBlock{Table: table})
	}
	if uploaded > 0 {
		table := &common.MailTable{Head: []string{"对象", "大小", "JSONL 记录"}}
		for _, uf := range summary.Uploaded {
			records := "-"
			if uf.IsJSONL {
				records = fmt.Sprintf("%d", uf.JSONLRecords)
			}
			table.Rows = append(table.Rows, []string{uf.Key, humanSize(uf.SizeBytes), records})
		}
		blocks = append(blocks, common.MailBlock{Table: table})
	}
	if runErr != nil || failed > 0 {
		blocks = append(blocks, common.MailBlock{Muted: true, Text: "本地分片保留 7 天，下一轮会自动重试。"})
	}
	mail := common.Mail{
		Tier: common.MailOps, Source: "BACKUP", Severity: severity, Title: title,
		Preheader: fmt.Sprintf("成功 %d，失败 %d", uploaded, failed), Blocks: blocks,
	}
	body, err := common.RenderMail(mail)
	if err != nil {
		return err
	}
	subject := "[Lazu ops] " + title
	// prefer YAML-provided From if SMTPFrom empty
	if common.SMTPFrom == "" && cfg.Upload.Email.From != "" {
		common.SMTPFrom = cfg.Upload.Email.From
	}
	if err := common.SendMail(common.MailMeta{Kind: common.MailKindOps, AlertText: common.MailAlertText(mail)}, subject, to, body); err != nil {
		return err
	}
	common.SysLog(fmt.Sprintf("upload email sent to %s (subject=%s)", to, subject))
	return nil
}

func shouldSendUploadEmail(summary *UploadSummary, runErr error) bool {
	return runErr != nil || (summary != nil && (len(summary.Failed) > 0 || len(summary.Warnings) > 0))
}

func shouldSendScheduledUploadEmail(summary *UploadSummary, runErr error, feishuConfigured bool) bool {
	return !feishuConfigured || shouldSendUploadEmail(summary, runErr)
}
