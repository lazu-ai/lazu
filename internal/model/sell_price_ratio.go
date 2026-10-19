package model

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// Sell prices that follow the official price.
//
// An operator prices a lane as a percent of the lab's list price: the lane
// carries a default ratio (setting.ChannelLane.PriceRatio) and a model's sell
// price may set its own. Billing never reads a ratio. A following row is an
// ordinary sell price whose numbers were derived and written as a new pricing
// version — when it is saved, when its lane's ratio changes and when the
// official price moves. Only the fields the official price carries are
// derived (input, output, cache read, cache write, the long-context tier);
// everything else on the row (1-hour cache write, audio, image, per call, per
// second, peak hours) is carried over untouched, and a field the operator
// pinned keeps its typed value.

const (
	SellPriceFieldInput        = "input"
	SellPriceFieldOutput       = "output"
	SellPriceFieldCacheRead    = "cache_read"
	SellPriceFieldCacheWrite5m = "cache_write_5m"
	SellPriceFieldContextTier  = "context_tier"
)

var sellPricePinnableFields = []string{
	SellPriceFieldInput,
	SellPriceFieldOutput,
	SellPriceFieldCacheRead,
	SellPriceFieldCacheWrite5m,
	SellPriceFieldContextTier,
}

// SellPriceSourceOfficialFollow marks a version written by a recompute rather
// than by a person.
const SellPriceSourceOfficialFollow = "official_follow"

// A present upstream number is not necessarily zero. Keep its explicit zero
// evidence distinct so catalogue defaults and rounded positive facts cannot
// become a free price merely because their field has models.dev provenance.
const ModelInfoSourceModelsDevExplicitZero = "models.dev:zero"

// NormalizePinnedFields drops unknown names and duplicates and returns the
// stored form, in a fixed order.
func NormalizePinnedFields(raw string) string {
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		seen[strings.TrimSpace(part)] = true
	}
	out := make([]string, 0, len(sellPricePinnableFields))
	for _, field := range sellPricePinnableFields {
		if seen[field] {
			out = append(out, field)
		}
	}
	return strings.Join(out, ",")
}

func pinnedFieldSet(raw string) map[string]bool {
	set := map[string]bool{}
	for _, field := range strings.Split(NormalizePinnedFields(raw), ",") {
		if field != "" {
			set[field] = true
		}
	}
	return set
}

// HasOfficialListPrice reports whether there is anything to follow.
func (price ModelOfficialPrice) HasOfficialListPrice() bool {
	return price.InputPerMtok() > 0 || price.OutputPerMtok() > 0 ||
		price.explicitZeroFields[SellPriceFieldInput] || price.explicitZeroFields[SellPriceFieldOutput]
}

// SellPriceRatio is the percent a following row is priced at.
func SellPriceRatio(row ModelSellPrice) float64 {
	if row.PriceRatio != nil && *row.PriceRatio > 0 {
		return *row.PriceRatio
	}
	return setting.LanePriceRatio(NormalizeChannelLane(row.ChannelGroup))
}

func roundSellPrice(value float64) float64 {
	return math.Round(value*1e6) / 1e6
}

