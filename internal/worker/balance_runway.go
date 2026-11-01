package worker

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/service"
)

// BalanceRunwayTask warns accounts whose balance will not cover the next few
// days of their own spend, and the operator with them. Hourly: a day's
// runway changes slowly, and the archive keeps each account to one alert a
// day whatever the cadence.
type BalanceRunwayTask struct{}

func NewBalanceRunwayTask() *BalanceRunwayTask       { return &BalanceRunwayTask{} }
func (t *BalanceRunwayTask) Name() string            { return "balance_runway" }
func (t *BalanceRunwayTask) Interval() time.Duration { return time.Hour }
func (t *BalanceRunwayTask) Run(ctx context.Context) error {
	return service.RunBalanceRunwayAlerts(ctx, time.Now())
}
