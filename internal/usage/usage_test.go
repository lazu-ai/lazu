package usage

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
)

func TestInputTokenDetailsAcceptsCacheWriteAliases(t *testing.T) {
	var details dto.InputTokenDetails
	if err := json.Unmarshal([]byte(`{
		"cached_tokens": 10,
		"cache_creation_input_tokens": 20,
		"cache_write_5m_tokens": 7,
		"cache_write_1h_tokens": 13,
		"cache_miss_tokens": 5,
		"text_tokens": 80
	}`), &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}

	if details.CachedTokens != 10 {
		t.Fatalf("expected cached tokens=10, got %d", details.CachedTokens)
	}
	if details.CacheWriteTokens != 0 {
		t.Fatalf("expected raw cache_write_tokens to stay 0 when only alias is provided, got %d", details.CacheWriteTokens)
	}
	if details.CachedCreationTokens != 20 {
		t.Fatalf("expected cache creation tokens=20, got %d", details.CachedCreationTokens)
	}
	if details.CachedCreation5mTokens != 7 || details.CachedCreation1hTokens != 13 {
		t.Fatalf("expected cache write buckets 7/13, got %d/%d", details.CachedCreation5mTokens, details.CachedCreation1hTokens)
	}
	if details.CacheMissTokens != 5 {
		t.Fatalf("expected cache miss tokens=5, got %d", details.CacheMissTokens)
	}

	raw, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal details: %v", err)
	}
	var output map[string]int
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("unmarshal marshaled details: %v", err)
	}
	if output["cache_write_tokens"] != 20 {
		t.Fatalf("expected normalized cache_write_tokens=20, got %d in %s", output["cache_write_tokens"], string(raw))
	}
	if output["cache_write_5m_tokens"] != 7 || output["cache_write_1h_tokens"] != 13 {
		t.Fatalf("expected normalized buckets 7/13, got %v in %s", output, string(raw))
	}
	if output["cache_miss_tokens"] != 5 {
		t.Fatalf("expected normalized cache_miss_tokens=5, got %d", output["cache_miss_tokens"])
	}
}

func TestSummaryFromDTOMapsDeepSeekCacheHitAndMiss(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:          100,
		CompletionTokens:      30,
		TotalTokens:           130,
		PromptCacheHitTokens:  40,
		PromptCacheMissTokens: 60,
	}

	summary := SummaryFromDTO(usage)
	if summary.CacheReadTokens != 40 {
		t.Fatalf("expected cache read tokens=40, got %d", summary.CacheReadTokens)
	}
	if summary.CacheMissTokens != 60 {
		t.Fatalf("expected cache miss tokens=60, got %d", summary.CacheMissTokens)
	}
	if summary.InputTokens != 60 {
		t.Fatalf("expected billable input tokens=60, got %d", summary.InputTokens)
	}
	if summary.OutputTokens != 30 {
		t.Fatalf("expected output tokens=30, got %d", summary.OutputTokens)
	}
}

func TestBillingUsageFromSummaryFallsBackUnknownCacheWriteToFiveMinuteBucket(t *testing.T) {
	summary := Summary{
		InputTokens:        50,
		OutputTokens:       20,
		CacheReadTokens:    10,
		CacheWriteTokens:   30,
		CacheWrite5mTokens: 7,
		CacheWrite1hTokens: 3,
	}

	billingUsage := BillingUsageFromSummary(summary)
	if billingUsage.CacheWrite5mTokens != 27 {
		t.Fatalf("expected unknown cache write to use 5m bucket, got %d", billingUsage.CacheWrite5mTokens)
	}
	if billingUsage.CacheWrite1hTokens != 3 {
		t.Fatalf("expected 1h bucket=3, got %d", billingUsage.CacheWrite1hTokens)
	}
}

