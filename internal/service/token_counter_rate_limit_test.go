package service

import (
	"math"
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
)

func TestRequestTPMTokensIncludesExplicitMaxOutput(t *testing.T) {
	got := RequestTPMTokens(120, &types.TokenCountMeta{MaxTokens: 80})
	if got != 200 {
		t.Fatalf("expected prompt plus max output, got %d", got)
	}
}

func TestRequestTPMTokensMultipliesMaxOutputByCompletionCount(t *testing.T) {
	got := RequestTPMTokens(120, &types.TokenCountMeta{MaxTokens: 80, CompletionCount: 4})
	if got != 440 {
		t.Fatalf("expected prompt plus max output for each completion, got %d", got)
	}
}

func TestRequestTPMTokensFallsBackToPromptOnly(t *testing.T) {
	if got := RequestTPMTokens(120, nil); got != 120 {
		t.Fatalf("expected prompt-only tokens for nil metadata, got %d", got)
	}
	if got := RequestTPMTokens(120, &types.TokenCountMeta{MaxTokens: 0}); got != 120 {
		t.Fatalf("expected prompt-only tokens without explicit max output, got %d", got)
	}
}

func TestRequestTPMTokensSaturatesOnOverflow(t *testing.T) {
	got := RequestTPMTokens(120, &types.TokenCountMeta{MaxTokens: math.MaxInt, CompletionCount: 2})
	if got != math.MaxInt {
		t.Fatalf("expected saturated token count, got %d", got)
	}
}
