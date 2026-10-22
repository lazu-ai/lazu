package controller

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

// SubmitSupportReport mails the reader's problem report to support.
func SubmitSupportReport(c *gin.Context) {
	var report service.SupportReport
	if err := c.ShouldBindJSON(&report); err != nil {
		httperr.Code(c, types.ErrorCodeSupportReportInvalid, types.D{"reason": "the body must be a JSON object"})
		return
	}
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	err = service.SubmitSupportReport(c.Request.Context(), user, report, c.Request.UserAgent())
	var invalid *service.SupportReportInvalid
	switch {
	case err == nil:
		common.ApiSuccess(c, gin.H{"sent": true})
	case errors.As(err, &invalid):
		httperr.Code(c, types.ErrorCodeSupportReportInvalid, types.D{"reason": invalid.Reason})
	case errors.Is(err, service.ErrSupportUnavailable):
		httperr.Code(c, types.ErrorCodeSupportUnavailable)
	case errors.Is(err, service.ErrSupportRateLimited):
		httperr.Code(c, types.ErrorCodeSupportReportRateLimited)
	default:
		// The transport's reply stays in the server log.
		httperr.Write(c, types.WrapErr(err, types.ErrorCodeSupportReportFailed))
	}
}
