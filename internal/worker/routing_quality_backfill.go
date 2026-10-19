package worker

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
)

type RoutingQualityBackfillTask struct{}

const routingQualityBackfillTimeout = 30 * time.Second

func NewRoutingQualityBackfillTask() *RoutingQualityBackfillTask {
	return &RoutingQualityBackfillTask{}
}

func (t *RoutingQualityBackfillTask) Name() string {
	return "routing_quality_backfill"
}

func (t *RoutingQualityBackfillTask) Interval() time.Duration {
	return time.Minute
}

func (t *RoutingQualityBackfillTask) Run(ctx context.Context) error {
	// This diagnostic task must not monopolize the Telemetry store or delay
	// later financial tasks in a run-once worker after its budget expires.
	runCtx, cancel := context.WithTimeout(ctx, routingQualityBackfillTimeout)
	defer cancel()
	return BackfillQualitySignalsContext(runCtx)
}

func BackfillQualitySignals() error {
	return BackfillQualitySignalsContext(context.Background())
}

func BackfillQualitySignalsContext(ctx context.Context) error {
	return BackfillQualitySignalsAtContext(ctx, time.Now().UTC())
}

func BackfillQualitySignalsAt(now time.Time) error {
	return BackfillQualitySignalsAtContext(context.Background(), now)
}

func BackfillQualitySignalsAtContext(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	outcomes, err := model.QueryOutcomesNeedingBackfillContext(ctx, now.Add(-1*time.Hour), now.Add(-5*time.Minute))
	if err != nil {
		return routingQualityBackfillError(ctx, err)
	}
	for _, outcome := range outcomes {
		if err := ctx.Err(); err != nil {
			return err
		}
		decision, err := model.GetDecisionByRequestIDContext(ctx, outcome.RequestID)
		if err != nil || decision == nil {
			// Missing decisions can arrive later, but cancellation must not be
			// mistaken for a missing row and silently reported as success.
			if err := ctx.Err(); err != nil {
				return err
			}
			continue
		}
		start := outcome.OutcomeAt
		retried, err := model.HasUserRetriedContext(ctx, decision.UserID, decision.PromptHash, start, start.Add(5*time.Minute))
		if err != nil {
			return routingQualityBackfillError(ctx, err)
		}
		switched, err := model.HasUserSwitchedModelContext(ctx, decision.SessionID, decision.ResolvedModel, start, start.Add(5*time.Minute))
		if err != nil {
			return routingQualityBackfillError(ctx, err)
		}
		if err := model.UpdateOutcomeQualitySignalsContext(ctx, outcome.RequestID, retried, switched); err != nil {
			return routingQualityBackfillError(ctx, err)
		}
	}
	return ctx.Err()
}

func routingQualityBackfillError(ctx context.Context, err error) error {
	// Driver interruption and an automatic rollback can replace the original
	// context error. Keep cancellation recognizable to the scheduler/caller.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
