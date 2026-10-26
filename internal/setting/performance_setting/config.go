package performance_setting

import (
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/config"
)

type PerformanceSetting struct {
	MonitorEnabled                    bool `json:"monitor_enabled"`
	MonitorCPUThreshold               int  `json:"monitor_cpu_threshold"`
	MonitorMemoryThreshold            int  `json:"monitor_memory_threshold"`
	MonitorDiskThreshold              int  `json:"monitor_disk_threshold"`
	MonitorActiveConnectionsThreshold int  `json:"monitor_active_connections_threshold"`
}

var performanceSetting = PerformanceSetting{
	MonitorEnabled:                    true,
	MonitorCPUThreshold:               90,
	MonitorMemoryThreshold:            90,
	MonitorDiskThreshold:              95,
	MonitorActiveConnectionsThreshold: 0,
}

func init() {
	config.GlobalConfig.Register("performance_setting", &performanceSetting)
	performanceSetting.Sync()
}

// Sync 将配置同步到运行态公共配置。
func (s *PerformanceSetting) Sync() {
	if s == nil {
		return
	}
	common.SetPerformanceMonitorConfig(common.PerformanceMonitorConfig{
		Enabled:                    s.MonitorEnabled,
		CPUThreshold:               s.MonitorCPUThreshold,
		MemoryThreshold:            s.MonitorMemoryThreshold,
		DiskThreshold:              s.MonitorDiskThreshold,
		ActiveConnectionsThreshold: s.MonitorActiveConnectionsThreshold,
	})
}
