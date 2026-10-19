package claude

import (
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
)

const (
	ThinkingMinBudgetTokens = 1024
	ThinkingMinMaxTokens    = 1280
)

func parseThinkingSuffix(model string) string {
	if strings.HasSuffix(model, "-thinking") {
		return "-thinking"
	}
	if strings.HasSuffix(model, "#thinking") {
		return "#thinking"
	}
	return ""
}

func TrimThinkingSuffix(model string) (string, string) {
	suffix := parseThinkingSuffix(model)
	if suffix == "" {
		return model, ""
	}
	return strings.TrimSuffix(model, suffix), suffix
}

func TrimEffortSuffix(model string) (string, string, bool) {
	for _, suffix := range []string{"-max", "-xhigh", "-high", "-medium", "-low", "-minimal"} {
		if strings.HasSuffix(model, suffix) {
			return strings.TrimSuffix(model, suffix), strings.TrimPrefix(suffix, "-"), true
		}
	}
	return model, "", false
}

func SupportsAdaptiveThinkingModel(model string) bool {
	return strings.HasPrefix(model, "claude-opus-4-6") || strings.HasPrefix(model, "claude-opus-4-7")
}

func ApplyAdaptiveThinking(request *dto.ClaudeRequest, effort string) {
	if request == nil {
		return
	}
	if effort == "" {
		effort = "high"
	}

	request.Thinking = &dto.Thinking{
		Type: "adaptive",
	}
	request.OutputConfig = []byte(fmt.Sprintf(`{"effort":"%s"}`, effort))
	request.TopP = 0
	request.TopK = 0

	if strings.HasPrefix(request.Model, "claude-opus-4-7") {
		request.Thinking.Display = "summarized"
		request.Temperature = nil
		return
	}

	request.Temperature = common.GetPointer[float64](1.0)
}

func EffortToPercentage(effort string) float64 {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "high":
		return 0.8
	case "medium":
		return 0.5
	case "low":
		return 0.2
	default:
		return 0
	}
}

// EnsureThinkingConstraints applies Anthropic extended thinking constraints.
// It only mutates the request when thinking is enabled.
func EnsureThinkingConstraints(request *dto.ClaudeRequest) {
	if request == nil || request.Thinking == nil {
		return
	}

	if request.Thinking.Type == "adaptive" {
		request.TopP = 0
		request.TopK = 0
		if strings.HasPrefix(request.Model, "claude-opus-4-7") {
			if request.Thinking.Display == "" {
				request.Thinking.Display = "summarized"
			}
			request.Temperature = nil
		}
		return
	}

	if request.Thinking.Type != "enabled" {
		return
	}

	if request.MaxTokens < ThinkingMinMaxTokens {
		request.MaxTokens = ThinkingMinMaxTokens
	}

	budgetTokens := request.Thinking.GetBudgetTokens()
	if budgetTokens <= 0 {
		percentage := model_setting.GetClaudeSettings().ThinkingAdapterBudgetTokensPercentage
		budgetTokens = int(float64(request.MaxTokens) * percentage)
	}

	if budgetTokens < ThinkingMinBudgetTokens {
		budgetTokens = ThinkingMinBudgetTokens
	}
	if request.MaxTokens < uint(budgetTokens) {
		request.MaxTokens = uint(budgetTokens)
	}

	request.Thinking.BudgetTokens = common.GetPointer[int](budgetTokens)

	// https://docs.anthropic.com/en/docs/build-with-claude/extended-thinking#important-considerations-when-using-extended-thinking
	request.TopP = 0
	request.Temperature = common.GetPointer[float64](1.0)
}

func GetDefaultMaxTokensForThinkingModel(originalModel, normalizedModel string) int {
	settings := model_setting.GetClaudeSettings()
	if maxTokens, ok := settings.DefaultMaxTokens[originalModel]; ok {
		return maxTokens
	}
	return settings.GetDefaultMaxTokens(normalizedModel)
}
