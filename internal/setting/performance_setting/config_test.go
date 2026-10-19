package performance_setting

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestPerformanceSettingSync(t *testing.T) {
	original := common.GetPerformanceMonitorConfig()
	t.Cleanup(func() {
		common.SetPerformanceMonitorConfig(original)
	})

	setting := &PerformanceSetting{
		MonitorEnabled:                    true,
		MonitorCPUThreshold:               88,
		MonitorMemoryThreshold:            77,
		MonitorDiskThreshold:              66,
		MonitorActiveConnectionsThreshold: 55,
	}
	setting.Sync()

	config := common.GetPerformanceMonitorConfig()
	if !config.Enabled {
		t.Fatalf("expected performance monitor to be enabled")
	}
	if config.CPUThreshold != 88 || config.MemoryThreshold != 77 || config.DiskThreshold != 66 || config.ActiveConnectionsThreshold != 55 {
		t.Fatalf("unexpected synced config: %+v", config)
	}
}