// DeriveFollowingSellPrice returns the row with its unpinned official fields
// recomputed as official × ratio. A field the official price does not carry
// keeps the row's own value. The row is returned unchanged when it does not
// follow the official price.
func DeriveFollowingSellPrice(row ModelSellPrice, official ModelOfficialPrice) ModelSellPrice {
	if !row.FollowOfficial {
		return row
	}
	factor := SellPriceRatio(row) / 100
	pinned := pinnedFieldSet(row.PinnedFields)
	derive := func(field string, current *float64, list float64) *float64 {
		if pinned[field] || list < 0 || list == 0 && !official.explicitZeroFields[field] {
			return current
		}
		value := roundSellPrice(list * factor)
		return &value
	}
	out := row
	out.PinnedFields = NormalizePinnedFields(row.PinnedFields)
	out.InputPerMtok = derive(SellPriceFieldInput, row.InputPerMtok, official.InputPerMtok())
	out.OutputPerMtok = derive(SellPriceFieldOutput, row.OutputPerMtok, official.OutputPerMtok())
	out.CacheReadPerMtok = derive(SellPriceFieldCacheRead, row.CacheReadPerMtok, official.CacheReadPerMtok)
	out.CacheWrite5mPerMtok = derive(SellPriceFieldCacheWrite5m, row.CacheWrite5mPerMtok, official.CacheWritePerMtok)
	// The official price has no 1-hour cache write. When the 5-minute write
	// was derived, the 1-hour one keeps its ratio to it, so converting a fixed
	// Claude price to 50% does not leave the 1-hour write at the old price.
	if !pinned[SellPriceFieldCacheWrite5m] && (official.CacheWritePerMtok > 0 || official.explicitZeroFields[SellPriceFieldCacheWrite5m]) &&
		row.CacheWrite5mPerMtok != nil && *row.CacheWrite5mPerMtok > 0 &&
		row.CacheWrite1hPerMtok != nil && out.CacheWrite5mPerMtok != nil {
		value := roundSellPrice(*out.CacheWrite5mPerMtok * (*row.CacheWrite1hPerMtok / *row.CacheWrite5mPerMtok))
		out.CacheWrite1hPerMtok = &value
	}
	if !pinned[SellPriceFieldContextTier] {
		tier := official.ContextTier
		if tier == nil || tier.Threshold <= 0 {
			out.ContextTierThreshold = 0
			out.ContextTierInputPerMtok = nil
			out.ContextTierOutputPerMtok = nil
			out.ContextTierCacheReadPerMtok = nil
			out.ContextTierCacheWrite5mPerMtok = nil
			out.ContextTierCacheWrite1hPerMtok = nil
		} else {
			scale := func(list *float64) *float64 {
				if list == nil || *list < 0 {
					return nil
				}
				value := roundSellPrice(*list * factor)
				return &value
			}
			out.ContextTierThreshold = tier.Threshold
			out.ContextTierInputPerMtok = scale(tier.InputPerMtok)
			out.ContextTierOutputPerMtok = scale(tier.OutputPerMtok)
			out.ContextTierCacheReadPerMtok = scale(tier.CacheReadPerMtok)
			out.ContextTierCacheWrite5mPerMtok = scale(tier.CacheWrite5mPerMtok)
			out.ContextTierCacheWrite1hPerMtok = scale(tier.CacheWrite1hPerMtok)
		}
	}
	return out
}

func sellPriceValuesEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return math.Abs(*a-*b) <= 1e-9*math.Max(1, math.Abs(*a))
}

// SellPriceDerivedFieldsEqual reports whether two rows bill the same on the
// fields a recompute can change.
func SellPriceDerivedFieldsEqual(a, b ModelSellPrice) bool {
	return sellPriceValuesEqual(a.InputPerMtok, b.InputPerMtok) &&
		sellPriceValuesEqual(a.OutputPerMtok, b.OutputPerMtok) &&
		sellPriceValuesEqual(a.CacheReadPerMtok, b.CacheReadPerMtok) &&
		sellPriceValuesEqual(a.CacheWrite5mPerMtok, b.CacheWrite5mPerMtok) &&
		sellPriceValuesEqual(a.CacheWrite1hPerMtok, b.CacheWrite1hPerMtok) &&
		a.ContextTierThreshold == b.ContextTierThreshold &&
		sellPriceValuesEqual(a.ContextTierInputPerMtok, b.ContextTierInputPerMtok) &&
		sellPriceValuesEqual(a.ContextTierOutputPerMtok, b.ContextTierOutputPerMtok) &&
		sellPriceValuesEqual(a.ContextTierCacheReadPerMtok, b.ContextTierCacheReadPerMtok) &&
		sellPriceValuesEqual(a.ContextTierCacheWrite5mPerMtok, b.ContextTierCacheWrite5mPerMtok) &&
		sellPriceValuesEqual(a.ContextTierCacheWrite1hPerMtok, b.ContextTierCacheWrite1hPerMtok)
}

// OfficialPriceOfModel is the model's official list price, read from its
// catalog row rather than the pricing cache so a write that just changed it
// is seen.
func OfficialPriceOfModel(db *gorm.DB, modelName string) (ModelOfficialPrice, bool, error) {
	if db == nil {
		return ModelOfficialPrice{}, false, nil
	}
	var rows []Model
	if err := db.Where("model_name = ?", modelName).Limit(1).Find(&rows).Error; err != nil {
		return ModelOfficialPrice{}, false, err
	}
	if len(rows) == 0 {
		return ModelOfficialPrice{}, false, nil
	}
	price := officialPriceFromModel(rows[0])
	return price, price.HasOfficialListPrice(), nil
}

