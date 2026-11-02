package model

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// Retention is split per table by data shape, not by a single LOG_RETENTION_DAYS:
//   - logs / log_insights: raw per-request rows. Controlled by LOG_RETENTION_DAYS
//     (default 30; automatic cleanup enforces the 30-day ranking window).
//   - usage_data: per-user × model × hour aggregate. Small, kept 365d so users
//     can see annual consumption history.
const usageDataRetentionDays = 365

func PurgeLogData(cutoff time.Time) {
	if cutoff.IsZero() {
		return
	}
	cutoffUnix := cutoff.Unix()
	now := time.Now()
	usageCutoff := now.AddDate(0, 0, -usageDataRetentionDays).Unix()

	if TelemetryDB() != nil {
		var err error
		if TelemetryDB().Dialector.Name() == "postgres" {
			_, err = ExpireRequestLogDetails(context.Background(), cutoff)
		} else {
			_, err = DeleteOldLog(context.Background(), cutoffUnix, 5000)
		}
		if err != nil {
			common.SysError("failed to purge logs: " + err.Error())
		}
		if err := TelemetryDB().Where("created_at < ?", usageCutoff).Delete(&UsageData{}).Error; err != nil {
			common.SysError("failed to purge usage_data: " + err.Error())
		}
	}
}
