package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func GetUsageRequestDetail(c *gin.Context) {
	requestID := strings.TrimSpace(c.Param("request_id"))
	if requestID == "" {
		writeControlPlaneCatalogError(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "request_id"})
		return
	}

	log, err := model.GetConsumeLogByRequestIDForTokenContext(c.Request.Context(), c.GetInt("id"), c.GetInt("token_id"), requestID, model.RequestResourcePrincipal(c))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Receipts become visible asynchronously. A polling miss is a read
			// result and must not append a failed model request to usage history.
			httperr.WriteOpenAI(c, types.Err(types.ErrorCodeRequestNotFound, types.D{"field": "request_id", "request_id": requestID}))
			return
		}
		writeInternalControlPlaneError(c, err)
		return
	}

	other, _ := common.StrToMap(log.Other)
	quote := parseUsageRequestQuote(log.LineItems)
	outcome, _ := model.GetOutcomeByRequestIDContext(c.Request.Context(), requestID)

	c.JSON(http.StatusOK, dto.UsageRequestDetailResponse{
		Object:    "usage.request",
		ID:        requestID,
		CreatedAt: log.CreatedAt,
		Model:     log.ModelName,
		Endpoint:  stringFromMap(other, "request_path"),
		Usage:     buildUsageRequestUsage(log, other, outcome),
		Billing:   buildUsageRequestBilling(log, quote),
		Routing:   buildUsageRequestRouting(outcome),
		LogID:     log.PublicID,
	})
}

func parseUsageRequestQuote(raw string) billing.QuoteResult {
	if strings.TrimSpace(raw) == "" {
		return billing.QuoteResult{}
	}
	var quote billing.QuoteResult
	_ = json.Unmarshal([]byte(raw), &quote)
	return quote
}

