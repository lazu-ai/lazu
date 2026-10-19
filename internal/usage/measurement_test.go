package usage

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestUsageMeasurementSurvivesFactRoundTripWithoutWireTrust(t *testing.T) {
	for _, family := range []ProviderFamily{ProviderFamilyOpenAICompatible, ProviderFamilyAnthropic} {
		u := &dto.Usage{PromptTokens: 8, CompletionTokens: 3, InputTokenSource: dto.UsageSourceLocalEstimate, OutputTokenSource: dto.UsageSourceProvider}
		fact := FactFromProviderUsage(u, nil, family)
		encoded, _ := json.Marshal(fact)
		var restored Fact
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if restored.Measurement == nil || restored.Measurement.Input != dto.UsageSourceLocalEstimate || restored.Measurement.Output != dto.UsageSourceProvider {
			t.Fatalf("lost mixed source: %s", encoded)
		}
		wire, _ := json.Marshal(u)
		if strings.Contains(string(wire), "local_estimate") {
			t.Fatalf("internal field leaked: %s", wire)
		}
		var incoming dto.Usage
		if err := json.Unmarshal([]byte(`{"prompt_tokens":8,"input_token_source":"local_estimate","InputTokenSource":"unavailable"}`), &incoming); err != nil {
			t.Fatal(err)
		}
		if incoming.Measurement().Input != dto.UsageSourceUnavailable {
			t.Fatal("provider can forge accounting source")
		}
	}
}

func TestUntaggedUsageFactDoesNotInventProviderProvenance(t *testing.T) {
	for _, family := range []ProviderFamily{ProviderFamilyOpenAICompatible, ProviderFamilyAnthropic} {
		fact := FactFromProviderUsage(&dto.Usage{PromptTokens: 1, CompletionTokens: 258}, nil, family)
		if fact.Measurement == nil || fact.Measurement.Input != dto.UsageSourceUnavailable || fact.Measurement.Output != dto.UsageSourceUnavailable {
			t.Fatalf("untagged usage incorrectly attributed: %+v", fact.Measurement)
		}
	}
}
