package providercap

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestGetProfile_ProtocolFamily(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		apiType int
		family  ProtocolFamily
	}{
		{
			name:    "openai",
			apiType: constant.APITypeOpenAI,
			family:  ProtocolFamilyOpenAI,
		},
		{
			name:    "anthropic",
			apiType: constant.APITypeAnthropic,
			family:  ProtocolFamilyClaude,
		},
		{
			name:    "gemini",
			apiType: constant.APITypeGemini,
			family:  ProtocolFamilyGemini,
		},
		{
			name:    "xai",
			apiType: constant.APITypeXai,
			family:  ProtocolFamilyCustom,
		},
		{
			name:    "unknown",
			apiType: -999,
			family:  ProtocolFamilyUnknown,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ProtocolFamilyOf(tt.apiType); got != tt.family {
				t.Fatalf("unexpected family for apiType %d, want=%s got=%s", tt.apiType, tt.family, got)
			}
		})
	}
}

func TestBuildSnapshotChannelOverlayCanDeclareResponsesOnly(t *testing.T) {
	no := false
	snapshot := BuildSnapshot(SnapshotInput{
		APIType: constant.APITypeOpenAI,
		Dialect: DialectOpenAI,
		Channel: &dto.ChannelCapabilities{SupportsChatCompletions: &no},
	})
	if snapshot.Features.Has(textgen.FeatureEndpointChat) {
		t.Fatal("explicit channel overlay should remove chat completions endpoint")
	}
	if !snapshot.Features.Has(textgen.FeatureEndpointResponses) {
		t.Fatal("responses endpoint should remain available from the OpenAI dialect")
	}
}

func TestBuildSnapshotDistinguishesCloudflareFromNativeOpenAI(t *testing.T) {
	modelCapabilities := model.ModelCapabilities{SupportsFunctionCalling: true, SupportsResponseSchema: true}
	cloudflare := BuildSnapshot(SnapshotInput{APIType: constant.APITypeOpenAI, Dialect: DialectCloudflareAIV1, ModelFamily: "kimi-k2", Model: modelCapabilities})
	if !cloudflare.Features.Has(textgen.FeatureEndpointChat) || cloudflare.Features.Has(textgen.FeatureEndpointResponses) {
		t.Fatalf("Cloudflare must be Chat-only: %v", cloudflare.Features.Names())
	}
	if !cloudflare.Features.Has(textgen.FeatureFunctionTools) || !cloudflare.Features.Has(textgen.FeatureJSONSchema) {
		t.Fatalf("model semantic capabilities were not composed: %v", cloudflare.Features.Names())
	}

	openai := BuildSnapshot(SnapshotInput{APIType: constant.APITypeOpenAI, Dialect: DialectOpenAI, ModelFamily: "gpt-5", Model: modelCapabilities})
	if !openai.Features.Has(textgen.FeatureEndpointResponses) {
		t.Fatalf("native OpenAI must expose Responses: %v", openai.Features.Names())
	}
	if !openai.Features.Has(textgen.FeatureDeveloperRole) || cloudflare.Features.Has(textgen.FeatureDeveloperRole) {
		t.Fatalf("developer role should be restricted to verified OpenAI dialects: openai=%v cloudflare=%v", openai.Features.Names(), cloudflare.Features.Names())
	}
	if openai.Revision == cloudflare.Revision {
		t.Fatal("dialect changes must produce a distinct capability revision")
	}
	for _, feature := range []textgen.FeatureID{textgen.FeatureStop, textgen.FeatureN, textgen.FeatureSeed, textgen.FeatureLogProbs, textgen.FeatureTopK} {
		if !cloudflare.Features.Has(feature) {
			t.Fatalf("native Chat sampling feature %s missing from baseline: %v", feature, cloudflare.Features.Names())
		}
	}
}

func TestNormalizeDialectRejectsProviderTypeMismatch(t *testing.T) {
	if got := NormalizeDialect(constant.APITypeAnthropic, DialectOpenAIChatCompatible); got != DialectAnthropic {
		t.Fatalf("Anthropic API type must use Anthropic dialect, got %q", got)
	}
	if got := NormalizeDialect(constant.APITypeGemini, DialectOpenAI); got != DialectGemini {
		t.Fatalf("Gemini API type must use Gemini dialect, got %q", got)
	}
	if got := NormalizeDialect(constant.APITypeOpenAI, DialectAnthropic); got != DialectOpenAIChatCompatible {
		t.Fatalf("OpenAI API type must reject Anthropic dialect, got %q", got)
	}
}

func TestGetProfile_KnownApiTypesDoNotUseUnknownFamily(t *testing.T) {
	t.Parallel()

	knownAPITypes := []int{
		constant.APITypeOpenAI,
		constant.APITypeAnthropic,
		constant.APITypeGemini,
		constant.APITypeXai,
	}

	for _, apiType := range knownAPITypes {
		if family := ProtocolFamilyOf(apiType); family == ProtocolFamilyUnknown {
			t.Fatalf("apiType %d should have explicit protocol family profile", apiType)
		}
	}
}

