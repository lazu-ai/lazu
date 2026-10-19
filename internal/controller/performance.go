package controller

import (
	"runtime"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"

	"github.com/gin-gonic/gin"
)

type RuntimeStats struct {
	Alloc         uint64 `json:"alloc"`
	TotalAlloc    uint64 `json:"total_alloc"`
	Sys           uint64 `json:"sys"`
	NumGC         uint32 `json:"num_gc"`
	NumGoroutine  int    `json:"num_goroutine"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

type PerformanceStatsConfig struct {
	MonitorEnabled                    bool   `json:"monitor_enabled"`
	MonitorCPUThreshold               int    `json:"monitor_cpu_threshold"`
	MonitorMemoryThreshold            int    `json:"monitor_memory_threshold"`
	MonitorDiskThreshold              int    `json:"monitor_disk_threshold"`
	MonitorActiveConnectionsThreshold int    `json:"monitor_active_connections_threshold"`
	MonitorDiskPath                   string `json:"monitor_disk_path"`
	IsRunningInContainer              bool   `json:"is_running_in_container"`
	SampleIntervalSeconds             int    `json:"sample_interval_seconds"`
}

type PerformanceStats struct {
	SystemStatus    common.SystemStatus           `json:"system_status"`
	DiskSpaceInfo   common.DiskSpaceInfo          `json:"disk_space_info"`
	RuntimeStats    RuntimeStats                  `json:"runtime_stats"`
	HTTPStats       middleware.StatsInfo          `json:"http_stats"`
	HotPathStats    common.HotPathMetricsSnapshot `json:"hot_path_stats"`
	IdentityL1      model.IdentityL1Stats         `json:"identity_l1"`
	SQLPoolStats    map[string]model.SQLPoolStats `json:"sql_pool_stats"`
	Config          PerformanceStatsConfig        `json:"config"`
	Overloaded      bool                          `json:"overloaded"`
	OverloadReasons []string                      `json:"overload_reasons"`
	Now             int64                         `json:"now"`
}

func GetPerformanceStats(c *gin.Context) {
	config := common.GetPerformanceMonitorConfig()
	status := common.GetSystemStatus()
	httpStats := middleware.GetStats()
	reasons := common.GetSystemOverloadReasons(status, config, httpStats.ActiveConnections)
	diskSpaceInfo := common.GetDiskSpaceInfo()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	stats := PerformanceStats{
		SystemStatus:  status,
		DiskSpaceInfo: diskSpaceInfo,
		RuntimeStats: RuntimeStats{
			Alloc:         memStats.Alloc,
			TotalAlloc:    memStats.TotalAlloc,
			Sys:           memStats.Sys,
			NumGC:         memStats.NumGC,
			NumGoroutine:  runtime.NumGoroutine(),
			UptimeSeconds: time.Now().Unix() - common.StartTime,
		},
		HTTPStats:    httpStats,
		HotPathStats: common.SnapshotHotPathMetrics(),
		IdentityL1:   model.SnapshotIdentityL1(),
		SQLPoolStats: model.SnapshotSQLPoolStats(),
		Config: PerformanceStatsConfig{
			MonitorEnabled:                    config.Enabled,
			MonitorCPUThreshold:               config.CPUThreshold,
			MonitorMemoryThreshold:            config.MemoryThreshold,
			MonitorDiskThreshold:              config.DiskThreshold,
			MonitorActiveConnectionsThreshold: config.ActiveConnectionsThreshold,
			MonitorDiskPath:                   common.GetSystemMonitorDiskPath(),
			IsRunningInContainer:              common.IsRunningInContainer(),
			SampleIntervalSeconds:             5,
		},
		Overloaded:      len(reasons) > 0,
		OverloadReasons: reasons,
		Now:             time.Now().Unix(),
	}

	common.ApiSuccess(c, stats)
}
