package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	"github.com/lazu-ai/lazu/internal/relay/metrics"
	"github.com/lazu-ai/lazu/internal/types"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
	"github.com/lazu-ai/lazu/pkg/pricecalendar"
	"github.com/shopspring/decimal"
)

const defaultModelCreatedAt int64 = 1626777600

const publicModelDiscoveryCacheTTL = time.Minute

var modelDiscoveryJSONCache = struct {
	sync.RWMutex
	body      []byte
	expiresAt time.Time
}{}

func getAccessibleModelNames(c *gin.Context) ([]string, error) {
	modelNames, err := getEnabledCatalogModelNames(c)
	if err != nil {
		return nil, err
	}

	modelLimitEnabled := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
	tokenModelLimit := getTokenModelLimitSet(c)
	blockedVendors := getCatalogBlockedVendorSet(c)
	memberModelLimit, _ := common.GetContextKeyType[map[string]bool](c, constant.ContextKeyMemberModelLimit)

	filtered := make([]string, 0, len(modelNames))
	for _, modelName := range modelNames {
		if common.IsDecisionModel(modelName) && !common.DecisionsEnabled() {
			continue
		}
		if modelLimitEnabled && !tokenScopeAllowsModel(c, tokenModelLimit, modelName) {
			continue
		}
		if len(memberModelLimit) > 0 && !catalogScopeAllowsModel(memberModelLimit, nil, modelName) {
			continue
		}
		if len(blockedVendors) > 0 {
			vendorID, _ := model.GetModelVendorMeta(modelName)
			if _, blocked := blockedVendors[vendorID]; blocked {
				continue
			}
		}
		filtered = append(filtered, modelName)
	}

	return filtered, nil
}

func getEnabledCatalogModelNames(c *gin.Context) ([]string, error) {
	if rawID, pinned := c.Get(string(constant.ContextKeyTokenSpecificChannelId)); pinned {
		id, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(rawID)))
		if err != nil {
			return nil, err
		}
		channel, err := model.CacheGetChannel(id)
		if err != nil {
			return nil, err
		}
		if channel.Status != common.ChannelStatusEnabled {
			return nil, nil
		}
		return filterPublishedCatalogModelNames(channel.GetModels()), nil
	}
	groups, err := getCatalogBaseGroups(c)
	if err != nil {
		return nil, err
	}
	groupModelNames := collectCatalogGroupModels(groups)
	groupModelNames = appendCatalogVendorRoutedModels(groupModelNames, getCatalogModelLanes(c), getCatalogVendorGroups(c))
	return filterPublishedCatalogModelNames(groupModelNames), nil
}

func getUserCatalogModelNames() []string {
	return filterPublishedCatalogModelNames(collectCatalogGroupModels([]string{
		model.ChannelLaneDirect,
		model.ChannelLaneAgent,
	}))
}

func filterPublishedCatalogModelNames(groupModelNames []string) []string {
	if len(groupModelNames) == 0 {
		return []string{}
	}

	publicModels := make(map[string]bool)
	for _, row := range model.GetPricing() {
		publicModels[row.ModelName] = true
	}
	modelNames := make([]string, 0, len(groupModelNames))
	for _, modelName := range groupModelNames {
		if publicModels[modelName] {
			modelNames = append(modelNames, modelName)
		}
	}
	sort.Strings(modelNames)
	return modelNames
}

func collectCatalogGroupModels(groups []string) []string {
	seen := make(map[string]struct{})
	modelNames := make([]string, 0)
	appendModel := func(name string) {
		if strings.TrimSpace(name) == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		modelNames = append(modelNames, name)
	}

	for _, group := range groups {
		if strings.TrimSpace(group) == "" {
			continue
		}
		for _, name := range model.GetGroupEnabledModels(group) {
			appendModel(name)
		}
	}

	return modelNames
}

// appendCatalogVendorRoutedModels reshapes the offer to the Key's own lane
// policy. A model whose effective lane is cheap is offered only when the cheap
// lane actually carries it — the request path would reject it rather than
// raise the price — and an agent-lane model the Key routes there is added even
// when no other lane lists it.
func appendCatalogVendorRoutedModels(modelNames []string, modelLanes model.TokenModelLaneMap, vendorGroups model.TokenVendorGroupMap) []string {
	vendorGroups = vendorGroups.Normalize()
	modelLanes = modelLanes.Normalize()

	routesCheap := func(modelName string) bool {
		_, desired, _ := model.LaneCandidatesForModel(modelName, modelLanes, vendorGroups)
		return desired == model.ChannelLaneAgent
	}

	// Do not advertise direct-only models the Key routes to cheap: the request
	// path will reject them rather than raise the price.
	cheapModels := stringSet(model.GetGroupEnabledModels(model.ChannelLaneAgent))
	filtered := make([]string, 0, len(modelNames))
	for _, modelName := range modelNames {
		if routesCheap(modelName) {
			if _, priced := model.GetActiveModelSellPrice(modelName, model.ChannelLaneAgent); !priced || !cheapModels[modelName] || !model.IsValidChannelLane(model.ChannelLaneAgent) {
				continue
			}
		}
		filtered = append(filtered, modelName)
	}
	modelNames = filtered

	if !model.IsValidChannelLane(model.ChannelLaneAgent) {
		return modelNames
	}
	seen := make(map[string]struct{}, len(modelNames))
	for _, modelName := range modelNames {
		seen[modelName] = struct{}{}
	}
	for _, modelName := range model.GetGroupEnabledModels(model.ChannelLaneAgent) {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		if !routesCheap(modelName) {
			continue
		}
		if _, hasCheapPrice := model.GetActiveModelSellPrice(modelName, model.ChannelLaneAgent); !hasCheapPrice {
			continue
		}
		seen[modelName] = struct{}{}
		modelNames = append(modelNames, modelName)
	}
	return modelNames
}

func getCatalogBaseGroups(c *gin.Context) ([]string, error) {
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	tokenGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	if strings.TrimSpace(group) == "" {
		group = model.ChannelLaneDirect
		if tokenGroup != "" {
			group = model.NormalizeChannelLane(tokenGroup)
		}
	}

	return []string{group}, nil
}

func getCatalogModelLanes(c *gin.Context) model.TokenModelLaneMap {
	if lanes, ok := common.GetContextKeyType[model.TokenModelLaneMap](c, constant.ContextKeyTokenModelLanes); ok {
		return lanes.Normalize()
	}
	return model.TokenModelLaneMap{}
}

func getCatalogVendorGroups(c *gin.Context) model.TokenVendorGroupMap {
	if groups, ok := common.GetContextKeyType[model.TokenVendorGroupMap](c, constant.ContextKeyTokenVendorGroups); ok {
		return groups.Normalize()
	}
	return model.TokenVendorGroupMap{}
}

func getTokenModelLimitSet(c *gin.Context) map[string]bool {
	if tokenModelLimit, ok := common.GetContextKeyType[map[string]bool](c, constant.ContextKeyTokenModelLimit); ok {
		return tokenModelLimit
	}
	return map[string]bool{}
}

func getCatalogBlockedVendorSet(c *gin.Context) map[int]struct{} {
	blocked := make(map[int]struct{})
	if !common.GetContextKeyBool(c, constant.ContextKeyTokenVendorLimitEnabled) {
		return blocked
	}
	vendorLimits, ok := common.GetContextKeyType[model.IntSlice](c, constant.ContextKeyTokenVendorLimits)
	if !ok {
		return blocked
	}
	for _, vendorID := range vendorLimits {
		if vendorID <= 0 {
			continue
		}
		blocked[vendorID] = struct{}{}
	}
	return blocked
}

// The catalog has to answer the same question the relay does, or a Key scoped
// to a whole vendor sees an empty model list and then works anyway.
func tokenScopeAllowsModel(c *gin.Context, tokenModelLimit map[string]bool, modelName string) bool {
	scopeVendors, _ := common.GetContextKeyType[map[int]struct{}](c, constant.ContextKeyTokenModelScopeVendors)
	return catalogScopeAllowsModel(tokenModelLimit, scopeVendors, modelName)
}

// A published Decisions item has its own price and route, but accepts the
// official wire model. This one-way scope projection never grants Chat access
// to a Key limited to the Decisions alias. Publication, vendor and member
// restrictions are still checked by getAccessibleModelNames.
func catalogScopeAllowsModel(limits map[string]bool, vendors map[int]struct{}, modelName string) bool {
	return model.TokenModelScopeAllows(limits, vendors, modelName) ||
		(modelName == common.OpenAIDecisionModel && model.TokenModelScopeAllows(limits, vendors, common.OpenAIDecisionRequestModel))
}

func buildOpenAIModels(modelNames []string) []dto.OpenAIModels {
	modelNames = model.ListedModelNames(modelNames)
	models := make([]dto.OpenAIModels, 0, len(modelNames))
	owners := model.CatalogModelOwners(modelNames)
	for _, modelName := range modelNames {
		models = append(models, openAIModelForName(modelName, owners))
	}
	return models
}

func ListModelCatalog(c *gin.Context) {
	modelNames, err := getAccessibleModelNames(c)
	if err != nil {
		writeInternalControlPlaneError(c, err)
		return
	}

	items := buildModelCatalogItems(modelNames, buildCatalogPricingContext(c))
	projectTokenScopedProtocolEndpoints(c, items)
	sortCatalogItems(items)

	c.JSON(http.StatusOK, dto.ModelCatalogResponse{
		Object: "list",
		Data:   items,
	})
}

func projectTokenScopedProtocolEndpoints(c *gin.Context, items []dto.ModelCatalogItem) {
	vendorGroups := getCatalogVendorGroups(c).Normalize()
	modelLanes := getCatalogModelLanes(c)
	for index := range items {
		projectProtocolEndpoints(c, items[index:index+1], catalogProjectionGroups(modelLanes, vendorGroups, items[index].ID))
	}
}

func catalogProjectionGroups(modelLanes model.TokenModelLaneMap, vendorGroups model.TokenVendorGroupMap, modelName string) []string {
	groups, _, _ := model.LaneCandidatesForModel(modelName, modelLanes, vendorGroups)
	return uniqueCatalogGroups(groups)
}

