package middleware

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
)

func decisionScopeContext(t *testing.T, path string, limit string, member string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, http.NoBody)
	r, err := dto.ParseDecisionRequest([]byte(`{"model":"gpt-6-luna","input":"The screen is broken.","questions":[{"type":"predicate","instructions":"Is the screen broken?"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	c.Set("decision_request", r)
	common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{limit: true})
	if member != "" {
		common.SetContextKey(c, constant.ContextKeyMemberModelLimit, map[string]bool{member: true})
	}
	return c
}

func TestOfficialDecisionModelScopeIsEndpointBound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, path, key, member, model string
		want                           bool
	}{
		{"official_key", "/v1/decisions", "gpt-6-luna", "gpt-6-luna", dto.OpenAIDecisionModel, true},
		{"decision_only_key", "/v1/decisions", dto.OpenAIDecisionModel, dto.OpenAIDecisionModel, dto.OpenAIDecisionModel, true},
		{"unrelated_key", "/v1/decisions", "jev-1.13", "", dto.OpenAIDecisionModel, false},
		{"member_restriction", "/v1/decisions", "gpt-6-luna", "jev-1.13", dto.OpenAIDecisionModel, false},
		{"decision_key_cannot_chat", "/v1/chat/completions", dto.OpenAIDecisionModel, "", "gpt-6-luna", false},
		{"chat_does_not_remap", "/v1/chat/completions", "gpt-6-luna", "", dto.OpenAIDecisionModel, false},
		{"responses_does_not_remap", "/v1/responses", "gpt-6-luna", "", dto.OpenAIDecisionModel, false},
		{"other_model_does_not_remap", "/v1/decisions", "gpt-6-luna", "", "gpt-6-sol", false},
		{"member_decision_only_cannot_chat", "/v1/chat/completions", "gpt-6-luna", dto.OpenAIDecisionModel, "gpt-6-luna", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := decisionScopeContext(t, tc.path, tc.key, tc.member)
			if got := authorizeTokenModelAndVendor(c, tc.model); got != tc.want {
				t.Fatalf("authorization=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestOfficialDecisionScopeRequiresValidatedNativeRequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*gin.Context)
	}{
		{"missing_dto", func(c *gin.Context) { c.Set("decision_request", nil) }},
		{"legacy_alias", func(c *gin.Context) {
			c.Set("decision_request", &dto.DecisionRequest{Model: dto.OpenAIDecisionModel})
		}},
		{"mismatched_internal_model", func(c *gin.Context) {
			r := c.MustGet("decision_request").(*dto.DecisionRequest)
			r.Model = "jev-1.13"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := decisionScopeContext(t, "/v1/decisions", "gpt-6-luna", "")
			tc.change(c)
			if authorizeTokenModelAndVendor(c, dto.OpenAIDecisionModel) {
				t.Fatal("unvalidated or legacy DTO inherited native model grant")
			}
		})
	}
}

func TestOfficialDecisionVendorRestrictionsRemainEffective(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()
	for _, vendor := range []model.Vendor{{Id: 1, PublicID: "vendor-decision-openai", Name: "OpenAI"}, {Id: 2, PublicID: "vendor-decision-other", Name: "Other"}} {
		if err := model.CoreDB().Create(&vendor).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := model.CoreDB().Create(&model.Channel{Id: 101, Type: constant.ChannelTypeOpenAI, Key: "fixture-only", Status: common.ChannelStatusEnabled, Name: "decision-scope-fixture", Models: dto.OpenAIDecisionModel + "," + dto.OpenAIDecisionWireModel, Group: model.ChannelLaneDirect}).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{dto.OpenAIDecisionModel, dto.OpenAIDecisionWireModel} {
		if err := model.CoreDB().Create(&model.Model{ModelName: name, VendorID: 1, Status: 1, NameRule: model.NameRuleExact, LifecycleStatus: model.ModelLifecycleActive}).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.CoreDB().Create(&model.Ability{Group: model.ChannelLaneDirect, Model: name, ChannelId: 101, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	model.RefreshPricing()
	if id, _ := model.GetModelVendorMeta(dto.OpenAIDecisionModel); id != 1 {
		t.Fatalf("fixture must register Decisions vendor, got %d", id)
	}
	for _, tc := range []struct {
		name          string
		key           string
		scopeVendors  map[int]struct{}
		deniedVendors model.IntSlice
		want          bool
	}{
		{"official_grant_cannot_override_vendor_deny", "gpt-6-luna", nil, model.IntSlice{1}, false},
		{"alias_grant_cannot_override_vendor_deny", dto.OpenAIDecisionModel, nil, model.IntSlice{1}, false},
		{"unrelated_vendor_deny_does_not_block", "gpt-6-luna", nil, model.IntSlice{2}, true},
		{"openai_vendor_scope_still_allows", "unrelated", map[int]struct{}{1: {}}, nil, true},
		{"other_vendor_scope_does_not_grant", "unrelated", map[int]struct{}{2: {}}, nil, false},
		{"vendor_scope_cannot_override_vendor_deny", "unrelated", map[int]struct{}{1: {}}, model.IntSlice{1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := decisionScopeContext(t, "/v1/decisions", tc.key, "")
			common.SetContextKey(c, constant.ContextKeyTokenModelScopeVendors, tc.scopeVendors)
			common.SetContextKey(c, constant.ContextKeyTokenVendorLimitEnabled, true)
			common.SetContextKey(c, constant.ContextKeyTokenVendorLimits, tc.deniedVendors)
			if got := authorizeTokenModelAndVendor(c, dto.OpenAIDecisionModel); got != tc.want {
				t.Fatalf("vendor authorization=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestOfficialDecisionEphemeralGrantPreservesOtherRestrictions(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()
	for _, tc := range []struct {
		name, path, grantedModel, requestedModel, provider, session, scope string
		want                                                               bool
	}{
		{"official_grant", "/v1/decisions", "gpt-6-luna", dto.OpenAIDecisionModel, "openai", "session-fixture", model.EphemeralCredentialScopeRelay, true},
		{"alias_grant", "/v1/decisions", dto.OpenAIDecisionModel, dto.OpenAIDecisionModel, "openai", "session-fixture", model.EphemeralCredentialScopeRelay, true},
		{"other_model", "/v1/decisions", "jev-1.13", dto.OpenAIDecisionModel, "openai", "session-fixture", model.EphemeralCredentialScopeRelay, false},
		{"provider_binding", "/v1/decisions", "gpt-6-luna", dto.OpenAIDecisionModel, "openrouter", "session-fixture", model.EphemeralCredentialScopeRelay, false},
		{"session_binding", "/v1/decisions", "gpt-6-luna", dto.OpenAIDecisionModel, "openai", "other-session", model.EphemeralCredentialScopeRelay, false},
		{"scope_binding", "/v1/decisions", "gpt-6-luna", dto.OpenAIDecisionModel, "openai", "session-fixture", "management", false},
		{"chat_cannot_remap", "/v1/chat/completions", "gpt-6-luna", dto.OpenAIDecisionModel, "openai", "session-fixture", model.EphemeralCredentialScopeRelay, false},
		{"alias_cannot_chat", "/v1/chat/completions", dto.OpenAIDecisionModel, "gpt-6-luna", "openai", "session-fixture", model.EphemeralCredentialScopeRelay, false},
		{"other_requested_model", "/v1/decisions", "gpt-6-luna", "gpt-6-sol", "openai", "session-fixture", model.EphemeralCredentialScopeRelay, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := decisionScopeContext(t, tc.path, "gpt-6-luna", "")
			// Spare capacity catches an accidental append into the credential's
			// original backing array, not just changes to its slice length.
			models := make(model.StringSlice, 4)
			models[0], models[1] = tc.grantedModel, "sentinel-preserved"
			credential := &model.EphemeralCredential{Models: models[:1], Providers: model.StringSlice{"openai"}, SessionID: "session-fixture", Scope: tc.scope}
			before := *credential
			before.Models = append(model.StringSlice(nil), credential.Models...)
			before.Providers = append(model.StringSlice(nil), credential.Providers...)
			common.SetContextKey(c, constant.ContextKeyEphemeralCredential, credential)
			common.SetContextKey(c, constant.ContextKeySessionID, tc.session)
			if got := authorizeEphemeralCredential(c, tc.requestedModel, tc.provider); got != tc.want {
				t.Fatalf("ephemeral authorization=%v want=%v", got, tc.want)
			}
			if !reflect.DeepEqual(*credential, before) || models[1] != "sentinel-preserved" {
				t.Fatal("endpoint grant mutated reusable credential scope")
			}
		})
	}
}

func TestOfficialDecisionRoutingReusesValidatedRequest(t *testing.T) {
	c := decisionScopeContext(t, "/v1/decisions", "gpt-6-luna", "")
	// The original body has already been consumed. Routing must use the DTO,
	// keeping billing on Decisions instead of the same-name chat model.
	r, selectChannel, err := getModelRequest(c)
	if err != nil || !selectChannel || r.Model != dto.OpenAIDecisionModel {
		t.Fatalf("decision routing model=%+v select=%v err=%v", r, selectChannel, err)
	}
}
