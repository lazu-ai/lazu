package common

import "testing"

func TestGetSystemOverloadReasons(t *testing.T) {
	config := PerformanceMonitorConfig{
		Enabled:                    true,
		CPUThreshold:               80,
		MemoryThreshold:            70,
		DiskThreshold:              60,
		ActiveConnectionsThreshold: 100,
	}

	reasons := GetSystemOverloadReasons(SystemStatus{
		CPUUsage:    81.5,
		MemoryUsage: 70.2,
		DiskUsage:   59.9,
	}, config, 100)

	if len(reasons) != 3 {
		t.Fatalf("expected 3 overload reasons, got %d: %v", len(reasons), reasons)
	}
	if reasons[0] == "" || reasons[1] == "" || reasons[2] == "" {
		t.Fatalf("expected non-empty overload reasons, got %v", reasons)
	}
}

func TestGetSystemOverloadReasonsDisabled(t *testing.T) {
	reasons := GetSystemOverloadReasons(SystemStatus{
		CPUUsage:    99,
		MemoryUsage: 99,
		DiskUsage:   99,
	}, PerformanceMonitorConfig{
		Enabled:                    false,
		CPUThreshold:               80,
		MemoryThreshold:            80,
		DiskThreshold:              80,
		ActiveConnectionsThreshold: 10,
	}, 999)

	if len(reasons) != 0 {
		t.Fatalf("expected no overload reasons when monitoring is disabled, got %v", reasons)
	}
}
