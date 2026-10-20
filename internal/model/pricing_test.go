package model

import (
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"gorm.io/gorm"
)

func TestGetModelEndpointInfo_PrefersModelSpecificOverride(t *testing.T) {
	previousPricingMap := pricingMap
	previousVendorsList := vendorsList
	previousLastGetPricingTime := lastGetPricingTime
	previousSupportedEndpointMap := supportedEndpointMap
	previousModelEndpointInfoMap := modelEndpointInfoMap
	defer func() {
		pricingMap = previousPricingMap
		vendorsList = previousVendorsList
		lastGetPricingTime = previousLastGetPricingTime
		supportedEndpointMap = previousSupportedEndpointMap
		modelEndpointInfoMap = previousModelEndpointInfoMap
	}()

	pricingMap = []Pricing{{ModelName: "model-a"}, {ModelName: "model-b"}}
	lastGetPricingTime = time.Now()
	supportedEndpointMap = map[string]common.EndpointInfo{
		string(constant.EndpointTypeOpenAI): {
			Path:   "/v1/chat/completions",
			Method: "POST",
		},
	}
	modelEndpointInfoMap = map[string]map[string]common.EndpointInfo{
		"model-a": {
			string(constant.EndpointTypeOpenAI): {
				Path:   "/v1/model-a/chat",
				Method: "POST",
			},
		},
	}

	info, ok := GetModelEndpointInfo("model-a", constant.EndpointTypeOpenAI)
	if !ok {
		t.Fatal("expected model-specific endpoint info")
	}
	if info.Path != "/v1/model-a/chat" {
		t.Fatalf("expected model-specific path, got %q", info.Path)
	}

	fallbackInfo, ok := GetModelEndpointInfo("model-b", constant.EndpointTypeOpenAI)
	if !ok {
		t.Fatal("expected fallback endpoint info")
	}
	if fallbackInfo.Path != "/v1/chat/completions" {
		t.Fatalf("expected default path, got %q", fallbackInfo.Path)
	}
}

func TestGetModelEndpointOverrideTypesReturnsOnlyExplicitMetadata(t *testing.T) {
	previousPricingMap := pricingMap
	previousLastGetPricingTime := lastGetPricingTime
	previousModelEndpointInfoMap := modelEndpointInfoMap
	previousModelSupportEndpointTypes := modelSupportEndpointTypes
	defer func() {
		pricingMap = previousPricingMap
		lastGetPricingTime = previousLastGetPricingTime
		modelEndpointInfoMap = previousModelEndpointInfoMap
		modelSupportEndpointTypes = previousModelSupportEndpointTypes
	}()

	pricingMap = []Pricing{{ModelName: "override-model"}}
	lastGetPricingTime = time.Now()
	modelEndpointInfoMap = map[string]map[string]common.EndpointInfo{
		"override-model": {
			string(constant.EndpointTypeJinaRerank): {Path: "/v1/rerank", Method: "POST"},
		},
	}
	modelSupportEndpointTypes = map[string][]constant.EndpointType{
		"override-model": {constant.EndpointTypeOpenAI, constant.EndpointTypeEmbeddings},
	}

	types := GetModelEndpointOverrideTypes("override-model")
	if len(types) != 1 || types[0] != constant.EndpointTypeJinaRerank {
		t.Fatalf("override types = %+v, want explicit rerank only", types)
	}
}

