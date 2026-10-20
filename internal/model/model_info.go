package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Where a model fact came from. Anything not written by models.dev is kept
// when the daily sync disagrees; the disagreement is queued for review.
const (
	ModelInfoSourceModelsDev = "models.dev"
	ModelInfoSourceManual    = "manual"
	// Legacy marks values that existed before sources were tracked. Release
	// dates there were mostly the listing day, so the first sync offers to
	// replace them in one batch.
	ModelInfoSourceLegacy   = "legacy"
	ModelInfoSourceInferred = "inferred"
)

// Tracked model facts, keyed as the info-sources map and review rows name them.
const (
	ModelInfoFieldMaker            = "maker"
	ModelInfoFieldReleaseDate      = "release_date"
	ModelInfoFieldKnowledgeCutoff  = "knowledge_cutoff"
	ModelInfoFieldContextLength    = "context_length"
	ModelInfoFieldInputLimit       = "input_limit"
	ModelInfoFieldMaxOutputTokens  = "max_output_tokens"
	ModelInfoFieldOfficialPrice    = "official_price"
	ModelInfoFieldOfficialCache    = "official_cache_price"
	ModelInfoFieldOfficialTier     = "official_context_tier"
	ModelInfoFieldModalities       = "modalities"
	ModelInfoFieldCapabilities     = "capabilities"
	ModelInfoFieldReasoningOptions = "reasoning_options"
	ModelInfoFieldUnsupported      = "unsupported_params"
	ModelInfoFieldOpenWeights      = "open_weights"
)

// ModelReasoningOptions is how a model's thinking can be controlled: whether
// it can be switched off, the effort levels it accepts, lowest first, and,
// on Anthropic's Messages API, the shape its thinking controls take (Mode)
// and how it takes thinking turned off (Off). Toggle and Efforts come from
// models.dev; Mode and Off are lazu's own facts (reasoning_policy.go) and
// survive a models.dev sync.
type ModelReasoningOptions struct {
	Toggle  bool     `json:"toggle,omitempty"`
	Efforts []string `json:"efforts,omitempty"`
	// Mode is ReasoningModeBudget or ReasoningModeAdaptive; "" is unknown,
	// which every reader treats as the budget shape it sent before Mode
	// existed.
	Mode string `json:"mode,omitempty"`
	// Off is one of the ReasoningOff* values; "" is unknown, sent as asked.
	Off string `json:"off,omitempty"`
}

func (o ModelReasoningOptions) IsZero() bool {
	return !o.Toggle && len(o.Efforts) == 0 && o.Mode == "" && o.Off == ""
}

func (o ModelReasoningOptions) Value() (driver.Value, error) { return jsonColumnValue(o) }
func (o *ModelReasoningOptions) Scan(value interface{}) error {
	*o = ModelReasoningOptions{}
	return jsonColumnScan(value, o)
}

// ModelModalities lists what a model reads and writes: text, image, audio,
// video, pdf.
type ModelModalities struct {
	Input  []string `json:"input,omitempty"`
	Output []string `json:"output,omitempty"`
}

func (m ModelModalities) IsZero() bool { return len(m.Input) == 0 && len(m.Output) == 0 }

func (m ModelModalities) Value() (driver.Value, error) { return jsonColumnValue(m) }
func (m *ModelModalities) Scan(value interface{}) error {
	*m = ModelModalities{}
	return jsonColumnScan(value, m)
}

// ModelContextTier is a JSON column holding the lab's long-context list
// price. A zero threshold means the lab prices every length the same (or we
// have not learnt otherwise) and stores as an empty string.
type ModelContextTier ContextTierPrice

func (t ModelContextTier) IsZero() bool { return t.Threshold <= 0 }

// Price returns the tier as the catalog type, or nil when there is none.
func (t ModelContextTier) Price() *ContextTierPrice {
	if t.IsZero() {
		return nil
	}
	price := ContextTierPrice(t)
	return &price
}

func (t ModelContextTier) Value() (driver.Value, error) {
	if t.IsZero() {
		return "", nil
	}
	return jsonColumnValue(ContextTierPrice(t))
}

func (t *ModelContextTier) Scan(value interface{}) error {
	*t = ModelContextTier{}
	return jsonColumnScan(value, t)
}

// ModelStringList is a JSON array column.
type ModelStringList []string

