package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/relay/wirepatch"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
	"github.com/lazu-ai/lazu/internal/types"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/packages/domain/providercap"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TextHelper(c *gin.Context, info *relaycommon.RelayInfo) (lazuErr *types.LazuError) {
	info.InitChannelMeta(c)

	textReq, ok := info.Request.(*dto.GeneralOpenAIRequest)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected dto.GeneralOpenAIRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	requestCopy := *textReq
	requestCopy.Messages = append([]dto.Message(nil), textReq.Messages...)
	if textReq.StreamOptions != nil {
		options := *textReq.StreamOptions
		requestCopy.StreamOptions = &options
	}
	request := &requestCopy
	var err error

	stripLazuFromRequest(request)
	stripProviderSpecificToolCallMetadata(request, info)

	if request.WebSearchOptions != nil {
		c.Set("chat_completion_web_search_context_size", request.WebSearchOptions.SearchContextSize)
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	includeUsage := true
	// 判断用户是否需要返回使用情况
	if request.StreamOptions != nil {
		includeUsage = request.StreamOptions.IncludeUsage
	}

	// 如果不支持StreamOptions，将StreamOptions设置为nil
	if !info.SupportStreamOptions || !request.Stream {
		request.StreamOptions = nil
	} else {
		// 如果支持StreamOptions，且请求中没有设置StreamOptions，根据配置文件设置StreamOptions
		if constant.ForceStreamOption {
			request.StreamOptions = &dto.StreamOptions{
				IncludeUsage: true,
			}
		}
	}

	info.ShouldIncludeUsage = includeUsage

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	var requestBody io.Reader

	passThroughBodyEnabled := model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled
	bridged := planBridges(info)
	// A degraded native plan is sent encoded from the IR: the client's wire
	// still carries the feature the degradation stood in for.
	degradedNative := info.ProtocolPlan != nil && info.ProtocolPlan.Native && info.ProtocolPlan.Degraded() && info.ProtocolRequest != nil
	if degradedNative {
		encoded, encodeErr := degradedChatRequest(info, request)
		if encodeErr != nil {
			return encodeErr
		}
		request = encoded
	}

	nativeConverter, nativeConvertible := adaptor.(interface {
		ConvertNativeRequest([]byte, *relaycommon.RelayInfo, bool) ([]byte, error)
	})
	if nativeConvertible && !bridged && !degradedNative && (info.ProtocolRequest == nil || !openai_chat.HasOSSURLHint(info.ProtocolRequest)) {
		bodyTimer := common.StartHotPathStage(common.HotPathStageOutboundBody)
		body := info.NativeBodyCache
		if body == nil || info.NativeBodyChannelID != info.ChannelId {
			body, err = common.GetRequestBody(c)
			if err == nil {
				body, err = nativeConverter.ConvertNativeRequest(body, info, false)
			}
			if err == nil {
				info.NativeBodyCache = body
				info.NativeBodyChannelID = info.ChannelId
			}
		}
		bodyTimer.Finish(err)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		requestBody = bytes.NewReader(body)
	} else if passThroughBodyEnabled && !bridged && !degradedNative {
		body, err := common.GetRequestBody(c)
		if err != nil {
			return types.NewRequestBodyError(err)
		}
		// Sanitize the outbound copy only; retries retain the immutable client body.
		body, err = stripLazuFromRawBody(body)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		if common.DebugEnabled {
			println("requestBody: ", string(body))
		}
		requestBody = bytes.NewBuffer(body)
	} else {
		if bridged {
			body, _, encodeErr := BridgeBody(c, info, adaptor)
			if encodeErr != nil {
				return encodeErr
			}
			requestBody = bytes.NewBuffer(body)
		} else {
			openAIConverter, converterErr := requireOpenAIRequestConverter(adaptor, info)
			if converterErr != nil {
				return converterErr
			}
			convertedRequest, err := openAIConverter.ConvertOpenAIRequest(c, info, request)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}

			jsonData, err := common.Marshal(convertedRequest)
			if err != nil {
				return types.NewError(err, types.ErrorCodeJsonMarshalFailed, types.ErrOptionWithSkipRetry())
			}

			if _, sameProtocol := convertedRequest.(*dto.GeneralOpenAIRequest); sameProtocol {
				jsonData, err = mergeClientUnknownFields(c, info, jsonData, degradedNative)
				if err != nil {
					return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
				}
			}

			// remove disabled fields for OpenAI API
			jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}

			// apply param override
			if len(info.ParamOverride) > 0 {
				jsonData, err = relaycommon.ApplyParamOverride(jsonData, info.ParamOverride)
				if err != nil {
					return types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
				}
			}
			// wire dialect patch: vendor quirks on the compiled body, once per
			// attempt, after ParamOverride (internal/relay/wirepatch).
			jsonData = wirepatch.Apply(wirepatch.ResolveDialect(info), jsonData)

			logger.LogDebug(c, fmt.Sprintf("text request body: %s", string(jsonData)))
			requestBody = bytes.NewBuffer(jsonData)
		}
	}
	if info.ChannelOtherSettings.ResponsesSingleUserText {
		body, readErr := io.ReadAll(requestBody)
		if readErr != nil {
			return types.NewRequestBodyError(readErr)
		}
		body, err = normalizeSingleUserTextResponses(body, info)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		requestBody = bytes.NewBuffer(body)
	}
	if info.ChannelOtherSettings.ExplicitStreamRequired {
		body, readErr := io.ReadAll(requestBody)
		if readErr != nil {
			return types.NewRequestBodyError(readErr)
		}
		body, err = applyExplicitStreamRequirement(body, info)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		requestBody = bytes.NewBuffer(body)
	}

	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeAPIConnectionError, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	if resp != nil {
		httpResp = resp.(*http.Response)
		info.IsStream = info.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
		if httpResp.StatusCode != http.StatusOK {
			lazuErr := service.RelayErrorHandler(c, httpResp, false)
			// reset status code 重置状态码
			service.ResetStatusCode(lazuErr, statusCodeMappingStr)
			return lazuErr
		}
	}

	usage, lazuErr := adaptor.DoResponse(c, httpResp, info)
	if lazuErr != nil {
		settleTerminalProtocolStream(c, info, usage, lazuErr)
		// reset status code 重置状态码
		service.ResetStatusCode(lazuErr, statusCodeMappingStr)
		return lazuErr
	}

	if strings.HasPrefix(info.OriginModelName, "gpt-4o-audio") {
		service.PostAudioConsumeQuota(c, info, usage.(*dto.Usage), "")
	} else {
		postConsumeQuota(c, info, usage.(*dto.Usage), "")
	}
	return nil
}