func officialPriceFromModel(m Model) ModelOfficialPrice {
	price := ModelOfficialPrice{
		InputPerToken:     m.OfficialInputPrice,
		OutputPerToken:    m.OfficialOutputPrice,
		CacheReadPerMtok:  m.OfficialCacheReadPerMtok,
		CacheWritePerMtok: m.OfficialCacheWritePerMtok,
		ContextTier:       m.OfficialContextTier.Price(),
	}
	for _, field := range []struct {
		name, group, column string
		value               float64
	}{
		{SellPriceFieldInput, ModelInfoFieldOfficialPrice, "official_input_price", m.OfficialInputPrice},
		{SellPriceFieldOutput, ModelInfoFieldOfficialPrice, "official_output_price", m.OfficialOutputPrice},
		{SellPriceFieldCacheRead, ModelInfoFieldOfficialCache, "official_cache_read_per_mtok", m.OfficialCacheReadPerMtok},
		{SellPriceFieldCacheWrite5m, ModelInfoFieldOfficialCache, "official_cache_write_per_mtok", m.OfficialCacheWritePerMtok},
	} {
		if field.value == 0 && m.InfoSources.Get(field.group) == ModelInfoSourceModelsDev && m.InfoSources.Get(field.column) == ModelInfoSourceModelsDevExplicitZero {
			if price.explicitZeroFields == nil {
				price.explicitZeroFields = make(map[string]bool)
			}
			price.explicitZeroFields[field.name] = true
		}
	}
	return price
}

// PrepareFollowingSellPrice fills a following row's derived fields before it
// is saved, reading the official price of catalogName (the row's own model
// name when empty). A model without an official list price has nothing to
// follow.
func PrepareFollowingSellPrice(db *gorm.DB, row ModelSellPrice, catalogName string) (ModelSellPrice, error) {
	if !row.FollowOfficial {
		row.PriceRatio = nil
		row.PinnedFields = ""
		return row, nil
	}
	if row.PriceRatio != nil && (math.IsNaN(*row.PriceRatio) || *row.PriceRatio <= 0 || *row.PriceRatio > 1000) {
		return row, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "price_ratio"})
	}
	if strings.TrimSpace(catalogName) == "" {
		catalogName = row.ModelName
	}
	official, ok, err := OfficialPriceOfModel(db, catalogName)
	if err != nil {
		return row, err
	}
	if !ok && catalogName != row.ModelName {
		official, ok, err = OfficialPriceOfModel(db, row.ModelName)
		if err != nil {
			return row, err
		}
	}
	if !ok {
		return row, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "follow_official"})
	}
	return DeriveFollowingSellPrice(row, official), nil
}

// SellPriceFollowChange is one row a recompute republished.
type SellPriceFollowChange struct {
	ModelName string
	Lane      string
	Before    ModelSellPrice
	After     ModelSellPrice
}

// Catalogue input/output columns accept per-token and per-million units.
// Converting equivalent decimal facts may differ by a few floating-point
// representable steps; tolerate only that arithmetic rounding, not a price
// delta or a difference in presence evidence.
func officialPriceUnitsEqual(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) || math.IsInf(a, 0) || math.IsInf(b, 0) {
		return false
	}
	if a == b {
		return true
	}
	if a == 0 || b == 0 {
		return false
	}
	magnitude := math.Max(math.Abs(a), math.Abs(b))
	upper := math.Nextafter(magnitude, math.Inf(1))
	spacing := upper - magnitude
	if math.IsInf(upper, 0) {
		spacing = magnitude - math.Nextafter(magnitude, 0)
	}
	return math.Abs(a-b) <= 2*spacing
}

