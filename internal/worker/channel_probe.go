package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/controller"
)

// ChannelProbeTask keeps stability evidence fresh for routes that real traffic
// does not cover. It wakes often but does very little most of the time: the
// candidate selection skips anything with recent traffic or a recent probe, so
// a busy platform normally probes nothing at all.
type ChannelProbeTask struct{}

func NewChannelProbeTask() *ChannelProbeTask {
	return &ChannelProbeTask{}
}

func (t *ChannelProbeTask) Name() string {
	return "channel_probe"
}

// Interval is the scheduler's heartbeat, not the probe cadence. Per-route
// cadence comes from the idle tiers inside candidate selection.
func (t *ChannelProbeTask) Interval() time.Duration {
	return 10 * time.Minute
}

// Run probes idle routes, then runs the source-observation sweep even when
// the probe sweep failed. Health pages no longer depend on this task: they
// read the hourly rollup (RouteHealthRollupTask), which computes from raw
// facts on its own schedule.
func (t *ChannelProbeTask) Run(ctx context.Context) error {
	now := time.Now().UTC()
	probeErr := runChannelProbeTask(ctx, now, controller.RunChannelProbeSweep)
	if ctx.Err() != nil {
		return errors.Join(probeErr, ctx.Err())
	}
	return errors.Join(probeErr, runRouteObservationTask(ctx, now, controller.RunRouteObservationSweep))
}

// runRouteObservationTask runs the checks that watched routes are owed. It
// follows the liveness sweep so the two draw on one daily budget in a fixed
// order: liveness first, since every route needs it.
func runRouteObservationTask(
	ctx context.Context,
	now time.Time,
	runSweep func(context.Context, time.Time) (controller.ChannelProbeSweepResult, error),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := runSweep(ctx, now)
	if err != nil {
		return fmt.Errorf("route observation sweep: %w", err)
	}
	if result.Probed > 0 || result.BudgetHit {
		common.SysLog(fmt.Sprintf(
			"route observation sweep: due=%d ran=%d failed=%d skipped=%d budget_hit=%t est_cost=$%.5f",
			result.Considered, result.Probed, result.Failures, result.Skipped, result.BudgetHit, result.EstimatedUSD))
	}
	return nil
}

func runChannelProbeTask(
	ctx context.Context,
	now time.Time,
	runSweep func(context.Context, time.Time) (controller.ChannelProbeSweepResult, error),
) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	result, err := runSweep(ctx, now)
	if err != nil {
		return fmt.Errorf("channel probe sweep: %w", err)
	}
	if result.Probed > 0 || result.BudgetHit {
		common.SysLog(fmt.Sprintf(
			"channel probe sweep: considered=%d probed=%d failures=%d skipped=%d budget_hit=%t est_cost=$%.5f",
			result.Considered, result.Probed, result.Failures, result.Skipped, result.BudgetHit, result.EstimatedUSD))
	}
	return nil
}
