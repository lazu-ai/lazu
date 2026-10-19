package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultPricingSeedVersion int64 = 1
const defaultPricingSeedSource = "default_pricing_seed"
const openingBalanceRefType = "opening_balance"

const (
	WalletLedgerKindTopup           = "topup"
	WalletLedgerKindSpend           = "spend"
	WalletLedgerKindBonus           = "bonus"
	WalletLedgerKindRefund          = "refund"
	WalletLedgerKindPaymentReversal = "payment_reversal"
	WalletLedgerKindAdjustment      = "adjustment"
)

var (
	legacyQuotaUSDPattern   = regexp.MustCompile(`[＄$]([-+]?[0-9]+(?:\.[0-9]+)?)[[:space:]]*额度`)
	legacyQuotaPointPattern = regexp.MustCompile(`([-+]?[0-9]+)[[:space:]]*点额度`)
)

var providerCostPriceTableCache = struct {
	sync.Mutex
	db        *gorm.DB
	checked   bool
	available bool
}{}

type ProviderCostPrice struct {
	ImagePricing        *billing.ImagePrices `json:"image_pricing,omitempty" gorm:"type:text;serializer:json"`
	VideoPricing        *billing.VideoPrices `json:"video_pricing,omitempty" gorm:"type:text;serializer:json"`
	ID                  uint                 `json:"id" gorm:"primaryKey"`
	ModelName           string               `json:"model_name" gorm:"type:varchar(191);index:idx_provider_cost_prices_model_provider_channel_version,unique;not null"`
	Provider            string               `json:"provider" gorm:"type:varchar(64);default:'seed';index:idx_provider_cost_prices_model_provider_channel_version,unique"`
	ChannelID           int                  `json:"channel_id" gorm:"default:0;index:idx_provider_cost_prices_model_provider_channel_version,unique"`
	InputPerMtok        *float64             `json:"input_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	OutputPerMtok       *float64             `json:"output_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	CacheReadPerMtok    *float64             `json:"cache_read_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	CacheWrite5mPerMtok *float64             `json:"cache_write_5m_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	CacheWrite1hPerMtok *float64             `json:"cache_write_1h_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	AudioInputPerMtok   *float64             `json:"audio_input_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	AudioOutputPerMtok  *float64             `json:"audio_output_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ImageInputPerMtok   *float64             `json:"image_input_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ImageOutputPerMtok  *float64             `json:"image_output_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	RequestUSD          *float64             `json:"request_usd,omitempty" gorm:"type:decimal(20,10)"`
	SecondUSD           *float64             `json:"second_usd,omitempty" gorm:"type:decimal(20,10)"`
	// Context tier on the cost side, same meaning as on ModelSellPrice: past
	// the threshold the whole request costs the tier prices. Whether a source
	// passes on the lab's long-context surcharge is a property of the source,
	// so each cost row carries its own. A row without one falls back to the
	// lab's official tier only when it is the list-price row (channel 0).
	ContextTierThreshold           int64     `json:"context_tier_threshold,omitempty" gorm:"default:0"`
	ContextTierInputPerMtok        *float64  `json:"context_tier_input_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ContextTierOutputPerMtok       *float64  `json:"context_tier_output_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ContextTierCacheReadPerMtok    *float64  `json:"context_tier_cache_read_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ContextTierCacheWrite5mPerMtok *float64  `json:"context_tier_cache_write_5m_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ContextTierCacheWrite1hPerMtok *float64  `json:"context_tier_cache_write_1h_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	Source                         string    `json:"source" gorm:"type:varchar(64);default:'manual'"`
	PricingVersion                 int64     `json:"pricing_version" gorm:"index:idx_provider_cost_prices_model_provider_channel_version,unique;not null"`
	CreatedAt                      time.Time `json:"created_at"`
	UpdatedAt                      time.Time `json:"updated_at"`
}

func (price ProviderCostPrice) ToBillingPriceSheet() billing.PriceSheet {
	return billing.PriceSheet{
		ModelName:                   price.ModelName,
		Images:                      price.ImagePricing.Scaled(1),
		Video:                       price.VideoPricing.Scaled(1),
		InputPerMtokMicrousd:        usdToMicrousdPtr(price.InputPerMtok),
		OutputPerMtokMicrousd:       usdToMicrousdPtr(price.OutputPerMtok),
		CacheReadPerMtokMicrousd:    usdToMicrousdPtr(price.CacheReadPerMtok),
		CacheWrite5mPerMtokMicrousd: usdToMicrousdPtr(price.CacheWrite5mPerMtok),
		CacheWrite1hPerMtokMicrousd: usdToMicrousdPtr(price.CacheWrite1hPerMtok),
		AudioInputPerMtokMicrousd:   usdToMicrousdPtr(price.AudioInputPerMtok),
		AudioOutputPerMtokMicrousd:  usdToMicrousdPtr(price.AudioOutputPerMtok),
		ImageInputPerMtokMicrousd:   usdToMicrousdPtr(price.ImageInputPerMtok),
		ImageOutputPerMtokMicrousd:  usdToMicrousdPtr(price.ImageOutputPerMtok),
		RequestMicrousd:             usdToMicrousdPtr(price.RequestUSD),
		SecondMicrousd:              usdToMicrousdPtr(price.SecondUSD),
		PricingVersion:              price.PricingVersion,
	}
}

// HasContextTier reports whether long inputs cost separately on this source.
func (price ProviderCostPrice) HasContextTier() bool {
	return price.ContextTierThreshold > 0
}

// ForInputTokens returns the cost that applies to a request with this many
// input tokens (see ModelSellPrice.ForInputTokens).
func (price ProviderCostPrice) ForInputTokens(inputTokens int64) (ProviderCostPrice, bool) {
	if !price.HasContextTier() || inputTokens <= price.ContextTierThreshold {
		return price, false
	}
	tiered := price
	tiered.InputPerMtok = pickTierPrice(price.ContextTierInputPerMtok, price.InputPerMtok)
	tiered.OutputPerMtok = pickTierPrice(price.ContextTierOutputPerMtok, price.OutputPerMtok)
	tiered.CacheReadPerMtok = pickTierPrice(price.ContextTierCacheReadPerMtok, price.CacheReadPerMtok)
	tiered.CacheWrite5mPerMtok = pickTierPrice(price.ContextTierCacheWrite5mPerMtok, price.CacheWrite5mPerMtok)
	tiered.CacheWrite1hPerMtok = pickTierPrice(price.ContextTierCacheWrite1hPerMtok, price.CacheWrite1hPerMtok)
	return tiered, true
}

// WithOfficialContextTier gives a list-price cost row the lab's long-context
// tier, scaled from the lab's base prices to the row's own: a row at 80% of
// list costs 80% of the long-context list price too. Dimensions the lab does
// not tier, or the row does not price, keep the base cost.
func (price ProviderCostPrice) WithOfficialContextTier(official ModelOfficialPrice) ProviderCostPrice {
	tier := official.ContextTier
	if price.HasContextTier() || tier == nil || tier.Threshold <= 0 {
		return price
	}
	scaled := func(cost *float64, base float64, long *float64) *float64 {
		if cost == nil || long == nil || base <= 0 {
			return nil
		}
		value := *cost * (*long / base)
		return &value
	}
	price.ContextTierThreshold = tier.Threshold
	price.ContextTierInputPerMtok = scaled(price.InputPerMtok, official.InputPerMtok(), tier.InputPerMtok)
	price.ContextTierOutputPerMtok = scaled(price.OutputPerMtok, official.OutputPerMtok(), tier.OutputPerMtok)
	price.ContextTierCacheReadPerMtok = scaled(price.CacheReadPerMtok, official.CacheReadPerMtok, tier.CacheReadPerMtok)
	price.ContextTierCacheWrite5mPerMtok = scaled(price.CacheWrite5mPerMtok, official.CacheWritePerMtok, tier.CacheWrite5mPerMtok)
	return price
}

func pickTierPrice(tier, base *float64) *float64 {
	if tier != nil {
		return tier
	}
	return base
}

