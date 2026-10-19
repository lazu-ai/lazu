package controller

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 模型运营 (model operations) console API.
//
// The console page has two tabs that ask two different questions about the same
// four-layer concept model (厂牌 → 模型 → 对外档位 → 来源池):
//
//   - 模型 tab: for each model, which tiers are on sale at what price, and which
//     sources back each tier. Tiers are `Channel.Group` lanes (direct = 稳定,
//     cheap = 便宜) and the pool is the `Ability` rows for (lane, model).
//   - 来源 tab: for each channel, what it costs, what it serves, and how stable it
//     has been. Stability is aggregated live out of `routing_outcome`.
//
// Nothing here invents a new storage model: it joins channels + abilities +
// provider_cost_prices + routing_outcome and returns what actually exists.

const (
	modelOpsDefaultWindowHours = 48
	modelOpsMaxWindowHours     = 168
	// One slice per hour of the default 48-hour window, drawn as the same thin
	// status bars the health page uses; wider windows widen the slice.
	modelOpsProbeBuckets = 48
	// Guard rail on the ?models= fan-out so a hand-crafted request cannot ask
	// the pool query to expand an unbounded IN list.
	modelOpsMaxPoolModels = 400
	modelOpsMaxSpendDays  = 90
)

type modelOpsProbeBucket struct {
	StartAt      time.Time `json:"start_at"`
	EndAt        time.Time `json:"end_at"`
	RequestCount int64     `json:"request_count"`
	ErrorCount   int64     `json:"error_count"`
	// SuccessRate is null (not 0) when the bucket saw no traffic, so the UI can
	// draw "no data" instead of a misleading total-outage bar.
	SuccessRate *float64 `json:"success_rate"`
}

type modelOpsHealth struct {
	RequestCount  int64                 `json:"request_count"`
	ErrorCount    int64                 `json:"error_count"`
	SuccessRate   *float64              `json:"success_rate"`
	AvgLatencyMs  *float64              `json:"avg_latency_ms"`
	LastOutcomeAt *time.Time            `json:"last_outcome_at"`
	Buckets       []modelOpsProbeBucket `json:"buckets,omitempty"`
	// Source says what the record is made of: "traffic", or "probe" for a
	// route that carried no traffic in the window and is known only from its
	// probes — a spare, or a new route under observation.
	Source string `json:"source,omitempty"`
}

type modelOpsSource struct {
	ID         string `json:"id"`
	InternalID int    `json:"internal_id"`
	Name       string `json:"name"`
	Type       int    `json:"type"`
	TypeName   string `json:"type_name"`
	SourceType string `json:"source_type"`
	// NeedsClassification is true while nobody has decided what this source is.
	// The migration refuses to guess, so the console can list exactly the rows
	// still waiting on a human.
	NeedsClassification bool     `json:"needs_classification"`
	Lane                string   `json:"lane"`
	Status              int      `json:"status"`
	BaseURL             string   `json:"base_url"`
	Endpoint            string   `json:"endpoint"`
	KeyFinger           string   `json:"key_fingerprint"`
	Weight              int64    `json:"weight"`
	Priority            int64    `json:"priority"`
	AutoBan             bool     `json:"auto_ban"`
	ModelCount          int      `json:"model_count"`
	ModelNames          []string `json:"model_names"`
	ResponseTime        int      `json:"response_time_ms"`
	TestTime            int64    `json:"test_time"`
	// Cost basis is the median of this channel's own provider_cost_prices rows.
	// Nil when the channel has no cost rows — the console renders "—" rather
	// than inventing a margin.
	CostInputPerMtok  *float64       `json:"cost_input_per_mtok"`
	CostOutputPerMtok *float64       `json:"cost_output_per_mtok"`
	CostRequestUSD    *float64       `json:"cost_request_usd"`
	CostSecondUSD     *float64       `json:"cost_second_usd"`
	CostRowCount      int            `json:"cost_row_count"`
	Health            modelOpsHealth `json:"health"`
	// Observation is present while any of this source's routes is being
	// watched before going live; see route_observation.go.
	Observation *observationView `json:"observation,omitempty"`
}

type modelOpsSourceRules struct {
	WindowHours                   int     `json:"window_hours"`
	AutomaticDisableEnabled       bool    `json:"automatic_disable_enabled"`
	AutomaticEnableEnabled        bool    `json:"automatic_enable_enabled"`
	ChannelDisableThresholdSecond float64 `json:"channel_disable_threshold_seconds"`
	AutoTestEnabled               bool    `json:"auto_test_channel_enabled"`
	AutoTestMinutes               int     `json:"auto_test_channel_minutes"`
	OutcomeTelemetryAvailable     bool    `json:"outcome_telemetry_available"`
}

type modelOpsPoolEntry struct {
	ChannelID         string         `json:"channel_id"`
	ChannelInternalID int            `json:"channel_internal_id"`
	Name              string         `json:"name"`
	SourceType        string         `json:"source_type"`
	Type              int            `json:"type"`
	Lane              string         `json:"lane"`
	Weight            int64          `json:"weight"`
	Priority          int64          `json:"priority"`
	Enabled           bool           `json:"enabled"`
	ChannelStatus     int            `json:"channel_status"`
	ResponseTime      int            `json:"response_time_ms"`
	CostInputPerMtok  *float64       `json:"cost_input_per_mtok"`
	CostOutputPerMtok *float64       `json:"cost_output_per_mtok"`
	CostRequestUSD    *float64       `json:"cost_request_usd"`
	CostSecondUSD     *float64       `json:"cost_second_usd"`
	Health            modelOpsHealth `json:"health"`
	// Role is what this source is for on this route: primary, backup, or
	// inventory.
	Role        string           `json:"role"`
	Observation *observationView `json:"observation,omitempty"`
}

