package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/controller"
)

type channelProbeTaskContextKey struct{}

func TestChannelProbeTaskCanceledBeforeSelectionDoesNoWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	selected := false
	err := runChannelProbeTask(ctx, time.Now().UTC(),
		func(context.Context, time.Time) (controller.ChannelProbeSweepResult, error) {
			selected = true
			return controller.ChannelProbeSweepResult{}, nil
		},
	)
	if !errors.Is(err, context.Canceled) || selected {
		t.Fatalf("canceled task performed work: selected=%t err=%v", selected, err)
	}
}

func TestChannelProbeTaskKeepsTheScheduledContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), channelProbeTaskContextKey{}, "owned-task")
	err := runChannelProbeTask(ctx, time.Now().UTC(),
		func(got context.Context, _ time.Time) (controller.ChannelProbeSweepResult, error) {
			if got.Value(channelProbeTaskContextKey{}) != "owned-task" {
				return controller.ChannelProbeSweepResult{}, errors.New("candidate selection lost the scheduled task context")
			}
			return controller.ChannelProbeSweepResult{}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

type channelHealthBudgetTestTask struct {
	name string
	run  func(context.Context) error
}

func (task channelHealthBudgetTestTask) Name() string            { return task.name }
func (task channelHealthBudgetTestTask) Interval() time.Duration { return time.Minute }
func (task channelHealthBudgetTestTask) Run(ctx context.Context) error {
	return task.run(ctx)
}

func TestChannelHealthFailureDoesNotCancelFollowingFinancialTask(t *testing.T) {
	parent := context.WithValue(context.Background(), channelProbeTaskContextKey{}, "financial-still-live")
	financialRuns := 0
	err := RunOnce(parent,
		channelHealthBudgetTestTask{name: "channel-health-diagnostic", run: func(ctx context.Context) error {
			return runChannelProbeTask(ctx, time.Now().UTC(),
				func(context.Context, time.Time) (controller.ChannelProbeSweepResult, error) {
					return controller.ChannelProbeSweepResult{}, context.DeadlineExceeded
				},
			)
		}},
		channelHealthBudgetTestTask{name: "financial-following-test", run: func(ctx context.Context) error {
			financialRuns++
			if ctx.Err() != nil || ctx.Value(channelProbeTaskContextKey{}) != "financial-still-live" {
				t.Error("health diagnostic timeout leaked into the following task")
			}
			return nil
		}},
	)
	if !errors.Is(err, context.DeadlineExceeded) || financialRuns != 1 {
		t.Fatalf("diagnostic failure skipped financial work: runs=%d err=%v", financialRuns, err)
	}
}
