package usage

import (
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/pkg/billing"
)

const (
	DimensionInput           = "input"
	DimensionOutput          = "output"
	DimensionCacheRead       = "cache_read"
	DimensionCacheWrite      = "cache_write"
	DimensionCacheWrite5m    = "cache_write_5m"
	DimensionCacheWrite1h    = "cache_write_1h"
	DimensionCacheMiss       = "cache_miss"
	DimensionAudioInput      = "audio_input"
	DimensionAudioOutput     = "audio_output"
	DimensionImageInput      = "image_input"
	DimensionImageOutput     = "image_output"
	DimensionRequest         = "request"
	DimensionSecond          = "second"
	DimensionWebSearch       = "web_search"
	DimensionClaudeWebSearch = "claude_web_search"
	DimensionFileSearch      = "file_search"
	DimensionImageGeneration = "image_generation_call"
	DimensionImageCount      = "image_count"
	DimensionVideoSeconds    = "video_seconds"
	UnitToken                = "token"
	UnitRequest              = "request"
	UnitSecond               = "second"
	UnitImage                = "image"
)

type DimensionUsage struct {
	Name     string `json:"name"`
	Quantity int64  `json:"quantity"`
	Unit     string `json:"unit"`
}

type Summary struct {
	PromptTokens       int
	CompletionTokens   int
	TotalTokens        int
	InputTokens        int
	OutputTokens       int
	CacheReadTokens    int
	CacheWriteTokens   int
	CacheWrite5mTokens int
	CacheWrite1hTokens int
	CacheMissTokens    int
	AudioInputTokens   int
	AudioOutputTokens  int
	ImageInputTokens   int
	ImageOutputTokens  int
	ReasoningTokens    int
	Dimensions         []DimensionUsage
}

func NormalizeDTOUsageFields(usage *dto.Usage) {
	if usage == nil {
		return
	}
	if usage.PromptTokens == 0 && usage.InputTokens > 0 {
		usage.PromptTokens = usage.InputTokens
	}
	if usage.CompletionTokens == 0 && usage.OutputTokens > 0 {
		usage.CompletionTokens = usage.OutputTokens
	}
	if usage.PromptTokensDetails.CachedTokens == 0 && usage.PromptCacheHitTokens > 0 {
		usage.PromptTokensDetails.CachedTokens = usage.PromptCacheHitTokens
	}
	if usage.PromptTokensDetails.CacheMissTokens == 0 && usage.PromptCacheMissTokens > 0 {
		usage.PromptTokensDetails.CacheMissTokens = usage.PromptCacheMissTokens
	}
	if usage.InputTokensDetails != nil {
		mergeInputTokenDetails(&usage.PromptTokensDetails, *usage.InputTokensDetails)
	}
	// The images API reports the image/text split under output_tokens_details;
	// without this the whole count lands on the flat output rate.
	if usage.OutputTokensDetails != nil {
		mergeOutputTokenDetails(&usage.CompletionTokenDetails, *usage.OutputTokensDetails)
	}
	normalizeInputTokenDetails(&usage.PromptTokensDetails)
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
}