func (l ModelStringList) Value() (driver.Value, error) {
	if l == nil {
		return "[]", nil
	}
	return jsonColumnValue([]string(l))
}
func (l *ModelStringList) Scan(value interface{}) error {
	*l = nil
	return jsonColumnScan(value, (*[]string)(l))
}

// ModelInfoSources maps a tracked field to where its value came from.
type ModelInfoSources map[string]string

func (s ModelInfoSources) Value() (driver.Value, error) {
	if s == nil {
		return "{}", nil
	}
	return jsonColumnValue(map[string]string(s))
}
func (s *ModelInfoSources) Scan(value interface{}) error {
	*s = nil
	return jsonColumnScan(value, (*map[string]string)(s))
}

// Get returns the source of a field, or "" when nothing is recorded.
func (s ModelInfoSources) Get(field string) string {
	if s == nil {
		return ""
	}
	return s[field]
}

func jsonColumnValue(v interface{}) (driver.Value, error) {
	bytes, err := common.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(bytes), nil
}

func jsonColumnScan(value interface{}, out interface{}) error {
	var bytes []byte
	switch v := value.(type) {
	case nil:
		return nil
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("unsupported type %T for JSON column", value)
	}
	if len(strings.TrimSpace(string(bytes))) == 0 {
		return nil
	}
	return common.Unmarshal(bytes, out)
}

// ModelIsNewRelease reports whether a model came out within the last two
// weeks — the plaza's 新发布 badge. It reads the release date, never the day
// the model was listed here.
func ModelIsNewRelease(releaseDate string, today string) bool {
	if len(releaseDate) != 10 || len(today) != 10 {
		return false
	}
	released, err1 := parseISODate(releaseDate)
	now, err2 := parseISODate(today)
	if err1 != nil || err2 != nil {
		return false
	}
	days := now.Sub(released).Hours() / 24
	return days >= 0 && days <= 14
}

func parseISODate(value string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.TrimSpace(value))
}

// NormalizeISODate accepts YYYY-MM-DD (or a longer timestamp starting with
// it) and returns the date part, or "" when it is not a date.
func NormalizeISODate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 10 {
		value = value[:10]
	}
	if _, err := parseISODate(value); err != nil {
		return ""
	}
	return value
}

// ModelsDevSyncLastRunOptionKey stores when the models.dev sync last ran
// (unix seconds), so the daily job runs once a day across restarts.
const ModelsDevSyncLastRunOptionKey = "ModelsDevSyncLastRun"

