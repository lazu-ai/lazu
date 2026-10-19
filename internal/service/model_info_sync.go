package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ModelInfoSyncResult summarises one models.dev sync.
type ModelInfoSyncResult struct {
	RunAt       int64 `json:"run_at"`
	Matched     int   `json:"matched"`
	Filled      int   `json:"filled"`
	Updated     int   `json:"updated"`
	Diffs       int   `json:"diffs"`
	FirstRun    int   `json:"first_run"`
	NewReleases int   `json:"new_releases"`
	TierGaps    int   `json:"tier_gaps"`
	// Followed counts official prices taken from upstream; the sell prices
	// that follow them are repriced after the run.
	Followed int `json:"followed"`
}

// ModelInfoPrefill is what a new model's registration form is filled with.
type ModelInfoPrefill struct {
	ModelName             string                      `json:"model_name"`
	Name                  string                      `json:"name,omitempty"`
	Maker                 string                      `json:"maker,omitempty"`
	ModelsDevID           string                      `json:"models_dev_id,omitempty"`
	Description           string                      `json:"description,omitempty"`
	ReleaseDate           string                      `json:"release_date,omitempty"`
	KnowledgeCutoff       string                      `json:"knowledge_cutoff,omitempty"`
	ContextLength         int64                       `json:"context_length,omitempty"`
	InputLimit            int64                       `json:"input_limit,omitempty"`
	MaxOutputTokens       int64                       `json:"max_output_tokens,omitempty"`
	OfficialInputPerMtok  *float64                    `json:"official_input_per_mtok,omitempty"`
	OfficialOutputPerMtok *float64                    `json:"official_output_per_mtok,omitempty"`
	CacheReadPerMtok      *float64                    `json:"cache_read_per_mtok,omitempty"`
	CacheWritePerMtok     *float64                    `json:"cache_write_per_mtok,omitempty"`
	Modalities            model.ModelModalities       `json:"modalities"`
	ReasoningOptions      model.ModelReasoningOptions `json:"reasoning_options"`
	UnsupportedParams     []string                    `json:"unsupported_params"`
	OpenWeights           bool                        `json:"open_weights"`
	Capabilities          model.ModelCapabilities     `json:"capabilities"`
	ContextTier           *model.ContextTierPrice     `json:"context_tier,omitempty"`
	// Filled lists the prefill fields that carry a value, for the form's
	// "已填入 N 项" line.
	Filled []string `json:"filled"`
}

// modelInfoPricePair is the official input/output price per million tokens.
type modelInfoPricePair struct {
	Input  float64 `json:"input_per_mtok"`
	Output float64 `json:"output_per_mtok"`
}

// Omitted dimensions are unknown upstream and must leave local prices intact.
type modelInfoPriceUpdate struct {
	Input  *float64 `json:"input_per_mtok,omitempty"`
	Output *float64 `json:"output_per_mtok,omitempty"`
}

// modelInfoCachePriceUpdate is the official cache read / cache write price
// per million tokens. Omitted dimensions leave local prices intact.
type modelInfoCachePriceUpdate struct {
	CacheRead  *float64 `json:"cache_read_per_mtok,omitempty"`
	CacheWrite *float64 `json:"cache_write_per_mtok,omitempty"`
}

var modelInfoSyncMu sync.Mutex

// releaseDateTolerance is how far two release dates may sit apart before it
// is worth an operator's attention: labs and catalogs routinely disagree by a
// day around time zones and staged rollouts.
const releaseDateToleranceDays = 2

// newReleaseWindowDays is how far back a lab release still counts as new.
const newReleaseWindowDays = 30

// RunModelInfoSync pulls models.dev and reconciles every catalog model with
// it. Empty facts are filled and logged for undo, disagreements on facts that
// affect billing or access are queued, releases we do not list are queued,
// and models whose lab prices long inputs higher are flagged.
func RunModelInfoSync(ctx context.Context, scheduled bool) (ModelInfoSyncResult, error) {
	if !modelInfoSyncMu.TryLock() {
		return ModelInfoSyncResult{}, types.Err(types.ErrorCodeStateConflict)
	}
	defer modelInfoSyncMu.Unlock()

	started := time.Now()
	claimed, err := model.ClaimModelsDevSync(started, scheduled)
	if err != nil {
		return ModelInfoSyncResult{}, err
	}
	if !claimed {
		if scheduled {
			return ModelInfoSyncResult{}, nil
		}
		return ModelInfoSyncResult{}, types.Err(types.ErrorCodeStateConflict)
	}
	defer func() {
		if err := model.ReleaseModelsDevSync(started); err != nil {
			logModelsDev("release lease: " + err.Error())
		}
	}()
	// Recheck after acquiring the lease: another process may have completed
	// between the initial daily check and our successful compare-and-set.
	if scheduled {
		last, err := model.ModelsDevSyncLastRun()
		if err != nil {
			return ModelInfoSyncResult{}, err
		}
		if last > 0 && time.Unix(last, 0).In(started.Location()).Format("2006-01-02") == started.Format("2006-01-02") {
			return ModelInfoSyncResult{}, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	catalog, err := FetchModelsDevCatalog(ctx)
	if err != nil {
		return ModelInfoSyncResult{}, err
	}
	return reconcileModelInfo(NewModelsDevIndex(catalog), time.Now())
}

func reconcileModelInfo(index *ModelsDevIndex, now time.Time) (ModelInfoSyncResult, error) {
	result := ModelInfoSyncResult{RunAt: now.Unix()}
	repriced := make([]string, 0)
	vendorNames := loadVendorNames()
	lanes := model.GetActiveModelSellPriceLaneMap()
	missing, err := model.GetMissingModels()
	if err != nil {
		return result, err
	}
	// All automatic facts, proposals and the success marker share one commit.
	// A failed run must not leave fills that the latest-run undo UI cannot see.
	err = model.CoreDB().Transaction(func(tx *gorm.DB) error {
		var models []model.Model
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id ASC").Find(&models).Error; err != nil {
			return err
		}
		localNames := make(map[string]struct{}, len(models))
		for _, m := range models {
			for _, candidate := range modelsDevCandidates(m.ModelName) {
				localNames[candidate] = struct{}{}
			}
		}
		for i := range models {
			m := &models[i]
			maker := model.ResolveModelMaker(m.Maker, m.ModelName, vendorNames[m.VendorID])
			entry, ok := index.Lookup(m.ModelName, maker, m.ModelsDevID)
			if !ok {
				for _, kind := range []string{model.ModelInfoReviewDiff, model.ModelInfoReviewFirstRun, model.ModelInfoReviewTierGap} {
					if err := model.ResolvePendingModelInfoReviewsDB(tx, kind, m.ModelName, ""); err != nil {
						return err
					}
				}
				continue
			}
			result.Matched++
			if maker == "" {
				maker = makerForModelsDevProvider(entry.ProviderID)
			}
			reconciler := newModelInfoReconciler(m, maker, entry, result.RunAt)
			reconciler.db = tx
			reconciler.run()
			if err := reconciler.save(); err != nil {
				return fmt.Errorf("save %s: %w", m.ModelName, err)
			}
			result.Filled += reconciler.filled
			result.Updated += reconciler.updated
			result.Diffs += reconciler.diffs
			result.FirstRun += reconciler.firstRun
			result.Followed += reconciler.followed
			if reconciler.officialChanged {
				repriced = append(repriced, m.ModelName)
			}
			gap, err := recordTierGap(tx, m.ModelName, maker, entry, lanes[m.ModelName], result.RunAt)
			if err != nil {
				return err
			}
			if gap {
				result.TierGaps++
			}
		}
		var err error
		result.NewReleases, err = recordNewReleases(tx, index, missing, localNames, now, result.RunAt)
		if err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&model.Option{Key: model.ModelsDevSyncLastRunOptionKey, Value: strconv.FormatInt(result.RunAt, 10)}).Error
	})
	if err != nil {
		return ModelInfoSyncResult{RunAt: result.RunAt}, err
	}
	model.RefreshPricing()
	if len(repriced) > 0 {
		model.RecomputeFollowingSellPricesLogged(repriced, nil, "models.dev")
	}
	logModelsDev(fmt.Sprintf("synced: matched %d, filled %d, updated %d, diffs %d, first-run %d, new %d, tier gaps %d, followed %d",
		result.Matched, result.Filled, result.Updated, result.Diffs, result.FirstRun, result.NewReleases, result.TierGaps, result.Followed))
	return result, nil
}

