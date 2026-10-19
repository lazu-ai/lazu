package model

import (
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"

	"gorm.io/gorm"
)

const (
	NameRuleExact = iota
	NameRulePrefix
	NameRuleContains
	NameRuleSuffix
)

const DefaultModelSort = "created_desc"

func modelSortOrder(sortBy string) string {
	switch sortBy {
	case "name_asc":
		return "models.model_name ASC, models.id DESC"
	case "name_desc":
		return "models.model_name DESC, models.id DESC"
	case "created_asc":
		return "models.created_time ASC, models.id ASC"
	case "updated_asc":
		return "models.updated_time ASC, models.id ASC"
	case "updated_desc":
		return "models.updated_time DESC, models.id DESC"
	case "status_asc":
		return "models.status ASC, models.updated_time DESC, models.id DESC"
	case "status_desc":
		return "models.status DESC, models.updated_time DESC, models.id DESC"
	default:
		return "models.created_time DESC, models.id DESC"
	}
}

type BoundChannel struct {
	Name string `json:"name"`
	Type int    `json:"type"`
}

type Model struct {
	Id                  int               `json:"id"`
	ModelName           string            `json:"model_name" gorm:"size:128;not null;uniqueIndex:uk_model_name_delete_at,priority:1"`
	Description         string            `json:"description,omitempty" gorm:"type:text"`
	DescriptionEn       string            `json:"description_en,omitempty" gorm:"type:text"`
	DescriptionZh       string            `json:"description_zh,omitempty" gorm:"type:text"`
	Icon                string            `json:"icon,omitempty" gorm:"type:varchar(128)"`
	Tags                string            `json:"tags,omitempty" gorm:"type:varchar(255)"`
	Family              string            `json:"family,omitempty" gorm:"type:varchar(64);not null;default:'';index"`
	Capabilities        ModelCapabilities `json:"capabilities" gorm:"type:json"`
	ContextLength       int64             `json:"context_length,omitempty" gorm:"default:0"`
	MaxOutputTokens     int64             `json:"max_output_tokens,omitempty" gorm:"default:0"`
	OfficialInputPrice  float64           `json:"official_input_price,omitempty" gorm:"type:decimal(20,10);default:0"`
	OfficialOutputPrice float64           `json:"official_output_price,omitempty" gorm:"type:decimal(20,10);default:0"`
	// Official cache prices, USD per million tokens (per token would not fit
	// decimal(20,10): DeepSeek's $0.003625/M read is 3.625e-9), written only
	// by the models.dev sync (see ModelInfoColumns).
	OfficialCacheReadPerMtok  float64 `json:"official_cache_read_per_mtok,omitempty" gorm:"type:decimal(20,10);default:0"`
	OfficialCacheWritePerMtok float64 `json:"official_cache_write_per_mtok,omitempty" gorm:"type:decimal(20,10);default:0"`
	// OfficialContextTier is the lab's long-context list price (USD per
	// million tokens past the threshold), also written only by the sync.
	OfficialContextTier ModelContextTier `json:"official_context_tier,omitempty" gorm:"type:text"`
	DeprecationDate     string           `json:"deprecation_date,omitempty" gorm:"type:varchar(32)"`
	VendorID            int              `json:"vendor_id,omitempty" gorm:"index"`
	Endpoints           string           `json:"endpoints,omitempty" gorm:"type:text"`
	Status              int              `json:"status" gorm:"default:1"`
	LifecycleStatus     string           `json:"lifecycle_status" gorm:"type:varchar(32);not null;default:'active';index"`
	ReplacementModel    string           `json:"replacement_model,omitempty" gorm:"type:varchar(128)"`
	RetireAfter         int64            `json:"retire_after,omitempty" gorm:"bigint;default:0"`
	Source              string           `json:"source,omitempty" gorm:"type:varchar(64);not null;default:'manual'"`
	LastSeenAt          int64            `json:"last_seen_at,omitempty" gorm:"bigint;default:0"`
	SyncOfficial        int              `json:"sync_official" gorm:"default:1"`
	CreatedTime         int64            `json:"created_time" gorm:"bigint"`
	UpdatedTime         int64            `json:"updated_time" gorm:"bigint"`
	DeletedAt           gorm.DeletedAt   `json:"-" gorm:"index;uniqueIndex:uk_model_name_delete_at,priority:2"`

	// Model facts, mostly filled from models.dev (see model_info.go).
	Maker             string                `json:"maker" gorm:"type:varchar(32);not null;default:''"`
	ReleaseDate       string                `json:"release_date" gorm:"type:varchar(10);not null;default:''"`
	KnowledgeCutoff   string                `json:"knowledge_cutoff" gorm:"type:varchar(10);not null;default:''"`
	InputLimit        int64                 `json:"input_limit" gorm:"default:0"`
	Modalities        ModelModalities       `json:"modalities" gorm:"type:text"`
	ReasoningOptions  ModelReasoningOptions `json:"reasoning_options" gorm:"type:text"`
	UnsupportedParams ModelStringList       `json:"unsupported_params" gorm:"type:text"`
	// VideoCapabilities is the operator's row of what a video model takes
	// (durations, resolutions, ratios, inputs, passthrough). Nil falls back
	// to the family defaults; see ModelVideoCapabilities.
	VideoCapabilities *dto.VideoCapabilities `json:"video_capabilities,omitempty" gorm:"type:text;serializer:json"`
	OpenWeights       bool                   `json:"open_weights" gorm:"default:false"`
	ModelsDevID       string                 `json:"models_dev_id" gorm:"type:varchar(160);not null;default:''"`
	InfoSources       ModelInfoSources       `json:"info_sources" gorm:"type:text"`
	InfoLocked        bool                   `json:"info_locked" gorm:"default:false"`

	BoundChannels       []BoundChannel `json:"bound_channels,omitempty" gorm:"-"`
	EnableGroups        []string       `json:"enable_groups,omitempty" gorm:"-"`
	QuotaTypes          []int          `json:"quota_types,omitempty" gorm:"-"`
	QuotaType           *int           `json:"quota_type,omitempty" gorm:"-"`
	InputPerMtok        *float64       `json:"input_per_mtok,omitempty" gorm:"-"`
	OutputPerMtok       *float64       `json:"output_per_mtok,omitempty" gorm:"-"`
	CacheReadPerMtok    *float64       `json:"cache_read_per_mtok,omitempty" gorm:"-"`
	CacheWrite5mPerMtok *float64       `json:"cache_write_5m_per_mtok,omitempty" gorm:"-"`
	CacheWrite1hPerMtok *float64       `json:"cache_write_1h_per_mtok,omitempty" gorm:"-"`
	AudioInputPerMtok   *float64       `json:"audio_input_per_mtok,omitempty" gorm:"-"`
	AudioOutputPerMtok  *float64       `json:"audio_output_per_mtok,omitempty" gorm:"-"`
	ImageInputPerMtok   *float64       `json:"image_input_per_mtok,omitempty" gorm:"-"`
	ImageOutputPerMtok  *float64       `json:"image_output_per_mtok,omitempty" gorm:"-"`
	RequestUSD          *float64       `json:"request_usd,omitempty" gorm:"-"`
	SecondUSD           *float64       `json:"second_usd,omitempty" gorm:"-"`
	PricingVersion      int64          `json:"pricing_version,omitempty" gorm:"-"`
	Lanes               []PricingLane  `json:"lanes,omitempty" gorm:"-"`
	NameRule            int            `json:"name_rule" gorm:"default:0"`

	// MakerResolved is the maker the catalog groups this model under — the
	// stored maker, else inferred from the name or vendor.
	MakerResolved string `json:"maker_resolved,omitempty" gorm:"-"`

	MatchedModels []string `json:"matched_models,omitempty" gorm:"-"`
	MatchedCount  int      `json:"matched_count,omitempty" gorm:"-"`
}