func TestSummaryFromRealtimePreservesBillableDimensions(t *testing.T) {
	summary := SummaryFromRealtime(&dto.RealtimeUsage{
		InputTokens:  100,
		OutputTokens: 35,
		TotalTokens:  135,
		InputTokenDetails: dto.InputTokenDetails{
			CachedTokens: 20,
			AudioTokens:  15,
			ImageTokens:  5,
		},
		OutputTokenDetails: dto.OutputTokenDetails{
			AudioTokens:     10,
			ImageTokens:     5,
			ReasoningTokens: 8,
		},
	})

	if summary.InputTokens != 60 || summary.CacheReadTokens != 20 {
		t.Fatalf("unexpected realtime input split: %+v", summary)
	}
	if summary.AudioInputTokens != 15 || summary.ImageInputTokens != 5 {
		t.Fatalf("unexpected realtime input modalities: %+v", summary)
	}
	if summary.OutputTokens != 20 || summary.AudioOutputTokens != 10 || summary.ImageOutputTokens != 5 {
		t.Fatalf("unexpected realtime output split: %+v", summary)
	}
	if summary.ReasoningTokens != 8 {
		t.Fatalf("expected reasoning tokens to survive: %+v", summary)
	}

	billingUsage := BillingUsageFromSummary(summary)
	if billingUsage.InputTokens != 60 || billingUsage.CacheReadTokens != 20 ||
		billingUsage.AudioInputTokens != 15 || billingUsage.ImageInputTokens != 5 ||
		billingUsage.OutputTokens != 20 || billingUsage.AudioOutputTokens != 10 ||
		billingUsage.ImageOutputTokens != 5 {
		t.Fatalf("unexpected realtime billing usage: %+v", billingUsage)
	}
}

func TestSummaryFromRealtimeRemovesCachedAudioOverlap(t *testing.T) {
	realtimeUsage := &dto.RealtimeUsage{
		InputTokens:  100,
		OutputTokens: 10,
		TotalTokens:  110,
		InputTokenDetails: dto.InputTokenDetails{
			CachedTokens: 20,
			TextTokens:   60,
			AudioTokens:  40,
			CachedTokensDetails: dto.CachedTokenDetails{
				AudioTokens: 20,
			},
		},
		OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 10},
	}
	summary := SummaryFromRealtime(realtimeUsage)

	if summary.InputTokens != 60 || summary.CacheReadTokens != 20 || summary.AudioInputTokens != 20 {
		t.Fatalf("expected cached audio to be removed from the billable audio bucket: %+v", summary)
	}
	billingUsage := BillingUsageFromSummary(summary)
	if billingUsage.InputTokens != 60 || billingUsage.CacheReadTokens != 20 || billingUsage.AudioInputTokens != 20 {
		t.Fatalf("expected non-overlapping realtime billing dimensions: %+v", billingUsage)
	}
	if billingUsage.InputTokens+billingUsage.CacheReadTokens+billingUsage.AudioInputTokens != 100 {
		t.Fatalf("expected input billing dimensions to reconcile to provider total: %+v", billingUsage)
	}

	fact := FactFromRealtimeUsage(realtimeUsage, nil, ProviderFamilyOpenAI)
	assertFactDimension(t, fact, DimensionInput, 60, UnitToken)
	assertFactDimension(t, fact, DimensionCacheRead, 20, UnitToken)
	assertFactDimension(t, fact, DimensionAudioInput, 20, UnitToken)
}

func TestSummaryFromRealtimePreservesAudioWhenCachedTokensAreText(t *testing.T) {
	realtimeUsage := &dto.RealtimeUsage{
		InputTokens:  100,
		OutputTokens: 10,
		TotalTokens:  110,
		InputTokenDetails: dto.InputTokenDetails{
			CachedTokens: 20,
			TextTokens:   80,
			AudioTokens:  20,
			CachedTokensDetails: dto.CachedTokenDetails{
				TextTokens: 20,
			},
		},
		OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 10},
	}

	summary := SummaryFromRealtime(realtimeUsage)
	if summary.InputTokens != 60 || summary.CacheReadTokens != 20 || summary.AudioInputTokens != 20 {
		t.Fatalf("cached text must not be subtracted from audio: %+v", summary)
	}
	billingUsage := BillingUsageFromSummary(summary)
	if billingUsage.InputTokens != 60 || billingUsage.CacheReadTokens != 20 || billingUsage.AudioInputTokens != 20 {
		t.Fatalf("expected cached text and uncached audio to retain distinct rates: %+v", billingUsage)
	}
}

