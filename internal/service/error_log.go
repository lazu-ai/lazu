package service

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
)

// ErrorLogEntry 错误日志条目结构
type ErrorLogEntry struct {
	Timestamp string                 `json:"timestamp"`
	UserId    int                    `json:"user_id"`
	Username  string                 `json:"username"`
	ChannelId int                    `json:"channel_id"`
	ModelName string                 `json:"model_name"`
	TokenName string                 `json:"token_name"`
	TokenId   int                    `json:"token_id"`
	Content   string                 `json:"content"`
	UseTime   int                    `json:"use_time"`
	IsStream  bool                   `json:"is_stream"`
	Group     string                 `json:"group"`
	Ip        string                 `json:"ip,omitempty"`
	Other     map[string]interface{} `json:"other,omitempty"`
}

const maxErrorLogFileSize = 128 << 20 // 128 MiB

var (
	errorLogWriterOnce sync.Once
	errorLogWriter     *RotatingLogWriter
)

// getErrorLogWriter 获取错误日志写入器单例
// 注意：将检查逻辑放在 sync.Once 内部，避免 ErrorLogEnabled 动态变化时的边界情况
func getErrorLogWriter() *RotatingLogWriter {
	errorLogWriterOnce.Do(func() {
		errorLogWriter = NewRotatingLogWriter(common.ErrorLogPath, maxErrorLogFileSize, func() bool {
			return constant.ErrorLogEnabled && common.ErrorLogPath != ""
		})
	})
	return errorLogWriter
}

// CloseErrorLogWriter 关闭错误日志写入器，在程序优雅退出时调用
// 如果功能未启用（ErrorLogEnabled=false），writer 不会被初始化，返回 nil 表示无需关闭
func CloseErrorLogWriter() error {
	if errorLogWriter == nil {
		// 功能未启用或从未写入过日志，无需关闭
		return nil
	}
	return errorLogWriter.Close()
}

// RecordErrorLogToFile 将错误日志异步写入文件，并触发错误率监控
func RecordErrorLogToFile(entry ErrorLogEntry) {
	if !constant.ErrorLogEnabled || common.ErrorLogPath == "" {
		return
	}

	// 记录到错误率监控
	GetErrorRateMonitor().RecordError(entry)

	common.SafeGo("error-log-append", func() {
		if err := appendErrorLogEntry(entry); err != nil {
			common.SysError(fmt.Sprintf("failed to append error log entry: %v", err))
		}
	})
}

func appendErrorLogEntry(entry ErrorLogEntry) error {
	if common.ErrorLogPath == "" {
		return fmt.Errorf("error log path is empty")
	}

	entry.Timestamp = nowBeijing().Format(time.RFC3339Nano)

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	writer := getErrorLogWriter()
	return writer.Append(data)
}
