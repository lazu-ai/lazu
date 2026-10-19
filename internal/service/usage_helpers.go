package service

import (
	"github.com/lazu-ai/lazu/internal/dto"
)

func ResponseText2Usage(responseText string, modeName string, promptTokens int) *dto.Usage {
	usage := &dto.Usage{InputTokenSource: dto.UsageSourceLocalEstimate, OutputTokenSource: dto.UsageSourceLocalEstimate}
	if promptTokens == 0 {
		usage.InputTokenSource = dto.UsageSourceUnavailable
	}
	usage.PromptTokens = promptTokens
	ctkm := CountTextToken(responseText, modeName)
	usage.CompletionTokens = ctkm
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}

func ValidUsage(usage *dto.Usage) bool {
	return usage != nil && (usage.PromptTokens != 0 ||
		usage.CompletionTokens != 0 ||
		usage.InputTokens != 0 ||
		usage.OutputTokens != 0)
}
