package controller

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestSelectPreferredEndpointType_PreservesModelPreferredOrder(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []constant.EndpointType
		want      constant.EndpointType
	}{
		{
			name:      "keeps first endpoint even for image-capable models",
			endpoints: []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeImageGeneration},
			want:      constant.EndpointTypeOpenAI,
		},
		{
			name:      "keeps anthropic first when model prefers it",
			endpoints: []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI},
			want:      constant.EndpointTypeAnthropic,
		},
		{
			name:      "falls back to first endpoint for text output",
			endpoints: []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeAnthropic},
			want:      constant.EndpointTypeOpenAI,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := selectPreferredEndpointType(tc.endpoints)
			if got != tc.want {
				t.Fatalf("expected preferred endpoint %q, got %q", tc.want, got)
			}
		})
	}
}

func TestSortEndpointTypes_PreservesInputOrderAndDeduplicates(t *testing.T) {
	endpoints := []constant.EndpointType{
		constant.EndpointTypeGemini,
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeGemini,
		constant.EndpointTypeAnthropic,
	}

	got := sortEndpointTypes(endpoints)
	want := []constant.EndpointType{
		constant.EndpointTypeGemini,
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeAnthropic,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected endpoint order %v, got %v", want, got)
	}
}

func TestCatalogChannelSupportsNormalizedModelName(t *testing.T) {
	channel := &model.Channel{Models: "gpt-4o-gizmo-*"}
	if !catalogChannelSupportsModel(channel, "gpt-4o-gizmo-custom") {
		t.Fatal("normalized model variant should match the channel wildcard ability")
	}
	if catalogChannelSupportsModel(channel, "gpt-4.1-mini") {
		t.Fatal("unrelated model must not match the channel wildcard ability")
	}
}

func TestBuildCatalogCurlExample_UsesPlaceholderBaseURL(t *testing.T) {
	example := buildCatalogCurlExample(
		"gpt-image-1",
		constant.EndpointTypeImageGeneration,
		[]dto.ModelCatalogEndpoint{{Type: constant.EndpointTypeImageGeneration, Path: "/v1/images/generations"}},
		dto.ModelCatalogModality{Output: []string{"image"}},
	)

	if !strings.Contains(example, "{{base_url}}/v1/images/generations") {
		t.Fatalf("expected placeholder base URL in example, got %q", example)
	}
}

func TestBuildCatalogVideoEndpointUsesAsyncVideosPath(t *testing.T) {
	endpoints := []dto.ModelCatalogEndpoint{{Type: constant.EndpointTypeVideoGeneration, Path: defaultCatalogPath(constant.EndpointTypeVideoGeneration, "veo-3-fast")}}
	if endpoints[0].Path != "/v1/videos" {
		t.Fatalf("expected video endpoint path /v1/videos, got %+v", endpoints)
	}
	modality := buildCatalogModality(false, model.Pricing{}, []constant.EndpointType{constant.EndpointTypeVideoGeneration})
	if !stringSliceContains(modality.Output, "video") {
		t.Fatalf("expected video output modality, got %+v", modality)
	}
	example := buildCatalogCurlExample("veo-3-fast", constant.EndpointTypeVideoGeneration, endpoints, modality)
	// The example follows the model's capability row: Veo defaults to eight
	// seconds at 720p, and generation needs no pre-made artifact.
	if !strings.Contains(example, "{{base_url}}/v1/videos") ||
		!strings.Contains(example, "\"seconds\":8") ||
		!strings.Contains(example, "\"resolution\":\"720p\"") ||
		strings.Contains(example, "artifact_url") {
		t.Fatalf("expected video curl example, got %q", example)
	}
	params := buildCatalogParameters("veo-3-fast", constant.EndpointTypeVideoGeneration, 0, nil)
	artifactURL, ok := params["artifact_url"]
	if !ok || artifactURL.Required {
		t.Fatalf("expected optional artifact_url catalog parameter, got %+v", params)
	}
	if _, ok := params["artifact_file_id"]; !ok {
		t.Fatalf("expected artifact_file_id catalog parameter, got %+v", params)
	}
	seconds := params["seconds"]
	if len(seconds.Enum) != 3 || seconds.Enum[2] != "8" || params["resolution"].Enum == nil || params["frame_images"].Type != "array" {
		t.Fatalf("video parameters should come from the capability row, got %+v", params)
	}
}