func buildUsageRequestUsage(log *model.Log, other map[string]interface{}, outcome *model.RoutingOutcome) dto.UsageRequestUsage {
	if log != nil {
		if fact, ok := usagepkg.ParseFact(log.UsageFact); ok {
			return fact.UsageRequestUsage()
		}
	}
	builder := newUsageDimensionBuilder()

	cacheReadTokens := firstPositiveUsageInt(
		intFromUsageMap(other, "cache_tokens"),
		intFromUsageMap(other, "cached_tokens"),
		intFromUsageMap(other, "prompt_cache_hit_tokens"),
		intFromRoutingOutcomePtr(outcome, func(o *model.RoutingOutcome) *int { return o.CachedInputTokens }),
	)
	cacheWriteTotal := firstPositiveUsageInt(
		intFromUsageMap(other, "cache_write_tokens"),
		intFromUsageMap(other, "cache_creation_tokens"),
		intFromRoutingOutcomePtr(outcome, func(o *model.RoutingOutcome) *int { return o.CacheWriteTokens }),
	)
	cacheWrite5mTokens := firstPositiveUsageInt(
		intFromUsageMap(other, "cache_write_5m_tokens"),
		intFromUsageMap(other, "cache_creation_5m_tokens"),
		intFromRoutingOutcomePtr(outcome, func(o *model.RoutingOutcome) *int { return o.CacheWrite5mTokens }),
	)
	cacheWrite1hTokens := firstPositiveUsageInt(
		intFromUsageMap(other, "cache_write_1h_tokens"),
		intFromUsageMap(other, "cache_creation_1h_tokens"),
		intFromRoutingOutcomePtr(outcome, func(o *model.RoutingOutcome) *int { return o.CacheWrite1hTokens }),
	)
	cacheMissTokens := firstPositiveUsageInt(
		intFromUsageMap(other, "cache_miss_tokens"),
		intFromUsageMap(other, "prompt_cache_miss_tokens"),
		intFromRoutingOutcomePtr(outcome, func(o *model.RoutingOutcome) *int { return o.CacheMissTokens }),
	)
	audioInputTokens := firstPositiveUsageInt(
		intFromUsageMap(other, "audio_input_tokens"),
		intFromUsageMap(other, "audio_input"),
		intFromRoutingOutcomePtr(outcome, func(o *model.RoutingOutcome) *int { return o.AudioInputTokens }),
	)
	audioOutputTokens := firstPositiveUsageInt(
		intFromUsageMap(other, "audio_output_tokens"),
		intFromUsageMap(other, "audio_output"),
		intFromRoutingOutcomePtr(outcome, func(o *model.RoutingOutcome) *int { return o.AudioOutputTokens }),
	)
	imageInputTokens := firstPositiveUsageInt(
		intFromUsageMap(other, "image_input_tokens"),
		intFromUsageMap(other, "image_input"),
		intFromUsageMap(other, "image_output"),
		intFromRoutingOutcomePtr(outcome, func(o *model.RoutingOutcome) *int { return o.ImageInputTokens }),
	)

	builder.addIfMissing(usagepkg.DimensionCacheRead, int64(cacheReadTokens), usagepkg.UnitToken)
	cacheWriteUnknown := cacheWriteTotal - cacheWrite5mTokens - cacheWrite1hTokens
	if cacheWriteUnknown < 0 {
		cacheWriteUnknown = 0
	}
	builder.addIfMissing(usagepkg.DimensionCacheWrite, int64(cacheWriteUnknown), usagepkg.UnitToken)
	builder.addIfMissing(usagepkg.DimensionCacheWrite5m, int64(cacheWrite5mTokens), usagepkg.UnitToken)
	builder.addIfMissing(usagepkg.DimensionCacheWrite1h, int64(cacheWrite1hTokens), usagepkg.UnitToken)
	builder.addIfMissing(usagepkg.DimensionCacheMiss, int64(cacheMissTokens), usagepkg.UnitToken)
	builder.addIfMissing(usagepkg.DimensionAudioInput, int64(audioInputTokens), usagepkg.UnitToken)
	builder.addIfMissing(usagepkg.DimensionAudioOutput, int64(audioOutputTokens), usagepkg.UnitToken)
	builder.addIfMissing(usagepkg.DimensionImageInput, int64(imageInputTokens), usagepkg.UnitToken)
	builder.addIfMissing(usagepkg.DimensionWebSearch, int64(intFromUsageMap(other, "web_search_call_count")), usagepkg.UnitRequest)
	builder.addIfMissing(usagepkg.DimensionFileSearch, int64(intFromUsageMap(other, "file_search_call_count")), usagepkg.UnitRequest)
	if boolFromUsageMap(other, "image_generation_call") {
		builder.addIfMissing(usagepkg.DimensionImageGeneration, 1, usagepkg.UnitRequest)
	}

	inputTokens := log.PromptTokens - audioInputTokens - imageInputTokens
	if !usesProviderSeparatedCacheInput(log, other) {
		inputTokens -= cacheReadTokens + cacheWriteTotal
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	outputTokens := log.CompletionTokens - audioOutputTokens
	if outputTokens < 0 {
		outputTokens = 0
	}
	builder.addIfMissing(usagepkg.DimensionInput, int64(inputTokens), usagepkg.UnitToken)
	builder.addIfMissing(usagepkg.DimensionOutput, int64(outputTokens), usagepkg.UnitToken)

	return dto.UsageRequestUsage{
		PromptTokens:     log.PromptTokens,
		CompletionTokens: log.CompletionTokens,
		TotalTokens:      log.PromptTokens + log.CompletionTokens,
		Dimensions:       builder.dimensions(),
	}
}

func buildUsageRequestBilling(log *model.Log, quote billing.QuoteResult) dto.UsageRequestBilling {
	pricingVersion := log.PricingVersion
	if pricingVersion == 0 {
		pricingVersion = quote.PricingVersion
	}
	billingOut := dto.UsageRequestBilling{
		Currency:               "USD",
		AmountMicrousd:         log.AmountMicrousd.Int64(),
		PricingVersion:         pricingVersion,
		LineItems:              quote.LineItems,
		MissingPriceDimensions: quote.MissingPriceDimensions,
		ContextTier:            quote.ContextTier,
		Peak:                   quote.Peak,
	}
	if quote.Discount.AmountMicrousd > 0 {
		billingOut.Discount = &dto.UsageRequestDiscount{Pct: quote.Discount.Pct.InexactFloat64(), AmountMicrousd: quote.Discount.AmountMicrousd.Int64()}
	}
	return billingOut
}

// Request receipts expose public timing, never the internal routing decision.
func buildUsageRequestRouting(outcome *model.RoutingOutcome) *dto.UsageRequestRouting {
	if outcome == nil {
		return nil
	}
	return &dto.UsageRequestRouting{StatusCode: outcome.StatusCode, TotalDurationMs: outcome.TotalDurationMs, TTFTMs: outcome.TTFTMs, WasStreaming: outcome.WasStreaming}
}

func buildUsageRequestProviderUsage(log *model.Log, other map[string]interface{}) dto.UsageRequestProviderUsage {
	modelName := ""
	if log != nil {
		modelName = log.ModelName
		if fact, ok := usagepkg.ParseFact(log.UsageFact); ok {
			return usageRequestProviderUsageFromFact(fact, modelName, other)
		}
	}
	pricing := model.Pricing{ModelName: modelName}
	for _, item := range model.GetPricing() {
		if item.ModelName == modelName {
			pricing = item
			break
		}
	}
	if family := usagepkg.ProviderFamilyFromFields(other); family != usagepkg.ProviderFamilyUnknown {
		return dto.UsageRequestProviderUsage{
			Family:    string(family),
			RawFields: usageProviderRawFields(other),
		}
	}
	return dto.UsageRequestProviderUsage{
		Family:    usageProviderFamily(modelName, pricing, ""),
		RawFields: usageProviderRawFields(other),
	}
}

func usageRequestProviderUsageFromFact(fact usagepkg.Fact, modelName string, other map[string]interface{}) dto.UsageRequestProviderUsage {
	rawFields := fact.ProviderRawUsage
	if len(rawFields) == 0 {
		rawFields = usageProviderRawFields(other)
	}
	family := strings.TrimSpace(fact.ProviderFamily)
	if family == "" {
		if fieldFamily := usagepkg.ProviderFamilyFromFields(other); fieldFamily != usagepkg.ProviderFamilyUnknown {
			family = string(fieldFamily)
		}
	}
	if family == "" {
		family = usageProviderFamily(modelName, model.Pricing{ModelName: modelName}, "")
	}
	return dto.UsageRequestProviderUsage{
		Family:    family,
		RawFields: rawFields,
	}
}

func usageProviderRawFields(other map[string]interface{}) map[string]any {
	if len(other) == 0 {
		return nil
	}
	keys := []string{
		"cache_tokens",
		"cached_tokens",
		"cache_write_tokens",
		"cache_write_5m_tokens",
		"cache_write_1h_tokens",
		"cache_creation_input_tokens",
		"cache_creation_tokens",
		"cache_creation_5m_tokens",
		"cache_creation_1h_tokens",
		"cache_miss_tokens",
		"prompt_cache_hit_tokens",
		"prompt_cache_miss_tokens",
		"audio_input_tokens",
		"audio_output_tokens",
		"image_input_tokens",
		"audio_input",
		"audio_output",
		"image_input",
		"image_output",
		"text_input",
		"text_output",
		"per_call_price_usd",
	}
	result := make(map[string]any)
	for _, key := range keys {
		if value, ok := other[key]; ok {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

type usageDimensionBuilder struct {
	ordered []dto.UsageDimension
	seen    map[string]int
}

func newUsageDimensionBuilder() *usageDimensionBuilder {
	return &usageDimensionBuilder{seen: make(map[string]int)}
}

func (b *usageDimensionBuilder) add(name string, quantity int64, unit string) {
	if name == "" || quantity <= 0 {
		return
	}
	if idx, ok := b.seen[name]; ok {
		b.ordered[idx].Quantity += quantity
		return
	}
	b.seen[name] = len(b.ordered)
	b.ordered = append(b.ordered, dto.UsageDimension{
		Name:     name,
		Quantity: quantity,
		Unit:     unit,
	})
}

func (b *usageDimensionBuilder) addIfMissing(name string, quantity int64, unit string) {
	if _, ok := b.seen[name]; ok {
		return
	}
	b.add(name, quantity, unit)
}

func (b *usageDimensionBuilder) dimensions() []dto.UsageDimension {
	if b == nil {
		return nil
	}
	return b.ordered
}

func unitForUsageDimension(dimension string) string {
	switch dimension {
	case usagepkg.DimensionRequest:
		return usagepkg.UnitRequest
	case usagepkg.DimensionSecond:
		return usagepkg.UnitSecond
	default:
		return usagepkg.UnitToken
	}
}

func parseUsageFallbackChain(raw string) []string {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err == nil {
		return values
	}
	return []string{raw}
}

func stringFromMap(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	switch value := values[key].(type) {
	case string:
		return value
	default:
		return ""
	}
}

func intFromUsageMap(values map[string]interface{}, key string) int {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func boolFromUsageMap(values map[string]interface{}, key string) bool {
	if values == nil {
		return false
	}
	switch value := values[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(value, "true")
	default:
		return false
	}
}

func usesProviderSeparatedCacheInput(log *model.Log, other map[string]interface{}) bool {
	if boolFromUsageMap(other, "claude") {
		return true
	}
	return usagepkg.ProviderFamilyFromFields(other) == usagepkg.ProviderFamilyAnthropic
}

func intFromRoutingOutcomePtr(outcome *model.RoutingOutcome, pick func(*model.RoutingOutcome) *int) int {
	if outcome == nil || pick == nil {
		return 0
	}
	value := pick(outcome)
	if value == nil {
		return 0
	}
	return *value
}

func firstPositiveUsageInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
