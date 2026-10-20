package model

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lazu-ai/lazu/pkg/billing"
	"sort"
	"strings"

	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/types"
)

type Pricing struct {
	ImagePricing              *billing.ImagePrices           `json:"image_pricing,omitempty"`
	VideoPricing              *billing.VideoPrices           `json:"video_pricing,omitempty"`
	VideoCapabilities         *dto.VideoCapabilities         `json:"video_capabilities,omitempty"`
	ModelName                 string                         `json:"model_name"`
	Description               string                         `json:"description,omitempty"`
	DescriptionEn             string                         `json:"description_en,omitempty"`
	DescriptionZh             string                         `json:"description_zh,omitempty"`
	Icon                      string                         `json:"icon,omitempty"`
	Tags                      string                         `json:"tags,omitempty"`
	Family                    string                         `json:"family,omitempty"`
	Capabilities              ModelCapabilities              `json:"capabilities"`
	ContextLength             int64                          `json:"context_length,omitempty"`
	MaxOutputTokens           int64                          `json:"max_output_tokens,omitempty"`
	OfficialInputPrice        float64                        `json:"official_input_price,omitempty"`
	OfficialOutputPrice       float64                        `json:"official_output_price,omitempty"`
	OfficialCacheReadPerMtok  float64                        `json:"official_cache_read_per_mtok,omitempty"`
	OfficialCacheWritePerMtok float64                        `json:"official_cache_write_per_mtok,omitempty"`
	OfficialContextTier       *ContextTierPrice              `json:"official_context_tier,omitempty"`
	DeprecationDate           string                         `json:"deprecation_date,omitempty"`
	VendorID                  string                         `json:"vendor_id,omitempty"`
	InternalVendorID          int                            `json:"-"`
	QuotaType                 int                            `json:"quota_type"`
	InputPerMtok              *float64                       `json:"input_per_mtok,omitempty"`
	OutputPerMtok             *float64                       `json:"output_per_mtok,omitempty"`
	CacheReadPerMtok          *float64                       `json:"cache_read_per_mtok,omitempty"`
	CacheWrite5mPerMtok       *float64                       `json:"cache_write_5m_per_mtok,omitempty"`
	CacheWrite1hPerMtok       *float64                       `json:"cache_write_1h_per_mtok,omitempty"`
	AudioInputPerMtok         *float64                       `json:"audio_input_per_mtok,omitempty"`
	AudioOutputPerMtok        *float64                       `json:"audio_output_per_mtok,omitempty"`
	ImageInputPerMtok         *float64                       `json:"image_input_per_mtok,omitempty"`
	ImageOutputPerMtok        *float64                       `json:"image_output_per_mtok,omitempty"`
	RequestUSD                *float64                       `json:"request_usd,omitempty"`
	SecondUSD                 *float64                       `json:"second_usd,omitempty"`
	PeakHoursUTC              string                         `json:"peak_hours_utc,omitempty"`
	PeakMultiplier            *float64                       `json:"peak_multiplier,omitempty"`
	PricingVersion            int64                          `json:"pricing_version,omitempty"`
	HasPrice                  bool                           `json:"has_price"`
	PricingStatus             string                         `json:"pricing_status,omitempty"`
	PricingSource             string                         `json:"pricing_source,omitempty"`
	OwnerBy                   string                         `json:"owner_by"`
	BrandIcon                 string                         `json:"brand_icon,omitempty"`
	EnableGroup               []string                       `json:"-"`
	SupportedEndpointTypes    []constant.EndpointType        `json:"supported_endpoint_types"`
	SupportedEndpoints        map[string]common.EndpointInfo `json:"supported_endpoints,omitempty"`
	CreatedAt                 int64                          `json:"created_at,omitempty"`
	ReleasedAt                *int64                         `json:"released_at,omitempty"`
	OpenSource                bool                           `json:"open_source"`
	ModalityInput             []string                       `json:"modality_input,omitempty"`
	ModalityOutput            []string                       `json:"modality_output,omitempty"`
	Lanes                     []PricingLane                  `json:"lanes,omitempty"`
	ContextTier               *ContextTierPrice              `json:"context_tier,omitempty"`

	// Maker is the lab behind the model and MakerRank its place in the
	// operator's order; rows arrive already sorted by CompareModelOrder.
	Maker             string                 `json:"maker,omitempty"`
	MakerRank         int                    `json:"maker_rank"`
	ReleaseDate       string                 `json:"release_date,omitempty"`
	IsNewRelease      bool                   `json:"is_new_release,omitempty"`
	KnowledgeCutoff   string                 `json:"knowledge_cutoff,omitempty"`
	InputLimit        int64                  `json:"input_limit,omitempty"`
	ReasoningOptions  *ModelReasoningOptions `json:"reasoning_options,omitempty"`
	UnsupportedParams []string               `json:"unsupported_params,omitempty"`
}

