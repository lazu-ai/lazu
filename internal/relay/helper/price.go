package helper

import (
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	common_constant "github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

// resolveUsingLane fills in the lane the request is priced on.
func resolveUsingLane(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) {
	if relayInfo.UsingGroup == "" {
		if fg, ok := ctx.Get("final_group"); ok {
			if fgStr, ok2 := fg.(string); ok2 && fgStr != "" {
				relayInfo.UsingGroup = fgStr
			}
		}
	}
	if relayInfo.UsingGroup == "" {
		relayInfo.UsingGroup = common.GetContextKeyString(ctx, common_constant.ContextKeyUsingGroup)
	}
	if relayInfo.UsingGroup == "" {
		relayInfo.UsingGroup = model.ChannelLaneDirect
	}
}

func PriceQuoteHelper(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta) (types.PriceData, error) {
	resolveUsingLane(c, info)
	sellPrice, hasSellPrice := model.GetActiveModelSellPrice(info.OriginModelName, info.UsingGroup)
	// An unpriced model never runs: whether one may is the operator's call, and
	// the per-user opt-in that used to bill it at $0 is retired.
	if !hasSellPrice {
		matchName := model.NormalizePricingModelName(info.OriginModelName)
		return types.PriceData{}, fmt.Errorf("model %q has no %s price configured; please contact the administrator", matchName, model.NormalizeChannelLane(info.UsingGroup))
	}

	if request, ok := info.Request.(*dto.ImageRequest); ok && dto.IsSeedream(request.Model) && sellPrice.ImagePricing == nil {
		return types.PriceData{}, fmt.Errorf("Seedream image pricing is not configured")
	}
	if request, ok := info.Request.(*dto.ImageRequest); ok && strings.HasPrefix(dto.SeedreamName(request.Model), "seedream-5-0-pro") {
		p := sellPrice.ImagePricing
		if p.PixelThreshold != 2610000 || p.HighResolutionOutputUSD == nil || p.LayerUSD == nil || p.HighResolutionLayerUSD == nil || p.ReferenceUSD == nil {
			return types.PriceData{}, fmt.Errorf("Seedream Pro pricing requires pixel, layer and reference prices")
		}
	}
	usePrice := isPerCallSellPrice(sellPrice)
	perCallPriceUSD := 0.0
	if usePrice && sellPrice.RequestUSD != nil {
		perCallPriceUSD = *sellPrice.RequestUSD
		if meta.ImagePriceAdjustmentFactor != 0 {
			perCallPriceUSD = perCallPriceUSD * meta.ImagePriceAdjustmentFactor
		}
	}

	var preConsumedQuota billing.MicroUSD
	var freeModel bool

	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
		if usePrice && perCallPriceUSD == 0 {
			preConsumedQuota = 0
			freeModel = true
		}
	}

	priceData := types.PriceData{
		FreeModel:       freeModel,
		PerCallPriceUSD: perCallPriceUSD,
		UsePrice:        usePrice,
	}
	info.PriceData = priceData
	if !freeModel {
		billingUsage := billing.Usage{}
		if request, ok := info.Request.(*dto.ImageRequest); ok && dto.IsSeedream(request.Model) {
			billingUsage.Images = request.SeedreamReservation()
		} else if usePrice {
			billingUsage.RequestCount = 1
		} else {
			billingUsage = tokenReservationUsage(info, promptTokens, meta)
		}
		preConsumedQuote := service.QuoteModelUsage(info, info.OriginModelName, billingUsage, nil)
		priceData.PricingVersion = preConsumedQuote.PricingVersion
		preConsumedQuota = service.QuoteToQuota(preConsumedQuote)
	}
	priceData.QuotaToPreConsume = preConsumedQuota

	if common.DebugEnabled {
		println(fmt.Sprintf("price_quote_helper result: %s", priceData.ToSetting()))
	}
	info.PriceData = priceData
	return priceData, nil
}

// Reservation is a price estimate, never settled usage. Generative requests
// without an output limit use the existing floor per completion; input-only
// operations must not acquire output costs from a model's configured price.
func tokenReservationUsage(info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta) billing.Usage {
	input := common.Max(promptTokens, common.PreConsumedQuota)
	outputMeta := types.TokenCountMeta{MaxTokens: defaultOutputReservation(info), CompletionCount: 1}
	if meta != nil {
		if meta.MaxTokens > 0 {
			outputMeta.MaxTokens = meta.MaxTokens
		}
		outputMeta.CompletionCount = meta.CompletionCount
	}
	return billing.Usage{InputTokens: int64(input), OutputTokens: int64(service.RequestTPMTokens(0, &outputMeta))}
}

func defaultOutputReservation(info *relaycommon.RelayInfo) int {
	switch info.RelayMode {
	case relayconstant.RelayModeEmbeddings, relayconstant.RelayModeModerations, relayconstant.RelayModeRerank, relayconstant.RelayModeDecisions:
		return 0
	}
	// Native Gemini embeddings share the Gemini format and relay mode with
	// generateContent, so the validated request distinguishes these operations.
	switch info.Request.(type) {
	case *dto.GeminiEmbeddingRequest, *dto.GeminiBatchEmbeddingRequest:
		return 0
	}
	switch info.RelayFormat {
	case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude, types.RelayFormatGemini,
		types.RelayFormatOpenAIImage, types.RelayFormatOpenAIAudio, types.RelayFormatOpenAIRealtime:
		return common.PreConsumedQuota
	default:
		return 0
	}
}

func HasExplicitSellPrice(modelName string) bool {
	_, ok := model.GetActiveModelSellPrice(modelName, model.ChannelLaneDirect)
	return ok
}

func isPerCallSellPrice(price model.ModelSellPrice) bool {
	return (price.ImagePricing != nil || price.RequestUSD != nil || price.SecondUSD != nil) &&
		price.InputPerMtok == nil &&
		price.OutputPerMtok == nil &&
		price.CacheReadPerMtok == nil &&
		price.CacheWrite5mPerMtok == nil &&
		price.CacheWrite1hPerMtok == nil &&
		price.AudioInputPerMtok == nil &&
		price.AudioOutputPerMtok == nil &&
		price.ImageInputPerMtok == nil &&
		price.ImageOutputPerMtok == nil
}
