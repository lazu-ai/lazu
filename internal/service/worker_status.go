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

// StartWorker 标记 worker 开始执行
func (m *WorkerStatusManager) StartWorker(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	status := &WorkerStatus{
		Name:      name,
		Status:    "running",
		StartTime: &now,
	}
	m.statuses[name] = status
}

// CompleteWorker 标记 worker 执行完成
func (m *WorkerStatusManager) CompleteWorker(name string, message string, result map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()

	status, exists := m.statuses[name]
	if !exists {
		status = &WorkerStatus{Name: name}
	}

	now := time.Now()
	status.Status = "completed"
	status.EndTime = &now
	status.Message = message
	status.Result = result
	status.LastRunTime = &now

	if status.StartTime != nil {
		duration := now.Sub(*status.StartTime)
		status.Duration = duration.Truncate(time.Millisecond).String()
	}

	m.statuses[name] = status
	m.addToHistory(name, status)
}

// FailWorker 标记 worker 执行失败
func (m *WorkerStatusManager) FailWorker(name string, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	status, exists := m.statuses[name]
	if !exists {
		status = &WorkerStatus{Name: name}
	}

	now := time.Now()
	status.Status = "failed"
	status.EndTime = &now
	status.Message = message
	status.LastRunTime = &now

	if status.StartTime != nil {
		duration := now.Sub(*status.StartTime)
		status.Duration = duration.Truncate(time.Millisecond).String()
	}

	m.statuses[name] = status
	m.addToHistory(name, status)
}

func (m *WorkerStatusManager) addToHistory(name string, status *WorkerStatus) {
	// 创建副本保存到历史
	historyCopy := &WorkerStatus{
		Name:        status.Name,
		Status:      status.Status,
		StartTime:   status.StartTime,
		EndTime:     status.EndTime,
		Duration:    status.Duration,
		Message:     status.Message,
		Result:      status.Result,
		LastRunTime: status.LastRunTime,
	}

	if m.history[name] == nil {
		m.history[name] = make([]*WorkerStatus, 0, maxHistoryPerWorker)
	}

	// 添加到历史，保持最大数量
	m.history[name] = append(m.history[name], historyCopy)
	if len(m.history[name]) > maxHistoryPerWorker {
		m.history[name] = m.history[name][1:]
	}
}

// GetStatus 获取单个 worker 的当前状态
func (m *WorkerStatusManager) GetStatus(name string) *WorkerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if status, exists := m.statuses[name]; exists {
		return status
	}
	return &WorkerStatus{Name: name, Status: "unknown"}
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

// GetHistory 获取单个 worker 的执行历史
func (m *WorkerStatusManager) GetHistory(name string) []*WorkerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if history, exists := m.history[name]; exists {
		result := make([]*WorkerStatus, len(history))
		copy(result, history)
		return result
	}
	return nil
}

// IsRunning 检查 worker 是否正在运行
func (m *WorkerStatusManager) IsRunning(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if status, exists := m.statuses[name]; exists {
		return status.Status == "running"
	}
	return false
}