// ContextTierPrice is a lane's long-context pricing as the catalog shows it.
type ContextTierPrice struct {
	Threshold           int64    `json:"threshold"`
	InputPerMtok        *float64 `json:"input_per_mtok,omitempty"`
	OutputPerMtok       *float64 `json:"output_per_mtok,omitempty"`
	CacheReadPerMtok    *float64 `json:"cache_read_per_mtok,omitempty"`
	CacheWrite5mPerMtok *float64 `json:"cache_write_5m_per_mtok,omitempty"`
	CacheWrite1hPerMtok *float64 `json:"cache_write_1h_per_mtok,omitempty"`
}

// ContextTier is the long-context pricing of a sell price, or nil.
func (price ModelSellPrice) ContextTier() *ContextTierPrice {
	if !price.HasContextTier() {
		return nil
	}
	return &ContextTierPrice{
		Threshold:           price.ContextTierThreshold,
		InputPerMtok:        price.ContextTierInputPerMtok,
		OutputPerMtok:       price.ContextTierOutputPerMtok,
		CacheReadPerMtok:    price.ContextTierCacheReadPerMtok,
		CacheWrite5mPerMtok: price.ContextTierCacheWrite5mPerMtok,
		CacheWrite1hPerMtok: price.ContextTierCacheWrite1hPerMtok,
	}
}

type PricingLane struct {
	ImagePricing        *billing.ImagePrices `json:"image_pricing,omitempty"`
	VideoPricing        *billing.VideoPrices `json:"video_pricing,omitempty"`
	ModelName           string               `json:"model_name"`
	ChannelGroup        string               `json:"channel_group"`
	InputPerMtok        *float64             `json:"input_per_mtok,omitempty"`
	OutputPerMtok       *float64             `json:"output_per_mtok,omitempty"`
	CacheReadPerMtok    *float64             `json:"cache_read_per_mtok,omitempty"`
	CacheWrite5mPerMtok *float64             `json:"cache_write_5m_per_mtok,omitempty"`
	CacheWrite1hPerMtok *float64             `json:"cache_write_1h_per_mtok,omitempty"`
	AudioInputPerMtok   *float64             `json:"audio_input_per_mtok,omitempty"`
	AudioOutputPerMtok  *float64             `json:"audio_output_per_mtok,omitempty"`
	ImageInputPerMtok   *float64             `json:"image_input_per_mtok,omitempty"`
	ImageOutputPerMtok  *float64             `json:"image_output_per_mtok,omitempty"`
	RequestUSD          *float64             `json:"request_usd,omitempty"`
	SecondUSD           *float64             `json:"second_usd,omitempty"`
	PeakHoursUTC        string               `json:"peak_hours_utc,omitempty"`
	PeakMultiplier      *float64             `json:"peak_multiplier,omitempty"`
	PricingVersion      int64                `json:"pricing_version,omitempty"`
	ContextTier         *ContextTierPrice    `json:"context_tier,omitempty"`
	// Available and ClientOnly describe what the lane can actually serve right
	// now, not what it costs. They are filled per request from the live channel
	// cache (GetLaneServiceability) rather than stored with the price, because a
	// lane can lose its last channel long before anyone edits a price row.
	Available  bool   `json:"available"`
	ClientOnly string `json:"client_only,omitempty"`
}

