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
		// The legacy Cloudflare gateway transport fixes its model to typesafe/jev;
		// it cannot promise a newly requested fixed version or moving alias.
		return dialect == "typesafe" || dialect == "openrouter" || (dialect == "cloudflare_gateway" && model == "jev-1.13")
	case model == "clef" || model == "clef-flash":
		return dialect == "cloudflare_workers_ai"
	default:
		return false
	}
}

// DecisionUpstreamModel applies only when the channel has no explicit mapping.
// Fixed public versions stay pinned; latest remains an opt-in moving alias.
func DecisionUpstreamModel(model, dialect string) string {
	switch dialect {
	case "typesafe":
		switch model {
		case "jev-1.13", "jev-1.14":
			return model + ".0"
		}
	case "openrouter":
		if !strings.HasPrefix(model, "typesafe/") {
			return "typesafe/" + model
		}
	case "openai":
		return "gpt-6-luna"
	}
	return model
}