func TestGetAccessibleModelNamesFiltersByRoutingGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()

	channels := []model.Channel{
		{
			Id:     1,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "direct-upstream",
			Status: common.ChannelStatusEnabled,
			Name:   "direct-channel",
			Models: "catalog-direct,catalog-shared",
			Group:  model.ChannelLaneDirect,
		},
		{
			Id:     2,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "cheap-upstream",
			Status: common.ChannelStatusEnabled,
			Name:   "agent-lane-channel",
			Models: "catalog-cheap,catalog-shared",
			Group:  model.ChannelLaneAgent,
		},
	}
	if err := model.CoreDB().Create(&channels).Error; err != nil {
		t.Fatalf("failed to insert channels: %v", err)
	}
	abilities := []model.Ability{
		{Group: model.ChannelLaneDirect, Model: "catalog-direct", ChannelId: 1, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "catalog-shared", ChannelId: 1, Enabled: true},
		{Group: model.ChannelLaneAgent, Model: "catalog-cheap", ChannelId: 2, Enabled: true},
		{Group: model.ChannelLaneAgent, Model: "catalog-shared", ChannelId: 2, Enabled: true},
	}
	if err := model.CoreDB().Create(&abilities).Error; err != nil {
		t.Fatalf("failed to insert abilities: %v", err)
	}
	models := []model.Model{
		{ModelName: "catalog-direct", Status: 1, NameRule: model.NameRuleExact},
		{ModelName: "catalog-cheap", Status: 1, NameRule: model.NameRuleExact},
		{ModelName: "catalog-shared", Status: 1, NameRule: model.NameRuleExact},
	}
	if err := model.CoreDB().Create(&models).Error; err != nil {
		t.Fatalf("failed to insert model metadata: %v", err)
	}
	model.RefreshPricing()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("token_id", 1)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, model.ChannelLaneDirect)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, model.ChannelLaneDirect)

	got, err := getAccessibleModelNames(c)
	if err != nil {
		t.Fatalf("unexpected catalog error: %v", err)
	}
	want := []string{"catalog-direct", "catalog-shared"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected direct group catalog %v, got %v", want, got)
	}

	got = getUserCatalogModelNames()
	want = []string{"catalog-cheap", "catalog-direct", "catalog-shared"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected user catalog to span published lanes %v, got %v", want, got)
	}
}

func TestGetCatalogBaseGroupsKeepsExplicitLaneWithoutToken(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUsingGroup, model.ChannelLaneDirect)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, model.ChannelLaneDirect)

	got, err := getCatalogBaseGroups(c)
	if err != nil {
		t.Fatalf("unexpected catalog group error: %v", err)
	}
	want := []string{model.ChannelLaneDirect}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected explicit direct lane %v, got %v", want, got)
	}
}

