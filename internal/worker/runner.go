package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// Task represents a scheduled unit of work.
type Task interface {
	Name() string
	Interval() time.Duration
	Run(ctx context.Context) error
}

type InitialDelayTask interface {
	InitialDelay(now time.Time) time.Duration
}

// Run launches each task in its own goroutine and blocks until the context is canceled.
func Run(ctx context.Context, tasks ...Task) {
	if len(tasks) == 0 {
		common.SysLog("worker: no tasks configured, exiting")
		return
	}

	var wg sync.WaitGroup
	for _, task := range tasks {
		wg.Add(1)
		task := task
		common.SafeCtxGo(ctx, "worker:"+task.Name(), func() {
			defer wg.Done()
			executeLoop(ctx, task)
		})
	}

	<-ctx.Done()
	wg.Wait()
}

// RunOnce executes each task exactly once and returns a joined error when one
// or more tasks fail. It is intended for external schedulers such as
// Cloudflare Cron Triggers, where the scheduler owns the interval and the
// container process should exit after completing a bounded batch.
func RunOnce(ctx context.Context, tasks ...Task) error {
	if len(tasks) == 0 {
		return errors.New("worker: no tasks selected")
	}

	var taskErrors []error
	for _, task := range tasks {
		if err := task.Run(ctx); err != nil {
			common.SysLog(fmt.Sprintf("worker task %s error: %v", task.Name(), err))
			taskErrors = append(taskErrors, fmt.Errorf("%s: %w", task.Name(), err))
			continue
		}
		common.SysLog(fmt.Sprintf("worker task %s completed", task.Name()))
	}
	return errors.Join(taskErrors...)
}

func executeLoop(ctx context.Context, task Task) {
	interval := task.Interval()
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if delayed, ok := task.(InitialDelayTask); ok {
		delay := delayed.InitialDelay(time.Now().UTC())
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
	runOnce(ctx, task)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce(ctx, task)
		}
	}
}

func runOnce(ctx context.Context, task Task) {
	err := task.Run(ctx)
	if err != nil {
		common.SysLog(fmt.Sprintf("worker task %s error: %v", task.Name(), err))
	} else {
		common.SysLog(fmt.Sprintf("worker task %s completed", task.Name()))
	}
	noteTaskResult(ctx, task, err)
}