type PricingVendor struct {
	PublicID    string `json:"id"`
	InternalID  int    `json:"-"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

var (
	pricingMap           []Pricing
	vendorsList          []PricingVendor
	supportedEndpointMap map[string]common.EndpointInfo
	modelEndpointInfoMap map[string]map[string]common.EndpointInfo
	lastGetPricingTime   time.Time
	updatePricingLock    sync.Mutex

	// 缓存映射：模型名 -> 启用分组 / 计费类型
	modelEnableGroups     = make(map[string][]string)
	modelQuotaTypeMap     = make(map[string]int)
	modelEnableGroupsLock = sync.RWMutex{}

	modelVendorMap        = make(map[string]int)
	vendorDetailMap       = make(map[int]PricingVendor)
	modelOfficialPriceMap = make(map[string]ModelOfficialPrice)
	modelVendorLock       = sync.RWMutex{}
)

// ModelOfficialPrice is the vendor's published list price for a model: input
// and output in USD per token (either catalogue unit, see billing_quote.go),
// cache in USD per million tokens. Cache prices are zero when the lab
// publishes none.
type ModelOfficialPrice struct {
	InputPerToken     float64
	OutputPerToken    float64
	CacheReadPerMtok  float64
	CacheWritePerMtok float64
	// Catalogue zeroes are also the missing-value default. Only per-field
	// models.dev evidence lets control-plane following treat a zero as free.
	explicitZeroFields map[string]bool
	// ContextTier is the lab's long-context list price, per million tokens;
	// nil when the lab prices every length the same.
	ContextTier *ContextTierPrice
}

// OfficialPerTokenCeiling separates the catalogue's two units for official
// input and output prices. The column holds USD per token (LiteLLM's unit),
// but some rows were typed per million tokens; a per-token price of $0.001
// would be $1,000 per million, so anything at or above it can only be a
// per-million figure. ModelInfoPanel reads the column the same way.
const OfficialPerTokenCeiling = 0.001

// OfficialPerMtok reads an official input/output column as USD per million.
func OfficialPerMtok(price float64) float64 {
	if price <= 0 {
		return 0
	}
	if price < OfficialPerTokenCeiling {
		return price * 1e6
	}
	return price
}

// InputPerMtok is the official input price in USD per million tokens.
func (price ModelOfficialPrice) InputPerMtok() float64 { return OfficialPerMtok(price.InputPerToken) }

// OutputPerMtok is the official output price in USD per million tokens.
func (price ModelOfficialPrice) OutputPerMtok() float64 { return OfficialPerMtok(price.OutputPerToken) }

var (
	modelSupportEndpointTypes = make(map[string][]constant.EndpointType)
	modelSupportEndpointsLock = sync.RWMutex{}
)

func GetPricing() []Pricing {
	updatePricingLock.Lock()
	defer updatePricingLock.Unlock()
	if time.Since(lastGetPricingTime) > time.Minute || (len(pricingMap) == 0 && !apiControlPlaneReader()) {
		modelSupportEndpointsLock.Lock()
		updatePricing()
		modelSupportEndpointsLock.Unlock()
	}
	return pricingMap
}

func GetVendors() []PricingVendor {
	GetPricing()
	updatePricingLock.Lock()
	defer updatePricingLock.Unlock()
	return vendorsList
}

func GetModelSupportEndpointTypes(model string) []constant.EndpointType {
	if model == "" {
		return make([]constant.EndpointType, 0)
	}
	modelSupportEndpointsLock.RLock()
	defer modelSupportEndpointsLock.RUnlock()
	if endpoints, ok := modelSupportEndpointTypes[model]; ok {
		return endpoints
	}
	return make([]constant.EndpointType, 0)
}

// GetModelEndpointOverrideTypes returns only endpoint types explicitly declared
// by the model metadata. Unlike GetModelSupportEndpointTypes it does not include
// abilities from other channels that happen to serve the same model, so callers
// can safely combine it with one concrete candidate channel's protocol.
func GetModelEndpointOverrideTypes(modelName string) []constant.EndpointType {
	if strings.TrimSpace(modelName) == "" {
		return []constant.EndpointType{}
	}
	// Unit consumers may exercise protocol selection before a database is
	// configured. Production always has CoreDB() and refreshes metadata here.
	if CoreDB() != nil {
		GetPricing()
	}
	modelSupportEndpointsLock.RLock()
	defer modelSupportEndpointsLock.RUnlock()

	overrides := modelEndpointInfoMap[modelName]
	result := make([]constant.EndpointType, 0, len(overrides))
	for endpointType := range overrides {
		result = append(result, constant.EndpointType(endpointType))
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func updatePricing() {
	if apiControlPlaneReader() {
		logControlRefresh("catalog", refreshCatalogFromRedis())
		lastGetPricingTime = time.Now()
		return
	}
	logControlRefresh("catalog", updatePricingFromCore())
}

func updatePricingFromCore() error {
	revision, err := beginControlSnapshot(context.Background(), catalogSnapshotKey)
	if err != nil {
		return err
	}
	enableAbilities, err := GetAllEnableAbilityWithChannels()
	if err != nil {
		return fmt.Errorf("GetAllEnableAbilityWithChannels: %w", err)
	}
	// 预加载模型元数据与供应商一次，避免循环查询
	var allMeta []Model
	if err := CoreDB().Order("id ASC").Find(&allMeta).Error; err != nil {
		return err
	}
	catalogNames := make([]string, 0, len(enableAbilities))
	for _, ability := range enableAbilities {
		catalogNames = append(catalogNames, ability.Model)
	}
	for _, meta := range allMeta {
		if meta.NameRule == NameRuleExact {
			catalogNames = append(catalogNames, meta.ModelName)
		}
	}
	metaMap := resolveCatalogModelMetadata(allMeta, catalogNames)

	// 预加载供应商
	var vendors []Vendor
	if err := CoreDB().Find(&vendors).Error; err != nil {
		return err
	}
	vendorMap := make(map[int]*Vendor)
	for i := range vendors {
		vendorMap[vendors[i].Id] = &vendors[i]
	}

	// 预加载模型元数据（用于 ReleasedAt）
	var allModelMeta []ModelMetadata
	if err := CoreDB().Select("model_name, released_at").Find(&allModelMeta).Error; err != nil {
		return err
	}
	modelMetaMap := make(map[string]*ModelMetadata, len(allModelMeta))
	for i := range allModelMeta {
		modelMetaMap[allModelMeta[i].ModelName] = &allModelMeta[i]
	}

	// 初始化默认供应商映射
	initDefaultVendorMapping(metaMap, vendorMap, enableAbilities)
	sellPriceByModel := GetActiveModelSellPriceMap()
	sellPriceLanesByModel := GetActiveModelSellPriceLaneMap()

	// 构建对前端友好的供应商列表
	vendorDetails := make(map[int]PricingVendor)
	vendorsList = make([]PricingVendor, 0, len(vendorMap))
	for _, v := range vendorMap {
		info := PricingVendor{
			PublicID:    v.PublicID,
			InternalID:  v.Id,
			Name:        v.Name,
			Description: v.Description,
			Icon:        v.Icon,
		}
		vendorsList = append(vendorsList, info)
		vendorDetails[v.Id] = info
	}

	modelGroupsMap := make(map[string]*types.Set[string])

	for _, ability := range enableAbilities {
		groups, ok := modelGroupsMap[ability.Model]
		if !ok {
			groups = types.NewSet[string]()
			modelGroupsMap[ability.Model] = groups
		}
		groups.Add(ability.Group)
	}

	//这里使用切片而不是Set，因为一个模型可能支持多个端点类型，并且第一个端点是优先使用端点
	modelSupportEndpointsStr := make(map[string][]string)

	// 先根据已有能力填充原生端点
	for _, ability := range enableAbilities {
		endpoints := modelSupportEndpointsStr[ability.Model]
		channelTypes := common.GetEndpointTypesByChannelType(ability.ChannelType, ability.Model)
		for _, channelType := range channelTypes {
			if !common.StringsContains(endpoints, string(channelType)) {
				endpoints = append(endpoints, string(channelType))
			}
		}
		modelSupportEndpointsStr[ability.Model] = endpoints
	}

	// 再补充模型自定义端点
	modelEndpointInfoMap = make(map[string]map[string]common.EndpointInfo, len(metaMap))
	for modelName, meta := range metaMap {
		endpointOverrides := parseEndpointInfoOverrides(meta.Endpoints)
		if len(endpointOverrides) == 0 {
			continue
		}
		modelEndpointInfoMap[modelName] = endpointOverrides

		endpoints := modelSupportEndpointsStr[modelName]
		for endpointType := range endpointOverrides {
			if !common.StringsContains(endpoints, endpointType) {
				endpoints = append(endpoints, endpointType)
			}
		}
		modelSupportEndpointsStr[modelName] = endpoints
	}

	// A model with a video capability row is served by /v1/videos only: the
	// chat endpoints its channel type implies would be advertised (and shown
	// as the default example) but cannot generate a clip.
	for modelName, meta := range metaMap {
		if meta.VideoCapabilities != nil {
			modelSupportEndpointsStr[modelName] = []string{string(constant.EndpointTypeVideoGeneration)}
		}
	}

	modelSupportEndpointTypes = make(map[string][]constant.EndpointType)
	for model, endpoints := range modelSupportEndpointsStr {
		supportedEndpoints := make([]constant.EndpointType, 0)
		for _, endpointStr := range endpoints {
			endpointType := constant.EndpointType(endpointStr)
			supportedEndpoints = append(supportedEndpoints, endpointType)
		}
		modelSupportEndpointTypes[model] = supportedEndpoints
	}

	// 全局 fallback 只保留协议默认端点，模型级 override 仅通过
	// modelEndpointInfoMap 在按模型查询时生效，避免一个模型的自定义路径
	// 污染同协议下其他模型的 catalog 展示。
	supportedEndpointMap = buildSupportedEndpointMap(modelSupportEndpointTypes)

	setModelVideoCapabilities(collectModelVideoCapabilities(metaMap, modelSupportEndpointTypes))

	today := time.Now().Format("2006-01-02")
	pricingMap = make([]Pricing, 0)
	for model, groups := range modelGroupsMap {
		meta, hasMeta := metaMap[model]
		if !hasMeta || !meta.IsPubliclyListable() {
			continue
		}

		pricing := Pricing{
			ModelName:              model,
			EnableGroup:            groups.Items(),
			SupportedEndpointTypes: modelSupportEndpointTypes[model],
			SupportedEndpoints: buildModelSupportedEndpointMap(
				model,
				modelSupportEndpointTypes[model],
				supportedEndpointMap,
				modelEndpointInfoMap,
			),
		}

		// 补充模型元数据（描述、标签、供应商、状态）
		pricing.Description = meta.Description
		pricing.DescriptionEn = meta.DescriptionEn
		pricing.DescriptionZh = meta.DescriptionZh
		pricing.Icon = meta.Icon
		pricing.Tags = meta.Tags
		pricing.Family = meta.Family
		pricing.Capabilities = meta.Capabilities
		pricing.ContextLength = meta.ContextLength
		pricing.MaxOutputTokens = meta.MaxOutputTokens
		pricing.OfficialInputPrice = meta.OfficialInputPrice
		pricing.OfficialOutputPrice = meta.OfficialOutputPrice
		pricing.OfficialCacheReadPerMtok = meta.OfficialCacheReadPerMtok
		pricing.OfficialCacheWritePerMtok = meta.OfficialCacheWritePerMtok
		pricing.OfficialContextTier = meta.OfficialContextTier.Price()
		pricing.DeprecationDate = meta.DeprecationDate
		pricing.InternalVendorID = meta.VendorID
		pricing.VideoCapabilities = publicVideoCapabilities(model, modelSupportEndpointTypes[model])
		if vendor, ok := vendorMap[meta.VendorID]; ok {
			pricing.VendorID = vendor.PublicID
			pricing.OwnerBy = vendor.Name
			pricing.BrandIcon = vendor.Icon
		}
		if owner, icon, ok := InferCanonicalModelBrand(model); ok {
			pricing.OwnerBy = owner
			if icon != "" {
				pricing.BrandIcon = icon
			}
		}
		pricing.CreatedAt = meta.CreatedTime

		// Derive open_source and modality from model name and tags
		pricing.OpenSource = IsOpenSourceModel(model, pricing.Tags) || meta.OpenWeights
		modality := DeriveModality(pricing.Tags)
		pricing.ModalityInput = modality.Input
		pricing.ModalityOutput = modality.Output
		if !meta.Modalities.IsZero() {
			pricing.ModalityInput = meta.Modalities.Input
			pricing.ModalityOutput = meta.Modalities.Output
		}

		// 补充 ReleasedAt（来自 ModelMetadata）
		if mm, ok := modelMetaMap[model]; ok && mm.ReleasedAt != nil {
			ts := mm.ReleasedAt.Unix()
			pricing.ReleasedAt = &ts
		}
		vendorName := ""
		if vendor, ok := vendorMap[meta.VendorID]; ok {
			vendorName = vendor.Name
		}
		pricing.applyModelInfo(meta, vendorName, today)
		if sellPrice, ok := resolveModelSellPrice(model, sellPriceByModel); ok {
			pricing.applyModelSellPrice(sellPrice)
		}
		if lanes := resolveModelSellPriceLanes(model, sellPriceLanesByModel); len(lanes) > 0 {
			pricing.Lanes = lanes
		}
		pricing.updatePricingStatus()
		pricingMap = append(pricingMap, pricing)
	}

	// 刷新缓存映射，供高并发快速查询
	modelEnableGroupsLock.Lock()
	modelEnableGroups = make(map[string][]string)
	modelQuotaTypeMap = make(map[string]int)
	for _, p := range pricingMap {
		modelEnableGroups[p.ModelName] = p.EnableGroup
		modelQuotaTypeMap[p.ModelName] = p.QuotaType
	}
	modelEnableGroupsLock.Unlock()

	newModelVendorMap := make(map[string]int, len(pricingMap))
	newOfficialPriceMap := make(map[string]ModelOfficialPrice, len(pricingMap))
	for _, p := range pricingMap {
		newModelVendorMap[p.ModelName] = p.InternalVendorID
		if p.OfficialInputPrice > 0 || p.OfficialOutputPrice > 0 {
			newOfficialPriceMap[p.ModelName] = ModelOfficialPrice{
				InputPerToken:     p.OfficialInputPrice,
				OutputPerToken:    p.OfficialOutputPrice,
				CacheReadPerMtok:  p.OfficialCacheReadPerMtok,
				CacheWritePerMtok: p.OfficialCacheWritePerMtok,
				ContextTier:       p.OfficialContextTier,
			}
		}
	}
	modelVendorLock.Lock()
	modelVendorMap = newModelVendorMap
	modelOfficialPriceMap = newOfficialPriceMap
	vendorDetailMap = vendorDetails
	modelVendorLock.Unlock()

	SortByModelOrder(pricingMap, func(p Pricing) ModelOrderKey { return p.OrderKey() })
	declaredEfforts := make(map[string]ModelReasoningOptions, len(metaMap))
	for name, meta := range metaMap {
		if !meta.ReasoningOptions.IsZero() {
			declaredEfforts[name] = meta.ReasoningOptions
		}
	}
	setCatalogReasoningOptions(declaredEfforts)

	modelNames := make([]string, 0, len(pricingMap))
	for _, p := range pricingMap {
		modelNames = append(modelNames, p.ModelName)
	}
	if !apiControlPlaneReader() {
		_ = EnsureModelMetadataForNames(modelNames)
	}
	if CoreDB().Dialector.Name() == "postgres" {
		if err := publishCatalogSnapshot(declaredEfforts, revision); err != nil {
			return err
		}
	}

	lastGetPricingTime = time.Now()
	return nil
}

func (pricing *Pricing) applyModelSellPrice(sellPrice ModelSellPrice) {
	pricing.InputPerMtok = sellPrice.InputPerMtok
	pricing.OutputPerMtok = sellPrice.OutputPerMtok
	pricing.CacheReadPerMtok = sellPrice.CacheReadPerMtok
	pricing.CacheWrite5mPerMtok = sellPrice.CacheWrite5mPerMtok
	pricing.CacheWrite1hPerMtok = sellPrice.CacheWrite1hPerMtok
	pricing.AudioInputPerMtok = sellPrice.AudioInputPerMtok
	pricing.AudioOutputPerMtok = sellPrice.AudioOutputPerMtok
	pricing.ImageInputPerMtok = sellPrice.ImageInputPerMtok
	pricing.ImageOutputPerMtok = sellPrice.ImageOutputPerMtok
	pricing.ImagePricing = sellPrice.ImagePricing
	// The public copy: per-second prices, not the provider-cost multiplier.
	pricing.VideoPricing = sellPrice.VideoPricing.Public()
	pricing.RequestUSD = sellPrice.RequestUSD
	pricing.SecondUSD = sellPrice.SecondUSD
	pricing.PeakHoursUTC = sellPrice.PeakHoursUTC
	pricing.PeakMultiplier = sellPrice.PeakMultiplier
	pricing.PricingVersion = sellPrice.PricingVersion
	pricing.ContextTier = sellPrice.ContextTier()
	pricing.PricingSource = sellPrice.Source
	if (sellPrice.RequestUSD != nil || sellPrice.SecondUSD != nil || !sellPrice.VideoPricing.IsEmpty()) && sellPrice.InputPerMtok == nil {
		pricing.QuotaType = 1
	}
	if sellPrice.InputPerMtok != nil {
		pricing.QuotaType = 0
	}
}

func (pricing *Pricing) updatePricingStatus() {
	pricing.HasPrice = pricingHasConfiguredPrice(*pricing)
	if pricing.HasPrice {
		pricing.PricingStatus = "configured"
		if strings.TrimSpace(pricing.PricingSource) == "" {
			pricing.PricingSource = "model_sell_prices"
		}
		return
	}
	pricing.PricingStatus = "missing"
	pricing.PricingSource = ""
}

func pricingHasConfiguredPrice(pricing Pricing) bool {
	if pricing.InputPerMtok != nil ||
		pricing.OutputPerMtok != nil ||
		pricing.CacheReadPerMtok != nil ||
		pricing.CacheWrite5mPerMtok != nil ||
		pricing.CacheWrite1hPerMtok != nil ||
		pricing.AudioInputPerMtok != nil ||
		pricing.AudioOutputPerMtok != nil ||
		pricing.ImageInputPerMtok != nil ||
		pricing.ImageOutputPerMtok != nil ||
		pricing.RequestUSD != nil ||
		pricing.SecondUSD != nil ||
		!pricing.VideoPricing.IsEmpty() {
		return true
	}
	for _, lane := range pricing.Lanes {
		if pricingLaneHasConfiguredPrice(lane) {
			return true
		}
	}
	return false
}

func pricingLaneHasConfiguredPrice(lane PricingLane) bool {
	return lane.InputPerMtok != nil ||
		lane.OutputPerMtok != nil ||
		lane.CacheReadPerMtok != nil ||
		lane.CacheWrite5mPerMtok != nil ||
		lane.CacheWrite1hPerMtok != nil ||
		lane.AudioInputPerMtok != nil ||
		lane.AudioOutputPerMtok != nil ||
		lane.ImageInputPerMtok != nil ||
		lane.ImageOutputPerMtok != nil ||
		lane.RequestUSD != nil ||
		lane.SecondUSD != nil
}

func resolveModelSellPrice(modelName string, sellPriceByModel map[string]ModelSellPrice) (ModelSellPrice, bool) {
	if sellPrice, ok := sellPriceByModel[modelName]; ok {
		return sellPrice, true
	}
	if matched := NormalizePricingModelName(modelName); matched != "" && matched != modelName {
		if sellPrice, ok := sellPriceByModel[matched]; ok {
			return sellPrice, true
		}
	}
	return ModelSellPrice{}, false
}

func resolveModelSellPriceLanes(modelName string, sellPriceByModel map[string][]ModelSellPrice) []PricingLane {
	rows, ok := sellPriceByModel[modelName]
	if !ok {
		if matched := NormalizePricingModelName(modelName); matched != "" && matched != modelName {
			rows, ok = sellPriceByModel[matched]
		}
	}
	if !ok || len(rows) == 0 {
		return nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return channelLaneSortRank(rows[i].ChannelGroup) < channelLaneSortRank(rows[j].ChannelGroup)
	})
	lanes := make([]PricingLane, 0, len(rows))
	for _, row := range rows {
		lanes = append(lanes, modelSellPriceToPricingLane(row))
	}
	return lanes
}

func modelSellPriceToPricingLane(row ModelSellPrice) PricingLane {
	return PricingLane{
		ModelName:           row.ModelName,
		ChannelGroup:        NormalizeChannelLane(row.ChannelGroup),
		InputPerMtok:        row.InputPerMtok,
		OutputPerMtok:       row.OutputPerMtok,
		CacheReadPerMtok:    row.CacheReadPerMtok,
		CacheWrite5mPerMtok: row.CacheWrite5mPerMtok,
		CacheWrite1hPerMtok: row.CacheWrite1hPerMtok,
		AudioInputPerMtok:   row.AudioInputPerMtok,
		AudioOutputPerMtok:  row.AudioOutputPerMtok,
		ImageInputPerMtok:   row.ImageInputPerMtok,
		ImageOutputPerMtok:  row.ImageOutputPerMtok,
		ImagePricing:        row.ImagePricing,
		VideoPricing:        row.VideoPricing.Public(),
		RequestUSD:          row.RequestUSD,
		SecondUSD:           row.SecondUSD,
		PeakHoursUTC:        row.PeakHoursUTC,
		PeakMultiplier:      row.PeakMultiplier,
		PricingVersion:      row.PricingVersion,
		ContextTier:         row.ContextTier(),
	}
}

func channelLaneSortRank(group string) int {
	switch NormalizeChannelLane(group) {
	case ChannelLaneDirect:
		return 0
	case ChannelLaneAgent:
		return 1
	default:
		return 10
	}
}

// GetSupportedEndpointMap 返回全局端点到路径的映射
func GetSupportedEndpointMap() map[string]common.EndpointInfo {
	GetPricing()
	modelSupportEndpointsLock.RLock()
	defer modelSupportEndpointsLock.RUnlock()

	result := make(map[string]common.EndpointInfo, len(supportedEndpointMap))
	for endpointType, info := range supportedEndpointMap {
		result[endpointType] = info
	}
	return result
}

// GetModelEndpointInfo returns the most specific endpoint info for a model/type pair.
func GetModelEndpointInfo(modelName string, endpointType constant.EndpointType) (common.EndpointInfo, bool) {
	GetPricing()
	modelSupportEndpointsLock.RLock()
	defer modelSupportEndpointsLock.RUnlock()

	if endpointOverrides, ok := modelEndpointInfoMap[modelName]; ok {
		if info, ok := endpointOverrides[string(endpointType)]; ok {
			return info, true
		}
	}
	info, ok := supportedEndpointMap[string(endpointType)]
	return info, ok
}

// GetModelVendorMeta returns the vendor ID and name for the given model if known.
func GetModelVendorMeta(modelName string) (int, string) {
	if strings.TrimSpace(modelName) == "" {
		return 0, ""
	}
	GetPricing()
	modelVendorLock.RLock()
	defer modelVendorLock.RUnlock()
	if vendorID, ok := modelVendorMap[modelName]; ok {
		vendorName := ""
		if vendor, exists := vendorDetailMap[vendorID]; exists {
			vendorName = vendor.Name
		}
		return vendorID, vendorName
	}
	return 0, ""
}

// GetModelOfficialPrice returns the model's catalogued official list price.
func GetModelOfficialPrice(modelName string) (ModelOfficialPrice, bool) {
	if strings.TrimSpace(modelName) == "" || CoreDB() == nil {
		return ModelOfficialPrice{}, false
	}
	GetPricing()
	modelVendorLock.RLock()
	defer modelVendorLock.RUnlock()
	price, ok := modelOfficialPriceMap[modelName]
	return price, ok
}

func GetModelVendorPublicMeta(modelName string) (int, string, string) {
	if strings.TrimSpace(modelName) == "" {
		return 0, "", ""
	}
	GetPricing()
	modelVendorLock.RLock()
	defer modelVendorLock.RUnlock()
	if vendorID, ok := modelVendorMap[modelName]; ok {
		if vendor, exists := vendorDetailMap[vendorID]; exists {
			return vendorID, vendor.PublicID, vendor.Name
		}
		return vendorID, "", ""
	}
	return 0, "", ""
}

func parseEndpointInfoOverrides(rawEndpoints string) map[string]common.EndpointInfo {
	if strings.TrimSpace(rawEndpoints) == "" {
		return nil
	}

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(rawEndpoints), &raw); err != nil {
		return nil
	}

	result := make(map[string]common.EndpointInfo)
	for endpointType, value := range raw {
		switch val := value.(type) {
		case string:
			if strings.TrimSpace(val) == "" {
				continue
			}
			result[endpointType] = common.EndpointInfo{
				Path:   val,
				Method: "POST",
			}
		case map[string]interface{}:
			endpointInfo := common.EndpointInfo{Method: "POST"}
			if path, ok := val["path"].(string); ok {
				endpointInfo.Path = path
			}
			if method, ok := val["method"].(string); ok && strings.TrimSpace(method) != "" {
				endpointInfo.Method = strings.ToUpper(method)
			}
			if strings.TrimSpace(endpointInfo.Path) == "" {
				continue
			}
			result[endpointType] = endpointInfo
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

func buildSupportedEndpointMap(
	supportedTypes map[string][]constant.EndpointType,
) map[string]common.EndpointInfo {
	result := make(map[string]common.EndpointInfo)
	for _, endpoints := range supportedTypes {
		for _, endpointType := range endpoints {
			if _, exists := result[string(endpointType)]; exists {
				continue
			}
			if info, ok := common.GetDefaultEndpointInfo(endpointType); ok {
				result[string(endpointType)] = info
			}
		}
	}
	return result
}

func buildModelSupportedEndpointMap(
	modelName string,
	endpointTypes []constant.EndpointType,
	defaultEndpoints map[string]common.EndpointInfo,
	modelOverrides map[string]map[string]common.EndpointInfo,
) map[string]common.EndpointInfo {
	if len(endpointTypes) == 0 {
		return nil
	}

	result := make(map[string]common.EndpointInfo, len(endpointTypes))
	overrideMap := modelOverrides[modelName]
	for _, endpointType := range endpointTypes {
		if info, ok := overrideMap[string(endpointType)]; ok {
			result[string(endpointType)] = info
			continue
		}
		if info, ok := defaultEndpoints[string(endpointType)]; ok {
			result[string(endpointType)] = info
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// applyModelInfo copies the model facts the catalog shows. A model's own
// release date wins over the legacy model_metadata timestamp.
func (pricing *Pricing) applyModelInfo(meta *Model, vendorName string, today string) {
	pricing.Maker = ResolveModelMaker(meta.Maker, pricing.ModelName, vendorName)
	pricing.MakerRank = ModelMakerRank(pricing.Maker)
	pricing.ReleaseDate = meta.ConfirmedReleaseDate()
	pricing.ReleasedAt = nil
	if release, err := parseISODate(pricing.ReleaseDate); err == nil && pricing.ReleaseDate != "" {
		ts := release.Unix()
		pricing.ReleasedAt = &ts
	}
	pricing.IsNewRelease = ModelIsNewRelease(pricing.ReleaseDate, today)
	pricing.KnowledgeCutoff = meta.KnowledgeCutoff
	pricing.InputLimit = meta.InputLimit
	if !meta.ReasoningOptions.IsZero() {
		options := meta.ReasoningOptions
		pricing.ReasoningOptions = &options
	}
	if len(meta.UnsupportedParams) > 0 {
		pricing.UnsupportedParams = append([]string(nil), meta.UnsupportedParams...)
	}
}

// OrderKey is the display-order key of a catalog row.
func (pricing Pricing) OrderKey() ModelOrderKey {
	return ModelOrderKey{Name: pricing.ModelName, Maker: pricing.Maker, ReleaseDate: pricing.ReleaseDate}
}
