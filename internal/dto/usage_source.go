package dto

// Usage sources are internal accounting metadata, never accepted from provider
// wire JSON. They do not change the OpenAI/Anthropic usage response schema.
type UsageSource string

const (
	UsageSourceProvider      UsageSource = "provider"
	UsageSourceLocalEstimate UsageSource = "local_estimate"
	UsageSourceUnavailable   UsageSource = "unavailable"
)

type UsageMeasurement struct {
	Input  UsageSource `json:"input"`
	Output UsageSource `json:"output"`
}

func (u *Usage) Measurement() UsageMeasurement {
	if u == nil {
		return UsageMeasurement{Input: UsageSourceUnavailable, Output: UsageSourceUnavailable}
	}
	source := func(explicit UsageSource) UsageSource {
		if explicit != "" {
			return explicit
		}
		return UsageSourceUnavailable
	}
	return UsageMeasurement{
		Input:  source(u.InputTokenSource),
		Output: source(u.OutputTokenSource),
	}
}

// MarkProviderUsage is called only at a trusted upstream decoding boundary,
// before any local fallback changes the counters. A total plus one side can
// also establish the other side; a total alone cannot establish the split.
func (u *Usage) MarkProviderUsage() {
	if u == nil {
		return
	}
	input, output := max(u.PromptTokens, u.InputTokens), max(u.CompletionTokens, u.OutputTokens)
	if u.InputTokenSource == "" && (input > 0 || (output > 0 && u.TotalTokens >= output)) {
		u.InputTokenSource = UsageSourceProvider
	}
	if u.OutputTokenSource == "" && (output > 0 || (input > 0 && u.TotalTokens >= input)) {
		u.OutputTokenSource = UsageSourceProvider
	}
}
