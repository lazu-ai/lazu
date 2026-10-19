package runtime

import (
	"encoding/json"
	"errors"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"gorm.io/gorm"
)

func TestSingleUserTextRequiresResponsesOnlyAndChecksOverrides(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:runtime_single_user_text?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	})
	if err := db.AutoMigrate(&model.Channel{}, &model.Ability{}); err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	parsed := &textgen.ParsedRequest{
		Client: textgen.Protocol{Family: textgen.ProtocolOpenAIChat},
		Semantic: &textgen.Request{Model: textgen.ModelRef{Canonical: "fixture"}, Conversation: []textgen.InputItem{{
			Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "hello"}},
		}}},
	}
	for _, settings := range []string{
		`{"responses_single_user_text":true,"protocol_dialect":"openai_chat_compatible"}`,
		`{"responses_single_user_text":true,"protocol_dialect":"openai"}`,
	} {
		channel := &model.Channel{Type: constant.ChannelTypeOpenAI, OtherSettings: settings}
		if _, err := PrepareAttempt(parsed, channel, "fixture"); err == nil {
			t.Fatal("misconfigured single-user-text channel accepted")
		}
	}
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, OtherSettings: `{"responses_single_user_text":true,"protocol_dialect":"openai","capabilities":{"supports_chat_completions":false,"supports_responses":true}}`}
	patch := `{"store":true}`
	channel.ParamOverride = &patch
	if _, err := PrepareAttempt(parsed, channel, "fixture"); err == nil {
		t.Fatal("override bypassed stateless restriction")
	}
	channel.ParamOverride = nil
	before, _ := json.Marshal(parsed.Semantic)
	if attempt, err := PrepareAttempt(parsed, channel, "fixture"); err != nil || attempt.Plan.UpstreamProtocol.Family != textgen.ProtocolOpenAIResponses {
		t.Fatalf("valid route failed: %v", err)
	}
	after, _ := json.Marshal(parsed.Semantic)
	if string(before) != string(after) {
		t.Fatal("planning changed caller input")
	}
}

func TestApplySemanticOverridesPreservesOriginal(t *testing.T) {
	original := &textgen.Request{Sampling: textgen.SamplingOptions{Temperature: textgen.Some(1.0)}}
	clone := original.Clone()
	unsupported, err := applySemanticOverrides(clone, map[string]interface{}{"temperature": float64(0), "store": false})
	if err != nil || unsupported != "" {
		t.Fatalf("applySemanticOverrides() = unsupported %q, err %v", unsupported, err)
	}
	if clone.Sampling.Temperature.Value != 0 || !clone.Sampling.Temperature.Set || !clone.Lifecycle.Store.Set || clone.Lifecycle.Store.Value {
		t.Fatalf("semantic override was not applied with presence: %#v", clone)
	}
	if original.Sampling.Temperature.Value != 1 || original.Lifecycle.Store.Set {
		t.Fatalf("original request was mutated: %#v", original)
	}
}

func TestDescriptorFromPricingFallsBackToNormalizedModelName(t *testing.T) {
	pricing := []model.Pricing{{
		ModelName: "gpt-4o-gizmo-*",
		Family:    "gpt-4o",
		Capabilities: model.ModelCapabilities{
			SupportsFunctionCalling: true,
			SupportsVision:          true,
		},
		ContextLength:   128000,
		MaxOutputTokens: 4096,
	}}
	descriptor := descriptorFromPricing(pricing, "gpt-4o-gizmo-custom")
	if descriptor.Family != "gpt-4o" || !descriptor.Capabilities.SupportsFunctionCalling || !descriptor.Capabilities.SupportsVision || descriptor.ContextWindow != 128000 || descriptor.MaxOutputTokens != 4096 {
		t.Fatalf("normalized descriptor lookup lost model metadata: %+v", descriptor)
	}
}

