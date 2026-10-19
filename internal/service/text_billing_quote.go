package service

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
	"github.com/shopspring/decimal"
)

type TextBillingQuote struct {
	Usage                       *dto.Usage
	Quote                       billing.QuoteResult
	Quota                       billing.MicroUSD
	BillingUsage                billing.Usage
	ExtraContent                string
	WebSearchMicrousd           decimal.Decimal
	WebSearchPrice              float64
	ClaudeWebSearchMicrousd     decimal.Decimal
	ClaudeWebSearchPrice        float64
	ClaudeWebSearchCallCount    int
	FileSearchMicrousd          decimal.Decimal
	FileSearchPrice             float64
	AudioInputMicrousd          decimal.Decimal
	AudioInputPrice             float64
	ImageGenerationCallMicrousd decimal.Decimal
	ImageGenerationCallPrice    float64
}

func BuildTextBillingQuote(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent string) TextBillingQuote {
	if usage == nil {
		// No usage or observed output was supplied by the response handler.
		// A reservation is not evidence of execution, including per-call models.
		return TextBillingQuote{Usage: &dto.Usage{InputTokenSource: dto.UsageSourceUnavailable, OutputTokenSource: dto.UsageSourceUnavailable}, ExtraContent: extraContent + "（用量不可用，未按预留结算）"}
	}

	if usage.ImageUsage != nil {
		units := billing.Usage{Images: usage.ImageUsage}
		quote := QuoteModelUsage(relayInfo, relayInfo.OriginModelName, units, nil)
		return TextBillingQuote{Usage: usage, Quote: quote, Quota: QuoteToQuota(quote), BillingUsage: units, ExtraContent: extraContent}
	}

	usagepkg.NormalizeDTOUsageFields(usage)
	usageSummary := usagepkg.SummaryFromDTO(usage)

	modelName := relayInfo.OriginModelName
	result := TextBillingQuote{
		Usage:        usage,
		ExtraContent: extraContent,
	}

	if relayInfo.ResponsesUsageInfo != nil {
		if webSearchTool := ResponsesWebSearchToolInfo(relayInfo); webSearchTool != nil && webSearchTool.CallCount > 0 {
			result.WebSearchPrice = operation_setting.GetWebSearchPricePerThousand(modelName, webSearchTool.SearchContextSize)
			result.WebSearchMicrousd = decimal.NewFromFloat(result.WebSearchPrice).
				Mul(decimal.NewFromInt(int64(webSearchTool.CallCount))).
				Div(decimal.NewFromInt(1000)).
				Mul(decimal.NewFromInt(billing.MicroUSDPerUSD))
			result.ExtraContent += fmt.Sprintf("Web Search 调用 %d 次，上下文大小 %s，调用花费 %s",
				webSearchTool.CallCount, webSearchTool.SearchContextSize, result.WebSearchMicrousd.String())
		}
	} else if strings.HasSuffix(modelName, "search-preview") {
		searchContextSize := ctx.GetString("chat_completion_web_search_context_size")
		if searchContextSize == "" {
			searchContextSize = "medium"
		}
		result.WebSearchPrice = operation_setting.GetWebSearchPricePerThousand(modelName, searchContextSize)
		result.WebSearchMicrousd = decimal.NewFromFloat(result.WebSearchPrice).
			Div(decimal.NewFromInt(1000)).
			Mul(decimal.NewFromInt(billing.MicroUSDPerUSD))
		result.ExtraContent += fmt.Sprintf("Web Search 调用 1 次，上下文大小 %s，调用花费 %s",
			searchContextSize, result.WebSearchMicrousd.String())
	}

	result.ClaudeWebSearchCallCount = ctx.GetInt("claude_web_search_requests")
	if result.ClaudeWebSearchCallCount > 0 {
		result.ClaudeWebSearchPrice = operation_setting.GetClaudeWebSearchPricePerThousand()
		result.ClaudeWebSearchMicrousd = decimal.NewFromFloat(result.ClaudeWebSearchPrice).
			Div(decimal.NewFromInt(1000)).
			Mul(decimal.NewFromInt(billing.MicroUSDPerUSD)).
			Mul(decimal.NewFromInt(int64(result.ClaudeWebSearchCallCount)))
		result.ExtraContent += fmt.Sprintf("Claude Web Search 调用 %d 次，调用花费 %s",
			result.ClaudeWebSearchCallCount, result.ClaudeWebSearchMicrousd.String())
	}

	if relayInfo.ResponsesUsageInfo != nil {
		if fileSearchTool, exists := relayInfo.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch]; exists && fileSearchTool.CallCount > 0 {
			result.FileSearchPrice = operation_setting.GetFileSearchPricePerThousand()
			result.FileSearchMicrousd = decimal.NewFromFloat(result.FileSearchPrice).
				Mul(decimal.NewFromInt(int64(fileSearchTool.CallCount))).
				Div(decimal.NewFromInt(1000)).
				Mul(decimal.NewFromInt(billing.MicroUSDPerUSD))
			result.ExtraContent += fmt.Sprintf("File Search 调用 %d 次，调用花费 %s",
				fileSearchTool.CallCount, result.FileSearchMicrousd.String())
		}
	}

	if ctx.GetBool("image_generation_call") {
		result.ImageGenerationCallPrice = operation_setting.GetGPTImage1PriceOnceCall(ctx.GetString("image_generation_call_quality"), ctx.GetString("image_generation_call_size"))
		result.ImageGenerationCallMicrousd = decimal.NewFromFloat(result.ImageGenerationCallPrice).Mul(decimal.NewFromInt(billing.MicroUSDPerUSD))
		result.ExtraContent += fmt.Sprintf("Image Generation Call 花费 %s", result.ImageGenerationCallMicrousd.String())
	}

	audioTokens := usageSummary.AudioInputTokens

	if !relayInfo.PriceData.UsePrice && audioTokens > 0 {
		result.AudioInputPrice = operation_setting.GetGeminiInputAudioPricePerMillionTokens(modelName)
		if result.AudioInputPrice > 0 {
			result.AudioInputMicrousd = decimal.NewFromFloat(result.AudioInputPrice).Mul(decimal.NewFromInt(int64(audioTokens)))
			result.ExtraContent += fmt.Sprintf("Audio Input 花费 %s", result.AudioInputMicrousd.String())
		}
	}

	var contextInputTokens int64
	if relayInfo.PriceData.UsePrice {
		result.BillingUsage.RequestCount = 1
	} else {
		result.BillingUsage = usagepkg.BillingUsageFromSummary(usageSummary)
		contextInputTokens = result.BillingUsage.InputTokenTotal()
		if result.AudioInputPrice > 0 {
			result.BillingUsage.AudioInputTokens = 0
		}
	}

	extraLineItems := make([]billing.LineItem, 0, 6)
	addExtraLine := func(dimension string, amount decimal.Decimal) {
		amountMicrousd := QuotaDecimalToMicrousd(amount)
		if amountMicrousd <= 0 {
			return
		}
		extraLineItems = append(extraLineItems, billing.LineItem{
			Dimension:      dimension,
			Quantity:       1,
			UnitMicrousd:   amountMicrousd,
			AmountMicrousd: amountMicrousd,
		})
	}
	addExtraLine("web_search", result.WebSearchMicrousd)
	addExtraLine("claude_web_search", result.ClaudeWebSearchMicrousd)
	addExtraLine("file_search", result.FileSearchMicrousd)
	addExtraLine("audio_input_separate", result.AudioInputMicrousd)
	addExtraLine("image_generation_call", result.ImageGenerationCallMicrousd)

	result.Quote = quoteModelUsageWithInputTotal(relayInfo, modelName, result.BillingUsage, extraLineItems, contextInputTokens)
	result.Quota = QuoteToQuota(result.Quote)
	return result
}

