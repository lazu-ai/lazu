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
		nil,
	})
	if len(notices) != 0 {
		t.Fatalf("defaults must stay quiet, got %v", notices)
	}
	notices = deprecatedOptionNotices([]*Option{
		{Key: "LogConsumeEnabled", Value: "false"},
		{Key: "WorkerUrl", Value: "https://worker.example"},
	})
	if len(notices) != 2 || !strings.Contains(notices[0], "LogConsumeEnabled") || !strings.Contains(notices[1], "WorkerUrl") {
		t.Fatalf("notices = %v", notices)
	}
	for _, notice := range notices {
		if !strings.Contains(notice, "2027-01-24") || strings.Contains(notice, "secret") {
			t.Fatalf("notice must name the removal date and no value: %q", notice)
		}
	}
}
