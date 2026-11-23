package model

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// MinimumLogRetentionDays preserves the history used by rollups and investigations.
const MinimumLogRetentionDays = 30

func PurgeLogData(cutoff time.Time) {
	if cutoff.IsZero() {
		return
	}
	cutoffUnix := cutoff.Unix()

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
	}
}