func TestRefreshPricing_DoesNotPromoteModelSpecificEndpointOverride(t *testing.T) {
	previousDB := coreDB
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false

	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	EnsureColumnNamesInitialized()

	if err := db.AutoMigrate(&Channel{}, &Ability{}, &Model{}, &ModelMetadata{}, &Vendor{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}

	previousPricingMap := pricingMap
	previousLastGetPricingTime := lastGetPricingTime
	previousSupportedEndpointMap := supportedEndpointMap
	previousModelEndpointInfoMap := modelEndpointInfoMap
	previousModelSupportEndpointTypes := modelSupportEndpointTypes
	previousModelEnableGroups := modelEnableGroups
	previousModelQuotaTypeMap := modelQuotaTypeMap
	previousModelVendorMap := modelVendorMap
	previousVendorDetailMap := vendorDetailMap
	defer func() {
		pricingMap = previousPricingMap
		lastGetPricingTime = previousLastGetPricingTime
		supportedEndpointMap = previousSupportedEndpointMap
		modelEndpointInfoMap = previousModelEndpointInfoMap
		modelSupportEndpointTypes = previousModelSupportEndpointTypes
		modelEnableGroups = previousModelEnableGroups
		modelQuotaTypeMap = previousModelQuotaTypeMap
		modelVendorMap = previousModelVendorMap
		vendorDetailMap = previousVendorDetailMap
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		common.RedisEnabled = previousRedisEnabled
		coreDB = previousDB
	}()

	models := []*Model{
		{
			Id:        1,
			ModelName: "model-a",
			VendorID:  1,
			Status:    1,
			Endpoints: `{"openai":{"path":"/v1/model-a/chat","method":"POST"}}`,
		},
		{
			Id:        2,
			ModelName: "model-b",
			VendorID:  1,
			Status:    1,
		},
	}
	for _, meta := range models {
		if err := meta.Insert(); err != nil {
			t.Fatalf("failed to insert model metadata: %v", err)
		}
	}

	if err := (&Vendor{Id: 1, Name: "Test Vendor", Status: 1}).Insert(); err != nil {
		t.Fatalf("failed to insert vendor: %v", err)
	}

	channels := []*Channel{
		{
			Id:     11,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "upstream-a",
			Status: common.ChannelStatusEnabled,
			Name:   "channel-a",
			Models: "model-a",
			Group:  "pricing-group",
		},
		{
			Id:     12,
			Type:   constant.ChannelTypeOpenAI,
			Key:    "upstream-b",
			Status: common.ChannelStatusEnabled,
			Name:   "channel-b",
			Models: "model-b",
			Group:  "pricing-group",
		},
	}
	for _, channel := range channels {
		if err := coreDB.Create(channel).Error; err != nil {
			t.Fatalf("failed to insert channel: %v", err)
		}
	}

	abilities := []*Ability{
		{Group: "pricing-group", Model: "model-a", ChannelId: 11, Enabled: true},
		{Group: "pricing-group", Model: "model-b", ChannelId: 12, Enabled: true},
	}
	for _, ability := range abilities {
		if err := coreDB.Create(ability).Error; err != nil {
			t.Fatalf("failed to insert ability: %v", err)
		}
	}

	RefreshPricing()

	globalEndpoints := GetSupportedEndpointMap()
	defaultOpenAIEndpoint, ok := globalEndpoints[string(constant.EndpointTypeOpenAI)]
	if !ok {
		t.Fatalf("expected global openai endpoint fallback")
	}
	if defaultOpenAIEndpoint.Path != "/v1/chat/completions" {
		t.Fatalf("expected global fallback path /v1/chat/completions, got %q", defaultOpenAIEndpoint.Path)
	}

	modelAEndpoint, ok := GetModelEndpointInfo("model-a", constant.EndpointTypeOpenAI)
	if !ok {
		t.Fatalf("expected model-a override endpoint")
	}
	if modelAEndpoint.Path != "/v1/model-a/chat" {
		t.Fatalf("expected model-a override path, got %q", modelAEndpoint.Path)
	}

	modelBEndpoint, ok := GetModelEndpointInfo("model-b", constant.EndpointTypeOpenAI)
	if !ok {
		t.Fatalf("expected model-b fallback endpoint")
	}
	if modelBEndpoint.Path != "/v1/chat/completions" {
		t.Fatalf("expected model-b to keep default path, got %q", modelBEndpoint.Path)
	}

	var modelAPricing *Pricing
	var modelBPricing *Pricing
	for i := range pricingMap {
		switch pricingMap[i].ModelName {
		case "model-a":
			modelAPricing = &pricingMap[i]
		case "model-b":
			modelBPricing = &pricingMap[i]
		}
	}
	if modelAPricing == nil || modelBPricing == nil {
		t.Fatalf("expected pricing entries for both models, got %#v", pricingMap)
	}
	if got := modelAPricing.SupportedEndpoints[string(constant.EndpointTypeOpenAI)].Path; got != "/v1/model-a/chat" {
		t.Fatalf("expected model-a pricing endpoint to keep override, got %q", got)
	}
	if got := modelBPricing.SupportedEndpoints[string(constant.EndpointTypeOpenAI)].Path; got != "/v1/chat/completions" {
		t.Fatalf("expected model-b pricing endpoint to keep default path, got %q", got)
	}
}

func TestRefreshPricingOnlyPublishesActiveLifecycleModels(t *testing.T) {
	previousDB := coreDB
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false

	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	EnsureColumnNamesInitialized()

	if err := db.AutoMigrate(&Channel{}, &Ability{}, &Model{}, &ModelMetadata{}, &Vendor{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}

	previousPricingMap := pricingMap
	previousLastGetPricingTime := lastGetPricingTime
	previousSupportedEndpointMap := supportedEndpointMap
	previousModelEndpointInfoMap := modelEndpointInfoMap
	previousModelSupportEndpointTypes := modelSupportEndpointTypes
	previousModelEnableGroups := modelEnableGroups
	previousModelQuotaTypeMap := modelQuotaTypeMap
	previousModelVendorMap := modelVendorMap
	previousVendorDetailMap := vendorDetailMap
	defer func() {
		pricingMap = previousPricingMap
		lastGetPricingTime = previousLastGetPricingTime
		supportedEndpointMap = previousSupportedEndpointMap
		modelEndpointInfoMap = previousModelEndpointInfoMap
		modelSupportEndpointTypes = previousModelSupportEndpointTypes
		modelEnableGroups = previousModelEnableGroups
		modelQuotaTypeMap = previousModelQuotaTypeMap
		modelVendorMap = previousModelVendorMap
		vendorDetailMap = previousVendorDetailMap
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		common.RedisEnabled = previousRedisEnabled
		coreDB = previousDB
	}()

	for _, meta := range []*Model{
		{Id: 21, ModelName: "active-public-model", Status: 1, LifecycleStatus: ModelLifecycleActive},
		{Id: 22, ModelName: "candidate-private-model", Status: 1, LifecycleStatus: ModelLifecycleCandidate},
		{Id: 23, ModelName: "gpt-4", Status: 1, LifecycleStatus: ModelLifecycleActive},
	} {
		if err := meta.Insert(); err != nil {
			t.Fatalf("failed to insert model %s: %v", meta.ModelName, err)
		}
	}

	channel := &Channel{
		Id:     31,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream",
		Status: common.ChannelStatusEnabled,
		Name:   "channel",
		Models: "active-public-model,candidate-private-model,missing-metadata-model,gpt-4",
		Group:  "pricing-group",
	}
	if err := coreDB.Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	for _, name := range []string{"active-public-model", "candidate-private-model", "missing-metadata-model", "gpt-4"} {
		if err := coreDB.Create(&Ability{
			Group:     "pricing-group",
			Model:     name,
			ChannelId: channel.Id,
			Enabled:   true,
		}).Error; err != nil {
			t.Fatalf("failed to insert ability for %s: %v", name, err)
		}
	}

	RefreshPricing()

	got := make(map[string]bool)
	for _, item := range GetPricing() {
		got[item.ModelName] = true
	}
	if !got["active-public-model"] {
		t.Fatalf("expected active model in pricing, got %#v", got)
	}
	for _, name := range []string{"candidate-private-model", "missing-metadata-model", "gpt-4"} {
		if got[name] {
			t.Fatalf("expected %s to stay out of public pricing, got %#v", name, got)
		}
	}
}

func TestResolveModelSellPriceUsesFormattedModelName(t *testing.T) {
	requestUSD := 0.2
	sellPrice, ok := resolveModelSellPrice(
		"gpt-4-gizmo-custom",
		map[string]ModelSellPrice{
			"gpt-4-gizmo-*": {
				ModelName:      "gpt-4-gizmo-*",
				RequestUSD:     &requestUSD,
				PricingVersion: 7,
			},
		},
	)

	if !ok {
		t.Fatal("expected pattern sell price to resolve")
	}
	if sellPrice.RequestUSD == nil || *sellPrice.RequestUSD != requestUSD {
		t.Fatalf("expected request price %.2f, got %#v", requestUSD, sellPrice.RequestUSD)
	}
	if sellPrice.PricingVersion != 7 {
		t.Fatalf("expected pricing version 7, got %d", sellPrice.PricingVersion)
	}
}

func TestInferCanonicalModelBrandPrefersGeminiGoogle(t *testing.T) {
	owner, icon, ok := InferCanonicalModelBrand("gemini-2.5-pro-preview")
	if !ok {
		t.Fatal("expected gemini model brand inference")
	}
	if owner != "Google" {
		t.Fatalf("expected Google owner, got %q", owner)
	}
	if icon != "Gemini.Color" {
		t.Fatalf("expected Gemini icon, got %q", icon)
	}
}

func TestPricingStatusDistinguishesMissingFromConfiguredZero(t *testing.T) {
	missing := Pricing{ModelName: "unpriced-model"}
	missing.updatePricingStatus()
	if missing.HasPrice {
		t.Fatal("expected missing price to be reported as unconfigured")
	}
	if missing.PricingStatus != "missing" {
		t.Fatalf("expected missing status, got %q", missing.PricingStatus)
	}
	if missing.PricingSource != "" {
		t.Fatalf("expected empty source for missing price, got %q", missing.PricingSource)
	}

	zero := 0.0
	configured := Pricing{ModelName: "free-model", InputPerMtok: &zero}
	configured.updatePricingStatus()
	if !configured.HasPrice {
		t.Fatal("expected explicit zero price to be configured")
	}
	if configured.PricingStatus != "configured" {
		t.Fatalf("expected configured status, got %q", configured.PricingStatus)
	}
	if configured.PricingSource != "model_sell_prices" {
		t.Fatalf("expected default pricing source, got %q", configured.PricingSource)
	}

	laneOnly := Pricing{
		ModelName: "lane-only-model",
		Lanes: []PricingLane{
			{ChannelGroup: ChannelLaneAgent, RequestUSD: &zero},
		},
	}
	laneOnly.updatePricingStatus()
	if !laneOnly.HasPrice {
		t.Fatal("expected lane-only explicit zero price to be configured")
	}
	if laneOnly.PricingStatus != "configured" {
		t.Fatalf("expected lane-only configured status, got %q", laneOnly.PricingStatus)
	}
}

func TestRefreshPricing_VideoModelsAdvertiseOnlyTheVideoEndpoint(t *testing.T) {
	previousDB := coreDB
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	db, err := gorm.Open(gormsqlite.Open("file:video_endpoint_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&Channel{}, &Ability{}, &Model{}, &ModelMetadata{}, &Vendor{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	previousPricingMap := pricingMap
	previousLastGetPricingTime := lastGetPricingTime
	previousSupportedEndpointMap := supportedEndpointMap
	previousModelEndpointInfoMap := modelEndpointInfoMap
	previousModelSupportEndpointTypes := modelSupportEndpointTypes
	previousModelEnableGroups := modelEnableGroups
	previousModelQuotaTypeMap := modelQuotaTypeMap
	previousModelVendorMap := modelVendorMap
	previousVendorDetailMap := vendorDetailMap
	defer func() {
		pricingMap = previousPricingMap
		lastGetPricingTime = previousLastGetPricingTime
		supportedEndpointMap = previousSupportedEndpointMap
		modelEndpointInfoMap = previousModelEndpointInfoMap
		modelSupportEndpointTypes = previousModelSupportEndpointTypes
		modelEnableGroups = previousModelEnableGroups
		modelQuotaTypeMap = previousModelQuotaTypeMap
		modelVendorMap = previousModelVendorMap
		vendorDetailMap = previousVendorDetailMap
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		common.RedisEnabled = previousRedisEnabled
		coreDB = previousDB
	}()

	for _, meta := range []*Model{
		{Id: 1, ModelName: "seedance-test", VendorID: 1, Status: 1, VideoCapabilities: &dto.VideoCapabilities{MinSeconds: 4, MaxSeconds: 30}},
		{Id: 2, ModelName: "chat-test", VendorID: 1, Status: 1},
	} {
		if err := meta.Insert(); err != nil {
			t.Fatalf("failed to insert model metadata: %v", err)
		}
	}
	if err := (&Vendor{Id: 1, Name: "Test Vendor", Status: 1}).Insert(); err != nil {
		t.Fatalf("failed to insert vendor: %v", err)
	}
	if err := coreDB.Create(&Channel{Id: 21, Type: constant.ChannelTypeOpenAI, Key: "k", Status: common.ChannelStatusEnabled, Name: "aggregator", Models: "seedance-test,chat-test", Group: "direct"}).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	for _, name := range []string{"seedance-test", "chat-test"} {
		if err := coreDB.Create(&Ability{Group: "direct", Model: name, ChannelId: 21, Enabled: true}).Error; err != nil {
			t.Fatalf("failed to insert ability: %v", err)
		}
	}

	RefreshPricing()

	if got := GetModelSupportEndpointTypes("seedance-test"); len(got) != 1 || got[0] != constant.EndpointTypeVideoGeneration {
		t.Fatalf("a model with a video capability row is served by /v1/videos only, got %v", got)
	}
	if got := GetModelSupportEndpointTypes("chat-test"); len(got) == 0 || got[0] != constant.EndpointTypeOpenAI {
		t.Fatalf("a chat model on the same channel keeps its chat endpoints, got %v", got)
	}
}
