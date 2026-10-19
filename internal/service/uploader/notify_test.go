package uploader

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUploadIssueFingerprintGroupsFilesByRootCause(t *testing.T) {
	first := &UploadSummary{Failed: map[string]string{
		"archive/requests/one.jsonl": "operation error S3: PutObject, StatusCode: 401, Unauthorized",
	}}
	second := &UploadSummary{Failed: map[string]string{
		"archive/requests/two.jsonl": "StatusCode: 401 Unauthorized",
	}}
	if got, want := uploadIssueFingerprint(first, nil), uploadIssueFingerprint(second, nil); got != want {
		t.Fatalf("same root cause should share a fingerprint, got %q and %q", got, want)
	}
}

func TestUploadFailureCauseIsActionable(t *testing.T) {
	summary := &UploadSummary{Failed: map[string]string{
		"archive/errors/example.jsonl": "operation error S3: PutObject, StatusCode: 403 AccessDenied",
	}}
	cause := uploadFailureCause(summary, nil)
	if !strings.Contains(cause, "bucket scope") {
		t.Fatalf("expected actionable permission guidance, got %q", cause)
	}

	cause = uploadFailureCause(nil, errors.New("context deadline exceeded"))
	if !strings.Contains(cause, "timed out") {
		t.Fatalf("expected timeout guidance, got %q", cause)
	}
}

func TestTruncateUploadNotificationTextPreservesUTF8(t *testing.T) {
	got := truncateUploadNotificationText(strings.Repeat("错", 181), 180)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated text must remain valid UTF-8: %q", got)
	}
	if got != strings.Repeat("错", 180)+"…" {
		t.Fatalf("unexpected rune-safe truncation: %q", got)
	}
}

func TestPgDumpFailureFingerprintIncludesStderrCause(t *testing.T) {
	baseErr := errors.New("pg_dump exec: exit status 1")
	auth := pgDumpFailureFingerprint(PgDumpResult{Err: baseErr, DumpStderr: "password authentication failed"})
	permission := pgDumpFailureFingerprint(PgDumpResult{Err: baseErr, DumpStderr: "permission denied for schema public"})
	if auth == permission {
		t.Fatalf("different pg_dump stderr causes must change the fingerprint: %q", auth)
	}
}

func TestPgDumpFailureFingerprintExcludesTimestampedObjectKey(t *testing.T) {
	first := pgDumpFailureFingerprint(PgDumpResult{Err: errors.New("upload pg-dump/2026-08/lazu-2026-08-19_0530.sql.gz: connection reset")})
	second := pgDumpFailureFingerprint(PgDumpResult{Err: errors.New("upload pg-dump/2026-08/lazu-2026-08-20_0530.sql.gz: connection reset")})
	if first != second {
		t.Fatalf("timestamped object keys must not change the root-cause fingerprint: %q != %q", first, second)
	}
}