func TestDescriptorFromPricingInheritsDeclaredFamilyProfile(t *testing.T) {
	pricing := []model.Pricing{
		{
			ModelName: "gpt-5",
			Family:    "gpt-5",
			Capabilities: model.ModelCapabilities{
				SupportsFunctionCalling: true,
				SupportsReasoning:       true,
			},
			ContextLength:   200000,
			MaxOutputTokens: 32000,
		},
		{
			ModelName: "gpt-5-vision",
			Family:    "gpt-5",
			Capabilities: model.ModelCapabilities{
				SupportsVision: true,
			},
			MaxOutputTokens: 16000,
		},
	}
	descriptor := descriptorFromPricing(pricing, "gpt-5-vision")
	if !descriptor.Capabilities.SupportsFunctionCalling || !descriptor.Capabilities.SupportsReasoning || !descriptor.Capabilities.SupportsVision {
		t.Fatalf("family and model capabilities were not composed: %+v", descriptor.Capabilities)
	}
	if descriptor.ContextWindow != 200000 || descriptor.MaxOutputTokens != 16000 {
		t.Fatalf("model constraints did not override family defaults: %+v", descriptor)
	}
}

func TestApplySemanticOverridesContinuesAfterUnknownKey(t *testing.T) {
	request := &textgen.Request{}
	unsupported, err := applySemanticOverrides(request, map[string]interface{}{
		"aaa_unknown":      true,
		"reasoning_effort": "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unsupported != "aaa_unknown" || request.Reasoning.Effort != "high" {
		t.Fatalf("recognized override was skipped: unsupported=%q request=%+v", unsupported, request)
	}
}

func TestApplySemanticOverrideOperationsContinueAfterUnsupportedOperation(t *testing.T) {
	request := &textgen.Request{}
	unsupported, err := applySemanticOverrides(request, map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{"mode": "set", "path": "provider.extension", "value": true},
			map[string]interface{}{"mode": "set", "path": "reasoning_effort", "value": "medium"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if unsupported != "operations" || request.Reasoning.Effort != "medium" {
		t.Fatalf("later recognized operation was skipped: unsupported=%q request=%+v", unsupported, request)
	}
}

func TestSnapshotForCustomAndXAIProtocolBoundaries(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:runtime_protocol?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	}()
	if err := db.AutoMigrate(&model.Channel{}, &model.Ability{}); err != nil {
		t.Fatal(err)
	}

	request := &textgen.Request{Model: textgen.ModelRef{Canonical: "test-model"}, Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "hello"}}}}}

	custom := &model.Channel{Id: 1, Type: constant.ChannelTypeCustom}
	customPlan, err := PlanForChannel(&textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, Semantic: request}, custom, "test-model")
	if err != nil || customPlan == nil || !customPlan.Native {
		t.Fatalf("custom OpenAI-compatible channel should support native Chat: plan=%+v err=%v", customPlan, err)
	}
	customOpenAI := &model.Channel{Id: 3, Type: constant.ChannelTypeCustom, OtherSettings: `{"protocol_dialect":"openai"}`}
	customResponsesPlan, err := PlanForChannel(&textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}, Semantic: request}, customOpenAI, "test-model")
	if err != nil || customResponsesPlan == nil || customResponsesPlan.Native || customResponsesPlan.UpstreamProtocol.Family != textgen.ProtocolOpenAIChat {
		t.Fatalf("fixed Custom URL must default to a Chat bridge: plan=%+v err=%v", customResponsesPlan, err)
	}
	customResponses := &model.Channel{Id: 4, Type: constant.ChannelTypeCustom, OtherSettings: `{"protocol_dialect":"openai","capabilities":{"supports_responses":true}}`}
	customNativeResponses, err := PlanForChannel(&textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}, Semantic: request}, customResponses, "test-model")
	if err != nil || customNativeResponses == nil || !customNativeResponses.Native {
		t.Fatalf("explicit Responses-only Custom URL should be native: plan=%+v err=%v", customNativeResponses, err)
	}
	customAmbiguous := &model.Channel{Id: 5, Type: constant.ChannelTypeCustom, OtherSettings: `{"protocol_dialect":"openai","capabilities":{"supports_chat_completions":true,"supports_responses":true}}`}
	customAmbiguousPlan, err := PlanForChannel(&textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}, Semantic: request}, customAmbiguous, "test-model")
	if err != nil || customAmbiguousPlan == nil || customAmbiguousPlan.Native || customAmbiguousPlan.UpstreamProtocol.Family != textgen.ProtocolOpenAIChat {
		t.Fatalf("ambiguous fixed Custom URL must remain Chat-only: plan=%+v err=%v", customAmbiguousPlan, err)
	}

	xai := &model.Channel{Id: 2, Type: constant.ChannelTypeXai}
	xaiChat, err := PlanForChannel(&textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, Semantic: request}, xai, "test-model")
	if err != nil || xaiChat == nil || !xaiChat.Native {
		t.Fatalf("xAI native Chat should remain available: plan=%+v err=%v", xaiChat, err)
	}
	if _, err := PlanForChannel(&textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}, Semantic: request}, xai, "test-model"); err == nil {
		t.Fatal("xAI Responses-to-Chat bridge must fail closed until its adaptor is plan-aware")
	}
}