func TestIsSupported_ClaudeGuard(t *testing.T) {
	t.Parallel()

	supported, reason := IsSupported(constant.APITypeOpenAI, types.RelayFormatClaude)
	if !supported {
		t.Fatalf("openai should support claude compatibility conversion, reason=%s", reason)
	}

	supported, reason = IsSupported(constant.APITypeXai, types.RelayFormatClaude)
	if supported {
		t.Fatalf("xai should be blocked for claude route")
	}
	if reason == "" {
		t.Fatalf("expected non-empty block reason for baidu claude guard")
	}
}

func TestIsSupported_ResponsesAndRealtimeGuard(t *testing.T) {
	t.Parallel()

	if ok, _ := IsSupported(constant.APITypeOpenAI, types.RelayFormatOpenAIResponses); !ok {
		t.Fatalf("openai should support responses profile")
	}
	if ok, _ := IsSupported(constant.APITypeGemini, types.RelayFormatOpenAIResponses); ok {
		t.Fatalf("gemini should be blocked for responses profile")
	}
	if ok, _ := IsSupported(constant.APITypeOpenAI, types.RelayFormatOpenAIRealtime); !ok {
		t.Fatalf("openai should support realtime profile")
	}
	if ok, _ := IsSupported(constant.APITypeAnthropic, types.RelayFormatOpenAIRealtime); ok {
		t.Fatalf("anthropic should be blocked for realtime profile")
	}
}

func TestIsSupported_GeminiGuard(t *testing.T) {
	t.Parallel()

	if ok, _ := IsSupported(constant.APITypeGemini, types.RelayFormatGemini); !ok {
		t.Fatalf("gemini should support gemini profile")
	}
	if ok, _ := IsSupported(constant.APITypeOpenAI, types.RelayFormatGemini); !ok {
		t.Fatalf("openai should support gemini compatibility conversion")
	}
	if ok, _ := IsSupported(constant.APITypeAnthropic, types.RelayFormatGemini); ok {
		t.Fatalf("anthropic should be blocked for gemini profile")
	}
}

func TestBuildSnapshotDeepSeekCarriesJSONSchemaOnResponsesOnly(t *testing.T) {
	snapshot := BuildSnapshot(SnapshotInput{APIType: constant.APITypeOpenAI, Dialect: DialectDeepSeek, Model: model.ModelCapabilities{SupportsResponseSchema: true, SupportsFunctionCalling: true}})
	if !snapshot.Features.Has(textgen.FeatureEndpointChat) || !snapshot.Features.Has(textgen.FeatureEndpointResponses) {
		t.Fatalf("deepseek has both endpoints: %v", snapshot.Features.Names())
	}
	if !snapshot.Features.Has(textgen.FeatureJSONSchema) {
		t.Fatal("the model's schema support is kept on the snapshot")
	}
	if snapshot.FeaturesOn(textgen.FeatureEndpointChat).Has(textgen.FeatureJSONSchema) {
		t.Fatal("DeepSeek Chat does not carry json_schema")
	}
	if !snapshot.FeaturesOn(textgen.FeatureEndpointResponses).Has(textgen.FeatureJSONSchema) {
		t.Fatal("DeepSeek Responses carries json_schema")
	}
	if snapshot.UnsupportedEverywhere(textgen.FeatureSet(1)<<textgen.FeatureJSONSchema) != 0 {
		t.Fatal("a feature one endpoint carries is not unsupported")
	}
	// a model row without schema support has no gap to record
	plain := BuildSnapshot(SnapshotInput{APIType: constant.APITypeOpenAI, Dialect: DialectDeepSeek})
	if plain.EndpointGaps != nil {
		t.Fatalf("no gap without the feature: %v", plain.EndpointGaps)
	}
	if plain.Revision == snapshot.Revision {
		t.Fatal("gaps are part of the revision")
	}
}

func TestBuildSnapshotChannelCanDenyJSONSchema(t *testing.T) {
	no := false
	snapshot := BuildSnapshot(SnapshotInput{APIType: constant.APITypeOpenAI, Dialect: DialectOpenAIChatCompatible, Model: model.ModelCapabilities{SupportsResponseSchema: true}, Channel: &dto.ChannelCapabilities{SupportsJSONSchema: &no}})
	if snapshot.Features.Has(textgen.FeatureJSONSchema) || !snapshot.Denied.Has(textgen.FeatureJSONSchema) {
		t.Fatalf("explicit false must remove and deny json_schema: features=%v denied=%v", snapshot.Features.Names(), snapshot.Denied.Names())
	}
	if snapshot.Denied.Has(textgen.FeatureStrictSchema) {
		t.Fatal("the overlay says nothing about strict function tools")
	}
	if !snapshot.Features.Has(textgen.FeatureJSONOutput) {
		t.Fatal("json_object stays available")
	}
	yes := true
	kept := BuildSnapshot(SnapshotInput{APIType: constant.APITypeOpenAI, Dialect: DialectOpenAIChatCompatible, Model: model.ModelCapabilities{SupportsResponseSchema: true}, Channel: &dto.ChannelCapabilities{SupportsJSONSchema: &yes}})
	if !kept.Features.Has(textgen.FeatureJSONSchema) || kept.Denied != 0 {
		t.Fatal("true and nil change nothing")
	}
}