func SummaryFromDTO(usage *dto.Usage) Summary {
	if usage == nil {
		return Summary{}
	}
	NormalizeDTOUsageFields(usage)
	details := usage.PromptTokensDetails
	cacheWrite5m := details.CachedCreation5mTokens
	cacheWrite1h := details.CachedCreation1hTokens
	cacheWriteTotal := details.CacheWriteTokens
	if cacheWriteTotal == 0 {
		cacheWriteTotal = details.CachedCreationTokens
	}
	if cacheWriteTotal == 0 {
		cacheWriteTotal = cacheWrite5m + cacheWrite1h
	}
	cacheWriteUnknown := cacheWriteTotal - cacheWrite5m - cacheWrite1h
	if cacheWriteUnknown < 0 {
		cacheWriteUnknown = 0
	}
	audioInputTokens, imageInputTokens := billableInputModalities(details)
	inputTokens := usage.PromptTokens - imageInputTokens - audioInputTokens
	if !usage.PromptExcludesCache {
		inputTokens -= details.CachedTokens + cacheWriteTotal
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	outputTokens := usage.CompletionTokens - usage.CompletionTokenDetails.AudioTokens - usage.CompletionTokenDetails.ImageTokens
	if outputTokens < 0 {
		outputTokens = 0
	}
	summary := Summary{
		PromptTokens:       usage.PromptTokens,
		CompletionTokens:   usage.CompletionTokens,
		TotalTokens:        usage.TotalTokens,
		InputTokens:        inputTokens,
		OutputTokens:       outputTokens,
		CacheReadTokens:    details.CachedTokens,
		CacheWriteTokens:   cacheWriteTotal,
		CacheWrite5mTokens: cacheWrite5m,
		CacheWrite1hTokens: cacheWrite1h,
		CacheMissTokens:    details.CacheMissTokens,
		AudioInputTokens:   audioInputTokens,
		AudioOutputTokens:  usage.CompletionTokenDetails.AudioTokens,
		ImageInputTokens:   imageInputTokens,
		ImageOutputTokens:  usage.CompletionTokenDetails.ImageTokens,
		ReasoningTokens:    usage.CompletionTokenDetails.ReasoningTokens,
	}
	addTokenDimension := func(name string, quantity int) {
		if quantity > 0 {
			summary.Dimensions = append(summary.Dimensions, DimensionUsage{Name: name, Quantity: int64(quantity), Unit: UnitToken})
		}
	}
	addTokenDimension(DimensionInput, inputTokens)
	addTokenDimension(DimensionCacheRead, details.CachedTokens)
	addTokenDimension(DimensionCacheWrite, cacheWriteUnknown)
	addTokenDimension(DimensionCacheWrite5m, cacheWrite5m)
	addTokenDimension(DimensionCacheWrite1h, cacheWrite1h)
	addTokenDimension(DimensionCacheMiss, details.CacheMissTokens)
	addTokenDimension(DimensionAudioInput, audioInputTokens)
	addTokenDimension(DimensionImageInput, imageInputTokens)
	addTokenDimension(DimensionOutput, outputTokens)
	addTokenDimension(DimensionAudioOutput, usage.CompletionTokenDetails.AudioTokens)
	addTokenDimension(DimensionImageOutput, usage.CompletionTokenDetails.ImageTokens)
	return summary
}

func SummaryFromRealtime(usage *dto.RealtimeUsage) Summary {
	if usage == nil {
		return Summary{}
	}
	return SummaryFromDTO(&dto.Usage{
		PromptTokens:           usage.InputTokens,
		CompletionTokens:       usage.OutputTokens,
		TotalTokens:            usage.TotalTokens,
		PromptTokensDetails:    usage.InputTokenDetails,
		CompletionTokenDetails: usage.OutputTokenDetails,
	})
}

// cached_tokens is a subset of the provider's modality totals. Use the
// provider's cached modality split to remove only cached audio/image; cached
// text is already removed from the flat input bucket.
func billableInputModalities(details dto.InputTokenDetails) (int, int) {
	cachedModalityTokens := details.CachedTokens - details.CachedTokensDetails.TextTokens
	if cachedModalityTokens < 0 {
		cachedModalityTokens = 0
	}
	cachedAudioTokens := details.CachedTokensDetails.AudioTokens
	if cachedAudioTokens > cachedModalityTokens {
		cachedAudioTokens = cachedModalityTokens
	}
	if cachedAudioTokens > details.AudioTokens {
		cachedAudioTokens = details.AudioTokens
	}
	audioTokens := details.AudioTokens - cachedAudioTokens
	if audioTokens < 0 {
		audioTokens = 0
	}
	cachedModalityTokens -= cachedAudioTokens
	cachedImageTokens := details.CachedTokensDetails.ImageTokens
	if cachedImageTokens > cachedModalityTokens {
		cachedImageTokens = cachedModalityTokens
	}
	if cachedImageTokens > details.ImageTokens {
		cachedImageTokens = details.ImageTokens
	}
	imageTokens := details.ImageTokens - cachedImageTokens
	if imageTokens < 0 {
		imageTokens = 0
	}
	return audioTokens, imageTokens
}

func BillingUsageFromSummary(summary Summary) billing.Usage {
	cacheWrite5m := summary.CacheWrite5mTokens
	cacheWrite1h := summary.CacheWrite1hTokens
	cacheWriteUnknown := summary.CacheWriteTokens - cacheWrite5m - cacheWrite1h
	if cacheWriteUnknown > 0 {
		cacheWrite5m += cacheWriteUnknown
	}
	return billing.Usage{
		InputTokens:        int64(summary.InputTokens),
		OutputTokens:       int64(summary.OutputTokens),
		CacheReadTokens:    int64(summary.CacheReadTokens),
		CacheWrite5mTokens: int64(cacheWrite5m),
		CacheWrite1hTokens: int64(cacheWrite1h),
		AudioInputTokens:   int64(summary.AudioInputTokens),
		AudioOutputTokens:  int64(summary.AudioOutputTokens),
		ImageInputTokens:   int64(summary.ImageInputTokens),
		ImageOutputTokens:  int64(summary.ImageOutputTokens),
	}
}

func mergeInputTokenDetails(target *dto.InputTokenDetails, source dto.InputTokenDetails) {
	if target.CachedTokens == 0 {
		target.CachedTokens = source.CachedTokens
	}
	if target.CachedTokensDetails.TextTokens == 0 {
		target.CachedTokensDetails.TextTokens = source.CachedTokensDetails.TextTokens
	}
	if target.CachedTokensDetails.AudioTokens == 0 {
		target.CachedTokensDetails.AudioTokens = source.CachedTokensDetails.AudioTokens
	}
	if target.CachedTokensDetails.ImageTokens == 0 {
		target.CachedTokensDetails.ImageTokens = source.CachedTokensDetails.ImageTokens
	}
	if target.CacheWriteTokens == 0 {
		target.CacheWriteTokens = source.CacheWriteTokens
	}
	if target.CacheMissTokens == 0 {
		target.CacheMissTokens = source.CacheMissTokens
	}
	if target.CachedCreationTokens == 0 {
		target.CachedCreationTokens = source.CachedCreationTokens
	}
	if target.CachedCreation5mTokens == 0 {
		target.CachedCreation5mTokens = source.CachedCreation5mTokens
	}
	if target.CachedCreation1hTokens == 0 {
		target.CachedCreation1hTokens = source.CachedCreation1hTokens
	}
	if target.TextTokens == 0 {
		target.TextTokens = source.TextTokens
	}
	if target.AudioTokens == 0 {
		target.AudioTokens = source.AudioTokens
	}
	if target.ImageTokens == 0 {
		target.ImageTokens = source.ImageTokens
	}
}

func mergeOutputTokenDetails(target *dto.OutputTokenDetails, source dto.OutputTokenDetails) {
	if target.TextTokens == 0 {
		target.TextTokens = source.TextTokens
	}
	if target.AudioTokens == 0 {
		target.AudioTokens = source.AudioTokens
	}
	if target.ImageTokens == 0 {
		target.ImageTokens = source.ImageTokens
	}
	if target.ReasoningTokens == 0 {
		target.ReasoningTokens = source.ReasoningTokens
	}
}

func normalizeInputTokenDetails(details *dto.InputTokenDetails) {
	if details == nil {
		return
	}
	if details.CachedCreationTokens == 0 && details.CacheWriteTokens > 0 {
		details.CachedCreationTokens = details.CacheWriteTokens
	}
	if details.CacheWriteTokens == 0 {
		if details.CachedCreationTokens > 0 {
			details.CacheWriteTokens = details.CachedCreationTokens
		} else {
			details.CacheWriteTokens = details.CachedCreation5mTokens + details.CachedCreation1hTokens
		}
	}
	if details.CachedCreationTokens == 0 {
		details.CachedCreationTokens = details.CachedCreation5mTokens + details.CachedCreation1hTokens
	}
}