// GetModelOpsSources backs the 来源 tab: one row per channel with its cost basis,
// what it serves, and its stability over the requested window.
func GetModelOpsSources(c *gin.Context) {
	windowHours := parseModelOpsWindowHours(c.Query("window"))
	since := modelOpsWindowStart(windowHours)
	modelName := strings.TrimSpace(c.Query("model"))

	var channels []*model.Channel
	query := model.CoreDB().Model(&model.Channel{}).
		Where("type NOT IN ?", constant.SearchChannelTypes()).
		Order("priority desc, id desc").
		Omit("key")
	if modelName != "" {
		query = query.Where(
			"EXISTS (SELECT 1 FROM abilities WHERE abilities.channel_id = channels.id AND abilities.model = ?)",
			modelName,
		)
	}
	if err := query.Find(&channels).Error; err != nil {
		httperr.Write(c, err)
		return
	}

	internalIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		internalIDs = append(internalIDs, channel.Id)
	}

	health, telemetryAvailable, err := modelOpsChannelHealth(internalIDs, since, windowHours)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	costs, err := modelOpsChannelCostBasis(internalIDs)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	observations, err := modelOpsSourceObservations(internalIDs, since, windowHours)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	items := make([]modelOpsSource, 0, len(channels))
	for _, channel := range channels {
		cost := costs[channel.Id]
		sourceHealth := health[channel.Id]
		observation := observations[channel.Id]
		if observation.view != nil && observation.view.LiveRoutes == 0 && sourceHealth.RequestCount == 0 {
			// A source that is entirely new has no traffic to show; its
			// probes are its record.
			sourceHealth = observation.probeHealth
		}
		items = append(items, modelOpsSource{
			ID:                  channel.PublicID,
			InternalID:          channel.Id,
			Name:                channel.Name,
			Type:                channel.Type,
			TypeName:            constant.ChannelTypeNames[channel.Type],
			SourceType:          channel.GetSourceType(),
			NeedsClassification: !model.IsValidChannelSourceType(channel.SourceType),
			Lane:                model.NormalizeChannelLane(channel.Group),
			Status:              channel.Status,
			BaseURL:             channel.GetBaseURL(),
			Endpoint:            modelOpsEndpointLabel(channel),
			KeyFinger:           modelOpsKeyFingerprint(channel.KeyDigest),
			Weight:              int64(channel.GetWeight()),
			Priority:            channel.GetPriority(),
			AutoBan:             channel.GetAutoBan(),
			ModelCount:          len(channel.GetModels()),
			ModelNames:          channel.GetModels(),
			ResponseTime:        channel.ResponseTime,
			TestTime:            channel.TestTime,
			CostInputPerMtok:    cost.input,
			CostOutputPerMtok:   cost.output,
			CostRequestUSD:      cost.request,
			CostSecondUSD:       cost.second,
			CostRowCount:        cost.rows,
			Health:              sourceHealth,
			Observation:         observation.view,
		})
	}

	monitor := operation_setting.GetMonitorSetting()
	common.ApiSuccess(c, gin.H{
		"items": items,
		"rules": modelOpsSourceRules{
			WindowHours:                   windowHours,
			AutomaticDisableEnabled:       common.AutomaticDisableChannelEnabled,
			AutomaticEnableEnabled:        common.AutomaticEnableChannelEnabled,
			ChannelDisableThresholdSecond: common.ChannelDisableThreshold,
			AutoTestEnabled:               monitor.AutoTestChannelEnabled,
			AutoTestMinutes:               monitor.AutoTestChannelMinutes,
			OutcomeTelemetryAvailable:     telemetryAvailable,
		},
	})
}

// GetModelOpsPools backs the 来源池 column and the per-tier cards: for each of the
// requested models it returns the abilities (lane × channel × weight) that serve
// it, decorated with the same live health the 来源 tab shows.
func GetModelOpsPools(c *gin.Context) {
	names := modelOpsSplitList(c.Query("models"))
	if len(names) == 0 {
		common.ApiSuccess(c, gin.H{"items": gin.H{}, "window_hours": modelOpsDefaultWindowHours})
		return
	}
	if len(names) > modelOpsMaxPoolModels {
		names = names[:modelOpsMaxPoolModels]
	}
	windowHours := parseModelOpsWindowHours(c.Query("window"))
	since := modelOpsWindowStart(windowHours)

	type abilityRow struct {
		Group     string
		Model     string
		ChannelId int
		Enabled   bool
		Priority  *int64
		Weight    uint
		Role      string
	}
	var abilities []abilityRow
	if err := model.CoreDB().Model(&model.Ability{}).
		Where("model IN ?", names).
		Find(&abilities).Error; err != nil {
		httperr.Write(c, err)
		return
	}

	channelIDSet := make(map[int]struct{}, len(abilities))
	for _, ability := range abilities {
		channelIDSet[ability.ChannelId] = struct{}{}
	}
	channelIDs := make([]int, 0, len(channelIDSet))
	for id := range channelIDSet {
		channelIDs = append(channelIDs, id)
	}

	channelsByID := make(map[int]*model.Channel, len(channelIDs))
	if len(channelIDs) > 0 {
		var channels []*model.Channel
		if err := model.CoreDB().Model(&model.Channel{}).
			Where("id IN ?", channelIDs).
			Omit("key").
			Find(&channels).Error; err != nil {
			httperr.Write(c, err)
			return
		}
		for _, channel := range channels {
			channelsByID[channel.Id] = channel
		}
	}

	// Health here is scoped to (model, channel): a source can be healthy overall
	// and still be the one failing this particular model.
	health, _, err := modelOpsModelChannelHealth(names, channelIDs, since, windowHours)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	costs, err := modelOpsModelChannelCosts(names, channelIDs)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	views, _, err := buildObservationViews(channelIDs, time.Now().UTC())
	if err != nil {
		httperr.Write(c, err)
		return
	}
	probeHealth, err := modelOpsRouteProbeHealth(channelIDs, since, windowHours)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	items := make(map[string]map[string][]modelOpsPoolEntry, len(names))
	for _, ability := range abilities {
		lane := model.NormalizeChannelLane(ability.Group)
		if !model.IsKnownChannelLane(lane) {
			continue
		}
		channel := channelsByID[ability.ChannelId]
		if channel == nil {
			continue
		}
		if constant.IsSearchChannelType(channel.Type) {
			continue
		}
		priority := int64(0)
		if ability.Priority != nil {
			priority = *ability.Priority
		}
		cost := costs[modelOpsCostKey(ability.Model, ability.ChannelId)]
		entry := modelOpsPoolEntry{
			ChannelID:         channel.PublicID,
			ChannelInternalID: channel.Id,
			Name:              channel.Name,
			SourceType:        channel.GetSourceType(),
			Type:              channel.Type,
			Lane:              lane,
			Weight:            int64(ability.Weight),
			Priority:          priority,
			Enabled:           ability.Enabled,
			ChannelStatus:     channel.Status,
			ResponseTime:      channel.ResponseTime,
			CostInputPerMtok:  cost.input,
			CostOutputPerMtok: cost.output,
			CostRequestUSD:    cost.request,
			CostSecondUSD:     cost.second,
			Health:            health[modelOpsCostKey(ability.Model, ability.ChannelId)],
			Role:              model.NormalizeAbilityRole(ability.Role),
		}
		cellKey := model.ChannelHealthCellKey(ability.Model, lane, ability.ChannelId)
		if entry.Health.RequestCount == 0 {
			if probed, ok := probeHealth[cellKey]; ok {
				entry.Health = probed
			}
		}
		if view, ok := views[cellKey]; ok {
			view := view
			entry.Observation = &view
		}
		if items[ability.Model] == nil {
			items[ability.Model] = make(map[string][]modelOpsPoolEntry, 2)
		}
		items[ability.Model][lane] = append(items[ability.Model][lane], entry)
	}
	for _, lanes := range items {
		for lane := range lanes {
			entries := lanes[lane]
			sort.SliceStable(entries, func(i, j int) bool {
				// Live sources first, then backups, then everything parked;
				// a tier card reads top down as "what serves this".
				if rank := modelOpsRoleRank(entries[i].Role) - modelOpsRoleRank(entries[j].Role); rank != 0 {
					return rank < 0
				}
				if entries[i].Priority != entries[j].Priority {
					return entries[i].Priority > entries[j].Priority
				}
				if entries[i].Weight != entries[j].Weight {
					return entries[i].Weight > entries[j].Weight
				}
				return entries[i].Name < entries[j].Name
			})
			lanes[lane] = entries
		}
	}

	common.ApiSuccess(c, gin.H{"items": items, "window_hours": windowHours})
}

