package dto

import "testing"

func TestOpenAIDecisionChannelSettings(t *testing.T) {
	settings := ChannelSettings{DecisionDialect: "openai", MaxRPS: 30, MaxInFlight: 128, SharedLimitGroup: "official-decisions", SharedMaxRPS: 30, SharedMaxInFlight: 128}
	if err := settings.ValidateDecisionSettings(); err != nil {
		t.Fatal(err)
	}
	settings.DecisionDialect = "openai_unknown"
	if settings.ValidateDecisionSettings() == nil {
		t.Fatal("unknown dialect was accepted")
	}
}
