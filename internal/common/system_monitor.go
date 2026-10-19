package common

import (
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
)

const systemMonitorInterval = 10 * time.Second

// DiskSpaceInfo 为当前采样路径所在文件系统的空间信息。
type DiskSpaceInfo struct {
	Path        string  `json:"path"`
	Total       uint64  `json:"total"`
	Free        uint64  `json:"free"`
	Used        uint64  `json:"used"`
	UsedPercent float64 `json:"used_percent"`
}

// SystemStatus 为最近一次采样的系统资源概览。
type SystemStatus struct {
	CPUUsage    float64 `json:"cpu_usage"`
	MemoryUsage float64 `json:"memory_usage"`
	DiskUsage   float64 `json:"disk_usage"`
	UpdatedAt   int64   `json:"updated_at"`
}

var latestSystemStatus atomic.Value

func init() {
	latestSystemStatus.Store(SystemStatus{})
}

// StartSystemMonitor 启动后台系统资源采样协程。
func StartSystemMonitor() {
	SafeGo("system-monitor", func() {
		for {
			updateSystemStatus()
			time.Sleep(systemMonitorInterval)
		}
	})
}

func updateSystemStatus() {
	status := SystemStatus{
		UpdatedAt: time.Now().Unix(),
	}

	if percents, err := cpu.Percent(0, false); err == nil && len(percents) > 0 {
		status.CPUUsage = percents[0]
	}

	if memInfo, err := mem.VirtualMemory(); err == nil {
		status.MemoryUsage = memInfo.UsedPercent
	}

	diskInfo := GetDiskSpaceInfo()
	status.DiskUsage = diskInfo.UsedPercent

	latestSystemStatus.Store(status)
}

func GetSystemStatus() SystemStatus {
	return latestSystemStatus.Load().(SystemStatus)
}