// GetModelOpsSpend returns spend for the exact models on the current catalog
// page. The general dashboard intentionally exposes only a top-20 leaderboard,
// which is not a truthful source for a paginated operations table.
func GetModelOpsSpend(c *gin.Context) {
	names := modelOpsSplitList(c.Query("models"))
	if len(names) == 0 {
		common.ApiSuccess(c, gin.H{"items": []model.DashboardUsageDimensionRow{}})
		return
	}
	if len(names) > modelOpsMaxPoolModels {
		names = names[:modelOpsMaxPoolModels]
	}
	days, err := strconv.Atoi(strings.TrimSpace(c.Query("days")))
	if err != nil || days <= 0 {
		days = 30
	}
	if days > modelOpsMaxSpendDays {
		days = modelOpsMaxSpendDays
	}
	end := time.Now().UTC().Unix()
	start := end - int64(days*24*60*60)
	rows, err := model.BuildAdminDashboardModelRowsContext(c.Request.Context(), names, start, end)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows, "window_days": days})
}

type modelOpsPoolWeightRequest struct {
	ChannelID string `json:"channel_id"`
	Lane      string `json:"lane"`
	Model     string `json:"model"`
	Weight    *int64 `json:"weight"`
}

// UpdateModelOpsPoolWeight writes the pool-local weight, i.e. `abilities.weight`
// for one (lane, model, channel). Both the DB-backed and memory-cached pickers
// read this value. Re-saving a source preserves overrides for abilities that
// continue to exist; its global channel weight is only the default for new
// model/lane memberships.
func UpdateModelOpsPoolWeight(c *gin.Context) {
	var req modelOpsPoolWeightRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if req.Weight == nil || *req.Weight < 0 {
		httperr.Invalid(c, "weight")
		return
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "model"})
		return
	}
	lane := model.NormalizeChannelLane(req.Lane)
	if !model.IsValidChannelLane(lane) {
		httperr.Code(c, types.ErrorCodeLaneNotFound, types.D{"lane": req.Lane})
		return
	}
	channelID, err := resolveChannelID(req.ChannelID)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	result := model.CoreDB().Model(&model.Ability{}).
		Where("model = ? AND channel_id = ?", modelName, channelID).
		Where(modelOpsAbilityGroupColumn()+" = ?", lane).
		Update("weight", uint(*req.Weight))
	if result.Error != nil {
		httperr.Write(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		httperr.Code(c, types.ErrorCodeChannelRouteNotFound, types.D{"channel_id": req.ChannelID, "model": modelName, "lane": lane})
		return
	}
	model.InitChannelCache()
	common.ApiSuccess(c, gin.H{"model": modelName, "lane": lane, "weight": *req.Weight})
}

type modelOpsPoolWeightBatchRequest struct {
	ChannelID string  `json:"channel_id"`
	Weight    *int64  `json:"weight"`
	Routes    []route `json:"routes"`
}

type route struct {
	Lane  string `json:"lane"`
	Model string `json:"model"`
}

