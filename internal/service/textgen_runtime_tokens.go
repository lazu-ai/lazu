package service

import textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"

// The attempt planner counts the text a degradation adds to the prompt with
// the gateway's tokenizer (textgenruntime.EstimateTextTokens).
func init() {
	textgenruntime.EstimateTextTokens = CountTextToken
}