func projectProtocolEndpoints(c *gin.Context, items []dto.ModelCatalogItem, groups []string) {
	for index := range items {
		item := &items[index]
		if item.DecisionCapabilities != nil {
			continue
		}
		modes := textgenEndpointModes(c, groups, item.ID)
		types := make([]constant.EndpointType, 0, len(item.SupportedEndpointTypes)+2)
		for _, endpointType := range item.SupportedEndpointTypes {
			if endpointType != constant.EndpointTypeOpenAI && endpointType != constant.EndpointTypeOpenAIResponse {
				types = append(types, endpointType)
			}
		}
		for _, endpointType := range []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse} {
			if _, ok := modes[endpointType]; ok {
				types = append(types, endpointType)
			}
		}
		item.SupportedEndpointTypes = sortEndpointTypes(types)
		item.DefaultEndpointType = selectPreferredEndpointType(item.SupportedEndpointTypes)
		item.SupportedEndpoints = buildCatalogEndpoints(item.ID, item.SupportedEndpointTypes)
		for endpointIndex := range item.SupportedEndpoints {
			item.SupportedEndpoints[endpointIndex].Mode = modes[item.SupportedEndpoints[endpointIndex].Type]
		}
		// These fields describe the preferred endpoint shape, so rebuild them
		// after token-scoped native/bridge projection changes that endpoint.
		item.Parameters = buildCatalogParameters(item.ID, item.DefaultEndpointType, item.MaxOutputTokens, model.GetModelParamsSchema(item.ID))
		item.Example = dto.ModelCatalogExample{Curl: buildCatalogCurlExample(item.ID, item.DefaultEndpointType, item.SupportedEndpoints, item.Modality)}
	}
}

func uniqueCatalogGroups(groups []string) []string {
	seen := make(map[string]struct{}, len(groups))
	result := make([]string, 0, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		result = append(result, group)
	}
	return result
}

func textgenEndpointModes(c *gin.Context, groups []string, modelName string) map[constant.EndpointType]string {
	result := make(map[constant.EndpointType]string)
	protocols := []struct {
		family       textgen.ProtocolFamily
		endpointType constant.EndpointType
	}{
		{family: textgen.ProtocolOpenAIChat, endpointType: constant.EndpointTypeOpenAI},
		{family: textgen.ProtocolOpenAIResponses, endpointType: constant.EndpointTypeOpenAIResponse},
	}
	semantic := &textgen.Request{Model: textgen.ModelRef{Canonical: modelName}, Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "catalog capability probe"}}}}}
	modeForChannel := func(channel *model.Channel, protocol textgen.ProtocolFamily) string {
		if channel == nil || !catalogChannelSupportsModel(channel, modelName) {
			return ""
		}
		if channel.GetOtherSettings().CodexClientOnly && protocol != textgen.ProtocolOpenAIResponses {
			return ""
		}
		// A Claude Code-only source answers Claude Code's Messages calls and
		// nothing an OpenAI-shaped client could send.
		if channel.GetOtherSettings().ClaudeCodeClientOnly {
			return ""
		}
		parsed := &textgen.ParsedRequest{Client: textgen.Protocol{Family: protocol, Dialect: "openai"}, Semantic: semantic}
		plan, planErr := textgenruntime.PlanForChannel(parsed, channel, modelName)
		if planErr != nil {
			return ""
		}
		if plan.Native {
			return "native"
		}
		return "bridged"
	}
	if rawID, pinned := c.Get(string(constant.ContextKeyTokenSpecificChannelId)); pinned {
		channelID, parseErr := strconv.Atoi(strings.TrimSpace(fmt.Sprint(rawID)))
		if parseErr != nil {
			return result
		}
		channel, channelErr := model.CacheGetChannel(channelID)
		if channelErr != nil {
			return result
		}
		for _, protocol := range protocols {
			if mode := modeForChannel(channel, protocol.family); mode != "" {
				result[protocol.endpointType] = mode
			}
		}
		return result
	}
	for _, protocol := range protocols {
		for _, group := range groups {
			channels := model.CacheGetCatalogChannels(group, modelName)
			groupMode := ""
			for _, channel := range channels {
				mode := modeForChannel(channel, protocol.family)
				if mode == "native" {
					groupMode = mode
					break
				}
				if mode == "bridged" {
					groupMode = mode
				}
			}
			if groupMode != "" {
				result[protocol.endpointType] = groupMode
				break
			}
		}
	}
	return result
}

func catalogChannelSupportsModel(channel *model.Channel, modelName string) bool {
	normalized := model.NormalizePricingModelName(modelName)
	for _, supported := range channel.GetModels() {
		supported = strings.TrimSpace(supported)
		if strings.EqualFold(supported, modelName) || (!strings.EqualFold(normalized, modelName) && strings.EqualFold(supported, normalized)) {
			return true
		}
	}
	return false
}

func ListUserModelCatalog(c *gin.Context) {
	// The plaza presents every published lane; buildCatalogPricingContext carries
	// the viewer's price plan for personalized prices. API-key catalogs continue
	// through getAccessibleModelNames above.
	modelNames := getUserCatalogModelNames()
	items := buildModelCatalogItemsWithOptions(modelNames, catalogBuildOptions{
		pricingContext:        buildCatalogPricingContext(c),
		pricingMode:           catalogPricingModeList,
		includePersonalized:   true,
		includeHealth:         true,
		accessibleByModel:     stringSet(modelNames),
		availableLanesByModel: getCatalogLaneAvailability(),
	})
	projectProtocolEndpoints(c, items, []string{model.ChannelLaneDirect, model.ChannelLaneAgent})
	sortCatalogItems(items)
	c.JSON(http.StatusOK, dto.ModelCatalogResponse{Object: "list", Data: items})
}

