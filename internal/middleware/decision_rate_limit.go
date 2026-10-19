package middleware

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/ratelimit"
	"github.com/lazu-ai/lazu/internal/types"
	"strconv"
)

func CheckDecisionGenerationRate(c *gin.Context) error {
	if model.GatewayLedgerEnabled() {
		// The paid child consumes these dedicated buckets when adopting its
		// transferred reservation, together with the ordinary relay limits.
		return nil
	}
	result, err := ratelimit.DefaultService().PreflightRequest(c.Request.Context(), ratelimit.RequestPreflightInput{Namespace: "decision_qgen", UserID: rateLimitUserIDFromContext(c), TokenID: common.GetContextKeyInt(c, constant.ContextKeyTokenId), TokenRateLimitsEnabled: true, TokenRPMLimit: 2, AccountRPMLimit: 2, DurationSeconds: 60, WindowMinutes: 1})
	if err != nil {
		return types.NewError(err, types.ErrorCodeInternalError, types.ErrOptionWithSkipRetry())
	}
	if !result.Allowed() {
		c.Header("Retry-After", strconv.FormatInt(result.RetryAfterSeconds, 10))
		return types.NewError(fmt.Errorf("automatic question generation is limited to 2 requests per minute"), types.ErrorCodeTokenRateLimitExceeded, types.ErrOptionWithSkipRetry())
	}
	return nil
}
