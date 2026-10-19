package service

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
)

func TestBuildClaudeBillingUsageUsesNonCachedInputTokens(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 50,
		TotalTokens:      1050,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         300,
			CachedCreationTokens: 100,
		},
	}

	got := buildClaudeBillingUsage(false, usagepkg.SummaryFromDTO(usage))

	if got.InputTokens != 600 {
		t.Fatalf("expected ordinary input to exclude cache read/write tokens, got %d", got.InputTokens)
	}
	if got.CacheReadTokens != 300 {
		t.Fatalf("expected cache read tokens 300, got %d", got.CacheReadTokens)
	}
	if got.CacheWrite5mTokens != 100 {
		t.Fatalf("expected unknown cache write tokens to bill as 5m cache writes, got %d", got.CacheWrite5mTokens)
	}
	if got.OutputTokens != 50 {
		t.Fatalf("expected output tokens 50, got %d", got.OutputTokens)
	}
}