func TestCatalogProjectionGroupsScopesVendorLaneToCurrentModel(t *testing.T) {
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	defer func() { common.MemoryCacheEnabled = previousMemoryCache }()
	if err := model.CoreDB().Create(&model.Vendor{Id: 901, PublicID: "vendor-a", Name: "Strict vendor"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Model{ModelName: "model-without-cheap-price", VendorID: 901, Status: 1, NameRule: model.NameRuleExact}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Channel{Id: 901, Type: constant.ChannelTypeOpenAI, Key: "upstream", Status: common.ChannelStatusEnabled, Name: "direct-only", Models: "model-without-cheap-price", Group: model.ChannelLaneDirect}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Ability{Group: model.ChannelLaneDirect, Model: "model-without-cheap-price", ChannelId: 901, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	vendorGroups := model.TokenVendorGroupMap{"vendor-a": "cheap", "vendor-b": "premium"}
	if got, want := catalogProjectionGroups(nil, vendorGroups, "model-without-cheap-price"), []string{}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unpriced cheap preference must have no candidate lanes: groups=%v want=%v", got, want)
	}
	if got, want := catalogProjectionGroups(nil, vendorGroups, "unmapped-model"), []string{"direct"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unmapped vendor inherited unrelated lanes: groups=%v want=%v", got, want)
	}
}

func TestTextgenEndpointModesScopesPinnedTokenToSpecificChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	defer func() { common.MemoryCacheEnabled = previousMemoryCache }()
	channels := []model.Channel{
		{Id: 31, Type: constant.ChannelTypeOpenAI, Key: "chat", Status: common.ChannelStatusEnabled, Name: "chat-only", Models: "pinned-model", Group: model.ChannelLaneDirect, OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`},
		{Id: 32, Type: constant.ChannelTypeOpenAI, Key: "openai", Status: common.ChannelStatusEnabled, Name: "native", Models: "pinned-model", Group: model.ChannelLaneDirect, OtherSettings: `{"protocol_dialect":"openai"}`},
	}
	if err := model.CoreDB().Create(&channels).Error; err != nil {
		t.Fatalf("seed channels: %v", err)
	}
	if err := model.CoreDB().Create(&[]model.Ability{
		{Group: model.ChannelLaneDirect, Model: "pinned-model", ChannelId: 31, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "pinned-model", ChannelId: 32, Enabled: true},
	}).Error; err != nil {
		t.Fatalf("seed abilities: %v", err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(string(constant.ContextKeyTokenSpecificChannelId), "31")
	modes := textgenEndpointModes(c, []string{model.ChannelLaneDirect}, "pinned-model")
	if modes[constant.EndpointTypeOpenAI] != "native" || modes[constant.EndpointTypeOpenAIResponse] != "bridged" {
		t.Fatalf("expected modes from pinned chat-only channel, got %+v", modes)
	}
}

func TestTextgenEndpointModesUsesFirstRoutableLaneBeforeDirectFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	defer func() { common.MemoryCacheEnabled = previousMemoryCache }()

	channels := []model.Channel{
		{Id: 41, Type: constant.ChannelTypeOpenAI, Key: "cheap-chat", Status: common.ChannelStatusEnabled, Name: "cheap-chat", Models: "lane-mode-model", Group: model.ChannelLaneAgent, OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`},
		{Id: 42, Type: constant.ChannelTypeOpenAI, Key: "direct-openai", Status: common.ChannelStatusEnabled, Name: "direct-openai", Models: "lane-mode-model", Group: model.ChannelLaneDirect, OtherSettings: `{"protocol_dialect":"openai"}`},
	}
	if err := model.CoreDB().Create(&channels).Error; err != nil {
		t.Fatalf("seed channels: %v", err)
	}
	if err := model.CoreDB().Create(&[]model.Ability{
		{Group: model.ChannelLaneAgent, Model: "lane-mode-model", ChannelId: 41, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "lane-mode-model", ChannelId: 42, Enabled: true},
	}).Error; err != nil {
		t.Fatalf("seed abilities: %v", err)
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	modes := textgenEndpointModes(c, []string{model.ChannelLaneAgent, model.ChannelLaneDirect}, "lane-mode-model")
	if modes[constant.EndpointTypeOpenAI] != "native" || modes[constant.EndpointTypeOpenAIResponse] != "bridged" {
		t.Fatalf("direct fallback overrode the selected agent lane: %+v", modes)
	}
}

func TestGetAccessibleModelNamesIncludesVendorRoutedCheapLane(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()

	vendors := []model.Vendor{
		{Id: 101, PublicID: "cheap-vendor", Name: "Cheap Vendor"},
		{Id: 102, PublicID: "other-vendor", Name: "Other Vendor"},
	}
	if err := model.CoreDB().Create(&vendors).Error; err != nil {
		t.Fatalf("failed to insert vendors: %v", err)
	}
	channels := []model.Channel{
		{
			Id:     11,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "direct-upstream",
			Status: common.ChannelStatusEnabled,
			Name:   "direct-channel",
			Models: "catalog-direct,catalog-vendor-direct",
			Group:  model.ChannelLaneDirect,
		},
		{
			Id:     12,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "cheap-upstream",
			Status: common.ChannelStatusEnabled,
			Name:   "agent-lane-channel",
			Models: "catalog-vendor-cheap,catalog-other-cheap",
			Group:  model.ChannelLaneAgent,
		},
	}
	if err := model.CoreDB().Create(&channels).Error; err != nil {
		t.Fatalf("failed to insert channels: %v", err)
	}
	abilities := []model.Ability{
		{Group: model.ChannelLaneDirect, Model: "catalog-vendor-direct", ChannelId: 11, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "catalog-direct", ChannelId: 11, Enabled: true},
		{Group: model.ChannelLaneAgent, Model: "catalog-vendor-cheap", ChannelId: 12, Enabled: true},
		{Group: model.ChannelLaneAgent, Model: "catalog-other-cheap", ChannelId: 12, Enabled: true},
	}
	if err := model.CoreDB().Create(&abilities).Error; err != nil {
		t.Fatalf("failed to insert abilities: %v", err)
	}
	models := []model.Model{
		{ModelName: "catalog-vendor-direct", VendorID: 101, Status: 1, NameRule: model.NameRuleExact},
		{ModelName: "catalog-direct", Status: 1, NameRule: model.NameRuleExact},
		{ModelName: "catalog-vendor-cheap", VendorID: 101, Status: 1, NameRule: model.NameRuleExact},
		{ModelName: "catalog-other-cheap", VendorID: 102, Status: 1, NameRule: model.NameRuleExact},
	}
	if err := model.CoreDB().Create(&models).Error; err != nil {
		t.Fatalf("failed to insert model metadata: %v", err)
	}
	price := 1.25
	if err := model.CoreDB().Create(&[]model.ModelSellPrice{
		{
			ModelName:      "catalog-vendor-cheap",
			ChannelGroup:   model.ChannelLaneAgent,
			InputPerMtok:   &price,
			Active:         true,
			PricingVersion: 1,
		},
		{
			ModelName:      "catalog-other-cheap",
			ChannelGroup:   model.ChannelLaneAgent,
			InputPerMtok:   &price,
			Active:         true,
			PricingVersion: 1,
		},
	}).Error; err != nil {
		t.Fatalf("failed to insert sell prices: %v", err)
	}
	model.RefreshPricing()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("token_id", 1)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, model.ChannelLaneDirect)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, model.ChannelLaneDirect)
	common.SetContextKey(c, constant.ContextKeyTokenVendorGroups, model.TokenVendorGroupMap{
		"cheap-vendor": model.ChannelLaneAgent,
	})
	if got, want := catalogProjectionGroups(getCatalogModelLanes(c), getCatalogVendorGroups(c), "catalog-vendor-cheap"), []string{model.ChannelLaneAgent}; !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog lane candidates=%v want=%v", got, want)
	}

	got, err := getAccessibleModelNames(c)
	if err != nil {
		t.Fatalf("unexpected catalog error: %v", err)
	}
	want := []string{"catalog-direct", "catalog-other-cheap", "catalog-vendor-cheap"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected vendor-routed cheap catalog %v, got %v", want, got)
	}
	common.SetContextKey(c, constant.ContextKeyTokenVendorGroups, model.TokenVendorGroupMap{})
	got, err = getAccessibleModelNames(c)
	if err != nil {
		t.Fatalf("unexpected unpinned catalog error: %v", err)
	}
	want = []string{"catalog-direct", "catalog-other-cheap", "catalog-vendor-cheap", "catalog-vendor-direct"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected unpinned catalog to include agent-lane-only models %v, got %v", want, got)
	}

	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "11")
	got, err = getEnabledCatalogModelNames(c)
	if err != nil || !reflect.DeepEqual(got, []string{"catalog-direct", "catalog-vendor-direct"}) {
		t.Fatalf("pinned direct catalog filtered by cheap preference: %v, %v", got, err)
	}
}