func TestFactFromDTOAndFieldsPersistsCanonicalDimensions(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:          100,
		CompletionTokens:      30,
		TotalTokens:           130,
		PromptCacheHitTokens:  40,
		PromptCacheMissTokens: 60,
	}
	fields := map[string]interface{}{
		"web_search_call_count":  float64(2),
		"file_search_call_count": 1,
		"image_generation_call":  "true",
		"admin_info":             map[string]interface{}{"use_channel": []string{"secret"}},
	}

	fact := FactFromDTOAndFields(usage, fields)
	if fact.Version != FactVersion {
		t.Fatalf("expected fact version=%d, got %d", FactVersion, fact.Version)
	}
	assertFactDimension(t, fact, DimensionInput, 60, UnitToken)
	assertFactDimension(t, fact, DimensionCacheRead, 40, UnitToken)
	assertFactDimension(t, fact, DimensionCacheMiss, 60, UnitToken)
	assertFactDimension(t, fact, DimensionWebSearch, 2, UnitRequest)
	assertFactDimension(t, fact, DimensionFileSearch, 1, UnitRequest)
	assertFactDimension(t, fact, DimensionImageGeneration, 1, UnitRequest)
	if _, ok := fact.ProviderRawUsage["admin_info"]; ok {
		t.Fatalf("expected provider raw usage to omit admin_info, got %#v", fact.ProviderRawUsage)
	}

	parsed, ok := ParseFact(fact.JSONString())
	if !ok {
		t.Fatalf("expected serialized usage fact to parse")
	}
	assertFactDimension(t, parsed, DimensionCacheRead, 40, UnitToken)
}

