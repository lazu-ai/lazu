package common

import "strings"

const (
	DecisionsEnabledOption                       = "DecisionsEnabled"
	DecisionQuestionGenerationEnabledOption      = "DecisionQuestionGenerationEnabled"
	DecisionQuestionGenerationDefaultModelOption = "DecisionQuestionGenerationDefaultModel"
	OpenAIDecisionModel                          = "gpt-6-luna-decisions"
	OpenAIDecisionRequestModel                   = "gpt-6-luna"
)

func DecisionsEnabled() bool { return decisionOption(DecisionsEnabledOption) == "true" }
func DecisionQuestionGenerationEnabled() bool {
	return decisionOption(DecisionQuestionGenerationEnabledOption) == "true"
}
func DecisionQuestionGenerationDefaultModel() string {
	if model := strings.TrimSpace(decisionOption(DecisionQuestionGenerationDefaultModelOption)); model != "" {
		return model
	}
	return "gpt-6-luna"
}
func decisionOption(key string) string {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	return OptionMap[key]
}

func IsDecisionModel(model string) bool {
	return model == OpenAIDecisionModel || strings.HasPrefix(model, "jev-") || model == "clef" || model == "clef-flash"
}

// DecisionModelSupportsDialect keeps a public decision alias on its own
// protocol. The ordinary gpt-6-luna model remains a text-generation model.
func DecisionModelSupportsDialect(model, dialect string) bool {
	switch {
	case model == OpenAIDecisionModel:
		return dialect == "openai"
	case strings.HasPrefix(model, "jev-"):
		return dialect == "typesafe" || dialect == "openrouter" || dialect == "cloudflare_gateway"
	case model == "clef" || model == "clef-flash":
		return dialect == "cloudflare_workers_ai"
	default:
		return false
	}
}
