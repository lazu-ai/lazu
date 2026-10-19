package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

func ReturnPreConsumedQuota(c *gin.Context, relayInfo *relaycommon.RelayInfo) {
	if relayInfo == nil {
		return
	}
	if model.GatewayLedgerEnabled() {
		if relayInfo.LedgerSettlementFinalized {
			return
		}
		if err := settleRedisLedger(relayInfo, 0, true); err != nil {
			common.SysLog("failed durable reservation release: " + err.Error())
		}
		return
	}
	releaseSpendBudgetReservationForRelay(relayInfo)
	if relayInfo.FinalPreConsumedQuota != 0 {
		logger.LogInfo(c, fmt.Sprintf("User %d request failed, returning pre-consumed quota %s", relayInfo.BillingUserID(), logger.FormatQuota(relayInfo.FinalPreConsumedQuota)))
		// Refunds are settlement work and must outlive the inbound request. In
		// particular, a client-canceled context is already done at this point.
		common.SafeCtxGo(context.Background(), "return-pre-consumed-quota", func() {
			relayInfoCopy := *relayInfo

			err := PostConsumeQuota(&relayInfoCopy, -relayInfoCopy.FinalPreConsumedQuota, 0, false)
			if err != nil {
				common.SysLog("error return pre-consumed quota: " + err.Error())
			}
		})
	}
}

// PreConsumeQuota checks if the user has enough quota to pre-consume.
// It returns the pre-consumed quota if successful, or an error if not.
func PreConsumeQuota(c *gin.Context, preConsumedQuota billing.MicroUSD, relayInfo *relaycommon.RelayInfo) *types.LazuError {
	return preConsumeQuota(c, preConsumedQuota, relayInfo, false)
}

// PreConsumeEstimatedQuota reserves a token-priced worst-case estimate (input
// plus the full output limit). When the Key remainder or wallet balance is
// positive but below that estimate, it holds what remains instead of
// rejecting: an estimate is not a charge, and settlement records the actual
// cost. Per-call prices are exact and keep the strict check.
func PreConsumeEstimatedQuota(c *gin.Context, preConsumedQuota billing.MicroUSD, relayInfo *relaycommon.RelayInfo) *types.LazuError {
	return preConsumeQuota(c, preConsumedQuota, relayInfo, !relayInfo.PriceData.UsePrice)
}

// clampReservationToRemaining lowers an estimate to the smallest positive
// remainder below it. A zero or negative remainder is left to the caller's
// rejection, so an exhausted Key or wallet is still refused.
func clampReservationToRemaining(amount billing.MicroUSD, remainders ...billing.MicroUSD) billing.MicroUSD {
	for _, remaining := range remainders {
		if remaining > 0 && remaining < amount {
			amount = remaining
		}
	}
	return amount
}

