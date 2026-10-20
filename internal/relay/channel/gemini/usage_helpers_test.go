package gemini

import (
	"encoding/base64"
	geminicodec "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/gemini"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
)

func TestGeminiTotalOnlyUsageRequiresKnownInputToSplit(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		metadata                  dto.GeminiUsageMetadata
		fallback, input, output   int
		inputSource, outputSource dto.UsageSource
	}{
		{"unknown input", dto.GeminiUsageMetadata{TotalTokenCount: 100}, 0, 0, 0, dto.UsageSourceUnavailable, dto.UsageSourceUnavailable},
		{"known local input", dto.GeminiUsageMetadata{TotalTokenCount: 100}, 20, 20, 80, dto.UsageSourceLocalEstimate, dto.UsageSourceLocalEstimate},
		{"known provider input", dto.GeminiUsageMetadata{PromptTokenCount: 20, TotalTokenCount: 100}, 0, 20, 80, dto.UsageSourceProvider, dto.UsageSourceProvider},
		{"known provider output", dto.GeminiUsageMetadata{CandidatesTokenCount: 5, TotalTokenCount: 100}, 0, 0, 5, dto.UsageSourceUnavailable, dto.UsageSourceProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{UsagePromptTokensSet: true, UsagePromptTokensAvailable: tc.fallback > 0, UsagePromptTokens: tc.fallback}
			usage := buildGeminiUsageWithLocalFallback(nil, info, tc.metadata)
			if usage.PromptTokens != tc.input || usage.CompletionTokens != tc.output || usage.TotalTokens != 100 {
				t.Fatalf("invented split: %+v", usage)
			}
			if got := usage.Measurement(); got.Input != tc.inputSource || got.Output != tc.outputSource {
				t.Fatalf("wrong sources: %+v", got)
			}
			// The billing normalizer must not reintroduce the unknown total as
			// expensive output after the adapter preserves it for diagnostics.
			summary := usagepkg.SummaryFromDTO(&usage)
			if summary.InputTokens != tc.input || summary.OutputTokens != tc.output {
				t.Fatalf("billing invented a split: %+v", summary)
			}
		})
	}
}

func TestEstimateGeminiInlineAudioTokensStripsMIMEParameters(t *testing.T) {
	audioData := base64.StdEncoding.EncodeToString(make([]byte, 48000))
	tests := []struct {
		name       string
		mimeType   string
		wantTokens int
	}{
		{name: "24 kHz", mimeType: "audio/L16;codec=pcm;rate=24000", wantTokens: 13},
		{name: "16 kHz", mimeType: "audio/L16;codec=pcm;rate=16000", wantTokens: 20},
		{name: "48 kHz", mimeType: "audio/L16;codec=pcm;rate=48000", wantTokens: 6},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tokens, err := estimateGeminiInlineAudioTokens(&dto.GeminiInlineData{
				MimeType: test.mimeType,
				Data:     audioData,
			})
			if err != nil {
				t.Fatalf("estimate parameterized inline audio: %v", err)
			}
			if tokens != test.wantTokens {
				t.Fatalf("expected %d tokens for %s, got %d", test.wantTokens, test.mimeType, tokens)
			}
		})
	}
}

