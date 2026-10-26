package service

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
)

// ErrorRateMonitor 监控错误率并在超过阈值时发送告警邮件
type ErrorRateMonitor struct {
	// 配置
	windowDuration time.Duration // 监控窗口时长
	threshold      int           // 告警阈值（窗口内错误数）
	cooldown       time.Duration // 发送告警后的冷却时间

	// 状态
	mu            sync.Mutex
	errorCount    int64
	windowStart   time.Time
	lastAlertTime time.Time

	// 最近错误样本（用于告警邮件）
	recentErrors     []ErrorLogEntry
	maxRecentErrors  int
	recentErrorsLock sync.Mutex
}

var (
	errorRateMonitor     *ErrorRateMonitor
	errorRateMonitorOnce sync.Once
)

// DefaultErrorRateConfig 默认配置
const (
	defaultWindowDuration = 5 * time.Minute  // 5分钟窗口
	defaultThreshold      = 100              // 100个错误触发告警
	defaultCooldown       = 30 * time.Minute // 30分钟冷却
	defaultMaxRecent      = 10               // 保留最近10个错误样本
)

// GetErrorRateMonitor 获取错误率监控单例
func GetErrorRateMonitor() *ErrorRateMonitor {
	errorRateMonitorOnce.Do(func() {
		errorRateMonitor = &ErrorRateMonitor{
			windowDuration:  defaultWindowDuration,
			threshold:       defaultThreshold,
			cooldown:        defaultCooldown,
			windowStart:     time.Now(),
			maxRecentErrors: defaultMaxRecent,
			recentErrors:    make([]ErrorLogEntry, 0, defaultMaxRecent),
		}
	})
	return errorRateMonitor
}

// RecordError 记录一个错误并检查是否需要告警
func (m *ErrorRateMonitor) RecordError(entry ErrorLogEntry) {
	if !constant.ErrorLogEnabled {
		return
	}

	now := time.Now()

	m.mu.Lock()
	// 检查是否需要重置窗口
	if now.Sub(m.windowStart) > m.windowDuration {
		m.errorCount = 0
		m.windowStart = now
		m.clearRecentErrors()
	}

	atomic.AddInt64(&m.errorCount, 1)
	count := atomic.LoadInt64(&m.errorCount)
	windowStart := m.windowStart
	lastAlert := m.lastAlertTime
	m.mu.Unlock()

	// 保存最近的错误样本
	m.addRecentError(entry)

	// 检查是否需要发送告警
	if count >= int64(m.threshold) && now.Sub(lastAlert) > m.cooldown {
		m.mu.Lock()
		// 双重检查
		if atomic.LoadInt64(&m.errorCount) >= int64(m.threshold) && now.Sub(m.lastAlertTime) > m.cooldown {
			m.lastAlertTime = now
			recentErrors := m.getRecentErrorsCopy()
			m.mu.Unlock()

			// 异步发送告警邮件
			common.SafeGo("error-rate-alert-email", func() {
				m.sendAlertEmail(count, windowStart, recentErrors)
			})
		} else {
			m.mu.Unlock()
		}
	}
}

func (m *ErrorRateMonitor) addRecentError(entry ErrorLogEntry) {
	m.recentErrorsLock.Lock()
	defer m.recentErrorsLock.Unlock()

	if len(m.recentErrors) >= m.maxRecentErrors {
		// 移除最旧的
		m.recentErrors = m.recentErrors[1:]
	}
	m.recentErrors = append(m.recentErrors, entry)
}

func (m *ErrorRateMonitor) clearRecentErrors() {
	m.recentErrorsLock.Lock()
	defer m.recentErrorsLock.Unlock()
	m.recentErrors = m.recentErrors[:0]
}

func (m *ErrorRateMonitor) getRecentErrorsCopy() []ErrorLogEntry {
	m.recentErrorsLock.Lock()
	defer m.recentErrorsLock.Unlock()
	result := make([]ErrorLogEntry, len(m.recentErrors))
	copy(result, m.recentErrors)
	return result
}

func (m *ErrorRateMonitor) sendAlertEmail(errorCount int64, windowStart time.Time, recentErrors []ErrorLogEntry) {
	window := strings.TrimSuffix(m.windowDuration.String(), "0s") // 5m0s -> 5m
	facts := []common.MailFact{
		{Label: "窗口", Value: windowStart.Format("15:04:05") + " 起 " + window},
		{Label: "阈值", Value: fmt.Sprintf("%d / %s", m.threshold, window)},
	}
	counts := map[int]int{}
	top, topCount := 0, 0
	for _, entry := range recentErrors {
		counts[entry.ChannelId]++
		if counts[entry.ChannelId] > topCount {
			top, topCount = entry.ChannelId, counts[entry.ChannelId]
		}
	}
	if topCount > 0 {
		facts = append(facts, common.MailFact{Label: "样本最多的通道", Value: fmt.Sprintf("#%d · %d / %d", top, topCount, len(recentErrors))})
	}
	facts = append(facts, common.MailFact{Label: "下次告警最早", Value: time.Now().Add(m.cooldown).Format("15:04")})
	blocks := []common.MailBlock{{Facts: facts}}
	if len(recentErrors) > 0 {
		table := &common.MailTable{Head: []string{"时间", "通道", "模型", "错误"}}
		for _, entry := range recentErrors {
			timestamp := entry.Timestamp
			if len(timestamp) > 19 {
				timestamp = timestamp[11:19]
			}
			content := entry.Content
			if len([]rune(content)) > 80 {
				content = string([]rune(content)[:80]) + "…"
			}
			table.Rows = append(table.Rows, []string{timestamp, fmt.Sprintf("%d", entry.ChannelId), entry.ModelName, content})
		}
		blocks = append(blocks, common.MailBlock{Table: table})
	}
	blocks = append(blocks, common.MailBlock{Primary: &common.MailAction{Label: "查看错误日志", URL: common.MailSiteURL("/console/usage")}})
	title := fmt.Sprintf("%s 内 %d 个错误", window, errorCount)
	if err := SendOpsMail(title, common.Mail{
		Source: "ERROR RATE", Severity: "ALERT", Title: title,
		Preheader: fmt.Sprintf("阈值 %d / %s", m.threshold, window), Blocks: blocks,
	}); err != nil {
		common.SysError("error rate alert: " + err.Error())
	}
}
