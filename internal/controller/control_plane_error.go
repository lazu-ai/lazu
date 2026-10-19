package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

// writeControlPlaneAPIError answers a /v1 control-plane route (files, media
// jobs, usage summary, video jobs) in the OpenAI envelope. Requests made with
// a project Key also leave a rejection row, which the project console reads.
func writeControlPlaneAPIError(c *gin.Context, err error) {
	status, body := httperr.Resolve(c, err)
	if common.GetContextKeyInt(c, constant.ContextKeyProjectID) > 0 && model.TelemetryDB() != nil {
		common.SetContextKey(c, constant.ContextKeyRequestErrorCode, body.Code)
		model.RecordErrorLog(c, c.GetInt("id"), 0, c.GetString("model"), c.GetString("token_name"), body.Message, c.GetInt("token_id"), 0, false, c.GetString("group"), map[string]interface{}{"error_code": body.Code, "status_code": status})
	}
	if body.RequestID != "" {
		c.Header("X-Request-Id", body.RequestID)
	}
	if body.Lazu != "" {
		c.Header(httperr.HeaderLazuError, body.Lazu)
	}
	c.JSON(status, gin.H{"error": body})
}

// writeControlPlaneCatalogError is writeControlPlaneAPIError for a code.
func writeControlPlaneCatalogError(c *gin.Context, code types.ErrorCode, details ...types.D) {
	writeControlPlaneAPIError(c, types.Err(code, details...))
}

// writeInternalControlPlaneError logs err and answers internal_error.
func writeInternalControlPlaneError(c *gin.Context, err error) {
	writeControlPlaneAPIError(c, types.WrapErr(err, types.ErrorCodeInternalError))
}