// RecomputeFollowingSellPrices re-derives the active following rows of the
// given models (every model when empty) on the given lanes (every lane when
// empty) and publishes a new version of each row whose numbers moved.
func RecomputeFollowingSellPrices(models []string, lanes []string, reason string) ([]SellPriceFollowChange, error) {
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&ModelSellPrice{}) {
		return nil, nil
	}
	query := CoreDB().Where("active = ? AND follow_official = ?", true, true)
	if len(models) > 0 {
		names := make([]string, 0, len(models))
		for _, name := range models {
			names = append(names, NormalizePricingModelName(name))
		}
		query = query.Where("model_name IN ?", names)
	}
	if len(lanes) > 0 {
		query = query.Where("channel_group IN ?", lanes)
	}
	var rows []ModelSellPrice
	if err := query.Order("model_name ASC, channel_group ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.ModelName] = true
	}
	var catalog []Model
	// A stored price owns normalized variants. Read the catalogue on this
	// explicit administration path and apply the exact same normalization;
	// SQL equality with only the stored name would omit variant-only rows.
	if err := CoreDB().Select("model_name", "official_input_price", "official_output_price", "official_cache_read_per_mtok", "official_cache_write_per_mtok", "official_context_tier", "info_sources").Order("id ASC").Find(&catalog).Error; err != nil {
		return nil, err
	}
	officials := make(map[string]ModelOfficialPrice, len(catalog))
	for _, m := range catalog {
		name := NormalizePricingModelName(m.ModelName)
		if !seen[name] {
			continue
		}
		price := officialPriceFromModel(m)
		if !price.HasOfficialListPrice() {
			continue
		}
		// Compare catalogue units consistently and refuse distinct official
		// facts sharing one stored owner. No alias may silently pick a price.
		if previous, exists := officials[name]; exists {
			if !officialPriceUnitsEqual(previous.InputPerMtok(), price.InputPerMtok()) ||
				!officialPriceUnitsEqual(previous.OutputPerMtok(), price.OutputPerMtok()) {
				return nil, types.Err(types.ErrorCodeVersionConflict)
			}
			comparable := price
			comparable.InputPerToken, comparable.OutputPerToken = 0, 0
			previous.InputPerToken, previous.OutputPerToken = 0, 0
			if !reflect.DeepEqual(previous, comparable) {
				return nil, types.Err(types.ErrorCodeVersionConflict)
			}
		}
		officials[name] = price
	}

	changes := make([]SellPriceFollowChange, 0)
	for _, row := range rows {
		official, ok := officials[row.ModelName]
		if !ok || !official.HasOfficialListPrice() {
			continue
		}
		next := DeriveFollowingSellPrice(row, official)
		if SellPriceDerivedFieldsEqual(row, next) {
			continue
		}
		if err := validateModelSellPrice(next); err != nil {
			common.SysLog(fmt.Sprintf("sell price follow skipped %s/%s: %v", row.ModelName, row.ChannelGroup, err))
			continue
		}
		changes = append(changes, SellPriceFollowChange{ModelName: row.ModelName, Lane: row.ChannelGroup, Before: row, After: next})
	}
	if len(changes) == 0 {
		return nil, nil
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].ModelName < changes[j].ModelName })

	version := time.Now().UnixNano()
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		for i := range changes {
			next := changes[i].After
			next.ID = 0
			next.Active = true
			next.Source = SellPriceSourceOfficialFollow
			next.PricingVersion = version + int64(i)
			next.CreatedAt = time.Time{}
			next.UpdatedAt = time.Time{}
			result := tx.Model(&ModelSellPrice{}).
				Where("id = ? AND active = ?", changes[i].Before.ID, true).
				Update("active", false)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				// Someone published this lane meanwhile; their row stands.
				return types.Err(types.ErrorCodeVersionConflict)
			}
			if err := tx.Create(&next).Error; err != nil {
				return err
			}
			changes[i].After = next
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, change := range changes {
		common.SysLog(fmt.Sprintf("sell price followed official (%s): %s/%s input %s→%s output %s→%s",
			reason, change.ModelName, change.Lane,
			formatOptionalPrice(change.Before.InputPerMtok), formatOptionalPrice(change.After.InputPerMtok),
			formatOptionalPrice(change.Before.OutputPerMtok), formatOptionalPrice(change.After.OutputPerMtok)))
	}
	if refreshErr := RefreshPricingSnapshot(); refreshErr != nil {
		common.SysLog("failed to refresh pricing snapshot after sell price follow: " + refreshErr.Error())
	}
	return changes, nil
}

func formatOptionalPrice(value *float64) string {
	if value == nil {
		return "—"
	}
	return fmt.Sprintf("%g", *value)
}

// RecomputeFollowingSellPricesLogged is for triggers that must not
// fail their own request when the follow-up recompute does.
func RecomputeFollowingSellPricesLogged(models []string, lanes []string, reason string) {
	if _, err := RecomputeFollowingSellPrices(models, lanes, reason); err != nil {
		common.SysLog(fmt.Sprintf("sell price follow failed (%s): %v", reason, err))
	}
}

// CountSellPriceModesInLane counts the models priced on a lane by whether
// their active price follows the official price or is fixed.
func CountSellPriceModesInLane(lane string) (following int64, fixed int64, err error) {
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&ModelSellPrice{}) {
		return 0, 0, nil
	}
	lane = NormalizeChannelLane(lane)
	if err = CoreDB().Model(&ModelSellPrice{}).Where("channel_group = ? AND active = ? AND follow_official = ?", lane, true, true).Count(&following).Error; err != nil {
		return 0, 0, err
	}
	err = CoreDB().Model(&ModelSellPrice{}).Where("channel_group = ? AND active = ? AND follow_official = ?", lane, true, false).Count(&fixed).Error
	return following, fixed, err
}
