package gemini

import (
	"mime"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
)

func estimateGeminiInlineAudioTokens(inlineData *dto.GeminiInlineData) (int, error) {
	if inlineData == nil || inlineData.Data == "" {
		return 0, nil
	}
	mimeType := strings.ToLower(strings.TrimSpace(inlineData.MimeType))
	sampleRate := 0
	if mediaType, parameters, err := mime.ParseMediaType(mimeType); err == nil {
		mimeType = strings.ToLower(mediaType)
		if rawRate := strings.TrimSpace(parameters["rate"]); rawRate != "" {
			if parsedRate, parseErr := strconv.Atoi(rawRate); parseErr == nil && parsedRate > 0 {
				sampleRate = parsedRate
			}
		}
	} else if parameterIndex := strings.IndexByte(mimeType, ';'); parameterIndex >= 0 {
		mimeType = strings.TrimSpace(mimeType[:parameterIndex])
	}
	if !strings.HasPrefix(mimeType, "audio/") {
		return 0, nil
	}

	format := strings.TrimPrefix(mimeType, "audio/")
	switch format {
	case "l16", "pcm", "pcm16", "raw":
		format = "pcm16"
	case "pcmu", "mulaw", "g711_ulaw":
		format = "g711_ulaw"
	case "pcma", "alaw", "g711_alaw":
		format = "g711_alaw"
	}
	if format == "pcm16" && sampleRate > 0 {
		return service.CountPCM16AudioTokenOutput(inlineData.Data, sampleRate)
	}
	return service.CountAudioTokenOutput(inlineData.Data, format)
}

func applyGeminiStreamFallbackUsage(
	usage *dto.Usage,
	responseText string,
	modelName string,
	fallbackPromptTokens int,
	imageCount int,
	audioTokens int,
	hasProviderUsage bool,
) {
	if usage == nil {
		return
	}
	if usage.CompletionTokens == 0 {
		textTokens := 0
		if responseText != "" {
			textTokens = service.CountTextToken(responseText, modelName)
		}
		imageTokens := imageCount * 258
		usage.CompletionTokens = textTokens + imageTokens + audioTokens
		usage.OutputTokenSource = dto.UsageSourceLocalEstimate
		usage.CompletionTokenDetails.ImageTokens = imageTokens
		usage.CompletionTokenDetails.AudioTokens = audioTokens
		if usage.CompletionTokens > 0 && usage.PromptTokens == 0 {
			usage.PromptTokens = fallbackPromptTokens
			usage.InputTokenSource = dto.UsageSourceUnavailable
			if fallbackPromptTokens > 0 {
				usage.InputTokenSource = dto.UsageSourceLocalEstimate
			}
		}
	}
	if usage.CompletionTokens == 0 && !hasProviderUsage && usage.PromptTokens == 0 {
		*usage = dto.Usage{}
	}
	reconcileGeminiUsageTotal(usage)
}

func hasGeminiUsageMetadata(metadata dto.GeminiUsageMetadata) bool {
	return metadata.PromptTokenCount != 0 ||
		metadata.ToolUsePromptTokenCount != 0 ||
		metadata.CandidatesTokenCount != 0 ||
		metadata.TotalTokenCount != 0 ||
		metadata.ThoughtsTokenCount != 0 ||
		metadata.CachedContentTokenCount != 0 ||
		len(metadata.PromptTokensDetails) != 0 ||
		len(metadata.ToolUsePromptTokensDetails) != 0 ||
		len(metadata.CandidatesTokensDetails) != 0
}

func applyGeminiPromptTokenDetails(usage *dto.Usage, details []dto.GeminiPromptTokensDetails) {
	if usage == nil || len(details) == 0 {
		return
	}

	textTokens := 0
	audioTokens := 0
	imageTokens := 0

	for _, detail := range details {
		switch strings.ToUpper(detail.Modality) {
		case "TEXT":
			textTokens += detail.TokenCount
		case "AUDIO":
			audioTokens += detail.TokenCount
		case "IMAGE", "VIDEO":
			imageTokens += detail.TokenCount
		}
	}

	usage.PromptTokensDetails.TextTokens = textTokens
	usage.PromptTokensDetails.AudioTokens = audioTokens
	usage.PromptTokensDetails.ImageTokens = imageTokens
}