func applyExplicitStreamRequirement(body []byte, info *relaycommon.RelayInfo) ([]byte, error) {
	if info == nil || info.ChannelMeta == nil || !info.ChannelOtherSettings.ExplicitStreamRequired {
		return body, nil
	}
	return relaycommon.SetExplicitStream(body, info.IsStream)
}

func settleTerminalProtocolStream(c *gin.Context, info *relaycommon.RelayInfo, rawUsage any, terminalErr *types.LazuError) {
	// The full result is captured atomically with financial settlement. Stamp
	// the known failure before building that envelope, not in the controller
	// after the settlement has already published it.
	if terminalErr != nil {
		common.SetContextKey(c, constant.ContextKeyRequestErrorCode, string(terminalErr.GetErrorCode()))
		common.SetContextKey(c, constant.ContextKeyRequestErrorType, string(terminalErr.GetErrorType()))
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
		return
	}
	// Consume the one-shot signal before settlement so a later error path can
	// never apply the same usage twice.
	common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, false)
	usage, ok := rawUsage.(*dto.Usage)
	if !ok || usage == nil {
		usage = &dto.Usage{}
	}
	if info.RelayFormat == types.RelayFormatClaude {
		service.PostClaudeConsumeQuota(c, info, usage)
	} else {
		postConsumeQuota(c, info, usage, "")
	}
	// The request-scoped reservation is now reconciled. Clearing it prevents
	// the controller's cancellation fallback from issuing a second refund.
	info.FinalPreConsumedQuota = 0
}

func stripLazuMetadataFromSemantic(request *textgen.Request) {
	if request == nil || len(request.Metadata.Metadata) == 0 {
		return
	}
	var metadata map[string]interface{}
	if err := common.Unmarshal(request.Metadata.Metadata, &metadata); err != nil {
		return
	}
	if _, ok := metadata["lazu"]; !ok {
		return
	}
	delete(metadata, "lazu")
	if len(metadata) == 0 {
		request.Metadata.Metadata = nil
		return
	}
	request.Metadata.Metadata, _ = common.Marshal(metadata)
}

func postConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent string) {
	billingTimer := common.StartRelayPhase(ctx, common.RelayPhaseBilling)
	preparedQuote := service.BuildTextBillingQuote(ctx, relayInfo, usage, extraContent)
	usage = preparedQuote.Usage
	extraContent = preparedQuote.ExtraContent
	service.AnnotateUsageCost(ctx, usage, preparedQuote.Quote)
	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	promptTokens := usage.PromptTokens
	cacheTokens := usage.PromptTokensDetails.CachedTokens
	imageTokens := usage.PromptTokensDetails.ImageTokens
	audioTokens := usage.PromptTokensDetails.AudioTokens
	audioCompletionTokens := usage.CompletionTokenDetails.AudioTokens
	completionTokens := usage.CompletionTokens
	cachedCreationTokens := usage.PromptTokensDetails.CachedCreationTokens
	cacheCreation5mTokens := usage.PromptTokensDetails.CachedCreation5mTokens
	cacheCreation1hTokens := usage.PromptTokensDetails.CachedCreation1hTokens
	cacheMissTokens := usage.PromptTokensDetails.CacheMissTokens

	modelName := relayInfo.OriginModelName

	tokenName := ctx.GetString("token_name")
	perCallPriceUSD := relayInfo.PriceData.PerCallPriceUSD
	usePrice := relayInfo.PriceData.UsePrice

	var appliedAdjustmentFactors map[string]float64
	quote := preparedQuote.Quote
	quota := preparedQuote.Quota
	relayInfo.PriceData.PricingVersion = quote.PricingVersion
	totalTokens := promptTokens + completionTokens

	var logContent string

	// record all the consume log even if quota is 0
	if totalTokens == 0 && !usePrice {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		quote = billing.QuoteResult{}
		logContent += "（上游未返回用量）"
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, "+
			"tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, relayInfo.FinalPreConsumedQuota))
	} else {
		if totalTokens == 0 {
			logContent += fmt.Sprintf("（未返回token，按固定价格计费）")
		}
		model.UpdateUserUsedQuota(relayInfo.BillingUserID(), quota)
	}

	quotaDelta := quota - relayInfo.FinalPreConsumedQuota

	//logger.LogInfo(ctx, fmt.Sprintf("request quota delta: %s", logger.FormatQuota(quotaDelta)))

	if quotaDelta > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("Additional quota charged after pre-charge: %s (actual cost: %s, pre-charged: %s)",
			logger.FormatQuota(quotaDelta),
			logger.FormatQuota(quota),
			logger.FormatQuota(relayInfo.FinalPreConsumedQuota),
		))
	} else if quotaDelta < 0 {
		logger.LogInfo(ctx, fmt.Sprintf("Excess pre-charged quota refunded: %s (actual cost: %s, pre-charged: %s)",
			logger.FormatQuota(-quotaDelta),
			logger.FormatQuota(quota),
			logger.FormatQuota(relayInfo.FinalPreConsumedQuota),
		))
	}

	logModel := modelName
	if strings.HasPrefix(logModel, "gpt-4-gizmo") {
		logModel = "gpt-4-gizmo-*"
		logContent += fmt.Sprintf("，模型 %s", modelName)
	}
	if strings.HasPrefix(logModel, "gpt-4o-gizmo") {
		logModel = "gpt-4o-gizmo-*"
		logContent += fmt.Sprintf("，模型 %s", modelName)
	}
	if extraContent != "" {
		logContent += ", " + extraContent
	}
	other := service.GenerateTextOtherInfo(ctx, relayInfo, cacheTokens, perCallPriceUSD)
	if metadata, exists := ctx.Get("decision_metadata"); exists {
		other["decision"] = metadata
	}
	if parentID := ctx.GetString("parent_request_id"); parentID != "" {
		other["parent_request_id"] = parentID
		other["request_kind"] = "decision_question_generation"
	}
	if imageTokens != 0 {
		other["image"] = true
		// Image tokens in the prompt. Rows before 2026-10 stored this
		// under image_output; readers still accept that key.
		other["image_input_tokens"] = imageTokens
	}
	if reasoning := service.ReportedReasoningTokens(usage); reasoning > 0 {
		other["reasoning_tokens"] = reasoning
	}
	if audioTokens != 0 || audioCompletionTokens != 0 {
		other["audio"] = true
		other["audio_input_tokens"] = audioTokens
		other["audio_output_tokens"] = audioCompletionTokens
	}
	if cachedCreationTokens != 0 {
		other["cache_creation_tokens"] = cachedCreationTokens
		other["cache_write_tokens"] = cachedCreationTokens
	}
	if cacheCreation5mTokens != 0 {
		other["cache_creation_5m_tokens"] = cacheCreation5mTokens
		other["cache_write_5m_tokens"] = cacheCreation5mTokens
	}
	if cacheCreation1hTokens != 0 {
		other["cache_creation_1h_tokens"] = cacheCreation1hTokens
		other["cache_write_1h_tokens"] = cacheCreation1hTokens
	}
	if cacheMissTokens != 0 {
		other["cache_miss_tokens"] = cacheMissTokens
	}
	if !preparedQuote.WebSearchMicrousd.IsZero() {
		if relayInfo.ResponsesUsageInfo != nil {
			if webSearchTool := service.ResponsesWebSearchToolInfo(relayInfo); webSearchTool != nil {
				other["web_search"] = true
				other["web_search_call_count"] = webSearchTool.CallCount
				other["web_search_price"] = preparedQuote.WebSearchPrice
			}
		} else if strings.HasSuffix(modelName, "search-preview") {
			other["web_search"] = true
			other["web_search_call_count"] = 1
			other["web_search_price"] = preparedQuote.WebSearchPrice
		}
	} else if !preparedQuote.ClaudeWebSearchMicrousd.IsZero() {
		other["web_search"] = true
		other["web_search_call_count"] = preparedQuote.ClaudeWebSearchCallCount
		other["web_search_price"] = preparedQuote.ClaudeWebSearchPrice
	}
	if !preparedQuote.FileSearchMicrousd.IsZero() && relayInfo.ResponsesUsageInfo != nil {
		if fileSearchTool, exists := relayInfo.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch]; exists {
			other["file_search"] = true
			other["file_search_call_count"] = fileSearchTool.CallCount
			other["file_search_price"] = preparedQuote.FileSearchPrice
		}
	}
	if !preparedQuote.AudioInputMicrousd.IsZero() {
		other["audio_input_seperate_price"] = true
		other["audio_input_token_count"] = audioTokens
		other["audio_input_price"] = preparedQuote.AudioInputPrice
	}
	if !preparedQuote.ImageGenerationCallMicrousd.IsZero() {
		other["image_generation_call"] = true
		other["image_generation_call_price"] = preparedQuote.ImageGenerationCallPrice
	}
	if len(appliedAdjustmentFactors) > 0 {
		other["price_adjustments"] = appliedAdjustmentFactors
		if len(relayInfo.PriceData.OtherPriceAdjustmentReasons) > 0 {
			other["price_adjustment_reasons"] = relayInfo.PriceData.OtherPriceAdjustmentReasons
		}
	}
	billingTimer.Finish()
	consumeLogTimer := common.StartRelayPhase(ctx, common.RelayPhaseConsumeLog)
	model.RecordConsumeLog(ctx, relayInfo.ActorID(), model.RecordConsumeLogParams{
		ProjectID: relayInfo.ProjectID, PayerUserID: relayInfo.BillingUserID(),
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		AmountMicrousd:   quote.TotalMicrousd,
		PricingVersion:   quote.PricingVersion,
		LineItems:        service.QuoteLineItemsJSON(quote),
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
		UsageFact:        usagepkg.FactFromProviderUsage(usage, other, service.UsageProviderFamilyForRelay(relayInfo)),
	})
	consumeLogTimer.Finish()
	var err error
	if model.GatewayLedgerEnabled() {
		err = service.SettleRedisLedgerWithFacts(ctx, relayInfo, quota)
	} else {
		err = service.PostConsumeQuota(relayInfo, quotaDelta, relayInfo.FinalPreConsumedQuota, true)
	}
	if err != nil {
		logger.LogError(ctx, "error consuming token remain quota: "+err.Error())
	}
	if call := common.InternalRelayCallStateFromContext(ctx.Request.Context()); call != nil {
		call.SettlementError = err
		call.Receipt = &common.InternalChargeReceipt{RequestID: relayInfo.RequestID, UserID: relayInfo.BillingUserID(), TokenID: relayInfo.TokenId, ProjectID: relayInfo.ProjectID, ActorUserID: relayInfo.ActorUserID, ChannelID: relayInfo.ChannelId, Model: relayInfo.OriginModelName, ChargedMicrousd: relayInfo.SettledChargeMicrousd.Int64(), PricingVersion: quote.PricingVersion, Unlimited: relayInfo.TokenUnlimited, SettlementUnix: relayInfo.BudgetSettlementAt.Unix()}
	}

}

