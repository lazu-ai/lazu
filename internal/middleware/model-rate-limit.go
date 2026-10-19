package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/ratelimit"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"
)

// accountRateLimitFromContext is the RPM/TPM ceiling for the account the
// request is charged to (a project's payer, see rateLimitUserIDFromContext).
func accountRateLimitFromContext(c *gin.Context) (rpm, tpm int) {
	return setting.AccountRateLimit(
		common.GetContextKeyBool(c, constant.ContextKeyUserHasToppedUp),
		common.GetContextKeyInt(c, constant.ContextKeyUserRPMLimit),
		common.GetContextKeyInt(c, constant.ContextKeyUserTPMLimit),
	)
}

// Project account limits aggregate against the payer, including service actors (0).
// This does not change the actor used for resource access or audit records.
func rateLimitUserIDFromContext(c *gin.Context) int {
	if common.GetContextKeyInt(c, constant.ContextKeyProjectID) > 0 {
		if payer := common.GetContextKeyInt(c, constant.ContextKeyPayerUserID); payer > 0 {
			return payer
		}
	}
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	if userID == 0 {
		userID = c.GetInt("id")
	}
	return userID
}

func requestRateLimitInputFromContext(c *gin.Context, duration int64, totalMaxCount int, successMaxCount int) ratelimit.RequestPreflightInput {
	rpm, _ := accountRateLimitFromContext(c)
	userID := rateLimitUserIDFromContext(c)
	return ratelimit.RequestPreflightInput{
		UserID:                 userID,
		TokenID:                common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		TokenRateLimitsEnabled: common.GetContextKeyBool(c, constant.ContextKeyTokenRateLimitsEnabled),
		TokenRPMLimit:          common.GetContextKeyInt(c, constant.ContextKeyTokenRPMLimit),
		AccountRPMLimit:        rpm,
		TotalRequestLimit:      totalMaxCount,
		SuccessRequestLimit:    successMaxCount,
		DurationSeconds:        duration,
		WindowMinutes:          setting.ModelRequestRateLimitDurationMinutes,
	}
}

func tokenRateLimitInputFromContext(c *gin.Context, tokens int, duration int64) ratelimit.TokenPreflightInput {
	_, tpm := accountRateLimitFromContext(c)
	userID := rateLimitUserIDFromContext(c)
	return ratelimit.TokenPreflightInput{
		UserID:                 userID,
		TokenID:                common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		TokenRateLimitsEnabled: common.GetContextKeyBool(c, constant.ContextKeyTokenRateLimitsEnabled),
		TokenTPMLimit:          common.GetContextKeyInt(c, constant.ContextKeyTokenTPMLimit),
		AccountTPMLimit:        tpm,
		Tokens:                 tokens,
		DurationSeconds:        duration,
		WindowMinutes:          setting.ModelRequestRateLimitDurationMinutes,
	}
}

func abortWithRateLimitDecision(c *gin.Context, result ratelimit.Result) {
	if result.RetryAfterSeconds > 0 {
		c.Header("Retry-After", strconv.FormatInt(result.RetryAfterSeconds, 10))
	}
	switch result.Decision {
	case ratelimit.DecisionTokenRPM, ratelimit.DecisionTokenTPM:
		abortWithCode(c, types.ErrorCodeTokenRateLimitExceeded)
	case ratelimit.DecisionAccountRPM, ratelimit.DecisionAccountTPM, ratelimit.DecisionSuccessRPM:
		abortWithCode(c, types.ErrorCodeRequestRateLimitExceeded, types.D{"limit": result.Limit, "window_minutes": result.WindowMinutes})
	case ratelimit.DecisionTotalRPM:
		abortWithCode(c, types.ErrorCodeTotalRequestRateLimitExceeded, types.D{"limit": result.Limit, "window_minutes": result.WindowMinutes})
	default:
		abortWithCode(c, types.ErrorCodeRateLimitCheckFailed)
	}
}

func errorCodeForTokenDecision(result ratelimit.Result) types.ErrorCode {
	switch result.Decision {
	case ratelimit.DecisionTokenTPM:
		return types.ErrorCodeTokenRateLimitExceeded
	case ratelimit.DecisionAccountTPM:
		return types.ErrorCodeRequestRateLimitExceeded
	default:
		return types.ErrorCodeRateLimitCheckFailed
	}
}

func CheckRequestTokenRateLimit(c *gin.Context, tokens int) (types.ErrorCode, error) {
	if model.GatewayLedgerEnabled() {
		prepareLedgerRateLimits(c, tokens)
		return "", nil
	}
	if !setting.ModelRequestRateLimitEnabled {
		return "", nil
	}
	duration := int64(setting.ModelRequestRateLimitDurationMinutes * 60)
	result, err := ratelimit.DefaultService().CheckAndRecordTPM(
		context.Background(),
		tokenRateLimitInputFromContext(c, tokens, duration),
	)
	if err != nil {
		return types.ErrorCodeRateLimitCheckFailed, err
	}
	if !result.Allowed() {
		if result.RetryAfterSeconds > 0 {
			c.Header("Retry-After", strconv.FormatInt(result.RetryAfterSeconds, 10))
		}
		return errorCodeForTokenDecision(result), errors.New(string(result.Decision))
	}
	return "", nil
}

