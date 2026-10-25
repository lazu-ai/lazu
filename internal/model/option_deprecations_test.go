package model

import (
	"strings"
	"testing"
)

func TestDeprecatedOptionNoticesOnlyForConfiguredValues(t *testing.T) {
	notices := deprecatedOptionNotices([]*Option{
		{Key: "LogConsumeEnabled", Value: "true"},
		{Key: "WorkerUrl", Value: "  "},
		{Key: "WorkerValidKey", Value: "secret"},
		{Key: "general_setting.quota_display_type", Value: "USD"},
		nil,
	})
	if len(notices) != 0 {
		t.Fatalf("defaults must stay quiet, got %v", notices)
	}
	notices = deprecatedOptionNotices([]*Option{
		{Key: "LogConsumeEnabled", Value: "false"},
		{Key: "WorkerUrl", Value: "https://worker.example"},
		{Key: "general_setting.quota_display_type", Value: "TOKENS"},
	})
	if len(notices) != 3 || !strings.Contains(notices[0], "LogConsumeEnabled") || !strings.Contains(notices[1], "WorkerUrl") || !strings.Contains(notices[2], "TOKENS") {
		t.Fatalf("notices = %v", notices)
	}
	for _, notice := range notices {
		if !strings.Contains(notice, "2027-01-24") || strings.Contains(notice, "secret") {
			t.Fatalf("notice must name the removal date and no value: %q", notice)
		}
	}
}