// migrateModelReleaseDates copies the old model_metadata.released_at onto the
// model as its release date, marked legacy: most of those timestamps were the
// day the model was listed here, and the first models.dev sync offers to
// replace them. Idempotent — only models without a release date are touched.
// migrateOfficialPricesToPerMillion repairs official prices the LiteLLM sync
// stored in USD per token (4e-06) instead of USD per million tokens (4). No
// real official price is below a thousandth of a dollar per million tokens,
// so anything under that is a per-token figure. Idempotent.
func migrateOfficialPricesToPerMillion(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Model{}) {
		return nil
	}
	for _, column := range []string{"official_input_price", "official_output_price"} {
		if !db.Migrator().HasColumn(&Model{}, column) {
			continue
		}
		if err := db.Model(&Model{}).
			Where(column+" > 0 AND "+column+" < ?", 0.001).
			Update(column, gorm.Expr(column+" * ?", 1_000_000)).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateModelReleaseDates(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Model{}) || !db.Migrator().HasTable(&ModelMetadata{}) ||
		!db.Migrator().HasColumn(&Model{}, "ReleaseDate") {
		return nil
	}
	var rows []Model
	if err := db.Select("id, model_name, info_sources").Where("release_date = ?", "").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	var metas []ModelMetadata
	if err := db.Select("model_name, released_at").Where("released_at IS NOT NULL").Find(&metas).Error; err != nil {
		return err
	}
	released := make(map[string]time.Time, len(metas))
	for _, meta := range metas {
		if meta.ReleasedAt != nil && !meta.ReleasedAt.IsZero() {
			released[meta.ModelName] = *meta.ReleasedAt
		}
	}
	for _, row := range rows {
		if row.InfoSources.Get(ModelInfoFieldReleaseDate) == ModelInfoSourceManual {
			continue
		}
		at, ok := released[row.ModelName]
		if !ok {
			continue
		}
		sources := ModelInfoSources{}
		for key, value := range row.InfoSources {
			sources[key] = value
		}
		sources[ModelInfoFieldReleaseDate] = ModelInfoSourceLegacy
		if err := db.Model(&Model{}).Where("id = ?", row.Id).Updates(map[string]interface{}{
			"release_date": at.UTC().Format("2006-01-02"),
			"info_sources": sources,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// modelInfoTrackedValue is a tracked field's comparable form on a model.
func modelInfoTrackedValue(m Model, field string) string {
	switch field {
	case ModelInfoFieldMaker:
		return m.Maker
	case ModelInfoFieldReleaseDate:
		return m.ReleaseDate
	case ModelInfoFieldKnowledgeCutoff:
		return m.KnowledgeCutoff
	case ModelInfoFieldContextLength:
		return fmt.Sprint(m.ContextLength)
	case ModelInfoFieldInputLimit:
		return fmt.Sprint(m.InputLimit)
	case ModelInfoFieldMaxOutputTokens:
		return fmt.Sprint(m.MaxOutputTokens)
	case ModelInfoFieldOfficialPrice:
		return fmt.Sprint(m.OfficialInputPrice, "/", m.OfficialOutputPrice)
	case ModelInfoFieldOfficialCache:
		return fmt.Sprint(m.OfficialCacheReadPerMtok, "/", m.OfficialCacheWritePerMtok)
	case ModelInfoFieldOfficialTier:
		if m.OfficialContextTier.IsZero() {
			return ""
		}
		raw, _ := common.Marshal(ContextTierPrice(m.OfficialContextTier))
		return string(raw)
	case ModelInfoFieldModalities:
		raw, _ := common.Marshal(m.Modalities)
		return string(raw)
	case ModelInfoFieldCapabilities:
		raw, _ := common.Marshal(m.Capabilities)
		return string(raw)
	case ModelInfoFieldReasoningOptions:
		raw, _ := common.Marshal(m.ReasoningOptions)
		return string(raw)
	case ModelInfoFieldUnsupported:
		return strings.Join(m.UnsupportedParams, ",")
	case ModelInfoFieldOpenWeights:
		return fmt.Sprint(m.OpenWeights)
	}
	return ""
}

func modelInfoTrackedIsSet(m Model, field string) bool {
	switch field {
	case ModelInfoFieldContextLength:
		return m.ContextLength > 0
	case ModelInfoFieldInputLimit:
		return m.InputLimit > 0
	case ModelInfoFieldMaxOutputTokens:
		return m.MaxOutputTokens > 0
	case ModelInfoFieldOfficialPrice:
		return m.OfficialInputPrice > 0 || m.OfficialOutputPrice > 0
	case ModelInfoFieldOfficialCache:
		return m.OfficialCacheReadPerMtok > 0 || m.OfficialCacheWritePerMtok > 0
	case ModelInfoFieldOfficialTier:
		return !m.OfficialContextTier.IsZero()
	case ModelInfoFieldModalities:
		return !m.Modalities.IsZero()
	case ModelInfoFieldCapabilities:
		return m.Capabilities.HasAny()
	case ModelInfoFieldReasoningOptions:
		return !m.ReasoningOptions.IsZero()
	case ModelInfoFieldUnsupported:
		return len(m.UnsupportedParams) > 0
	case ModelInfoFieldOpenWeights:
		return m.OpenWeights
	}
	return modelInfoTrackedValue(m, field) != ""
}

var modelInfoTrackedFields = []string{
	ModelInfoFieldMaker, ModelInfoFieldReleaseDate, ModelInfoFieldKnowledgeCutoff, ModelInfoFieldContextLength,
	ModelInfoFieldInputLimit, ModelInfoFieldMaxOutputTokens, ModelInfoFieldOfficialPrice, ModelInfoFieldOfficialCache,
	ModelInfoFieldOfficialTier, ModelInfoFieldModalities, ModelInfoFieldCapabilities, ModelInfoFieldReasoningOptions, ModelInfoFieldUnsupported, ModelInfoFieldOpenWeights,
}

// RegistrationInfoSources labels a new model's facts: the ones the form
// prefilled from models.dev and left alone come from there, anything else
// set was typed by the operator.
func RegistrationInfoSources(m Model, prefilled, manual []string) ModelInfoSources {
	fromUpstream := make(map[string]struct{}, len(prefilled))
	for _, field := range prefilled {
		fromUpstream[field] = struct{}{}
	}
	sources := ModelInfoSources{}
	manualSet := make(map[string]bool, len(manual))
	for _, field := range manual {
		manualSet[field] = true
	}
	for _, field := range modelInfoTrackedFields {
		if manualSet[field] {
			sources[field] = ModelInfoSourceManual
			continue
		}
		if _, ok := fromUpstream[field]; ok {
			sources[field] = ModelInfoSourceModelsDev
		} else if modelInfoTrackedIsSet(m, field) {
			sources[field] = ModelInfoSourceManual
		}
	}
	return sources
}

// NormalizeModelInfo validates facts before either registration or editing.
func NormalizeModelInfo(m *Model) error {
	date := NormalizeISODate(m.ReleaseDate)
	if strings.TrimSpace(m.ReleaseDate) != "" && date == "" {
		return fmt.Errorf("release_date must be YYYY-MM-DD")
	}
	m.ReleaseDate = date
	m.Maker = strings.ToLower(strings.TrimSpace(m.Maker))
	if m.Maker != "" {
		if _, known := ModelMakerByKey(m.Maker); !known {
			return fmt.Errorf("unknown maker %q", m.Maker)
		}
	}
	if m.InputLimit < 0 || m.ContextLength < 0 || m.MaxOutputTokens < 0 {
		return fmt.Errorf("model token limits cannot be negative")
	}
	return nil
}

// ConfirmedReleaseDate excludes the old listing dates until reviewed.
func (m Model) ConfirmedReleaseDate() string {
	if m.InfoSources.Get(ModelInfoFieldReleaseDate) == ModelInfoSourceLegacy {
		return ""
	}
	return NormalizeISODate(m.ReleaseDate)
}

// UpdateWithModelInfo saves the row, provenance and obsolete proposals together.
// Lock before taking the comparison snapshot so a concurrent sync cannot change it.
func (m *Model) UpdateWithModelInfo(sent map[string]json.RawMessage) error {
	if err := NormalizeModelInfo(m); err != nil {
		return err
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var before Model
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&before, m.Id).Error; err != nil {
			return err
		}
		if before.ModelName != m.ModelName {
			m.ModelsDevID = ""
		}
		if err := m.updateDB(tx); err != nil {
			return err
		}
		return applyModelInfoEditsDB(tx, before, *m, sent)
	})
}

// ApplyModelInfoEdits writes the model-info fields an edit form sent (the
// whole-record update skips them) and marks every fact the operator changed
// as manual, so the daily sync proposes rather than overwrites it.
func ApplyModelInfoEdits(before, after Model, sent map[string]json.RawMessage) error {
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		return applyModelInfoEditsDB(tx, before, after, sent)
	})
}

