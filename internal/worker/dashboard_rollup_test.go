package worker

import "testing"

func TestDashboardUsageRollupOneShotSkipsBootstrap(t *testing.T) {
	task := NewDashboardUsageRollupOneShotTask()
	if !task.bootstrapped {
		t.Fatal("expected one-shot rollup to use the steady-state refresh window")
	}
}