func TestBuildCatalogPricingUsesExplicitSellPriceData(t *testing.T) {
	input := 2.5
	output := 10.0
	cacheRead := 1.25
	cacheWrite5m := 3.125
	cacheWrite1h := 5.0
	got := buildCatalogPricing(
		true,
		model.Pricing{
			ModelName:              "catalog-priced-model",
			QuotaType:              0,
			InputPerMtok:           &input,
			OutputPerMtok:          &output,
			CacheReadPerMtok:       &cacheRead,
			CacheWrite5mPerMtok:    &cacheWrite5m,
			CacheWrite1hPerMtok:    &cacheWrite1h,
			OfficialInputPrice:     999,
			OfficialOutputPrice:    999,
			SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeOpenAI},
		},
		catalogPricingContext{usingGroup: "default"},
	)

	if got.BillingType != "per_token" {
		t.Fatalf("expected per_token billing type, got %q", got.BillingType)
	}
	assertFloatPtr(t, "input_per_million", got.InputPerMillion, 2.5)
	assertFloatPtr(t, "output_per_million", got.OutputPerMillion, 10)
	assertFloatPtr(t, "cache_read_per_million", got.CacheReadPerMillion, 1.25)
	assertFloatPtr(t, "cache_write_5m_per_million", got.CacheWrite5mPerMillion, 3.125)
	assertFloatPtr(t, "cache_write_1h_per_million", got.CacheWrite1hPerMillion, 5)
}

func TestBuildCatalogPricingOmitsUnsupportedCacheWrites(t *testing.T) {
	got := buildCatalogPricing(
		true,
		model.Pricing{
			ModelName:    "catalog-no-cache-write-model",
			QuotaType:    0,
			InputPerMtok: float64Ptr(0.5),
		},
		catalogPricingContext{usingGroup: "default"},
	)

	if got.CacheWrite5mPerMillion != nil {
		t.Fatalf("expected 5m cache write price to be omitted, got %v", *got.CacheWrite5mPerMillion)
	}
	if got.CacheWrite1hPerMillion != nil {
		t.Fatalf("expected 1h cache write price to be omitted, got %v", *got.CacheWrite1hPerMillion)
	}
}

func TestBuildCatalogPricingPrefersSellPriceTableFields(t *testing.T) {
	input := 3.0
	output := 9.0
	cacheWrite := 4.5

	got := buildCatalogPricing(
		true,
		model.Pricing{
			ModelName:           "catalog-sell-price-model",
			QuotaType:           0,
			InputPerMtok:        &input,
			OutputPerMtok:       &output,
			CacheWrite5mPerMtok: &cacheWrite,
		},
		catalogPricingContext{usingGroup: "default"},
	)

	assertFloatPtr(t, "input_per_million", got.InputPerMillion, 3)
	assertFloatPtr(t, "output_per_million", got.OutputPerMillion, 9)
	assertFloatPtr(t, "cache_write_5m_per_million", got.CacheWrite5mPerMillion, 4.5)
}

func TestBuildCatalogPricingSkipsUnconfiguredPrice(t *testing.T) {
	got := buildCatalogPricing(
		true,
		model.Pricing{
			ModelName: "catalog-unconfigured-model",
			QuotaType: 0,
		},
		catalogPricingContext{usingGroup: "default"},
	)

	if got.BillingType != "" {
		t.Fatalf("expected billing type to be omitted for unconfigured price, got %q", got.BillingType)
	}
	if got.InputPerMillion != nil || got.OutputPerMillion != nil {
		t.Fatalf("expected token prices to be omitted for unconfigured price, got %#v", got)
	}
}