func TestFactFromProviderUsageGoldenMatrix(t *testing.T) {
	tests := []struct {
		name           string
		providerFamily ProviderFamily
		rawUsage       string
		wantFamily     string
		wantDimensions map[string]int64
	}{
		{
			name:           "openai cache read and write",
			providerFamily: ProviderFamilyOpenAI,
			rawUsage: `{
				"prompt_tokens": 100,
				"completion_tokens": 30,
				"total_tokens": 130,
				"prompt_tokens_details": {
					"cached_tokens": 40,
					"cache_write_tokens": 10
				}
			}`,
			wantFamily: string(ProviderFamilyOpenAI),
			wantDimensions: map[string]int64{
				DimensionInput:      50,
				DimensionCacheRead:  40,
				DimensionCacheWrite: 10,
				DimensionOutput:     30,
			},
		},
		{
			name:           "deepseek cache hit and miss",
			providerFamily: ProviderFamilyDeepSeek,
			rawUsage: `{
				"prompt_tokens": 100,
				"completion_tokens": 30,
				"total_tokens": 130,
				"prompt_cache_hit_tokens": 40,
				"prompt_cache_miss_tokens": 60
			}`,
			wantFamily: string(ProviderFamilyDeepSeek),
			wantDimensions: map[string]int64{
				DimensionInput:     60,
				DimensionCacheRead: 40,
				DimensionCacheMiss: 60,
				DimensionOutput:    30,
			},
		},
		{
			name:           "qwen compatible cache write alias",
			providerFamily: ProviderFamilyQwen,
			rawUsage: `{
				"prompt_tokens": 100,
				"completion_tokens": 20,
				"total_tokens": 120,
				"prompt_tokens_details": {
					"cached_tokens": 30,
					"cache_creation_input_tokens": 10
				}
			}`,
			wantFamily: string(ProviderFamilyQwen),
			wantDimensions: map[string]int64{
				DimensionInput:      60,
				DimensionCacheRead:  30,
				DimensionCacheWrite: 10,
				DimensionOutput:     20,
			},
		},
		{
			name:           "gemini cached content and modalities",
			providerFamily: ProviderFamilyGemini,
			rawUsage: `{
				"prompt_tokens": 180,
				"completion_tokens": 40,
				"total_tokens": 220,
				"prompt_tokens_details": {
					"cached_tokens": 15,
					"text_tokens": 145,
					"audio_tokens": 20,
					"image_tokens": 15
				}
			}`,
			wantFamily: string(ProviderFamilyGemini),
			wantDimensions: map[string]int64{
				DimensionInput:      130,
				DimensionCacheRead:  15,
				DimensionAudioInput: 20,
				DimensionImageInput: 15,
				DimensionOutput:     40,
			},
		},
		{
			name:           "gemini image output split from text output",
			providerFamily: ProviderFamilyGemini,
			rawUsage: `{
				"prompt_tokens": 30,
				"completion_tokens": 1140,
				"total_tokens": 1170,
				"prompt_tokens_details": {
					"text_tokens": 30
				},
				"completion_tokens_details": {
					"text_tokens": 20,
					"image_tokens": 1120
				}
			}`,
			wantFamily: string(ProviderFamilyGemini),
			wantDimensions: map[string]int64{
				DimensionInput:       30,
				DimensionOutput:      20,
				DimensionImageOutput: 1120,
			},
		},
		{
			name:           "gemini image output with no text detail entry",
			providerFamily: ProviderFamilyGemini,
			rawUsage: `{
				"prompt_tokens": 6,
				"completion_tokens": 1422,
				"total_tokens": 1428,
				"prompt_tokens_details": {
					"text_tokens": 6
				},
				"completion_tokens_details": {
					"image_tokens": 1120
				}
			}`,
			wantFamily: string(ProviderFamilyGemini),
			wantDimensions: map[string]int64{
				DimensionInput:       6,
				DimensionOutput:      302,
				DimensionImageOutput: 1120,
			},
		},
		{
			name:           "openai images api output_tokens_details",
			providerFamily: ProviderFamilyOpenAI,
			rawUsage: `{
				"input_tokens": 12,
				"output_tokens": 4360,
				"total_tokens": 4372,
				"input_tokens_details": {"cached_tokens": 0, "text_tokens": 12},
				"output_tokens_details": {"reasoning_tokens": 0, "text_tokens": 200, "image_tokens": 4160}
			}`,
			wantFamily: string(ProviderFamilyOpenAI),
			wantDimensions: map[string]int64{
				DimensionInput:       12,
				DimensionOutput:      200,
				DimensionImageOutput: 4160,
			},
		},
		{
			name:           "claude separated cache input",
			providerFamily: ProviderFamilyAnthropic,
			rawUsage: `{
				"prompt_tokens": 21,
				"completion_tokens": 8,
				"total_tokens": 29,
				"prompt_tokens_details": {
					"cached_tokens": 300,
					"cache_write_tokens": 500,
					"cache_write_5m_tokens": 200,
					"cache_write_1h_tokens": 100
				}
			}`,
			wantFamily: string(ProviderFamilyAnthropic),
			wantDimensions: map[string]int64{
				DimensionInput:        21,
				DimensionCacheRead:    300,
				DimensionCacheWrite:   200,
				DimensionCacheWrite5m: 200,
				DimensionCacheWrite1h: 100,
				DimensionOutput:       8,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var usage dto.Usage
			if err := json.Unmarshal([]byte(tc.rawUsage), &usage); err != nil {
				t.Fatalf("unmarshal usage fixture: %v", err)
			}
			fact := FactFromProviderUsage(&usage, map[string]interface{}{"provider_family": tc.wantFamily}, tc.providerFamily)
			if fact.ProviderFamily != tc.wantFamily {
				t.Fatalf("expected provider family %q, got %q", tc.wantFamily, fact.ProviderFamily)
			}
			for dimension, quantity := range tc.wantDimensions {
				assertFactDimension(t, fact, dimension, quantity, UnitToken)
			}
		})
	}
}

func TestFactFromRealtimeUsageStreamingCacheGolden(t *testing.T) {
	fact := FactFromRealtimeUsage(&dto.RealtimeUsage{
		InputTokens:  100,
		OutputTokens: 35,
		TotalTokens:  135,
		InputTokenDetails: dto.InputTokenDetails{
			CachedTokens: 20,
			TextTokens:   65,
			AudioTokens:  15,
		},
		OutputTokenDetails: dto.OutputTokenDetails{
			TextTokens:  25,
			AudioTokens: 10,
		},
	}, map[string]interface{}{"provider_family": string(ProviderFamilyOpenAI)}, ProviderFamilyOpenAI)

	if fact.TotalTokens != 135 {
		t.Fatalf("expected total tokens=135, got %d", fact.TotalTokens)
	}
	assertFactDimension(t, fact, DimensionInput, 65, UnitToken)
	assertFactDimension(t, fact, DimensionCacheRead, 20, UnitToken)
	assertFactDimension(t, fact, DimensionAudioInput, 15, UnitToken)
	assertFactDimension(t, fact, DimensionOutput, 25, UnitToken)
	assertFactDimension(t, fact, DimensionAudioOutput, 10, UnitToken)
}

