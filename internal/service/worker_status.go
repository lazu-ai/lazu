package service

import (
	"sync"
	"time"
)

// WorkerStatus 表示一个 worker 任务的状态
type WorkerStatus struct {
	Name        string         `json:"name"`
	Status      string         `json:"status"` // idle, running, completed, failed
	StartTime   *time.Time     `json:"start_time,omitempty"`
	EndTime     *time.Time     `json:"end_time,omitempty"`
	Duration    string         `json:"duration,omitempty"`
	Message     string         `json:"message,omitempty"`
	Result      map[string]any `json:"result,omitempty"`
	LastRunTime *time.Time     `json:"last_run_time,omitempty"`
}

// WorkerStatusManager 管理所有 worker 的状态
type WorkerStatusManager struct {
	mu       sync.RWMutex
	statuses map[string]*WorkerStatus
	history  map[string][]*WorkerStatus // 每个 worker 保留最近 N 次执行记录
}

const maxHistoryPerWorker = 10

var (
	workerStatusManager     *WorkerStatusManager
	workerStatusManagerOnce sync.Once
)

// GetWorkerStatusManager 获取 worker 状态管理器单例
func GetWorkerStatusManager() *WorkerStatusManager {
	workerStatusManagerOnce.Do(func() {
		workerStatusManager = &WorkerStatusManager{
			statuses: make(map[string]*WorkerStatus),
			history:  make(map[string][]*WorkerStatus),
		}
		// 初始化已知的 worker
		for _, name := range []string{"object-upload", "channel-test"} {
			workerStatusManager.statuses[name] = &WorkerStatus{
				Name:   name,
				Status: "idle",
			}
		}
	})
	return workerStatusManager
}

// GetAllStatuses 获取所有 worker 的状态
func (m *WorkerStatusManager) GetAllStatuses() map[string]*WorkerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*WorkerStatus)
	for k, v := range m.statuses {
		result[k] = v
	}
	return result
}
