package worker

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

type DashboardUsageRollupTask struct {
	bootstrapped bool
}

func NewDashboardUsageRollupTask() *DashboardUsageRollupTask {
	return &DashboardUsageRollupTask{}
}

// NewDashboardUsageRollupOneShotTask skips the historical bootstrap. External
// schedulers create a fresh process for every run, so bootstrapping there would
// repeatedly rebuild the full historical window instead of refreshing recent
// buckets.
func NewDashboardUsageRollupOneShotTask() *DashboardUsageRollupTask {
	return &DashboardUsageRollupTask{bootstrapped: true}
}

func (t *DashboardUsageRollupTask) Name() string {
	return "dashboard_usage_rollup"
}

func (t *DashboardUsageRollupTask) Interval() time.Duration {
	return time.Duration(common.GetEnvOrDefault("DASHBOARD_ROLLUP_INTERVAL_SECONDS", 60)) * time.Second
}

func (t *DashboardUsageRollupTask) Run(ctx context.Context) error {
	if !common.GetEnvOrDefaultBool("DASHBOARD_ROLLUP_WORKER_ENABLED", true) {
		return nil
	}
	// Rollups are committed incrementally by the telemetry writer. Historical
	// repairs are explicit migrations, never repeated by every jobs restart.
	if err := model.DrainTelemetryCleanupJobs(); err != nil {
		return err
	}
	if err := model.EnsureDashboardRollupHours(ctx, time.Now()); err != nil {
		return err
	}
	return model.RefreshTelemetryChannelProjection(ctx)
}
