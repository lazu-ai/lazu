package model

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The overview's client mix, the usage log's filtered 用户 tab and every
// project-scoped usage page (the wallet, a member's own usage) summarise a
// range of requests by project, member, key, model, client, channel and
// group. On 2026-10-16 they read the request log for it: 1 s for the
// platform's ninety days of clients, 1.7 s to 6 s for a filtered thirty-day
// user tab, about 3 s for the busiest project's thirty days.
//
// A usage slice is one hour of requests that share all of those facts, with
// the sums and the latency histogram the pages show. Thirty days of the
// platform are 17k slices against 361k requests, the busiest project 1.3k.
// The batched relay consumer writes them with the log rows; schema 34 filled
// them for every retained request. Readers take whole hours from the slices
// and only a window's partial first and last hour from the log.

const (
	usageSliceTextLimit  = 191
	usageSliceShortLimit = 64
)

// DashboardUsageHourlySlice is one hour of consume (type 2) or rejected
// (type 5) requests with one combination of the usage pages' facts. Text keys
// keep the log's raw value (empty for none), so readers name them as the raw
// queries do. Outcome sums count consume requests only.
type DashboardUsageHourlySlice struct {
	ID                int64   `json:"id" gorm:"primaryKey"`
	BucketHour        int64   `json:"bucket_hour" gorm:"not null;uniqueIndex:idx_usage_slice_key,priority:1;index:idx_usage_slice_project,priority:2;index:idx_usage_slice_user,priority:2"`
	LogType           int     `json:"log_type" gorm:"not null;uniqueIndex:idx_usage_slice_key,priority:2"`
	ProjectID         int     `json:"project_id" gorm:"not null;default:0;uniqueIndex:idx_usage_slice_key,priority:3;index:idx_usage_slice_project,priority:1"`
	UserID            int     `json:"user_id" gorm:"not null;default:0;uniqueIndex:idx_usage_slice_key,priority:4;index:idx_usage_slice_user,priority:1"`
	TokenID           int     `json:"token_id" gorm:"not null;default:0;uniqueIndex:idx_usage_slice_key,priority:5"`
	ChannelID         int     `json:"channel_id" gorm:"not null;default:0;uniqueIndex:idx_usage_slice_key,priority:6"`
	ModelName         string  `json:"model_name" gorm:"size:191;not null;default:'';uniqueIndex:idx_usage_slice_key,priority:7"`
	Client            string  `json:"client" gorm:"size:64;not null;default:'';uniqueIndex:idx_usage_slice_key,priority:8"`
	LogGroup          string  `json:"log_group" gorm:"size:64;not null;default:'';uniqueIndex:idx_usage_slice_key,priority:9"`
	Username          string  `json:"username" gorm:"size:191;not null;default:'';uniqueIndex:idx_usage_slice_key,priority:10"`
	TokenName         string  `json:"token_name" gorm:"size:191;not null;default:'';uniqueIndex:idx_usage_slice_key,priority:11"`
	RequestCount      int64   `json:"request_count" gorm:"not null;default:0"`
	AmountMicrousd    int64   `json:"amount_microusd" gorm:"not null;default:0"`
	PromptTokens      int64   `json:"prompt_tokens" gorm:"not null;default:0"`
	CompletionTokens  int64   `json:"completion_tokens" gorm:"not null;default:0"`
	CachedInputTokens int64   `json:"cached_input_tokens" gorm:"not null;default:0"`
	UpstreamCostUSD   float64 `json:"upstream_cost_usd" gorm:"column:upstream_cost_usd;not null;default:0"`
	GrossMarginUSD    float64 `json:"gross_margin_usd" gorm:"column:gross_margin_usd;not null;default:0"`
	CostSampleCount   int64   `json:"cost_sample_count" gorm:"not null;default:0"`
	ErrorCount        int64   `json:"error_count" gorm:"not null;default:0"`
	LatencyMsSum      int64   `json:"latency_ms_sum" gorm:"not null;default:0"`
	LatencySamples    int64   `json:"latency_samples" gorm:"not null;default:0"`
	// LatencyBuckets is a dense JSON histogram on the route health buckets
	// (within 9.6%), merged bin by bin on conflict like the TTFT histogram.
	LatencyBuckets string `json:"latency_buckets" gorm:"type:text;not null;default:''"`
	LastUsedAt     int64  `json:"last_used_at" gorm:"not null;default:0"`
}

func (DashboardUsageHourlySlice) TableName() string { return "dashboard_usage_hourly_slices" }

