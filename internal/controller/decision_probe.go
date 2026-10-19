package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	"github.com/lazu-ai/lazu/internal/relay/channel/decision"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
	"io"
	"net/http"
	"time"
)

func testDecisionChannel(c *gin.Context, info *relaycommon.RelayInfo, record bool) (outcome testResult) {
	outcome.context = c
	started := time.Now()
	defer func() {
		outcome.providerAttempted = common.GetContextKeyBool(c, constant.ContextKeyProviderAttempted)
		if outcome.providerAttempted {
			if outcome.localErr != nil && outcome.lazuErr == nil {
				outcome.lazuErr = types.NewError(outcome.localErr, types.ErrorCodeUpstreamDecisionInvalid)
			}
			status := http.StatusOK
			if outcome.localErr != nil {
				status = http.StatusBadGateway
			}
			middleware.RecordManualRelayPostCall(info.ChannelId, started, status, "")
		}
	}()
	r, ok := info.Request.(*dto.DecisionRequest)
	if !ok {
		outcome.localErr = fmt.Errorf("invalid decision probe")
		return
	}
	adapter := decision.GetAdaptor(info.ChannelSetting.DecisionDialect)
	if adapter == nil {
		outcome.localErr = fmt.Errorf("decision dialect is not configured")
		return
	}
	request := *r
	if err := helper.ModelMappedHelper(c, info, &request); err != nil {
		outcome.localErr = err
		return
	}
	if !info.IsModelMapped {
		switch adapter.Dialect {
		case "typesafe":
			request.Model = "jev-1.13.0"
		case "openrouter":
			request.Model = "typesafe/" + request.Model
		case "openai":
			request.Model = "gpt-6-luna"
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	if c.Request.Body == nil {
		c.Request.Body = http.NoBody
	}
	req, err := adapter.BuildRequest(ctx, info.ChannelBaseUrl, info.ChannelSetting.CFAccountID, info.ApiKey, request.Model, &request)
	if err != nil {
		outcome.localErr = err
		return
	}
	started = time.Now()
	resp, err := channel.DoRequest(c, req, info)
	if err != nil {
		outcome.localErr = err
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		outcome.lazuErr = service.RelayErrorHandler(c, resp, true)
		outcome.localErr = outcome.lazuErr
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		outcome.localErr = err
		return
	}
	result, err := adapter.ParseDecisionResponse(body, r)
	if err != nil {
		outcome.localErr = err
		return
	}
	if r.OpenAI != nil {
		outcome.body = result.NativeResponse
	} else {
		outcome.body, _ = json.Marshal(result)
	}
	outcome.durationMs = int(time.Since(started).Milliseconds())
	if result.Usage != nil {
		outcome.promptTokens = result.Usage.InputTokens
		outcome.outputTokens = result.Usage.OutputTokens
	}
	if record {
		probeUsage := billing.Usage{InputTokens: int64(outcome.promptTokens), OutputTokens: int64(outcome.outputTokens)}
		if result.ProviderUsage != nil {
			probeUsage = usagepkg.BillingUsageFromSummary(usagepkg.SummaryFromDTO(result.ProviderUsage))
		}
		quote := service.QuoteModelUsage(info, info.OriginModelName, probeUsage, nil)
		model.RecordConsumeLog(c, 1, model.RecordConsumeLogParams{ChannelId: info.ChannelId, PromptTokens: outcome.promptTokens, CompletionTokens: outcome.outputTokens, ModelName: info.OriginModelName, TokenName: "模型测试", AmountMicrousd: quote.TotalMicrousd, PricingVersion: quote.PricingVersion, LineItems: service.QuoteLineItemsJSON(quote), Content: "模型测试", Group: info.UsingGroup, Other: map[string]interface{}{"request_kind": "decision_probe", "upstream_model": result.Model}})
	}
	return
}

func probeReplyHasDecision(body []byte) bool {
	var reply struct {
		Answers map[string]struct {
			Type string  `json:"type"`
			Noul float64 `json:"noul"`
		} `json:"answers"`
	}
	if json.Unmarshal(body, &reply) != nil {
		return false
	}
	answer, ok := reply.Answers["red_apple"]
	return ok && answer.Type == "noul" && answer.Noul >= 0.8 && answer.Noul <= 1
}
