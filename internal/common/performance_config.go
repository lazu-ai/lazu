package common

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// PerformanceMonitorConfig 定义系统性能监控阈值
type PerformanceMonitorConfig struct {
	Enabled                    bool `json:"enabled"`
	CPUThreshold               int  `json:"cpu_threshold"`
	MemoryThreshold            int  `json:"memory_threshold"`
	DiskThreshold              int  `json:"disk_threshold"`
	ActiveConnectionsThreshold int  `json:"active_connections_threshold"`
}

var performanceMonitorConfig atomic.Value

func init() {
	performanceMonitorConfig.Store(PerformanceMonitorConfig{
		Enabled:                    true,
		CPUThreshold:               90,
		MemoryThreshold:            90,
		DiskThreshold:              95,
		ActiveConnectionsThreshold: 0,
	})
}

func GetPerformanceMonitorConfig() PerformanceMonitorConfig {
	return performanceMonitorConfig.Load().(PerformanceMonitorConfig)
}

func SetPerformanceMonitorConfig(config PerformanceMonitorConfig) {
	performanceMonitorConfig.Store(config)
}

// GetSystemMonitorDiskPath 返回用于磁盘占用采样的路径。
func GetSystemMonitorDiskPath() string {
	candidates := []string{
		GetDataDir(),
		".",
		os.TempDir(),
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		absPath, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		info, statErr := os.Stat(absPath)
		if statErr != nil {
			continue
		}
		if info.IsDir() {
			return absPath
		}
		return filepath.Dir(absPath)
	}

	return os.TempDir()
}

// GetSystemOverloadReasons 根据当前系统状态与阈值返回过载原因。
func GetSystemOverloadReasons(status SystemStatus, config PerformanceMonitorConfig, activeConnections int64) []string {
	if !config.Enabled {
		return nil
	}

	reasons := make([]string, 0, 4)
	if config.CPUThreshold > 0 && status.CPUUsage >= float64(config.CPUThreshold) {
		reasons = append(reasons, fmt.Sprintf("system cpu overloaded (current: %.1f%%, threshold: %d%%)", status.CPUUsage, config.CPUThreshold))
	}
	if config.MemoryThreshold > 0 && status.MemoryUsage >= float64(config.MemoryThreshold) {
		reasons = append(reasons, fmt.Sprintf("system memory overloaded (current: %.1f%%, threshold: %d%%)", status.MemoryUsage, config.MemoryThreshold))
	}
	if config.DiskThreshold > 0 && status.DiskUsage >= float64(config.DiskThreshold) {
		reasons = append(reasons, fmt.Sprintf("system disk overloaded (current: %.1f%%, threshold: %d%%)", status.DiskUsage, config.DiskThreshold))
	}
	if config.ActiveConnectionsThreshold > 0 && activeConnections >= int64(config.ActiveConnectionsThreshold) {
		reasons = append(reasons, fmt.Sprintf("system active connections overloaded (current: %d, threshold: %d)", activeConnections, config.ActiveConnectionsThreshold))
	}
	return reasons
}
