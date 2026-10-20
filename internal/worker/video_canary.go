package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/controller"
)

// VideoCanaryTask generates the smallest clip on video routes nothing else
// speaks for (see controller.RunVideoCanarySweep). It is its own task because
// a canary holds its run for minutes while the provider works, which would
// stall the liveness and text probes if it shared their sweep.
type VideoCanaryTask struct{}

func NewVideoCanaryTask() *VideoCanaryTask {
	return &VideoCanaryTask{}
}

func (t *VideoCanaryTask) Name() string {
	return "video_canary"
}

// Interval is the heartbeat; the per-route cadence is a day, an hour for a
// route tripped by failed generations.
func (t *VideoCanaryTask) Interval() time.Duration {
	return 10 * time.Minute
}

func (t *VideoCanaryTask) Run(ctx context.Context) error {
	result, err := controller.RunVideoCanarySweep(ctx, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("video canary sweep: %w", err)
	}
	if result.Probed > 0 || result.BudgetHit {
		common.SysLog(fmt.Sprintf(
			"video canary sweep: due=%d ran=%d failed=%d skipped=%d budget_hit=%t est_cost=$%.2f",
			result.Considered, result.Probed, result.Failures, result.Skipped, result.BudgetHit, result.EstimatedUSD))
	}
	return nil
}
