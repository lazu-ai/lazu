package middleware

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

// 不记录日志的路径前缀
var skipLogPaths = []string{
	"/api/uptime/status",
	"/api/option/",
	"/api/status",
	"/health",
	"/favicon.ico",
}

// 高频路径，只在 debug 模式下记录
var debugOnlyPaths = []string{
	"/api/user/self",
	"/api/data/",
}

func shouldSkipLog(path string) bool {
	for _, prefix := range skipLogPaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isDebugOnlyPath(path string) bool {
	for _, prefix := range debugOnlyPaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func accessLogEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("ACCESS_LOG")))
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return common.ShouldLog(common.LogLevelInfo)
}

func SetUpLogger(server *gin.Engine) {
	if !accessLogEnabled() {
		return
	}

	if strings.EqualFold(strings.TrimSpace(os.Getenv("ACCESS_LOG_FORMAT")), "json") {
		server.Use(func(c *gin.Context) {
			startedAt := time.Now()
			path := c.Request.URL.Path
			c.Next()
			if shouldSkipLog(path) || (isDebugOnlyPath(path) && !common.ShouldLog(common.LogLevelDebug)) {
				return
			}
			entry := map[string]any{
				"client_ip":  common.GetRealClientIP(c),
				"latency_ms": float64(time.Since(startedAt).Microseconds()) / 1000,
				"level":      "info",
				"method":     c.Request.Method,
				"msg":        "http_request",
				"node":       common.NodeName,
				"path":       path,
				"provider":   common.NodeProvider,
				"region":     common.NodeRegion,
				"request_id": c.GetString(common.RequestIdKey),
				"status":     c.Writer.Status(),
				"time":       time.Now().UTC().Format(time.RFC3339Nano),
			}
			encoded, err := json.Marshal(entry)
			if err == nil {
				_, _ = gin.DefaultWriter.Write(append(encoded, '\n'))
			}
		})
		return
	}

	server.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		// 跳过不重要的路径
		if shouldSkipLog(param.Path) {
			return ""
		}

		// 高频路径只在 debug 模式输出
		if isDebugOnlyPath(param.Path) && !common.ShouldLog(common.LogLevelDebug) {
			return ""
		}

		var requestID string
		if param.Keys != nil {
			requestID, _ = param.Keys[common.RequestIdKey].(string)
		}
		return fmt.Sprintf("[GIN] %s | %s | %3d | %13v | %15s | %7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			requestID,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			param.Path,
		)
	}))
}
