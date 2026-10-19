package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/controller"
)

func TestRunChannelProbeTaskReportsSweepFailure(t *testing.T) {
	want := errors.New("candidate selection failed")
	err := runChannelProbeTask(context.Background(), time.Now().UTC(),
		func(context.Context, time.Time) (controller.ChannelProbeSweepResult, error) {
			return controller.ChannelProbeSweepResult{}, want
		},
	)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want the sweep failure", err)
	}
}