func TestBuildUsageFromGeminiMetadataIncludesToolUsePromptTokens(t *testing.T) {
	usage := geminicodec.UsageOf(dto.GeminiUsageMetadata{
		PromptTokenCount:        120,
		ToolUsePromptTokenCount: 80,
		CandidatesTokenCount:    20,
		ThoughtsTokenCount:      10,
		TotalTokenCount:         230,
		CachedContentTokenCount: 15,
		PromptTokensDetails: []dto.GeminiPromptTokensDetails{
			{Modality: "TEXT", TokenCount: 100},
			{Modality: "AUDIO", TokenCount: 20},
		},
		ToolUsePromptTokensDetails: []dto.GeminiPromptTokensDetails{
			{Modality: "TEXT", TokenCount: 80},
		},
	}, 0)

	if usage.PromptTokens != 200 {
		t.Fatalf("expected prompt tokens 200, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 30 {
		t.Fatalf("expected completion tokens 30, got %d", usage.CompletionTokens)
	}
	if usage.TotalTokens != 230 {
		t.Fatalf("expected total tokens 230, got %d", usage.TotalTokens)
	}
	if usage.CompletionTokenDetails.ReasoningTokens != 10 {
		t.Fatalf("expected reasoning tokens 10, got %d", usage.CompletionTokenDetails.ReasoningTokens)
	}
	if usage.PromptTokensDetails.CachedTokens != 15 {
		t.Fatalf("expected cached tokens 15, got %d", usage.PromptTokensDetails.CachedTokens)
	}
	if usage.PromptTokensDetails.TextTokens != 180 {
		t.Fatalf("expected text prompt tokens 180, got %d", usage.PromptTokensDetails.TextTokens)
	}
	if usage.PromptTokensDetails.AudioTokens != 20 {
		t.Fatalf("expected audio prompt tokens 20, got %d", usage.PromptTokensDetails.AudioTokens)
	}
}

func TestBuildUsageFromGeminiMetadataSplitsImageOutputTokens(t *testing.T) {
	// gemini-3.1-flash-image returns image tokens inside candidatesTokenCount.
	// They must be split out because image output is priced at $60/Mtok while
	// text output is $3/Mtok.
	usage := geminicodec.UsageOf(dto.GeminiUsageMetadata{
		PromptTokenCount:     30,
		CandidatesTokenCount: 1140,
		TotalTokenCount:      1170,
		PromptTokensDetails: []dto.GeminiPromptTokensDetails{
			{Modality: "TEXT", TokenCount: 30},
		},
		CandidatesTokensDetails: []dto.GeminiPromptTokensDetails{
			{Modality: "IMAGE", TokenCount: 1120},
			{Modality: "TEXT", TokenCount: 20},
		},
	}, 0)

	if usage.CompletionTokens != 1140 {
		t.Fatalf("expected completion tokens 1140, got %d", usage.CompletionTokens)
	}
	if usage.CompletionTokenDetails.ImageTokens != 1120 {
		t.Fatalf("expected image output tokens 1120, got %d", usage.CompletionTokenDetails.ImageTokens)
	}
	if usage.CompletionTokenDetails.TextTokens != 20 {
		t.Fatalf("expected text output tokens 20, got %d", usage.CompletionTokenDetails.TextTokens)
	}
}

// Verbatim usageMetadata captured from a reseller for gemini-3.1-flash-image:
// only an IMAGE entry is reported, and candidatesTokenCount exceeds it — the
// remainder is text/thinking and must stay on the output dimension.
func TestBuildUsageFromGeminiMetadataRealUpstreamImageShape(t *testing.T) {
	usage := geminicodec.UsageOf(dto.GeminiUsageMetadata{
		PromptTokenCount:     6,
		CandidatesTokenCount: 1422,
		TotalTokenCount:      1428,
		PromptTokensDetails: []dto.GeminiPromptTokensDetails{
			{Modality: "TEXT", TokenCount: 6},
		},
		CandidatesTokensDetails: []dto.GeminiPromptTokensDetails{
			{Modality: "IMAGE", TokenCount: 1120},
		},
	}, 0)

	if usage.CompletionTokenDetails.ImageTokens != 1120 {
		t.Fatalf("expected image output tokens 1120, got %d", usage.CompletionTokenDetails.ImageTokens)
	}
	if usage.CompletionTokens != 1422 {
		t.Fatalf("expected completion tokens 1422, got %d", usage.CompletionTokens)
	}
	// No TEXT entry was reported, so nothing should be fabricated.
	if usage.CompletionTokenDetails.TextTokens != 0 {
		t.Fatalf("expected text output tokens untouched at 0, got %d", usage.CompletionTokenDetails.TextTokens)
	}
}

func TestBuildUsageFromGeminiMetadataUsesFallbackPromptTokens(t *testing.T) {
	usage := geminicodec.UsageOf(dto.GeminiUsageMetadata{
		PromptTokenCount:     0,
		CandidatesTokenCount: 90,
		ThoughtsTokenCount:   10,
		TotalTokenCount:      110,
	}, 20)

	if usage.PromptTokens != 20 {
		t.Fatalf("expected fallback prompt tokens 20, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 100 {
		t.Fatalf("expected completion tokens 100, got %d", usage.CompletionTokens)
	}
	if usage.PromptTokensDetails.TextTokens != 20 {
		t.Fatalf("expected fallback text prompt tokens 20, got %d", usage.PromptTokensDetails.TextTokens)
	}
}

func TestBuildUsageFromGeminiMetadataRepairsMissingTotal(t *testing.T) {
	metadata := dto.GeminiUsageMetadata{
		PromptTokenCount:     20,
		CandidatesTokenCount: 5,
		ThoughtsTokenCount:   3,
	}

	if !geminicodec.HasUsage(metadata) {
		t.Fatal("expected component token counts to be recognized as provider usage")
	}
	usage := geminicodec.UsageOf(metadata, 0)
	if usage.PromptTokens != 20 || usage.CompletionTokens != 8 || usage.TotalTokens != 28 {
		t.Fatalf("unexpected repaired Gemini usage: %+v", usage)
	}
	if usage.CompletionTokenDetails.ReasoningTokens != 3 {
		t.Fatalf("expected reasoning details to survive: %+v", usage.CompletionTokenDetails)
	}
}

func TestReconcileGeminiUsageTotalAfterFallbackCompletion(t *testing.T) {
	usage := dto.Usage{
		PromptTokens:     20,
		CompletionTokens: 5,
		TotalTokens:      20,
	}

	geminicodec.ReconcileTotal(&usage)

	if usage.TotalTokens != 25 {
		t.Fatalf("expected total to include fallback completion tokens, got %+v", usage)
	}
}

func TestBuildUsageFromGeminiMetadataDoesNotDeriveNegativeCompletion(t *testing.T) {
	usage := geminicodec.UsageOf(dto.GeminiUsageMetadata{
		PromptTokenCount: 20,
		TotalTokenCount:  10,
	}, 0)

	if usage.CompletionTokens != 0 || usage.TotalTokens != 20 {
		t.Fatalf("expected inconsistent provider total to be repaired without negative tokens: %+v", usage)
	}
}

func TestHasGeminiUsageMetadataRejectsEmptyMetadata(t *testing.T) {
	if geminicodec.HasUsage(dto.GeminiUsageMetadata{}) {
		t.Fatal("empty metadata must not suppress local usage fallback")
	}
}
