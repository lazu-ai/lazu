package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

const (
	loggerINFO  = "INFO"
	loggerWarn  = "WARN"
	loggerError = "ERR"
	loggerDebug = "DEBUG"
)

const maxLogCount = 1000000

var logCount atomic.Int64
var setupLogLock sync.RWMutex
var setupLogWorking atomic.Bool

func SetupLogger() {
	if *common.LogDir != "" {
		ok := setupLogLock.TryLock()
		if !ok {
			log.Println("setup log is already working")
			return
		}
		defer func() {
			setupLogLock.Unlock()
		}()
		logPath := filepath.Join(*common.LogDir, fmt.Sprintf("lazu-%s.log", time.Now().Format("20060102150405")))
		fd, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal("failed to open log file")
		}
		gin.DefaultWriter = io.MultiWriter(os.Stdout, fd)
		gin.DefaultErrorWriter = io.MultiWriter(os.Stderr, fd)
	}
}

func LogInfo(ctx context.Context, msg string) {
	if !common.ShouldLog(common.LogLevelInfo) {
		return
	}
	logHelper(ctx, loggerINFO, msg)
}

func LogWarn(ctx context.Context, msg string) {
	if !common.ShouldLog(common.LogLevelWarn) {
		return
	}
	logHelper(ctx, loggerWarn, msg)
}

func LogError(ctx context.Context, msg string) {
	if !common.ShouldLog(common.LogLevelError) {
		return
	}
	logHelper(ctx, loggerError, msg)
}

func LogDebug(ctx context.Context, msg string) {
	if !common.ShouldLog(common.LogLevelDebug) {
		return
	}
	logHelper(ctx, loggerDebug, msg)
}

func logHelper(ctx context.Context, level string, msg string) {
	setupLogLock.RLock()
	writer := gin.DefaultErrorWriter
	if level == loggerINFO {
		writer = gin.DefaultWriter
	}
	setupLogLock.RUnlock()
	id := any("SYSTEM")
	if ctx != nil {
		id = ctx.Value(common.RequestIdKey)
	}
	if id == nil {
		id = "SYSTEM"
	}
	now := time.Now()
	_, _ = fmt.Fprintf(writer, "[%s] %v | %s | %s \n", level, now.Format("2006/01/02 - 15:04:05"), id, msg)
	if logCount.Add(1) > maxLogCount && setupLogWorking.CompareAndSwap(false, true) {
		logCount.Store(0)
		common.SafeGo("logger-setup", func() {
			defer setupLogWorking.Store(false)
			SetupLogger()
		})
	}
}

func LogQuota(quota billing.MicroUSD) string {
	q := float64(quota)
	switch operation_setting.GetQuotaDisplayType() {
	case operation_setting.QuotaDisplayTypeTokens:
		return fmt.Sprintf("%d 点额度", quota)
	default: // USD
		return fmt.Sprintf("＄%.6f 额度", q/float64(billing.MicroUSDPerUSD))
	}
}

func FormatQuota(quota billing.MicroUSD) string {
	q := float64(quota)
	switch operation_setting.GetQuotaDisplayType() {
	case operation_setting.QuotaDisplayTypeTokens:
		return fmt.Sprintf("%d", quota)
	default:
		return fmt.Sprintf("＄%.6f", q/float64(billing.MicroUSDPerUSD))
	}
}

// LogJson 仅供测试使用 only for test
func LogJson(ctx context.Context, msg string, obj any) {
	jsonStr, err := json.Marshal(obj)
	if err != nil {
		LogError(ctx, fmt.Sprintf("json marshal failed: %s", err.Error()))
		return
	}
	LogDebug(ctx, fmt.Sprintf("%s | %s", msg, string(jsonStr)))
}
