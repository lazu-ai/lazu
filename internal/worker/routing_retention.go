package worker

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

type RoutingRetentionTask struct{}

func NewRoutingRetentionTask() *RoutingRetentionTask {
	return &RoutingRetentionTask{}
}

func (t *RoutingRetentionTask) Name() string {
	return "routing_retention"
}

func (t *RoutingRetentionTask) Interval() time.Duration {
	return 24 * time.Hour
}

func (t *RoutingRetentionTask) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return PurgeOldRoutingData()
	}
}

func PurgeOldRoutingData() error {
	return PurgeOldRoutingDataAt(time.Now().UTC())
}

func PurgeOldRoutingDataAt(now time.Time) error {
	if model.TelemetryDB() == nil {
		return nil
	}
	if model.TelemetryDB().Dialector.Name() == "postgres" {
		if err := model.MaintainTelemetryPartitions(context.Background(), now, common.LogRetentionDays); err != nil {
			return err
		}
	} else {
		for _, entry := range []struct {
			row    interface{}
			column string
			days   int
		}{{&model.RoutingAttemptOutcome{}, "outcome_at", model.RoutingAttemptRetentionDays}, {&model.RoutingDecision{}, "decision_at", model.RoutingDecisionRetentionDays}, {&model.RoutingOutcome{}, "outcome_at", model.RoutingOutcomeRetentionDays}} {
			if model.TelemetryDB().Migrator().HasTable(entry.row) {
				if err := model.TelemetryDB().Where(entry.column+" < ?", now.AddDate(0, 0, -entry.days)).Delete(entry.row).Error; err != nil {
					return err
				}
			}
		}
	}
	if err := model.PurgeOldChannelProbeResults(now.AddDate(0, 0, -model.ChannelProbeRetentionDays)); err != nil {
		return err
	}
	if err := model.PurgeOldSearchHealthSamples(now.AddDate(0, 0, -30)); err != nil {
		return err
	}
	if err := model.PurgeOrphanRouteHealthStates(); err != nil {
		return err
	}
	return model.PurgeStaleRouteHealthStates(now.AddDate(0, 0, -7))
}
