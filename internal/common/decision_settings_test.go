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

func TestDecisionUpstreamModels(t *testing.T) {
	for _, tc := range []struct{ model, dialect, want string }{
		{"jev-1.13", "typesafe", "jev-1.13.0"}, {"jev-1.14", "typesafe", "jev-1.14.0"},
		{"jev-1.14.0", "typesafe", "jev-1.14.0"}, {"jev-latest", "typesafe", "jev-latest"},
		{"jev-1.14", "openrouter", "typesafe/jev-1.14"}, {"typesafe/jev-1.14", "openrouter", "typesafe/jev-1.14"},
		{"clef", "cloudflare_workers_ai", "clef"},
	} {
		if got := DecisionUpstreamModel(tc.model, tc.dialect); got != tc.want {
			t.Errorf("%s/%s = %s, want %s", tc.model, tc.dialect, got, tc.want)
		}
	}
}

func TestNewJevVersionsAvoidUnversionedCloudflareTransport(t *testing.T) {
	for _, name := range []string{"jev-1.14", "jev-1.14.0", "jev-latest"} {
		if DecisionModelSupportsDialect(name, "cloudflare_gateway") {
			t.Fatalf("unversioned transport selected for %s", name)
		}
		for _, dialect := range []string{"typesafe", "openrouter"} {
			if !DecisionModelSupportsDialect(name, dialect) {
				t.Fatalf("missing %s/%s", name, dialect)
			}
		}
	}
}