func (mi *Model) BeforeSave(tx *gorm.DB) error {
	mi.normalizeLifecycleForSave()
	return nil
}

func (mi *Model) normalizeLifecycleForSave() {
	mi.ModelName = strings.TrimSpace(mi.ModelName)
	mi.Family = strings.ToLower(strings.TrimSpace(mi.Family))
	if mi.Family == "" {
		mi.Family = InferModelFamily(mi.ModelName)
	}
	mi.LifecycleStatus = NormalizeModelLifecycleStatus(mi.LifecycleStatus)
	if mi.Source = strings.TrimSpace(mi.Source); mi.Source == "" {
		mi.Source = ModelSourceManual
	}
	if IsRetiredModelName(mi.ModelName) && mi.LifecycleStatus != ModelLifecycleRetired {
		mi.LifecycleStatus = ModelLifecycleRetired
		if mi.RetireAfter == 0 {
			mi.RetireAfter = common.GetTimestamp()
		}
	}
}

// InferModelFamily is used only when persisting/backfilling legacy metadata.
// Data-plane planning reads the stored Family value and never infers from a
// model name on the hot path.
func InferModelFamily(modelName string) string {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	known := []string{"kimi-k2", "gpt-5", "gpt-4", "claude-4", "claude-3", "gemini-3", "gemini-2", "deepseek-v4", "deepseek-v3", "qwen3", "glm-5"}
	for _, family := range known {
		if strings.HasPrefix(name, family) {
			return family
		}
	}
	for _, separator := range []string{"-", ":"} {
		if index := strings.Index(name, separator); index > 0 {
			return name[:index]
		}
	}
	return name
}

