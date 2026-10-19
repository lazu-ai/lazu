package uploader

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/service/alerting"
)

const (
	hourlyUploadIncidentKey = "hourly-upload"
	pgDumpIncidentKey       = "pg-dump"
)

func NotifyPgDumpResult(res PgDumpResult) {
	if res.Reason != "" && res.Err == nil {
		alerting.RecordHealth(pgDumpIncidentKey, "Database backup", "skipped", res.Reason)
		return
	}
	if res.Err != nil {
		summary := fmt.Sprintf("error=%v", res.Err)
		alerting.RecordHealth(pgDumpIncidentKey, "Database backup", "failed", summary)
		alerting.Report(alerting.Incident{
			Key:         pgDumpIncidentKey,
			Fingerprint: pgDumpFailureFingerprint(res),
			Priority:    alerting.PriorityMaintenance,
			Message: fmt.Sprintf("❌ Lazu pg_dump FAILED\nerror: %v\nstderr: %s",
				res.Err, truncateUploadNotificationText(res.DumpStderr, 360)),
		})
		return
	}
	summary := fmt.Sprintf("size=%s duration=%s", humanSize(res.SizeBytes), res.Duration.Truncate(time.Millisecond))
	alerting.RecordHealth(pgDumpIncidentKey, "Database backup", "ok", summary)
	alerting.Resolve(pgDumpIncidentKey, fmt.Sprintf("✅ Lazu pg_dump recovered\nkey=%s", res.Key))
}

func notifyUploadResult(summary *UploadSummary, runErr error) {
	if runErr != nil && runErr.Error() == "upload already running" {
		// A concurrent run has no new outcome. Keep the prior health snapshot so
		// an unresolved failure remains visible in the daily digest.
		return
	}

	uploaded, failed, warnings := uploadCounts(summary)
	if runErr == nil && failed == 0 && warnings == 0 {
		alerting.RecordHealth(hourlyUploadIncidentKey, "R2 JSONL upload", "ok",
			fmt.Sprintf("uploaded=%d failed=0 warnings=0", uploaded))
		alerting.Resolve(hourlyUploadIncidentKey,
			fmt.Sprintf("✅ Lazu hourly upload recovered\nuploaded=%d failed=0 warnings=0", uploaded))
		return
	}

	status := "warning"
	if runErr != nil || failed > 0 {
		status = "failed"
	}
	alerting.RecordHealth(hourlyUploadIncidentKey, "R2 JSONL upload", status,
		fmt.Sprintf("uploaded=%d failed=%d warnings=%d", uploaded, failed, warnings))

	msg := fmt.Sprintf("⚠️ Lazu hourly upload needs attention\nuploaded=%d failed=%d warnings=%d",
		uploaded, failed, warnings)
	if runErr != nil {
		msg += "\nerror: " + runErr.Error()
	}
	if cause := uploadFailureCause(summary, runErr); cause != "" {
		msg += "\ncause: " + cause
	}
	if summary != nil && len(summary.Warnings) > 0 {
		limit := len(summary.Warnings)
		if limit > 3 {
			limit = 3
		}
		msg += "\nwarnings: " + strings.Join(summary.Warnings[:limit], " | ")
	}
	if summary != nil && len(summary.Failed) > 0 {
		keys := make([]string, 0, len(summary.Failed))
		for key := range summary.Failed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 3 {
			keys = keys[:3]
		}
		msg += "\nsample failures: " + strings.Join(keys, ", ")
		if sampleErr := summary.Failed[keys[0]]; sampleErr != "" {
			msg += "\nsample error: " + truncateUploadNotificationText(sampleErr, 360)
		}
	}
	alerting.Report(alerting.Incident{
		Key:         hourlyUploadIncidentKey,
		Fingerprint: uploadIssueFingerprint(summary, runErr),
		Priority:    alerting.PriorityMaintenance,
		Message:     msg,
	})
}

func pgDumpFailureFingerprint(res PgDumpResult) string {
	parts := make([]string, 0, 2)
	if res.Err != nil {
		parts = append(parts, "error:"+normalizeUploadIssue(pgDumpErrorCause(res.Err.Error())))
	}
	if stderr := strings.TrimSpace(res.DumpStderr); stderr != "" {
		parts = append(parts, "stderr:"+normalizeUploadIssue(stderr))
	}
	return strings.Join(parts, "|")
}

func pgDumpErrorCause(value string) string {
	if strings.HasPrefix(value, "upload pg-dump/") {
		if separator := strings.Index(value, ": "); separator >= 0 {
			return "upload: " + value[separator+2:]
		}
	}
	return value
}

func uploadCounts(summary *UploadSummary) (uploaded, failed, warnings int) {
	if summary == nil {
		return 0, 0, 0
	}
	return len(summary.Uploaded), len(summary.Failed), len(summary.Warnings)
}

func uploadIssueFingerprint(summary *UploadSummary, runErr error) string {
	parts := make([]string, 0, 4)
	if runErr != nil {
		parts = append(parts, "run:"+normalizeUploadIssue(runErr.Error()))
	}
	if summary != nil {
		uniqueErrors := make(map[string]struct{}, len(summary.Failed))
		for _, failure := range summary.Failed {
			uniqueErrors[normalizeUploadIssue(failure)] = struct{}{}
		}
		for failure := range uniqueErrors {
			parts = append(parts, "failure:"+failure)
		}
		for _, warning := range summary.Warnings {
			parts = append(parts, "warning:"+normalizeUploadIssue(warning))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func normalizeUploadIssue(value string) string {
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "401") && strings.Contains(lower, "unauthorized"):
		return "storage-401-unauthorized"
	case strings.Contains(lower, "403") || strings.Contains(lower, "accessdenied") || strings.Contains(lower, "access denied"):
		return "storage-403-forbidden"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return "storage-timeout"
	case strings.Contains(lower, "disk usage high"):
		return "disk-usage-high"
	default:
		return truncateUploadNotificationText(strings.TrimSpace(lower), 180)
	}
}

func uploadFailureCause(summary *UploadSummary, runErr error) string {
	values := make([]string, 0, 1)
	if runErr != nil {
		values = append(values, runErr.Error())
	}
	if summary != nil {
		for _, failure := range summary.Failed {
			values = append(values, failure)
		}
	}
	for _, value := range values {
		switch normalizeUploadIssue(value) {
		case "storage-401-unauthorized":
			return "object storage returned 401 Unauthorized; verify or rotate the S3 credentials and ensure the endpoint belongs to the same account"
		case "storage-403-forbidden":
			return "object storage denied access; verify bucket scope and object read/write permissions"
		case "storage-timeout":
			return "object storage timed out; check endpoint reachability and provider health"
		}
	}
	return ""
}

func truncateUploadNotificationText(value string, max int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if max <= 0 || len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}
