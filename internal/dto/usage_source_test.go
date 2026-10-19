package dto

import "testing"

func TestUsageMeasurementRequiresExplicitProvenance(t *testing.T) {
	for _, usage := range []*Usage{nil, {}, {PromptTokens: 8, CompletionTokens: 258, TotalTokens: 266}} {
		if got := usage.Measurement(); got.Input != UsageSourceUnavailable || got.Output != UsageSourceUnavailable {
			t.Fatalf("untagged counters acquired provenance: %+v", got)
		}
	}
}

func TestMarkProviderUsageBeforeFallback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage Usage
		want  UsageMeasurement
	}{
		{"canonical", Usage{PromptTokens: 8, CompletionTokens: 3}, UsageMeasurement{UsageSourceProvider, UsageSourceProvider}},
		{"aliases", Usage{InputTokens: 8, OutputTokens: 3}, UsageMeasurement{UsageSourceProvider, UsageSourceProvider}},
		{"partial", Usage{PromptTokens: 8}, UsageMeasurement{UsageSourceProvider, UsageSourceUnavailable}},
		{"total with input", Usage{PromptTokens: 8, TotalTokens: 11}, UsageMeasurement{UsageSourceProvider, UsageSourceProvider}},
		{"total without split", Usage{TotalTokens: 11}, UsageMeasurement{UsageSourceUnavailable, UsageSourceUnavailable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.usage.MarkProviderUsage()
			if got := tc.usage.Measurement(); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	usage := Usage{PromptTokens: 8}
	usage.MarkProviderUsage()
	usage.CompletionTokens = 258
	usage.OutputTokenSource = UsageSourceLocalEstimate
	if got := usage.Measurement(); got.Input != UsageSourceProvider || got.Output != UsageSourceLocalEstimate {
		t.Fatalf("fallback changed provider provenance: %+v", got)
	}
}