func (mi *Model) IsPubliclyListable() bool {
	if mi == nil {
		return false
	}
	return mi.Status == 1 &&
		IsPublicModelLifecycle(mi.LifecycleStatus) &&
		!IsRetiredModelName(mi.ModelName)
}

func (mi *Model) Insert() error {
	mi.normalizeLifecycleForSave()
	now := common.GetTimestamp()
	mi.CreatedTime = now
	mi.UpdatedTime = now
	if mi.LastSeenAt == 0 {
		mi.LastSeenAt = now
	}
	return CoreDB().Create(mi).Error
}

func IsModelNameDuplicated(id int, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	var cnt int64
	err := CoreDB().Model(&Model{}).Where("model_name = ? AND id <> ?", name, id).Count(&cnt).Error
	return cnt > 0, err
}

func (mi *Model) Update() error {
	return mi.updateDB(CoreDB())
}

func (mi *Model) updateDB(db *gorm.DB) error {
	if err := mi.preserveManagedFieldsForUpdate(db); err != nil {
		return err
	}
	mi.normalizeLifecycleForSave()
	mi.UpdatedTime = common.GetTimestamp()
	return db.Session(&gorm.Session{AllowGlobalUpdate: false, FullSaveAssociations: false}).
		Model(&Model{}).
		Where("id = ?", mi.Id).
		Omit(append([]string{"created_time"}, ModelInfoColumns...)...).
		Select("*").
		Updates(mi).Error
}

// ModelInfoColumns are written only through the model-info paths
// (ApplyModelInfoEdits, the models.dev sync), never by a whole-record update:
// a form that predates a column would otherwise blank it.
var ModelInfoColumns = []string{
	"maker", "release_date", "knowledge_cutoff", "input_limit", "modalities",
	"reasoning_options", "unsupported_params", "open_weights", "models_dev_id",
	"info_sources", "info_locked", "official_cache_read_per_mtok", "official_cache_write_per_mtok",
	"official_context_tier",
}

func (mi *Model) preserveManagedFieldsForUpdate(db *gorm.DB) error {
	var current Model
	if err := db.Select("lifecycle_status", "source", "last_seen_at", "retire_after").
		Where("id = ?", mi.Id).
		First(&current).Error; err != nil {
		return err
	}
	if strings.TrimSpace(mi.LifecycleStatus) == "" {
		mi.LifecycleStatus = current.LifecycleStatus
	}
	if strings.TrimSpace(mi.Source) == "" {
		mi.Source = current.Source
	}
	if mi.LastSeenAt == 0 {
		mi.LastSeenAt = current.LastSeenAt
	}
	if mi.RetireAfter == 0 {
		mi.RetireAfter = current.RetireAfter
	}
	return nil
}

func (mi *Model) Delete() error {
	return CoreDB().Delete(mi).Error
}

