package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

type runOnceTestTask struct {
	err  error
	runs int
}

func (t *runOnceTestTask) Name() string            { return "run_once_test" }
func (t *runOnceTestTask) Interval() time.Duration { return time.Minute }
func (t *runOnceTestTask) Run(context.Context) error {
	t.runs++
	return t.err
}

func TestRunOnceExecutesTaskExactlyOnce(t *testing.T) {
	task := &runOnceTestTask{}
	if err := RunOnce(context.Background(), task); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if task.runs != 1 {
		t.Fatalf("expected one task run, got %d", task.runs)
	}
}

func TestRunOnceReturnsTaskError(t *testing.T) {
	expected := errors.New("boom")
	task := &runOnceTestTask{err: expected}
	err := RunOnce(context.Background(), task)
	if !errors.Is(err, expected) {
		t.Fatalf("expected joined task error, got %v", err)
	}
}

func TestRunOnceRejectsEmptySelection(t *testing.T) {
	if err := RunOnce(context.Background()); err == nil {
		t.Fatal("expected empty selection error")
	}
}
