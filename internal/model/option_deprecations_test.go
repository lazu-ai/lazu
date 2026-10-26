package model

import (
	"regexp"
	"strings"
	"testing"
)

func TestDeprecatedOptionNoticesOnlyForConfiguredValues(t *testing.T) {
	notices := deprecatedOptionNotices([]*Option{
		{Key: "LogConsumeEnabled", Value: "true"},
		{Key: "WorkerUrl", Value: "  "},
		{Key: "WorkerValidKey", Value: "secret"},
		{Key: "general_setting.quota_display_type", Value: "USD"},
		{Key: "DataExportEnabled", Value: "false"},
		nil,
	})
	if len(notices) != 0 {
		t.Fatalf("defaults must stay quiet, got %v", notices)
	}
	notices = deprecatedOptionNotices([]*Option{
		{Key: "LogConsumeEnabled", Value: "false"},
		{Key: "WorkerUrl", Value: "https://worker.example"},
		{Key: "general_setting.quota_display_type", Value: "TOKENS"},
		{Key: "DataExportEnabled", Value: "true"},
	})
	if len(notices) != 4 || !strings.Contains(notices[0], "LogConsumeEnabled") || !strings.Contains(notices[1], "WorkerUrl") || !strings.Contains(notices[2], "TOKENS") || !strings.Contains(notices[3], "DataExportEnabled") {
		t.Fatalf("notices = %v", notices)
	}
	for _, notice := range notices {
		if !removalDate.MatchString(notice) || strings.Contains(notice, "secret") {
			t.Fatalf("notice must name the removal date and no value: %q", notice)
		}
	}
}

var removalDate = regexp.MustCompile(`removed after \d{4}-\d{2}-\d{2}`)