func GetVendorModelCounts() (map[int64]int64, error) {
	var stats []struct {
		VendorID int64
		Count    int64
	}
	if err := CoreDB().Model(&Model{}).
		Select("vendor_id as vendor_id, count(*) as count").
		Group("vendor_id").
		Scan(&stats).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(stats))
	for _, s := range stats {
		m[s.VendorID] = s.Count
	}
	return m, nil
}

func GetAllModels(offset int, limit int, sortBy string) ([]*Model, error) {
	var models []*Model
	if sortBy == ModelSortCatalog {
		if err := CoreDB().Order("model_name ASC").Find(&models).Error; err != nil {
			return nil, err
		}
		return pageByCatalogOrder(models, offset, limit), nil
	}
	err := CoreDB().Order(modelSortOrder(sortBy)).Offset(offset).Limit(limit).Find(&models).Error
	return models, err
}

func EnrichModelSellPrices(models []*Model) {
	if len(models) == 0 {
		return
	}
	sellPriceLanesByModel := GetActiveModelSellPriceLaneMap()
	sellPriceByModel := make(map[string]ModelSellPrice, len(sellPriceLanesByModel))
	for modelName, rows := range sellPriceLanesByModel {
		var cheap *ModelSellPrice
		for _, row := range rows {
			if row.ChannelGroup == ChannelLaneDirect {
				sellPriceByModel[modelName] = row
				break
			}
			if row.ChannelGroup == ChannelLaneAgent {
				copy := row
				cheap = &copy
			}
		}
		if _, hasDirect := sellPriceByModel[modelName]; !hasDirect && cheap != nil {
			sellPriceByModel[modelName] = *cheap
		}
	}
	for _, item := range models {
		if item == nil {
			continue
		}
		if sellPrice, ok := resolveModelSellPrice(item.ModelName, sellPriceByModel); ok {
			item.applyModelSellPrice(sellPrice)
		}
		if lanes := resolveModelSellPriceLanes(item.ModelName, sellPriceLanesByModel); len(lanes) > 0 {
			item.Lanes = lanes
		}
	}
}

func (mi *Model) applyModelSellPrice(sellPrice ModelSellPrice) {
	mi.InputPerMtok = sellPrice.InputPerMtok
	mi.OutputPerMtok = sellPrice.OutputPerMtok
	mi.CacheReadPerMtok = sellPrice.CacheReadPerMtok
	mi.CacheWrite5mPerMtok = sellPrice.CacheWrite5mPerMtok
	mi.CacheWrite1hPerMtok = sellPrice.CacheWrite1hPerMtok
	mi.AudioInputPerMtok = sellPrice.AudioInputPerMtok
	mi.AudioOutputPerMtok = sellPrice.AudioOutputPerMtok
	mi.ImageInputPerMtok = sellPrice.ImageInputPerMtok
	mi.ImageOutputPerMtok = sellPrice.ImageOutputPerMtok
	mi.RequestUSD = sellPrice.RequestUSD
	mi.SecondUSD = sellPrice.SecondUSD
	mi.PricingVersion = sellPrice.PricingVersion

	if hasTokenPrice(sellPrice) {
		quotaType := 0
		mi.QuotaType = &quotaType
		return
	}
	if sellPrice.RequestUSD != nil || sellPrice.SecondUSD != nil {
		quotaType := 1
		mi.QuotaType = &quotaType
	}
}

func hasTokenPrice(sellPrice ModelSellPrice) bool {
	return sellPrice.InputPerMtok != nil ||
		sellPrice.OutputPerMtok != nil ||
		sellPrice.CacheReadPerMtok != nil ||
		sellPrice.CacheWrite5mPerMtok != nil ||
		sellPrice.CacheWrite1hPerMtok != nil ||
		sellPrice.AudioInputPerMtok != nil ||
		sellPrice.AudioOutputPerMtok != nil ||
		sellPrice.ImageInputPerMtok != nil ||
		sellPrice.ImageOutputPerMtok != nil
}