// applyGeminiCandidatesTokenDetails splits output tokens by modality. Image
// output is priced separately from text output (e.g. $60/Mtok vs $3/Mtok on
// gemini-3.1-flash-image), so it must not stay folded into candidatesTokenCount.
func applyGeminiCandidatesTokenDetails(usage *dto.Usage, details []dto.GeminiPromptTokensDetails) {
	if usage == nil || len(details) == 0 {
		return
	}

	textTokens := 0
	audioTokens := 0
	imageTokens := 0

	for _, detail := range details {
		switch strings.ToUpper(detail.Modality) {
		case "TEXT":
			textTokens += detail.TokenCount
		case "AUDIO":
			audioTokens += detail.TokenCount
		case "IMAGE", "VIDEO":
			imageTokens += detail.TokenCount
		}
	}

	// Real responses often carry only an IMAGE entry: candidatesTokenCount 1422
	// with details [{IMAGE, 1120}]. The 302-token remainder is text/thinking and
	// stays on the output dimension, so only assign what the provider reported.
	if textTokens > 0 {
		usage.CompletionTokenDetails.TextTokens = textTokens
	}
	if audioTokens > 0 {
		usage.CompletionTokenDetails.AudioTokens = audioTokens
	}
	usage.CompletionTokenDetails.ImageTokens = imageTokens
}

func mergeGeminiPromptTokenDetails(usage *dto.Usage, details []dto.GeminiPromptTokensDetails) {
	if usage == nil || len(details) == 0 {
		return
	}

	var detailUsage dto.Usage
	applyGeminiPromptTokenDetails(&detailUsage, details)

	usage.PromptTokensDetails.TextTokens += detailUsage.PromptTokensDetails.TextTokens
	usage.PromptTokensDetails.AudioTokens += detailUsage.PromptTokensDetails.AudioTokens
	usage.PromptTokensDetails.ImageTokens += detailUsage.PromptTokensDetails.ImageTokens
}

func reconcileGeminiUsageTotal(usage *dto.Usage) {
	if usage == nil {
		return
	}
	componentTotal := usage.PromptTokens + usage.CompletionTokens
	if usage.TotalTokens < componentTotal {
		usage.TotalTokens = componentTotal
	}
}

func buildGeminiUsageWithLocalFallback(c *gin.Context, info *relaycommon.RelayInfo, metadata dto.GeminiUsageMetadata) dto.Usage {
	fallback := 0
	if metadata.PromptTokenCount+metadata.ToolUsePromptTokenCount <= 0 {
		fallback = service.PromptTokensForUsage(c, info)
	}
	return buildUsageFromGeminiMetadata(metadata, fallback)
}

func buildUsageFromGeminiMetadata(metadata dto.GeminiUsageMetadata, fallbackPromptTokens int) dto.Usage {
	promptTokens := metadata.PromptTokenCount + metadata.ToolUsePromptTokenCount
	if promptTokens <= 0 && fallbackPromptTokens > 0 {
		promptTokens = fallbackPromptTokens
	}

	usage := dto.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: metadata.CandidatesTokenCount + metadata.ThoughtsTokenCount,
		TotalTokens:      metadata.TotalTokenCount,
	}
	if metadata.PromptTokenCount+metadata.ToolUsePromptTokenCount > 0 {
		usage.InputTokenSource = dto.UsageSourceProvider
	}
	if usage.CompletionTokens > 0 {
		usage.OutputTokenSource = dto.UsageSourceProvider
	}
	if metadata.PromptTokenCount+metadata.ToolUsePromptTokenCount <= 0 {
		usage.InputTokenSource = dto.UsageSourceUnavailable
		if promptTokens > 0 {
			usage.InputTokenSource = dto.UsageSourceLocalEstimate
		}
	}

	usage.CompletionTokenDetails.ReasoningTokens = metadata.ThoughtsTokenCount
	usage.PromptTokensDetails.CachedTokens = metadata.CachedContentTokenCount

	mergeGeminiPromptTokenDetails(&usage, metadata.PromptTokensDetails)
	mergeGeminiPromptTokenDetails(&usage, metadata.ToolUsePromptTokensDetails)
	applyGeminiCandidatesTokenDetails(&usage, metadata.CandidatesTokensDetails)

	if usage.PromptTokens > 0 && usage.TotalTokens >= usage.PromptTokens && usage.CompletionTokens <= 0 {
		usage.CompletionTokens = usage.TotalTokens - usage.PromptTokens
		if usage.InputTokenSource != "" {
			usage.OutputTokenSource = usage.InputTokenSource
		}
	}
	reconcileGeminiUsageTotal(&usage)

	if usage.PromptTokens > 0 &&
		usage.PromptTokensDetails.TextTokens == 0 &&
		usage.PromptTokensDetails.AudioTokens == 0 &&
		usage.PromptTokensDetails.ImageTokens == 0 {
		usage.PromptTokensDetails.TextTokens = usage.PromptTokens
	}

	return usage
}