func ResponsesWebSearchToolInfo(relayInfo *relaycommon.RelayInfo) *relaycommon.BuildInToolInfo {
	if relayInfo == nil || relayInfo.ResponsesUsageInfo == nil || relayInfo.ResponsesUsageInfo.BuiltInTools == nil {
		return nil
	}
	if tool, exists := relayInfo.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearch]; exists {
		return tool
	}
	if tool, exists := relayInfo.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]; exists {
		return tool
	}
	return nil
}

func QuoteCostUSD(quote billing.QuoteResult) float64 {
	return decimal.NewFromInt(quote.TotalMicrousd.Int64()).
		Div(decimal.NewFromInt(billing.MicroUSDPerUSD)).
		InexactFloat64()
}

func AnnotateUsageCost(ctx *gin.Context, usage *dto.Usage, quote billing.QuoteResult) {
	if usage == nil {
		return
	}
	cost := QuoteCostUSD(quote)
	usage.Cost = cost
	AttachCostHeaders(ctx, quote)
}

func AttachCostHeaders(ctx *gin.Context, quote billing.QuoteResult) {
	if ctx == nil {
		return
	}
	cost := decimal.NewFromInt(quote.TotalMicrousd.Int64()).
		Div(decimal.NewFromInt(billing.MicroUSDPerUSD)).
		StringFixed(6)
	ctx.Header("x-lazu-cost-usd", cost)
	if requestID := ctx.GetString(common.RequestIdKey); requestID != "" {
		ctx.Header("x-lazu-request-id", requestID)
	}
}
