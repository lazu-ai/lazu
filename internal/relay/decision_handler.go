package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	"github.com/lazu-ai/lazu/internal/relay/channel/decision"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/relay/metrics"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

func DecisionHelper(c *gin.Context, info *relaycommon.RelayInfo) (resultErr *types.LazuError) {
	info.InitChannelMeta(c)
	r, ok := info.Request.(*dto.DecisionRequest)
	if !ok {
		return types.NewError(fmt.Errorf("invalid decision request"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	adapter := decision.GetAdaptor(info.ChannelSetting.DecisionDialect)
	if adapter == nil {
		return types.NewError(fmt.Errorf("decision dialect is not configured"), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	request := *r
	if err := helper.ModelMappedHelper(c, info, &request); err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	if !info.IsModelMapped {
		switch adapter.Dialect {
		case "typesafe":
			if request.Model == "jev-1.13" {
				request.Model = "jev-1.13.0"
			}
		case "openrouter":
			request.Model = "typesafe/" + request.Model
		case "openai":
			request.Model = "gpt-6-luna"
		}
		info.UpstreamModelName = request.Model
	}
	timeout := 5 * time.Second
	if info.ChannelSetting.DecisionTimeoutMS > 0 {
		timeout = time.Duration(info.ChannelSetting.DecisionTimeoutMS) * time.Millisecond
	}
	attemptCtx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	parentRequest := c.Request
	c.Request = c.Request.WithContext(attemptCtx)
	defer func() { c.Request = parentRequest }()
	upstream, err := adapter.BuildRequest(attemptCtx, info.ChannelBaseUrl, info.ChannelSetting.CFAccountID, info.ApiKey, request.Model, &request)
	if err != nil {
		return decisionRelayError(err, types.ErrorCodeConvertRequestFailed)
	}
	started := time.Now()
	telemetryFinished := false
	finishTelemetry := func(outcome metrics.ErrorCategory) {
		if telemetryFinished {
			return
		}
		telemetryFinished = true
		if providerStarted := common.GetContextKeyTime(c, constant.ContextKeyDecisionUpstreamStartedAt); !providerStarted.IsZero() {
			started = providerStarted
		}
		metrics.Default.OnPostCall(info.ChannelId, time.Since(started), info.PromptTokens, outcome)
	}
	resp, err := channel.DoRequest(c, upstream, info)
	if !common.GetContextKeyBool(c, constant.ContextKeyProviderAttempted) {
		if err != nil {
			return decisionRelayError(err, types.ErrorCodeAPIConnectionError)
		}
	} else {
		defer func() {
			outcome := metrics.CategoryOK
			if resultErr != nil {
				outcome = metrics.CategoryChannelFault
				if resultErr.StatusCode == 429 {
					outcome = metrics.CategoryRateLimited
				}
			}
			finishTelemetry(outcome)
		}()
	}
	if err != nil {
		return decisionRelayError(err, types.ErrorCodeAPIConnectionError)
	}
	bodyClosed := false
	defer func() {
		if !bodyClosed {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests && adapter.Dialect != "openai" {
			return types.NewError(fmt.Errorf("upstream decision capacity exhausted"), types.ErrorCodeUpstreamCapacity)
		}
		return service.RelayErrorHandler(c, resp, false)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return service.UpstreamReadError(c, err)
	}
	if len(body) > 4<<20 {
		return types.NewError(fmt.Errorf("decision response is too large"), types.ErrorCodeUpstreamDecisionInvalid)
	}
	decoded, err := adapter.ParseDecisionResponse(body, r)
	if err != nil {
		return decisionRelayError(err, types.ErrorCodeUpstreamDecisionInvalid)
	}
	usage := &dto.Usage{PromptTokens: info.PromptTokens, InputTokenSource: dto.UsageSourceLocalEstimate, OutputTokenSource: dto.UsageSourceUnavailable}
	if decoded.ProviderUsage != nil {
		copy := *decoded.ProviderUsage
		usage = &copy
	} else if decoded.Usage != nil {
		usage.PromptTokens, usage.CompletionTokens = decoded.Usage.InputTokens, decoded.Usage.OutputTokens
		usage.InputTokenSource, usage.OutputTokenSource = dto.UsageSourceProvider, dto.UsageSourceProvider
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	info.PromptTokens = usage.PromptTokens
	info.UpstreamModelName = decoded.Model
	// Stop upstream accounting and release admission before local settlement.
	// Local billing failures must not turn a successful provider into a fault.
	_ = resp.Body.Close()
	bodyClosed = true
	finishTelemetry(metrics.CategoryOK)
	questionTypes := r.QuestionTypes()
	imageFacts := make([]map[string]interface{}, 0, len(r.ImageInfo))
	for _, img := range r.ImageInfo {
		imageFacts = append(imageFacts, map[string]interface{}{"sha256": img.SHA256, "bytes": img.Bytes})
	}
	c.Set("decision_metadata", map[string]interface{}{"question_types": questionTypes, "images": imageFacts, "provider": adapter.Dialect, "question_count": r.QuestionCount(), "image_count": len(r.ImageInfo), "upstream_model": decoded.Model, "usage_source": usage.Measurement(), "generation_request_id": decisionGenerationID(r)})
	response := dto.DecisionResponse{ID: "dec_" + info.RequestID, Object: "decision", Created: time.Now().Unix(), Model: info.OriginModelName, Answers: decoded.Answers, Usage: dto.DecisionUsage{InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens}, QuestionGeneration: r.GenerationResult}
	if r.GenerationResult != nil {
		response.Questions = r.Questions
	}
	if r.GenerationEnabled && r.GenerationResult == nil {
		response.Warnings = []string{"explicit questions take precedence over question_generation"}
	}
	encoded := decoded.NativeResponse
	if r.OpenAI == nil {
		encoded, err = json.Marshal(response)
		if err != nil {
			return types.NewError(err, types.ErrorCodeInternalError, types.ErrOptionWithSkipRetry())
		}
	} else if len(encoded) == 0 {
		return types.NewError(fmt.Errorf("native decision response is missing"), types.ErrorCodeUpstreamDecisionInvalid, types.ErrOptionWithSkipRetry())
	}
	service.AttachModelLogPayload(c, &service.ModelLogPayload{ResponseBody: encoded, Usage: usage, StatusCode: http.StatusOK})
	ids := r.QuestionIDs()
	sort.Strings(ids)
	postConsumeQuota(c, info, usage, "Decision: "+strings.Join(ids, ", "))
	c.Data(http.StatusOK, "application/json", encoded)
	return nil
}

func decisionGenerationID(r *dto.DecisionRequest) string {
	if r.GenerationResult != nil {
		return r.GenerationResult.RequestID
	}
	return ""
}
func decisionRelayError(err error, fallback types.ErrorCode) *types.LazuError {
	var typed *types.LazuError
	if errors.As(err, &typed) {
		return typed
	}
	return types.NewError(err, fallback, types.ErrOptionWithDetails(map[string]string{"raw_message": common.MaskSensitiveInfo(strings.TrimSpace(err.Error()))}))
}
