package common

import (
	"reflect"
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
)

func TestDecisionModelAliasKeepsTextModelEndpoints(t *testing.T) {
	if IsDecisionModel("gpt-6-luna") || IsDecisionModel(OpenAIDecisionModel+"-preview") {
		t.Fatal("ordinary text model or unknown alias became a decision model")
	}
	if !IsDecisionModel(OpenAIDecisionModel) {
		t.Fatal("official decision alias is not registered")
	}
	if got := GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, OpenAIDecisionModel); !reflect.DeepEqual(got, []constant.EndpointType{constant.EndpointTypeDecisions}) {
		t.Fatalf("decision endpoints = %v", got)
	}
	for _, endpoint := range GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "gpt-6-luna") {
		if endpoint == constant.EndpointTypeDecisions {
			t.Fatal("ordinary text model advertises decisions")
		}
	}
}

func TestDecisionModelDialectIsolation(t *testing.T) {
	for _, model := range []string{OpenAIDecisionModel, "gpt-6-luna", "jev-1.13", "clef", "clef-flash", "unknown"} {
		for _, dialect := range []string{"", "openai", "typesafe", "openrouter", "cloudflare_gateway", "cloudflare_workers_ai"} {
			want := model == OpenAIDecisionModel && dialect == "openai" ||
				model == "jev-1.13" && (dialect == "typesafe" || dialect == "openrouter" || dialect == "cloudflare_gateway") ||
				(model == "clef" || model == "clef-flash") && dialect == "cloudflare_workers_ai"
			if got := DecisionModelSupportsDialect(model, dialect); got != want {
				t.Errorf("model %q / dialect %q = %v, want %v", model, dialect, got, want)
			}
		}
	}
}
