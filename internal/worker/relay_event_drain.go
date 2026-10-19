package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

type RelayEventDrainTask struct{}

func NewRelayEventDrainTask() *RelayEventDrainTask {
	return &RelayEventDrainTask{}
}

func (t *RelayEventDrainTask) Name() string {
	return "relay_event_drain"
}

func (t *RelayEventDrainTask) Interval() time.Duration {
	return time.Minute
}

func (t *RelayEventDrainTask) Run(ctx context.Context) error {
	maxBatches := common.GetEnvOrDefault("RELAY_EVENT_DRAIN_MAX_BATCHES", 64)
	timeBudget := time.Duration(common.GetEnvOrDefault("RELAY_EVENT_DRAIN_TIME_BUDGET_SECONDS", 45)) * time.Second
	if timeBudget <= 0 || timeBudget >= time.Minute {
		timeBudget = 45 * time.Second
	}
	consumer := fmt.Sprintf("%s-%d", firstNonEmptyWorkerValue(common.NodeName, "relay-event-jobs"), os.Getpid())

	drainCtx, cancel := context.WithTimeout(ctx, timeBudget)
	defer cancel()
	if common.DataExportEnabled {
		defer model.SaveUsageDataCache()
	}

	totalProcessed := 0
	for {
		processed, err := model.DrainRelayEvents(drainCtx, consumer, maxBatches)
		totalProcessed += processed
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				common.SysLog(fmt.Sprintf(
					"relay event drain reached %s time budget after processing %d events",
					timeBudget,
					totalProcessed,
				))
				return nil
			}
			return err
		}
		if processed == 0 {
			common.SysLog(fmt.Sprintf("relay event drain processed %d events and reached idle", totalProcessed))
			return nil
		}
	}
}

func firstNonEmptyWorkerValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