type usageSliceKey struct {
	Hour      int64
	LogType   int
	ProjectID int
	UserID    int
	TokenID   int
	ChannelID int
	ModelName string
	Client    string
	LogGroup  string
	Username  string
	TokenName string
}

func (k usageSliceKey) less(o usageSliceKey) bool {
	ints := [][2]int64{{k.Hour, o.Hour}, {int64(k.LogType), int64(o.LogType)}, {int64(k.ProjectID), int64(o.ProjectID)},
		{int64(k.UserID), int64(o.UserID)}, {int64(k.TokenID), int64(o.TokenID)}, {int64(k.ChannelID), int64(o.ChannelID)}}
	for _, pair := range ints {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	texts := [][2]string{{k.ModelName, o.ModelName}, {k.Client, o.Client}, {k.LogGroup, o.LogGroup}, {k.Username, o.Username}, {k.TokenName, o.TokenName}}
	for _, pair := range texts {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return false
}

// usageSliceFact is one request as the raw usage queries see it: the log's
// facts and, for a consume request, its routing outcome joined by request id.
type usageSliceFact struct {
	LogType           int
	ProjectID         int
	UserID            int
	TokenID           int
	ChannelID         int
	ModelName         string
	Client            string
	LogGroup          string
	Username          string
	TokenName         string
	CreatedAt         int64
	AmountMicrousd    int64
	PromptTokens      int64
	CompletionTokens  int64
	CachedInputTokens *int64
	UpstreamCostUSD   *float64
	GrossMarginUSD    *float64
	StatusCode        *int64
	TotalDurationMs   *int64
}

type usageSliceAccumulator struct {
	slices  map[usageSliceKey]*DashboardUsageHourlySlice
	latency map[usageSliceKey][]int64
}

func newUsageSliceAccumulator() *usageSliceAccumulator {
	return &usageSliceAccumulator{slices: map[usageSliceKey]*DashboardUsageHourlySlice{}, latency: map[usageSliceKey][]int64{}}
}

func (a *usageSliceAccumulator) add(fact usageSliceFact) {
	key := usageSliceKey{
		Hour: dashboardRollupHour(fact.CreatedAt), LogType: fact.LogType, ProjectID: fact.ProjectID, UserID: fact.UserID,
		TokenID: fact.TokenID, ChannelID: fact.ChannelID,
		ModelName: truncateString(fact.ModelName, usageSliceTextLimit), Client: truncateString(fact.Client, usageSliceShortLimit),
		LogGroup: truncateString(fact.LogGroup, usageSliceShortLimit), Username: truncateString(fact.Username, usageSliceTextLimit),
		TokenName: truncateString(fact.TokenName, usageSliceTextLimit),
	}
	slice := a.slices[key]
	if slice == nil {
		slice = &DashboardUsageHourlySlice{BucketHour: key.Hour, LogType: key.LogType, ProjectID: key.ProjectID, UserID: key.UserID, TokenID: key.TokenID,
			ChannelID: key.ChannelID, ModelName: key.ModelName, Client: key.Client, LogGroup: key.LogGroup, Username: key.Username, TokenName: key.TokenName}
		a.slices[key] = slice
	}
	slice.RequestCount++
	slice.AmountMicrousd += fact.AmountMicrousd
	slice.PromptTokens += fact.PromptTokens
	slice.CompletionTokens += fact.CompletionTokens
	if fact.CreatedAt > slice.LastUsedAt {
		slice.LastUsedAt = fact.CreatedAt
	}
	if fact.CachedInputTokens != nil {
		slice.CachedInputTokens += *fact.CachedInputTokens
	}
	if fact.UpstreamCostUSD != nil {
		slice.UpstreamCostUSD += *fact.UpstreamCostUSD
		slice.CostSampleCount++
	}
	if fact.GrossMarginUSD != nil {
		slice.GrossMarginUSD += *fact.GrossMarginUSD
	}
	if fact.StatusCode != nil && *fact.StatusCode >= 400 {
		slice.ErrorCount++
	}
	if fact.TotalDurationMs != nil {
		slice.LatencyMsSum += *fact.TotalDurationMs
		slice.LatencySamples++
		a.latency[key] = addDashboardTTFTBucket(a.latency[key], *fact.TotalDurationMs)
	}
}

// rows returns the accumulated slices in key order, the lock order of every
// writer, with their histograms encoded.
func (a *usageSliceAccumulator) rows() []DashboardUsageHourlySlice {
	keys := make([]usageSliceKey, 0, len(a.slices))
	for key := range a.slices {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
	out := make([]DashboardUsageHourlySlice, 0, len(keys))
	for _, key := range keys {
		slice := *a.slices[key]
		slice.LatencyBuckets = *encodeDashboardTTFTBuckets(a.latency[key])
		out = append(out, slice)
	}
	return out
}

func usageSliceFactFromLog(log *Log, outcome *RoutingOutcome) usageSliceFact {
	fact := usageSliceFact{LogType: log.Type, ProjectID: log.ProjectID, UserID: log.UserId, TokenID: log.TokenId, ChannelID: log.ChannelId,
		ModelName: log.ModelName, Client: log.ClientFramework, LogGroup: log.Group, Username: log.Username, TokenName: log.TokenName,
		CreatedAt: log.CreatedAt, AmountMicrousd: log.AmountMicrousd.Int64(), PromptTokens: int64(log.PromptTokens), CompletionTokens: int64(log.CompletionTokens)}
	if log.Type != LogTypeConsume || outcome == nil {
		return fact
	}
	toInt64 := func(value *int) *int64 {
		if value == nil {
			return nil
		}
		v := int64(*value)
		return &v
	}
	fact.CachedInputTokens, fact.StatusCode, fact.TotalDurationMs = toInt64(outcome.CachedInputTokens), toInt64(outcome.StatusCode), toInt64(outcome.TotalDurationMs)
	fact.UpstreamCostUSD, fact.GrossMarginUSD = outcome.UpstreamCostUSD, outcome.GrossMarginUSD
	return fact
}

var usageSliceCounters = []string{"request_count", "amount_microusd", "prompt_tokens", "completion_tokens", "cached_input_tokens",
	"upstream_cost_usd", "gross_margin_usd", "cost_sample_count", "error_count", "latency_ms_sum", "latency_samples"}

var usageSliceKeyColumns = []string{"bucket_hour", "log_type", "project_id", "user_id", "token_id", "channel_id", "model_name", "client", "log_group", "username", "token_name"}

// recordUsageSlicesBatchTx adds a committed batch's consume and rejected
// requests to their slices: one statement per batch, in the batch's
// transaction. Slices exist on PostgreSQL; embedded installs read raw logs.
func recordUsageSlicesBatchTx(tx *gorm.DB, logs []Log, events []relayResultEvent) error {
	if !routingFactsAreViews(tx) {
		return nil
	}
	outcomes := map[string]*RoutingOutcome{}
	for _, event := range events {
		if event.RoutingOutcome != nil {
			outcomes[event.RoutingOutcome.RequestID] = event.RoutingOutcome
		}
	}
	acc := newUsageSliceAccumulator()
	for i := range logs {
		row := &logs[i]
		if row.Type != LogTypeConsume && row.Type != LogTypeError {
			continue
		}
		var outcome *RoutingOutcome
		if row.RequestID != "" {
			outcome = outcomes[row.RequestID]
		}
		acc.add(usageSliceFactFromLog(row, outcome))
	}
	return upsertUsageSlicesTx(tx, acc.rows())
}

func upsertUsageSlicesTx(tx *gorm.DB, rows []DashboardUsageHourlySlice) error {
	if len(rows) == 0 {
		return nil
	}
	table := DashboardUsageHourlySlice{}.TableName()
	return batchUpsert(tx, &rows, table, usageSliceKeyColumns, usageSliceCounters, map[string]interface{}{
		"latency_buckets": mergeHistogramSQL(table, "latency_buckets"),
		"last_used_at":    excludedExtreme(table, "last_used_at", true),
	})
}

// recordUsageSlicesForLogTx adds one log written outside the batched
// consumer (a media job's charge) to its slice, read back through the same
// fact query a rebuild uses, so the two agree on its outcome join.
func recordUsageSlicesForLogTx(tx *gorm.DB, log *Log) error {
	if log == nil || log.Id == 0 || (log.Type != LogTypeConsume && log.Type != LogTypeError) || !routingFactsAreViews(tx) {
		return nil
	}
	hour := dashboardRollupHour(log.CreatedAt)
	facts, err := usageSliceFactsContext(tx.Statement.Context, tx, hour, hour+dashboardRollupHourSeconds, []int{log.Type}, true, func(query *gorm.DB) *gorm.DB {
		return query.Where("logs.id = ?", log.Id)
	})
	if err != nil {
		return err
	}
	acc := newUsageSliceAccumulator()
	for _, fact := range facts {
		acc.add(fact)
	}
	return upsertUsageSlicesTx(tx, acc.rows())
}

// usageSliceFactsContext reads [from, to) of the request log as slice facts.
// scope narrows the logs (column names logs.*). Consume requests join their
// outcome exactly as the raw dashboards do; rejected ones need only counts.
func usageSliceFactsContext(ctx context.Context, db *gorm.DB, from, to int64, logTypes []int, withOutcome bool, scope func(*gorm.DB) *gorm.DB) ([]usageSliceFact, error) {
	var facts []usageSliceFact
	if from >= to {
		return facts, nil
	}
	keys := `logs.type AS log_type, COALESCE(logs.project_id,0) AS project_id, COALESCE(logs.user_id,0) AS user_id,
		COALESCE(logs.token_id,0) AS token_id, COALESCE(logs.channel_id,0) AS channel_id, COALESCE(logs.model_name,'') AS model_name,
		COALESCE(logs.client_framework,'') AS client, COALESCE(logs."group",'') AS log_group, COALESCE(logs.username,'') AS username,
		COALESCE(logs.token_name,'') AS token_name, logs.created_at, COALESCE(logs.amount_microusd,0) AS amount_microusd,
		COALESCE(logs.prompt_tokens,0) AS prompt_tokens, COALESCE(logs.completion_tokens,0) AS completion_tokens`
	for _, logType := range logTypes {
		query := db.WithContext(ctx).Table("logs").Where("logs.type = ? AND logs.created_at >= ? AND logs.created_at < ?", logType, from, to)
		if scope != nil {
			query = scope(query)
		}
		selection := keys
		if logType == LogTypeConsume && withOutcome {
			query = dashboardUsageWithBoundedOutcome(query)
			selection += `, routing_outcome.cached_input_tokens, routing_outcome.upstream_cost_usd, routing_outcome.gross_margin_usd,
				routing_outcome.status_code, routing_outcome.total_duration_ms`
		}
		var rows []usageSliceFact
		if err := query.Select(selection).Scan(&rows).Error; err != nil {
			return nil, err
		}
		facts = append(facts, rows...)
	}
	return facts, nil
}

// usageSliceRebuildSpan is one rebuild transaction; the busiest six hours
// hold about 25k requests.
const usageSliceRebuildSpan = 6 * time.Hour

// rebuildUsageSlices recomputes every slice of [from, to) from the request
// log, six hours per transaction. Writers must be stopped for hours that
// still receive requests.
func rebuildUsageSlices(ctx context.Context, db *gorm.DB, from, to time.Time) error {
	for span := from.UTC().Truncate(usageSliceRebuildSpan); span.Before(to); span = span.Add(usageSliceRebuildSpan) {
		if err := ctx.Err(); err != nil {
			return err
		}
		start, end := span, span.Add(usageSliceRebuildSpan)
		if start.Before(from) {
			start = from.UTC().Truncate(time.Hour)
		}
		if end.After(to) {
			end = to
		}
		if err := withoutJIT(db, func(tx *gorm.DB) error {
			if err := tx.Where("bucket_hour >= ? AND bucket_hour < ?", start.Unix(), end.Unix()).Delete(&DashboardUsageHourlySlice{}).Error; err != nil {
				return err
			}
			facts, err := usageSliceFactsContext(ctx, tx, start.Unix(), end.Unix(), []int{LogTypeConsume, LogTypeError}, true, nil)
			if err != nil {
				return err
			}
			acc := newUsageSliceAccumulator()
			for _, fact := range facts {
				acc.add(fact)
			}
			rows := acc.rows()
			if len(rows) == 0 {
				return nil
			}
			return tx.CreateInBatches(&rows, 500).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

// usageSlicesAvailable reports whether the Telemetry store keeps slices.
func usageSlicesAvailable() bool {
	return dashboardRollupEnabled() && routingFactsAreViews(TelemetryDB())
}

// usageSliceRead is one usage page's request scope, as slice and as raw log
// predicates that select the same requests.
type usageSliceRead struct {
	logTypes    []int
	withOutcome bool
	columns     string
	slices      func(*gorm.DB) *gorm.DB
	raw         func(*gorm.DB) *gorm.DB
}

// loadUsageSlicesContext returns [from, to) as slices: the whole hours from
// the slice table and the partial first and last hour folded from the log.
func loadUsageSlicesContext(ctx context.Context, read usageSliceRead, from, to int64) ([]DashboardUsageHourlySlice, error) {
	if from >= to {
		return nil, nil
	}
	db := TelemetryDB().WithContext(ctx)
	firstWhole := dashboardRollupHour(from)
	if firstWhole < from {
		firstWhole += dashboardRollupHourSeconds
	}
	lastWhole := dashboardRollupHour(to)
	acc := newUsageSliceAccumulator()
	addRaw := func(start, end int64) error {
		facts, err := usageSliceFactsContext(ctx, db, start, end, read.logTypes, read.withOutcome, read.raw)
		if err != nil {
			return err
		}
		for _, fact := range facts {
			acc.add(fact)
		}
		return nil
	}
	if firstWhole >= lastWhole {
		if err := addRaw(from, to); err != nil {
			return nil, err
		}
		return acc.rows(), nil
	}
	if err := addRaw(from, firstWhole); err != nil {
		return nil, err
	}
	if err := addRaw(lastWhole, to); err != nil {
		return nil, err
	}
	var rows []DashboardUsageHourlySlice
	query := db.Model(&DashboardUsageHourlySlice{}).Where("bucket_hour >= ? AND bucket_hour < ? AND log_type IN ?", firstWhole, lastWhole, read.logTypes)
	if read.columns != "" {
		query = query.Select(read.columns)
	}
	if read.slices != nil {
		query = read.slices(query)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return append(rows, acc.rows()...), nil
}

// usageSliceGroup sums slices into one dashboard row.
type usageSliceGroup struct {
	row       DashboardUsageDimensionRow
	upstream  float64
	gross     float64
	latency   []int64
	latencyN  int64
	latencyMs int64
}

func (g *usageSliceGroup) add(slice *DashboardUsageHourlySlice, withLatency bool) {
	g.row.RequestCount += slice.RequestCount
	g.row.AmountMicrousd += slice.AmountMicrousd
	g.row.PromptTokens += slice.PromptTokens
	g.row.CompletionTokens += slice.CompletionTokens
	g.row.CachedInputTokens += slice.CachedInputTokens
	g.row.CostSampleCount += slice.CostSampleCount
	g.row.ErrorCount += slice.ErrorCount
	g.upstream += slice.UpstreamCostUSD
	g.gross += slice.GrossMarginUSD
	if slice.LastUsedAt > g.row.LastUsedAt {
		g.row.LastUsedAt = slice.LastUsedAt
	}
	if withLatency && slice.LatencySamples > 0 {
		g.latencyN += slice.LatencySamples
		g.latencyMs += slice.LatencyMsSum
		g.latency = addUsageSliceHistogram(g.latency, slice.LatencyBuckets)
	}
}

// usageSliceMicrousd converts a dollar sum as the raw queries do:
// CAST(SUM(usd) * 1000000 AS BIGINT), which rounds half to even.
func usageSliceMicrousd(usd float64) int64 {
	return int64(math.RoundToEven(usd * 1000000))
}

func (g *usageSliceGroup) result() DashboardUsageDimensionRow {
	row := g.row
	row.UpstreamMicrousd, row.GrossMicrousd = usageSliceMicrousd(g.upstream), usageSliceMicrousd(g.gross)
	if g.latencyN > 0 {
		row.AvgLatencyMs = float64(g.latencyMs) / float64(g.latencyN)
		_, p95 := dashboardTTFTP95FromBuckets(g.latency)
		if p95 != nil {
			row.P95LatencyMs = *p95
		}
	}
	return row
}

// addUsageSliceHistogram adds a dense JSON histogram ("[1, 0, 2]") into dst
// without a JSON decoder; a thirty-day group reads thousands of them.
func addUsageSliceHistogram(dst []int64, encoded string) []int64 {
	if len(dst) != routeHealthHistogramSize {
		dst = newDashboardTTFTBuckets()
	}
	index := 0
	var value int64
	digits := false
	for i := 0; i < len(encoded); i++ {
		c := encoded[i]
		switch {
		case c >= '0' && c <= '9':
			value = value*10 + int64(c-'0')
			digits = true
		case c == ',' || c == ']':
			if digits && index < len(dst) {
				dst[index] += value
			}
			if digits || c == ',' {
				index++
			}
			value, digits = 0, false
		}
	}
	return dst
}

// rankUsageSliceGroups orders rows as the raw queries do (amount, then
// requests, descending; then name) and keeps the first limit.
func rankUsageSliceGroups(groups map[string]*usageSliceGroup, limit int) []DashboardUsageDimensionRow {
	rows := make([]DashboardUsageDimensionRow, 0, len(groups))
	for _, group := range groups {
		rows = append(rows, group.result())
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].AmountMicrousd != rows[j].AmountMicrousd {
			return rows[i].AmountMicrousd > rows[j].AmountMicrousd
		}
		if rows[i].RequestCount != rows[j].RequestCount {
			return rows[i].RequestCount > rows[j].RequestCount
		}
		return rows[i].Name < rows[j].Name
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	populateDashboardUsageRowsDerived(rows)
	return rows
}

func usageSliceName(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func groupUsageSlices(slices []DashboardUsageHourlySlice, withLatency bool, key func(*DashboardUsageHourlySlice) (string, int, string)) map[string]*usageSliceGroup {
	groups := map[string]*usageSliceGroup{}
	for i := range slices {
		slice := &slices[i]
		if slice.LogType != LogTypeConsume {
			continue
		}
		groupKey, id, name := key(slice)
		group := groups[groupKey]
		if group == nil {
			group = &usageSliceGroup{row: DashboardUsageDimensionRow{ID: id, Name: name}}
			groups[groupKey] = group
		}
		if name > group.row.Name {
			group.row.Name = name
		}
		group.add(slice, withLatency)
	}
	return groups
}

func usageSliceIDKey(id int, name string) string {
	return strconv.Itoa(id) + "\x00" + name
}

// --- project usage (wallet, a member's own usage) --------------------------

// usageSliceProjectRead selects ApplyProjectLogAccess's requests.
func usageSliceProjectRead(access ProjectLogAccess) usageSliceRead {
	return usageSliceRead{
		logTypes:    []int{LogTypeConsume, LogTypeError},
		withOutcome: true,
		raw:         func(tx *gorm.DB) *gorm.DB { return ApplyProjectLogAccess(tx, access) },
		slices: func(tx *gorm.DB) *gorm.DB {
			if access.Project.ID <= 0 || access.Viewer.UserID <= 0 || access.Viewer.ProjectID != access.Project.ID {
				return tx.Where("1 = 0")
			}
			tx = tx.Where("(project_id = ? OR (project_id = 0 AND user_id = ?))", access.Project.ID, access.Project.OwnerUserID)
			if !access.Viewer.IsManager() {
				if access.Viewer.Status != ProjectMemberActive && access.Viewer.Status != ProjectMemberSuspended {
					return tx.Where("1 = 0")
				}
				tx = tx.Where("user_id = ?", access.Viewer.UserID)
			}
			if access.ActorID != nil {
				tx = tx.Where("user_id = ?", *access.ActorID)
			}
			if access.TokenID > 0 {
				tx = tx.Where("token_id = ?", access.TokenID)
			}
			return tx
		},
	}
}

// buildProjectDashboardUsageFromSlicesContext answers the project usage page
// from slices. Customer pages carry no vendor cost; the caller zeroes it.
func buildProjectDashboardUsageFromSlicesContext(ctx context.Context, access ProjectLogAccess, window DashboardUsageWindow, result *DashboardUsage) error {
	slices, err := loadUsageSlicesContext(ctx, usageSliceProjectRead(access), window.StartTimestamp, window.EndTimestamp+1)
	if err != nil {
		return err
	}
	internalIDs, err := hiddenTokenIDs()
	if err != nil {
		return err
	}
	internal := make(map[int]struct{}, len(internalIDs))
	for _, id := range internalIDs {
		internal[id] = struct{}{}
	}
	var totals usageSliceGroup
	users, keys := map[int]struct{}{}, map[int]struct{}{}
	days := map[int64]*usageSliceGroup{}
	for i := range slices {
		slice := &slices[i]
		if slice.LogType == LogTypeError {
			result.Totals.ErrorCount += slice.RequestCount
			continue
		}
		totals.add(slice, false)
		if slice.UserID > 0 {
			users[slice.UserID] = struct{}{}
		}
		if _, hidden := internal[slice.TokenID]; slice.TokenID > 0 && !hidden {
			keys[slice.TokenID] = struct{}{}
		}
		day := (slice.BucketHour / secondsInDay) * secondsInDay
		if days[day] == nil {
			days[day] = &usageSliceGroup{}
		}
		days[day].add(slice, false)
	}
	sum := totals.result()
	rejected := result.Totals.ErrorCount
	result.Totals = DashboardUsageTotals{RequestCount: sum.RequestCount, AmountMicrousd: sum.AmountMicrousd, UpstreamMicrousd: sum.UpstreamMicrousd,
		GrossMicrousd: sum.GrossMicrousd, CostSampleCount: sum.CostSampleCount, PromptTokens: sum.PromptTokens, CachedInputTokens: sum.CachedInputTokens,
		CompletionTokens: sum.CompletionTokens, ErrorCount: rejected, ActiveUsers: int64(len(users)), ActiveKeys: int64(len(keys))}
	populateDashboardUsageTotalsDerived(&result.Totals)

	result.ByModel = rankUsageSliceGroups(groupUsageSlices(slices, true, func(s *DashboardUsageHourlySlice) (string, int, string) {
		name := usageSliceName(s.ModelName)
		return name, 0, name
	}), 20)
	result.ByKey = rankUsageSliceGroups(groupUsageSlices(slices, true, func(s *DashboardUsageHourlySlice) (string, int, string) {
		name := usageSliceName(s.TokenName)
		return usageSliceIDKey(s.TokenID, name), s.TokenID, name
	}), 50)
	if access.Viewer.IsManager() {
		result.ByUser = rankUsageSliceGroups(groupUsageSlices(slices, true, func(s *DashboardUsageHourlySlice) (string, int, string) {
			name := usageSliceName(s.Username)
			return usageSliceIDKey(s.UserID, name), s.UserID, name
		}), 100)
	} else {
		result.ByUser = []DashboardUsageDimensionRow{}
	}
	bucketStarts := make([]int64, 0, len(days))
	for day := range days {
		bucketStarts = append(bucketStarts, day)
	}
	sort.Slice(bucketStarts, func(i, j int) bool { return bucketStarts[i] < bucketStarts[j] })
	result.TimeSeries = make([]DashboardUsageTimeRow, 0, len(bucketStarts))
	for _, day := range bucketStarts {
		row := days[day].result()
		result.TimeSeries = append(result.TimeSeries, DashboardUsageTimeRow{Bucket: day, RequestCount: row.RequestCount, AmountMicrousd: row.AmountMicrousd,
			UpstreamMicrousd: row.UpstreamMicrousd, GrossMicrousd: row.GrossMicrousd, CostSampleCount: row.CostSampleCount, PromptTokens: row.PromptTokens,
			CachedInputTokens: row.CachedInputTokens, CompletionTokens: row.CompletionTokens, ErrorCount: row.ErrorCount})
	}
	return nil
}

// --- the usage log's filtered user tab ---------------------------------------

func usageSliceUserFilterRead(filters DashboardUsageLogFilters) usageSliceRead {
	username := strings.TrimSpace(filters.Username)
	group := strings.TrimSpace(filters.Group)
	return usageSliceRead{
		logTypes:    []int{LogTypeConsume},
		withOutcome: true,
		raw: func(tx *gorm.DB) *gorm.DB {
			if filters.UserID > 0 {
				tx = tx.Where("logs.user_id = ?", filters.UserID)
			} else if username != "" {
				tx = tx.Where("logs.username = ?", username)
			}
			if group != "" {
				tx = tx.Where(`logs."group" = ?`, group)
			}
			if filters.ChannelID > 0 {
				tx = tx.Where("logs.channel_id = ?", filters.ChannelID)
			}
			return tx
		},
		slices: func(tx *gorm.DB) *gorm.DB {
			if filters.UserID > 0 {
				tx = tx.Where("user_id = ?", filters.UserID)
			} else if username != "" {
				tx = tx.Where("username = ?", truncateString(username, usageSliceTextLimit))
			}
			if group != "" {
				tx = tx.Where("log_group = ?", truncateString(group, usageSliceShortLimit))
			}
			if filters.ChannelID > 0 {
				tx = tx.Where("channel_id = ?", filters.ChannelID)
			}
			return tx
		},
	}
}

func buildAdminDashboardUserRowsFromSlicesContext(ctx context.Context, filters DashboardUsageLogFilters, window DashboardUsageWindow) ([]DashboardUsageDimensionRow, error) {
	slices, err := loadUsageSlicesContext(ctx, usageSliceUserFilterRead(filters), window.StartTimestamp, window.EndTimestamp+1)
	if err != nil {
		return nil, err
	}
	limit := 50
	if filters.UserID > 0 || strings.TrimSpace(filters.Username) != "" {
		limit = 1
	}
	return rankUsageSliceGroups(groupUsageSlices(slices, true, func(s *DashboardUsageHourlySlice) (string, int, string) {
		name := usageSliceName(s.Username)
		// Historical rows keep the username of their time; an immutable-ID
		// scope is one account under its greatest name, as raw MAX() gives.
		if filters.UserID > 0 {
			return strconv.Itoa(s.UserID), s.UserID, name
		}
		return usageSliceIDKey(s.UserID, name), s.UserID, name
	}), limit), nil
}

// --- the overview's client mix ----------------------------------------------

func overviewClientRowsFromSlicesContext(ctx context.Context, start, end int64) ([]overviewClientRow, error) {
	slices, err := loadUsageSlicesContext(ctx, usageSliceRead{
		logTypes: []int{LogTypeConsume},
		columns:  "log_type, client, user_id, request_count, prompt_tokens, completion_tokens, amount_microusd",
	}, start, end)
	if err != nil {
		return nil, err
	}
	index := map[string]int{}
	users := map[string]map[int]struct{}{}
	var rows []overviewClientRow
	for i := range slices {
		slice := &slices[i]
		name := usageSliceName(slice.Client)
		at, ok := index[name]
		if !ok {
			at = len(rows)
			index[name] = at
			rows = append(rows, overviewClientRow{Client: name})
			users[name] = map[int]struct{}{}
		}
		rows[at].Reqs += slice.RequestCount
		rows[at].Tokens += slice.PromptTokens + slice.CompletionTokens
		rows[at].Amount += slice.AmountMicrousd
		users[name][slice.UserID] = struct{}{}
	}
	for i := range rows {
		rows[i].Users = int64(len(users[rows[i].Client]))
	}
	return rows, nil
}

// --- schema 34 ---------------------------------------------------------------

// usageSlicesPrefillMarker records the hour (Unix hours, in Version) before
// which the online prefill built every slice.
const usageSlicesPrefillMarker = "telemetry_usage_slices_prefill_v34"

// usageSlicesOldestHour is the hour of the oldest consume or rejected log.
func usageSlicesOldestHour(db *gorm.DB) (time.Time, bool, error) {
	var oldest struct{ First *int64 }
	if err := db.Raw("SELECT MIN(created_at) AS first FROM logs WHERE type IN (?, ?)", LogTypeConsume, LogTypeError).Scan(&oldest).Error; err != nil {
		return time.Time{}, false, err
	}
	if oldest.First == nil {
		return time.Time{}, false, nil
	}
	return time.Unix(dashboardRollupHour(*oldest.First), 0).UTC(), true, nil
}

// PrefillSchema34 is the online half of the schema-34 upgrade. With the
// previous release still serving, it creates the slice table and builds the
// slices of every hour that has stopped receiving requests. It records no
// schema version; running it again rebuilds the same hours.
func PrefillSchema34(ctx context.Context, db *gorm.DB) error {
	if db == nil || db.Dialector.Name() != "postgres" {
		return errors.New("schema 34 prefill needs the PostgreSQL Telemetry database")
	}
	session := schema33Session(ctx, db)
	if err := session.AutoMigrate(&DashboardUsageHourlySlice{}); err != nil {
		return err
	}
	now := time.Now().UTC()
	through := now.Add(-schema33ClosedHourLag).Truncate(time.Hour)
	from, ok, err := usageSlicesOldestHour(session)
	if err != nil {
		return err
	}
	if ok && from.Before(through) {
		if err := rebuildUsageSlices(ctx, session, from, through); err != nil {
			return err
		}
	}
	return session.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope"}}, DoUpdates: clause.AssignmentColumns([]string{"version", "applied_at"})}).
		Create(&SchemaVersion{Scope: usageSlicesPrefillMarker, Version: int(through.Unix() / 3600), AppliedAt: now}).Error
}

// migrateSchema34Telemetry is the stopped-writer step: it builds the hours
// the prefill left, or every hour without one.
func migrateSchema34Telemetry(db *gorm.DB) error {
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return runOneTimeMigration(db, "telemetry_usage_slices_v34", func() error {
		ctx := context.Background()
		session := schema33Session(ctx, db)
		if err := session.AutoMigrate(&DashboardUsageHourlySlice{}); err != nil {
			return err
		}
		var prefill SchemaVersion
		if err := session.Where("scope = ?", usageSlicesPrefillMarker).Limit(1).Find(&prefill).Error; err != nil {
			return err
		}
		from, ok := time.Unix(int64(prefill.Version)*3600, 0).UTC(), prefill.Scope != ""
		if !ok {
			var err error
			if from, ok, err = usageSlicesOldestHour(session); err != nil || !ok {
				return err
			}
		}
		if err := rebuildUsageSlices(ctx, session, from, time.Now().UTC().Add(time.Hour).Truncate(time.Hour)); err != nil {
			return err
		}
		common.SysLog("schema 34: usage slices built from " + from.Format(time.RFC3339))
		return nil
	})
}
