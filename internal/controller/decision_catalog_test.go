package controller

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
)

func TestOfficialDecisionCatalogDoesNotReplaceTextModel(t *testing.T) {
	cleanup := setupUserModelsClassifiedTestDB(t)
	defer cleanup()
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	defer func() { common.MemoryCacheEnabled = previousMemoryCache }()
	common.OptionMapRWMutex.Lock()
	oldMap := common.OptionMap
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	old, existed := common.OptionMap[common.DecisionsEnabledOption]
	common.OptionMap[common.DecisionsEnabledOption] = "true"
	common.OptionMapRWMutex.Unlock()
	defer func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if oldMap == nil {
			common.OptionMap = nil
		} else if existed {
			common.OptionMap[common.DecisionsEnabledOption] = old
		} else {
			delete(common.OptionMap, common.DecisionsEnabledOption)
		}
	}()
	if err := model.CoreDB().Create(&model.Vendor{Id: 9859, Name: "OpenAI", PublicID: "synthetic-openai", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{common.OpenAIDecisionModel, "gpt-6-luna"} {
		for _, row := range []interface{}{
			&model.Model{ModelName: name, Status: 1, NameRule: model.NameRuleExact, VendorID: 9859},
			&model.Channel{Id: 9850 + index, Type: constant.ChannelTypeOpenAI, Key: "synthetic", Status: common.ChannelStatusEnabled, Models: name, Group: model.ChannelLaneDirect},
			&model.Ability{Group: model.ChannelLaneDirect, Model: name, ChannelId: 9850 + index, Enabled: true},
			&model.ModelSellPrice{ModelName: name, ChannelGroup: model.ChannelLaneDirect, InputPerMtok: common.GetPointer(0.12), OutputPerMtok: common.GetPointer(0.0), ImageInputPerMtok: common.GetPointer(0.12), ImageOutputPerMtok: common.GetPointer(0.0), CacheReadPerMtok: common.GetPointer(0.0), CacheWrite5mPerMtok: common.GetPointer(0.0), CacheWrite1hPerMtok: common.GetPointer(0.0), Active: true, PricingVersion: 1},
		} {
			if err := model.CoreDB().Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	model.RefreshPricing()
	items := buildModelCatalogItemsWithOptions([]string{common.OpenAIDecisionModel, "gpt-6-luna"}, catalogBuildOptions{pricingMode: catalogPricingModeList})
	if len(items) != 2 {
		t.Fatalf("catalog items = %+v", items)
	}
	for _, item := range items {
		if item.ID == "gpt-6-luna" {
			if item.RequestModel != "" || item.DecisionCapabilities != nil || item.DefaultEndpointType == constant.EndpointTypeDecisions {
				t.Fatal("text model was reclassified")
			}
			continue
		}
		if item.RequestModel != common.OpenAIDecisionRequestModel || item.DecisionCapabilities == nil || !reflect.DeepEqual(item.SupportedEndpointTypes, []constant.EndpointType{constant.EndpointTypeDecisions}) || !reflect.DeepEqual(item.SupportedParameters, []string{"input", "questions"}) {
			t.Fatalf("official decision contract = %+v", item)
		}
		if !reflect.DeepEqual(item.UsageCapabilities.BillableDimensions, []string{"input", "image_input"}) || item.Pricing.ImageInput == nil || *item.Pricing.ImageInput != 0.12 {
			t.Fatalf("official billable dimensions = %+v pricing = %+v", item.UsageCapabilities, item.Pricing)
		}
		if item.Parameters["questions"].Type != "array" || item.Parameters["state"].Required || !item.Parameters["input"].Required {
			t.Fatalf("official parameters = %+v", item.Parameters)
		}
		bodyStart := strings.Index(item.Example.Curl, "-d '")
		if bodyStart < 0 {
			t.Fatal("catalog example has no JSON request")
		}
		raw := item.Example.Curl[bodyStart+4:]
		raw = strings.TrimSuffix(raw, "'")
		var body struct {
			Model     string `json:"model"`
			Input     string `json:"input"`
			Questions []struct {
				Type string `json:"type"`
			} `json:"questions"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatal(err)
		}
		if body.Model != common.OpenAIDecisionRequestModel || body.Input == "" || len(body.Questions) != 1 || body.Questions[0].Type != "predicate" {
			t.Fatalf("example = %s", raw)
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("token_id", 1)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, model.ChannelLaneDirect)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, model.ChannelLaneDirect)
	common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
	for _, scenario := range []struct {
		allowed string
		want    []string
	}{
		{common.OpenAIDecisionModel, []string{common.OpenAIDecisionModel}},
		{common.OpenAIDecisionRequestModel, []string{common.OpenAIDecisionRequestModel, common.OpenAIDecisionModel}},
		{"unrelated", []string{}},
	} {
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{scenario.allowed: true})
		names, err := getAccessibleModelNames(c)
		if err != nil || !reflect.DeepEqual(names, scenario.want) {
			t.Fatalf("token-scoped models for %s = %v, %v; want %v", scenario.allowed, names, err, scenario.want)
		}
		scopedItems := buildModelCatalogItemsWithOptions(names, catalogBuildOptions{pricingMode: catalogPricingModeList})
		if len(scopedItems) != len(scenario.want) {
			t.Fatalf("token-scoped catalog = %+v", scopedItems)
		}
		for _, item := range scopedItems {
			if item.ID == common.OpenAIDecisionModel && item.RequestModel != common.OpenAIDecisionRequestModel {
				t.Fatalf("token-scoped Decisions wire model = %+v", item)
			}
		}
	}
	common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{common.OpenAIDecisionRequestModel: true})
	for _, allowed := range []string{common.OpenAIDecisionModel, common.OpenAIDecisionRequestModel, "unrelated"} {
		common.SetContextKey(c, constant.ContextKeyMemberModelLimit, map[string]bool{allowed: true})
		names, err := getAccessibleModelNames(c)
		want := []string{}
		if allowed == common.OpenAIDecisionModel {
			want = []string{common.OpenAIDecisionModel}
		} else if allowed == common.OpenAIDecisionRequestModel {
			want = []string{common.OpenAIDecisionRequestModel, common.OpenAIDecisionModel}
		}
		if err != nil || !reflect.DeepEqual(names, want) {
			t.Fatalf("member-scoped catalog = %v, %v; want %v", names, err, want)
		}
	}
	common.SetContextKey(c, constant.ContextKeyMemberModelLimit, map[string]bool{})
	common.SetContextKey(c, constant.ContextKeyTokenVendorLimitEnabled, true)
	common.SetContextKey(c, constant.ContextKeyTokenVendorLimits, model.IntSlice{9859})
	if names, err := getAccessibleModelNames(c); err != nil || len(names) != 0 {
		t.Fatalf("vendor-blocked catalog = %v, %v", names, err)
	}
	common.SetContextKey(c, constant.ContextKeyTokenVendorLimitEnabled, false)
	if err := model.CoreDB().Model(&model.Ability{}).Where("model = ?", common.OpenAIDecisionModel).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	if names, err := getAccessibleModelNames(c); err != nil || !reflect.DeepEqual(names, []string{common.OpenAIDecisionRequestModel}) {
		t.Fatalf("unroutable Decisions catalog = %v, %v", names, err)
	}
	if err := model.CoreDB().Model(&model.Ability{}).Where("model = ?", common.OpenAIDecisionModel).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	legacy := decisionCatalogParameters("jev-1.13")
	if !legacy["state"].Required || legacy["questions"].Type != "object" || !strings.Contains(decisionCatalogExample("jev-1.13"), `"type":"noul"`) {
		t.Fatal("legacy contract changed")
	}
	common.OptionMapRWMutex.Lock()
	common.OptionMap[common.DecisionsEnabledOption] = "false"
	common.OptionMapRWMutex.Unlock()
	items = buildModelCatalogItemsWithOptions([]string{common.OpenAIDecisionModel, "gpt-6-luna"}, catalogBuildOptions{pricingMode: catalogPricingModeList})
	if len(items) != 1 || items[0].ID != "gpt-6-luna" {
		t.Fatalf("disabled decision visibility = %+v", items)
	}
}

func TestOfficialDecisionListingPriceUnitsAndDimensions(t *testing.T) {
	raw, err := os.ReadFile("../../ops/references/openai-decisions-listing.json")
	if err != nil {
		t.Fatal(err)
	}
	var template struct {
		WireModel        string                    `json:"wire_model"`
		Registration     model.Model               `json:"model_registration"`
		Channel          model.Channel             `json:"channel_configuration_without_credentials"`
		Costs            []model.ProviderCostPrice `json:"channel_provider_cost_prices"`
		Sell             model.ModelSellPrice      `json:"sell_price"`
		RequiredBindings map[string]string         `json:"required_bindings"`
	}
	if err := json.Unmarshal(raw, &template); err != nil {
		t.Fatal(err)
	}
	if template.WireModel != common.OpenAIDecisionRequestModel || template.Registration.ModelName != common.OpenAIDecisionModel || template.Registration.OfficialInputPrice != .1/1e6 || template.RequiredBindings["model_registration.vendor_id"] == "" {
		t.Fatalf("listing model and official USD/token price = %+v", template.Registration)
	}
	settings := template.Channel.GetSetting()
	if template.Channel.Status != common.ChannelStatusManuallyDisabled || template.Channel.Models != common.OpenAIDecisionModel || settings.DecisionDialect != "openai" || settings.MaxRPS != 30 || settings.MaxInFlight != 128 || settings.SharedMaxRPS != 30 || settings.SharedMaxInFlight != 128 {
		t.Fatalf("unsafe publication or capacity defaults = %+v", template.Channel)
	}
	if len(template.Costs) != 1 || template.Costs[0].ModelName != common.OpenAIDecisionModel || template.Sell.ModelName != common.OpenAIDecisionModel || template.Sell.FollowOfficial {
		t.Fatal("listing must keep the decision alias's cost and explicit sell row separate")
	}
	for _, scenario := range []struct {
		name   string
		want   float64
		prices []*float64
	}{
		{"cost input and image", .10, []*float64{template.Costs[0].InputPerMtok, template.Costs[0].ImageInputPerMtok}},
		{"sell input and image", .12, []*float64{template.Sell.InputPerMtok, template.Sell.ImageInputPerMtok}},
		{"explicit free dimensions", 0, []*float64{template.Costs[0].OutputPerMtok, template.Costs[0].ImageOutputPerMtok, template.Costs[0].CacheReadPerMtok, template.Costs[0].CacheWrite5mPerMtok, template.Costs[0].CacheWrite1hPerMtok, template.Sell.OutputPerMtok, template.Sell.ImageOutputPerMtok, template.Sell.CacheReadPerMtok, template.Sell.CacheWrite5mPerMtok, template.Sell.CacheWrite1hPerMtok}},
	} {
		for _, price := range scenario.prices {
			if price == nil || *price != scenario.want {
				t.Fatalf("%s: missing or wrong price %v", scenario.name, price)
			}
		}
	}
}