func preConsumeQuota(c *gin.Context, preConsumedQuota billing.MicroUSD, relayInfo *relaycommon.RelayInfo, clampToRemaining bool) *types.LazuError {
	if model.GatewayLedgerEnabled() {
		if value, ok := c.Get("decision_native_ledger_input"); ok {
			if input, ok := value.(*ledger.Input); ok && input.TokenID == relayInfo.TokenId && input.PayerID == relayInfo.BillingUserID() {
				copy := *input
				relayInfo.RedisLedgerInput = &copy
				relayInfo.FinalPreConsumedQuota = billing.MicroUSD(copy.Amount)
				relayInfo.BudgetSettlementAt = copy.OccurredAt
				return nil
			}
		}
		if clampToRemaining {
			return reserveRedisLedgerEstimate(c, preConsumedQuota, relayInfo)
		}
		return reserveRedisLedger(c, preConsumedQuota, relayInfo)
	}
	if relayInfo.ProjectID > 0 {
		held, err := model.ProjectPaymentHeld(relayInfo.ProjectID)
		if err != nil {
			return types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if held {
			return types.NewErrorWithStatusCode(model.ErrProjectAccessSuspended, types.ErrorCodeProjectAccessSuspended, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}
	}
	if relayInfo.BudgetSettlementAt.IsZero() {
		relayInfo.BudgetSettlementAt = time.Now().UTC()
	}
	userQuota, err := model.GetUserQuota(relayInfo.BillingUserID(), false)
	if err != nil {
		return types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
	}

	var projectKey *model.Token
	if relayInfo.ProjectID > 0 && !relayInfo.TokenUnlimited && !relayInfo.IsPlayground {
		token, tokenErr := model.GetTokenById(relayInfo.TokenId)
		if tokenErr != nil {
			return types.NewError(tokenErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		projectKey = token
	}
	if clampToRemaining && preConsumedQuota > 0 {
		remainders := []billing.MicroUSD{userQuota}
		if projectKey != nil {
			remainders = append(remainders, projectKey.RemainQuota)
		} else if !relayInfo.TokenUnlimited && !relayInfo.IsPlayground {
			// Set by token auth. If stale, the conditional Key update below
			// still refuses a hold the Key cannot cover.
			if remaining, ok := c.Get("token_quota"); ok {
				if value, ok := remaining.(int64); ok {
					remainders = append(remainders, billing.MicroUSD(value))
				}
			}
		}
		preConsumedQuota = clampReservationToRemaining(preConsumedQuota, remainders...)
	}
	chargeInput := spendBudgetChargeInputFromRelay(relayInfo, preConsumedQuota)
	// Check the Key before member/project budgets and the wallet. This keeps
	// the rejection reason deterministic even when several limits are empty.
	if projectKey != nil && (projectKey.RemainQuota < preConsumedQuota || projectKey.RemainQuota <= 0) {
		return types.NewErrorWithStatusCode(model.ErrTokenBudgetExhausted, types.ErrorCodeKeyBudgetExceeded, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
	}
	if err := model.CheckSpendBudgetBeforeCharge(chargeInput); err != nil {
		if errors.Is(err, model.ErrSpendBudgetExceeded) {
			code := SpendBudgetErrorCode(err)
			return types.NewErrorWithStatusCode(err, code, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
		}
		return types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
	}
	if userQuota <= 0 || userQuota < preConsumedQuota {
		releaseSpendBudgetReservationInput(chargeInput)
		return types.NewErrorWithStatusCode(fmt.Errorf("insufficient account balance"), types.ErrorCodeInsufficientQuota, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
	}

	// Every prepaid request reserves funds. An unlimited API key removes its
	// own cap; it does not make the account balance unlimited under concurrency.
	relayInfo.UserQuota = userQuota

	if preConsumedQuota > 0 {
		refID, metadata := relayQuotaLedgerEventData(relayInfo, "relay_preconsume", 0)
		timer := common.StartHotPathStage(common.HotPathStageSettlementPreconsume)
		err := model.PreConsumeQuotaWithLedger(model.PreConsumeQuotaWithLedgerInput{
			UserID:         relayInfo.BillingUserID(),
			ProjectID:      relayInfo.ProjectID,
			TokenID:        relayInfo.TokenId,
			TokenKey:       relayInfo.TokenKey,
			TokenUnlimited: relayInfo.TokenUnlimited,
			IsPlayground:   relayInfo.IsPlayground,
			Quota:          preConsumedQuota,
			RefType:        "relay_preconsume",
			RefID:          refID,
			PricingVersion: relayInfo.PriceData.PricingVersion,
			Metadata:       metadata,
		})
		timer.Finish(err)
		if errors.Is(err, model.ErrProjectAccessSuspended) {
			releaseSpendBudgetReservationInput(chargeInput)
			return types.NewErrorWithStatusCode(err, types.ErrorCodeProjectAccessSuspended, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}
		if err != nil && strings.Contains(err.Error(), "token quota is not enough") {
			releaseSpendBudgetReservationInput(chargeInput)
			if relayInfo.ProjectID > 0 {
				return types.NewErrorWithStatusCode(err, types.ErrorCodeKeyBudgetExceeded, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
			}
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}
		if err != nil && strings.Contains(err.Error(), "user quota is not enough") {
			releaseSpendBudgetReservationInput(chargeInput)
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientQuota, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
		}
		if err != nil {
			releaseSpendBudgetReservationInput(chargeInput)
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
		logger.LogInfo(c, fmt.Sprintf("User %d pre-consumed quota %s, remaining quota after pre-consume: %s", relayInfo.BillingUserID(), logger.FormatQuota(preConsumedQuota), logger.FormatQuota(userQuota-preConsumedQuota)))
	}
	if preConsumedQuota == 0 {
		if err := model.CheckProjectPaymentAdmission(relayInfo.ProjectID); err != nil {
			releaseSpendBudgetReservationInput(chargeInput)
			return paymentAdmissionError(err)
		}
	}
	relayInfo.FinalPreConsumedQuota = preConsumedQuota
	return nil
}

func CheckProjectPaymentAdmission(projectID int) *types.LazuError {
	return paymentAdmissionError(model.CheckProjectPaymentAdmission(projectID))
}

func paymentAdmissionError(err error) *types.LazuError {
	if err == nil {
		return nil
	}
	if errors.Is(err, model.ErrProjectAccessSuspended) {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeProjectAccessSuspended, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	return types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
}

func releaseSpendBudgetReservationForRelay(relayInfo *relaycommon.RelayInfo) {
	if relayInfo == nil {
		return
	}
	releaseSpendBudgetReservationInput(spendBudgetChargeInputFromRelay(relayInfo, 0))
}

func releaseSpendBudgetReservationInput(input model.SpendBudgetChargeInput) {
	if err := model.ReleaseSpendBudgetReservation(input); err != nil {
		common.SysLog("failed to release spend budget reservation: " + err.Error())
	}
}

// SpendBudgetErrorCode keeps file and relay budget rejections consistent.
func SpendBudgetErrorCode(err error) types.ErrorCode {
	code := types.ErrorCodeInsufficientQuota // Preserve legacy non-project Key/user budget behavior.
	var block model.SpendBudgetBlockError
	if errors.As(err, &block) && block.Budget.ProjectID > 0 {
		switch block.Budget.Scope {
		case model.SpendBudgetScopeToken:
			code = types.ErrorCodeKeyBudgetExceeded
		case model.SpendBudgetScopeUser:
			code = types.ErrorCodeMemberBudgetExceeded
		case model.SpendBudgetScopeOrg:
			code = types.ErrorCodeProjectBudgetExceeded
		}
	}
	if errors.As(err, &block) && block.Budget.ProjectID == 0 && block.Budget.Scope == model.SpendBudgetScopeOrg {
		code = types.ErrorCodePlatformBudgetExceeded
	}
	return code
}
