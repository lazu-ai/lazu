package worker

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/pkg/pricecalendar"
)

type PricingCalendarTask struct{}

func NewPricingCalendarTask() *PricingCalendarTask     { return &PricingCalendarTask{} }
func (t *PricingCalendarTask) Name() string            { return "pricing_calendar" }
func (t *PricingCalendarTask) Interval() time.Duration { return 24 * time.Hour }
func (t *PricingCalendarTask) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Task errors use the worker's existing administrator alert path.
	return pricecalendar.CheckCoverage(time.Now())
}