// ModelRequestRateLimit gates relay requests through the unified limiter
// service. Preflight checks token RPM, tier RPM, successful-request count, and
// legacy total-request count before recording any RPM window, so a rejection by
// one limiter cannot drain another limiter's budget.
func ModelRequestRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		if call := common.InternalRelayCallStateFromContext(c.Request.Context()); call != nil && call.Preflight {
			c.Next()
			return
		}
		if model.GatewayLedgerEnabled() {
			prepareLedgerRateLimits(c, 0)
			c.Next()
			return
		}
		if !setting.ModelRequestRateLimitEnabled {
			c.Next()
			return
		}

		duration := int64(setting.ModelRequestRateLimitDurationMinutes * 60)
		totalMaxCount := setting.ModelRequestRateLimitCount
		successMaxCount := setting.ModelRequestRateLimitSuccessCount

		input := requestRateLimitInputFromContext(c, duration, totalMaxCount, successMaxCount)
		timer := common.StartHotPathStage(common.HotPathStageRateLimitPreflight)
		result, err := ratelimit.DefaultService().PreflightRequest(context.Background(), input)
		timer.Finish(err)
		if err != nil {
			abortWithCode(c, types.ErrorCodeRateLimitCheckFailed)
			return
		}
		if !result.Allowed() {
			abortWithRateLimitDecision(c, result)
			return
		}

		c.Next()

		if c.Writer.Status() < http.StatusBadRequest {
			timer := common.StartHotPathStage(common.HotPathStageRateLimitPostSuccess)
			if err := ratelimit.DefaultService().RecordSuccess(context.Background(), input); err != nil {
				common.SysLog("failed to record successful model request rate limit: " + err.Error())
				timer.Finish(err)
			} else {
				timer.Finish(nil)
			}
		}
	}
}

// Production admission checks and consumes every bucket together with funds
// in Reserve. The middleware prepares policy; it does not spend a separate
// Redis window before a budget or wallet rejection can occur.
func prepareLedgerRateLimits(c *gin.Context, tokens int) {
	duration := int64(setting.ModelRequestRateLimitDurationMinutes * 60)
	if duration <= 0 {
		duration = 60
	}
	total, success := setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount
	request := requestRateLimitInputFromContext(c, duration, total, success)
	token := tokenRateLimitInputFromContext(c, tokens, duration)
	rates := make([]ledger.RateLimit, 0, 6)
	if !setting.ModelRequestRateLimitEnabled {
		request.TokenRateLimitsEnabled = false
		request.AccountRPMLimit, request.SuccessRequestLimit, request.TotalRequestLimit = 0, 0, 0
		token.TokenRateLimitsEnabled, token.AccountTPMLimit = false, 0
	}
	add := func(key string, decision ratelimit.Decision, limit int, cost int64, onSuccess bool) {
		if limit > 0 {
			rates = append(rates, ledger.RateLimit{Key: key, Decision: string(decision), Limit: int64(limit), Cost: cost, WindowSeconds: duration, OnSuccess: onSuccess})
		}
	}
	if request.TokenRateLimitsEnabled && request.TokenID > 0 {
		add(fmt.Sprintf("token_rpm:%d", request.TokenID), ratelimit.DecisionTokenRPM, request.TokenRPMLimit, 1, false)
	}
	if request.UserID > 0 {
		add(fmt.Sprintf("account_rpm:%d", request.UserID), ratelimit.DecisionAccountRPM, request.AccountRPMLimit, 1, false)
		add(fmt.Sprintf("success_rpm:%d", request.UserID), ratelimit.DecisionSuccessRPM, request.SuccessRequestLimit, 1, true)
		add(fmt.Sprintf("total_rpm:%d", request.UserID), ratelimit.DecisionTotalRPM, request.TotalRequestLimit, 1, false)
	}
	if tokens > 0 {
		if token.TokenRateLimitsEnabled && token.TokenID > 0 {
			add(fmt.Sprintf("token_tpm:%d", token.TokenID), ratelimit.DecisionTokenTPM, token.TokenTPMLimit, int64(tokens), false)
		}
		if token.UserID > 0 {
			add(fmt.Sprintf("account_tpm:%d", token.UserID), ratelimit.DecisionAccountTPM, token.AccountTPMLimit, int64(tokens), false)
		}
	}
	if call := common.InternalRelayCallStateFromContext(c.Request.Context()); call != nil && call.Reservation != nil {
		// Automatic question generation has its own two-per-minute guard even
		// when administrators disable the ordinary relay limiter.
		if request.TokenID > 0 {
			rates = append(rates, ledger.RateLimit{Key: fmt.Sprintf("decision_qgen:token:%d", request.TokenID), Decision: string(ratelimit.DecisionTokenRPM), Limit: 2, Cost: 1, WindowSeconds: 60})
		}
		if request.UserID > 0 {
			rates = append(rates, ledger.RateLimit{Key: fmt.Sprintf("decision_qgen:account:%d", request.UserID), Decision: string(ratelimit.DecisionAccountRPM), Limit: 2, Cost: 1, WindowSeconds: 60})
		}
	}
	c.Set(ledger.RateLimitsContextKey, rates)
}
