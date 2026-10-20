package runtime

import (
	"encoding/json"
	"errors"
	"strings"
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

// A channel that denies json_schema gets the registered stand-in: the plan
// carries the degradation warning and the caller's request is untouched.
func TestPrepareAttemptDegradesDeniedJSONSchema(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:runtime_degrade?mode=memory&cache=shared"), &gorm.Config{})
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
		Semantic: &textgen.Request{
			Model:        textgen.ModelRef{Canonical: "fixture"},
			Instructions: []textgen.Instruction{{Role: "system", Text: "Name the session."}},
			Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "hello"}}}},
			Output:       textgen.OutputConstraint{Type: "json_schema", Name: "title", Schema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, OtherSettings: `{"protocol_dialect":"openai_chat_compatible","capabilities":{"supports_json_schema":false}}`}
	before, _ := json.Marshal(parsed.Semantic)
	attempt, err := PrepareAttempt(parsed, channel, "fixture")
	if err != nil {
		t.Fatalf("degraded route failed: %v", err)
	}
	if attempt.Request == parsed.Semantic || attempt.Request.Output.Type != "" || len(attempt.Request.Instructions) != 1 {
		t.Fatalf("expected a degraded clone, got %+v", attempt.Request)
	}
	// the catalog knows nothing of "fixture", so json_object is not known to
	// be carried either: the schema goes as an instruction alone
	if len(attempt.Plan.Warnings) != 1 || attempt.Plan.Warnings[0].Message != "degraded:json_schema_to_instruction" {
		t.Fatalf("expected the degradation warning on the plan, got %+v", attempt.Plan.Warnings)
	}
	after, _ := json.Marshal(parsed.Semantic)
	if string(before) != string(after) {
		t.Fatal("degradation changed caller input")
	}
	// the instruction the stand-in adds counts toward the context window: a
	// request just under the limit before it is over the limit after
	nearLimit := *parsed
	nearLimit.InputTokens = 100
	tight := &model.Channel{Type: constant.ChannelTypeOpenAI, OtherSettings: `{"protocol_dialect":"openai_chat_compatible","capabilities":{"supports_json_schema":false,"context_window":110}}`}
	if _, err := PrepareAttempt(&nearLimit, tight, "fixture"); err == nil {
		t.Fatal("the degraded prompt is over the context window and must be refused")
	}
	nearLimit.Semantic = nearLimit.Semantic.Clone()
	nearLimit.Semantic.Output = textgen.OutputConstraint{}
	if _, err := PrepareAttempt(&nearLimit, tight, "fixture"); err != nil {
		t.Fatalf("without the degradation the request fits: %v", err)
	}
	// a nested field the IR does not model would be lost by encoding the
	// degraded request from the IR, so there is no degradation and the
	// denial stands
	nested := *parsed
	nested.Semantic = parsed.Semantic.Clone()
	nested.Semantic.Raw.UnknownFields = map[string]json.RawMessage{"messages[0].content[0].vendor": json.RawMessage(`1`)}
	if _, err := PrepareAttempt(&nested, channel, "fixture"); err == nil {
		t.Fatal("a request the IR cannot encode losslessly must not be degraded")
	}
	// without the overlay the same request is left for the provider to judge
	open := &model.Channel{Type: constant.ChannelTypeOpenAI, OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`}
	attempt, err = PrepareAttempt(parsed, open, "fixture")
	if err != nil || attempt.Request != parsed.Semantic || len(attempt.Plan.Warnings) != 0 {
		t.Fatalf("unknown support must not degrade: %+v %v", attempt, err)
	}
}

// A Claude client's thinking reaches another vendor's model as
// reasoning_effort only when the catalog says the model reasons; a model it
// does not describe gets no effort, as the legacy conversion sent none.
func TestPrepareAttemptLeavesClaudeThinkingOutForAModelNotKnownToReason(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:runtime_claude_thinking?mode=memory&cache=shared"), &gorm.Config{})
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
	parsed, err := Parse(textgen.ProtocolAnthropic, []byte(`{"model":"fixture","max_tokens":100,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	chat := &model.Channel{Type: constant.ChannelTypeOpenAI, OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`}
	attempt, err := PrepareAttempt(parsed, chat, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Plan.Native || attempt.Plan.UpstreamProtocol.Family != textgen.ProtocolOpenAIChat {
		t.Fatalf("expected a Chat bridge, got %+v", attempt.Plan)
	}
	if attempt.Request.Reasoning != (textgen.ReasoningOptions{}) || parsed.Semantic.Reasoning.Effort != "high" {
		t.Fatalf("thinking must be left out on a clone: attempt %+v, parsed %+v", attempt.Request.Reasoning, parsed.Semantic.Reasoning)
	}
	anthropic := &model.Channel{Type: constant.ChannelTypeAnthropic}
	native, err := PrepareAttempt(parsed, anthropic, "fixture")
	if err != nil || !native.Plan.Native || native.Request.Reasoning.Effort != "high" {
		t.Fatalf("a native attempt keeps the client's thinking: %+v %v", native, err)
	}

	// a tool result's image, for a model not known to see images, becomes a
	// note and a warning; the client's own request is untouched
	withImage, err := Parse(textgen.ProtocolAnthropic, []byte(`{"model":"fixture","max_tokens":100,"messages":[{"role":"user","content":"look"},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"a.png"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	blind, err := PrepareAttempt(withImage, chat, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	result := blind.Request.Conversation[2]
	if len(result.Content) != 0 || !strings.Contains(result.Output, "omitted") || len(withImage.Semantic.Conversation[2].Content) != 1 {
		t.Fatalf("image not omitted on a clone: %+v", result)
	}
	warned := false
	for _, warning := range blind.Plan.Warnings {
		warned = warned || warning.Message == "omitted:tool_result_image"
	}
	if !warned || blind.Plan.Degraded() {
		t.Fatalf("warnings: %+v", blind.Plan.Warnings)
	}

	// a Claude client is never degraded: a denied schema is refused
	schema, err := Parse(textgen.ProtocolAnthropic, []byte(`{"model":"fixture","max_tokens":100,"output_config":{"format":{"type":"json_schema","schema":{"type":"object"}}},"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	denying := &model.Channel{Type: constant.ChannelTypeOpenAI, OtherSettings: `{"protocol_dialect":"openai_chat_compatible","capabilities":{"supports_json_schema":false}}`}
	if _, err := PrepareAttempt(schema, denying, "fixture"); err == nil {
		t.Fatal("a Claude client's denied schema must be refused, not degraded")
	}
}

// Across families the channel's override is its upstream wire's own: a
// field the IR does not model plans the bridge (the relay applies it to the
// encoded body, as the legacy conversions did) instead of refusing it, and
// the IR is left as the client sent it. Between Chat and Responses an
// unmodeled override still refuses the bridge.
func TestPrepareAttemptLeavesCrossFamilyOverridesToTheWire(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:runtime_cross_family_override?mode=memory&cache=shared"), &gorm.Config{})
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
	override := `{"operations":[{"path":"metadata.user_id","mode":"set","value":"operator"},{"path":"temperature","mode":"set","value":0.2}]}`

	chat, err := Parse(textgen.ProtocolOpenAIChat, []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	anthropic := &model.Channel{Type: constant.ChannelTypeAnthropic, ParamOverride: &override}
	attempt, err := PrepareAttempt(chat, anthropic, "claude-haiku-5-5")
	if err != nil {
		t.Fatalf("a Chat client on an Anthropic channel with an override must plan: %v", err)
	}
	if attempt.Plan.UpstreamProtocol.Family != textgen.ProtocolAnthropic || attempt.Request != chat.Semantic {
		t.Fatalf("expected a Messages bridge on the client's own IR: %+v", attempt.Plan)
	}

	claude, err := Parse(textgen.ProtocolAnthropic, []byte(`{"model":"fixture","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	openai := &model.Channel{Type: constant.ChannelTypeOpenAI, OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`, ParamOverride: &override}
	if _, err := PrepareAttempt(claude, openai, "fixture"); err != nil {
		t.Fatalf("a Claude client on an OpenAI channel with an override must plan: %v", err)
	}

	responses, err := Parse(textgen.ProtocolOpenAIResponses, []byte(`{"model":"fixture","input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareAttempt(responses, openai, "fixture"); err == nil {
		t.Fatal("between Chat and Responses an unmodeled override still refuses the bridge")
	}
}