func ListModelDiscovery(c *gin.Context) {
	response, err := buildModelDiscoveryResponse(c)
	if err != nil {
		writeInternalControlPlaneError(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func RetrieveModelDiscovery(c *gin.Context) {
	modelID := strings.TrimSpace(c.Param("model"))
	if modelID == "" {
		writeControlPlaneCatalogError(c, types.ErrorCodeModelNotFound, types.D{"field": "model"})
		return
	}

	response, err := buildModelDiscoveryResponseForNames(c, []string{modelID})
	if err != nil {
		writeInternalControlPlaneError(c, err)
		return
	}
	if len(response.Data) == 0 {
		writeControlPlaneCatalogError(c, types.ErrorCodeModelNotFound, types.D{"model": modelID, "field": "model"})
		return
	}
	c.JSON(http.StatusOK, response.Data[0])
}

func GetModelsJSON(c *gin.Context) {
	body, err := cachedPublicModelDiscoveryJSON()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	// This payload now carries lane health, which goes stale in minutes. The
	// long shared max-age was fine for a pure price/spec catalog; keeping it
	// would mean a CDN advertising an outage for an hour after recovery.
	c.Header("Cache-Control", "public, max-age=60, s-maxage=60")
	c.Header("X-Lazu-Models-Source", "/v1/models")
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func InvalidateModelDiscoveryCache() {
	modelDiscoveryJSONCache.Lock()
	modelDiscoveryJSONCache.body = nil
	modelDiscoveryJSONCache.expiresAt = time.Time{}
	modelDiscoveryJSONCache.Unlock()
}

func cachedPublicModelDiscoveryJSON() ([]byte, error) {
	now := time.Now()
	modelDiscoveryJSONCache.RLock()
	if len(modelDiscoveryJSONCache.body) > 0 && now.Before(modelDiscoveryJSONCache.expiresAt) {
		body := append([]byte(nil), modelDiscoveryJSONCache.body...)
		modelDiscoveryJSONCache.RUnlock()
		return body, nil
	}
	modelDiscoveryJSONCache.RUnlock()

	response := buildPublicModelDiscoveryResponse()
	body, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}

	modelDiscoveryJSONCache.Lock()
	modelDiscoveryJSONCache.body = append([]byte(nil), body...)
	modelDiscoveryJSONCache.expiresAt = now.Add(publicModelDiscoveryCacheTTL)
	modelDiscoveryJSONCache.Unlock()
	return body, nil
}

type catalogPricingContext struct {
	usingGroup string
	tokenGroup string
	// pricePlanID is the viewer's 专属折扣 plan; 0 = standard prices.
	pricePlanID int
}

type catalogPricingMode int

const (
	catalogPricingModeAdjusted catalogPricingMode = iota
	catalogPricingModeList
)

type catalogBuildOptions struct {
	pricingContext        catalogPricingContext
	pricingMode           catalogPricingMode
	includePersonalized   bool
	includeHealth         bool
	accessibleByModel     map[string]bool
	availableLanesByModel map[string]map[string]bool
	// healthByModelLane is model -> lane key -> health, populated alongside
	// healthByModel when health is requested.
	healthByModelLane map[string]map[string]dto.ModelCatalogHealth
}

func buildCatalogPricingContext(c *gin.Context) catalogPricingContext {
	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	tokenGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	pricePlanID := common.GetContextKeyInt(c, constant.ContextKeyUserPricePlanID)
	if pricePlanID <= 0 {
		if userID := c.GetInt("id"); userID > 0 {
			if user, err := model.GetUserCache(userID); err == nil && user != nil {
				pricePlanID = user.PricePlanID
			}
		}
	}
	return catalogPricingContext{
		usingGroup:  usingGroup,
		tokenGroup:  tokenGroup,
		pricePlanID: pricePlanID,
	}
}

func buildModelCatalogItems(modelNames []string, pricingContext catalogPricingContext) []dto.ModelCatalogItem {
	return buildModelCatalogItemsWithOptions(modelNames, catalogBuildOptions{
		pricingContext:        pricingContext,
		pricingMode:           catalogPricingModeAdjusted,
		availableLanesByModel: getCatalogLaneAvailability(),
	})
}

func buildModelCatalogItemsWithOptions(modelNames []string, options catalogBuildOptions) []dto.ModelCatalogItem {
	modelNames = listedCatalogModelNames(modelNames, options.accessibleByModel)
	pricingList := model.GetPricing()
	pricingByModel := make(map[string]model.Pricing, len(pricingList))
	for _, pricing := range pricingList {
		pricingByModel[pricing.ModelName] = pricing
	}

	vendorByID := make(map[int]model.PricingVendor, len(model.GetVendors()))
	for _, vendor := range model.GetVendors() {
		vendorByID[vendor.InternalID] = vendor
	}

	metadataByModel := loadCatalogMetadataByModel(modelNames)
	owners := model.CatalogModelOwners(modelNames)
	healthByModel := map[string]dto.ModelCatalogHealth{}
	if options.includeHealth {
		options.healthByModelLane = loadCatalogHealthByLane(modelNames)
		healthByModel = summarizeCatalogLaneHealth(modelNames, options.healthByModelLane)
	}

	items := make([]dto.ModelCatalogItem, 0, len(modelNames))
	for _, modelName := range modelNames {
		if common.IsDecisionModel(modelName) && !common.DecisionsEnabled() {
			continue
		}
		pricing, hasPricing := pricingByModel[modelName]
		if !hasPricing {
			continue
		}
		endpointTypes := pricing.SupportedEndpointTypes
		if !hasPricing || len(endpointTypes) == 0 {
			endpointTypes = model.GetModelSupportEndpointTypes(modelName)
		}
		if common.IsDecisionModel(modelName) {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeDecisions}
		}
		modality := buildCatalogModality(hasPricing, pricing, endpointTypes)
		endpointTypes = sortEndpointTypes(endpointTypes)
		preferredEndpoint := selectPreferredEndpointType(endpointTypes)
		endpoints := buildCatalogEndpoints(modelName, endpointTypes)

		vendorName := owners[modelName]
		if vendorName == "" && hasPricing {
			vendorName = pricing.OwnerBy
		}
		if vendorName == "" && hasPricing {
			vendorName = vendorByID[pricing.InternalVendorID].Name
		}
		if vendorName == "" {
			vendorName = modelOwnerWithCatalog(modelName, owners)
		}

		created := pricing.CreatedAt
		if created == 0 {
			created = defaultModelCreatedAt
		}
		capabilities := pricing.Capabilities
		metadata, hasMetadata := metadataByModel[modelName]

		catalogPricing := buildCatalogListPricing(hasPricing, pricing)
		if options.pricingMode == catalogPricingModeAdjusted {
			catalogPricing = buildCatalogPricing(hasPricing, pricing, options.pricingContext)
		}

		item := dto.ModelCatalogItem{
			ID:                     modelName,
			Object:                 "model",
			Created:                created,
			Name:                   modelName,
			Provider:               vendorName,
			Family:                 modelFamily(modelName),
			OwnedBy:                vendorName,
			Description:            bestModelDescription(hasPricing, pricing),
			DescriptionEn:          pricing.DescriptionEn,
			DescriptionZh:          pricing.DescriptionZh,
			Modality:               modality,
			InputModalities:        modality.Input,
			OutputModalities:       modality.Output,
			Capabilities:           catalogCapabilities(capabilities),
			SupportedEndpointTypes: endpointTypes,
			SupportedEndpoints:     endpoints,
			DefaultEndpointType:    preferredEndpoint,
			ContextLength:          pricing.ContextLength,
			MaxOutputTokens:        pricing.MaxOutputTokens,
			Pricing:                catalogPricing,
			Lanes:                  buildCatalogLanes(pricing, options),
			UsageCapabilities:      buildCatalogUsageCapabilities(modelName, pricing, catalogPricing, vendorName, endpointTypes, modality),
			SupportedParameters:    buildSupportedParameters(capabilities),
			ReleasedAt:             pricing.ReleasedAt,
			DeprecationDate:        pricing.DeprecationDate,
			Parameters:             buildCatalogParameters(modelName, preferredEndpoint, pricing.MaxOutputTokens, model.GetModelParamsSchema(modelName)),
			Example:                dto.ModelCatalogExample{Curl: buildCatalogCurlExample(modelName, preferredEndpoint, endpoints, modality)},
			Tags:                   splitModelTags(pricing.Tags),
			OpenSource:             hasPricing && pricing.OpenSource,
			Maker:                  pricing.Maker,
			MakerRank:              pricing.MakerRank,
			ReleaseDate:            pricing.ReleaseDate,
			IsNewRelease:           pricing.IsNewRelease,
			KnowledgeCutoff:        pricing.KnowledgeCutoff,
			InputLimit:             pricing.InputLimit,
			UnsupportedParams:      pricing.UnsupportedParams,
		}
		if dto.IsSeedream(modelName) {
			item.SupportedParameters = make([]string, 0, len(item.Parameters))
			for name := range item.Parameters {
				item.SupportedParameters = append(item.SupportedParameters, name)
			}
			sort.Strings(item.SupportedParameters)
		}

		if caps := dto.DecisionCapabilitiesForModel(modelName); caps != nil {
			item.DecisionCapabilities = caps
			item.Capabilities = dto.ModelCatalogCapabilities{SupportsVision: caps.MaxImages > 0}
			item.Modality = dto.ModelCatalogModality{Input: []string{"text"}, Output: []string{"decision"}}
			if caps.MaxImages > 0 {
				item.Modality.Input = append(item.Modality.Input, "image")
			}
			item.InputModalities, item.OutputModalities = item.Modality.Input, item.Modality.Output
			item.SupportedParameters = []string{"state", "questions", "images", "question_generation"}
			if modelName == common.OpenAIDecisionModel {
				item.RequestModel = common.OpenAIDecisionRequestModel
				item.SupportedParameters = []string{"input", "questions"}
				item.UsageCapabilities = officialDecisionCatalogUsage(catalogPricing)
			}
			item.Parameters = decisionCatalogParameters(modelName)
			item.Example = dto.ModelCatalogExample{Curl: decisionCatalogExample(modelName)}
		}
		if pricing.ReasoningOptions != nil {
			item.ReasoningOptions = &dto.ModelCatalogReasoning{
				Toggle:  pricing.ReasoningOptions.Toggle,
				Efforts: pricing.ReasoningOptions.Efforts,
			}
		}
		if hasMetadata {
			item.IsNew = metadata.IsNew
			item.IsFeatured = metadata.IsFeatured
			item.Popularity = metadata.PopularityScore
		}
		if health, ok := healthByModel[modelName]; ok {
			// Keep the optional top-level field for API compatibility, but derive
			// it from the same lane evidence. The removed legacy implementation
			// initialized an unsampled enabled channel to 100% availability, which
			// contradicted a lane correctly reporting "no data" after restart.
			item.Health = &health
		}
		if options.includePersonalized {
			accessible := options.accessibleByModel[modelName]
			item.Accessible = &accessible
			yourPrice := buildCatalogPricing(hasPricing, pricing, options.pricingContext)
			item.YourPrice = &yourPrice
			item.YourPricesByGroup = buildCatalogPersonalizedLanePrices(pricing, options.pricingContext)
		}
		items = append(items, item)
	}
	return items
}

func listedCatalogModelNames(names []string, accessible map[string]bool) []string {
	listed := model.ListedModelNames(names)
	present := stringSet(listed)
	for _, name := range names {
		target := model.CompatibilityModelTarget(name)
		// Discovery includes inaccessible public models too. A visible
		// canonical model must not hide the only name this key can call.
		if target != "" && accessible[name] && !accessible[target] && !present[name] {
			listed = append(listed, name)
			present[name] = true
		}
	}
	return listed
}

// summarizeCatalogLaneHealth preserves the optional top-level catalog field
// without reintroducing a second health system. It is deliberately
// conservative: the weakest/unknown lane determines the model status and an
// availability is only published when every lane has evidence.
func summarizeCatalogLaneHealth(
	modelNames []string,
	byModelLane map[string]map[string]dto.ModelCatalogHealth,
) map[string]dto.ModelCatalogHealth {
	result := make(map[string]dto.ModelCatalogHealth, len(modelNames))
	statusRank := map[string]int{"available": 1, "unknown": 2, "degraded": 3, "unavailable": 4}
	for _, modelName := range modelNames {
		summary := dto.ModelCatalogHealth{Status: "unknown", ObservedWindow: "none"}
		lanes := byModelLane[modelName]
		if len(lanes) == 0 {
			result[modelName] = summary
			continue
		}

		worstRank := 0
		allAvailability := true
		availability := 1.0
		observed := channelHealthSourceSample
		for _, lane := range lanes {
			if rank := statusRank[lane.Status]; rank > worstRank {
				worstRank = rank
				summary.Status = lane.Status
			}
			if lane.Availability == nil {
				allAvailability = false
			} else if *lane.Availability < availability {
				availability = *lane.Availability
			}
			if lane.ObservedWindow == channelHealthSourceNone {
				observed = channelHealthSourceNone
			}
			if lane.LatencyP50Ms != nil &&
				(summary.LatencyP50Ms == nil || *lane.LatencyP50Ms > *summary.LatencyP50Ms) {
				value := *lane.LatencyP50Ms
				summary.LatencyP50Ms = &value
			}
			if lane.LatencyP95Ms != nil &&
				(summary.LatencyP95Ms == nil || *lane.LatencyP95Ms > *summary.LatencyP95Ms) {
				value := *lane.LatencyP95Ms
				summary.LatencyP95Ms = &value
			}
		}
		summary.ObservedWindow = observed
		if allAvailability {
			summary.Availability = &availability
		}
		// A lane's latency is a measurement whether customers or probes took
		// it, so the model keeps the slowest lane's figure even when another
		// lane has no evidence at all.
		result[modelName] = summary
	}
	return result
}

func buildModelDiscoveryResponse(c *gin.Context) (dto.ModelCatalogResponse, error) {
	if c.GetInt("token_id") > 0 {
		return buildModelDiscoveryResponseForNames(c, getPublishedModelDiscoveryNames())
	}
	return buildModelDiscoveryResponseForNames(c, getPublicModelDiscoveryNames())
}

func buildModelDiscoveryResponseForNames(c *gin.Context, modelNames []string) (dto.ModelCatalogResponse, error) {
	authenticated := c.GetInt("token_id") > 0
	options := catalogBuildOptions{
		pricingContext:        buildCatalogPricingContext(c),
		pricingMode:           catalogPricingModeList,
		includePersonalized:   authenticated,
		includeHealth:         catalogIncludeRequested(c, "health"),
		availableLanesByModel: getCatalogLaneAvailability(),
	}
	if authenticated {
		accessibleNames, err := getAccessibleModelNames(c)
		if err != nil {
			return dto.ModelCatalogResponse{}, err
		}
		options.accessibleByModel = stringSet(accessibleNames)
	}
	items := buildModelCatalogItemsWithOptions(modelNames, options)
	sortCatalogItems(items)

	response := dto.ModelCatalogResponse{
		Object: "list",
		Data:   items,
	}
	if authenticated {
		group := options.pricingContext.usingGroup
		if strings.TrimSpace(group) == "" {
			group = model.ChannelLaneDirect
		}
		pricingContext := &dto.ModelCatalogPricingContext{Group: group, DiscountFactor: 1}
		if plan, ok := model.GetPricePlan(options.pricingContext.pricePlanID); ok {
			pricingContext.PricePlan = plan.Name
		}
		response.YourPricingContext = pricingContext
	}
	return response, nil
}

func buildPublicModelDiscoveryResponse() dto.ModelCatalogResponse {
	items := buildModelCatalogItemsWithOptions(getPublicModelDiscoveryNames(), catalogBuildOptions{
		pricingMode: catalogPricingModeList,
		// The public plaza shows lane health too. Publishing it is the point:
		// it is only worth splitting stable from eco if a visitor can see how
		// the two are actually behaving. Both the server body cache and shared
		// response cache are limited to one minute so organic outages do not
		// remain frozen behind a catalog cache tuned for static specifications.
		includeHealth:         true,
		availableLanesByModel: getCatalogLaneAvailability(),
	})
	sortCatalogItems(items)
	return dto.ModelCatalogResponse{
		Object: "list",
		Data:   items,
	}
}

func getPublicModelDiscoveryNames() []string {
	return model.ListedModelNames(getPublishedModelDiscoveryNames())
}

func getPublishedModelDiscoveryNames() []string {
	pricingList := model.GetPricing()
	names := make([]string, 0, len(pricingList))
	seen := make(map[string]struct{}, len(pricingList))
	for _, pricing := range pricingList {
		name := strings.TrimSpace(pricing.ModelName)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func stringSet(items []string) map[string]bool {
	result := make(map[string]bool, len(items))
	for _, item := range items {
		result[item] = true
	}
	return result
}

func getCatalogLaneAvailability() map[string]map[string]bool {
	available := make(map[string]map[string]bool)
	for _, group := range []string{model.ChannelLaneDirect, model.ChannelLaneAgent} {
		for _, modelName := range model.GetGroupEnabledModels(group) {
			if available[modelName] == nil {
				available[modelName] = make(map[string]bool)
			}
			available[modelName][group] = true
		}
	}
	return available
}

func catalogIncludeRequested(c *gin.Context, name string) bool {
	if c == nil {
		return false
	}
	for _, part := range strings.Split(c.Query("include"), ",") {
		if strings.EqualFold(strings.TrimSpace(part), name) {
			return true
		}
	}
	return false
}

func sortEndpointTypes(endpointTypes []constant.EndpointType) []constant.EndpointType {
	seen := make(map[constant.EndpointType]struct{}, len(endpointTypes))
	ordered := make([]constant.EndpointType, 0, len(endpointTypes))
	for _, endpointType := range endpointTypes {
		if endpointType == "" {
			continue
		}
		if _, ok := seen[endpointType]; ok {
			continue
		}
		seen[endpointType] = struct{}{}
		ordered = append(ordered, endpointType)
	}
	return ordered
}

func selectPreferredEndpointType(endpointTypes []constant.EndpointType) constant.EndpointType {
	if len(endpointTypes) == 0 {
		return ""
	}
	return endpointTypes[0]
}

func buildCatalogEndpoints(
	modelName string,
	endpointTypes []constant.EndpointType,
) []dto.ModelCatalogEndpoint {
	endpoints := make([]dto.ModelCatalogEndpoint, 0, len(endpointTypes))
	for _, endpointType := range endpointTypes {
		info, ok := model.GetModelEndpointInfo(modelName, endpointType)
		if !ok {
			defaultInfo, hasDefault := common.GetDefaultEndpointInfo(endpointType)
			if hasDefault {
				info = defaultInfo
			}
		}
		if info.Method == "" {
			info.Method = http.MethodPost
		}
		path := strings.ReplaceAll(info.Path, "{model}", modelName)
		if path == "" {
			path = defaultCatalogPath(endpointType, modelName)
		}
		endpoints = append(endpoints, dto.ModelCatalogEndpoint{
			Type:   endpointType,
			Path:   path,
			Method: info.Method,
		})
	}
	return endpoints
}

func defaultCatalogPath(endpointType constant.EndpointType, modelName string) string {
	switch endpointType {
	case constant.EndpointTypeOpenAI:
		return "/v1/chat/completions"
	case constant.EndpointTypeOpenAIResponse:
		return "/v1/responses"
	case constant.EndpointTypeAnthropic:
		return "/v1/messages"
	case constant.EndpointTypeGemini:
		return strings.ReplaceAll("/v1beta/models/{model}:generateContent", "{model}", modelName)
	case constant.EndpointTypeEmbeddings:
		return "/v1/embeddings"
	case constant.EndpointTypeImageGeneration:
		return "/v1/images/generations"
	case constant.EndpointTypeVideoGeneration:
		return "/v1/videos"
	case constant.EndpointTypeJinaRerank:
		return "/v1/rerank"
	default:
		return ""
	}
}

func buildCatalogModality(
	hasPricing bool,
	pricing model.Pricing,
	endpointTypes []constant.EndpointType,
) dto.ModelCatalogModality {
	input := make([]string, 0)
	output := make([]string, 0)
	if hasPricing {
		input = append(input, pricing.ModalityInput...)
		output = append(output, pricing.ModalityOutput...)
	}
	if len(input) == 0 {
		input = append(input, "text")
	}
	if len(output) == 0 {
		output = append(output, "text")
	}

	for _, endpointType := range endpointTypes {
		switch endpointType {
		case constant.EndpointTypeEmbeddings:
			input = appendUniqueStrings(input, "text")
			output = appendUniqueStrings(output, "embedding")
		case constant.EndpointTypeImageGeneration:
			input = appendUniqueStrings(input, "text")
			output = appendUniqueStrings(output, "image")
		case constant.EndpointTypeVideoGeneration:
			input = appendUniqueStrings(input, "text")
			output = appendUniqueStrings(output, "video")
		case constant.EndpointTypeJinaRerank:
			input = appendUniqueStrings(input, "text")
			output = appendUniqueStrings(output, "rerank")
		case constant.EndpointTypeDecisions:
			input = []string{"text"}
			if caps := dto.DecisionCapabilitiesForModel(pricing.ModelName); caps != nil && caps.MaxImages > 0 {
				input = append(input, "image")
			}
			output = []string{"decision"}
		}
	}

	return dto.ModelCatalogModality{
		Input:  input,
		Output: output,
	}
}

func buildCatalogPricing(
	hasPricing bool,
	pricing model.Pricing,
	pricingContext catalogPricingContext,
) dto.ModelCatalogPricing {
	group := selectCatalogPricingGroup(pricingContext, pricing)
	if group == "" {
		group = model.ChannelLaneDirect
	}
	return buildCatalogPlanPricing(hasPricing, pricing, pricingContext.pricePlanID, group)
}

func buildCatalogListPricing(
	hasPricing bool,
	pricing model.Pricing,
) dto.ModelCatalogPricing {
	return buildCatalogPricingWithAdjustment(hasPricing, pricing, 1)
}

func buildCatalogPricingWithAdjustment(
	hasPricing bool,
	pricing model.Pricing,
	pricingAdjustment float64,
) dto.ModelCatalogPricing {
	result := dto.ModelCatalogPricing{
		Currency:       "USD",
		PricingVersion: pricing.PricingVersion,
	}
	if !hasPricing {
		return result
	}

	// Surface the peak schedule so clients/console can compute the effective
	// price during peak windows instead of reading the base as the only rate.
	result.PeakHoursUTC = pricing.PeakHoursUTC
	result.PeakMultiplier = pricing.PeakMultiplier
	result.PeakCalendar = pricecalendar.ForModel(pricing.ModelName)

	scale := func(value *float64) *float64 {
		if value == nil {
			return nil
		}
		return float64Ptr(*value * pricingAdjustment)
	}

	result.Input = scale(pricing.InputPerMtok)
	result.InputPerMillion = scale(pricing.InputPerMtok)
	result.Output = scale(pricing.OutputPerMtok)
	result.OutputPerMillion = scale(pricing.OutputPerMtok)
	// An adjustment discounts the whole order (billing.Quote): cache, audio,
	// image and per-call lines scale the same as input and output.
	result.CacheRead = scale(pricing.CacheReadPerMtok)
	result.CacheReadPerMillion = scale(pricing.CacheReadPerMtok)
	result.CacheWrite5m = scale(pricing.CacheWrite5mPerMtok)
	result.CacheWrite5mPerMillion = scale(pricing.CacheWrite5mPerMtok)
	result.CacheWrite1h = scale(pricing.CacheWrite1hPerMtok)
	result.CacheWrite1hPerMillion = scale(pricing.CacheWrite1hPerMtok)
	result.AudioInput = scale(pricing.AudioInputPerMtok)
	result.AudioOutput = scale(pricing.AudioOutputPerMtok)
	result.ImageInput = scale(pricing.ImageInputPerMtok)
	result.ImageOutput = scale(pricing.ImageOutputPerMtok)
	result.ImagePricing = pricing.ImagePricing.Scaled(pricingAdjustment)
	result.Request = scale(pricing.RequestUSD)
	result.RequestPrice = scale(pricing.RequestUSD)
	result.Second = scale(pricing.SecondUSD)
	result.SecondPrice = scale(pricing.SecondUSD)
	if video := pricing.VideoPricing.Public().Scaled(pricingAdjustment); !video.IsEmpty() {
		result.VideoPricing = video
		// Lists show one figure: the cheapest tier is what a clip starts at.
		if result.Second == nil {
			result.Second = video.LowestSecondUSD()
			result.SecondPrice = video.LowestSecondUSD()
		}
	}
	if tier := pricing.ContextTier; tier != nil && tier.Threshold > 0 {
		result.ContextTier = &dto.ModelCatalogContextTier{
			Threshold:    tier.Threshold,
			Input:        scale(tier.InputPerMtok),
			Output:       scale(tier.OutputPerMtok),
			CacheRead:    scale(tier.CacheReadPerMtok),
			CacheWrite5m: scale(tier.CacheWrite5mPerMtok),
			CacheWrite1h: scale(tier.CacheWrite1hPerMtok),
		}
	}

	switch {
	case pricing.RequestUSD != nil:
		result.BillingType = "per_call"
	case pricing.SecondUSD != nil, result.VideoPricing != nil:
		result.BillingType = "per_second"
	case catalogPriceConfigured(
		pricing.InputPerMtok,
		pricing.OutputPerMtok,
		pricing.CacheReadPerMtok,
		pricing.CacheWrite5mPerMtok,
		pricing.CacheWrite1hPerMtok,
		pricing.AudioInputPerMtok,
		pricing.AudioOutputPerMtok,
		pricing.ImageInputPerMtok,
		pricing.ImageOutputPerMtok,
	):
		result.BillingType = "per_token"
	}
	return result
}

func buildCatalogPersonalizedLanePrices(
	pricing model.Pricing,
	pricingContext catalogPricingContext,
) map[string]dto.ModelCatalogPricing {
	result := make(map[string]dto.ModelCatalogPricing, len(pricing.Lanes))
	for _, lane := range pricing.Lanes {
		group := model.NormalizeChannelLane(lane.ChannelGroup)
		if !model.IsKnownChannelLane(group) {
			continue
		}
		lanePricing := model.Pricing{
			ModelName:              pricing.ModelName,
			InputPerMtok:           lane.InputPerMtok,
			OutputPerMtok:          lane.OutputPerMtok,
			CacheReadPerMtok:       lane.CacheReadPerMtok,
			CacheWrite5mPerMtok:    lane.CacheWrite5mPerMtok,
			CacheWrite1hPerMtok:    lane.CacheWrite1hPerMtok,
			AudioInputPerMtok:      lane.AudioInputPerMtok,
			AudioOutputPerMtok:     lane.AudioOutputPerMtok,
			ImageInputPerMtok:      lane.ImageInputPerMtok,
			ImageOutputPerMtok:     lane.ImageOutputPerMtok,
			ImagePricing:           lane.ImagePricing,
			VideoPricing:           lane.VideoPricing,
			RequestUSD:             lane.RequestUSD,
			SecondUSD:              lane.SecondUSD,
			PeakHoursUTC:           lane.PeakHoursUTC,
			PeakMultiplier:         lane.PeakMultiplier,
			PricingVersion:         lane.PricingVersion,
			ContextTier:            lane.ContextTier,
			SupportedEndpointTypes: pricing.SupportedEndpointTypes,
		}
		result[group] = buildCatalogPlanPricing(true, lanePricing, pricingContext.pricePlanID, group)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func buildCatalogLanes(
	pricing model.Pricing,
	options catalogBuildOptions,
) []dto.ModelCatalogLane {
	lanes := make([]dto.ModelCatalogLane, 0, len(pricing.Lanes))
	for _, lane := range pricing.Lanes {
		group := model.NormalizeChannelLane(lane.ChannelGroup)
		if !model.IsKnownChannelLane(group) {
			continue
		}
		lanePricing := model.Pricing{
			ModelName:              pricing.ModelName,
			InputPerMtok:           lane.InputPerMtok,
			OutputPerMtok:          lane.OutputPerMtok,
			CacheReadPerMtok:       lane.CacheReadPerMtok,
			CacheWrite5mPerMtok:    lane.CacheWrite5mPerMtok,
			CacheWrite1hPerMtok:    lane.CacheWrite1hPerMtok,
			AudioInputPerMtok:      lane.AudioInputPerMtok,
			AudioOutputPerMtok:     lane.AudioOutputPerMtok,
			ImageInputPerMtok:      lane.ImageInputPerMtok,
			ImageOutputPerMtok:     lane.ImageOutputPerMtok,
			ImagePricing:           lane.ImagePricing,
			VideoPricing:           lane.VideoPricing,
			RequestUSD:             lane.RequestUSD,
			SecondUSD:              lane.SecondUSD,
			PeakHoursUTC:           lane.PeakHoursUTC,
			PeakMultiplier:         lane.PeakMultiplier,
			PricingVersion:         lane.PricingVersion,
			ContextTier:            lane.ContextTier,
			SupportedEndpointTypes: pricing.SupportedEndpointTypes,
		}
		listPrice := buildCatalogPricingWithAdjustment(true, lanePricing, 1)
		available := true
		if options.availableLanesByModel != nil {
			available = options.availableLanesByModel[pricing.ModelName][group]
		}
		catalogLane := dto.ModelCatalogLane{
			Key:              catalogLaneKey(group),
			Available:        available,
			Pricing:          listPrice,
			InputPerMillion:  listPrice.InputPerMillion,
			OutputPerMillion: listPrice.OutputPerMillion,
			PerCall:          listPrice.Request,
			PerSecond:        listPrice.Second,
			PeakHoursUTC:     listPrice.PeakHoursUTC,
			PeakCalendar:     listPrice.PeakCalendar,
			PeakMultiplier:   listPrice.PeakMultiplier,
		}
		if options.includePersonalized {
			yourPrice := buildCatalogPlanPricing(true, lanePricing, options.pricingContext.pricePlanID, group)
			catalogLane.YourPrice = &yourPrice
			if plan, ok := model.ResolvePlanDiscount(options.pricingContext.pricePlanID, group, pricing.ModelName); ok {
				catalogLane.PlanDiscountPct = plan.Pct.InexactFloat64()
			}
		}
		if options.includeHealth {
			// Lane health is part of the selected model-plaza contract, including
			// the honest no-evidence state. A missing or not-yet-materialized
			// snapshot must not make the whole health affordance disappear.
			health := dto.ModelCatalogHealth{Status: "unknown", ObservedWindow: "none"}
			if laneHealth, ok := options.healthByModelLane[pricing.ModelName][catalogLane.Key]; ok {
				health = laneHealth
			}
			catalogLane.Health = &health
		}
		lanes = append(lanes, catalogLane)
	}
	return lanes
}

func catalogLaneKey(group string) string {
	if group == model.ChannelLaneAgent {
		return "eco"
	}
	return "stable"
}

func buildCatalogUsageCapabilities(
	modelName string,
	pricing model.Pricing,
	catalogPricing dto.ModelCatalogPricing,
	vendorName string,
	endpointTypes []constant.EndpointType,
	modality dto.ModelCatalogModality,
) dto.ModelCatalogUsageCapabilities {
	if p := catalogPricing.ImagePricing; p != nil {
		billable := []string{"generated_image"}
		if p.HighResolutionOutputUSD != nil {
			billable = append(billable, "generated_image_high_resolution")
		}
		if p.LayerUSD != nil {
			billable = append(billable, "image_layer")
		}
		if p.HighResolutionLayerUSD != nil {
			billable = append(billable, "image_layer_high_resolution")
		}
		if p.ReferenceUSD != nil {
			billable = append(billable, "image_reference")
		}
		return dto.ModelCatalogUsageCapabilities{ReportedDimensions: []string{"image_count", "input_images"}, BillableDimensions: billable, ResponseFields: map[string][]string{"image_count": {"usage.generated_images"}, "input_images": {"usage.input_images"}}}
	}
	providerFamily := usageProviderFamily(modelName, pricing, vendorName)
	reported := make([]string, 0, 8)
	billable := make([]string, 0, 8)

	if catalogPriceConfigured(catalogPricing.Input, catalogPricing.InputPerMillion) ||
		catalogPriceConfigured(catalogPricing.Output, catalogPricing.OutputPerMillion) ||
		reportsTokenUsage(endpointTypes, modality) {
		reported = appendUniqueStrings(reported, usagepkg.DimensionInput, usagepkg.DimensionOutput)
	}
	if catalogPriceConfigured(catalogPricing.Input, catalogPricing.InputPerMillion) {
		billable = appendUniqueStrings(billable, usagepkg.DimensionInput)
	}
	if catalogPriceConfigured(catalogPricing.Output, catalogPricing.OutputPerMillion) {
		billable = appendUniqueStrings(billable, usagepkg.DimensionOutput)
	}

	cacheReadReported := pricing.Capabilities.SupportsPromptCaching ||
		catalogPriceConfigured(catalogPricing.CacheRead, catalogPricing.CacheReadPerMillion) ||
		providerFamily == "deepseek" ||
		providerFamily == "anthropic" ||
		providerFamily == "gemini" ||
		providerFamily == "qwen" ||
		providerFamily == "openrouter"
	if cacheReadReported {
		reported = appendUniqueStrings(reported, usagepkg.DimensionCacheRead)
	}
	if catalogPriceConfigured(catalogPricing.CacheRead, catalogPricing.CacheReadPerMillion) {
		billable = appendUniqueStrings(billable, usagepkg.DimensionCacheRead)
	}

	cacheWriteReported := providerFamily == "anthropic" ||
		providerFamily == "qwen" ||
		providerFamily == "openrouter" ||
		catalogPriceConfigured(catalogPricing.CacheWrite5m, catalogPricing.CacheWrite5mPerMillion) ||
		catalogPriceConfigured(catalogPricing.CacheWrite1h, catalogPricing.CacheWrite1hPerMillion)
	if cacheWriteReported {
		reported = appendUniqueStrings(reported, usagepkg.DimensionCacheWrite)
	}
	if providerFamily == "anthropic" || catalogPriceConfigured(catalogPricing.CacheWrite5m, catalogPricing.CacheWrite5mPerMillion) {
		reported = appendUniqueStrings(reported, usagepkg.DimensionCacheWrite5m)
	}
	if providerFamily == "anthropic" || catalogPriceConfigured(catalogPricing.CacheWrite1h, catalogPricing.CacheWrite1hPerMillion) {
		reported = appendUniqueStrings(reported, usagepkg.DimensionCacheWrite1h)
	}
	if catalogPriceConfigured(catalogPricing.CacheWrite5m, catalogPricing.CacheWrite5mPerMillion) {
		billable = appendUniqueStrings(billable, usagepkg.DimensionCacheWrite5m)
	}
	if catalogPriceConfigured(catalogPricing.CacheWrite1h, catalogPricing.CacheWrite1hPerMillion) {
		billable = appendUniqueStrings(billable, usagepkg.DimensionCacheWrite1h)
	}
	if providerFamily == "deepseek" {
		reported = appendUniqueStrings(reported, usagepkg.DimensionCacheMiss)
	}

	if pricing.Capabilities.SupportsAudioInput || catalogPriceConfigured(catalogPricing.AudioInput) {
		reported = appendUniqueStrings(reported, usagepkg.DimensionAudioInput)
	}
	if catalogPriceConfigured(catalogPricing.AudioInput) {
		billable = appendUniqueStrings(billable, usagepkg.DimensionAudioInput)
	}
	if pricing.Capabilities.SupportsAudioOutput || catalogPriceConfigured(catalogPricing.AudioOutput) {
		reported = appendUniqueStrings(reported, usagepkg.DimensionAudioOutput)
	}
	if catalogPriceConfigured(catalogPricing.AudioOutput) {
		billable = appendUniqueStrings(billable, usagepkg.DimensionAudioOutput)
	}
	if stringSliceContains(modality.Input, "image") || pricing.Capabilities.SupportsVision || catalogPriceConfigured(catalogPricing.ImageInput) {
		reported = appendUniqueStrings(reported, usagepkg.DimensionImageInput)
	}
	if catalogPriceConfigured(catalogPricing.ImageInput) {
		billable = appendUniqueStrings(billable, usagepkg.DimensionImageInput)
	}
	if stringSliceContains(modality.Output, "image") || catalogPriceConfigured(catalogPricing.ImageOutput) {
		reported = appendUniqueStrings(reported, usagepkg.DimensionImageOutput)
	}
	if catalogPriceConfigured(catalogPricing.ImageOutput) {
		billable = appendUniqueStrings(billable, usagepkg.DimensionImageOutput)
	}
	if endpointTypesContain(endpointTypes, constant.EndpointTypeImageGeneration) {
		reported = appendUniqueStrings(reported, usagepkg.DimensionImageCount)
	}
	if endpointTypesContain(endpointTypes, constant.EndpointTypeVideoGeneration) {
		reported = appendUniqueStrings(reported, usagepkg.DimensionVideoSeconds, usagepkg.DimensionSecond)
		if pricing.SecondUSD != nil || !pricing.VideoPricing.IsEmpty() {
			billable = appendUniqueStrings(billable, usagepkg.DimensionSecond)
		}
	}
	if pricing.QuotaType == 1 || catalogPriceConfigured(catalogPricing.Request, catalogPricing.RequestPrice) {
		reported = appendUniqueStrings(reported, usagepkg.DimensionRequest)
	}
	if catalogPriceConfigured(catalogPricing.Request, catalogPricing.RequestPrice) {
		billable = appendUniqueStrings(billable, usagepkg.DimensionRequest)
	}

	return dto.ModelCatalogUsageCapabilities{
		ReportedDimensions: reported,
		BillableDimensions: billable,
		ResponseFields:     buildCatalogUsageResponseFields(providerFamily, reported),
	}
}

func usageProviderFamily(modelName string, pricing model.Pricing, vendorName string) string {
	modelID := strings.ToLower(modelName)
	raw := strings.ToLower(strings.Join([]string{
		modelName,
		pricing.VendorID,
		pricing.OwnerBy,
		vendorName,
	}, " "))
	switch {
	case strings.Contains(raw, "deepseek"):
		return "deepseek"
	case strings.Contains(raw, "qwen") || strings.Contains(raw, "dashscope") || strings.Contains(raw, "aliyun") || strings.Contains(raw, "alibaba"):
		return "qwen"
	case strings.Contains(raw, "anthropic") || strings.Contains(raw, "claude"):
		return "anthropic"
	case strings.Contains(raw, "gemini") || strings.Contains(raw, "google") || strings.Contains(raw, "vertex"):
		return "gemini"
	case strings.Contains(raw, "openrouter"):
		return "openrouter"
	case strings.Contains(raw, "openai") || strings.Contains(raw, "gpt-") || strings.HasPrefix(modelID, "o3") || strings.HasPrefix(modelID, "o4"):
		return "openai"
	default:
		return "openai_compatible"
	}
}

func buildCatalogUsageResponseFields(providerFamily string, reportedDimensions []string) map[string][]string {
	openAIFields := []string{
		"usage.prompt_tokens",
		"usage.completion_tokens",
		"usage.total_tokens",
	}
	if stringSliceContains(reportedDimensions, usagepkg.DimensionCacheRead) {
		openAIFields = append(openAIFields,
			"usage.prompt_tokens_details.cached_tokens",
			"usage.input_tokens_details.cached_tokens",
		)
	}
	if stringSliceContains(reportedDimensions, usagepkg.DimensionCacheWrite) {
		openAIFields = append(openAIFields,
			"usage.prompt_tokens_details.cache_write_tokens",
			"usage.input_tokens_details.cache_write_tokens",
		)
	}
	if stringSliceContains(reportedDimensions, usagepkg.DimensionCacheWrite5m) {
		openAIFields = append(openAIFields,
			"usage.prompt_tokens_details.cache_write_5m_tokens",
			"usage.input_tokens_details.cache_write_5m_tokens",
		)
	}
	if stringSliceContains(reportedDimensions, usagepkg.DimensionCacheWrite1h) {
		openAIFields = append(openAIFields,
			"usage.prompt_tokens_details.cache_write_1h_tokens",
			"usage.input_tokens_details.cache_write_1h_tokens",
		)
	}
	if stringSliceContains(reportedDimensions, usagepkg.DimensionCacheMiss) {
		openAIFields = append(openAIFields,
			"usage.prompt_tokens_details.cache_miss_tokens",
			"usage.input_tokens_details.cache_miss_tokens",
		)
	}

	fields := map[string][]string{
		"openai_compatible": openAIFields,
		"request_detail": {
			"usage.dimensions",
			"billing.line_items",
		},
	}
	if providerFields := catalogProviderRawUsageFields(providerFamily); len(providerFields) > 0 {
		fields["provider_native"] = providerFields
	}
	return fields
}

func catalogProviderRawUsageFields(providerFamily string) []string {
	switch providerFamily {
	case "anthropic":
		return []string{
			"usage.cache_read_input_tokens",
			"usage.cache_creation_input_tokens",
			"usage.cache_creation.ephemeral_5m_input_tokens",
			"usage.cache_creation.ephemeral_1h_input_tokens",
		}
	case "deepseek":
		return []string{
			"usage.prompt_cache_hit_tokens",
			"usage.prompt_cache_miss_tokens",
		}
	case "gemini":
		return []string{
			"usage_metadata.promptTokenCount",
			"usage_metadata.candidatesTokenCount",
			"usage_metadata.totalTokenCount",
			"usage_metadata.cachedContentTokenCount",
		}
	case "qwen":
		return []string{
			"usage.cached_tokens",
			"usage.cache_creation_input_tokens",
			"usage.input_tokens_details.cached_tokens",
			"usage.input_tokens_details.cache_creation_input_tokens",
		}
	case "openai":
		return []string{
			"usage.prompt_tokens_details.cached_tokens",
		}
	case "openrouter":
		return []string{
			"usage.prompt_tokens_details.cached_tokens",
			"usage.prompt_tokens_details.cache_write_tokens",
		}
	default:
		return nil
	}
}

func reportsTokenUsage(endpointTypes []constant.EndpointType, modality dto.ModelCatalogModality) bool {
	if len(endpointTypes) == 0 {
		return true
	}
	for _, endpointType := range endpointTypes {
		switch endpointType {
		case constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse, constant.EndpointTypeAnthropic, constant.EndpointTypeGemini, constant.EndpointTypeEmbeddings, constant.EndpointTypeJinaRerank:
			return true
		}
	}
	return stringSliceContains(modality.Input, "text") || stringSliceContains(modality.Output, "text")
}

func endpointTypesContain(endpointTypes []constant.EndpointType, target constant.EndpointType) bool {
	for _, endpointType := range endpointTypes {
		if endpointType == target {
			return true
		}
	}
	return false
}

func catalogPriceConfigured(values ...*float64) bool {
	for _, value := range values {
		if value != nil {
			return true
		}
	}
	return false
}

func selectCatalogPricingGroup(
	pricingContext catalogPricingContext,
	pricing model.Pricing,
) string {
	if len(pricing.EnableGroup) == 0 {
		return pricingContext.usingGroup
	}
	if stringSliceContains(pricing.EnableGroup, pricingContext.usingGroup) {
		return pricingContext.usingGroup
	}
	if len(pricing.EnableGroup) == 1 {
		return pricing.EnableGroup[0]
	}
	return lowestCatalogPricingGroup(pricingContext, pricing.EnableGroup, pricing.ModelName)
}

func lowestCatalogPricingGroup(pricingContext catalogPricingContext, groups []string, currentModel string) string {
	if len(groups) == 0 {
		return pricingContext.usingGroup
	}
	bestGroup := groups[0]
	bestPlanFactor := catalogPlanFactorForLane(pricingContext, bestGroup, currentModel)
	for _, group := range groups[1:] {
		planFactor := catalogPlanFactorForLane(pricingContext, group, currentModel)
		if planFactor < bestPlanFactor {
			bestGroup = group
			bestPlanFactor = planFactor
		}
	}
	return bestGroup
}

func catalogPlanFactorForLane(pricingContext catalogPricingContext, group, currentModel string) float64 {
	if group == "" {
		group = model.ChannelLaneDirect
	}
	if plan, ok := model.ResolvePlanDiscount(pricingContext.pricePlanID, group, currentModel); ok {
		return 1 - plan.Pct.InexactFloat64()/100
	}
	return 1
}

func buildCatalogParameters(
	modelName string,
	endpointType constant.EndpointType,
	maxOutputTokens int64,
	paramsSchema *model.ModelParamsSchema,
) map[string]dto.CatalogParam {
	defaultMaxTokens := recommendedMaxTokens(maxOutputTokens, 1024)

	switch endpointType {
	case constant.EndpointTypeOpenAI:
		return map[string]dto.CatalogParam{
			"model": {
				Type:        "string",
				Description: "Model ID from the catalog.",
				Required:    true,
			},
			"messages": {
				Type:        "array",
				Description: "OpenAI-compatible chat messages.",
				Required:    true,
			},
			"temperature": {
				Type:        "float",
				Description: "Sampling temperature.",
				Default:     1.0,
				Range:       []float64{0, 2},
			},
			"max_tokens": {
				Type:        "integer",
				Description: "Maximum output tokens.",
				Default:     defaultMaxTokens,
			},
			"stream": {
				Type:        "boolean",
				Description: "Enable streaming SSE responses.",
				Default:     false,
			},
		}
	case constant.EndpointTypeOpenAIResponse:
		return map[string]dto.CatalogParam{
			"model": {
				Type:        "string",
				Description: "Model ID from the catalog.",
				Required:    true,
			},
			"input": {
				Type:        "string_or_array",
				Description: "Prompt or structured input.",
				Required:    true,
			},
			"temperature": {
				Type:        "float",
				Description: "Sampling temperature.",
				Default:     1.0,
				Range:       []float64{0, 2},
			},
			"max_output_tokens": {
				Type:        "integer",
				Description: "Maximum output tokens.",
				Default:     defaultMaxTokens,
			},
			"stream": {
				Type:        "boolean",
				Description: "Enable streaming SSE responses.",
				Default:     false,
			},
		}
	case constant.EndpointTypeAnthropic:
		return map[string]dto.CatalogParam{
			"model": {
				Type:        "string",
				Description: "Model ID from the catalog.",
				Required:    true,
			},
			"messages": {
				Type:        "array",
				Description: "Anthropic Messages API payload.",
				Required:    true,
			},
			"max_tokens": {
				Type:        "integer",
				Description: "Maximum output tokens.",
				Default:     defaultMaxTokens,
				Required:    true,
			},
			"temperature": {
				Type:        "float",
				Description: "Sampling temperature.",
				Default:     1.0,
				Range:       []float64{0, 1},
			},
			"stream": {
				Type:        "boolean",
				Description: "Enable streaming SSE responses.",
				Default:     false,
			},
		}
	case constant.EndpointTypeGemini:
		return map[string]dto.CatalogParam{
			"contents": {
				Type:        "array",
				Description: "Gemini content parts payload.",
				Required:    true,
			},
			"generationConfig.temperature": {
				Type:        "float",
				Description: "Sampling temperature.",
				Default:     1.0,
				Range:       []float64{0, 2},
			},
			"generationConfig.maxOutputTokens": {
				Type:        "integer",
				Description: "Maximum output tokens.",
				Default:     defaultMaxTokens,
			},
		}
	case constant.EndpointTypeEmbeddings:
		return map[string]dto.CatalogParam{
			"model": {
				Type:        "string",
				Description: "Model ID from the catalog.",
				Required:    true,
			},
			"input": {
				Type:        "string_or_array",
				Description: "Text or array of texts to embed.",
				Required:    true,
			},
			"dimensions": {
				Type:        "integer",
				Description: "Optional embedding dimensions override.",
			},
		}
	case constant.EndpointTypeImageGeneration:
		if dto.IsSeedream(modelName) {
			return seedreamCatalogParameters(modelName)
		}
		return map[string]dto.CatalogParam{
			"model": {
				Type:        "string",
				Description: "Model ID from the catalog.",
				Required:    true,
			},
			"prompt": {
				Type:        "string",
				Description: "Image generation prompt.",
				Required:    true,
			},
			"size": {
				Type:        "string",
				Description: "Requested image size.",
				Default:     "1024x1024",
				Enum:        []string{"1024x1024", "1536x1024", "1024x1536"},
			},
			"n": {
				Type:        "integer",
				Description: "Number of images to generate.",
				Default:     1,
				Range:       []float64{1, 10},
			},
		}
	case constant.EndpointTypeVideoGeneration:
		return videoCatalogParameters(modelName)
	case constant.EndpointTypeJinaRerank:
		return map[string]dto.CatalogParam{
			"model": {
				Type:        "string",
				Description: "Model ID from the catalog.",
				Required:    true,
			},
			"query": {
				Type:        "string",
				Description: "Search query used for reranking.",
				Required:    true,
			},
			"documents": {
				Type:        "array",
				Description: "Documents to rerank.",
				Required:    true,
			},
			"top_n": {
				Type:        "integer",
				Description: "How many ranked results to return.",
				Default:     3,
				Range:       []float64{1, 100},
			},
		}
	default:
		return map[string]dto.CatalogParam{}
	}
}

func buildCatalogCurlExample(
	modelName string,
	endpointType constant.EndpointType,
	endpoints []dto.ModelCatalogEndpoint,
	modality dto.ModelCatalogModality,
) string {
	baseURL := "{{base_url}}"
	path := endpointPathForType(endpointType, endpoints)
	switch endpointType {
	case constant.EndpointTypeOpenAIResponse:
		return fmt.Sprintf(
			"curl %s%s -H 'Authorization: Bearer YOUR_API_KEY' -H 'Content-Type: application/json' -d '{\"model\":\"%s\",\"input\":\"Hello from Lazu\"}'",
			baseURL,
			path,
			modelName,
		)
	case constant.EndpointTypeAnthropic:
		return fmt.Sprintf(
			"curl %s%s -H 'Authorization: Bearer YOUR_API_KEY' -H 'x-api-key: YOUR_API_KEY' -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' -d '{\"model\":\"%s\",\"max_tokens\":1024,\"messages\":[{\"role\":\"user\",\"content\":\"Hello from Lazu\"}]}'",
			baseURL,
			path,
			modelName,
		)
	case constant.EndpointTypeGemini:
		return fmt.Sprintf(
			"curl '%s%s?key=YOUR_API_KEY' -H 'Content-Type: application/json' -d '{\"contents\":[{\"parts\":[{\"text\":\"Hello from Lazu\"}]}]}'",
			baseURL,
			path,
		)
	case constant.EndpointTypeEmbeddings:
		return fmt.Sprintf(
			"curl %s%s -H 'Authorization: Bearer YOUR_API_KEY' -H 'Content-Type: application/json' -d '{\"model\":\"%s\",\"input\":\"Hello from Lazu\"}'",
			baseURL,
			path,
			modelName,
		)
	case constant.EndpointTypeImageGeneration:
		if dto.IsSeedream(modelName) {
			return fmt.Sprintf("curl %s%s -H 'Authorization: Bearer YOUR_API_KEY' -H 'Content-Type: application/json' -d '{\"model\":\"%s\",\"prompt\":\"Three colorful cubes on a white table\",\"size\":\"2K\",\"watermark\":false}'", baseURL, path, modelName)
		}
		return fmt.Sprintf(
			"curl %s%s -H 'Authorization: Bearer YOUR_API_KEY' -H 'Content-Type: application/json' -d '{\"model\":\"%s\",\"prompt\":\"A minimalist poster of a sunrise over the ocean\",\"size\":\"1024x1024\"}'",
			baseURL,
			path,
			modelName,
		)
	case constant.EndpointTypeVideoGeneration:
		return videoCatalogExample(baseURL, path, modelName)
	case constant.EndpointTypeJinaRerank:
		return fmt.Sprintf(
			"curl %s%s -H 'Authorization: Bearer YOUR_API_KEY' -H 'Content-Type: application/json' -d '{\"model\":\"%s\",\"query\":\"Which document is about billing?\",\"documents\":[\"API billing is usage-based.\",\"This guide covers image generation.\"],\"top_n\":2}'",
			baseURL,
			path,
			modelName,
		)
	default:
		if stringSliceContains(modality.Input, "image") {
			return fmt.Sprintf(
				"curl %s%s -H 'Authorization: Bearer YOUR_API_KEY' -H 'Content-Type: application/json' -d '{\"model\":\"%s\",\"messages\":[{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"Describe this image\"},{\"type\":\"image_url\",\"image_url\":{\"url\":\"https://upload.wikimedia.org/wikipedia/commons/thumb/3/3a/Cat03.jpg/1200px-Cat03.jpg\"}}]}],\"max_tokens\":300}'",
				baseURL,
				path,
				modelName,
			)
		}
		return fmt.Sprintf(
			"curl %s%s -H 'Authorization: Bearer YOUR_API_KEY' -H 'Content-Type: application/json' -d '{\"model\":\"%s\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello from Lazu\"}]}'",
			baseURL,
			path,
			modelName,
		)
	}
}

func endpointPathForType(endpointType constant.EndpointType, endpoints []dto.ModelCatalogEndpoint) string {
	for _, endpoint := range endpoints {
		if endpoint.Type == endpointType {
			return endpoint.Path
		}
	}
	return defaultCatalogPath(endpointType, "")
}

func bestModelDescription(hasPricing bool, pricing model.Pricing) string {
	if !hasPricing {
		return ""
	}
	for _, value := range []string{pricing.DescriptionEn, pricing.Description, pricing.DescriptionZh} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func splitModelTags(tags string) []string {
	if strings.TrimSpace(tags) == "" {
		return nil
	}
	parts := strings.Split(tags, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		result = appendUniqueStrings(result, part)
	}
	return result
}

func appendUniqueStrings(items []string, values ...string) []string {
	for _, value := range values {
		if strings.TrimSpace(value) == "" || stringSliceContains(items, value) {
			continue
		}
		items = append(items, value)
	}
	return items
}

func stringSliceContains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func recommendedMaxTokens(maxOutputTokens int64, fallback int64) int64 {
	if maxOutputTokens <= 0 {
		return fallback
	}
	if maxOutputTokens < fallback {
		return maxOutputTokens
	}
	return fallback
}

func catalogCapabilities(capabilities model.ModelCapabilities) dto.ModelCatalogCapabilities {
	return dto.ModelCatalogCapabilities{
		SupportsFunctionCalling:         capabilities.SupportsFunctionCalling,
		SupportsParallelFunctionCalling: capabilities.SupportsParallelFunctionCalling,
		SupportsVision:                  capabilities.SupportsVision,
		SupportsReasoning:               capabilities.SupportsReasoning,
		SupportsResponseSchema:          capabilities.SupportsResponseSchema,
		SupportsPromptCaching:           capabilities.SupportsPromptCaching,
		SupportsAudioInput:              capabilities.SupportsAudioInput,
		SupportsAudioOutput:             capabilities.SupportsAudioOutput,
		SupportsWebSearch:               capabilities.SupportsWebSearch,
		SupportsPDFInput:                capabilities.SupportsPDFInput,
		SupportsComputerUse:             capabilities.SupportsComputerUse,
	}
}

func buildSupportedParameters(capabilities model.ModelCapabilities) []string {
	params := make([]string, 0, 8)
	if capabilities.SupportsFunctionCalling {
		params = append(params, "tools", "tool_choice")
	}
	if capabilities.SupportsParallelFunctionCalling {
		params = append(params, "parallel_tool_calls")
	}
	if capabilities.SupportsReasoning {
		params = append(params, "reasoning_effort")
	}
	if capabilities.SupportsResponseSchema {
		params = append(params, "response_format")
	}
	if capabilities.SupportsWebSearch {
		params = append(params, "web_search_options")
	}
	if capabilities.SupportsPromptCaching {
		params = append(params, "cache_control")
	}
	return params
}

func modelFamily(modelName string) string {
	trimmed := strings.TrimSpace(modelName)
	if trimmed == "" {
		return ""
	}
	if idx := strings.Index(trimmed, "/"); idx >= 0 && idx+1 < len(trimmed) {
		trimmed = trimmed[idx+1:]
	}
	for _, sep := range []string{"-", ":"} {
		if idx := strings.Index(trimmed, sep); idx > 0 {
			return trimmed[:idx]
		}
	}
	return trimmed
}

func loadCatalogMetadataByModel(modelNames []string) map[string]model.ModelMetadata {
	result := make(map[string]model.ModelMetadata)
	if len(modelNames) == 0 || model.CoreDB() == nil || !model.CoreDB().Migrator().HasTable(&model.ModelMetadata{}) {
		return result
	}
	var records []model.ModelMetadata
	if err := model.CoreDB().Where("model_name IN ?", modelNames).Find(&records).Error; err != nil {
		return result
	}
	for _, record := range records {
		result[record.ModelName] = record
	}
	return result
}

type catalogHealthChannel struct {
	Model        string
	ChannelID    int
	Status       int
	ResponseTime int
}

func loadCatalogHealthByModel(modelNames []string) map[string]dto.ModelCatalogHealth {
	result := make(map[string]dto.ModelCatalogHealth, len(modelNames))
	if len(modelNames) == 0 {
		return result
	}
	for _, modelName := range modelNames {
		result[modelName] = dto.ModelCatalogHealth{
			Status:         "unavailable",
			ObservedWindow: "runtime",
		}
	}
	if model.CoreDB() == nil || !model.CoreDB().Migrator().HasTable(&model.Ability{}) || !model.CoreDB().Migrator().HasTable(&model.Channel{}) {
		return result
	}

	var rows []catalogHealthChannel
	if err := model.CoreDB().Table("abilities").
		Select("abilities.model as model, channels.id as channel_id, channels.status as status, channels.response_time as response_time").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where("abilities.model IN ? AND abilities.enabled = ?", modelNames, true).
		Scan(&rows).Error; err != nil {
		return result
	}

	byModel := make(map[string][]catalogHealthChannel, len(modelNames))
	seenByModel := make(map[string]map[int]struct{}, len(modelNames))
	for _, row := range rows {
		if _, ok := seenByModel[row.Model]; !ok {
			seenByModel[row.Model] = make(map[int]struct{})
		}
		if _, ok := seenByModel[row.Model][row.ChannelID]; ok {
			continue
		}
		seenByModel[row.Model][row.ChannelID] = struct{}{}
		byModel[row.Model] = append(byModel[row.Model], row)
	}
	for _, modelName := range modelNames {
		result[modelName] = buildCatalogHealth(byModel[modelName])
	}
	return result
}

func buildCatalogHealth(rows []catalogHealthChannel) dto.ModelCatalogHealth {
	health := dto.ModelCatalogHealth{
		Status:         "unavailable",
		ObservedWindow: "runtime",
	}
	if len(rows) == 0 {
		return health
	}

	latencies := make([]int, 0, len(rows))
	availabilitySum := 0.0
	availabilitySeen := 0
	availableCount := 0
	degradedCount := 0

	for _, row := range rows {
		if row.Status != common.ChannelStatusEnabled {
			availabilitySum += 0
			availabilitySeen++
			continue
		}

		snapshot := metrics.Default.Snapshot(row.ChannelID)
		channelAvailable := !snapshot.InCooldown() && snapshot.BreakerState != metrics.Open
		if snapshot.ErrorWindowSize > 0 && snapshot.ErrorRate >= 1 {
			channelAvailable = false
		}
		if channelAvailable {
			availableCount++
		}
		if snapshot.InCooldown() || snapshot.BreakerState != metrics.Closed || snapshot.ErrorRate >= 0.2 {
			degradedCount++
		}

		availability := 1.0
		if snapshot.ErrorWindowSize > 0 {
			availability = 1 - snapshot.ErrorRate
			if availability < 0 {
				availability = 0
			}
		}
		availabilitySum += availability
		availabilitySeen++

		if snapshot.SamplesCount > 0 && snapshot.AvgLatencyMs > 0 {
			latencies = append(latencies, int(snapshot.AvgLatencyMs+0.5))
		} else if row.ResponseTime > 0 {
			latencies = append(latencies, row.ResponseTime)
		}
	}

	if availabilitySeen > 0 {
		health.Availability = float64Ptr(availabilitySum / float64(availabilitySeen))
	}
	if len(latencies) > 0 {
		sort.Ints(latencies)
		health.LatencyP50Ms = intValuePtr(percentileInt(latencies, 0.50))
		health.LatencyP95Ms = intValuePtr(percentileInt(latencies, 0.95))
	}
	if availableCount > 0 {
		if degradedCount > 0 || availableCount < len(rows) {
			health.Status = "degraded"
		} else {
			health.Status = "available"
		}
	}
	return health
}

func percentileInt(values []int, percentile float64) int {
	if len(values) == 0 {
		return 0
	}
	if percentile <= 0 {
		return values[0]
	}
	index := int(float64(len(values))*percentile+0.999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func intValuePtr(value int) *int {
	return &value
}

func float64Ptr(value float64) *float64 {
	return &value
}

// sortCatalogItems puts catalog items in the one display order: the
// operator's maker order, newest release first (model.CompareModelOrder).
func sortCatalogItems(items []dto.ModelCatalogItem) {
	model.SortByModelOrder(items, func(item dto.ModelCatalogItem) model.ModelOrderKey {
		return model.ModelOrderKey{Name: item.ID, Maker: item.Maker, ReleaseDate: item.ReleaseDate}
	})
}

// buildCatalogPlanPricing is a lane's price as this viewer pays it: the
// standard price, or under a price plan min(standard, official × (1 − pct))
// per field — the same rule billing.ApplyPlanDiscount charges by. A field
// with no official price takes the pct off the standard price.
func buildCatalogPlanPricing(hasPricing bool, pricing model.Pricing, planID int, lane string) dto.ModelCatalogPricing {
	result := buildCatalogPricingWithAdjustment(hasPricing, pricing, 1)
	if !hasPricing {
		return result
	}
	plan, ok := model.ResolvePlanDiscount(planID, lane, pricing.ModelName)
	if !ok {
		return result
	}
	return catalogPlanPrices(pricing, officialPriceForCatalog(pricing.ModelName), plan.Pct.InexactFloat64(), 1)
}

func officialPriceForCatalog(name string) model.ModelOfficialPrice {
	official, _ := model.GetModelOfficialPrice(name)
	return official
}

// Both the lane and the lab can change prices at different input lengths.
// Publish every boundary, plus a separately computed peak schedule: multiplying
// an already-discounted price would stack the peak multiplier on the contract.
func catalogPlanPrices(pricing model.Pricing, official model.ModelOfficialPrice, pct, factor float64) dto.ModelCatalogPricing {
	project := func(input int64) dto.ModelCatalogPricing {
		p := pricing
		laneTier := p.ContextTier != nil && input > p.ContextTier.Threshold
		pick := func(v, base *float64) *float64 {
			if v != nil {
				return v
			}
			return base
		}
		if laneTier {
			tier := p.ContextTier
			p.InputPerMtok = pick(tier.InputPerMtok, p.InputPerMtok)
			p.OutputPerMtok = pick(tier.OutputPerMtok, p.OutputPerMtok)
			p.CacheReadPerMtok = pick(tier.CacheReadPerMtok, p.CacheReadPerMtok)
			p.CacheWrite5mPerMtok = pick(tier.CacheWrite5mPerMtok, p.CacheWrite5mPerMtok)
			p.CacheWrite1hPerMtok = pick(tier.CacheWrite1hPerMtok, p.CacheWrite1hPerMtok)
		}
		p.ContextTier = nil
		lab := official
		if tier := official.ContextTier; tier != nil && tier.Threshold > 0 {
			if input > tier.Threshold {
				value := func(v *float64, base float64) float64 {
					if v != nil {
						return *v
					}
					return base
				}
				lab.InputPerToken = value(tier.InputPerMtok, official.InputPerMtok()) / 1e6
				lab.OutputPerToken = value(tier.OutputPerMtok, official.OutputPerMtok()) / 1e6
				lab.CacheReadPerMtok = value(tier.CacheReadPerMtok, official.CacheReadPerMtok)
				lab.CacheWritePerMtok = value(tier.CacheWrite5mPerMtok, official.CacheWritePerMtok)
			}
		} else if laneTier {
			lab = model.ModelOfficialPrice{}
		}
		return applyCatalogPlanDiscount(buildCatalogPricingWithAdjustment(true, p, factor), p, lab, 1-pct/100)
	}
	result := project(0)
	thresholds := map[int64]bool{}
	for _, tier := range []*model.ContextTierPrice{pricing.ContextTier, official.ContextTier} {
		if tier != nil && tier.Threshold > 0 {
			thresholds[tier.Threshold] = true
		}
	}
	sorted := make([]int64, 0, len(thresholds))
	for threshold := range thresholds {
		sorted = append(sorted, threshold)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, threshold := range sorted {
		p := project(threshold + 1)
		result.ContextTiers = append(result.ContextTiers, dto.ModelCatalogContextTier{Threshold: threshold, Input: p.Input, Output: p.Output, CacheRead: p.CacheRead, CacheWrite5m: p.CacheWrite5m, CacheWrite1h: p.CacheWrite1h})
	}
	if len(result.ContextTiers) > 0 {
		result.ContextTier = &result.ContextTiers[0]
	}
	if factor == 1 && pricing.PeakMultiplier != nil && *pricing.PeakMultiplier > 0 && *pricing.PeakMultiplier != 1 {
		peak := catalogPlanPrices(pricing, official, pct, *pricing.PeakMultiplier)
		result.PeakPricing = &peak
	}
	return result
}

func applyCatalogPlanDiscount(result dto.ModelCatalogPricing, _ model.Pricing, official model.ModelOfficialPrice, keep float64) dto.ModelCatalogPricing {
	pct := decimal.NewFromFloat(1 - keep).Mul(decimal.NewFromInt(100))
	anchor := func(value *float64, officialPerMtok float64) *float64 {
		if value == nil {
			return nil
		}
		base := *value
		if officialPerMtok > 0 {
			base = officialPerMtok
		}
		price := billing.PlanDiscountPrice(decimal.NewFromFloat(*value), decimal.NewFromFloat(base), pct).InexactFloat64()
		return &price
	}
	result.Input = anchor(result.Input, official.InputPerMtok())
	result.InputPerMillion = anchor(result.InputPerMillion, official.InputPerMtok())
	result.Output = anchor(result.Output, official.OutputPerMtok())
	result.OutputPerMillion = anchor(result.OutputPerMillion, official.OutputPerMtok())
	result.CacheRead = anchor(result.CacheRead, official.CacheReadPerMtok)
	result.CacheReadPerMillion = anchor(result.CacheReadPerMillion, official.CacheReadPerMtok)
	result.CacheWrite5m = anchor(result.CacheWrite5m, official.CacheWritePerMtok)
	result.CacheWrite5mPerMillion = anchor(result.CacheWrite5mPerMillion, official.CacheWritePerMtok)
	for _, field := range []**float64{&result.CacheWrite1h, &result.CacheWrite1hPerMillion, &result.AudioInput, &result.AudioOutput, &result.ImageInput, &result.ImageOutput, &result.Request, &result.RequestPrice, &result.Second, &result.SecondPrice} {
		*field = anchor(*field, 0)
	}
	if result.ImagePricing != nil {
		factor := anchor(float64Ptr(1), 0)
		result.ImagePricing = result.ImagePricing.Scaled(*factor)
	}
	if result.VideoPricing != nil {
		factor := anchor(float64Ptr(1), 0)
		result.VideoPricing = result.VideoPricing.Scaled(*factor)
	}
	return result
}
