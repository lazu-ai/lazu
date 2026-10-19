package dto

import "testing"

func TestGeneralOpenAIRequestTokenCountMetaIncludesCompletionCount(t *testing.T) {
	req := GeneralOpenAIRequest{
		MaxTokens: 80,
		N:         4,
	}

	meta := req.GetTokenCountMeta()
	if meta.CompletionCount != 4 {
		t.Fatalf("expected completion count from n, got %d", meta.CompletionCount)
	}
}

func TestGeminiChatRequestTokenCountMetaIncludesCandidateCount(t *testing.T) {
	req := GeminiChatRequest{
		GenerationConfig: GeminiChatGenerationConfig{
			MaxOutputTokens: 80,
			CandidateCount:  4,
		},
	}

	meta := req.GetTokenCountMeta()
	if meta.CompletionCount != 4 {
		t.Fatalf("expected completion count from candidateCount, got %d", meta.CompletionCount)
	}
}