func loadVendorNames() map[int]string {
	var vendors []model.Vendor
	_ = model.CoreDB().Find(&vendors).Error
	names := make(map[int]string, len(vendors))
	for _, vendor := range vendors {
		names[vendor.Id] = vendor.Name
	}
	return names
}

func makerForModelsDevProvider(providerID string) string {
	for _, maker := range model.KnownModelMakers() {
		for _, id := range maker.ModelsDevProviders {
			if id == providerID {
				return maker.Key
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// One model's reconciliation.

type modelInfoFieldClass int

const (
	// Descriptive facts follow upstream unless an operator typed them.
	fieldDescriptive modelInfoFieldClass = iota
	// Guarded facts change what a request costs or accepts; a disagreement
	// is only ever proposed, never applied.
	fieldGuarded
	// The official list price follows upstream unless an operator typed it
	// (or locked the model); sell prices that follow it are repriced.
	fieldOfficial
	// The release date: legacy values are proposed as a first-run batch,
	// synced values follow upstream, typed values are proposed.
	fieldRelease
)

type modelInfoReconciler struct {
	db       *gorm.DB
	err      error
	m        *model.Model
	maker    string
	entry    ModelsDevEntry
	syncRun  int64
	sources  model.ModelInfoSources
	updates  map[string]interface{}
	filled   int
	updated  int
	diffs    int
	firstRun int
	followed int
	// officialChanged also covers newly proven zeroes that following prices
	// can now derive, even when the stored catalogue number was unchanged.
	officialChanged bool
}

func newModelInfoReconciler(m *model.Model, maker string, entry ModelsDevEntry, syncRun int64) *modelInfoReconciler {
	sources := model.ModelInfoSources{}
	for key, value := range m.InfoSources {
		sources[key] = value
	}
	return &modelInfoReconciler{db: model.CoreDB(), m: m, maker: maker, entry: entry, syncRun: syncRun, sources: sources, updates: map[string]interface{}{}}
}

// fieldSpec describes one tracked fact of the model being reconciled.
type fieldSpec struct {
	field    string
	class    modelInfoFieldClass
	local    string // display form, "" when unset
	upstream string // display form, "" when upstream says nothing
	same     bool
	// apply writes the upstream value into r.updates (and r.m).
	apply func()
	// payload is the upstream value an accepted review writes.
	payload interface{}
}

func (r *modelInfoReconciler) run() {
	up := r.entry.Model
	m := r.m

	r.reconcile(fieldSpec{
		field: model.ModelInfoFieldMaker, class: fieldDescriptive,
		local: m.Maker, upstream: r.maker, same: m.Maker == r.maker,
		apply:   func() { r.set("maker", r.maker, func() { m.Maker = r.maker }) },
		payload: r.maker,
	})
	if m.ModelsDevID != r.entry.ID() {
		r.set("models_dev_id", r.entry.ID(), func() { m.ModelsDevID = r.entry.ID() })
	}

	release := model.NormalizeISODate(up.ReleaseDate)
	r.reconcile(fieldSpec{
		field: model.ModelInfoFieldReleaseDate, class: fieldRelease,
		local: m.ReleaseDate, upstream: release,
		same:    daysApart(m.ReleaseDate, release) <= releaseDateToleranceDays,
		apply:   func() { r.set("release_date", release, func() { m.ReleaseDate = release }) },
		payload: release,
	})

	knowledge := normalizeKnowledge(up.Knowledge)
	r.reconcile(fieldSpec{
		field: model.ModelInfoFieldKnowledgeCutoff, class: fieldDescriptive,
		local: m.KnowledgeCutoff, upstream: knowledge, same: m.KnowledgeCutoff == knowledge,
		apply:   func() { r.set("knowledge_cutoff", knowledge, func() { m.KnowledgeCutoff = knowledge }) },
		payload: knowledge,
	})

	// Context: a local value equal to upstream's input limit is the same fact
	// under the older reading (input window rather than total window).
	contextSame := limitsClose(m.ContextLength, up.Limit.Context) || (up.Limit.Input > 0 && m.ContextLength == up.Limit.Input)
	r.reconcile(fieldSpec{
		field: model.ModelInfoFieldContextLength, class: fieldGuarded,
		local: intDisplay(m.ContextLength), upstream: intDisplay(up.Limit.Context), same: contextSame,
		apply:   func() { r.set("context_length", up.Limit.Context, func() { m.ContextLength = up.Limit.Context }) },
		payload: up.Limit.Context,
	})
	r.reconcile(fieldSpec{
		field: model.ModelInfoFieldInputLimit, class: fieldDescriptive,
		local: intDisplay(m.InputLimit), upstream: intDisplay(up.Limit.Input), same: m.InputLimit == up.Limit.Input,
		apply:   func() { r.set("input_limit", up.Limit.Input, func() { m.InputLimit = up.Limit.Input }) },
		payload: up.Limit.Input,
	})
	r.reconcile(fieldSpec{
		field: model.ModelInfoFieldMaxOutputTokens, class: fieldGuarded,
		local: intDisplay(m.MaxOutputTokens), upstream: intDisplay(up.Limit.Output), same: limitsClose(m.MaxOutputTokens, up.Limit.Output),
		apply: func() {
			r.set("max_output_tokens", up.Limit.Output, func() { m.MaxOutputTokens = up.Limit.Output })
		},
		payload: up.Limit.Output,
	})

	if up.Cost != nil && (up.Cost.Input != nil || up.Cost.Output != nil) {
		localPrice := modelInfoPricePair{Input: officialPerMtok(m.OfficialInputPrice), Output: officialPerMtok(m.OfficialOutputPrice)}
		upPrice := localPrice
		if up.Cost.Input != nil {
			upPrice.Input = *up.Cost.Input
		}
		if up.Cost.Output != nil {
			upPrice.Output = *up.Cost.Output
		}
		localDisplay := ""
		if m.OfficialInputPrice > 0 || m.OfficialOutputPrice > 0 {
			localDisplay = priceDisplay(localPrice)
		}
		r.reconcile(fieldSpec{
			field: model.ModelInfoFieldOfficialPrice, class: fieldOfficial,
			local: localDisplay, upstream: priceDisplay(upPrice),
			same: floatsClose(localPrice.Input, upPrice.Input) && floatsClose(localPrice.Output, upPrice.Output),
			apply: func() {
				if up.Cost.Input != nil {
					r.set("official_input_price", upPrice.Input/1e6, func() { m.OfficialInputPrice = upPrice.Input / 1e6 })
				}
				if up.Cost.Output != nil {
					r.set("official_output_price", upPrice.Output/1e6, func() { m.OfficialOutputPrice = upPrice.Output / 1e6 })
				}
			},
			payload: modelInfoPriceUpdate{Input: up.Cost.Input, Output: up.Cost.Output},
		})
		r.recordOfficialPricePresence(model.ModelInfoFieldOfficialPrice, "official_input_price", up.Cost.Input, officialPerMtok(m.OfficialInputPrice))
		r.recordOfficialPricePresence(model.ModelInfoFieldOfficialPrice, "official_output_price", up.Cost.Output, officialPerMtok(m.OfficialOutputPrice))
	} else {
		r.resolveProposals(model.ModelInfoFieldOfficialPrice)
	}

	// Cache is its own field so models that already carry an input/output
	// price get it filled rather than proposed. Labs price cache reads at
	// different fractions of input (OpenAI 10%, Anthropic 5%, DeepSeek 2%),
	// so it cannot be derived from the input price.
	if up.Cost != nil && (up.Cost.CacheRead != nil || up.Cost.CacheWrite != nil) {
		localRead, localWrite := m.OfficialCacheReadPerMtok, m.OfficialCacheWritePerMtok
		upRead, upWrite := localRead, localWrite
		if up.Cost.CacheRead != nil {
			upRead = *up.Cost.CacheRead
		}
		if up.Cost.CacheWrite != nil {
			upWrite = *up.Cost.CacheWrite
		}
		r.reconcile(fieldSpec{
			field: model.ModelInfoFieldOfficialCache, class: fieldOfficial,
			local: cachePriceDisplay(localRead, localWrite), upstream: cachePriceDisplay(upRead, upWrite),
			same: floatsClose(localRead, upRead) && floatsClose(localWrite, upWrite),
			apply: func() {
				if up.Cost.CacheRead != nil {
					r.set("official_cache_read_per_mtok", upRead, func() { m.OfficialCacheReadPerMtok = upRead })
				}
				if up.Cost.CacheWrite != nil {
					r.set("official_cache_write_per_mtok", upWrite, func() { m.OfficialCacheWritePerMtok = upWrite })
				}
			},
			payload: modelInfoCachePriceUpdate{CacheRead: up.Cost.CacheRead, CacheWrite: up.Cost.CacheWrite},
		})
		r.recordOfficialPricePresence(model.ModelInfoFieldOfficialCache, "official_cache_read_per_mtok", up.Cost.CacheRead, m.OfficialCacheReadPerMtok)
		r.recordOfficialPricePresence(model.ModelInfoFieldOfficialCache, "official_cache_write_per_mtok", up.Cost.CacheWrite, m.OfficialCacheWritePerMtok)
	} else {
		r.resolveProposals(model.ModelInfoFieldOfficialCache)
	}

	// The lab's long-context list price. Guarded like the other official
	// prices: it decides what the receipt compares a long request against.
	if upTier := modelsDevContextTier(up); upTier != nil {
		localTier := model.ContextTierPrice(m.OfficialContextTier)
		r.reconcile(fieldSpec{
			field: model.ModelInfoFieldOfficialTier, class: fieldOfficial,
			local: contextTierDisplay(m.OfficialContextTier.Price()), upstream: contextTierDisplay(upTier),
			same: contextTiersClose(&localTier, upTier),
			apply: func() {
				value := model.ModelContextTier(*upTier)
				r.set("official_context_tier", value, func() { m.OfficialContextTier = value })
			},
			payload: upTier,
		})
	} else {
		r.resolveProposals(model.ModelInfoFieldOfficialTier)
	}

	modalities := model.ModelModalities{Input: normalizeModalities(up.Modalities.Input), Output: normalizeModalities(up.Modalities.Output)}
	r.reconcile(fieldSpec{
		field: model.ModelInfoFieldModalities, class: fieldDescriptive,
		local: modalitiesDisplay(m.Modalities), upstream: modalitiesDisplay(modalities),
		same:    modalitiesDisplay(m.Modalities) == modalitiesDisplay(modalities),
		apply:   func() { r.set("modalities", modalities, func() { m.Modalities = modalities }) },
		payload: modalities,
	})

	capabilities := mergeModelsDevCapabilities(m.Capabilities, up)
	r.reconcile(fieldSpec{
		field: model.ModelInfoFieldCapabilities, class: fieldDescriptive,
		local: capabilitiesDisplay(m.Capabilities), upstream: capabilitiesDisplay(capabilities),
		same:    reflect.DeepEqual(m.Capabilities, capabilities),
		apply:   func() { r.set("capabilities", capabilities, func() { m.Capabilities = capabilities }) },
		payload: capabilities,
	})

	reasoning := reasoningFromModelsDev(up)
	r.reconcile(fieldSpec{
		field: model.ModelInfoFieldReasoningOptions, class: fieldDescriptive,
		local: reasoningDisplay(m.ReasoningOptions), upstream: reasoningDisplay(reasoning),
		same:    reasoningDisplay(m.ReasoningOptions) == reasoningDisplay(reasoning),
		apply:   func() { r.set("reasoning_options", reasoning, func() { m.ReasoningOptions = reasoning }) },
		payload: reasoning,
	})

	if up.Temperature != nil {
		unsupported := model.ModelStringList{}
		if !*up.Temperature {
			unsupported = model.ModelStringList{"temperature"}
		}
		upDisplay := strings.Join(unsupported, ",")
		if upDisplay == "" {
			upDisplay = "none"
		}
		localDisplay := strings.Join(m.UnsupportedParams, ",")
		if localDisplay == "" && m.InfoSources.Get(model.ModelInfoFieldUnsupported) != "" {
			localDisplay = "none"
		}
		r.reconcile(fieldSpec{
			field: model.ModelInfoFieldUnsupported, class: fieldDescriptive,
			local: localDisplay, upstream: upDisplay, same: localDisplay == upDisplay,
			apply:   func() { r.set("unsupported_params", unsupported, func() { m.UnsupportedParams = unsupported }) },
			payload: []string(unsupported),
		})
	} else {
		r.resolveProposals(model.ModelInfoFieldUnsupported)
	}

	openWeights := strconv.FormatBool(up.OpenWeights)
	localWeights := ""
	if m.InfoSources.Get(model.ModelInfoFieldOpenWeights) != "" || m.OpenWeights {
		localWeights = strconv.FormatBool(m.OpenWeights)
	}
	r.reconcile(fieldSpec{
		field: model.ModelInfoFieldOpenWeights, class: fieldDescriptive,
		local: localWeights, upstream: openWeights, same: localWeights == openWeights,
		apply:   func() { r.set("open_weights", up.OpenWeights, func() { m.OpenWeights = up.OpenWeights }) },
		payload: up.OpenWeights,
	})
}

func (r *modelInfoReconciler) set(column string, value interface{}, assign func()) {
	r.updates[column] = value
	assign()
}

func (r *modelInfoReconciler) resolveProposals(field string) {
	for _, kind := range []string{model.ModelInfoReviewDiff, model.ModelInfoReviewFirstRun} {
		r.recordError(model.ResolvePendingModelInfoReviewsDB(r.db, kind, r.m.ModelName, field))
	}
}

func (r *modelInfoReconciler) reconcile(spec fieldSpec) {
	if spec.upstream == "" || spec.upstream == "0" {
		r.resolveProposals(spec.field)
		return
	}
	source := r.sources.Get(spec.field)
	name := r.m.ModelName
	if spec.same {
		if source == "" || source == model.ModelInfoSourceLegacy || source == model.ModelInfoSourceInferred {
			r.sources[spec.field] = model.ModelInfoSourceModelsDev
		}
		r.recordError(model.ResolvePendingModelInfoReviewsDB(r.db, model.ModelInfoReviewDiff, name, spec.field))
		r.recordError(model.ResolvePendingModelInfoReviewsDB(r.db, model.ModelInfoReviewFirstRun, name, spec.field))
		return
	}
	localEmpty := spec.local == "" || spec.local == "0"
	payload, _ := json.Marshal(spec.payload)

	if localEmpty {
		// An operator who cleared a field (or undid a fill) keeps it clear.
		if source == model.ModelInfoSourceManual {
			return
		}
		if r.m.InfoLocked {
			r.queue(model.ModelInfoReviewDiff, spec, string(payload))
			return
		}
		spec.apply()
		r.sources[spec.field] = model.ModelInfoSourceModelsDev
		r.filled++
		if spec.class == fieldOfficial {
			r.officialChanged = true
		}
		if err := model.RecordModelInfoFillDB(r.db, name, r.maker, spec.field, "", spec.upstream, r.syncRun); err != nil {
			r.recordError(err)
		}
		return
	}

	if r.m.InfoLocked {
		r.queue(model.ModelInfoReviewDiff, spec, string(payload))
		return
	}
	switch spec.class {
	case fieldDescriptive:
		if source == model.ModelInfoSourceManual {
			return
		}
		spec.apply()
		r.sources[spec.field] = model.ModelInfoSourceModelsDev
		r.updated++
	case fieldRelease:
		switch source {
		case model.ModelInfoSourceModelsDev:
			spec.apply()
			r.updated++
		case model.ModelInfoSourceLegacy, "", model.ModelInfoSourceInferred:
			r.queue(model.ModelInfoReviewFirstRun, spec, string(payload))
		default:
			r.queue(model.ModelInfoReviewDiff, spec, string(payload))
		}
	case fieldGuarded:
		r.queue(model.ModelInfoReviewDiff, spec, string(payload))
	case fieldOfficial:
		if source == model.ModelInfoSourceManual {
			r.queue(model.ModelInfoReviewDiff, spec, string(payload))
			return
		}
		spec.apply()
		r.sources[spec.field] = model.ModelInfoSourceModelsDev
		r.officialChanged = true
		r.followed++
		r.recordError(model.ResolvePendingModelInfoReviewsDB(r.db, model.ModelInfoReviewDiff, r.m.ModelName, spec.field))
		r.recordError(model.RecordModelInfoPriceFollowDB(r.db, r.m.ModelName, r.maker, spec.field, spec.local, spec.upstream, r.syncRun))
	}
}

func (r *modelInfoReconciler) queue(kind string, spec fieldSpec, payload string) {
	note := modelInfoDiffNote(spec.field, spec.local, spec.upstream)
	if kind == model.ModelInfoReviewFirstRun {
		note = fmt.Sprintf("%d", daysApart(spec.local, spec.upstream))
	}
	if err := model.RecordModelInfoReviewDB(r.db, model.ModelInfoReview{
		Kind: kind, ModelName: r.m.ModelName, Maker: r.maker, Field: spec.field,
		LocalValue: spec.local, UpstreamValue: spec.upstream, Note: note, Payload: payload, SyncRun: r.syncRun,
	}); err != nil {
		r.recordError(err)
		return
	}
	if kind == model.ModelInfoReviewFirstRun {
		r.firstRun++
	} else {
		r.diffs++
	}
}

func (r *modelInfoReconciler) recordError(err error) {
	if r.err == nil {
		r.err = err
	}
}

// Group provenance cannot distinguish an omitted input from an explicit free
// input. Persist presence only for a supplied, accepted/matching upstream fact;
// a proposal behind a manual edit or lock cannot prove the stored zero.
func (r *modelInfoReconciler) recordOfficialPricePresence(group, column string, supplied *float64, stored float64) {
	if supplied != nil && *supplied >= 0 && r.sources[group] == model.ModelInfoSourceModelsDev && floatsClose(stored, *supplied) {
		if *supplied == 0 && stored == 0 {
			// Learning that an existing numeric zero is explicitly free changes
			// what a following price may derive, even without a numeric edit.
			if r.sources[column] != model.ModelInfoSourceModelsDevExplicitZero {
				r.officialChanged = true
			}
			r.sources[column] = model.ModelInfoSourceModelsDevExplicitZero
		} else {
			r.sources[column] = model.ModelInfoSourceModelsDev
		}
	}
}

func (r *modelInfoReconciler) save() error {
	if r.err != nil {
		return r.err
	}
	if !reflect.DeepEqual(r.sources, r.m.InfoSources) {
		r.updates["info_sources"] = r.sources
	}
	if len(r.updates) == 0 {
		return nil
	}
	r.updates["updated_time"] = common.GetTimestamp()
	return r.db.Model(&model.Model{}).Where("id = ?", r.m.Id).Updates(r.updates).Error
}

// modelInfoDiffNote is a short machine-readable hint the console turns into
// a sentence: why a disagreement might be on either side.
func modelInfoDiffNote(field, local, upstream string) string {
	switch field {
	case model.ModelInfoFieldReleaseDate:
		return "release_moved"
	case model.ModelInfoFieldMaxOutputTokens, model.ModelInfoFieldContextLength:
		if upstream != "" && upstream == local {
			return ""
		}
		return "limit_changed"
	case model.ModelInfoFieldOfficialPrice, model.ModelInfoFieldOfficialCache, model.ModelInfoFieldOfficialTier:
		return "price_changed"
	}
	return ""
}

// ---------------------------------------------------------------------------
// Tier gaps and new releases.

// recordTierGap flags a listed model whose lab prices long inputs higher
// while the direct lane has no context tier. Agent lanes have independent pricing.
func recordTierGap(db *gorm.DB, modelName, maker string, entry ModelsDevEntry, lanes []model.ModelSellPrice, syncRun int64) (bool, error) {
	tier := modelsDevContextTier(entry.Model)
	var direct *model.ModelSellPrice
	for i := range lanes {
		if model.NormalizeChannelLane(lanes[i].ChannelGroup) == model.ChannelLaneDirect {
			direct = &lanes[i]
			break
		}
	}
	// A direct price that follows the official price takes the lab's tier on
	// its own, unless the operator pinned the tier.
	followsTier := direct != nil && direct.FollowOfficial && !strings.Contains(direct.PinnedFields, model.SellPriceFieldContextTier)
	if tier == nil || direct == nil || direct.HasContextTier() || followsTier {
		return false, model.ResolvePendingModelInfoReviewsDB(db, model.ModelInfoReviewTierGap, modelName, "")
	}
	current := ""
	for _, lane := range lanes {
		if model.NormalizeChannelLane(lane.ChannelGroup) == model.ChannelLaneDirect && lane.InputPerMtok != nil && lane.OutputPerMtok != nil {
			current = priceDisplay(modelInfoPricePair{Input: *lane.InputPerMtok, Output: *lane.OutputPerMtok})
		}
	}
	payload, _ := json.Marshal(tier)
	upstream := ""
	if tier.InputPerMtok != nil && tier.OutputPerMtok != nil {
		upstream = priceDisplay(modelInfoPricePair{Input: *tier.InputPerMtok, Output: *tier.OutputPerMtok})
	}
	if err := model.RecordModelInfoReviewDB(db, model.ModelInfoReview{
		Kind: model.ModelInfoReviewTierGap, ModelName: modelName, Maker: maker, Field: "context_tier",
		LocalValue: current, UpstreamValue: upstream, Note: strconv.FormatInt(tier.Threshold, 10),
		Payload: string(payload), SyncRun: syncRun,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// contextTierDisplay prints a long-context price as "> 272k: 5 / 22.5".
func contextTierDisplay(tier *model.ContextTierPrice) string {
	if tier == nil || tier.Threshold <= 0 {
		return ""
	}
	value := func(p *float64) float64 {
		if p == nil {
			return 0
		}
		return *p
	}
	out := "> " + strconv.FormatInt(tier.Threshold, 10) + ": " +
		priceDisplay(modelInfoPricePair{Input: value(tier.InputPerMtok), Output: value(tier.OutputPerMtok)})
	if tier.CacheReadPerMtok != nil || tier.CacheWrite5mPerMtok != nil {
		out += " · " + cachePriceDisplay(value(tier.CacheReadPerMtok), value(tier.CacheWrite5mPerMtok))
	}
	return out
}

func contextTiersClose(a, b *model.ContextTierPrice) bool {
	if a == nil || b == nil || a.Threshold <= 0 || b.Threshold <= 0 {
		return (a == nil || a.Threshold <= 0) && (b == nil || b.Threshold <= 0)
	}
	close := func(x, y *float64) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return floatsClose(*x, *y)
	}
	return a.Threshold == b.Threshold && close(a.InputPerMtok, b.InputPerMtok) &&
		close(a.OutputPerMtok, b.OutputPerMtok) && close(a.CacheReadPerMtok, b.CacheReadPerMtok) &&
		close(a.CacheWrite5mPerMtok, b.CacheWrite5mPerMtok)
}

// modelsDevContextTier is the lab's first long-context tier, as prices per
// million tokens.
func modelsDevContextTier(up ModelsDevModel) *model.ContextTierPrice {
	if up.Cost == nil {
		return nil
	}
	for _, tier := range up.Cost.Tiers {
		if tier.Tier.Type != "context" || tier.Tier.Size <= 0 {
			continue
		}
		return &model.ContextTierPrice{
			Threshold:           tier.Tier.Size,
			InputPerMtok:        tier.Input,
			OutputPerMtok:       tier.Output,
			CacheReadPerMtok:    tier.CacheRead,
			CacheWrite5mPerMtok: tier.CacheWrite,
		}
	}
	return nil
}

func recordNewReleases(db *gorm.DB, index *ModelsDevIndex, missing []string, localNames map[string]struct{}, now time.Time, syncRun int64) (int, error) {
	since := now.AddDate(0, 0, -newReleaseWindowDays).Format("2006-01-02")
	makers := make([]string, 0)
	for _, maker := range model.KnownModelMakers() {
		makers = append(makers, maker.Key)
	}
	fresh := index.RecentLabReleases(makers, since)
	sort.Slice(fresh, func(i, j int) bool {
		return fresh[i].Entry.Model.ReleaseDate > fresh[j].Entry.Model.ReleaseDate
	})
	count := 0
	current := map[string]string{}
	for _, release := range fresh {
		if _, listed := localNames[strings.ToLower(release.Entry.Model.ID)]; listed {
			if err := model.ResolvePendingModelInfoReviewsDB(db, model.ModelInfoReviewNewRelease, release.Entry.Model.ID, ""); err != nil {
				return count, err
			}
			continue
		}
		current[strings.ToLower(release.Entry.Model.ID)] = release.Entry.Model.ID
		prefill := BuildModelInfoPrefill(release.Entry.Model.ID, release.Maker, release.Entry)
		payload, _ := json.Marshal(prefill)
		if err := model.RecordModelInfoReviewDB(db, model.ModelInfoReview{
			Kind: model.ModelInfoReviewNewRelease, ModelName: release.Entry.Model.ID, Maker: release.Maker,
			Field: "release", UpstreamValue: release.Entry.Model.ReleaseDate, Note: "lab", Payload: string(payload), SyncRun: syncRun,
		}); err != nil {
			return count, err
		}
		count++
	}

	// Models a channel serves that the catalog has never registered.
	for _, name := range missing {
		if _, discovered := current[strings.ToLower(name)]; discovered {
			continue
		}
		if _, listed := localNames[strings.ToLower(name)]; listed {
			continue
		}
		current[strings.ToLower(name)] = name
		maker := model.ResolveModelMaker("", name, "")
		prefill := ModelInfoPrefill{ModelName: name, Maker: maker, Filled: []string{}}
		if entry, ok := index.Lookup(name, maker, ""); ok {
			prefill = BuildModelInfoPrefill(name, maker, entry)
		}
		payload, _ := json.Marshal(prefill)
		if err := model.RecordModelInfoReviewDB(db, model.ModelInfoReview{
			Kind: model.ModelInfoReviewNewRelease, ModelName: name, Maker: prefill.Maker,
			Field: "release", UpstreamValue: prefill.ReleaseDate, Note: "channel", Payload: string(payload), SyncRun: syncRun,
		}); err != nil {
			return count, err
		}
		count++
	}
	// Retire proposals that are now listed or outside the discovery window.
	var pending []model.ModelInfoReview
	if err := db.Where("kind = ? AND status = ?", model.ModelInfoReviewNewRelease, model.ModelInfoReviewPending).Find(&pending).Error; err != nil {
		return count, err
	}
	for _, row := range pending {
		if canonical, ok := current[strings.ToLower(row.ModelName)]; !ok || canonical != row.ModelName {
			if err := model.ResolvePendingModelInfoReviewsDB(db, model.ModelInfoReviewNewRelease, row.ModelName, ""); err != nil {
				return count, err
			}
		}
	}
	return count, nil
}

// BuildModelInfoPrefill turns an upstream entry into registration defaults.
func BuildModelInfoPrefill(modelName, maker string, entry ModelsDevEntry) ModelInfoPrefill {
	up := entry.Model
	if maker == "" {
		maker = makerForModelsDevProvider(entry.ProviderID)
	}
	prefill := ModelInfoPrefill{
		ModelName:       modelName,
		Name:            up.Name,
		Maker:           maker,
		ModelsDevID:     entry.ID(),
		Description:     up.Description,
		ReleaseDate:     model.NormalizeISODate(up.ReleaseDate),
		KnowledgeCutoff: normalizeKnowledge(up.Knowledge),
		ContextLength:   up.Limit.Context,
		InputLimit:      up.Limit.Input,
		MaxOutputTokens: up.Limit.Output,
		Modalities: model.ModelModalities{
			Input: normalizeModalities(up.Modalities.Input), Output: normalizeModalities(up.Modalities.Output),
		},
		ReasoningOptions:  reasoningFromModelsDev(up),
		UnsupportedParams: []string{},
		OpenWeights:       up.OpenWeights,
		Capabilities:      mergeModelsDevCapabilities(model.ModelCapabilities{}, up),
		ContextTier:       modelsDevContextTier(up),
		Filled:            []string{},
	}
	if up.Temperature != nil && !*up.Temperature {
		prefill.UnsupportedParams = []string{"temperature"}
	}
	if up.Cost != nil {
		prefill.OfficialInputPerMtok = up.Cost.Input
		prefill.OfficialOutputPerMtok = up.Cost.Output
		prefill.CacheReadPerMtok = up.Cost.CacheRead
		prefill.CacheWritePerMtok = up.Cost.CacheWrite
	}
	add := func(field string, ok bool) {
		if ok {
			prefill.Filled = append(prefill.Filled, field)
		}
	}
	add("maker", prefill.Maker != "")
	add("release_date", prefill.ReleaseDate != "")
	add("context_length", prefill.ContextLength > 0)
	add("max_output_tokens", prefill.MaxOutputTokens > 0)
	add("official_input", prefill.OfficialInputPerMtok != nil)
	add("official_output", prefill.OfficialOutputPerMtok != nil)
	add("cache_read", prefill.CacheReadPerMtok != nil)
	add("modalities", len(prefill.Modalities.Input) > 0)
	add("capabilities", up.Reasoning || up.ToolCall || prefill.OpenWeights)
	return prefill
}

// LookupModelInfoPrefill finds registration defaults for a model id typed
// into the registration form.
func LookupModelInfoPrefill(ctx context.Context, modelName string) (ModelInfoPrefill, bool, error) {
	catalog, err := cachedModelsDevCatalog(ctx)
	if err != nil {
		return ModelInfoPrefill{}, false, err
	}
	index := NewModelsDevIndex(catalog)
	maker := model.ResolveModelMaker("", modelName, "")
	entry, ok := index.Lookup(modelName, maker, "")
	if !ok {
		return ModelInfoPrefill{ModelName: modelName, Maker: maker, Filled: []string{}}, false, nil
	}
	return BuildModelInfoPrefill(modelName, maker, entry), true, nil
}

var (
	modelsDevCacheMu sync.Mutex
	modelsDevCache   ModelsDevCatalog
	modelsDevCacheAt time.Time
)

// cachedModelsDevCatalog keeps the catalog for an hour for interactive
// lookups; the daily sync always fetches fresh.
func cachedModelsDevCatalog(ctx context.Context) (ModelsDevCatalog, error) {
	modelsDevCacheMu.Lock()
	defer modelsDevCacheMu.Unlock()
	if modelsDevCache != nil && time.Since(modelsDevCacheAt) < time.Hour {
		return modelsDevCache, nil
	}
	catalog, err := FetchModelsDevCatalog(ctx)
	if err != nil {
		return nil, err
	}
	modelsDevCache, modelsDevCacheAt = catalog, time.Now()
	return catalog, nil
}

// ---------------------------------------------------------------------------
// Applying an operator's decision.

// ApplyModelInfoReview writes an accepted review's upstream value onto the
// model, marking it as coming from models.dev.
func ApplyModelInfoReview(row *model.ModelInfoReview) error {
	return ApplyModelInfoReviews([]model.ModelInfoReview{*row})
}

// ApplyModelInfoReviews commits every decision together, or none of them.
func ApplyModelInfoReviews(rows []model.ModelInfoReview) error {
	return model.CoreDB().Transaction(func(tx *gorm.DB) error {
		for i := range rows {
			if err := applyModelInfoReviewDB(tx, &rows[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func applyModelInfoReviewDB(tx *gorm.DB, row *model.ModelInfoReview) error {
	var current model.ModelInfoReview
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, row.Id).Error; err != nil {
		return err
	}
	if current.Status != model.ModelInfoReviewPending || (current.Kind != model.ModelInfoReviewDiff && current.Kind != model.ModelInfoReviewFirstRun) {
		return types.Err(types.ErrorCodeVersionConflict)
	}
	var models []model.Model
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("model_name = ?", current.ModelName).Find(&models).Error; err != nil {
		return err
	}
	for _, m := range models {
		if modelInfoDisplay(m, current.Field) != current.LocalValue {
			return types.Err(types.ErrorCodeVersionConflict)
		}
	}
	if err := writeModelInfoFieldDB(tx, current.ModelName, current.Field, current.Payload, model.ModelInfoSourceModelsDev); err != nil {
		return err
	}
	return tx.Model(&current).Updates(map[string]interface{}{"status": model.ModelInfoReviewAccepted, "resolved_time": common.GetTimestamp()}).Error
}

// UndoModelInfoFill clears a filled field and marks it manual, so the next
// sync leaves it empty.
func UndoModelInfoFill(row *model.ModelInfoReview) error {
	return model.CoreDB().Transaction(func(tx *gorm.DB) error {
		var current model.ModelInfoReview
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, row.Id).Error; err != nil {
			return err
		}
		var last model.Option
		if err := tx.Where(&model.Option{Key: model.ModelsDevSyncLastRunOptionKey}).First(&last).Error; err != nil {
			return err
		}
		if current.Kind != model.ModelInfoReviewFilled || current.Status != model.ModelInfoReviewApplied || strconv.FormatInt(current.SyncRun, 10) != last.Value {
			return types.Err(types.ErrorCodeVersionConflict)
		}
		var models []model.Model
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("model_name = ?", current.ModelName).Find(&models).Error; err != nil {
			return err
		}
		if len(models) == 0 {
			return types.Err(types.ErrorCodeResourceNotFound, types.D{"resource": "model"})
		}
		for _, m := range models {
			if m.InfoSources.Get(current.Field) != model.ModelInfoSourceModelsDev || modelInfoDisplay(m, current.Field) != current.UpstreamValue {
				return types.Err(types.ErrorCodeVersionConflict)
			}
		}
		if err := writeModelInfoFieldDB(tx, current.ModelName, current.Field, "", model.ModelInfoSourceManual); err != nil {
			return err
		}
		return tx.Model(&model.ModelInfoReview{}).
			Where("kind = ? AND status = ? AND model_name = ? AND field = ? AND sync_run = ? AND local_value = ? AND upstream_value = ?",
				model.ModelInfoReviewFilled, model.ModelInfoReviewApplied, current.ModelName, current.Field, current.SyncRun, current.LocalValue, current.UpstreamValue).
			Updates(map[string]interface{}{"status": model.ModelInfoReviewUndone, "resolved_time": common.GetTimestamp()}).Error
	})
}

func modelInfoDisplay(m model.Model, field string) string {
	switch field {
	case model.ModelInfoFieldMaker:
		return m.Maker
	case model.ModelInfoFieldReleaseDate:
		return m.ReleaseDate
	case model.ModelInfoFieldKnowledgeCutoff:
		return m.KnowledgeCutoff
	case model.ModelInfoFieldContextLength:
		return intDisplay(m.ContextLength)
	case model.ModelInfoFieldInputLimit:
		return intDisplay(m.InputLimit)
	case model.ModelInfoFieldMaxOutputTokens:
		return intDisplay(m.MaxOutputTokens)
	case model.ModelInfoFieldOfficialPrice:
		if m.OfficialInputPrice == 0 && m.OfficialOutputPrice == 0 {
			return ""
		}
		return priceDisplay(modelInfoPricePair{Input: officialPerMtok(m.OfficialInputPrice), Output: officialPerMtok(m.OfficialOutputPrice)})
	case model.ModelInfoFieldOfficialCache:
		return cachePriceDisplay(m.OfficialCacheReadPerMtok, m.OfficialCacheWritePerMtok)
	case model.ModelInfoFieldOfficialTier:
		return contextTierDisplay(m.OfficialContextTier.Price())
	case model.ModelInfoFieldModalities:
		return modalitiesDisplay(m.Modalities)
	case model.ModelInfoFieldCapabilities:
		return capabilitiesDisplay(m.Capabilities)
	case model.ModelInfoFieldReasoningOptions:
		return reasoningDisplay(m.ReasoningOptions)
	case model.ModelInfoFieldUnsupported:
		if len(m.UnsupportedParams) == 0 {
			if m.InfoSources.Get(field) == "" {
				return ""
			}
			return "none"
		}
		return strings.Join(m.UnsupportedParams, ",")
	case model.ModelInfoFieldOpenWeights:
		if !m.OpenWeights && m.InfoSources.Get(field) == "" {
			return ""
		}
		return strconv.FormatBool(m.OpenWeights)
	}
	return ""
}

// writeModelInfoField sets one fact on every catalog row carrying the name
// (older data can hold duplicates) and records where the value came from.
func writeModelInfoField(modelName, field, payload, source string) error {
	return writeModelInfoFieldDB(model.CoreDB(), modelName, field, payload, source)
}

func writeModelInfoFieldDB(db *gorm.DB, modelName, field, payload, source string) error {
	var rows []model.Model
	if err := db.Where("model_name = ?", modelName).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return types.Err(types.ErrorCodeResourceNotFound, types.D{"resource": "model"})
	}
	for _, m := range rows {
		updates, err := modelInfoFieldUpdates(field, payload)
		if err != nil {
			return err
		}
		sources := model.ModelInfoSources{}
		for key, value := range m.InfoSources {
			sources[key] = value
		}
		sources[field] = source
		updates["info_sources"] = sources
		updates["updated_time"] = common.GetTimestamp()
		if err := db.Model(&model.Model{}).Where("id = ?", m.Id).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}

// modelInfoFieldUpdates maps a field and its JSON value to column updates.
// An empty payload clears the field.
func modelInfoFieldUpdates(field, payload string) (map[string]interface{}, error) {
	clear := strings.TrimSpace(payload) == ""
	updates := map[string]interface{}{}
	decode := func(out interface{}) error {
		if clear {
			return nil
		}
		return json.Unmarshal([]byte(payload), out)
	}
	switch field {
	case model.ModelInfoFieldMaker, model.ModelInfoFieldReleaseDate, model.ModelInfoFieldKnowledgeCutoff:
		var value string
		if err := decode(&value); err != nil {
			return nil, err
		}
		updates[field] = value
	case model.ModelInfoFieldContextLength, model.ModelInfoFieldInputLimit, model.ModelInfoFieldMaxOutputTokens:
		var value int64
		if err := decode(&value); err != nil {
			return nil, err
		}
		updates[field] = value
	case model.ModelInfoFieldOfficialPrice:
		var value modelInfoPriceUpdate
		if err := decode(&value); err != nil {
			return nil, err
		}
		if clear {
			updates["official_input_price"], updates["official_output_price"] = 0.0, 0.0
		} else {
			if value.Input != nil {
				updates["official_input_price"] = *value.Input / 1e6
			}
			if value.Output != nil {
				updates["official_output_price"] = *value.Output / 1e6
			}
		}
	case model.ModelInfoFieldOfficialCache:
		var value modelInfoCachePriceUpdate
		if err := decode(&value); err != nil {
			return nil, err
		}
		if clear {
			updates["official_cache_read_per_mtok"], updates["official_cache_write_per_mtok"] = 0.0, 0.0
		} else {
			if value.CacheRead != nil {
				updates["official_cache_read_per_mtok"] = *value.CacheRead
			}
			if value.CacheWrite != nil {
				updates["official_cache_write_per_mtok"] = *value.CacheWrite
			}
		}
	case model.ModelInfoFieldOfficialTier:
		var value model.ContextTierPrice
		if err := decode(&value); err != nil {
			return nil, err
		}
		if value.Threshold < 0 {
			return nil, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": field})
		}
		updates["official_context_tier"] = model.ModelContextTier(value)
	case model.ModelInfoFieldModalities:
		var value model.ModelModalities
		if err := decode(&value); err != nil {
			return nil, err
		}
		updates[field] = value
	case model.ModelInfoFieldCapabilities:
		var value model.ModelCapabilities
		if err := decode(&value); err != nil {
			return nil, err
		}
		updates[field] = value
	case model.ModelInfoFieldReasoningOptions:
		var value model.ModelReasoningOptions
		if err := decode(&value); err != nil {
			return nil, err
		}
		updates[field] = value
	case model.ModelInfoFieldUnsupported:
		var value []string
		if err := decode(&value); err != nil {
			return nil, err
		}
		updates[field] = model.ModelStringList(value)
	case model.ModelInfoFieldOpenWeights:
		var value bool
		if err := decode(&value); err != nil {
			return nil, err
		}
		updates[field] = value
	default:
		return nil, fmt.Errorf("unknown model info field %q", field)
	}
	return updates, nil
}

// ---------------------------------------------------------------------------
// Normalisers and display forms. Display forms double as the equality test
// and the value shown in the review queue.

func daysApart(a, b string) int {
	ta, errA := time.Parse("2006-01-02", a)
	tb, errB := time.Parse("2006-01-02", b)
	if errA != nil || errB != nil {
		if a == b {
			return 0
		}
		return math.MaxInt32
	}
	days := int(math.Round(ta.Sub(tb).Hours() / 24))
	if days < 0 {
		days = -days
	}
	return days
}

func normalizeKnowledge(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 7 {
		return value[:7]
	}
	return value
}

func intDisplay(value int64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

// officialPerMtok reads a stored official price as dollars per million
// tokens. The column historically holds dollars per token (LiteLLM's unit),
// but some rows were typed per million; a value that small can only be per
// token.
func officialPerMtok(value float64) float64 {
	if value > 0 && value < 0.001 {
		return value * 1e6
	}
	return value
}

// limitsClose treats token limits within 5% as the same fact: 1,048,576 and
// 1,000,000 are both "1M" to every reader, and 200,000 vs 204,800 is a
// rounding convention, not a change worth an operator's attention.
func limitsClose(a, b int64) bool {
	if a == b {
		return true
	}
	if a <= 0 || b <= 0 {
		return false
	}
	larger := math.Max(float64(a), float64(b))
	return math.Abs(float64(a)-float64(b))/larger <= 0.05
}

func floatsClose(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9+math.Abs(b)*1e-6
}

func priceDisplay(price modelInfoPricePair) string {
	return formatPerMtok(price.Input) + " / " + formatPerMtok(price.Output)
}

// cachePriceDisplay prints "read / write" per million, "" when neither is
// known and "—" for a side the lab does not price.
func cachePriceDisplay(read, write float64) string {
	if read <= 0 && write <= 0 {
		return ""
	}
	side := func(value float64) string {
		if value <= 0 {
			return "—"
		}
		return formatPerMtok(value)
	}
	return side(read) + " / " + side(write)
}

// formatPerMtok prints a per-million price without float noise: a stored
// per-token 1e-7 read back as 0.09999999999999999 is 0.1.
func formatPerMtok(value float64) string {
	return strconv.FormatFloat(math.Round(value*1e6)/1e6, 'f', -1, 64)
}

var modelsDevModalityOrder = map[string]int{"text": 0, "image": 1, "audio": 2, "video": 3, "pdf": 4}

func normalizeModalities(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		oi, iok := modelsDevModalityOrder[out[i]]
		oj, jok := modelsDevModalityOrder[out[j]]
		if !iok {
			oi = 99
		}
		if !jok {
			oj = 99
		}
		return oi < oj
	})
	return out
}

func modalitiesDisplay(value model.ModelModalities) string {
	if value.IsZero() {
		return ""
	}
	return strings.Join(normalizeModalities(value.Input), ",") + " -> " + strings.Join(normalizeModalities(value.Output), ",")
}

// mergeModelsDevCapabilities only ever switches flags on. Capabilities feed
// request gating (protocol capability snapshot), so a flag upstream leaves
// off must never turn a working request into a 400 here.
func mergeModelsDevCapabilities(current model.ModelCapabilities, up ModelsDevModel) model.ModelCapabilities {
	merged := current
	has := func(list []string, want string) bool {
		for _, value := range list {
			if strings.EqualFold(value, want) {
				return true
			}
		}
		return false
	}
	merged.SupportsFunctionCalling = merged.SupportsFunctionCalling || up.ToolCall
	merged.SupportsReasoning = merged.SupportsReasoning || up.Reasoning
	merged.SupportsResponseSchema = merged.SupportsResponseSchema || (up.StructuredOutput != nil && *up.StructuredOutput)
	merged.SupportsVision = merged.SupportsVision || has(up.Modalities.Input, "image")
	merged.SupportsPDFInput = merged.SupportsPDFInput || has(up.Modalities.Input, "pdf")
	merged.SupportsAudioInput = merged.SupportsAudioInput || has(up.Modalities.Input, "audio")
	merged.SupportsAudioOutput = merged.SupportsAudioOutput || has(up.Modalities.Output, "audio")
	return merged
}

func capabilitiesDisplay(value model.ModelCapabilities) string {
	parts := []string{}
	if value.SupportsReasoning {
		parts = append(parts, "reasoning")
	}
	if value.SupportsFunctionCalling {
		parts = append(parts, "tools")
	}
	if value.SupportsResponseSchema {
		parts = append(parts, "structured_output")
	}
	if value.SupportsVision {
		parts = append(parts, "vision")
	}
	if value.SupportsPDFInput {
		parts = append(parts, "pdf")
	}
	if value.SupportsAudioInput {
		parts = append(parts, "audio_in")
	}
	if value.SupportsAudioOutput {
		parts = append(parts, "audio_out")
	}
	return strings.Join(parts, ",")
}

// reasoningFromModelsDev reads a lab's reasoning controls: a "toggle" option
// means thinking can be switched off, an "effort" option lists the levels.
func reasoningFromModelsDev(up ModelsDevModel) model.ModelReasoningOptions {
	out := model.ModelReasoningOptions{}
	for _, option := range up.ReasoningOptions {
		switch option.Type {
		case "toggle":
			out.Toggle = true
		case "effort":
			for _, value := range option.Values {
				value = strings.ToLower(strings.TrimSpace(value))
				if value != "" {
					out.Efforts = append(out.Efforts, value)
				}
			}
		}
	}
	return out
}

func reasoningDisplay(value model.ModelReasoningOptions) string {
	if value.IsZero() {
		return ""
	}
	prefix := ""
	if value.Toggle {
		prefix = "toggle;"
	}
	return prefix + strings.Join(value.Efforts, ",")
}
