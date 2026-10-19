package middleware

import (
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// HTTPStats 存储HTTP统计信息
type HTTPStats struct {
	activeConnections int64
	requestCount      int64
	errorCount        int64
	totalDurationUS   int64
	maxDurationUS     int64
}

var globalStats = &HTTPStats{}

// StatsMiddleware 统计中间件
func StatsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		// 增加活跃连接数
		atomic.AddInt64(&globalStats.activeConnections, 1)

		// 确保在请求结束时减少连接数
		defer func() {
			atomic.AddInt64(&globalStats.activeConnections, -1)
			atomic.AddInt64(&globalStats.requestCount, 1)
			if c.Writer.Status() >= 500 {
				atomic.AddInt64(&globalStats.errorCount, 1)
			}
			durationUS := time.Since(startedAt).Microseconds()
			atomic.AddInt64(&globalStats.totalDurationUS, durationUS)
			for {
				current := atomic.LoadInt64(&globalStats.maxDurationUS)
				if durationUS <= current || atomic.CompareAndSwapInt64(&globalStats.maxDurationUS, current, durationUS) {
					break
				}
			}
		}()

		c.Next()
	}
}

// StatsInfo 统计信息结构
type StatsInfo struct {
	ActiveConnections int64 `json:"active_connections"`
	RequestCount      int64 `json:"request_count"`
	ErrorCount        int64 `json:"error_count"`
	TotalDurationUS   int64 `json:"total_duration_us"`
	MaxDurationUS     int64 `json:"max_duration_us"`
}

// GetStats 获取统计信息
func GetStats() StatsInfo {
	return StatsInfo{
		ActiveConnections: atomic.LoadInt64(&globalStats.activeConnections),
		RequestCount:      atomic.LoadInt64(&globalStats.requestCount),
		ErrorCount:        atomic.LoadInt64(&globalStats.errorCount),
		TotalDurationUS:   atomic.LoadInt64(&globalStats.totalDurationUS),
		MaxDurationUS:     atomic.LoadInt64(&globalStats.maxDurationUS),
	}
}
