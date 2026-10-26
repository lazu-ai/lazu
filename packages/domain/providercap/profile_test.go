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
	// a Gemini client's text generation reaches OpenAI channels planned, past
	// this guard; nothing unplanned in the Gemini format has an OpenAI path
	if ok, _ := IsSupported(constant.APITypeOpenAI, types.RelayFormatGemini); ok {
		t.Fatalf("openai should be blocked for unplanned gemini requests")
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
	// the gap is a wire fact: a sparse row records it too
	plain := BuildSnapshot(SnapshotInput{APIType: constant.APITypeOpenAI, Dialect: DialectDeepSeek})
	if !plain.EndpointGaps[textgen.FeatureEndpointChat].Has(textgen.FeatureJSONSchema) {
		t.Fatalf("the gap holds without the catalog flag: %v", plain.EndpointGaps)
	}
	if other := BuildSnapshot(SnapshotInput{APIType: constant.APITypeOpenAI, Dialect: DialectOpenAI}); other.Revision == plain.Revision || other.EndpointGaps != nil {
		t.Fatal("gaps are part of the revision, and OpenAI has none")
	}
}

// Anthropic's Messages API takes a schema but no bare json_object. Messages
// is the dialect's only endpoint, so the gap leaves json_object unsupported
// on the channel, where the registered stand-in applies; json_schema stays
// carried. The gap is a wire fact and holds for a sparse catalog row.
func TestAnthropicMessagesGapIsTheBareJSONObjectOnly(t *testing.T) {
	snapshot := BuildSnapshot(SnapshotInput{APIType: constant.APITypeAnthropic, Model: model.ModelCapabilities{SupportsResponseSchema: true}})
	gap := snapshot.EndpointGaps[textgen.FeatureEndpointMessages]
	if !gap.Has(textgen.FeatureJSONOutput) || gap.Has(textgen.FeatureJSONSchema) {
		t.Fatalf("gap: %v", gap.Names())
	}
	if snapshot.FeaturesOn(textgen.FeatureEndpointMessages).Has(textgen.FeatureJSONOutput) || !snapshot.FeaturesOn(textgen.FeatureEndpointMessages).Has(textgen.FeatureJSONSchema) {
		t.Fatal("Messages carries json_schema and not json_object")
	}
	if snapshot.UnsupportedEverywhere(textgen.FeatureSet(1)<<textgen.FeatureJSONOutput) == 0 || snapshot.UnsupportedEverywhere(textgen.FeatureSet(1)<<textgen.FeatureJSONSchema) != 0 {
		t.Fatal("json_object is unsupported everywhere on an Anthropic channel; json_schema is not")
	}
	plain := BuildSnapshot(SnapshotInput{APIType: constant.APITypeAnthropic})
	if !plain.EndpointGaps[textgen.FeatureEndpointMessages].Has(textgen.FeatureJSONOutput) {
		t.Fatalf("the gap holds without the catalog flag: %v", plain.EndpointGaps)
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

// A strict function tool raises the shared strict marker; DeepSeek Chat's gap
// is the response-format schema alone, so such a request stays native.
func TestDeepSeekChatGapIsTheResponseSchemaOnly(t *testing.T) {
	snapshot := BuildSnapshot(SnapshotInput{APIType: constant.APITypeOpenAI, Dialect: DialectDeepSeek, Model: model.ModelCapabilities{SupportsResponseSchema: true}})
	gap := snapshot.EndpointGaps[textgen.FeatureEndpointChat]
	if !gap.Has(textgen.FeatureJSONSchema) || gap.Has(textgen.FeatureStrictSchema) {
		t.Fatalf("gap: %v", gap.Names())
	}
}

// A sparse catalog row leaves json_schema unknown; DeepSeek Chat still
// rejects it, so the gap stands and the planner bridges to Responses.
func TestDeepSeekChatGapHoldsForASparseCatalogRow(t *testing.T) {
	snapshot := BuildSnapshot(SnapshotInput{APIType: constant.APITypeOpenAI, Dialect: DialectDeepSeek})
	semantic := &textgen.Request{Output: textgen.OutputConstraint{Type: "json_schema", Schema: []byte(`{"type":"object"}`)}}
	plan, err := textgen.PlanAttempt(textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, snapshot, semantic, 1, "deepseek-chat")
	if err != nil || plan.UpstreamProtocol.Family != textgen.ProtocolOpenAIResponses {
		t.Fatalf("a sparse row must still bridge json_schema to Responses: %+v %v", plan, err)
	}
	if !snapshot.EndpointGaps[textgen.FeatureEndpointChat].Has(textgen.FeatureJSONSchema) {
		t.Fatalf("gaps: %+v", snapshot.EndpointGaps)
	}
}