func TestProviderFamilyFromHintUsesExplicitProviderShape(t *testing.T) {
	tests := []struct {
		name string
		hint ProviderHint
		want ProviderFamily
	}{
		{name: "anthropic api type wins without model name sniffing", hint: ProviderHint{APIType: constant.APITypeAnthropic, ModelName: "sonnet"}, want: ProviderFamilyAnthropic},
		{name: "deepseek compatible path", hint: ProviderHint{APIType: constant.APITypeOpenAI, BaseURL: "https://api.deepseek.com", ModelName: "chat"}, want: ProviderFamilyDeepSeek},
		{name: "qwen compatible path", hint: ProviderHint{APIType: constant.APITypeOpenAI, ProviderName: "DashScope", ModelName: "qwen-plus"}, want: ProviderFamilyQwen},
		{name: "gemini api type", hint: ProviderHint{APIType: constant.APITypeGemini, ModelName: "gemini-pro"}, want: ProviderFamilyGemini},
		{name: "xai api type", hint: ProviderHint{APIType: constant.APITypeXai, ModelName: "grok"}, want: ProviderFamilyXAI},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProviderFamilyFromHint(tc.hint); got != tc.want {
				t.Fatalf("expected provider family %q, got %q", tc.want, got)
			}
		})
	}
}

func TestFactFromLogFieldsPreservesProviderSeparatedCacheInput(t *testing.T) {
	fact := FactFromLogFields(21, 8, map[string]interface{}{
		"claude":                true,
		"cache_tokens":          300,
		"cache_creation_tokens": 500,
		"cache_write_tokens":    500,
	}, true)

	assertFactDimension(t, fact, DimensionInput, 21, UnitToken)
	assertFactDimension(t, fact, DimensionCacheRead, 300, UnitToken)
	assertFactDimension(t, fact, DimensionCacheWrite, 500, UnitToken)
	assertFactDimension(t, fact, DimensionOutput, 8, UnitToken)
}

func TestFactNormalizeMediaUnitsIsIdempotent(t *testing.T) {
	fact := Fact{
		Version: FactVersion,
		MediaUnits: &MediaUnits{
			VideoSeconds:   5,
			ResolutionTier: "1280x720",
			ProviderUnit:   "video",
		},
	}

	fact = fact.Normalize()
	fact = fact.Normalize()

	assertFactDimension(t, fact, DimensionVideoSeconds, 5, UnitSecond)
	assertFactDimension(t, fact, DimensionSecond, 5, UnitSecond)
}

func assertFactDimension(t *testing.T, fact Fact, name string, quantity int64, unit string) {
	t.Helper()
	for _, dimension := range fact.Dimensions {
		if dimension.Name == name {
			if dimension.Quantity != quantity || dimension.Unit != unit {
				t.Fatalf("expected fact dimension %s=%d %s, got %d %s", name, quantity, unit, dimension.Quantity, dimension.Unit)
			}
			return
		}
	}
	t.Fatalf("expected fact dimension %s in %#v", name, fact.Dimensions)
}

func TestSummaryBillsAnthropicInputBesideItsCache(t *testing.T) {
	// Anthropic reports input_tokens without the cache; the claude adaptor
	// marks the usage so the cache is not subtracted a second time.
	anthropic := &dto.Usage{PromptTokens: 1_840, CompletionTokens: 1_960, PromptExcludesCache: true}
	anthropic.PromptTokensDetails.CachedTokens = 96_512
	anthropic.PromptTokensDetails.CachedCreationTokens = 8_230
	got := BillingUsageFromSummary(SummaryFromDTO(anthropic))
	if got.InputTokens != 1_840 || got.CacheReadTokens != 96_512 || got.CacheWrite5mTokens != 8_230 {
		t.Fatalf("anthropic usage billed as %+v; want input 1840, cache read 96512, cache write 8230", got)
	}

	// OpenAI folds cached tokens into prompt_tokens, so they come back out.
	openai := &dto.Usage{PromptTokens: 18_400, CompletionTokens: 1_180}
	openai.PromptTokensDetails.CachedTokens = 12_000
	if got := BillingUsageFromSummary(SummaryFromDTO(openai)); got.InputTokens != 6_400 || got.CacheReadTokens != 12_000 {
		t.Fatalf("openai usage billed as %+v; want input 6400, cache read 12000", got)
	}
}