// stripLazuMetadata removes the `metadata.lazu` private routing-hints
// object from both the parsed request struct AND the cached raw request body
// before forwarding upstream. OpenAI requires metadata values to be flat
// strings, so leaving the nested object in place causes a 400
// invalid_request_error.
//
// The raw-body strip is critical: MergeUnknownTopLevelFieldsFromContext
// re-injects any top-level field that exists in the original body but not
// the converted body, which would un-do the struct-level strip if metadata
// was dropped from the struct (because of `omitempty`).
func stripLazuMetadata(c *gin.Context, req *dto.GeneralOpenAIRequest) {
	stripLazuFromRequest(req)
	stripLazuFromCachedBody(c)
}

// stripProviderSpecificToolCallMetadata prevents provider extensions returned
// by one channel from leaking into a later request routed to a different
// protocol family. TextHelper operates on a deep copy of the client request,
// so stripping an OpenAI-compatible attempt does not remove the Gemini thought
// signature from the original request used by a Gemini retry.
func stripProviderSpecificToolCallMetadata(req *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) {
	if req == nil || info == nil || providercap.ProtocolFamilyOf(info.ApiType) == providercap.ProtocolFamilyGemini {
		return
	}

	for messageIndex := range req.Messages {
		toolCalls := req.Messages[messageIndex].ParseToolCalls()
		changed := false
		for toolCallIndex := range toolCalls {
			if toolCalls[toolCallIndex].ExtraContent == nil {
				continue
			}
			toolCalls[toolCallIndex].ExtraContent = nil
			changed = true
		}
		if changed {
			req.Messages[messageIndex].SetToolCalls(toolCalls)
		}
	}
}