func GetBoundChannelsByModelsMap(modelNames []string) (map[string][]BoundChannel, error) {
	result := make(map[string][]BoundChannel)
	if len(modelNames) == 0 {
		return result, nil
	}
	type row struct {
		Model string
		Name  string
		Type  int
	}
	var rows []row
	err := CoreDB().Table("channels").
		Select("abilities.model as model, channels.name as name, channels.type as type").
		Joins("JOIN abilities ON abilities.channel_id = channels.id").
		Where("abilities.model IN ? AND abilities.enabled = ?", modelNames, true).
		Distinct().
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		result[r.Model] = append(result[r.Model], BoundChannel{Name: r.Name, Type: r.Type})
	}
	return result, nil
}

func SearchModels(keyword string, vendor string, offset int, limit int, sortBy string) ([]*Model, int64, error) {
	return SearchModelsWithFilters(keyword, vendor, "", "", offset, limit, sortBy)
}

func SearchModelsWithFilters(keyword string, vendor string, status string, tier string, offset int, limit int, sortBy string) ([]*Model, int64, error) {
	var models []*Model
	db := CoreDB().Model(&Model{})
	var keywordOwnerIDs []int
	if vendor != "" {
		if vid, err := strconv.Atoi(vendor); err == nil {
			db = db.Where("models.vendor_id = ?", vid)
		} else {
			db = db.Joins("JOIN vendors filter_vendors ON filter_vendors.id = models.vendor_id").
				Where("filter_vendors.name LIKE ? OR filter_vendors.public_id LIKE ?", "%"+vendor+"%", "%"+vendor+"%")
		}
	}
	switch strings.TrimSpace(status) {
	case "offered":
		// Resolve the same metadata owners as the public catalog, including
		// prefix/suffix rules. Imported candidates are still available via all.
		catalog := GetPricing()
		names := make([]string, 0, len(catalog))
		for _, entry := range catalog {
			names = append(names, entry.ModelName)
		}
		var metadata []Model
		if err := CoreDB().Order("id ASC").Find(&metadata).Error; err != nil {
			return nil, 0, err
		}
		ids := make([]int, 0, len(names))
		for name, owner := range resolveCatalogModelMetadata(metadata, names) {
			ids = append(ids, owner.Id)
			if keyword != "" && strings.Contains(strings.ToLower(name), strings.ToLower(keyword)) {
				keywordOwnerIDs = append(keywordOwnerIDs, owner.Id)
			}
		}
		db = db.Where("models.id IN ?", ids)
	case "listed":
		db = db.Where("models.status = ?", 1)
	case "unlisted":
		db = db.Where("models.status <> ?", 1)
	}

	if keyword != "" {
		like := "%" + keyword + "%"
		db = db.Joins("LEFT JOIN vendors keyword_vendors ON keyword_vendors.id = models.vendor_id").
			Where("models.model_name LIKE ? OR models.tags LIKE ? OR keyword_vendors.name LIKE ? OR keyword_vendors.public_id LIKE ? OR models.id IN ?", like, like, like, like, keywordOwnerIDs)
	}

	pricingOwner := pricingModelNameSQL("models.model_name")
	priceExists := `EXISTS (
		SELECT 1 FROM model_sell_prices model_ops_prices
		WHERE (model_ops_prices.model_name = models.model_name
				OR model_ops_prices.model_name = ` + pricingOwner + `)
			AND model_ops_prices.active = ?
			AND (model_ops_prices.input_per_mtok IS NOT NULL
				OR model_ops_prices.output_per_mtok IS NOT NULL
				OR model_ops_prices.cache_read_per_mtok IS NOT NULL
				OR model_ops_prices.cache_write5m_per_mtok IS NOT NULL
				OR model_ops_prices.cache_write1h_per_mtok IS NOT NULL
				OR model_ops_prices.audio_input_per_mtok IS NOT NULL
				OR model_ops_prices.audio_output_per_mtok IS NOT NULL
				OR model_ops_prices.image_input_per_mtok IS NOT NULL
				OR model_ops_prices.image_output_per_mtok IS NOT NULL
				OR model_ops_prices.request_usd IS NOT NULL
				OR model_ops_prices.second_usd IS NOT NULL)
	)`
	directPriceExists := strings.Replace(priceExists, "AND model_ops_prices.active = ?", "AND model_ops_prices.channel_group = 'direct' AND model_ops_prices.active = ?", 1)
	cheapPriceExists := strings.Replace(priceExists, "AND model_ops_prices.active = ?", "AND model_ops_prices.channel_group = 'cheap' AND model_ops_prices.active = ?", 1)
	switch strings.TrimSpace(tier) {
	case "unpriced":
		db = db.Where("NOT "+priceExists, true)
	case "atRisk":
		db = db.Where("NOT "+directPriceExists, true)
	case "cheapMissingSource":
		abilityGroup := "model_ops_abilities." + abilityGroupCol()
		db = db.Where(cheapPriceExists, true).
			Where(`NOT EXISTS (
				SELECT 1 FROM abilities model_ops_abilities
				WHERE model_ops_abilities.model = models.model_name
					AND model_ops_abilities.enabled = ?
					AND `+abilityGroup+` = ?
			)`, true, ChannelLaneAgent)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if sortBy == ModelSortCatalog {
		if err := db.Order("models.model_name ASC").Find(&models).Error; err != nil {
			return nil, 0, err
		}
		return pageByCatalogOrder(models, offset, limit), total, nil
	}
	if err := db.Order(modelSortOrder(sortBy)).Offset(offset).Limit(limit).Find(&models).Error; err != nil {
		return nil, 0, err
	}
	return models, total, nil
}

// ModelSortCatalog orders the admin list the way every catalog reads: maker
// order, newest release first. It cannot be SQL (the maker order is an
// operator option and makers are partly inferred from names), so the matching
// rows are loaded, sorted, then paged — the model table is small.
const ModelSortCatalog = "catalog"

func pageByCatalogOrder(models []*Model, offset, limit int) []*Model {
	var vendors []Vendor
	_ = CoreDB().Select("id, name").Find(&vendors).Error
	vendorNames := make(map[int]string, len(vendors))
	for _, vendor := range vendors {
		vendorNames[vendor.Id] = vendor.Name
	}
	for _, m := range models {
		m.MakerResolved = ResolveModelMaker(m.Maker, m.ModelName, vendorNames[m.VendorID])
	}
	SortByModelOrder(models, func(m *Model) ModelOrderKey {
		return ModelOrderKey{Name: m.ModelName, Maker: m.MakerResolved, ReleaseDate: m.ConfirmedReleaseDate()}
	})
	if offset < 0 {
		offset = 0
	}
	if offset >= len(models) {
		return []*Model{}
	}
	end := len(models)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return models[offset:end]
}

// pricingModelNameSQL mirrors NormalizePricingModelName for correlated model
// list filters. Keeping the normalization in SQL preserves correct count and
// pagination semantics instead of filtering a page after it is loaded.
func pricingModelNameSQL(column string) string {
	appendThinking := "SUBSTR(" + column + ", 1, LENGTH(" + column + ") - 9) || '-thinking'"
	if !common.UsingSQLite && !common.UsingPostgreSQL {
		appendThinking = "CONCAT(SUBSTR(" + column + ", 1, LENGTH(" + column + ") - 9), '-thinking')"
	}
	trimThinking := "SUBSTR(" + column + ", 1, LENGTH(" + column + ") - 9)"
	return "CASE " +
		"WHEN " + column + " LIKE 'gemini-2.5-flash-lite%-thinking-%' THEN 'gemini-2.5-flash-lite-thinking-*' " +
		"WHEN " + column + " LIKE 'gemini-2.5-flash%-thinking-%' THEN 'gemini-2.5-flash-thinking-*' " +
		"WHEN " + column + " LIKE 'gemini-2.5-pro%-thinking-%' THEN 'gemini-2.5-pro-thinking-*' " +
		"WHEN " + column + " LIKE 'gpt-4-gizmo%' THEN 'gpt-4-gizmo-*' " +
		"WHEN " + column + " LIKE 'gpt-4o-gizmo%' THEN 'gpt-4o-gizmo-*' " +
		"WHEN " + column + " LIKE 'claude-%#thinking' THEN " + trimThinking + " " +
		"WHEN " + column + " LIKE 'claude-%-thinking' THEN " + trimThinking + " " +
		"WHEN " + column + " LIKE '%#thinking' THEN " + appendThinking + " " +
		"ELSE " + column + " END"
}

// ListCatalogModelNames returns every model name the catalog knows, retired
// ones excluded.
//
// It replaces the inherited per-channel-type tables of hardcoded model names
// that used to answer "what can this channel serve". Those came from the
// upstream gateway this is forked from: a fixed list of ~120 names, half of
// them models nobody has been able to call for two years, that no amount of
// operator work could correct. The catalog is the thing operators actually
// maintain, so it is what the channel form now offers.
func ListCatalogModelNames() []string {
	if CoreDB() == nil {
		return nil
	}
	var names []string
	if err := CoreDB().Model(&Model{}).
		Where("lifecycle_status <> ?", ModelLifecycleRetired).
		Where("name_rule = ?", NameRuleExact).
		Distinct().
		Order("model_name ASC").
		Pluck("model_name", &names).Error; err != nil {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" || IsRetiredModelName(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// CatalogModelOwners resolves configured vendors in one query for model lists.
// Deleted catalog/vendor rows are excluded; callers may infer unknown brands.
func CatalogModelOwners(names []string) map[string]string {
	owners := make(map[string]string)
	if CoreDB() == nil || len(names) == 0 {
		return owners
	}
	var rows []struct {
		Model
		Owner string
	}
	if err := CoreDB().Model(&Model{}).Select("models.*, COALESCE(vendors.name, '') AS owner").
		Joins("LEFT JOIN vendors ON vendors.id = models.vendor_id AND vendors.deleted_at IS NULL").
		Order("models.id ASC").Scan(&rows).Error; err != nil {
		return owners
	}
	metas := make([]Model, 0, len(rows))
	ownerByID := make(map[int]string, len(rows))
	for _, row := range rows {
		metas = append(metas, row.Model)
		ownerByID[row.Id] = row.Owner
	}
	for name, meta := range resolveCatalogModelMetadata(metas, names) {
		if owner := strings.TrimSpace(ownerByID[meta.Id]); owner != "" {
			owners[name] = owner
		}
	}
	return owners
}

// CatalogModelIDs identifies the metadata record that supplies each offered
// model's facts, including aliases covered by prefix/suffix/contains rules.
func CatalogModelIDs(names []string) (map[string]int, error) {
	ids := make(map[string]int)
	if len(names) == 0 {
		return ids, nil
	}
	var metas []Model
	if err := CoreDB().Select("id, model_name, name_rule, status, lifecycle_status").Order("id ASC").Find(&metas).Error; err != nil {
		return nil, err
	}
	for name, meta := range resolveCatalogModelMetadata(metas, names) {
		ids[name] = meta.Id
	}
	return ids, nil
}

// Resolve rules in the same priority order used by pricing: exact, prefix,
// suffix, then contains. Matching metadata without a vendor still wins over
// a less specific rule, so ownership cannot leak from a different catalog row.
func resolveCatalogModelMetadata(metas []Model, names []string) map[string]*Model {
	resolved := make(map[string]*Model)
	for i := range metas {
		metas[i].LifecycleStatus = NormalizeModelLifecycleStatus(metas[i].LifecycleStatus)
	}
	for _, rule := range []int{NameRuleExact, NameRulePrefix, NameRuleSuffix, NameRuleContains} {
		for i := range metas {
			meta := &metas[i]
			if meta.NameRule != rule || !meta.IsPubliclyListable() {
				continue
			}
			for _, name := range names {
				if _, exists := resolved[name]; exists {
					continue
				}
				matches := name == meta.ModelName
				switch rule {
				case NameRulePrefix:
					matches = strings.HasPrefix(name, meta.ModelName)
				case NameRuleSuffix:
					matches = strings.HasSuffix(name, meta.ModelName)
				case NameRuleContains:
					matches = strings.Contains(name, meta.ModelName)
				}
				if matches {
					resolved[name] = meta
				}
			}
		}
	}
	return resolved
}