// BatchUpdateModelOpsPoolWeight reweights every named route of ONE upstream in
// a single transaction. The provider ledger needs it: when a source is failing,
// an operator shifts its traffic away across all the models it serves, and
// doing that as N separate calls would leave the pool half-moved if one failed
// midway.
//
// Weight is set explicitly rather than "switch to the backup", because there is
// no backup-pool entity in the routing model — a pool is just the set of
// sources serving a (model, lane), and taking one to 0 hands its share to the
// rest by the picker's existing rules. Naming a concept the router does not
// have would be a UI fiction the backend cannot honour.
func BatchUpdateModelOpsPoolWeight(c *gin.Context) {
	var req modelOpsPoolWeightBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if req.Weight == nil || *req.Weight < 0 {
		httperr.Invalid(c, "weight")
		return
	}
	if len(req.Routes) == 0 {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "routes"})
		return
	}
	channelID, err := resolveChannelID(req.ChannelID)
	if err != nil {
		httperr.Write(c, err)
		return
	}

	type resolvedRoute struct {
		lane      string
		modelName string
	}
	resolved := make([]resolvedRoute, 0, len(req.Routes))
	for _, item := range req.Routes {
		modelName := strings.TrimSpace(item.Model)
		if modelName == "" {
			httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "model"})
			return
		}
		lane := model.NormalizeChannelLane(item.Lane)
		if !model.IsValidChannelLane(lane) {
			httperr.Code(c, types.ErrorCodeLaneNotFound, types.D{"lane": item.Lane})
			return
		}
		resolved = append(resolved, resolvedRoute{lane: lane, modelName: modelName})
	}

	updated := 0
	err = model.CoreDB().Transaction(func(tx *gorm.DB) error {
		for _, item := range resolved {
			result := tx.Model(&model.Ability{}).
				Where("model = ? AND channel_id = ?", item.modelName, channelID).
				Where(modelOpsAbilityGroupColumn()+" = ?", item.lane).
				Update("weight", uint(*req.Weight))
			if result.Error != nil {
				return result.Error
			}
			// A route that no longer exists is reported rather than silently
			// skipped: the operator asked to move traffic off a specific set,
			// and a partial move they were not told about is worse than none.
			if result.RowsAffected == 0 {
				return types.Err(types.ErrorCodeChannelRouteNotFound, types.D{"channel_id": req.ChannelID, "model": item.modelName, "lane": item.lane})
			}
			updated++
		}
		return nil
	})
	if err != nil {
		httperr.Write(c, err)
		return
	}

	model.InitChannelCache()
	refreshChannelHealthInBackground("pool weight update")
	common.ApiSuccess(c, gin.H{"updated": updated, "weight": *req.Weight})
}

type modelOpsTierWeightRequest struct {
	ChannelID string `json:"channel_id"`
	Weight    *uint  `json:"weight"`
}

type modelOpsTierRequest struct {
	Lane    string                      `json:"lane"`
	Price   *model.ModelSellPrice       `json:"price"`
	Weights []modelOpsTierWeightRequest `json:"weights"`
}

type modelOpsTierConfigRequest struct {
	Model string                `json:"model"`
	Tiers []modelOpsTierRequest `json:"tiers"`
}

// SaveModelOpsTierConfig commits every changed price and pool weight in one
// transaction. Admins may update routing weights; publishing prices remains a
// root-only operation, matching /api/pricing/sell_prices.
func SaveModelOpsTierConfig(c *gin.Context) {
	var req modelOpsTierConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "model"})
		return
	}
	if len(req.Tiers) == 0 {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "tiers"})
		return
	}
	prepared := make([]model.ModelOpsTierConfig, 0, len(req.Tiers))
	beforePrices := make(map[string]model.ModelSellPrice)
	for _, tier := range req.Tiers {
		lane := model.NormalizeChannelLane(tier.Lane)
		if !model.IsKnownChannelLane(lane) {
			httperr.Code(c, types.ErrorCodeLaneNotFound, types.D{"lane": tier.Lane})
			return
		}
		if tier.Price != nil && c.GetInt("role") < common.RoleRootUser {
			httperr.Code(c, types.ErrorCodeAccessDenied)
			return
		}
		if tier.Price != nil {
			if current, ok := model.GetActiveModelSellPrice(modelName, lane); ok {
				beforePrices[lane] = current
			}
		}
		weights := make([]model.ModelOpsPoolWeight, 0, len(tier.Weights))
		for _, weight := range tier.Weights {
			if weight.Weight == nil {
				httperr.Invalid(c, "weight")
				return
			}
			channelID, err := resolveChannelID(weight.ChannelID)
			if err != nil {
				httperr.Write(c, err)
				return
			}
			weights = append(weights, model.ModelOpsPoolWeight{ChannelID: channelID, Weight: *weight.Weight})
		}
		prepared = append(prepared, model.ModelOpsTierConfig{Lane: lane, Price: tier.Price, Weights: weights})
	}
	if err := model.SaveModelOpsTierConfigurations(modelName, prepared); err != nil {
		httperr.Write(c, err)
		return
	}
	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()
	// Only a direct-lane tier closes the lab-price gap; other lanes are independent.
	for _, lane := range model.GetActiveModelSellPriceLaneMap()[modelName] {
		if model.NormalizeChannelLane(lane.ChannelGroup) == model.ChannelLaneDirect && lane.HasContextTier() {
			_ = model.ResolvePendingModelInfoReviews(model.ModelInfoReviewTierGap, modelName, "")
			break
		}
	}
	model.WriteAdminAuditLog(c, "model_ops.tier_config.save", "model", modelName, modelName, beforePrices, req)
	common.ApiSuccess(c, gin.H{"model": modelName, "tiers": len(prepared)})
}

type modelOpsBatchStatusRequest struct {
	IDs    []int `json:"ids"`
	Status int   `json:"status"`
}