func TestBuildCatalogPricingExposesSecondPriceForVideoModels(t *testing.T) {
	second := 0.02
	pricing := model.Pricing{
		ModelName:              "veo-second-priced",
		SecondUSD:              &second,
		SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeVideoGeneration},
	}
	catalogPricing := buildCatalogPricing(true, pricing, catalogPricingContext{usingGroup: "direct"})
	if catalogPricing.Second == nil || *catalogPricing.Second != second {
		t.Fatalf("expected second price %.2f, got %#v", second, catalogPricing.Second)
	}
	if catalogPricing.SecondPrice == nil || *catalogPricing.SecondPrice != second {
		t.Fatalf("expected second_price %.2f, got %#v", second, catalogPricing.SecondPrice)
	}

	usage := buildCatalogUsageCapabilities(
		"veo-second-priced",
		pricing,
		catalogPricing,
		"Google",
		[]constant.EndpointType{constant.EndpointTypeVideoGeneration},
		dto.ModelCatalogModality{Input: []string{"text"}, Output: []string{"video"}},
	)
	assertStringSliceContains(t, usage.ReportedDimensions, "second")
	assertStringSliceContains(t, usage.BillableDimensions, "second")
}

// billing.Quote discounts the whole order since #313, so the catalog's
// account price scales cache and per-call rates too.
func TestBuildCatalogPricingDiscountsWholeOrderLikeBillingQuote(t *testing.T) {
	input := 2.0
	cacheRead := 1.0
	request := 0.5
	got := buildCatalogPricingWithAdjustment(true, model.Pricing{
		InputPerMtok:     &input,
		CacheReadPerMtok: &cacheRead,
		RequestUSD:       &request,
	}, 0.8)

	assertFloatPtr(t, "input_per_million", got.InputPerMillion, 1.6)
	assertFloatPtr(t, "cache_read_per_million", got.CacheReadPerMillion, cacheRead*0.8)
	assertFloatPtr(t, "request", got.Request, request*0.8)
	if got.BillingType != "per_call" {
		t.Fatalf("expected per_call billing type, got %q", got.BillingType)
	}
}

func TestBuildCatalogPersonalizedLanePricesKeepsGroupsIndependent(t *testing.T) {
	input := 1.0
	second := 0.02
	got := buildCatalogPersonalizedLanePrices(model.Pricing{
		ModelName: "lane-priced-model",
		Lanes: []model.PricingLane{
			{ChannelGroup: model.ChannelLaneDirect, InputPerMtok: &input},
			{ChannelGroup: model.ChannelLaneAgent, SecondUSD: &second},
		},
	}, catalogPricingContext{})

	assertFloatPtr(t, "direct input", got[model.ChannelLaneDirect].InputPerMillion, 1)
	assertFloatPtr(t, "cheap second", got[model.ChannelLaneAgent].Second, 0.02)
	if got[model.ChannelLaneDirect].BillingType != "per_token" ||
		got[model.ChannelLaneAgent].BillingType != "per_second" {
		t.Fatalf("expected independent lane billing modes, got %#v", got)
	}
}

func TestBuildCatalogUsageCapabilitiesDeepSeekCacheMiss(t *testing.T) {
	input := 0.27
	output := 1.1
	cacheRead := 0.028
	got := buildCatalogUsageCapabilities(
		"deepseek-chat",
		model.Pricing{
			ModelName:              "deepseek-chat",
			InputPerMtok:           &input,
			OutputPerMtok:          &output,
			CacheReadPerMtok:       &cacheRead,
			SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeOpenAI},
		},
		dto.ModelCatalogPricing{
			InputPerMillion:     &input,
			OutputPerMillion:    &output,
			CacheReadPerMillion: &cacheRead,
		},
		"DeepSeek",
		[]constant.EndpointType{constant.EndpointTypeOpenAI},
		dto.ModelCatalogModality{Input: []string{"text"}, Output: []string{"text"}},
	)

	assertStringSliceContains(t, got.ReportedDimensions, "cache_read")
	assertStringSliceContains(t, got.ReportedDimensions, "cache_miss")
	assertStringSliceContains(t, got.BillableDimensions, "cache_read")
	assertStringSliceContains(t, got.ResponseFields["provider_native"], "usage.prompt_cache_miss_tokens")
}

func TestBuildCatalogUsageCapabilitiesQwenCacheWrite(t *testing.T) {
	input := 0.3
	output := 1.2
	got := buildCatalogUsageCapabilities(
		"qwen-plus",
		model.Pricing{
			ModelName:              "qwen-plus",
			InputPerMtok:           &input,
			OutputPerMtok:          &output,
			SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeOpenAI},
		},
		dto.ModelCatalogPricing{
			InputPerMillion:  &input,
			OutputPerMillion: &output,
		},
		"DashScope",
		[]constant.EndpointType{constant.EndpointTypeOpenAI},
		dto.ModelCatalogModality{Input: []string{"text"}, Output: []string{"text"}},
	)

	assertStringSliceContains(t, got.ReportedDimensions, "cache_read")
	assertStringSliceContains(t, got.ReportedDimensions, "cache_write")
	assertStringSliceContains(t, got.ResponseFields["openai_compatible"], "usage.prompt_tokens_details.cache_write_tokens")
	assertStringSliceContains(t, got.ResponseFields["provider_native"], "usage.cache_creation_input_tokens")
	if stringSliceContains(got.BillableDimensions, "cache_write") {
		t.Fatalf("expected unsplit qwen cache_write not to be marked billable without explicit price, got %v", got.BillableDimensions)
	}
}

