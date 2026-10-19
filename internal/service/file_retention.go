package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// Retention per local file class:
//   - JSONL request/response captures: 7 days local, since the hourly
//     uploader pushes them to R2 and deletes the local copy. Seven days
//     leaves time to repair storage credentials without losing captures.
//   - App logs (lazu-*.log): 7 days local. Small footprint at warn level,
//     useful for panic debugging.
//
// Override globally via FILE_RETENTION_DAYS env (applies the same number
// to every target — rarely needed).
const (
	jsonlRetentionDays         = 7
	appLogRetentionDays        = 7
	FileRetentionIntervalHours = 24
)

type fileRetentionTarget struct {
	globPattern        string
	parseReferenceTime func(baseName string) (time.Time, bool)
	retentionDays      int
}

type fileRetentionStats struct {
	scanned int
	deleted int
	failed  int
}

var (
	fileRetentionOnce   sync.Once
	fileRetentionRunner = runFileRetention
)

func StartFileRetention() {
	fileRetentionOnce.Do(func() {
		go fileRetentionRunner()
	})
}

func runFileRetention() {
	cleanup := func() {
		stats := cleanupFilesByTargets(time.Now(), defaultFileRetentionTargets())
		common.SysLog(fmt.Sprintf(
			"file retention: scanned=%d deleted=%d failed=%d",
			stats.scanned, stats.deleted, stats.failed,
		))
	}

	cleanup()
	ticker := time.NewTicker(FileRetentionIntervalHours * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		cleanup()
	}
}

func defaultFileRetentionTargets() []fileRetentionTarget {
	exportDir := common.GetExportDir()
	targets := []fileRetentionTarget{
		// New names (post-rename).
		{
			globPattern:        filepath.Join(exportDir, "requests-*.jsonl"),
			parseReferenceTime: parseRequestsLogTime,
			retentionDays:      jsonlRetentionDays,
		},
		{
			globPattern:        filepath.Join(exportDir, "errors-*.jsonl"),
			parseReferenceTime: parseErrorsLogTime,
			retentionDays:      jsonlRetentionDays,
		},
		// Legacy names — kept so retention still catches files written
		// before the rename. Safe to delete this stanza once no
		// model-responses-* or error-logs-* remain in any /data/export.
		{
			globPattern:        filepath.Join(exportDir, "model-responses-*.jsonl"),
			parseReferenceTime: parseLegacyModelResponseLogTime,
			retentionDays:      jsonlRetentionDays,
		},
		{
			globPattern:        filepath.Join(exportDir, "error-logs-*.jsonl"),
			parseReferenceTime: parseLegacyErrorLogTime,
			retentionDays:      jsonlRetentionDays,
		},
	}

	// Honor configured writer paths so retention works with custom MODEL_LOG_PATH/ERROR_LOG_PATH.
	if target, ok := buildRotatingDailyFileTarget(common.ModelLogPath); ok {
		target.retentionDays = jsonlRetentionDays
		targets = append(targets, target)
	}
	if target, ok := buildRotatingDailyFileTarget(common.ErrorLogPath); ok {
		target.retentionDays = jsonlRetentionDays
		targets = append(targets, target)
	}

	logsDir := common.GetLogsDir()
	if common.LogDir != nil {
		if configured := strings.TrimSpace(*common.LogDir); configured != "" {
			logsDir = configured
		}
	}

	targets = append(targets,
		fileRetentionTarget{
			globPattern:        filepath.Join(logsDir, "lazu-*.log"),
			parseReferenceTime: parseAppLogTime,
			retentionDays:      appLogRetentionDays,
		},
	)
	return targets
}

