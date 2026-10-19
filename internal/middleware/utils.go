package middleware

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

// abortWithCode ends a /v1 request with a catalog error in its protocol's
// envelope. Everything on the wire — code, type, status, LZ number, English
// message — comes from the catalog; details fill the message template and are
// returned to the caller.
func abortWithCode(c *gin.Context, code types.ErrorCode, details ...types.D) {
	abortWithCodeStatus(c, 0, code, details...)
}

// abortWithCodeStatus is abortWithCode for a public row whose status varies
// by situation (e.g. preferred_lane_unavailable is 404 when the lane does not
// offer the model and 503 when it does but is down).
func abortWithCodeStatus(c *gin.Context, status int, code types.ErrorCode, details ...types.D) {
	var merged types.D
	for _, d := range details {
		if merged == nil {
			merged = types.D{}
		}
		for k, v := range d {
			merged[k] = v
		}
	}
	w := types.RenderWire(code, merged, status)
	body := httperr.Body{
		Message: w.Message,
		Type:    w.Type,
		Code:    string(w.Code),
		Lazu:    w.ID,
		Docs:    w.Docs,
	}
	if w.Audience == types.AudienceCustomer && len(merged) > 0 {
		body.Details = merged
		if field, ok := merged["field"].(string); ok {
			body.Param = field
		}
	}
	abortWithGatewayError(c, w.Status, body, code)
}

func abortRequestBodyError(c *gin.Context, err error) {
	abortWithCode(c, types.NewRequestBodyError(err).GetErrorCode())
}

// abortWithGatewayError writes the rendered body and records the rejection.
// internalCode is the precise code for operator logs; the body carries the
// public one.
func abortWithGatewayError(c *gin.Context, statusCode int, body httperr.Body, internalCode types.ErrorCode) {
	userId := c.GetInt("id")
	requestID := c.GetString(common.RequestIdKey)
	body.RequestID = requestID
	message := body.Message
	codeStr := body.Code
	httperr.WriteGatewayBody(c, statusCode, body)
	common.SetContextKey(c, constant.ContextKeyRequestErrorMessage, message)
	common.SetContextKey(c, constant.ContextKeyRequestErrorCode, codeStr)
	common.SetContextKey(c, constant.ContextKeyRequestErrorType, "auth_or_validation")
	c.Abort()
	modelName := common.RequestedModelName(c)
	if common.GetContextKeyInt(c, constant.ContextKeyProjectID) > 0 && model.TelemetryDB() != nil {
		model.RecordErrorLog(c, userId, 0, modelName, c.GetString("token_name"),
			message, c.GetInt("token_id"), 0, false, c.GetString("group"), map[string]interface{}{"error_code": codeStr, "status_code": statusCode})
	}
	logger.LogError(c.Request.Context(), fmt.Sprintf("user %d | %s", userId, message))

	// 记录到错误日志文件
	if constant.ErrorLogEnabled {
		other := make(map[string]interface{})
		if c.Request != nil && c.Request.URL != nil {
			other["request_path"] = c.Request.URL.Path
			other["request_method"] = c.Request.Method
		}
		other["request_id"] = c.GetString(common.RequestIdKey)
		other["duration_ms"] = service.RequestDurationMS(c)
		other["usage_unavailable"] = true
		other["error_type"] = "auth_or_validation"
		other["error_code"] = codeStr
		other["internal_error_code"] = string(internalCode)
		other["status_code"] = statusCode
		if userAgent := c.GetHeader("User-Agent"); userAgent != "" {
			other["user_agent"] = userAgent
		}

		service.RecordErrorLogToFile(service.ErrorLogEntry{
			UserId:    userId,
			Username:  c.GetString("username"),
			ChannelId: 0,
			ModelName: modelName,
			TokenName: c.GetString("token_name"),
			TokenId:   c.GetInt("token_id"),
			Content:   message,
			UseTime:   int(service.RequestDurationMS(c) / 1000),
			IsStream:  false,
			Group:     c.GetString("group"),
			// Use the real client IP from the trusted proxy chain; c.ClientIP()
			// returns the proxy peer address, which is useless for triage.
			Ip:    common.GetRealClientIP(c),
			Other: other,
		})
	}
}
