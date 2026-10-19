package controller

import (
	"fmt"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/logger"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func writeRelayError(c *gin.Context, relayFormat types.RelayFormat, ws *websocket.Conn, lazuErr *types.LazuError) {
	requestID := c.GetString(common.RequestIdKey)
	if requestID != "" {
		c.Header("X-Request-Id", requestID)
	}
	wire := lazuErr.Wire()
	if wire.ID != "" {
		c.Header("X-Lazu-Error", wire.ID)
	}
	if wire.Audience != types.AudienceCustomer {
		// Operator and internal causes are answered with a neutral sentence;
		// keep the precise one next to the request ID for whoever reads logs.
		logger.LogError(c.Request.Context(), fmt.Sprintf("%s %s request_id=%s cause=%s",
			wire.ID, lazuErr.GetErrorCode(), requestID, lazuErr.MaskSensitiveError()))
	}
	// The retries are spent and this is what the customer's client will print,
	// so it is the last chance to say what they can do about it.
	annotateLaneFailure(c, lazuErr)
	common.SetContextKey(c, constant.ContextKeyRequestErrorMessage, lazuErr.MaskSensitiveError())
	common.SetContextKey(c, constant.ContextKeyRequestErrorCode, string(lazuErr.GetErrorCode()))
	common.SetContextKey(c, constant.ContextKeyRequestErrorType, string(lazuErr.GetErrorType()))
	if common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamTerminal) {
		return
	}

	switch relayFormat {
	case types.RelayFormatDecisions:
		httperr.WriteOpenAI(c, lazuErr)
		return
	case types.RelayFormatOpenAIRealtime:
		openAIError := lazuErr.ToOpenAIError()
		openAIError.RequestID = requestID
		helper.WssError(c, ws, openAIError)
	case types.RelayFormatClaude:
		c.JSON(wire.Status, gin.H{
			"type":       "error",
			"error":      lazuErr.ToClaudeError(),
			"request_id": requestID,
		})
	default:
		if common.GetContextKeyBool(c, constant.ContextKeyIsStream) ||
			strings.HasPrefix(c.Writer.Header().Get("Content-Type"), "text/event-stream") {
			relay.SafeStreamLazuErrorForFormat(c, relayFormat, lazuErr)
			return
		}
		openAIError := lazuErr.ToOpenAIError()
		openAIError.RequestID = requestID
		c.JSON(wire.Status, gin.H{
			"error": openAIError,
		})
	}
}

// recordRelayRejection persists a relay failure that no channel produced.
// Channel errors go through processChannelError, which already writes both the
// DB row and the file entry and then marks "error_logged_to_file"; this path
// covers everything rejected before/instead of an upstream call — budget
// limits (member/key/project), insufficient quota, invalid requests.
//
// A request attributed to a team project must reach the logs table: the
// console's project attention aggregate, the "被拒 N 次" chips and the
// result=rejected&reason=<code> filter all read it. Non-project traffic keeps
// the previous file-only behaviour byte for byte.
func recordRelayRejection(c *gin.Context, lazuErr *types.LazuError) {
	if lazuErr == nil || c.GetBool("error_logged_to_file") || !types.IsRecordErrorLog(lazuErr) {
		return
	}

	userId := c.GetInt("id")
	tokenName := c.GetString("token_name")
	// "original_model" only exists once a channel was picked; budget rejections
	// fire before that, so fall back to the model the request asked for.
	modelName := common.RequestedModelName(c)
	tokenId := c.GetInt("token_id")
	userGroup := c.GetString("group")
	username := c.GetString("username")

	other := make(map[string]interface{})
	if c.Request != nil && c.Request.URL != nil {
		other["request_path"] = c.Request.URL.Path
		other["request_method"] = c.Request.Method
	}
	other["request_id"] = c.GetString(common.RequestIdKey)
	other["duration_ms"] = service.RequestDurationMS(c)
	other["usage_unavailable"] = true
	other["any_provider_attempted"] = common.GetContextKeyBool(c, constant.ContextKeyAnyProviderAttempted)
	other["error_type"] = string(lazuErr.GetErrorType())
	other["error_code"] = string(lazuErr.GetErrorCode())
	other["status_code"] = lazuErr.StatusCode

	// RecordErrorLog reads RejectionReason from ContextKeyRequestErrorCode
	// (set by writeRelayError just above) and falls back to other["error_code"];
	// either way the column holds the contract code, not a message.
	if common.GetContextKeyInt(c, constant.ContextKeyProjectID) > 0 && model.TelemetryDB() != nil {
		model.RecordErrorLog(c, userId, 0, modelName, tokenName, lazuErr.MaskSensitiveError(), tokenId, int(service.RequestDurationMS(c)/1000),
			common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}

	// 记录非渠道错误到错误日志文件（渠道错误已在 processChannelError 中记录）
	if !constant.ErrorLogEnabled {
		return
	}
	service.RecordErrorLogToFile(service.ErrorLogEntry{
		UserId:    userId,
		Username:  username,
		ChannelId: 0,
		ModelName: modelName,
		TokenName: tokenName,
		TokenId:   tokenId,
		Content:   lazuErr.Error(),
		UseTime:   int(service.RequestDurationMS(c) / 1000),
		IsStream:  common.GetContextKeyBool(c, constant.ContextKeyIsStream),
		Group:     userGroup,
		// Use the real client IP from our trusted proxy chain;
		// c.ClientIP() returns the proxy peer address, which is
		// useless for triage.
		Ip:    common.GetRealClientIP(c),
		Other: other,
	})
}