// BatchUpdateModelOpsStatus flips 上架 for a selection of models in one request.
// The console used to fire one PUT per row, each with its own toast and its own
// full cache invalidation.
func BatchUpdateModelOpsStatus(c *gin.Context) {
	var req modelOpsBatchStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	ids := make([]int, 0, len(req.IDs))
	seen := make(map[int]struct{}, len(req.IDs))
	for _, id := range req.IDs {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "ids"})
		return
	}
	status := 0
	if req.Status == 1 {
		status = 1
	}
	// Putting a model on sale with no active sell price is the "$0 billing"
	// failure mode this page exists to prevent — refuse it server-side too, not
	// only in the disabled toggle.
	if status == 1 {
		var models []model.Model
		if err := model.CoreDB().Where("id IN ?", ids).Find(&models).Error; err != nil {
			httperr.Write(c, err)
			return
		}
		// A model may be sold only on cheap when no direct source serves it.
		// The shared lane policy routes unpinned Keys to cheap in that case;
		// each model still needs a price and a live source on its sale lane.
		unpriced := make([]string, 0)
		directNames := make([]string, 0, len(models))
		cheapOnlyNames := make([]string, 0)
		for _, item := range models {
			if _, ok := model.GetActiveModelSellPrice(item.ModelName, model.ChannelLaneDirect); ok {
				directNames = append(directNames, item.ModelName)
				continue
			}
			if _, ok := model.GetActiveModelSellPrice(item.ModelName, model.ChannelLaneAgent); ok {
				cheapOnlyNames = append(cheapOnlyNames, item.ModelName)
				continue
			}
			unpriced = append(unpriced, item.ModelName)
		}
		if len(unpriced) > 0 {
			httperr.Code(c, types.ErrorCodeModelListingUnpriced, types.D{"models": strings.Join(unpriced, ", ")})
			return
		}
		unroutable, err := modelOpsModelsWithoutLanePool(directNames, model.ChannelLaneDirect)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		if len(unroutable) > 0 {
			httperr.Code(c, types.ErrorCodeModelListingNoSource, types.D{"lane": model.ChannelLaneDirect, "models": strings.Join(unroutable, ", ")})
			return
		}
		unroutable, err = modelOpsModelsWithoutLanePool(cheapOnlyNames, model.ChannelLaneAgent)
		if err != nil {
			httperr.Write(c, err)
			return
		}
		if len(unroutable) > 0 {
			httperr.Code(c, types.ErrorCodeModelListingNoSource, types.D{"lane": model.ChannelLaneAgent, "models": strings.Join(unroutable, ", ")})
			return
		}
	}
	result := model.CoreDB().Model(&model.Model{}).Where("id IN ?", ids).Update("status", status)
	if result.Error != nil {
		httperr.Write(c, result.Error)
		return
	}
	refreshModelMarketplaceCaches()
	InvalidateModelDiscoveryCache()
	common.ApiSuccess(c, gin.H{"updated": result.RowsAffected, "status": status})
}

// ---------------------------------------------------------------- helpers

func parseModelOpsWindowHours(raw string) int {
	value, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(strings.ToLower(raw)), "h"))
	if err != nil || value <= 0 {
		return modelOpsDefaultWindowHours
	}
	if value > modelOpsMaxWindowHours {
		return modelOpsMaxWindowHours
	}
	return value
}

func modelOpsSplitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func modelOpsAbilityGroupColumn() string {
	if common.UsingPostgreSQL {
		return `"group"`
	}
	return "`group`"
}

func modelOpsKeyFingerprint(digest string) string {
	digest = strings.TrimSpace(digest)
	if len(digest) < 4 {
		return ""
	}
	return digest[len(digest)-4:]
}

func modelOpsEndpointLabel(channel *model.Channel) string {
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		return constant.ChannelTypeNames[channel.Type]
	}
	trimmed := strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")
	return strings.TrimRight(trimmed, "/")
}

func modelOpsCostKey(modelName string, channelID int) string {
	return modelName + "\x00" + strconv.Itoa(channelID)
}

type modelOpsCost struct {
	input   *float64
	output  *float64
	request *float64
	second  *float64
	rows    int
}

func modelOpsModelsWithoutStablePool(names []string) ([]string, error) {
	return modelOpsModelsWithoutLanePool(names, model.ChannelLaneDirect)
}