func TestBuildCatalogUsageCapabilitiesAnthropicCacheWriteBuckets(t *testing.T) {
	input := 3.0
	output := 15.0
	cacheRead := 0.3
	cacheWrite5m := 3.75
	cacheWrite1h := 6.0
	got := buildCatalogUsageCapabilities(
		"claude-sonnet-4-20250514",
		model.Pricing{
			ModelName:           "claude-sonnet-4-20250514",
			InputPerMtok:        &input,
			OutputPerMtok:       &output,
			CacheReadPerMtok:    &cacheRead,
			CacheWrite5mPerMtok: &cacheWrite5m,
			CacheWrite1hPerMtok: &cacheWrite1h,
			Capabilities: model.ModelCapabilities{
				SupportsPromptCaching: true,
			},
			SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI},
		},
		dto.ModelCatalogPricing{
			InputPerMillion:        &input,
			OutputPerMillion:       &output,
			CacheReadPerMillion:    &cacheRead,
			CacheWrite5mPerMillion: &cacheWrite5m,
			CacheWrite1hPerMillion: &cacheWrite1h,
		},
		"Anthropic",
		[]constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI},
		dto.ModelCatalogModality{Input: []string{"text"}, Output: []string{"text"}},
	)

	assertStringSliceContains(t, got.ReportedDimensions, "cache_write_5m")
	assertStringSliceContains(t, got.ReportedDimensions, "cache_write_1h")
	assertStringSliceContains(t, got.BillableDimensions, "cache_write_5m")
	assertStringSliceContains(t, got.BillableDimensions, "cache_write_1h")
	assertStringSliceContains(t, got.ResponseFields["provider_native"], "usage.cache_creation_input_tokens")
}

func TestBuildCatalogLanesUsesEnabledRoutingAndPreservesEveryPriceDimension(t *testing.T) {
	input := 1.0
	cacheRead := 0.1
	cacheWrite := 1.25
	audioInput := 2.0
	imageOutput := 3.0
	lanes := buildCatalogLanes(model.Pricing{
		ModelName: "lane-priced-model",
		Lanes: []model.PricingLane{
			{
				ChannelGroup:        model.ChannelLaneDirect,
				InputPerMtok:        &input,
				CacheReadPerMtok:    &cacheRead,
				CacheWrite5mPerMtok: &cacheWrite,
				AudioInputPerMtok:   &audioInput,
			},
			{
				ChannelGroup:       model.ChannelLaneAgent,
				ImageOutputPerMtok: &imageOutput,
			},
		},
	}, catalogBuildOptions{
		availableLanesByModel: map[string]map[string]bool{
			"lane-priced-model": {model.ChannelLaneDirect: true},
		},
	})

	if len(lanes) != 2 {
		t.Fatalf("expected two lanes, got %+v", lanes)
	}
	if !lanes[0].Available || lanes[1].Available {
		t.Fatalf("expected only direct lane to be available, got %+v", lanes)
	}
	assertFloatPtr(t, "direct cache read", lanes[0].Pricing.CacheReadPerMillion, cacheRead)
	assertFloatPtr(t, "direct cache write", lanes[0].Pricing.CacheWrite5mPerMillion, cacheWrite)
	assertFloatPtr(t, "direct audio input", lanes[0].Pricing.AudioInput, audioInput)
	assertFloatPtr(t, "cheap image output", lanes[1].Pricing.ImageOutput, imageOutput)
}

func TestBuildCatalogLanesPublishesNoDataHealthWhileSnapshotWarms(t *testing.T) {
	lanes := buildCatalogLanes(model.Pricing{
		ModelName: "warming-model",
		Lanes: []model.PricingLane{
			{ChannelGroup: model.ChannelLaneDirect},
			{ChannelGroup: model.ChannelLaneAgent},
		},
	}, catalogBuildOptions{includeHealth: true})

	if len(lanes) != 2 {
		t.Fatalf("expected two lanes, got %+v", lanes)
	}
	for _, lane := range lanes {
		if lane.Health == nil {
			t.Fatalf("lane %q omitted health while its snapshot warmed", lane.Key)
		}
		if lane.Health.Status != "unknown" || lane.Health.ObservedWindow != "none" {
			t.Fatalf("lane %q health = %+v, want explicit no-data evidence", lane.Key, lane.Health)
		}
		if lane.Health.Availability != nil || lane.Health.LatencyP95Ms != nil {
			t.Fatalf("lane %q invented measurements without evidence: %+v", lane.Key, lane.Health)
		}
	}
}

func assertFloatPtr(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("expected %s=%v, got nil", name, want)
	}
	if *got != want {
		t.Fatalf("expected %s=%v, got %v", name, want, *got)
	}
}

func assertStringSliceContains(t *testing.T, values []string, want string) {
	t.Helper()
	if !stringSliceContains(values, want) {
		t.Fatalf("expected %q in %v", want, values)
	}
}

