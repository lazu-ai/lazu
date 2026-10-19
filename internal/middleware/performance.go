package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// SystemPerformanceCheck 在系统过载时拒绝新的重型 Relay / Task 请求。
func SystemPerformanceCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		lazuErr := checkSystemPerformance()
		if lazuErr == nil {
			c.Next()
			return
		}

		plainMessage := lazuErr.Error()
		common.SetContextKey(c, constant.ContextKeyRequestErrorMessage, plainMessage)
		common.SetContextKey(c, constant.ContextKeyRequestErrorCode, string(lazuErr.GetErrorCode()))
		common.SetContextKey(c, constant.ContextKeyRequestErrorType, "system_overloaded")

		path := c.Request.URL.Path
		openAIError := lazuErr.ToOpenAIError()
		openAIError.RequestID = c.GetString(common.RequestIdKey)
		if lazu := openAIError.Lazu; lazu != "" {
			c.Header("X-Lazu-Error", lazu)
		}
		if strings.HasPrefix(path, "/v1/messages") {
			c.JSON(lazuErr.WireStatus(), gin.H{
				"type":       "error",
				"error":      lazuErr.ToClaudeError(),
				"request_id": openAIError.RequestID,
			})
		} else {
			c.JSON(lazuErr.WireStatus(), gin.H{
				"error": openAIError,
			})
		}
		c.Abort()
	}
}

func checkSystemPerformance() *types.LazuError {
	config := common.GetPerformanceMonitorConfig()
	if !config.Enabled {
		return nil
	}

	status := common.GetSystemStatus()
	activeConnections := GetStats().ActiveConnections
	reasons := common.GetSystemOverloadReasons(status, config, activeConnections)
	switch {
	case config.CPUThreshold > 0 && status.CPUUsage >= float64(config.CPUThreshold):
		return newSystemOverloadedError(errors.New(reasons[0]))
	case config.MemoryThreshold > 0 && status.MemoryUsage >= float64(config.MemoryThreshold):
		return newSystemOverloadedError(errors.New(reasons[0]))
	case config.DiskThreshold > 0 && status.DiskUsage >= float64(config.DiskThreshold):
		return newSystemOverloadedError(errors.New(reasons[0]))
	case config.ActiveConnectionsThreshold > 0 && activeConnections >= int64(config.ActiveConnectionsThreshold):
		return newSystemOverloadedError(errors.New(reasons[0]))
	default:
		return nil
	}
}

func newSystemOverloadedError(err error) *types.LazuError {
	return types.NewErrorWithStatusCode(
		err,
		types.ErrorCodeGatewayOverloaded,
		http.StatusServiceUnavailable,
		types.ErrOptionWithSkipRetry(),
		types.ErrOptionWithNoRecordErrorLog(),
		types.ErrOptionWithDetails(map[string]string{"raw_message": common.MaskSensitiveInfo(err.Error())}),
	)
}