func modelOpsModelsWithoutLanePool(names []string, lane string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	candidatesByName := make(map[string][]string, len(names))
	candidateSet := make(map[string]struct{}, len(names)*2)
	for _, name := range names {
		candidates := []string{name}
		if normalized := model.NormalizePricingModelName(name); normalized != "" && normalized != name {
			candidates = append(candidates, normalized)
		}
		candidatesByName[name] = candidates
		for _, candidate := range candidates {
			candidateSet[candidate] = struct{}{}
		}
	}
	candidates := make([]string, 0, len(candidateSet))
	for candidate := range candidateSet {
		candidates = append(candidates, candidate)
	}
	var routable []string
	query := model.CoreDB().Table("abilities").
		Distinct("abilities.model").
		Joins("JOIN channels ON channels.id = abilities.channel_id").
		Where("abilities.model IN ?", candidates).
		Where("abilities.enabled = ?", true).
		Where("abilities."+modelOpsAbilityGroupColumn()+" = ?", lane).
		Where("channels.status = ?", common.ChannelStatusEnabled)
	if searchTypes := constant.SearchChannelTypes(); len(searchTypes) > 0 {
		query = query.Where("channels.type NOT IN ?", searchTypes)
	}
	if err := query.Pluck("abilities.model", &routable).Error; err != nil {
		return nil, err
	}
	available := make(map[string]struct{}, len(routable))
	for _, name := range routable {
		available[name] = struct{}{}
	}
	missing := make([]string, 0)
	for _, name := range names {
		routable := false
		for _, candidate := range candidatesByName[name] {
			if _, ok := available[candidate]; ok {
				routable = true
				break
			}
		}
		if !routable {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

func modelOpsOutcomeTableReady() bool {
	return model.CoreDB() != nil && model.TelemetryDB() != nil
}

// modelOpsUpstreamRecord sums one upstream's hourly route health over a
// window: every customer request and every attempt a retry superseded, the
// operator's view of how that upstream answered. Probes stay out; a route
// known only from probes is reported through its observation.
type modelOpsUpstreamRecord struct {
	requests, failures, durationSum, durationCount, lastAtMs int64
	buckets                                                  map[int]modelOpsBucketCount
}

func (r *modelOpsUpstreamRecord) add(row model.RouteHealthHourly, since time.Time, windowHours int) {
	r.requests += row.Samples
	r.failures += row.Failures
	r.durationSum += row.DurationSumMs
	r.durationCount += row.DurationCount
	if row.LastAtMs > r.lastAtMs {
		r.lastAtMs = row.LastAtMs
	}
	offset := row.Hour - since.Unix()
	if offset < 0 {
		return
	}
	index := int(offset / modelOpsBucketSeconds(windowHours))
	if index >= modelOpsSliceCount(windowHours) {
		return
	}
	if r.buckets == nil {
		r.buckets = map[int]modelOpsBucketCount{}
	}
	count := r.buckets[index]
	count.requests += row.Samples
	count.errors += row.Failures
	r.buckets[index] = count
}

func (r *modelOpsUpstreamRecord) health(since time.Time, windowHours int) modelOpsHealth {
	health := modelOpsHealth{
		RequestCount: r.requests,
		ErrorCount:   r.failures,
		SuccessRate:  modelOpsSuccessRate(r.requests, r.failures),
		Buckets:      modelOpsBucketSeries(since, windowHours, r.buckets),
	}
	if r.durationCount > 0 {
		avg := float64(r.durationSum) / float64(r.durationCount)
		health.AvgLatencyMs = &avg
	}
	if r.lastAtMs > 0 {
		at := time.UnixMilli(r.lastAtMs).UTC()
		health.LastOutcomeAt = &at
	}
	return health
}

func modelOpsHealthWindow(since time.Time, windowHours int) (model.RouteHealthWindow, error) {
	return model.LoadRouteHealthWindow(context.Background(), since, since.Add(time.Duration(windowHours)*time.Hour))
}

// modelOpsChannelHealth is each upstream's record over the window, with the
// window sliced into whole-hour buckets for its status bars.
func modelOpsChannelHealth(channelIDs []int, since time.Time, windowHours int) (map[int]modelOpsHealth, bool, error) {
	result := make(map[int]modelOpsHealth, len(channelIDs))
	if len(channelIDs) == 0 || !modelOpsOutcomeTableReady() {
		return result, false, nil
	}
	window, err := modelOpsHealthWindow(since, windowHours)
	if err != nil {
		return nil, false, err
	}
	wanted := make(map[int]bool, len(channelIDs))
	for _, id := range channelIDs {
		wanted[id] = true
	}
	records := map[int]*modelOpsUpstreamRecord{}
	for _, row := range window.Rows {
		if row.Kind == model.ChannelHealthKindProbe || !wanted[row.ChannelID] {
			continue
		}
		record := records[row.ChannelID]
		if record == nil {
			record = &modelOpsUpstreamRecord{}
			records[row.ChannelID] = record
		}
		record.add(row, since, windowHours)
	}
	for _, channelID := range channelIDs {
		record := records[channelID]
		if record == nil {
			record = &modelOpsUpstreamRecord{}
		}
		result[channelID] = record.health(since, windowHours)
	}
	return result, true, nil
}

// modelOpsModelChannelHealth is the same record per (model, upstream) pair,
// keyed by the routed model name.
func modelOpsModelChannelHealth(names []string, channelIDs []int, since time.Time, windowHours int) (map[string]modelOpsHealth, bool, error) {
	result := make(map[string]modelOpsHealth)
	if len(names) == 0 || len(channelIDs) == 0 || !modelOpsOutcomeTableReady() {
		return result, false, nil
	}
	window, err := modelOpsHealthWindow(since, windowHours)
	if err != nil {
		return nil, true, err
	}
	wantedNames := make(map[string]bool, len(names))
	for _, name := range names {
		wantedNames[name] = true
	}
	wantedChannels := make(map[int]bool, len(channelIDs))
	for _, id := range channelIDs {
		wantedChannels[id] = true
	}
	records := map[string]*modelOpsUpstreamRecord{}
	for _, row := range window.Rows {
		if row.Kind == model.ChannelHealthKindProbe || !wantedNames[row.ModelName] || !wantedChannels[row.ChannelID] {
			continue
		}
		key := modelOpsCostKey(row.ModelName, row.ChannelID)
		record := records[key]
		if record == nil {
			record = &modelOpsUpstreamRecord{}
			records[key] = record
		}
		record.add(row, since, windowHours)
	}
	for key, record := range records {
		result[key] = record.health(since, windowHours)
	}
	return result, true, nil
}

type modelOpsBucketCount struct {
	requests int64
	errors   int64
}

// modelOpsWindowStart puts the window on the clock — slices run 14:00 to
// 15:00, not 14:37 to 15:37 — and the newest slice is the one now is in, the
// same rule the health page follows.
func modelOpsWindowStart(windowHours int) time.Time {
	slice := time.Duration(modelOpsBucketSeconds(windowHours)) * time.Second
	window := time.Duration(windowHours) * time.Hour
	return time.Now().UTC().Truncate(slice).Add(slice).Add(-window)
}

// modelOpsBucketSeconds is a whole number of hours, because the record is
// read from hourly rows: one hour per slice up to a 48-hour window, then two,
// three or four.
func modelOpsBucketSeconds(windowHours int) int64 {
	if windowHours <= 0 {
		windowHours = modelOpsDefaultWindowHours
	}
	hours := (windowHours + modelOpsProbeBuckets - 1) / modelOpsProbeBuckets
	return int64(hours) * 3600
}

// modelOpsSliceCount is how many slices cover the window.
func modelOpsSliceCount(windowHours int) int {
	if windowHours <= 0 {
		windowHours = modelOpsDefaultWindowHours
	}
	hours := int(modelOpsBucketSeconds(windowHours) / 3600)
	return (windowHours + hours - 1) / hours
}

// modelOpsBucketSeries lays per-slice counts onto the full window. A slice
// that saw nothing keeps a null rate, so it draws as "no data" rather than as
// an outage.
func modelOpsBucketSeries(since time.Time, windowHours int, counts map[int]modelOpsBucketCount) []modelOpsProbeBucket {
	width := time.Duration(modelOpsBucketSeconds(windowHours)) * time.Second
	series := make([]modelOpsProbeBucket, modelOpsSliceCount(windowHours))
	for index := range series {
		start := since.Add(time.Duration(index) * width)
		count := counts[index]
		series[index] = modelOpsProbeBucket{
			StartAt:      start,
			EndAt:        start.Add(width),
			RequestCount: count.requests,
			ErrorCount:   count.errors,
			SuccessRate:  modelOpsSuccessRate(count.requests, count.errors),
		}
	}
	return series
}

func modelOpsSuccessRate(requestCount int64, errorCount int64) *float64 {
	if requestCount <= 0 {
		return nil
	}
	rate := float64(requestCount-errorCount) / float64(requestCount)
	if rate < 0 {
		rate = 0
	}
	return &rate
}

func modelOpsFloatOrNil(value float64, requestCount int64) *float64 {
	if requestCount <= 0 || value <= 0 {
		return nil
	}
	return &value
}

// modelOpsChannelCostBasis reduces a channel's provider_cost_prices rows to one
// representative value per billing dimension. The median is used rather than
// the mean so one oddly priced model cannot distort the ledger's 成本基准 column.
func modelOpsChannelCostBasis(channelIDs []int) (map[int]modelOpsCost, error) {
	result := make(map[int]modelOpsCost, len(channelIDs))
	if len(channelIDs) == 0 || model.CoreDB() == nil || !model.CoreDB().Migrator().HasTable(&model.ProviderCostPrice{}) {
		return result, nil
	}
	var rows []model.ProviderCostPrice
	if err := model.CoreDB().Where("channel_id IN ?", channelIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	inputs := make(map[int][]float64)
	outputs := make(map[int][]float64)
	requests := make(map[int][]float64)
	seconds := make(map[int][]float64)
	counts := make(map[int]int)
	for _, row := range rows {
		counts[row.ChannelID]++
		if row.InputPerMtok != nil {
			inputs[row.ChannelID] = append(inputs[row.ChannelID], *row.InputPerMtok)
		}
		if row.OutputPerMtok != nil {
			outputs[row.ChannelID] = append(outputs[row.ChannelID], *row.OutputPerMtok)
		}
		if row.RequestUSD != nil {
			requests[row.ChannelID] = append(requests[row.ChannelID], *row.RequestUSD)
		}
		if row.SecondUSD != nil {
			seconds[row.ChannelID] = append(seconds[row.ChannelID], *row.SecondUSD)
		}
	}
	for channelID, count := range counts {
		result[channelID] = modelOpsCost{
			input:   modelOpsMedian(inputs[channelID]),
			output:  modelOpsMedian(outputs[channelID]),
			request: modelOpsMedian(requests[channelID]),
			second:  modelOpsMedian(seconds[channelID]),
			rows:    count,
		}
	}
	return result, nil
}

// modelOpsModelChannelCosts resolves the exact cost row behind one (model,
// source) pair, falling back to the seed row (channel_id = 0) the billing path
// itself falls back to.
func modelOpsModelChannelCosts(names []string, channelIDs []int) (map[string]modelOpsCost, error) {
	result := make(map[string]modelOpsCost)
	if len(names) == 0 || len(channelIDs) == 0 || model.CoreDB() == nil || !model.CoreDB().Migrator().HasTable(&model.ProviderCostPrice{}) {
		return result, nil
	}
	candidatesByName := make(map[string][]string, len(names))
	candidateSet := make(map[string]struct{}, len(names)*2)
	for _, name := range names {
		candidates := []string{name}
		if normalized := model.NormalizePricingModelName(name); normalized != "" && normalized != name {
			candidates = append(candidates, normalized)
		}
		candidatesByName[name] = candidates
		for _, candidate := range candidates {
			candidateSet[candidate] = struct{}{}
		}
	}
	candidateNames := make([]string, 0, len(candidateSet))
	for candidate := range candidateSet {
		candidateNames = append(candidateNames, candidate)
	}
	scoped := append([]int{0}, channelIDs...)
	var rows []model.ProviderCostPrice
	if err := model.CoreDB().
		Where("model_name IN ? AND channel_id IN ?", candidateNames, scoped).
		Order("pricing_version DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	shared := make(map[string]modelOpsCost)
	specific := make(map[string]modelOpsCost)
	for _, row := range rows {
		cost := modelOpsCost{input: row.InputPerMtok, output: row.OutputPerMtok, request: row.RequestUSD, second: row.SecondUSD, rows: 1}
		if row.ChannelID == 0 {
			if _, exists := shared[row.ModelName]; !exists {
				shared[row.ModelName] = cost
			}
			continue
		}
		key := modelOpsCostKey(row.ModelName, row.ChannelID)
		if _, exists := specific[key]; !exists {
			specific[key] = cost
		}
	}
	for _, name := range names {
		for _, channelID := range channelIDs {
			resultKey := modelOpsCostKey(name, channelID)
			for _, candidate := range candidatesByName[name] {
				if cost, ok := specific[modelOpsCostKey(candidate, channelID)]; ok {
					result[resultKey] = cost
					break
				}
			}
			if _, exists := result[resultKey]; exists {
				continue
			}
			for _, candidate := range candidatesByName[name] {
				if fallback, ok := shared[candidate]; ok {
					result[resultKey] = scaleModelOpsCost(fallback, channelCostMultiplier(channelID))
					break
				}
			}
		}
	}
	return result, nil
}

func modelOpsMedian(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return &sorted[middle]
	}
	average := (sorted[middle-1] + sorted[middle]) / 2
	return &average
}

func modelOpsRoleRank(role string) int {
	switch model.NormalizeAbilityRole(role) {
	case model.AbilityRoleBackup:
		return 1
	case model.AbilityRoleInventory:
		return 2
	default:
		return 0
	}
}

// modelOpsRouteProbeHealth is each route's probe record over the window, in
// the same shape as its traffic record, for routes that carried no traffic.
func modelOpsRouteProbeHealth(channelIDs []int, since time.Time, windowHours int) (map[string]modelOpsHealth, error) {
	result := map[string]modelOpsHealth{}
	tallies, err := model.LoadRouteProbeTallies(channelIDs, since)
	if err != nil || len(tallies) == 0 {
		return result, err
	}
	buckets, err := model.LoadRouteProbeBuckets(channelIDs, since, modelOpsBucketSeconds(windowHours))
	if err != nil {
		return result, err
	}
	counts := map[string]map[int]modelOpsBucketCount{}
	for _, bucket := range buckets {
		key := model.ChannelHealthCellKey(bucket.ModelName, bucket.Lane, bucket.ChannelId)
		if counts[key] == nil {
			counts[key] = map[int]modelOpsBucketCount{}
		}
		counts[key][bucket.BucketIndex] = modelOpsBucketCount{bucket.Samples, bucket.Failures}
	}
	for key, tally := range tallies {
		result[key] = modelOpsHealth{
			RequestCount: tally.Samples,
			ErrorCount:   tally.Failures,
			SuccessRate:  modelOpsSuccessRate(tally.Samples, tally.Failures),
			AvgLatencyMs: modelOpsFloatOrNil(tally.AvgLatencyMs, tally.Samples),
			Buckets:      modelOpsBucketSeries(since, windowHours, counts[key]),
			Source:       "probe",
		}
	}
	return result, nil
}

type modelOpsSourceObservation struct {
	view        *observationView
	probeHealth modelOpsHealth
}

// modelOpsSourceObservations folds each source's watched routes into one view
// for the ledger, with the probe record of those routes over the window.
func modelOpsSourceObservations(channelIDs []int, since time.Time, windowHours int) (map[int]modelOpsSourceObservation, error) {
	result := map[int]modelOpsSourceObservation{}
	views, watched, err := buildObservationViews(channelIDs, time.Now().UTC())
	if err != nil || len(watched) == 0 {
		return result, err
	}
	ids := observationChannelIDs(watched)
	type liveRow struct {
		ChannelId int
		Routes    int
	}
	var live []liveRow
	if err := model.CoreDB().Model(&model.Ability{}).
		Select("channel_id, COUNT(*) AS routes").
		Where("channel_id IN ? AND role <> ?", ids, model.AbilityRoleInventory).
		Group("channel_id").
		Scan(&live).Error; err != nil {
		return result, err
	}
	liveRoutes := map[int]int{}
	for _, row := range live {
		liveRoutes[row.ChannelId] = row.Routes
	}
	probes, err := modelOpsRouteProbeHealth(ids, since, windowHours)
	if err != nil {
		return result, err
	}
	earliest := time.Now().UTC()
	byChannel := map[int][]model.RouteObservation{}
	for _, row := range watched {
		if row.StartedAt.Before(earliest) {
			earliest = row.StartedAt
		}
		byChannel[row.ChannelId] = append(byChannel[row.ChannelId], row)
	}
	probeErrors, err := model.LoadRouteLastProbeErrors(ids, earliest)
	if err != nil {
		return result, err
	}
	perChannel := map[int][]observationView{}
	models := map[int]map[string]bool{}
	pooled := map[int][]modelOpsHealth{}
	for _, row := range watched {
		key := model.ChannelHealthCellKey(row.ModelName, row.Lane, row.ChannelId)
		perChannel[row.ChannelId] = append(perChannel[row.ChannelId], views[key])
		if models[row.ChannelId] == nil {
			models[row.ChannelId] = map[string]bool{}
		}
		models[row.ChannelId][row.ModelName] = true
		if probe, ok := probes[key]; ok {
			pooled[row.ChannelId] = append(pooled[row.ChannelId], probe)
		}
	}
	for channelID, list := range perChannel {
		view := aggregateObservationViews(list, len(models[channelID]))
		view.LiveRoutes = liveRoutes[channelID]
		view.Summary = summarizeObservationRoutes(byChannel[channelID], views, probeErrors, probes, time.Now().UTC())
		result[channelID] = modelOpsSourceObservation{
			view:        &view,
			probeHealth: modelOpsPoolHealth(pooled[channelID], since, windowHours),
		}
	}
	return result, nil
}

// modelOpsPoolHealth sums several routes' records slice by slice.
func modelOpsPoolHealth(records []modelOpsHealth, since time.Time, windowHours int) modelOpsHealth {
	result := modelOpsHealth{Source: "probe"}
	if len(records) == 0 {
		return result
	}
	counts := map[int]modelOpsBucketCount{}
	var latency float64
	for _, record := range records {
		result.RequestCount += record.RequestCount
		result.ErrorCount += record.ErrorCount
		if record.AvgLatencyMs != nil {
			latency += *record.AvgLatencyMs * float64(record.RequestCount)
		}
		for index, bucket := range record.Buckets {
			current := counts[index]
			current.requests += bucket.RequestCount
			current.errors += bucket.ErrorCount
			counts[index] = current
		}
	}
	result.SuccessRate = modelOpsSuccessRate(result.RequestCount, result.ErrorCount)
	if result.RequestCount > 0 {
		avg := latency / float64(result.RequestCount)
		result.AvgLatencyMs = &avg
	}
	result.Buckets = modelOpsBucketSeries(since, windowHours, counts)
	return result
}

// channelCostMultiplier is the source's cost_multiplier setting, which scales
// the official list price it falls back to (billing applies the same rule).
func channelCostMultiplier(channelID int) float64 {
	channel, err := model.CacheGetChannel(channelID)
	if err != nil || channel == nil {
		return 0
	}
	return channel.GetOtherSettings().CostMultiplier
}

func scaleModelOpsCost(cost modelOpsCost, factor float64) modelOpsCost {
	if factor <= 0 || factor == 1 {
		return cost
	}
	scale := func(value *float64) *float64 {
		if value == nil {
			return nil
		}
		scaled := *value * factor
		return &scaled
	}
	cost.input, cost.output, cost.request, cost.second = scale(cost.input), scale(cost.output), scale(cost.request), scale(cost.second)
	return cost
}
