package service

import (
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

var logRetentionStarted bool

const maxLogRetentionDays = int64((1<<63 - 1) / (24 * time.Hour))

func StartLogRetention() {
	if logRetentionStarted || !common.IsMasterNode {
		return
	}
	logRetentionStarted = true
	go runLogRetention()
}

func runLogRetention() {
	intervalHours := common.LogRetentionIntervalHours
	if intervalHours <= 0 {
		intervalHours = 24
	}

	RunLogRetentionOnce(time.Now())
	ticker := time.NewTicker(time.Duration(intervalHours) * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		RunLogRetentionOnce(time.Now())
	}
}

// RunLogRetentionOnce performs the bounded database cleanup used by external
// schedulers. Local API runtimes can keep using StartLogRetention.
func RunLogRetentionOnce(now time.Time) {
	for _, failure := range model.RunExtensionRetention(now.UTC()) {
		common.SysError(failure.Error())
	}
	// Content retention is independent from the request-metadata/ranking window.
	// Disabling LOG_RETENTION_DAYS must not leave another copy of prompts forever.
	contentDays := common.RequestContentRetentionDays()
	if contentDays > 0 && int64(contentDays) <= maxLogRetentionDays {
		if err := model.PurgeAgentSessionContent(now.UTC().AddDate(0, 0, -contentDays)); err != nil {
			common.SysError("failed to purge agent session content: " + err.Error())
		} else {
			common.SysLog("agent session content retention completed; cutoff=" + now.UTC().AddDate(0, 0, -contentDays).Format(time.RFC3339))
		}
	}
	if common.LogRetentionDays <= 0 {
		common.SysDebug("log retention disabled")
		return
	}
	days := common.LogRetentionDays
	if int64(days) > maxLogRetentionDays {
		common.SysError(fmt.Sprintf("LOG_RETENTION_DAYS=%d exceeds the supported maximum of %d; skipping database log cleanup", days, maxLogRetentionDays))
		return
	}
	if days < model.ModelRankingWindowDays {
		common.SysWarn(fmt.Sprintf("LOG_RETENTION_DAYS=%d is shorter than the model ranking window; retaining %d days of database logs", days, model.ModelRankingWindowDays))
		days = model.ModelRankingWindowDays
	}
	// Ranking windows use elapsed 24-hour days, including across DST changes.
	model.PurgeLogData(now.Add(-time.Duration(days) * 24 * time.Hour))
}