func TestPrepareAttemptReplansFromOriginalRequestAcrossDialects(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:runtime_protocol_retry?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	}()
	if err := db.AutoMigrate(&model.Channel{}, &model.Ability{}); err != nil {
		t.Fatal(err)
	}

	original := &textgen.Request{
		Model:        textgen.ModelRef{Canonical: "test-model"},
		Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "hello"}}}},
		Sampling:     textgen.SamplingOptions{Temperature: textgen.Some(0.5)},
	}
	parsed := &textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}, Semantic: original}
	override := `{"temperature":0}`
	chatOnly := &model.Channel{Id: 11, Type: constant.ChannelTypeOpenAI, OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`, ParamOverride: &override}
	nativeResponses := &model.Channel{Id: 12, Type: constant.ChannelTypeOpenAI, OtherSettings: `{"protocol_dialect":"openai"}`}

	first, err := PrepareAttempt(parsed, chatOnly, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareAttempt(parsed, nativeResponses, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if first.Plan.Native || first.Plan.UpstreamProtocol.Family != textgen.ProtocolOpenAIChat {
		t.Fatalf("first attempt should bridge to Chat: %+v", first.Plan)
	}
	if !second.Plan.Native || second.Plan.UpstreamProtocol.Family != textgen.ProtocolOpenAIResponses {
		t.Fatalf("second attempt should independently use native Responses: %+v", second.Plan)
	}
	if first.Request == original || second.Request != original || first.Request == second.Request {
		t.Fatal("only the overridden attempt may clone; the retry must reuse immutable input")
	}
	if !first.Request.Sampling.Temperature.Set || first.Request.Sampling.Temperature.Value != 0 {
		t.Fatalf("first attempt override missing: %+v", first.Request.Sampling.Temperature)
	}
	if !second.Request.Sampling.Temperature.Set || second.Request.Sampling.Temperature.Value != 0.5 || original.Sampling.Temperature.Value != 0.5 {
		t.Fatalf("first attempt polluted the retry or original request: second=%+v original=%+v", second.Request.Sampling.Temperature, original.Sampling.Temperature)
	}
}

func TestValidateBridgeOverrideReturnsStableUnsupportedFeature(t *testing.T) {
	err := validateBridgeOverride(false, "custom_provider_field")
	var unsupported *textgen.UnsupportedFeatureError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected UnsupportedFeatureError, got %v", err)
	}
	if unsupported.Feature != textgen.FeatureUnknownField || unsupported.Path != "param_override.custom_provider_field" {
		t.Fatalf("unexpected unsupported override error: %+v", unsupported)
	}
	if err := validateBridgeOverride(true, "custom_provider_field"); err != nil {
		t.Fatalf("native override should remain allowed: %v", err)
	}
}

func TestArbitraryPatchIsRejectedOnlyForBridge(t *testing.T) {
	request := &textgen.Request{}
	unsupported, err := applySemanticOverrides(request, map[string]interface{}{"custom_provider_field": true})
	if err != nil || unsupported != "custom_provider_field" {
		t.Fatalf("unexpected arbitrary patch classification: %q, %v", unsupported, err)
	}
	if err := validateBridgeOverride(false, unsupported); err == nil {
		t.Fatal("expected arbitrary patch to be rejected for bridge")
	}
	if err := validateBridgeOverride(true, unsupported); err != nil {
		t.Fatalf("native same-protocol patch should remain compatible: %v", err)
	}
}

// A Chat request with tools to gpt-6 on an official surface goes out with
// reasoning off unless the client chose an effort; elsewhere it is untouched.
func TestPrepareAttemptTurnsReasoningOffForGPT6ChatTools(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:runtime_chat_tools_reasoning?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	})
	if err := db.AutoMigrate(&model.Channel{}, &model.Ability{}); err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	parsed := func(effort string, mode string) *textgen.ParsedRequest {
		return &textgen.ParsedRequest{
			Client: textgen.Protocol{Family: textgen.ProtocolOpenAIChat},
			Semantic: &textgen.Request{
				Model: textgen.ModelRef{Canonical: "gpt-6-sol"},
				Conversation: []textgen.InputItem{{
					Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "weather?"}},
				}},
				Tools:      []textgen.ToolDefinition{{Type: "function", Name: "get_weather"}},
				ToolPolicy: textgen.ToolPolicy{Mode: mode},
				Reasoning:  textgen.ReasoningOptions{Effort: effort},
			},
		}
	}
	official := &model.Channel{Type: constant.ChannelTypeOpenAI, OtherSettings: `{"protocol_dialect":"openai"}`}
	responsesOnly := &model.Channel{Type: constant.ChannelTypeOpenAI, OtherSettings: `{"protocol_dialect":"openai","capabilities":{"supports_chat_completions":false,"supports_responses":true}}`}
	reseller := &model.Channel{Type: constant.ChannelTypeOpenAI, OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`}
	for _, tc := range []struct {
		name    string
		channel *model.Channel
		model   string
		effort  string
		mode    string
		want    string
	}{
		{"native", official, "gpt-6-sol", "", "", "none"},
		{"bridge", responsesOnly, "gpt-6-sol", "", "", "none"},
		{"astra has no none effort", responsesOnly, "gpt-6-astra", "", "", ""},
		{"client effort kept", official, "gpt-6-sol", "high", "", "high"},
		{"tool_choice none", official, "gpt-6-sol", "", "none", ""},
		{"reseller untouched", reseller, "gpt-6-sol", "", "", ""},
		{"other model untouched", official, "gpt-5.5", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := parsed(tc.effort, tc.mode)
			attempt, err := PrepareAttempt(request, tc.channel, tc.model)
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			if got := attempt.Request.Reasoning.Effort; got != tc.want {
				t.Fatalf("effort = %q, want %q", got, tc.want)
			}
			if request.Semantic.Reasoning.Effort != tc.effort {
				t.Fatal("planning changed caller input")
			}
		})
	}
	request := parsed("", "")
	first, err := PrepareAttempt(request, official, "gpt-6-sol")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := PrepareAttempt(request, reseller, "gpt-6-sol")
	if err != nil {
		t.Fatal(err)
	}
	if first.Request == request.Semantic || retry.Request != request.Semantic || retry.Request.Reasoning.Effort != "" {
		t.Fatal("reasoning default polluted retry input or disabled ordinary request reuse")
	}
}