func TestCatalogIncludesCodexResponsesSourceForGet(t *testing.T) {
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()
	old := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	defer func() { common.MemoryCacheEnabled = old }()
	source := model.Channel{Id: 91, Type: constant.ChannelTypeOpenAI, Key: "synthetic", Status: common.ChannelStatusEnabled, Name: "codex", Models: "codex-test", Group: "cheap", OtherSettings: `{"codex_client_only":true,"protocol_dialect":"openai","capabilities":{"supports_responses":true,"supports_chat_completions":false}}`}
	if err := model.CoreDB().Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Ability{Group: "cheap", Model: "codex-test", ChannelId: 91, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/models/catalog", nil)
	modes := textgenEndpointModes(c, []string{"cheap"}, "codex-test")
	if modes[constant.EndpointTypeOpenAIResponse] != "native" || modes[constant.EndpointTypeOpenAI] != "" {
		t.Fatalf("wrong endpoints: %v", modes)
	}
	result := testChannel(&source, "codex-test", "openai-response")
	if result.localErr == nil || result.providerAttempted {
		t.Fatal("restricted source generic probe must skip before upstream")
	}
}

// A Key can pin one model to a lane. The pin has to beat the vendor default in
// both directions, or the connect dialog and the gateway would disagree about
// where a request goes.
func TestCatalogProjectionGroupsPrefersModelPinOverVendorDefault(t *testing.T) {
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	defer func() { common.MemoryCacheEnabled = previousMemoryCache }()
	if err := model.CoreDB().Create(&model.Vendor{Id: 911, PublicID: "vendor-pin", Name: "Pinned vendor"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Model{ModelName: "pinned-lane-model", VendorID: 911, Status: 1, NameRule: model.NameRuleExact}).Error; err != nil {
		t.Fatal(err)
	}
	channels := []model.Channel{
		{Id: 911, Type: constant.ChannelTypeOpenAI, Key: "direct", Status: common.ChannelStatusEnabled, Name: "direct", Models: "pinned-lane-model", Group: model.ChannelLaneDirect},
		{Id: 912, Type: constant.ChannelTypeOpenAI, Key: "cheap", Status: common.ChannelStatusEnabled, Name: "cheap", Models: "pinned-lane-model", Group: model.ChannelLaneAgent},
	}
	if err := model.CoreDB().Create(&channels).Error; err != nil {
		t.Fatal(err)
	}
	abilities := []model.Ability{
		{Group: model.ChannelLaneDirect, Model: "pinned-lane-model", ChannelId: 911, Enabled: true},
		{Group: model.ChannelLaneAgent, Model: "pinned-lane-model", ChannelId: 912, Enabled: true},
	}
	if err := model.CoreDB().Create(&abilities).Error; err != nil {
		t.Fatal(err)
	}
	input, output := 1.0, 2.0
	prices := []model.ModelSellPrice{
		{ModelName: "pinned-lane-model", ChannelGroup: model.ChannelLaneDirect, InputPerMtok: &input, OutputPerMtok: &output, Active: true, PricingVersion: 1},
		{ModelName: "pinned-lane-model", ChannelGroup: model.ChannelLaneAgent, InputPerMtok: &input, OutputPerMtok: &output, Active: true, PricingVersion: 2},
	}
	if err := model.CoreDB().Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()

	directVendor := model.TokenVendorGroupMap{"vendor-pin": model.ChannelLaneDirect}
	cheapVendor := model.TokenVendorGroupMap{"vendor-pin": model.ChannelLaneAgent}
	pinCheap := model.TokenModelLaneMap{"pinned-lane-model": model.ChannelLaneAgent}
	pinDirect := model.TokenModelLaneMap{"pinned-lane-model": model.ChannelLaneDirect}

	if got, want := catalogProjectionGroups(pinCheap, directVendor, "pinned-lane-model"), []string{model.ChannelLaneAgent}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a cheap pin must beat a direct vendor default: got=%v want=%v", got, want)
	}
	if got, want := catalogProjectionGroups(pinDirect, cheapVendor, "pinned-lane-model"), []string{model.ChannelLaneDirect}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a direct pin must beat a cheap vendor default: got=%v want=%v", got, want)
	}
	// A model nobody pinned still reads its vendor default.
	if got, want := catalogProjectionGroups(pinDirect, cheapVendor, "unpinned-model"), []string{model.ChannelLaneDirect}; !reflect.DeepEqual(got, want) {
		t.Fatalf("an unpinned model must follow its own vendor: got=%v want=%v", got, want)
	}
}

func TestCatalogDiscountScalesEveryRateLikeBilling(t *testing.T) {
	input, output, cache, write5m, write1h := 4.0, 15.0, 0.4, 5.0, 8.0
	got := buildCatalogPricingWithAdjustment(true, model.Pricing{ContextTier: &model.ContextTierPrice{
		Threshold: 272000, InputPerMtok: &input, OutputPerMtok: &output, CacheReadPerMtok: &cache, CacheWrite5mPerMtok: &write5m, CacheWrite1hPerMtok: &write1h,
	}}, 0.8)
	assertFloatPtr(t, "tier input", got.ContextTier.Input, 3.2)
	assertFloatPtr(t, "tier output", got.ContextTier.Output, 12)
	assertFloatPtr(t, "tier cache", got.ContextTier.CacheRead, cache*0.8)
	assertFloatPtr(t, "tier write5m", got.ContextTier.CacheWrite5m, write5m*0.8)
	assertFloatPtr(t, "tier write1h", got.ContextTier.CacheWrite1h, write1h*0.8)
}