func applyModelInfoEditsDB(tx *gorm.DB, before, after Model, sent map[string]json.RawMessage) error {
	updates := map[string]interface{}{}
	if err := NormalizeModelInfo(&after); err != nil {
		return err
	}
	columns := map[string]interface{}{
		"maker":              after.Maker,
		"release_date":       after.ReleaseDate,
		"knowledge_cutoff":   strings.TrimSpace(after.KnowledgeCutoff),
		"input_limit":        after.InputLimit,
		"modalities":         after.Modalities,
		"reasoning_options":  after.ReasoningOptions,
		"unsupported_params": after.UnsupportedParams,
		"open_weights":       after.OpenWeights,
		"models_dev_id":      strings.TrimSpace(after.ModelsDevID),
		"info_locked":        after.InfoLocked,
	}
	for column, value := range columns {
		if _, ok := sent[column]; ok {
			updates[column] = value
		}
	}
	if before.ModelName != after.ModelName {
		// A pin identifies the old model, even when an older edit form resends it.
		updates["models_dev_id"] = ""
		for _, kind := range []string{ModelInfoReviewDiff, ModelInfoReviewFirstRun, ModelInfoReviewTierGap} {
			if err := ResolvePendingModelInfoReviewsDB(tx, kind, before.ModelName, ""); err != nil {
				return err
			}
		}
	}
	// Compare with the values actually written.
	merged := before
	if _, ok := sent["maker"]; ok {
		merged.Maker = after.Maker
	}
	if _, ok := sent["release_date"]; ok {
		merged.ReleaseDate = after.ReleaseDate
	}
	if _, ok := sent["knowledge_cutoff"]; ok {
		merged.KnowledgeCutoff = strings.TrimSpace(after.KnowledgeCutoff)
	}
	if _, ok := sent["input_limit"]; ok {
		merged.InputLimit = after.InputLimit
	}
	if _, ok := sent["modalities"]; ok {
		merged.Modalities = after.Modalities
	}
	if _, ok := sent["reasoning_options"]; ok {
		merged.ReasoningOptions = after.ReasoningOptions
	}
	if _, ok := sent["unsupported_params"]; ok {
		merged.UnsupportedParams = after.UnsupportedParams
	}
	if _, ok := sent["open_weights"]; ok {
		merged.OpenWeights = after.OpenWeights
	}
	merged.ContextLength = after.ContextLength
	merged.MaxOutputTokens = after.MaxOutputTokens
	merged.OfficialInputPrice = after.OfficialInputPrice
	merged.OfficialOutputPrice = after.OfficialOutputPrice
	merged.Capabilities = after.Capabilities

	sources := ModelInfoSources{}
	for key, value := range before.InfoSources {
		sources[key] = value
	}
	changed := false
	changedFields := []string{}
	for _, field := range modelInfoTrackedFields {
		if modelInfoTrackedValue(before, field) != modelInfoTrackedValue(merged, field) {
			sources[field] = ModelInfoSourceManual
			changed = true
			changedFields = append(changedFields, field)
		}
	}
	if changed {
		updates["info_sources"] = sources
	}
	if len(updates) == 0 {
		return nil
	}
	if err := tx.Model(&Model{}).Where("id = ?", before.Id).Updates(updates).Error; err != nil {
		return err
	}
	for _, field := range changedFields {
		for _, kind := range []string{ModelInfoReviewDiff, ModelInfoReviewFirstRun} {
			if err := ResolvePendingModelInfoReviewsDB(tx, kind, before.ModelName, field); err != nil {
				return err
			}
		}
	}
	return nil
}

