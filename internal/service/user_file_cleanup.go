package service

import (
	"context"
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service/alerting"
	"github.com/robfig/cron/v3"
)

var userFileCleanupCron *cron.Cron

func StartUserFileCleanup() {
	if !common.IsMasterNode {
		return
	}
	if userFileCleanupCron != nil {
		return
	}
	// Align with the other backup/maintenance jobs so all cron slots
	// are reasoned about in one timezone. 04:30 sits between the hourly
	// JSONL upload (:00) and pg_dump (05:30) so they do not contend
	// for the same R2 connection burst.
	loc, err := time.LoadLocation(common.AppConfig.Upload.Timezone)
	if err != nil || loc == nil {
		loc = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	userFileCleanupCron = cron.New(cron.WithLocation(loc))
	_, _ = userFileCleanupCron.AddFunc("30 4 * * *", func() {
		_ = RunUserFileCleanupOnce(NowUnix())
	})
	userFileCleanupCron.Start()
}

// RunUserFileCleanupOnce performs one bounded cleanup pass for external
// schedulers such as the Cloudflare Jobs Worker.
func RunUserFileCleanupOnce(now int64) error {
	stats := CleanupExpiredUserFiles(now)
	common.SysLog(fmt.Sprintf("user file cleanup: scanned=%d deleted=%d failed=%d", stats.Scanned, stats.Deleted, stats.Failed))
	notifyUserFileCleanupResult(stats)
	if stats.Failed > 0 {
		return fmt.Errorf("user file cleanup failed for %d item(s)", stats.Failed)
	}
	return nil
}

func notifyUserFileCleanupResult(stats UserFileCleanupStats) {
	summary := fmt.Sprintf("scanned=%d deleted=%d failed=%d", stats.Scanned, stats.Deleted, stats.Failed)
	healthSummary := fmt.Sprintf("检查 %d 个文件，删除 %d 个，失败 %d 个", stats.Scanned, stats.Deleted, stats.Failed)
	if stats.Failed == 0 {
		alerting.RecordHealth("user-file-cleanup", "User file cleanup", "ok", healthSummary)
		alerting.Resolve("user-file-cleanup", "✅ Lazu user-file cleanup recovered")
		return
	}
	alerting.RecordHealth("user-file-cleanup", "User file cleanup", "failed", healthSummary)
	alerting.Report(alerting.Incident{
		Key:         "user-file-cleanup",
		Fingerprint: "user-file-delete-failures",
		Priority:    alerting.PriorityMaintenance,
		Message:     "⚠️ Lazu user-file cleanup had failures\n" + summary,
	})
}

type UserFileCleanupStats struct {
	Scanned int
	Deleted int
	Failed  int
}

func CleanupExpiredUserFiles(now int64) UserFileCleanupStats {
	stats := UserFileCleanupStats{}
	files, err := model.ListExpiredFiles(now, 1000)
	if err != nil {
		stats.Failed++
		common.SysError("user file cleanup: list expired files failed: " + err.Error())
		return stats
	}
	if len(files) == 0 {
		return stats
	}
	storageClient, storageErr := EnsureUserFileStorageClient()
	for _, file := range files {
		stats.Scanned++
		if storageErr != nil {
			stats.Failed++
			common.SysError(fmt.Sprintf("user file cleanup: storage unavailable file=%s key=%s err=%v", file.ID, file.StorageKey, storageErr))
			continue
		}
		if err := storageClient.Delete(context.Background(), file.StorageKey); err != nil {
			stats.Failed++
			common.SysError(fmt.Sprintf("user file cleanup: delete object failed file=%s key=%s err=%v", file.ID, file.StorageKey, err))
			continue
		}
		if err := model.DeleteExpiredFileRecord(&file); err != nil {
			stats.Failed++
			common.SysError(fmt.Sprintf("user file cleanup: delete db record failed file=%s err=%v", file.ID, err))
			continue
		}
		stats.Deleted++
	}
	return stats
}
