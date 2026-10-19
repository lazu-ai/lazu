package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay/channel/decision"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func DecisionsEnabled() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !common.DecisionsEnabled() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

func ParseDecisions() gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := common.GetRequestBody(c)
		if err != nil {
			httperr.Write(c, err)
			c.Abort()
			return
		}
		request, err := dto.ParseDecisionRequest(body)
		if err == nil {
			err = service.ValidateDecisionInputLimits(request)
		}
		if err != nil {
			httperr.Write(c, err)
			c.Abort()
			return
		}
		c.Set("decision_request", request)
		c.Next()
	}
}

func Decisions(c *gin.Context) {
	defer middleware.RecordManualRoutingOutcome(c)
	value, _ := c.Get("decision_request")
	request, ok := value.(*dto.DecisionRequest)
	if !ok {
		httperr.Write(c, types.Err(types.ErrorCodeInvalidRequest))
		return
	}
	settings, _ := c.Get("decision_question_generation_settings")
	keySettings, _ := settings.(model.DecisionQuestionGenerationSettings)
	enabled := keySettings.Enabled
	generator := keySettings.Model
	if request.QuestionGeneration != nil {
		if request.QuestionGeneration.Enabled != nil {
			enabled = *request.QuestionGeneration.Enabled
		}
		if request.QuestionGeneration.Model != "" {
			generator = request.QuestionGeneration.Model
		}
	}
	if generator == "" {
		generator = common.DecisionQuestionGenerationDefaultModel()
	}
	request.GenerationEnabled = enabled
	if request.QuestionCount() == 0 {
		if !enabled {
			httperr.Write(c, types.Err(types.ErrorCodeMissingQuestions))
			return
		}
		if !common.DecisionQuestionGenerationEnabled() {
			httperr.Write(c, types.Err(types.ErrorCodeMissingQuestions))
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		info, err := relaycommon.GenRelayInfo(c, types.RelayFormatDecisions, request, nil)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		info.InitChannelMeta(c)
		adapter := decision.GetAdaptor(info.ChannelSetting.DecisionDialect)
		if adapter != nil {
			if _, err := adapter.BuildRequest(ctx, info.ChannelBaseUrl, info.ChannelSetting.CFAccountID, info.ApiKey, request.Model, request); err != nil {
				httperr.Write(c, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry()))
				return
			}
		}
		if decision.GetAdaptor(info.ChannelSetting.DecisionDialect) == nil {
			httperr.Write(c, types.Err(types.ErrorCodeInvalidApiType))
			return
		}
		meta := request.GetTokenCountMeta()
		tokens, err := service.EstimateRequestTPMTokens(meta, info)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		price, err := helper.PriceQuoteHelper(c, info, tokens+1024, meta)
		if err != nil {
			httperr.Write(c, types.NewError(err, types.ErrorCodePricingNotConfigured, types.ErrOptionWithSkipRetry()))
			return
		}
		identity := middleware.InternalIdentitySnapshot(c)
		preflight, err := service.CallDecisionQuestionGenerator(ctx, identity, request, generator, true)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		if !model.GatewayLedgerEnabled() && common.GetContextKeyMicroUSD(c, constant.ContextKeyUserQuota).Int64() < price.QuotaToPreConsume.Int64()+preflight.ReservationMicrousd {
			httperr.Write(c, types.Err(types.ErrorCodeInsufficientQuota))
			return
		}
		if model.GatewayLedgerEnabled() {
			if code, rateErr := middleware.CheckRequestTokenRateLimit(c, service.RequestTPMTokens(tokens+1024, meta)); rateErr != nil {
				httperr.Write(c, types.NewError(rateErr, code, types.ErrOptionWithSkipRetry()))
				return
			}
		}
		releaseBudget, childInput, budgetErr := service.ReserveDecisionStages(c, info, price.QuotaToPreConsume, billing.MicroUSD(preflight.ReservationMicrousd))
		if budgetErr != nil {
			httperr.Write(c, budgetErr)
			return
		}
		defer releaseBudget()
		if err := middleware.CheckDecisionGenerationRate(c); err != nil {
			httperr.Write(c, err)
			return
		}
		generated, err := service.CallDecisionQuestionGenerator(ctx, identity, request, generator, false, childInput)
		if err == nil {
			request.Questions = generated.Questions
			err = request.Validate(true)
			if err == nil {
				err = service.ValidateDecisionInputLimits(request)
			}
		}
		if err != nil {
			if generated == nil || generated.Receipt == nil {
				httperr.Write(c, err)
				return
			}
			if generated != nil && generated.Receipt != nil {
				if refundErr := model.RecordDecisionGenerationRefund(*generated.Receipt, "invalid generated questions"); refundErr != nil {
					common.SysError("decision generation refund queued or failed: " + refundErr.Error())
				}
			}
			httperr.Write(c, types.NewError(fmt.Errorf("question generation failed validation"), types.ErrorCodeQuestionGenerationFailed, types.ErrOptionWithSkipRetry()))
			return
		}
		request.GenerationResult = generated.Result
	}
	if err := request.Validate(true); err != nil {
		httperr.Write(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	Relay(c, types.RelayFormatDecisions)
}

func withGeneratedDecisionDetails(c *gin.Context, err *types.LazuError) *types.LazuError {
	value, _ := c.Get("decision_request")
	request, _ := value.(*dto.DecisionRequest)
	if request == nil || request.GenerationResult == nil {
		return err
	}
	c.Set("decision_generation_error_details", map[string]interface{}{"questions": request.Questions, "question_generation": request.GenerationResult})
	return types.NewErrorWithStatusCode(errors.New(err.Error()), err.GetErrorCode(), err.StatusCode, types.ErrOptionWithSkipRetry(), types.ErrOptionWithDetails(map[string]interface{}{"questions": request.Questions, "question_generation": request.GenerationResult}))
}
