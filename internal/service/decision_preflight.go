package service

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"net/http"
	"time"
)

func ReserveDecisionStages(c *gin.Context, info *relaycommon.RelayInfo, native, generator billing.MicroUSD) (func(), *ledger.Input, *types.LazuError) {
	if !model.GatewayLedgerEnabled() {
		release, err := ReserveDecisionGenerationBudget(info, native, native+generator)
		return release, nil, err
	}
	if err := reserveRedisLedger(c, native+generator, info); err != nil {
		return nil, nil, err
	}
	parent, child, err := model.GatewayLedger().SplitReservation(c.Request.Context(), *info.RedisLedgerInput, common.GenerateRequestID(), int64(generator))
	if err != nil {
		ReturnPreConsumedQuota(c, info)
		c.Header("Retry-After", "1")
		return nil, nil, redisAdmissionError(err, nil)
	}
	c.Set("decision_native_ledger_input", &parent)
	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := model.GatewayLedger().Release(ctx, parent); err != nil {
			_ = model.GatewayLedger().PersistSettlement(parent, 0, "release")
		}
		if err := model.GatewayLedger().Release(ctx, child); err != nil {
			_ = model.GatewayLedger().PersistSettlement(child, 0, "release")
		}
	}
	return release, &child, nil
}

// Reserve both stages against the same parent budget before the paid generator.
// The reservation is replaced by native pre-consumption, or released on exit.
func ReserveDecisionGenerationBudget(info *relaycommon.RelayInfo, native, total billing.MicroUSD) (func(), *types.LazuError) {
	if err := CheckProjectPaymentAdmission(info.ProjectID); err != nil {
		return nil, err
	}
	key, err := model.GetTokenById(info.TokenId)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
	}
	if !key.UnlimitedQuota && key.RemainQuota < total {
		return nil, types.NewErrorWithStatusCode(model.ErrTokenBudgetExhausted, types.ErrorCodeKeyBudgetExceeded, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
	}
	balance, err := model.GetUserQuota(info.BillingUserID(), false)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
	}
	if balance < total {
		return nil, types.NewErrorWithStatusCode(model.ErrTokenBudgetExhausted, types.ErrorCodeInsufficientQuota, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
	}
	input := spendBudgetChargeInputFromRelay(info, total)
	if err := model.CheckSpendBudgetBeforeCharge(input); err != nil {
		return nil, types.NewErrorWithStatusCode(err, SpendBudgetErrorCode(err), http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
	}
	input.DeltaMicrousd = native
	if err := model.CheckSpendBudgetBeforeCharge(input); err != nil {
		releaseSpendBudgetReservationInput(input)
		return nil, types.NewErrorWithStatusCode(err, SpendBudgetErrorCode(err), http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
	}
	return func() { releaseSpendBudgetReservationInput(input) }, nil
}