func cleanupFilesByTargets(now time.Time, targets []fileRetentionTarget) fileRetentionStats {
	stats := fileRetentionStats{}
	seenPaths := make(map[string]struct{})
	envOverride := common.GetEnvOrDefault("FILE_RETENTION_DAYS", 0)
	for _, target := range targets {
		days := target.retentionDays
		if envOverride > 0 {
			days = envOverride
		}
		if days <= 0 {
			continue
		}
		cutoff := now.AddDate(0, 0, -days)
		matches, err := filepath.Glob(target.globPattern)
		if err != nil {
			stats.failed++
			common.SysError(fmt.Sprintf("file retention: invalid glob %s: %v", target.globPattern, err))
			continue
		}
		for _, path := range matches {
			cleanedPath := filepath.Clean(path)
			if _, exists := seenPaths[cleanedPath]; exists {
				continue
			}
			seenPaths[cleanedPath] = struct{}{}

			info, err := os.Stat(path)
			if err != nil {
				stats.failed++
				common.SysError(fmt.Sprintf("file retention: stat failed %s: %v", path, err))
				continue
			}
			if info.IsDir() {
				continue
			}

			stats.scanned++
			referenceTime := info.ModTime()
			if target.parseReferenceTime != nil {
				if parsedTime, ok := target.parseReferenceTime(filepath.Base(path)); ok {
					referenceTime = parsedTime
				}
			}
			if !referenceTime.Before(cutoff) {
				continue
			}

			if err := os.Remove(path); err != nil {
				stats.failed++
				common.SysError(fmt.Sprintf("file retention: remove failed %s: %v", path, err))
				continue
			}
			stats.deleted++
		}
	}
	return stats
}

func parseRequestsLogTime(baseName string) (time.Time, bool) {
	return parseRotatingDailyFilenameTime(baseName, "requests", ".jsonl")
}

func parseErrorsLogTime(baseName string) (time.Time, bool) {
	return parseRotatingDailyFilenameTime(baseName, "errors", ".jsonl")
}

func parseLegacyModelResponseLogTime(baseName string) (time.Time, bool) {
	return parseRotatingDailyFilenameTime(baseName, "model-responses", ".jsonl")
}

func parseLegacyErrorLogTime(baseName string) (time.Time, bool) {
	return parseRotatingDailyFilenameTime(baseName, "error-logs", ".jsonl")
}

func buildRotatingDailyFileTarget(basePath string) (fileRetentionTarget, bool) {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" {
		return fileRetentionTarget{}, false
	}
	baseName := filepath.Base(basePath)
	ext := filepath.Ext(baseName)
	namePrefix := strings.TrimSuffix(baseName, ext)
	if namePrefix == "" {
		return fileRetentionTarget{}, false
	}
	globPattern := filepath.Join(filepath.Dir(basePath), namePrefix+"-*")
	if ext != "" {
		globPattern += ext
	}

	return fileRetentionTarget{
		globPattern: globPattern,
		parseReferenceTime: func(baseName string) (time.Time, bool) {
			return parseRotatingDailyFilenameTime(baseName, namePrefix, ext)
		},
	}, true
}

func parseRotatingDailyFilenameTime(baseName, prefix, ext string) (time.Time, bool) {
	if prefix == "" {
		return time.Time{}, false
	}
	if ext != "" {
		if !strings.HasSuffix(baseName, ext) {
			return time.Time{}, false
		}
		baseName = strings.TrimSuffix(baseName, ext)
	}
	if !strings.HasPrefix(baseName, prefix+"-") {
		return time.Time{}, false
	}

	payload := strings.TrimPrefix(baseName, prefix+"-")
	if len(payload) < len("2006-01-02") {
		return time.Time{}, false
	}
	datePart := payload[:len("2006-01-02")]
	if suffix := payload[len("2006-01-02"):]; suffix != "" {
		if !strings.HasPrefix(suffix, "-") {
			return time.Time{}, false
		}
		indexPart := strings.TrimPrefix(suffix, "-")
		if indexPart == "" {
			return time.Time{}, false
		}
		if _, err := strconv.Atoi(indexPart); err != nil {
			return time.Time{}, false
		}
	}

	day, err := time.ParseInLocation("2006-01-02", datePart, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	// JSONL files are daily buckets; use end-of-day to avoid premature deletion.
	return day.Add(24*time.Hour - time.Nanosecond), true
}

func parseAppLogTime(baseName string) (time.Time, bool) {
	if !strings.HasPrefix(baseName, "lazu-") || !strings.HasSuffix(baseName, ".log") {
		return time.Time{}, false
	}

	payload := strings.TrimSuffix(strings.TrimPrefix(baseName, "lazu-"), ".log")
	if len(payload) < len("20060102150405") {
		return time.Time{}, false
	}
	timestampPart := payload[:len("20060102150405")]
	for _, c := range timestampPart {
		if c < '0' || c > '9' {
			return time.Time{}, false
		}
	}

	ts, err := time.ParseInLocation("20060102150405", timestampPart, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}