// ResolveModelMakers fills MakerResolved on a page of models.
func ResolveModelMakers(models []*Model) {
	var vendors []Vendor
	_ = CoreDB().Select("id, name").Find(&vendors).Error
	vendorNames := make(map[int]string, len(vendors))
	for _, vendor := range vendors {
		vendorNames[vendor.Id] = vendor.Name
	}
	for _, m := range models {
		if m != nil {
			m.MakerResolved = ResolveModelMaker(m.Maker, m.ModelName, vendorNames[m.VendorID])
		}
	}
}

// ModelsDevSyncLastRun reads the last successful sync from the database so
// API and worker processes do not depend on their process-local option cache.
func ModelsDevSyncLastRun() (int64, error) {
	var row Option
	err := CoreDB().Where(&Option{Key: ModelsDevSyncLastRunOptionKey}).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	value, _ := strconv.ParseInt(row.Value, 10, 64)
	return value, nil
}

const modelsDevSyncLeaseKey = "ModelsDevSyncLease"

// ClaimModelsDevSync leases the sync across all API/worker processes. Failed
// runs release it; a crashed process expires after ten minutes. Scheduled
// runs skip only a successfully completed run from today.
func ClaimModelsDevSync(now time.Time, scheduled bool) (bool, error) {
	if CoreDB() == nil {
		return false, fmt.Errorf("database unavailable")
	}
	if scheduled {
		last, err := ModelsDevSyncLastRun()
		if err != nil {
			return false, err
		}
		at := time.Unix(last, 0).In(now.Location())
		if last > 0 && at.Year() == now.Year() && at.YearDay() == now.YearDay() {
			return false, nil
		}
	}
	var row Option
	err := CoreDB().Where(commonKeyCol+" = ?", modelsDevSyncLeaseKey).First(&row).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return false, err
	}
	previous, _ := strconv.ParseInt(row.Value, 10, 64)
	if previous > 0 && now.UnixNano()-previous < int64(10*time.Minute) {
		return false, nil
	}
	next := strconv.FormatInt(now.UnixNano(), 10)
	if err == gorm.ErrRecordNotFound {
		created := CoreDB().Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: modelsDevSyncLeaseKey, Value: next})
		return created.Error == nil && created.RowsAffected == 1, created.Error
	}
	claimed := CoreDB().Model(&Option{}).Where(commonKeyCol+" = ? AND value = ?", modelsDevSyncLeaseKey, row.Value).Update("value", next)
	return claimed.Error == nil && claimed.RowsAffected == 1, claimed.Error
}

func ReleaseModelsDevSync(now time.Time) error {
	return CoreDB().Model(&Option{}).Where(commonKeyCol+" = ? AND value = ?", modelsDevSyncLeaseKey, strconv.FormatInt(now.UnixNano(), 10)).Update("value", "").Error
}