type ModelSellPrice struct {
	ImagePricing        *billing.ImagePrices `json:"image_pricing,omitempty" gorm:"type:text;serializer:json"`
	VideoPricing        *billing.VideoPrices `json:"video_pricing,omitempty" gorm:"type:text;serializer:json"`
	ID                  uint                 `json:"id" gorm:"primaryKey"`
	ModelName           string               `json:"model_name" gorm:"type:varchar(191);index:idx_model_sell_prices_model_lane_version,unique;not null"`
	ChannelGroup        string               `json:"channel_group" gorm:"type:varchar(32);default:'direct';index:idx_model_sell_prices_model_lane_version,unique;not null"`
	InputPerMtok        *float64             `json:"input_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	OutputPerMtok       *float64             `json:"output_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	CacheReadPerMtok    *float64             `json:"cache_read_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	CacheWrite5mPerMtok *float64             `json:"cache_write_5m_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	CacheWrite1hPerMtok *float64             `json:"cache_write_1h_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	AudioInputPerMtok   *float64             `json:"audio_input_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	AudioOutputPerMtok  *float64             `json:"audio_output_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ImageInputPerMtok   *float64             `json:"image_input_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ImageOutputPerMtok  *float64             `json:"image_output_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	RequestUSD          *float64             `json:"request_usd,omitempty" gorm:"type:decimal(20,10)"`
	SecondUSD           *float64             `json:"second_usd,omitempty" gorm:"type:decimal(20,10)"`
	// PeakHoursUTC is a comma-separated list of UTC hour ranges (e.g. "1-4,6-10",
	// each range [start,end) in 24h). PeakMultiplier scales every price field
	// while the current UTC hour falls inside one of the ranges — this models
	// DeepSeek-style peak/off-peak pricing (off-peak = the stored base price).
	// Empty PeakHoursUTC or nil/≤0 PeakMultiplier means no time-of-day pricing.
	PeakHoursUTC   string   `json:"peak_hours_utc,omitempty" gorm:"type:varchar(64)"`
	PeakMultiplier *float64 `json:"peak_multiplier,omitempty" gorm:"type:decimal(10,4)"`
	// ContextTierThreshold splits pricing by input length: when a request's
	// input tokens (cached included) exceed it, the whole request is billed
	// at the ContextTier* prices, as OpenAI and Google bill long prompts. Zero
	// means a single price. A tier price left nil keeps the base price.
	ContextTierThreshold           int64    `json:"context_tier_threshold,omitempty" gorm:"default:0"`
	ContextTierInputPerMtok        *float64 `json:"context_tier_input_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ContextTierOutputPerMtok       *float64 `json:"context_tier_output_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ContextTierCacheReadPerMtok    *float64 `json:"context_tier_cache_read_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ContextTierCacheWrite5mPerMtok *float64 `json:"context_tier_cache_write_5m_per_mtok,omitempty" gorm:"type:decimal(20,10)"`
	ContextTierCacheWrite1hPerMtok *float64 `json:"context_tier_cache_write_1h_per_mtok,omitempty" gorm:"type:decimal(20,10)"`

	// FollowOfficial derives the token prices from the model's official list
	// price × PriceRatio (percent; nil = the lane's default ratio). Fields in
	// PinnedFields (comma separated, see SellPriceField*) keep their stored
	// value. Billing reads only the stored numbers; see sell_price_ratio.go.
	FollowOfficial bool     `json:"follow_official" gorm:"default:false"`
	PriceRatio     *float64 `json:"price_ratio,omitempty" gorm:"type:decimal(10,4)"`
	PinnedFields   string   `json:"pinned_fields,omitempty" gorm:"type:varchar(128);default:''"`

	Source         string    `json:"source" gorm:"type:varchar(64);default:'manual'"`
	Active         bool      `json:"active" gorm:"index;default:true"`
	PricingVersion int64     `json:"pricing_version" gorm:"index:idx_model_sell_prices_model_lane_version,unique;not null"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// hourInUTCRanges reports whether hour (0-23) falls in any range of a
// comma-separated spec like "1-4,6-10". Each range is [start,end); a range with
// start>end (e.g. "22-2") wraps past midnight.
func hourInUTCRanges(hour int, ranges string) bool {
	for _, part := range strings.Split(ranges, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		bounds := strings.SplitN(part, "-", 2)
		if len(bounds) != 2 {
			continue
		}
		start, err1 := strconv.Atoi(strings.TrimSpace(bounds[0]))
		end, err2 := strconv.Atoi(strings.TrimSpace(bounds[1]))
		if err1 != nil || err2 != nil || start == end {
			continue
		}
		// Reject out-of-domain bounds so an admin typo like "25-1" or "1--1"
		// can't turn into a near-all-day surcharge. Hours are 0-23; end may be
		// 24 to mean midnight (e.g. "20-24" = 20,21,22,23).
		if start < 0 || start > 23 || end < 0 || end > 24 {
			continue
		}
		if start < end {
			if hour >= start && hour < end {
				return true
			}
		} else { // wraps midnight
			if hour >= start || hour < end {
				return true
			}
		}
	}
	return false
}

// PeakFactorAt returns the price multiplier in effect at now. Off-peak or
// unconfigured returns 1.
func (price ModelSellPrice) PeakFactorAt(now time.Time) float64 {
	if price.PeakMultiplier == nil || *price.PeakMultiplier <= 0 || strings.TrimSpace(price.PeakHoursUTC) == "" {
		return 1
	}
	if hourInUTCRanges(now.UTC().Hour(), price.PeakHoursUTC) {
		return *price.PeakMultiplier
	}
	return 1
}

// ToBillingPriceSheetAt builds the price sheet effective at now, applying the
// peak multiplier when now is inside PeakHoursUTC.
func (price ModelSellPrice) ToBillingPriceSheetAt(now time.Time) billing.PriceSheet {
	factor := price.PeakFactorAt(now)
	scale := func(p *float64) *billing.MicroUSD {
		if p == nil {
			return nil
		}
		v := *p * factor
		return usdToMicrousdPtr(&v)
	}
	return billing.PriceSheet{
		ModelName:                   price.ModelName,
		Images:                      price.ImagePricing.Scaled(factor),
		Video:                       price.VideoPricing.Scaled(factor),
		InputPerMtokMicrousd:        scale(price.InputPerMtok),
		OutputPerMtokMicrousd:       scale(price.OutputPerMtok),
		CacheReadPerMtokMicrousd:    scale(price.CacheReadPerMtok),
		CacheWrite5mPerMtokMicrousd: scale(price.CacheWrite5mPerMtok),
		CacheWrite1hPerMtokMicrousd: scale(price.CacheWrite1hPerMtok),
		AudioInputPerMtokMicrousd:   scale(price.AudioInputPerMtok),
		AudioOutputPerMtokMicrousd:  scale(price.AudioOutputPerMtok),
		ImageInputPerMtokMicrousd:   scale(price.ImageInputPerMtok),
		ImageOutputPerMtokMicrousd:  scale(price.ImageOutputPerMtok),
		RequestMicrousd:             scale(price.RequestUSD),
		SecondMicrousd:              scale(price.SecondUSD),
		PricingVersion:              price.PricingVersion,
	}
}

func (price ModelSellPrice) ToBillingPriceSheet() billing.PriceSheet {
	return price.ToBillingPriceSheetAt(time.Now())
}

// HasContextTier reports whether long inputs are priced separately.
func (price ModelSellPrice) HasContextTier() bool {
	return price.ContextTierThreshold > 0
}

// ForInputTokens returns the price that applies to a request with this many
// input tokens: the long-context prices once the input is past the
// threshold, the base prices otherwise. The second value says which applied.
func (price ModelSellPrice) ForInputTokens(inputTokens int64) (ModelSellPrice, bool) {
	if !price.HasContextTier() || inputTokens <= price.ContextTierThreshold {
		return price, false
	}
	tiered := price
	tiered.InputPerMtok = pickTierPrice(price.ContextTierInputPerMtok, price.InputPerMtok)
	tiered.OutputPerMtok = pickTierPrice(price.ContextTierOutputPerMtok, price.OutputPerMtok)
	tiered.CacheReadPerMtok = pickTierPrice(price.ContextTierCacheReadPerMtok, price.CacheReadPerMtok)
	tiered.CacheWrite5mPerMtok = pickTierPrice(price.ContextTierCacheWrite5mPerMtok, price.CacheWrite5mPerMtok)
	tiered.CacheWrite1hPerMtok = pickTierPrice(price.ContextTierCacheWrite1hPerMtok, price.CacheWrite1hPerMtok)
	return tiered, true
}

type WalletLedger struct {
	EventID        string           `json:"-" gorm:"size:191;index"`
	ID             uint             `json:"id" gorm:"primaryKey"`
	UserID         int              `json:"user_id" gorm:"index:idx_wallet_ledger_user_created;index:idx_wallet_ledger_opening,unique;not null"`
	DeltaMicrousd  billing.MicroUSD `json:"delta_microusd" gorm:"type:bigint;not null"`
	Kind           string           `json:"kind" gorm:"type:varchar(32);index;not null"`
	RefType        string           `json:"ref_type" gorm:"type:varchar(64);index:idx_wallet_ledger_opening,unique"`
	RefID          string           `json:"ref_id" gorm:"type:varchar(191);index:idx_wallet_ledger_opening,unique;index:idx_wallet_ledger_ref_id"`
	PricingVersion int64            `json:"pricing_version" gorm:"index;not null"`
	Metadata       string           `json:"metadata,omitempty" gorm:"type:text"`
	CreatedAt      time.Time        `json:"created_at" gorm:"index:idx_wallet_ledger_user_created"`
}

type WalletLedgerReconciliation struct {
	UserID         int              `json:"user_id"`
	CachedQuota    billing.MicroUSD `json:"cached_quota"`
	CachedMicrousd billing.MicroUSD `json:"cached_microusd"`
	LedgerMicrousd billing.MicroUSD `json:"ledger_microusd"`
	DeltaMicrousd  billing.MicroUSD `json:"delta_microusd"`
}

// WalletLedgerDailyMovement is the user-safe daily projection returned to the
// Console. Aggregating while streaming keeps response and application memory
// bounded even for accounts with very high request volume. Ref IDs, metadata,
// and individual request movements never leave the backend.
type WalletLedgerDailyMovement struct {
	Date                   string           `json:"date"`
	CreditMicrousd         billing.MicroUSD `json:"credit_microusd"`
	SpendMicrousd          billing.MicroUSD `json:"spend_microusd"`
	ClosingBalanceMicrousd billing.MicroUSD `json:"closing_balance_microusd"`
}

type WalletLedgerRange struct {
	OpeningBalanceMicrousd billing.MicroUSD            `json:"opening_balance_microusd"`
	Items                  []WalletLedgerDailyMovement `json:"items"`
}

type PreConsumeQuotaWithLedgerInput struct {
	UserID         int
	ProjectID      int
	TokenID        int
	TokenKey       string
	TokenUnlimited bool
	IsPlayground   bool
	Quota          billing.MicroUSD
	RefType        string
	RefID          string
	PricingVersion int64
	Metadata       map[string]any
}

type AdjustUserQuotaWithLedgerInput struct {
	UserID             int
	DeltaQuota         billing.MicroUSD
	Kind               string
	RefType            string
	RefID              string
	PricingVersion     int64
	Metadata           map[string]any
	AllowNegative      bool
	IncludeDeletedUser bool // historical payment reversal only; never restores login access
}

type AdjustRelayQuotaWithLedgerInput struct {
	AcceptedSettlement bool // trusted settlement path may update a soft-deleted logical Key without restoring access
	UserID             int
	TokenID            int
	TokenKey           string
	TokenUnlimited     bool
	IsPlayground       bool
	QuotaDelta         billing.MicroUSD
	RefType            string
	RefID              string
	PricingVersion     int64
	Metadata           map[string]any
}

func GetActiveModelSellPriceMap() map[string]ModelSellPrice {
	laneMap := GetActiveModelSellPriceLaneMap()
	result := make(map[string]ModelSellPrice)
	for modelName, rows := range laneMap {
		var cheap *ModelSellPrice
		for _, row := range rows {
			if row.ChannelGroup == ChannelLaneDirect {
				result[modelName] = row
				break
			}
			if row.ChannelGroup == ChannelLaneAgent {
				copy := row
				cheap = &copy
			}
		}
		if _, hasDirect := result[modelName]; !hasDirect && cheap != nil {
			result[modelName] = *cheap
		}
	}
	return result
}

func GetActiveModelSellPriceLaneMap() map[string][]ModelSellPrice {
	if snapshotRows, ok := getSnapshotActiveModelSellPriceLaneMap(); ok {
		return snapshotRows
	}
	result := make(map[string][]ModelSellPrice)
	if pricingSnapshotRequired() {
		return result
	}
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&ModelSellPrice{}) {
		return result
	}
	var rows []ModelSellPrice
	if err := CoreDB().Where("active = ?", true).Order("model_name ASC, channel_group ASC, pricing_version DESC").Find(&rows).Error; err != nil {
		common.SysLog("failed to load active model sell prices: " + err.Error())
		return result
	}
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		row.ChannelGroup = NormalizeChannelLane(row.ChannelGroup)
		key := row.ModelName + "\x00" + row.ChannelGroup
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result[row.ModelName] = append(result[row.ModelName], row)
	}
	return result
}

func GetActiveModelSellPrice(modelName string, channelGroup ...string) (ModelSellPrice, bool) {
	group := ChannelLaneDirect
	if len(channelGroup) > 0 {
		group = NormalizeChannelLane(channelGroup[0])
	}
	if !IsKnownChannelLane(group) {
		group = ChannelLaneDirect
	}
	if row, ok := getSnapshotActiveModelSellPrice(modelName, group); ok {
		return row, true
	}
	if pricingSnapshotRequired() {
		return ModelSellPrice{}, false // a complete registry also owns negative lookups
	}
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&ModelSellPrice{}) {
		return ModelSellPrice{}, false
	}
	if row, ok := findActiveModelSellPrice(modelName, group); ok {
		return row, true
	}
	if matched := NormalizePricingModelName(modelName); matched != "" && matched != modelName {
		return findActiveModelSellPrice(matched, group)
	}
	return ModelSellPrice{}, false
}

func findActiveModelSellPrice(modelName, channelGroup string) (ModelSellPrice, bool) {
	channelGroup = NormalizeChannelLane(channelGroup)
	var row ModelSellPrice
	if err := CoreDB().Where("active = ? AND model_name = ? AND channel_group = ?", true, modelName, channelGroup).
		Order("pricing_version DESC").
		Limit(1).
		Find(&row).Error; err != nil {
		common.SysLog("failed to load active model sell price: " + err.Error())
		return ModelSellPrice{}, false
	}
	if row.ID == 0 {
		return ModelSellPrice{}, false
	}
	row.ChannelGroup = NormalizeChannelLane(row.ChannelGroup)
	return row, true
}

func ListModelSellPrices() ([]ModelSellPrice, error) {
	if CoreDB() == nil {
		return nil, nil
	}
	var rows []ModelSellPrice
	err := CoreDB().Order("model_name ASC, channel_group ASC, pricing_version DESC").Find(&rows).Error
	for i := range rows {
		rows[i].ChannelGroup = NormalizeChannelLane(rows[i].ChannelGroup)
	}
	return rows, err
}

func ListProviderCostPricesForChannel(channelID int) ([]ProviderCostPrice, error) {
	if CoreDB() == nil || channelID <= 0 {
		return nil, nil
	}
	var rows []ProviderCostPrice
	err := CoreDB().Where("channel_id = ?", channelID).
		Order("model_name ASC, pricing_version DESC").
		Find(&rows).Error
	return rows, err
}

func GetActiveProviderCostPrice(modelName string, channelID int) (ProviderCostPrice, bool) {
	if CoreDB() == nil || !providerCostPriceTableExists() {
		return ProviderCostPrice{}, false
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return ProviderCostPrice{}, false
	}
	row, ok := findActiveProviderCostPrice(modelName, channelID)
	if !ok {
		if matched := NormalizePricingModelName(modelName); matched != "" && matched != modelName {
			row, ok = findActiveProviderCostPrice(matched, channelID)
		}
	}
	if !ok {
		return ProviderCostPrice{}, false
	}
	// The list-price row (channel 0) is the lab's price, so it takes the lab's
	// long-context tier when it has none of its own.
	if row.ChannelID == 0 {
		if official, ok := GetModelOfficialPrice(modelName); ok {
			row = row.WithOfficialContextTier(official)
		} else if official, ok := GetModelOfficialPrice(row.ModelName); ok {
			row = row.WithOfficialContextTier(official)
		}
	}
	// A source without its own cost row falls back to the official list price
	// (channel 0). Scale that by what the operator pays this source, if set.
	if row.ChannelID == 0 && channelID > 0 {
		if multiplier := channelCostMultiplier(channelID); multiplier > 0 && multiplier != 1 {
			row = row.Scaled(multiplier)
		}
	}
	return row, true
}

// channelCostMultiplier reads the source's cost_multiplier setting.
func channelCostMultiplier(channelID int) float64 {
	channel, err := CacheGetChannel(channelID)
	if err != nil || channel == nil {
		return 0
	}
	return channel.GetOtherSettings().CostMultiplier
}

// Scaled returns the price with every rate multiplied by factor.
func (price ProviderCostPrice) Scaled(factor float64) ProviderCostPrice {
	scale := func(value *float64) *float64 {
		if value == nil {
			return nil
		}
		scaled := *value * factor
		return &scaled
	}
	price.InputPerMtok = scale(price.InputPerMtok)
	price.OutputPerMtok = scale(price.OutputPerMtok)
	price.CacheReadPerMtok = scale(price.CacheReadPerMtok)
	price.CacheWrite5mPerMtok = scale(price.CacheWrite5mPerMtok)
	price.CacheWrite1hPerMtok = scale(price.CacheWrite1hPerMtok)
	price.AudioInputPerMtok = scale(price.AudioInputPerMtok)
	price.AudioOutputPerMtok = scale(price.AudioOutputPerMtok)
	price.ImageInputPerMtok = scale(price.ImageInputPerMtok)
	price.ImageOutputPerMtok = scale(price.ImageOutputPerMtok)
	price.ImagePricing = price.ImagePricing.Scaled(factor)
	price.RequestUSD = scale(price.RequestUSD)
	price.SecondUSD = scale(price.SecondUSD)
	price.ContextTierInputPerMtok = scale(price.ContextTierInputPerMtok)
	price.ContextTierOutputPerMtok = scale(price.ContextTierOutputPerMtok)
	price.ContextTierCacheReadPerMtok = scale(price.ContextTierCacheReadPerMtok)
	price.ContextTierCacheWrite5mPerMtok = scale(price.ContextTierCacheWrite5mPerMtok)
	price.ContextTierCacheWrite1hPerMtok = scale(price.ContextTierCacheWrite1hPerMtok)
	return price
}

func providerCostPriceTableExists() bool {
	if CoreDB() == nil {
		return false
	}
	providerCostPriceTableCache.Lock()
	if providerCostPriceTableCache.checked && providerCostPriceTableCache.db == CoreDB() {
		available := providerCostPriceTableCache.available
		providerCostPriceTableCache.Unlock()
		return available
	}
	providerCostPriceTableCache.Unlock()

	available := CoreDB().Migrator().HasTable(&ProviderCostPrice{})

	providerCostPriceTableCache.Lock()
	providerCostPriceTableCache.db = CoreDB()
	providerCostPriceTableCache.checked = true
	providerCostPriceTableCache.available = available
	providerCostPriceTableCache.Unlock()
	return available
}

func findActiveProviderCostPrice(modelName string, channelID int) (ProviderCostPrice, bool) {
	if snapshot, ready := currentPricingSnapshot(); ready {
		return snapshot.findProviderCost(modelName, channelID)
	}
	var row ProviderCostPrice
	channelIDs := []int{0}
	if channelID > 0 {
		channelIDs = []int{channelID, 0}
	}
	query := CoreDB().Where("model_name = ? AND channel_id IN ?", modelName, channelIDs)
	if channelID > 0 {
		query = query.Order(fmt.Sprintf("CASE WHEN channel_id = %d THEN 0 ELSE 1 END", channelID))
	}
	if err := query.
		Order("pricing_version DESC").
		Limit(1).
		Find(&row).Error; err != nil {
		common.SysLog("failed to load active provider cost price: " + err.Error())
		return ProviderCostPrice{}, false
	}
	if row.ID == 0 {
		return ProviderCostPrice{}, false
	}
	return row, true
}

func ReplaceProviderCostPricesForChannel(channelID int, rows []ProviderCostPrice) error {
	if CoreDB() == nil || channelID <= 0 || rows == nil {
		return nil
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("channel_id = ?", channelID).Delete(&ProviderCostPrice{}).Error; err != nil {
			return err
		}
		nowVersion := time.Now().UnixNano()
		for _, row := range rows {
			if err := row.ImagePricing.Validate(); err != nil {
				return err
			}
			row.ID = 0
			row.ChannelID = channelID
			row.ModelName = strings.TrimSpace(row.ModelName)
			if row.ModelName == "" {
				continue
			}
			if row.ContextTierThreshold < 0 {
				return fmt.Errorf("provider cost price %s: context_tier_threshold must not be negative", row.ModelName)
			}
			if strings.TrimSpace(row.Provider) == "" {
				row.Provider = "manual"
			}
			if row.PricingVersion == 0 {
				row.PricingVersion = nowVersion
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		if refreshErr := RefreshPricingSnapshot(); refreshErr != nil {
			return refreshErr
		}
	}
	return err
}

func UpsertActiveModelSellPrice(row ModelSellPrice) error {
	if CoreDB() == nil {
		return nil
	}
	row.ModelName = NormalizePricingModelName(strings.TrimSpace(row.ModelName))
	row.ChannelGroup = NormalizeChannelLane(row.ChannelGroup)
	if row.ModelName == "" {
		return types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "model_name"})
	}
	if !IsKnownChannelLane(row.ChannelGroup) {
		return types.Err(types.ErrorCodeLaneNotFound, types.D{"lane": row.ChannelGroup})
	}
	if err := validateModelSellPrice(row); err != nil {
		return err
	}
	row.Active = true
	if row.PricingVersion == 0 {
		row.PricingVersion = time.Now().UnixNano()
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ModelSellPrice{}).
			Where("model_name = ? AND channel_group = ? AND active = ?", row.ModelName, row.ChannelGroup, true).
			Update("active", false).Error; err != nil {
			return err
		}
		return tx.Create(&row).Error
	})
	if err == nil {
		if refreshErr := RefreshPricingSnapshot(); refreshErr != nil {
			common.SysLog("failed to refresh pricing snapshot after price upsert: " + refreshErr.Error())
		}
	}
	return err
}

func validateModelSellPrice(row ModelSellPrice) error {
	if err := row.ImagePricing.Validate(); err != nil {
		return err
	}
	if row.ImagePricing != nil {
		if row.RequestUSD == nil || *row.RequestUSD != row.ImagePricing.OutputUSD || row.FollowOfficial || row.SecondUSD != nil || row.InputPerMtok != nil || row.OutputPerMtok != nil {
			return fmt.Errorf("image_pricing requires a matching request_usd headline and fixed image-only pricing")
		}
	}
	prices := map[string]*float64{
		"input_per_mtok":          row.InputPerMtok,
		"output_per_mtok":         row.OutputPerMtok,
		"cache_read_per_mtok":     row.CacheReadPerMtok,
		"cache_write_5m_per_mtok": row.CacheWrite5mPerMtok,
		"cache_write_1h_per_mtok": row.CacheWrite1hPerMtok,
		"audio_input_per_mtok":    row.AudioInputPerMtok,
		"audio_output_per_mtok":   row.AudioOutputPerMtok,
		"image_input_per_mtok":    row.ImageInputPerMtok,
		"image_output_per_mtok":   row.ImageOutputPerMtok,
		"request_usd":             row.RequestUSD,
		"second_usd":              row.SecondUSD,
	}
	hasPrice := row.ImagePricing != nil
	for field, value := range prices {
		if value == nil {
			continue
		}
		hasPrice = true
		if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
			return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": field})
		}
	}
	if !hasPrice {
		return types.Err(types.ErrorCodeSellPriceMissing)
	}
	if row.PeakMultiplier != nil &&
		(math.IsNaN(*row.PeakMultiplier) || math.IsInf(*row.PeakMultiplier, 0) || *row.PeakMultiplier < 0) {
		return types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "peak_multiplier"})
	}
	if row.ContextTierThreshold < 0 {
		return fmt.Errorf("context_tier_threshold must be non-negative")
	}
	tierPrices := map[string]*float64{
		"context_tier_input_per_mtok":          row.ContextTierInputPerMtok,
		"context_tier_output_per_mtok":         row.ContextTierOutputPerMtok,
		"context_tier_cache_read_per_mtok":     row.ContextTierCacheReadPerMtok,
		"context_tier_cache_write_5m_per_mtok": row.ContextTierCacheWrite5mPerMtok,
		"context_tier_cache_write_1h_per_mtok": row.ContextTierCacheWrite1hPerMtok,
	}
	hasTierPrice := false
	for field, value := range tierPrices {
		if value == nil {
			continue
		}
		hasTierPrice = true
		if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
			return fmt.Errorf("%s must be a finite non-negative number", field)
		}
	}
	if row.ContextTierThreshold > 0 && !hasTierPrice {
		return fmt.Errorf("a context tier needs at least one long-context price")
	}
	return nil
}

func QuotaToMicrousd(quota billing.MicroUSD) billing.MicroUSD {
	return quota
}

func MicrousdToQuota(microusd billing.MicroUSD) billing.MicroUSD {
	return microusd
}

func USDToMicrousd(value float64) billing.MicroUSD {
	return billing.MicroUSD(decimal.NewFromFloat(value).
		Mul(decimal.NewFromInt(billing.MicroUSDPerUSD)).
		Round(0).
		IntPart())
}

func AppendWalletLedgerEvent(userID int, deltaMicrousd billing.MicroUSD, kind, refType, refID string, pricingVersion int64, metadata map[string]any) error {
	if CoreDB() == nil {
		return nil
	}
	return appendWalletLedgerEvent(CoreDB(), userID, deltaMicrousd, kind, refType, refID, pricingVersion, metadata)
}

func adjustUserQuotaWithLedgerTx(tx *gorm.DB, input AdjustUserQuotaWithLedgerInput) error {
	if input.DeltaQuota == 0 {
		return nil
	}
	if tx == nil {
		return fmt.Errorf("database transaction is required")
	}
	if input.UserID == 0 {
		return fmt.Errorf("user id is required")
	}
	if GatewayLedgerEnabled() && input.DeltaQuota < 0 {
		if err := fenceExternalLedgerDebit(tx, input.UserID, -int64(input.DeltaQuota), input.AllowNegative); err != nil {
			return err
		}
	}

	tx = SkipIdentityCounterRefresh(tx)
	updates := map[string]interface{}{"quota": gorm.Expr("quota + ?", input.DeltaQuota)}
	if GatewayLedgerEnabled() {
		updates["credit_seq"] = gorm.Expr("credit_seq + 1")
	}
	if input.DeltaQuota < 0 && !input.AllowNegative {
		chargeQuota := -input.DeltaQuota
		userUpdate := tx.Model(&User{}).
			Where("id = ? AND quota >= ?", input.UserID, chargeQuota).
			Updates(updates)
		if userUpdate.Error != nil {
			return userUpdate.Error
		}
		if userUpdate.RowsAffected == 0 {
			return fmt.Errorf("user quota is not enough, need quota: %d", chargeQuota)
		}
	} else {
		userTx := tx
		if input.IncludeDeletedUser {
			userTx = tx.Unscoped()
		}
		userUpdate := userTx.Model(&User{}).
			Where("id = ?", input.UserID).
			Updates(updates)
		if userUpdate.Error != nil {
			return userUpdate.Error
		}
		if input.IncludeDeletedUser && userUpdate.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
	}

	metadata := cloneLedgerMetadata(input.Metadata)
	metadata["quota_delta"] = input.DeltaQuota
	if err := appendWalletLedgerEvent(
		tx,
		input.UserID,
		QuotaToMicrousd(input.DeltaQuota),
		input.Kind,
		input.RefType,
		input.RefID,
		input.PricingVersion,
		metadata,
	); err != nil {
		return err
	}
	if GatewayLedgerEnabled() {
		return recordLedgerCreditSyncTx(tx, input.UserID, int64(input.DeltaQuota))
	}
	return nil
}

func AdjustUserQuotaWithLedger(input AdjustUserQuotaWithLedgerInput) error {
	if input.DeltaQuota == 0 || CoreDB() == nil {
		return nil
	}
	userCacheSnapshot := captureUserQuotaCacheSnapshot(input.UserID)
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		return adjustUserQuotaWithLedgerTx(tx, input)
	})
	if err != nil {
		return err
	}
	updateUserQuotaCacheAfterCommitWithSnapshot(input.UserID, input.DeltaQuota, userCacheSnapshot)
	return nil
}

func updateUserQuotaCacheAfterCommit(userID int, delta billing.MicroUSD) {
	updateUserQuotaCacheAfterCommitWithSnapshot(userID, delta, nil)
}

func updateUserQuotaCacheAfterCommitWithSnapshot(userID int, delta billing.MicroUSD, snapshot *userQuotaCacheSnapshot) {
	if GatewayLedgerEnabled() {
		if err := SyncLedgerCreditsForUser(userID); err != nil {
			common.SysError(fmt.Sprintf("ledger credit sync user=%d: %s", userID, err))
		}
		return
	}
	if snapshot == nil {
		if err := invalidateUserCache(userID); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate user cache for user %d: %s", userID, err.Error()))
		}
		return
	}
	result, err := cacheAdjustUserQuotaIfUnchanged(userID, snapshot.quota, delta)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to update user cache for user %d: %s", userID, err.Error()))
		if err := invalidateUserCache(userID); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate user cache for user %d: %s", userID, err.Error()))
		}
		return
	}
	if result == cacheUpdateMismatch {
		if err := invalidateUserCache(userID); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate user cache for user %d: %s", userID, err.Error()))
		}
	}
}

func updateRelayQuotaCachesAfterCommit(userID int, tokenKey string, quotaDelta billing.MicroUSD, userSnapshot *userQuotaCacheSnapshot, tokenSnapshot *tokenQuotaCacheSnapshot) {
	userDelta := -quotaDelta
	updateUserQuotaCacheAfterCommitWithSnapshot(userID, userDelta, userSnapshot)
	if GatewayLedgerEnabled() {
		return
	}
	if tokenKey == "" {
		return
	}
	if tokenSnapshot == nil {
		if err := invalidateTokenQuotaCache(tokenKey); err != nil {
			common.SysLog("failed to invalidate token quota cache: " + err.Error())
		}
		return
	}
	if quotaDelta < 0 && tokenSnapshot.used < -quotaDelta {
		if err := invalidateTokenQuotaCache(tokenKey); err != nil {
			common.SysLog("failed to invalidate token quota cache: " + err.Error())
		}
		return
	}
	result, err := cacheAdjustTokenQuotaIfUnchanged(tokenKey, tokenSnapshot, userDelta, quotaDelta)
	if err != nil {
		common.SysLog("failed to update token cache: " + err.Error())
		if err := invalidateTokenQuotaCache(tokenKey); err != nil {
			common.SysLog("failed to invalidate token quota cache: " + err.Error())
		}
		return
	}
	if result == cacheUpdateMismatch {
		if err := invalidateTokenQuotaCache(tokenKey); err != nil {
			common.SysLog("failed to invalidate token quota cache: " + err.Error())
		}
	}
}

func applyTokenQuotaDeltaTx(tx *gorm.DB, tokenID int, tokenKey string, quotaDelta billing.MicroUSD, accepted ...bool) (string, error) {
	if len(accepted) > 0 && accepted[0] {
		tx = tx.Unscoped().Session(&gorm.Session{})
	}
	if quotaDelta == 0 {
		return tokenKey, nil
	}
	resolvedTokenID, resolvedTokenKey, err := resolveTokenQuotaTargetTx(tx, tokenID, tokenKey)
	if err != nil {
		return "", err
	}
	tokenKey = resolvedTokenKey
	query := tx.Model(&Token{})
	query = query.Where("id = ?", resolvedTokenID)

	now := common.GetTimestamp()
	if quotaDelta > 0 {
		update := query.
			Where("(unlimited_quota = ? OR remain_quota >= ?)", true, quotaDelta).
			Updates(map[string]any{
				"remain_quota": gorm.Expr("remain_quota - ?", quotaDelta),
				"used_quota":   gorm.Expr("used_quota + ?", quotaDelta),
				"last_used_at": now,
			})
		if update.Error != nil {
			return tokenKey, update.Error
		}
		if update.RowsAffected == 0 {
			return tokenKey, fmt.Errorf("token quota is not enough, need quota: %d", quotaDelta)
		}
		return tokenKey, nil
	}

	refundQuota := -quotaDelta
	update := query.Updates(map[string]any{
		"remain_quota": gorm.Expr("remain_quota + ?", refundQuota),
		"used_quota": gorm.Expr(
			"CASE WHEN used_quota >= ? THEN used_quota - ? ELSE 0 END",
			refundQuota,
			refundQuota,
		),
	})
	if update.Error != nil {
		return tokenKey, update.Error
	}
	if update.RowsAffected == 0 {
		return tokenKey, fmt.Errorf("token not found")
	}
	return tokenKey, nil
}

func resolveTokenQuotaTargetTx(tx *gorm.DB, tokenID int, tokenKey string) (int, string, error) {
	if tokenID > 0 {
		if tokenKey != "" {
			return tokenID, tokenKey, nil
		}
		var token Token
		if err := tx.Select("id", commonKeyCol, "key_digest", "key_prefix", "key_suffix").Where("id = ?", tokenID).First(&token).Error; err != nil {
			return 0, "", err
		}
		return token.Id, token.Key, nil
	}
	candidates := common.APIKeyLookupCandidates(tokenKey)
	if len(candidates) == 0 {
		return 0, "", fmt.Errorf("token id or token key is required")
	}
	digests := tokenKeyLookupDigests(candidates)
	for _, candidate := range candidates {
		var token Token
		err := tx.Select("id", commonKeyCol, "key_digest").Where("key_digest IN ? OR "+commonKeyCol+" = ?", digests, candidate).First(&token).Error
		if err == nil {
			return token.Id, common.NormalizeAPIKey(tokenKey), nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, "", err
		}
	}
	return 0, "", fmt.Errorf("token not found")
}

func AdjustRelayQuotaWithLedger(input AdjustRelayQuotaWithLedgerInput) error {
	if input.QuotaDelta == 0 || CoreDB() == nil {
		return nil
	}
	if input.UserID == 0 {
		return fmt.Errorf("user id is required")
	}

	kind := WalletLedgerKindSpend
	if input.QuotaDelta < 0 {
		kind = WalletLedgerKindRefund
	}
	metadata := cloneLedgerMetadata(input.Metadata)
	metadata["quota_delta"] = input.QuotaDelta

	var tokenCacheKey string
	userCacheSnapshot := captureUserQuotaCacheSnapshot(input.UserID)
	tokenCacheSnapshot := captureTokenQuotaCacheSnapshot(input.TokenKey)
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var err error
		tokenCacheKey, err = adjustRelayQuotaWithLedgerTx(tx, input, kind, metadata)
		return err
	})
	if err != nil {
		return err
	}

	if tokenCacheKey == "" {
		tokenCacheKey = input.TokenKey
	}
	updateRelayQuotaCachesAfterCommit(input.UserID, tokenCacheKey, input.QuotaDelta, userCacheSnapshot, tokenCacheSnapshot)
	return nil
}

func adjustRelayQuotaWithLedgerTx(tx *gorm.DB, input AdjustRelayQuotaWithLedgerInput, kind string, metadata map[string]any) (string, error) {
	if tx == nil || input.QuotaDelta == 0 {
		return input.TokenKey, nil
	}
	if input.QuotaDelta > 0 {
		userUpdate := tx.Model(&User{}).
			Where("id = ? AND quota >= ?", input.UserID, input.QuotaDelta).
			Update("quota", gorm.Expr("quota - ?", input.QuotaDelta))
		if userUpdate.Error != nil {
			return input.TokenKey, userUpdate.Error
		}
		if userUpdate.RowsAffected == 0 {
			return input.TokenKey, fmt.Errorf("user quota is not enough, need quota: %d", input.QuotaDelta)
		}
	} else {
		refundQuota := -input.QuotaDelta
		if err := tx.Model(&User{}).
			Where("id = ?", input.UserID).
			Update("quota", gorm.Expr("quota + ?", refundQuota)).Error; err != nil {
			return input.TokenKey, err
		}
	}

	tokenCacheKey := input.TokenKey
	if !input.IsPlayground {
		cacheKey, err := applyTokenQuotaDeltaTx(tx, input.TokenID, input.TokenKey, input.QuotaDelta, input.AcceptedSettlement)
		if err != nil {
			return tokenCacheKey, err
		}
		tokenCacheKey = cacheKey
	}

	err := appendWalletLedgerEvent(
		tx,
		input.UserID,
		-QuotaToMicrousd(input.QuotaDelta),
		kind,
		input.RefType,
		input.RefID,
		input.PricingVersion,
		metadata,
	)
	return tokenCacheKey, err
}

func PreConsumeQuotaWithLedger(input PreConsumeQuotaWithLedgerInput) error {
	if input.Quota < 0 {
		return fmt.Errorf("quota must not be negative")
	}
	if input.Quota == 0 || CoreDB() == nil {
		return nil
	}

	var tokenCacheKey string
	userCacheSnapshot := captureUserQuotaCacheSnapshot(input.UserID)
	tokenCacheSnapshot := captureTokenQuotaCacheSnapshot(input.TokenKey)
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := checkProjectPaymentHoldTx(tx, input.ProjectID); err != nil {
			return err
		}
		userUpdate := tx.Model(&User{}).
			Where("id = ? AND quota >= ?", input.UserID, input.Quota).
			Update("quota", gorm.Expr("quota - ?", input.Quota))
		if userUpdate.Error != nil {
			return userUpdate.Error
		}
		if userUpdate.RowsAffected == 0 {
			return fmt.Errorf("user quota is not enough, need quota: %d", input.Quota)
		}

		if !input.IsPlayground {
			cacheKey, err := applyTokenQuotaDeltaTx(tx, input.TokenID, input.TokenKey, input.Quota)
			if err != nil {
				return err
			}
			tokenCacheKey = cacheKey
		}

		return appendWalletLedgerEvent(
			tx,
			input.UserID,
			-QuotaToMicrousd(input.Quota),
			WalletLedgerKindSpend,
			input.RefType,
			input.RefID,
			input.PricingVersion,
			input.Metadata,
		)
	})
	if err != nil {
		return err
	}

	if tokenCacheKey == "" {
		tokenCacheKey = input.TokenKey
	}
	updateRelayQuotaCachesAfterCommit(input.UserID, tokenCacheKey, input.Quota, userCacheSnapshot, tokenCacheSnapshot)
	return nil
}

func cloneLedgerMetadata(metadata map[string]any) map[string]any {
	result := make(map[string]any, len(metadata)+1)
	for key, value := range metadata {
		result[key] = value
	}
	return result
}

func GetWalletLedgerBalanceMicrousd(userID int) (billing.MicroUSD, error) {
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&WalletLedger{}) {
		return 0, nil
	}
	var balance billing.MicroUSD
	err := CoreDB().Model(&WalletLedger{}).
		Where("user_id = ?", userID).
		Select("COALESCE(SUM(delta_microusd), 0)").
		Scan(&balance).Error
	return balance, err
}

// AccountLedgerEvent is the safe union presented by the admin drawer. String
// ids keep authoritative wallet rows and pre-ledger audit rows in one stable
// namespace without exposing their internal references.
type AccountLedgerEvent struct {
	ID            string
	DeltaMicrousd billing.MicroUSD
	Kind          string
	Action        string
	ActorUsername string
	CreatedAt     time.Time
}

func ledgerMetadataActorUsername(metadata string) string {
	if strings.TrimSpace(metadata) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(metadata), &payload); err != nil {
		return ""
	}
	actor, _ := payload["admin_username"].(string)
	return strings.TrimSpace(actor)
}

// legacyManagementDeltaMicrousd recovers the balance movement embedded in
// pre-ledger audit copy. Those rows predate amount_microusd, but their content
// was emitted by logger.LogQuota in one of two stable formats: USD or raw
// quota points. A "from ... to ..." event stores two balances, so its movement
// is the difference; a direct adjustment/refund stores one signed amount.
func legacyManagementDeltaMicrousd(content string) (billing.MicroUSD, bool) {
	parseMatches := func(pattern *regexp.Regexp, scale decimal.Decimal) ([]billing.MicroUSD, bool) {
		matches := pattern.FindAllStringSubmatch(content, -1)
		if len(matches) == 0 {
			return nil, false
		}
		values := make([]billing.MicroUSD, 0, len(matches))
		for _, match := range matches {
			value, err := decimal.NewFromString(match[1])
			if err != nil {
				return nil, false
			}
			values = append(values, billing.MicroUSD(value.Mul(scale).Round(0).IntPart()))
		}
		return values, true
	}

	values, ok := parseMatches(legacyQuotaUSDPattern, decimal.NewFromInt(int64(billing.MicroUSDPerUSD)))
	if !ok {
		values, ok = parseMatches(legacyQuotaPointPattern, decimal.NewFromInt(1))
	}
	if !ok || len(values) == 0 {
		return 0, false
	}
	if strings.Contains(content, "从") && strings.Contains(content, "修改为") {
		if len(values) < 2 {
			return 0, false
		}
		return values[1] - values[0], true
	}
	return values[0], true
}

// GetUserWalletLedgerEvents returns authoritative wallet events plus genuine
// pre-ledger top-up/manage/refund history. The opening-balance migration seed
// is never presented as an event, and legacy rows at/after the first real
// ledger event are excluded so dual-written operations cannot appear twice.
func GetUserWalletLedgerEvents(userID int, limit int) ([]AccountLedgerEvent, error) {
	rows := make([]AccountLedgerEvent, 0)
	if userID <= 0 {
		return rows, errors.New("invalid wallet ledger user")
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 100 {
		limit = 100
	}
	var cutoff int64
	if CoreDB() != nil && CoreDB().Migrator().HasTable(&WalletLedger{}) {
		var current []WalletLedger
		ledgerEventsQuery := func() *gorm.DB {
			return CoreDB().Model(&WalletLedger{}).
				Select("id", "delta_microusd", "kind", "metadata", "created_at").
				Where("user_id = ?", userID).
				Where("ref_type IS NULL OR ref_type <> ?", openingBalanceRefType).
				Where("kind IN ?", []string{WalletLedgerKindTopup, WalletLedgerKindBonus, WalletLedgerKindRefund, WalletLedgerKindAdjustment, WalletLedgerKindPaymentReversal})
		}
		if err := ledgerEventsQuery().Order("created_at DESC").Order("id DESC").Limit(limit).Find(&current).Error; err != nil {
			return nil, err
		}
		for _, row := range current {
			rows = append(rows, AccountLedgerEvent{
				ID: fmt.Sprintf("wallet:%d", row.ID), DeltaMicrousd: row.DeltaMicrousd,
				Kind: row.Kind, ActorUsername: ledgerMetadataActorUsername(row.Metadata), CreatedAt: row.CreatedAt,
			})
		}
		var first WalletLedger
		if err := CoreDB().Model(&WalletLedger{}).
			Select("id", "created_at").
			Where("user_id = ?", userID).
			Where("ref_type IS NULL OR ref_type <> ?", openingBalanceRefType).
			Order("created_at ASC").Order("id ASC").Limit(1).Find(&first).Error; err != nil {
			return nil, err
		}
		if first.ID != 0 {
			cutoff = first.CreatedAt.Unix()
		}
	}

	// Account mutations live in the admin audit table rather than the wallet
	// ledger. Merge the target-scoped, non-wallet events into the same safe
	// timeline so profile, tier, lifecycle, and role changes are not hidden by
	// a heading that promises both ledger and audit history. Quota adjustments
	// are excluded here because their authoritative wallet row is already in
	// the union above.
	if CoreDB() != nil && CoreDB().Migrator().HasTable(&AdminAuditLog{}) {
		var target struct {
			PublicID string
		}
		if err := CoreDB().Model(&User{}).Select("public_id").Where("id = ?", userID).Find(&target).Error; err != nil {
			return nil, err
		}
		if target.PublicID != "" {
			var auditRows []AdminAuditLog
			if err := CoreDB().Model(&AdminAuditLog{}).
				Select("id", "public_id", "actor_username", "action", "created_at").
				Where("target_type = ? AND target_id = ?", "user", target.PublicID).
				Where("action <> ?", "user.quota.adjust").
				Order("created_at DESC").Order("id DESC").Limit(limit).
				Find(&auditRows).Error; err != nil {
				return nil, err
			}
			for _, row := range auditRows {
				id := row.PublicID
				if id == "" {
					id = strconv.FormatUint(uint64(row.ID), 10)
				}
				rows = append(rows, AccountLedgerEvent{
					ID:            "audit:" + id,
					Kind:          "audit",
					Action:        row.Action,
					ActorUsername: row.ActorUsername,
					CreatedAt:     row.CreatedAt,
				})
			}
		}
	}

	if CoreDB() != nil && CoreDB().Migrator().HasTable(&TopUp{}) {
		var topups []TopUp
		query := CoreDB().Model(&TopUp{}).
			Where("user_id = ? AND status = ?", userID, common.TopUpStatusSuccess)
		if cutoff > 0 {
			query = query.Where("CASE WHEN complete_time > 0 THEN complete_time ELSE create_time END < ?", cutoff)
		}
		if err := query.Order("CASE WHEN complete_time > 0 THEN complete_time ELSE create_time END DESC").Limit(limit).Find(&topups).Error; err != nil {
			return nil, err
		}
		for _, row := range topups {
			amount := row.PaidCreditedMicrousd()
			created := row.CompleteTime
			if created <= 0 {
				created = row.CreateTime
			}
			rows = append(rows, AccountLedgerEvent{
				ID: fmt.Sprintf("topup:%d", row.Id), DeltaMicrousd: amount,
				Kind: WalletLedgerKindTopup, CreatedAt: time.Unix(created, 0),
			})
		}
	}

	if TelemetryDB() != nil && TelemetryDB().Migrator().HasTable(&Log{}) {
		var logs []Log
		query := TelemetryDB().Model(&Log{}).
			Where("user_id = ? AND type IN ?", userID, []int{LogTypeManage, LogTypeRefund})
		if cutoff > 0 {
			query = query.Where("created_at < ?", cutoff)
		}
		if err := query.Order("created_at DESC").Order("id DESC").Limit(limit).Find(&logs).Error; err != nil {
			return nil, err
		}
		for _, row := range logs {
			kind := WalletLedgerKindAdjustment
			if row.Type == LogTypeRefund {
				kind = WalletLedgerKindRefund
			}
			delta := row.AmountMicrousd
			if delta == 0 {
				if recovered, ok := legacyManagementDeltaMicrousd(row.Content); ok {
					delta = recovered
				}
			}
			rows = append(rows, AccountLedgerEvent{
				ID: fmt.Sprintf("log:%d", row.Id), DeltaMicrousd: delta,
				Kind: kind, CreatedAt: time.Unix(row.CreatedAt, 0),
			})
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// GetUserWalletLedgerRange returns authoritative daily wallet aggregates for
// the requested half-open interval together with the balance immediately
// before it. Rows are streamed in ledger order so high-volume accounts do not
// load or serialize every request movement.
func GetUserWalletLedgerRange(userID int, start, end time.Time, location *time.Location) (WalletLedgerRange, error) {
	result := WalletLedgerRange{Items: make([]WalletLedgerDailyMovement, 0)}
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&WalletLedger{}) {
		return result, nil
	}
	if userID <= 0 || !end.After(start) {
		return result, errors.New("invalid wallet ledger range")
	}
	if location == nil {
		location = time.UTC
	}

	// Upgrade seeds represent the entire pre-ledger balance, not a real credit
	// on the migration day. Count a seed as baseline whenever it exists before
	// this range ends, even when its created_at falls inside the selected range,
	// and exclude it from the daily movement stream below.
	if err := CoreDB().Model(&WalletLedger{}).
		Where("user_id = ?", userID).
		Where(
			"((created_at < ? AND (ref_type IS NULL OR ref_type <> ?)) OR (ref_type = ? AND created_at < ?))",
			start,
			openingBalanceRefType,
			openingBalanceRefType,
			end,
		).
		Select("COALESCE(SUM(delta_microusd), 0)").
		Scan(&result.OpeningBalanceMicrousd).Error; err != nil {
		return WalletLedgerRange{}, err
	}

	rows, err := CoreDB().Model(&WalletLedger{}).
		Select("delta_microusd", "created_at").
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, start, end).
		Where("ref_type IS NULL OR ref_type <> ?", openingBalanceRefType).
		Order("created_at ASC").
		Order("id ASC").
		Rows()
	if err != nil {
		return WalletLedgerRange{}, err
	}
	defer rows.Close()

	runningBalance := result.OpeningBalanceMicrousd
	for rows.Next() {
		var movement struct {
			DeltaMicrousd billing.MicroUSD
			CreatedAt     time.Time
		}
		if err := CoreDB().ScanRows(rows, &movement); err != nil {
			return WalletLedgerRange{}, err
		}

		date := movement.CreatedAt.In(location).Format("2006-01-02")
		if len(result.Items) == 0 || result.Items[len(result.Items)-1].Date != date {
			result.Items = append(result.Items, WalletLedgerDailyMovement{Date: date})
		}
		item := &result.Items[len(result.Items)-1]
		runningBalance += movement.DeltaMicrousd
		if movement.DeltaMicrousd >= 0 {
			item.CreditMicrousd += movement.DeltaMicrousd
		} else {
			item.SpendMicrousd -= movement.DeltaMicrousd
		}
		item.ClosingBalanceMicrousd = runningBalance
	}
	if err := rows.Err(); err != nil {
		return WalletLedgerRange{}, err
	}
	return result, nil
}

func ReconcileWalletLedgerForUser(userID int) (WalletLedgerReconciliation, error) {
	var user User
	if CoreDB() == nil {
		return WalletLedgerReconciliation{}, nil
	}
	if err := CoreDB().Select("id", "quota").First(&user, userID).Error; err != nil {
		return WalletLedgerReconciliation{}, err
	}
	ledgerMicrousd, err := GetWalletLedgerBalanceMicrousd(userID)
	if err != nil {
		return WalletLedgerReconciliation{}, err
	}
	cachedMicrousd := QuotaToMicrousd(user.Quota)
	return WalletLedgerReconciliation{
		UserID:         user.Id,
		CachedQuota:    user.Quota,
		CachedMicrousd: cachedMicrousd,
		LedgerMicrousd: ledgerMicrousd,
		DeltaMicrousd:  ledgerMicrousd - cachedMicrousd,
	}, nil
}

func ListWalletLedgerReconciliationMismatches(limit int) ([]WalletLedgerReconciliation, error) {
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&WalletLedger{}) {
		return nil, nil
	}
	type balanceRow struct {
		UserID         int
		CachedQuota    billing.MicroUSD
		LedgerMicrousd billing.MicroUSD
	}
	query := CoreDB().Table("users").
		Select("users.id AS user_id, users.quota AS cached_quota, COALESCE(SUM(wallet_ledgers.delta_microusd), 0) AS ledger_microusd").
		Joins("LEFT JOIN wallet_ledgers ON wallet_ledgers.user_id = users.id").
		Group("users.id, users.quota").
		Order("users.id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	var rows []balanceRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	mismatches := make([]WalletLedgerReconciliation, 0)
	for _, row := range rows {
		cachedMicrousd := QuotaToMicrousd(row.CachedQuota)
		delta := row.LedgerMicrousd - cachedMicrousd
		if delta == 0 {
			continue
		}
		mismatches = append(mismatches, WalletLedgerReconciliation{
			UserID:         row.UserID,
			CachedQuota:    row.CachedQuota,
			CachedMicrousd: cachedMicrousd,
			LedgerMicrousd: row.LedgerMicrousd,
			DeltaMicrousd:  delta,
		})
	}
	return mismatches, nil
}

func appendWalletLedgerEvent(tx *gorm.DB, userID int, deltaMicrousd billing.MicroUSD, kind, refType, refID string, pricingVersion int64, metadata map[string]any) error {
	if tx == nil || deltaMicrousd == 0 {
		return nil
	}
	if !tx.Migrator().HasTable(&WalletLedger{}) {
		return nil
	}
	if kind == "" {
		kind = WalletLedgerKindAdjustment
	}
	if refType == "" {
		refType = "manual"
	}
	if refID == "" {
		refID = fmt.Sprintf("%s:%d:%d", kind, userID, time.Now().UnixNano())
	}
	var metadataJSON string
	if len(metadata) > 0 {
		if raw, err := json.Marshal(metadata); err == nil {
			metadataJSON = string(raw)
		}
	}
	row := WalletLedger{
		UserID:         userID,
		DeltaMicrousd:  deltaMicrousd,
		Kind:           kind,
		RefType:        refType,
		RefID:          refID,
		PricingVersion: pricingVersion,
		Metadata:       metadataJSON,
	}
	return tx.Create(&row).Error
}

func usdToMicrousdPtr(value *float64) *billing.MicroUSD {
	if value == nil {
		return nil
	}
	result := billing.MicroUSD(decimal.NewFromFloat(*value).Mul(decimal.NewFromInt(billing.MicroUSDPerUSD)).Round(0).IntPart())
	return &result
}

func float64Ptr(value float64) *float64 {
	return &value
}

// NormalizePricingModelName folds provider-specific model variants onto the
// explicit sell-price row that should own their billing dimensions.
func NormalizePricingModelName(name string) string {
	name = normalizeThinkingModelSuffix(name)

	switch {
	case strings.HasPrefix(name, "gemini-2.5-flash-lite"):
		name = handleThinkingBudgetModel(name, "gemini-2.5-flash-lite", "gemini-2.5-flash-lite-thinking-*")
	case strings.HasPrefix(name, "gemini-2.5-flash"):
		name = handleThinkingBudgetModel(name, "gemini-2.5-flash", "gemini-2.5-flash-thinking-*")
	case strings.HasPrefix(name, "gemini-2.5-pro"):
		name = handleThinkingBudgetModel(name, "gemini-2.5-pro", "gemini-2.5-pro-thinking-*")
	}

	if strings.HasPrefix(name, "gpt-4-gizmo") {
		return "gpt-4-gizmo-*"
	}
	if strings.HasPrefix(name, "gpt-4o-gizmo") {
		return "gpt-4o-gizmo-*"
	}
	return name
}

// GetPricingChannelGroups lists every lane pricing may be written against — the
// registry in display order, stopped lanes included, because a stopped lane's
// prices and discounts stay live for the rows already carrying it.
func GetPricingChannelGroups() []string {
	return setting.KnownLaneSlugs()
}

func ContainsPricingChannelGroup(group string) bool {
	group = NormalizeChannelLane(group)
	if group == "" {
		return false
	}
	for _, candidate := range GetPricingChannelGroups() {
		if candidate == group {
			return true
		}
	}
	return false
}

func handleThinkingBudgetModel(name, prefix, wildcard string) string {
	if strings.HasPrefix(name, prefix) && strings.Contains(name, "-thinking-") {
		return wildcard
	}
	return name
}

func normalizeThinkingModelSuffix(name string) string {
	if strings.HasPrefix(name, "claude-") {
		if strings.HasSuffix(name, "#thinking") {
			return strings.TrimSuffix(name, "#thinking")
		}
		if strings.HasSuffix(name, "-thinking") {
			return strings.TrimSuffix(name, "-thinking")
		}
	}
	if strings.HasSuffix(name, "#thinking") {
		return strings.TrimSuffix(name, "#thinking") + "-thinking"
	}
	return name
}

// DeleteModelSellPricesForModel removes every stored price for one model, in
// one lane or in all of them. Until now the table was append-only from the
// console's point of view: there was no delete endpoint, so a price written by
// mistake could only be superseded, never removed, and the retired seeder
// re-created by hand whatever SQL had deleted.
//
// Deleting a price is not the same as pricing something at zero. A model with
// no price is refused at the relay, so this is the operator saying "we do not
// sell this", and the caller is expected to have checked that nothing still
// serves it.
func DeleteModelSellPricesForModel(modelName string, channelGroup string) ([]ModelSellPrice, error) {
	if CoreDB() == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	modelName = NormalizePricingModelName(strings.TrimSpace(modelName))
	if modelName == "" {
		return nil, types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "model_name"})
	}
	lane := strings.TrimSpace(channelGroup)
	if lane != "" {
		lane = NormalizeChannelLane(lane)
		if !IsKnownChannelLane(lane) {
			return nil, types.Err(types.ErrorCodeLaneNotFound, types.D{"lane": channelGroup})
		}
	}
	var removed []ModelSellPrice
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		query := tx.Where("model_name = ?", modelName)
		if lane != "" {
			query = query.Where("channel_group = ?", lane)
		}
		if err := query.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id ASC").Find(&removed).Error; err != nil {
			return err
		}
		ids := make([]uint, 0, len(removed))
		for _, row := range removed {
			ids = append(ids, row.ID)
		}
		if len(ids) == 0 {
			return nil
		}
		return tx.Where("id IN ?", ids).Delete(&ModelSellPrice{}).Error
	})
	if err != nil {
		return nil, err
	}
	// Refresh even on a retry that finds no rows: the preceding attempt may
	// have committed the delete but failed to replace the in-memory snapshot.
	if err := RefreshPricingSnapshot(); err != nil {
		return removed, fmt.Errorf("prices deleted, but pricing snapshot refresh failed: %w", err)
	}
	return removed, nil
}
