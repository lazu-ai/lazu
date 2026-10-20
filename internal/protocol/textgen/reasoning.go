package textgen

// EffortForBudget is the reasoning effort a thinking budget asks for, for an
// upstream that takes an effort and no budget (Chat Completions, Responses).
// The bands mirror the budgets lazu gives each effort the other way
// (model.EffortBudgetTokens: low 4096, medium 8192, high 16384): a budget is
// the effort whose budget it reaches.
func EffortForBudget(budget int) string {
	switch {
	case budget <= 0:
		return ""
	case budget < 8192:
		return "low"
	case budget < 16384:
		return "medium"
	}
	return "high"
}

// ReasoningEffortFor is the effort to send an effort-only upstream: the one
// the client named, else the one its budget reaches.
func ReasoningEffortFor(options ReasoningOptions) string {
	if options.Effort != "" {
		return options.Effort
	}
	if options.Budget.Set {
		return EffortForBudget(options.Budget.Value)
	}
	return ""
}

// ResultText is a function result's text for an upstream with no error flag
// on a result (Chat Completions, Responses): a result the tool reported as
// failed says so in words.
func ResultText(item InputItem) string {
	if item.IsError {
		return "Error: " + item.Output
	}
	return item.Output
}
