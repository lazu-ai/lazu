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
		{name: NewDecisionRefundTask().Name(), interval: NewDecisionRefundTask().Interval()},
		{name: NewChannelMaintenanceTask().Name(), interval: NewChannelMaintenanceTask().Interval()},
		{name: NewPgDumpBackupTask().Name(), interval: NewPgDumpBackupTask().Interval()},
		{name: NewArchiveCompactorTask().Name(), interval: NewArchiveCompactorTask().Interval()},
	}
	want := map[string]time.Duration{
		"log_retention":       24 * time.Hour,
		"user_file_cleanup":   24 * time.Hour,
		"decision_refunds":    15 * time.Second,
		"channel_maintenance": time.Minute,
		"pg_dump_backup":      24 * time.Hour,
		"archive_compactor":   24 * time.Hour,
	}
	for _, test := range tests {
		if want[test.name] != test.interval {
			t.Fatalf("unexpected maintenance task %q interval %s", test.name, test.interval)
		}
	}
}