func stripLazuFromRawBody(body []byte) ([]byte, error) {
	var err error
	// Remove duplicate private keys too, without re-encoding caller numbers.
	for gjson.GetBytes(body, "metadata.lazu").Exists() {
		body, err = sjson.DeleteBytes(body, "metadata.lazu")
		if err != nil {
			return nil, err
		}
	}
	if metadata := gjson.GetBytes(body, "metadata"); metadata.IsObject() && len(metadata.Map()) == 0 {
		return sjson.DeleteBytes(body, "metadata")
	}
	return body, nil
}

func stripLazuFromRequest(req *dto.GeneralOpenAIRequest) {
	if req == nil || len(req.Metadata) == 0 {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(req.Metadata, &m); err != nil {
		return
	}
	if _, ok := m["lazu"]; !ok {
		return
	}
	delete(m, "lazu")
	if len(m) == 0 {
		req.Metadata = nil
		return
	}
	if b, err := json.Marshal(m); err == nil {
		req.Metadata = b
	}
}

func stripLazuFromCachedBody(c *gin.Context) {
	if c == nil {
		return
	}
	rawBody, err := common.GetRequestBody(c)
	if err != nil || len(rawBody) == 0 {
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &raw); err != nil {
		return
	}
	mdRaw, ok := raw["metadata"]
	if !ok || len(mdRaw) == 0 {
		return
	}
	var md map[string]any
	if err := json.Unmarshal(mdRaw, &md); err != nil {
		return
	}
	if _, ok := md["lazu"]; !ok {
		return
	}
	delete(md, "lazu")
	if len(md) == 0 {
		delete(raw, "metadata")
	} else {
		newMD, err := json.Marshal(md)
		if err != nil {
			return
		}
		raw["metadata"] = newMD
	}
	newBody, err := json.Marshal(raw)
	if err != nil {
		return
	}
	c.Set(common.KeyRequestBody, newBody)
}

// degradedChatRequest is the Chat request a degraded native plan sends: the
// client's request as the handler has settled it (model, stream options),
// with only what the degradation changed applied (openai_chat.ApplyDegraded).
func degradedChatRequest(info *relaycommon.RelayInfo, wire *dto.GeneralOpenAIRequest) (*dto.GeneralOpenAIRequest, *types.LazuError) {
	encoded, err := openai_chat.ApplyDegraded(wire, info.ProtocolRequest)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeConvertRequestFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return encoded, nil
}

// mergeClientUnknownFields puts the client's top-level fields the converted
// body lacks back on it, as a same-protocol relay always has. For a body
// encoded from a degraded IR only the fields the IR records as unknown go
// back: a known field missing from that body (response_format after a
// degradation) was removed on purpose.
func mergeClientUnknownFields(c *gin.Context, info *relaycommon.RelayInfo, body []byte, fromIR bool) ([]byte, error) {
	if !fromIR {
		return relaycommon.MergeUnknownTopLevelFieldsFromContext(c, body)
	}
	unknown := map[string]json.RawMessage{}
	for path, value := range info.ProtocolRequest.Raw.UnknownFields {
		if !strings.ContainsAny(path, ".[") {
			unknown[path] = value
		}
	}
	if len(unknown) == 0 {
		return body, nil
	}
	raw, err := common.Marshal(unknown)
	if err != nil {
		return nil, err
	}
	return relaycommon.MergeUnknownTopLevelFields(raw, body)
}