func TestCatalogDoesNotRestoreLegacyListingDate(t *testing.T) {
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	defer func() { common.MemoryCacheEnabled = previousMemoryCache }()
	name := "legacy-release-test"
	m := model.Model{ModelName: name, Status: 1, NameRule: model.NameRuleExact, ReleaseDate: "2026-09-28",
		InfoSources: model.ModelInfoSources{model.ModelInfoFieldReleaseDate: model.ModelInfoSourceLegacy}}
	listingDay := time.Now()
	for _, row := range []interface{}{
		&m,
		&model.ModelMetadata{ModelName: name, ReleasedAt: &listingDay},
		&model.Channel{Id: 923, Type: constant.ChannelTypeOpenAI, Key: "test", Status: common.ChannelStatusEnabled, Models: name, Group: model.ChannelLaneDirect},
		&model.Ability{Group: model.ChannelLaneDirect, Model: name, ChannelId: 923, Enabled: true},
		&model.ModelSellPrice{ModelName: name, ChannelGroup: model.ChannelLaneDirect, Active: true, PricingVersion: 1},
	} {
		if err := model.CoreDB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	model.RefreshPricing()
	items := buildModelCatalogItemsWithOptions([]string{name}, catalogBuildOptions{pricingMode: catalogPricingModeList})
	if len(items) != 1 {
		t.Fatalf("missing catalog item: %+v", items)
	}
	if items[0].ReleasedAt != nil || items[0].ReleaseDate != "" || items[0].IsNewRelease {
		t.Fatalf("listing day leaked as official release: %+v", items[0])
	}
	if err := model.CoreDB().Model(&m).Updates(map[string]interface{}{"release_date": "2026-09-01", "info_sources": model.ModelInfoSources{model.ModelInfoFieldReleaseDate: model.ModelInfoSourceManual}}).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	items = buildModelCatalogItemsWithOptions([]string{name}, catalogBuildOptions{pricingMode: catalogPricingModeList})
	if len(items) != 1 || items[0].ReleasedAt == nil || items[0].ReleaseDate != "2026-09-01" {
		t.Fatalf("verified release lost: %+v", items)
	}
}

func TestBuildCatalogPricingPublishesVideoTiersWithoutTheMarkup(t *testing.T) {
	multiplier := 1.07
	pricing := model.Pricing{
		ModelName: "seedance-2.5",
		VideoPricing: (&billing.VideoPrices{
			ResolutionSecondUSD:                map[string]float64{"480p": 0.103, "720p": 0.231, "1080p": 0.569},
			ProviderCostMultiplier:             &multiplier,
			ProviderCostMultiplierByResolution: map[string]float64{"1080p": 1.06},
		}).Public(),
		SupportedEndpointTypes: []constant.EndpointType{constant.EndpointTypeVideoGeneration},
	}
	catalogPricing := buildCatalogPricing(true, pricing, catalogPricingContext{usingGroup: "direct"})
	if catalogPricing.Second == nil || *catalogPricing.Second != 0.103 || catalogPricing.BillingType != "per_second" {
		t.Fatalf("a tiered video lane starts at its cheapest tier: %+v", catalogPricing)
	}
	video := catalogPricing.VideoPricing
	if video == nil || video.ResolutionSecondUSD["1080p"] != 0.569 || !video.ProviderCostBilled {
		t.Fatalf("the catalog carries the per-second tiers: %+v", video)
	}
	if video.ProviderCostMultiplier != nil || len(video.ProviderCostMultiplierByResolution) != 0 {
		t.Fatalf("the catalog must not publish the markup: %+v", video)
	}
	usage := buildCatalogUsageCapabilities("seedance-2.5", pricing, catalogPricing, "ByteDance",
		[]constant.EndpointType{constant.EndpointTypeVideoGeneration},
		dto.ModelCatalogModality{Input: []string{"text"}, Output: []string{"video"}})
	assertStringSliceContains(t, usage.BillableDimensions, "second")
}

func TestVideoCatalogExampleShowsTheWholeAsyncFlow(t *testing.T) {
	example := videoCatalogExample("https://lazu.ai", "/v1/videos", "seedance-2.5")
	for _, want := range []string{
		"curl https://lazu.ai/v1/videos -H",
		`"model":"seedance-2.5"`,
		"curl 'https://lazu.ai/v1/videos/JOB_ID?wait=60'",
		"artifact.url",
		"curl --fail https://lazu.ai/v1/videos/JOB_ID/content",
		"-o video.mp4",
	} {
		if !strings.Contains(example, want) {
			t.Fatalf("video example lacks %q:\n%s", want, example)
		}
	}
}
