package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

type namedTestTask struct{ name string }

func (t namedTestTask) Name() string              { return t.name }
func (t namedTestTask) Interval() time.Duration   { return time.Minute }
func (t namedTestTask) Run(context.Context) error { return nil }

func TestTaskFailureCountResetsOnSuccess(t *testing.T) {
	task := namedTestTask{name: "ops-watch-test"}
	ctx := context.Background()
	defer taskFailures.Delete(task.name)
	for i := 1; i <= 2; i++ {
		noteTaskResult(ctx, task, errors.New("boom"))
		if got, _ := taskFailures.Load(task.name); got != i {
			t.Fatalf("after %d failures count=%v", i, got)
		}
	}
	noteTaskResult(ctx, task, nil)
	if _, ok := taskFailures.Load(task.name); ok {
		t.Fatal("a successful run must reset the streak")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	noteTaskResult(cancelled, task, context.Canceled)
	if _, ok := taskFailures.Load(task.name); ok {
		t.Fatal("shutdown is not a failing run")
	}
}
