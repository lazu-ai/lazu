package worker

import (
	"testing"
	"time"
)

func TestMaintenanceTaskContracts(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
	}{
		{name: NewLogRetentionTask().Name(), interval: NewLogRetentionTask().Interval()},
		{name: NewUserFileCleanupTask().Name(), interval: NewUserFileCleanupTask().Interval()},
		{name: NewModelRankingRefreshTask().Name(), interval: NewModelRankingRefreshTask().Interval()},
		{name: NewChannelMaintenanceTask().Name(), interval: NewChannelMaintenanceTask().Interval()},
		{name: NewPgDumpBackupTask().Name(), interval: NewPgDumpBackupTask().Interval()},
		{name: NewArchiveCompactorTask().Name(), interval: NewArchiveCompactorTask().Interval()},
	}
	want := map[string]time.Duration{
		"log_retention":         24 * time.Hour,
		"user_file_cleanup":     24 * time.Hour,
		"model_ranking_refresh": time.Hour,
		"channel_maintenance":   time.Minute,
		"pg_dump_backup":        24 * time.Hour,
		"archive_compactor":     24 * time.Hour,
	}
	for _, test := range tests {
		if want[test.name] != test.interval {
			t.Fatalf("unexpected maintenance task %q interval %s", test.name, test.interval)
		}
	}
}
