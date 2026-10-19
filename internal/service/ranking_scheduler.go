package service

import (
	"errors"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/robfig/cron/v3"
)

var rankingCron *cron.Cron

// StartModelRankingScheduler refreshes ranking scores on a schedule.
func StartModelRankingScheduler() {
	if !common.IsMasterNode {
		return
	}
	if rankingCron != nil {
		return
	}
	loc := time.Local
	rankingCron = cron.New(cron.WithLocation(loc))

	// Rolling windows are rebuilt hourly; calendar resets would race with
	// this refresh and incorrectly erase the newly published counts.
	_, _ = rankingCron.AddFunc("0 * * * *", func() {
		if err := RunModelRankingRefresh(); err != nil {
			common.SysLog("ranking update failed: " + err.Error())
		}
	})

	rankingCron.Start()
}

// RunModelRankingRefresh rebuilds the sliding usage counters before scoring.
// It is idempotent and safe to invoke from an external hourly scheduler.
func RunModelRankingRefresh() error {
	return errors.Join(
		model.UpdateWeeklyRequestsFromLogs(),
		model.UpdateMonthlyRequestsFromLogs(),
		model.UpdateAllRankScores(),
	)
}
