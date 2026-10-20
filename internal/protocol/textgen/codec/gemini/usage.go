package gemini

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
)

// HasUsage says whether the reply reported any usage.
func HasUsage(metadata dto.GeminiUsageMetadata) bool {
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

func applyPromptTokenDetails(usage *dto.Usage, details []dto.GeminiPromptTokensDetails) {
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

// applyCandidatesTokenDetails splits output tokens by modality. Image
// output is priced separately from text output (e.g. $60/Mtok vs $3/Mtok on
// gemini-3.1-flash-image), so it must not stay folded into candidatesTokenCount.
func applyCandidatesTokenDetails(usage *dto.Usage, details []dto.GeminiPromptTokensDetails) {
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

func mergePromptTokenDetails(usage *dto.Usage, details []dto.GeminiPromptTokensDetails) {
	if usage == nil || len(details) == 0 {
		return
	}

	var detailUsage dto.Usage
	applyPromptTokenDetails(&detailUsage, details)

	usage.PromptTokensDetails.TextTokens += detailUsage.PromptTokensDetails.TextTokens
	usage.PromptTokensDetails.AudioTokens += detailUsage.PromptTokensDetails.AudioTokens
	usage.PromptTokensDetails.ImageTokens += detailUsage.PromptTokensDetails.ImageTokens
}

// ReconcileTotal raises the total to at least input plus output.
func ReconcileTotal(usage *dto.Usage) {
	if usage == nil {
		return
	}
	componentTotal := usage.PromptTokens + usage.CompletionTokens
	if usage.TotalTokens < componentTotal {
		usage.TotalTokens = componentTotal
	}
}

// UsageOf is Gemini's usage metadata as the shared usage fact: input with
// tool-use prompt tokens, output with thoughts, per-modality details, the
// cached count; fallbackPromptTokens stands in for an input it did not
// report.
func UsageOf(metadata dto.GeminiUsageMetadata, fallbackPromptTokens int) dto.Usage {
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

	mergePromptTokenDetails(&usage, metadata.PromptTokensDetails)
	mergePromptTokenDetails(&usage, metadata.ToolUsePromptTokensDetails)
	applyCandidatesTokenDetails(&usage, metadata.CandidatesTokensDetails)

	if usage.PromptTokens > 0 && usage.TotalTokens >= usage.PromptTokens && usage.CompletionTokens <= 0 {
		usage.CompletionTokens = usage.TotalTokens - usage.PromptTokens
		if usage.InputTokenSource != "" {
			usage.OutputTokenSource = usage.InputTokenSource
		}
	}
	ReconcileTotal(&usage)

	if usage.PromptTokens > 0 &&
		usage.PromptTokensDetails.TextTokens == 0 &&
		usage.PromptTokensDetails.AudioTokens == 0 &&
		usage.PromptTokensDetails.ImageTokens == 0 {
		usage.PromptTokensDetails.TextTokens = usage.PromptTokens
	}

	return usage
}
