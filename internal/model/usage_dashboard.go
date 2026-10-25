package model

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

const (
	secondsInDay                       = 24 * 60 * 60
	dashboardUsageDefaultWindowSeconds = 7 * secondsInDay
	dashboardUsageMaxWindowSeconds     = 90 * secondsInDay
	dashboardUsageCacheTTL             = 60 * time.Second
)

type DashboardUsageWindow struct {
	StartTimestamp int64 `json:"start_timestamp"`
	EndTimestamp   int64 `json:"end_timestamp"`
}

type DashboardUsageTotals struct {
	P95TTFTMs         *float64 `json:"p95_ttft_ms,omitempty"`
	TTFTSampleCount   int64    `json:"ttft_sample_count,omitempty"`
	RequestCount      int64    `json:"request_count"`
	OutcomeCount      int64    `json:"outcome_count,omitempty"`
	AmountMicrousd    int64    `json:"amount_microusd"`
	UpstreamMicrousd  int64    `json:"upstream_cost_microusd,omitempty" gorm:"column:upstream_cost_microusd"`
	GrossMicrousd     int64    `json:"gross_margin_microusd,omitempty" gorm:"column:gross_margin_microusd"`
	GrossMarginRatio  float64  `json:"gross_margin_ratio,omitempty"`
	CostSampleCount   int64    `json:"cost_sample_count,omitempty"`
	PromptTokens      int64    `json:"prompt_tokens"`
	CachedInputTokens int64    `json:"cached_input_tokens"`
	CompletionTokens  int64    `json:"completion_tokens"`
	CacheHitRatio     float64  `json:"cache_hit_ratio"`
	ErrorCount        int64    `json:"error_count,omitempty"`
	AvgLatencyMs      float64  `json:"avg_latency_ms,omitempty"`
	P95LatencyMs      float64  `json:"p95_latency_ms,omitempty"`
	ActiveUsers       int64    `json:"active_users,omitempty"`
	ActiveKeys        int64    `json:"active_keys,omitempty"`
}

type DashboardUsageDimensionRow struct {
	ID                int     `json:"id"`
	Name              string  `json:"name"`
	RequestCount      int64   `json:"request_count"`
	AmountMicrousd    int64   `json:"amount_microusd"`
	UpstreamMicrousd  int64   `json:"upstream_cost_microusd,omitempty" gorm:"column:upstream_cost_microusd"`
	GrossMicrousd     int64   `json:"gross_margin_microusd,omitempty" gorm:"column:gross_margin_microusd"`
	GrossMarginRatio  float64 `json:"gross_margin_ratio,omitempty"`
	CostSampleCount   int64   `json:"cost_sample_count,omitempty"`
	PromptTokens      int64   `json:"prompt_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	CompletionTokens  int64   `json:"completion_tokens"`
	CacheHitRatio     float64 `json:"cache_hit_ratio"`
	ErrorCount        int64   `json:"error_count,omitempty"`
	AvgLatencyMs      float64 `json:"avg_latency_ms,omitempty"`
	P95LatencyMs      float64 `json:"p95_latency_ms,omitempty"`
	LastUsedAt        int64   `json:"last_used_at"`
}

type DashboardUsageTimeRow struct {
	Bucket            int64 `json:"bucket"`
	RequestCount      int64 `json:"request_count"`
	AmountMicrousd    int64 `json:"amount_microusd"`
	UpstreamMicrousd  int64 `json:"upstream_cost_microusd,omitempty" gorm:"column:upstream_cost_microusd"`
	GrossMicrousd     int64 `json:"gross_margin_microusd,omitempty" gorm:"column:gross_margin_microusd"`
	CostSampleCount   int64 `json:"cost_sample_count,omitempty"`
	PromptTokens      int64 `json:"prompt_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	CompletionTokens  int64 `json:"completion_tokens"`
	ErrorCount        int64 `json:"error_count,omitempty"`
}

type DashboardUsage struct {
	Role       string                       `json:"role"`
	Scope      string                       `json:"scope"`
	Window     DashboardUsageWindow         `json:"window"`
	Totals     DashboardUsageTotals         `json:"totals"`
	ByModel    []DashboardUsageDimensionRow `json:"by_model"`
	ByKey      []DashboardUsageDimensionRow `json:"by_key"`
	ByUser     []DashboardUsageDimensionRow `json:"by_user,omitempty"`
	ByChannel  []DashboardUsageDimensionRow `json:"by_channel,omitempty"`
	TimeSeries []DashboardUsageTimeRow      `json:"time_series"`
}

// DashboardUsageWindowFor is the window a dashboard read of these bounds
// covers, for callers that need the window without the dashboard.
func DashboardUsageWindowFor(startTimestamp int64, endTimestamp int64) DashboardUsageWindow {
	return normalizeDashboardUsageWindow(startTimestamp, endTimestamp)
}

func normalizeDashboardUsageWindow(startTimestamp int64, endTimestamp int64) DashboardUsageWindow {
	now := common.GetTimestamp()
	if endTimestamp <= 0 {
		endTimestamp = now
	}
	if startTimestamp <= 0 {
		startTimestamp = endTimestamp - dashboardUsageDefaultWindowSeconds
	}
	if startTimestamp < 0 {
		startTimestamp = 0
	}
	if endTimestamp < startTimestamp {
		endTimestamp = startTimestamp
	}
	if endTimestamp-startTimestamp > dashboardUsageMaxWindowSeconds {
		startTimestamp = endTimestamp - dashboardUsageMaxWindowSeconds
	}
	return DashboardUsageWindow{StartTimestamp: startTimestamp, EndTimestamp: endTimestamp}
}

func dashboardUsageCanJoinOutcome() bool {
	return TelemetryDB() != nil
}

func dashboardUsageWithOutcome(tx *gorm.DB) *gorm.DB {
	if !dashboardUsageCanJoinOutcome() {
		return tx
	}
	if routingFactsAreViews(TelemetryDB()) {
		return dashboardUsageWithBoundedOutcome(tx)
	}
	return tx.Joins("LEFT JOIN routing_outcome ON routing_outcome.request_id = logs.request_id")
}

func dashboardCachedInputSelect() string {
	if dashboardUsageCanJoinOutcome() {
		return "COALESCE(SUM(routing_outcome.cached_input_tokens), 0)"
	}
	return "0"
}

func dashboardErrorCountSelect() string {
	if dashboardUsageCanJoinOutcome() {
		return "COALESCE(SUM(CASE WHEN routing_outcome.status_code >= 400 THEN 1 ELSE 0 END), 0)"
	}
	return "0"
}

func dashboardPrimaryDialectName() string {
	if TelemetryDB() == nil || TelemetryDB().Dialector == nil {
		return ""
	}
	return TelemetryDB().Dialector.Name()
}

func dashboardUsingPostgreSQL() bool {
	return common.UsingPostgreSQL || dashboardPrimaryDialectName() == common.DatabaseTypePostgreSQL
}

func dashboardUsingSQLite() bool {
	return common.UsingSQLite || dashboardPrimaryDialectName() == common.DatabaseTypeSQLite
}

func dashboardLatencySelect(column string) string {
	if dashboardUsingPostgreSQL() {
		return fmt.Sprintf("COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY %s) FILTER (WHERE %s IS NOT NULL), 0)", column, column)
	}
	return fmt.Sprintf("COALESCE(MAX(%s), 0)", column)
}

func dashboardUnixTimeSelect(column string) string {
	if dashboardUsingPostgreSQL() {
		return fmt.Sprintf("COALESCE(EXTRACT(EPOCH FROM MAX(%s))::bigint, 0)", column)
	}
	if dashboardUsingSQLite() {
		return fmt.Sprintf("COALESCE(CAST(strftime('%%s', MAX(%s)) AS INTEGER), 0)", column)
	}
	return fmt.Sprintf("COALESCE(UNIX_TIMESTAMP(MAX(%s)), 0)", column)
}

func dashboardIntegerCastType() string {
	if dashboardUsingPostgreSQL() {
		return "BIGINT"
	}
	return "INTEGER"
}

func dashboardMicrousdSelect(column string) string {
	return fmt.Sprintf("CAST(COALESCE(SUM(%s), 0) * 1000000 AS %s)", column, dashboardIntegerCastType())
}

func dashboardCostSampleSelect() string {
	if dashboardUsageCanJoinOutcome() {
		return "COALESCE(SUM(CASE WHEN routing_outcome.upstream_cost_usd IS NOT NULL THEN 1 ELSE 0 END), 0)"
	}
	return "0"
}

func dashboardCostMicrousdSelect(column string) string {
	if dashboardUsageCanJoinOutcome() {
		return dashboardMicrousdSelect(column)
	}
	return "0"
}

func dashboardWindowStartTime(window DashboardUsageWindow) time.Time {
	return time.Unix(window.StartTimestamp, 0).UTC()
}

func dashboardWindowEndTime(window DashboardUsageWindow) time.Time {
	return time.Unix(window.EndTimestamp, 0).UTC()
}

func dashboardCacheHitRatio(cachedInputTokens int64, promptTokens int64) float64 {
	if cachedInputTokens <= 0 || promptTokens <= 0 {
		return 0
	}
	ratio := float64(cachedInputTokens) / float64(promptTokens)
	if ratio > 1 {
		return 1
	}
	return ratio
}

func populateDashboardUsageTotalsDerived(totals *DashboardUsageTotals) {
	if totals == nil {
		return
	}
	totals.CacheHitRatio = dashboardCacheHitRatio(totals.CachedInputTokens, totals.PromptTokens)
	if totals.CostSampleCount > 0 && totals.AmountMicrousd != 0 {
		totals.GrossMarginRatio = float64(totals.GrossMicrousd) / float64(totals.AmountMicrousd)
	}
}

func populateDashboardUsageRowsDerived(rows []DashboardUsageDimensionRow) {
	for i := range rows {
		rows[i].CacheHitRatio = dashboardCacheHitRatio(rows[i].CachedInputTokens, rows[i].PromptTokens)
		if rows[i].CostSampleCount > 0 && rows[i].AmountMicrousd != 0 {
			rows[i].GrossMarginRatio = float64(rows[i].GrossMicrousd) / float64(rows[i].AmountMicrousd)
		}
	}
}

func userUsageBaseQuery(userID int, window DashboardUsageWindow) *gorm.DB {
	return dashboardUsageBaseQuery(window).
		Where("logs.user_id = ?", userID)
}

func dashboardUsageBaseQuery(window DashboardUsageWindow) *gorm.DB {
	tx := TelemetryDB().Model(&Log{}).
		Where("logs.type = ?", LogTypeConsume)
	if window.StartTimestamp > 0 {
		tx = tx.Where("logs.created_at >= ?", window.StartTimestamp)
	}
	if window.EndTimestamp > 0 {
		tx = tx.Where("logs.created_at <= ?", window.EndTimestamp)
	}
	return tx
}

func scanDashboardUsageTotals(tx *gorm.DB, totals *DashboardUsageTotals, includeActiveCounts bool) error {
	tx = dashboardUsageWithOutcome(tx)
	var selectArgs []any
	selectExpr := "COUNT(*) AS request_count, " +
		"COALESCE(SUM(logs.amount_microusd), 0) AS amount_microusd, " +
		dashboardCostMicrousdSelect("routing_outcome.upstream_cost_usd") + " AS upstream_cost_microusd, " +
		dashboardCostMicrousdSelect("routing_outcome.gross_margin_usd") + " AS gross_margin_microusd, " +
		dashboardCostSampleSelect() + " AS cost_sample_count, " +
		"COALESCE(SUM(logs.prompt_tokens), 0) AS prompt_tokens, " +
		dashboardCachedInputSelect() + " AS cached_input_tokens, " +
		"COALESCE(SUM(logs.completion_tokens), 0) AS completion_tokens"
	if includeActiveCounts {
		selectExpr += ", COUNT(DISTINCT CASE WHEN logs.user_id > 0 THEN logs.user_id END) AS active_users, "
		internalTokenIDs, err := hiddenTokenIDs()
		if err != nil {
			return err
		}
		keyExpr := "COUNT(DISTINCT CASE WHEN logs.token_id > 0 THEN logs.token_id END) AS active_keys"
		if len(internalTokenIDs) > 0 {
			keyExpr = "COUNT(DISTINCT CASE WHEN logs.token_id > 0 AND logs.token_id NOT IN ? THEN logs.token_id END) AS active_keys"
			selectArgs = append(selectArgs, internalTokenIDs)
		}
		selectExpr += keyExpr
	}
	if err := tx.Select(selectExpr, selectArgs...).Scan(totals).Error; err != nil {
		return err
	}
	populateDashboardUsageTotalsDerived(totals)
	return nil
}

func scanDashboardUsageRows(tx *gorm.DB, idExpr string, nameExpr string, groupBy string, limit int) ([]DashboardUsageDimensionRow, error) {
	if limit <= 0 {
		limit = 20
	}
	tx = dashboardUsageWithOutcome(tx)
	var rows []DashboardUsageDimensionRow
	avgLatencySelect := "0"
	p95LatencySelect := "0"
	if dashboardUsageCanJoinOutcome() {
		avgLatencySelect = "COALESCE(AVG(routing_outcome.total_duration_ms), 0)"
		p95LatencySelect = dashboardLatencySelect("routing_outcome.total_duration_ms")
	}
	err := tx.Select(
		idExpr + " AS id, " +
			nameExpr + " AS name, " +
			"COUNT(*) AS request_count, " +
			"COALESCE(SUM(logs.amount_microusd), 0) AS amount_microusd, " +
			dashboardCostMicrousdSelect("routing_outcome.upstream_cost_usd") + " AS upstream_cost_microusd, " +
			dashboardCostMicrousdSelect("routing_outcome.gross_margin_usd") + " AS gross_margin_microusd, " +
			dashboardCostSampleSelect() + " AS cost_sample_count, " +
			"COALESCE(SUM(logs.prompt_tokens), 0) AS prompt_tokens, " +
			dashboardCachedInputSelect() + " AS cached_input_tokens, " +
			"COALESCE(SUM(logs.completion_tokens), 0) AS completion_tokens, " +
			dashboardErrorCountSelect() + " AS error_count, " +
			avgLatencySelect + " AS avg_latency_ms, " +
			p95LatencySelect + " AS p95_latency_ms, " +
			"MAX(logs.created_at) AS last_used_at",
	).Group(groupBy).
		Order("amount_microusd DESC, request_count DESC, name ASC").
		Limit(limit).
		Find(&rows).Error
	populateDashboardUsageRowsDerived(rows)
	return rows, err
}

func scanDashboardUsageTimeRows(tx *gorm.DB) ([]DashboardUsageTimeRow, error) {
	tx = dashboardUsageWithOutcome(tx)
	bucketExpr := "(logs.created_at / 86400) * 86400"
	var timeRows []DashboardUsageTimeRow
	err := tx.Select(
		bucketExpr + " AS bucket, " +
			"COUNT(*) AS request_count, " +
			"COALESCE(SUM(logs.amount_microusd), 0) AS amount_microusd, " +
			dashboardCostMicrousdSelect("routing_outcome.upstream_cost_usd") + " AS upstream_cost_microusd, " +
			dashboardCostMicrousdSelect("routing_outcome.gross_margin_usd") + " AS gross_margin_microusd, " +
			dashboardCostSampleSelect() + " AS cost_sample_count, " +
			"COALESCE(SUM(logs.prompt_tokens), 0) AS prompt_tokens, " +
			dashboardCachedInputSelect() + " AS cached_input_tokens, " +
			"COALESCE(SUM(logs.completion_tokens), 0) AS completion_tokens, " +
			dashboardErrorCountSelect() + " AS error_count",
	).Group(bucketExpr).Order(bucketExpr + " asc").Find(&timeRows).Error
	return timeRows, err
}

func scanDashboardOutcomeStatsContext(ctx context.Context, userID int, role int, window DashboardUsageWindow, totals *DashboardUsageTotals) error {
	if TelemetryDB() == nil || totals == nil {
		return nil
	}
	type statsRow struct {
		OutcomeCount int64
		ErrorCount   int64
		AvgLatencyMs float64
		P95LatencyMs float64
	}
	var row statsRow
	tx := dashboardOutcomeWindowQuery(TelemetryDB().WithContext(ctx), window, dashboardMemberID(userID, role))
	if role < common.RoleRootUser {
		tx = tx.Where(dashboardOutcomeUserPredicate(), userID)
	}
	err := tx.Select(
		"COUNT(*) AS outcome_count, " +
			"COALESCE(SUM(CASE WHEN routing_outcome.status_code >= 400 THEN 1 ELSE 0 END), 0) AS error_count, " +
			"COALESCE(AVG(routing_outcome.total_duration_ms), 0) AS avg_latency_ms, " +
			dashboardLatencySelect("routing_outcome.total_duration_ms") + " AS p95_latency_ms",
	).Scan(&row).Error
	if err != nil {
		return err
	}
	totals.OutcomeCount = row.OutcomeCount
	totals.ErrorCount = row.ErrorCount
	totals.AvgLatencyMs = row.AvgLatencyMs
	totals.P95LatencyMs = row.P95LatencyMs
	return nil
}

func scanDashboardChannelRowsContext(ctx context.Context, userID int, role int, window DashboardUsageWindow, limit int) ([]DashboardUsageDimensionRow, error) {
	if TelemetryDB() == nil {
		return nil, nil
	}
	channelIDExpr := "COALESCE(routing_outcome.channel_id, 0)"
	channelNameExpr := "COALESCE(NULLIF(channels.name, ''), 'unknown')"
	tx := dashboardOutcomeWindowQuery(TelemetryDB().WithContext(ctx), window, dashboardMemberID(userID, role)).
		Joins("LEFT JOIN " + TelemetryChannelTable() + " channels ON channels.id = routing_outcome.channel_id")
	if role < common.RoleRootUser {
		tx = tx.Where(dashboardOutcomeUserPredicate(), userID)
	}
	var rows []DashboardUsageDimensionRow
	query := tx.Select(
		channelIDExpr + " AS id, " +
			channelNameExpr + " AS name, " +
			"COUNT(*) AS request_count, " +
			dashboardMicrousdSelect("routing_outcome.charged_cost_usd") + " AS amount_microusd, " +
			dashboardMicrousdSelect("routing_outcome.upstream_cost_usd") + " AS upstream_cost_microusd, " +
			dashboardMicrousdSelect("routing_outcome.gross_margin_usd") + " AS gross_margin_microusd, " +
			"COALESCE(SUM(CASE WHEN routing_outcome.upstream_cost_usd IS NOT NULL THEN 1 ELSE 0 END), 0) AS cost_sample_count, " +
			"COALESCE(SUM(routing_outcome.actual_input_tokens), 0) AS prompt_tokens, " +
			"COALESCE(SUM(routing_outcome.cached_input_tokens), 0) AS cached_input_tokens, " +
			"COALESCE(SUM(routing_outcome.actual_output_tokens), 0) AS completion_tokens, " +
			"COALESCE(SUM(CASE WHEN routing_outcome.status_code >= 400 THEN 1 ELSE 0 END), 0) AS error_count, " +
			"COALESCE(AVG(routing_outcome.total_duration_ms), 0) AS avg_latency_ms, " +
			dashboardLatencySelect("routing_outcome.total_duration_ms") + " AS p95_latency_ms, " +
			dashboardUnixTimeSelect("routing_outcome.outcome_at") + " AS last_used_at",
	).Group(channelIDExpr + ", " + channelNameExpr).
		Order("request_count DESC, amount_microusd DESC, name ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&rows).Error
	populateDashboardUsageRowsDerived(rows)
	return rows, err
}

func BuildUserDashboardUsage(userID int, startTimestamp int64, endTimestamp int64) (DashboardUsage, error) {
	return BuildUserDashboardUsageContext(context.Background(), userID, startTimestamp, endTimestamp)
}

func BuildUserDashboardUsageContext(ctx context.Context, userID int, startTimestamp int64, endTimestamp int64) (DashboardUsage, error) {
	window := normalizeDashboardUsageWindow(startTimestamp, endTimestamp)
	result := DashboardUsage{
		Role:   "member",
		Scope:  "user",
		Window: window,
	}

	if routingFactsAreViews(TelemetryDB()) {
		logs, err := scanPostgresDashboardLogUsage(userUsageBaseQuery(userID, window).WithContext(ctx), 0)
		if err != nil {
			return result, err
		}
		result.Totals, result.ByModel, result.ByKey, result.ByUser, result.TimeSeries = logs.Totals, logs.ByModel, logs.ByKey, logs.ByUser, logs.TimeSeries
		if err := scanDashboardOutcomeStatsContext(ctx, userID, common.RoleCommonUser, window, &result.Totals); err != nil {
			return result, err
		}
		result.ByChannel, err = scanDashboardChannelRowsContext(ctx, userID, common.RoleCommonUser, window, 20)
		return result, err
	}

	if err := scanDashboardUsageTotals(userUsageBaseQuery(userID, window).WithContext(ctx), &result.Totals, true); err != nil {
		return result, err
	}
	if err := scanDashboardOutcomeStatsContext(ctx, userID, common.RoleCommonUser, window, &result.Totals); err != nil {
		return result, err
	}

	var err error
	modelNameExpr := "COALESCE(NULLIF(logs.model_name, ''), 'unknown')"
	result.ByModel, err = scanDashboardUsageRows(userUsageBaseQuery(userID, window).WithContext(ctx), "0", modelNameExpr, modelNameExpr, 20)
	if err != nil {
		return result, err
	}
	keyNameExpr := "COALESCE(NULLIF(logs.token_name, ''), 'unknown')"
	result.ByKey, err = scanDashboardUsageRows(userUsageBaseQuery(userID, window).WithContext(ctx), "logs.token_id", keyNameExpr, "logs.token_id, "+keyNameExpr, 50)
	if err != nil {
		return result, err
	}
	result.ByChannel, err = scanDashboardChannelRowsContext(ctx, userID, common.RoleCommonUser, window, 20)
	if err != nil {
		return result, err
	}
	result.TimeSeries, err = scanDashboardUsageTimeRows(userUsageBaseQuery(userID, window).WithContext(ctx))
	if err != nil {
		return result, err
	}
	return result, nil
}

func BuildAdminDashboardUsage(startTimestamp int64, endTimestamp int64) (DashboardUsage, error) {
	return BuildAdminDashboardUsageContext(context.Background(), startTimestamp, endTimestamp)
}

func BuildAdminDashboardUsageContext(ctx context.Context, startTimestamp int64, endTimestamp int64) (DashboardUsage, error) {
	window := normalizeDashboardUsageWindow(startTimestamp, endTimestamp)
	result := DashboardUsage{
		Role:   "admin",
		Scope:  "global",
		Window: window,
	}

	if routingFactsAreViews(TelemetryDB()) {
		logs, err := scanPostgresDashboardLogUsage(dashboardUsageBaseQuery(window).WithContext(ctx), 50)
		if err != nil {
			return result, err
		}
		result.Totals, result.ByModel, result.ByKey, result.ByUser, result.TimeSeries = logs.Totals, logs.ByModel, logs.ByKey, logs.ByUser, logs.TimeSeries
		if err := scanDashboardOutcomeStatsContext(ctx, 0, common.RoleRootUser, window, &result.Totals); err != nil {
			return result, err
		}
		result.ByChannel, err = scanDashboardChannelRowsContext(ctx, 0, common.RoleRootUser, window, 0)
		return result, err
	}

	if err := scanDashboardUsageTotals(dashboardUsageBaseQuery(window).WithContext(ctx), &result.Totals, true); err != nil {
		return result, err
	}
	if err := scanDashboardOutcomeStatsContext(ctx, 0, common.RoleRootUser, window, &result.Totals); err != nil {
		return result, err
	}

	var err error
	modelNameExpr := "COALESCE(NULLIF(logs.model_name, ''), 'unknown')"
	result.ByModel, err = scanDashboardUsageRows(dashboardUsageBaseQuery(window).WithContext(ctx), "0", modelNameExpr, modelNameExpr, 20)
	if err != nil {
		return result, err
	}

	keyNameExpr := "COALESCE(NULLIF(logs.token_name, ''), 'unknown')"
	result.ByKey, err = scanDashboardUsageRows(dashboardUsageBaseQuery(window).WithContext(ctx), "logs.token_id", keyNameExpr, "logs.token_id, "+keyNameExpr, 50)
	if err != nil {
		return result, err
	}

	userNameExpr := "COALESCE(NULLIF(logs.username, ''), 'unknown')"
	result.ByUser, err = scanDashboardUsageRows(dashboardUsageBaseQuery(window).WithContext(ctx), "logs.user_id", userNameExpr, "logs.user_id, "+userNameExpr, 50)
	if err != nil {
		return result, err
	}
	result.ByChannel, err = scanDashboardChannelRowsContext(ctx, 0, common.RoleRootUser, window, 0)
	if err != nil {
		return result, err
	}
	result.TimeSeries, err = scanDashboardUsageTimeRows(dashboardUsageBaseQuery(window).WithContext(ctx))
	if err != nil {
		return result, err
	}
	return result, nil
}

// BuildAdminDashboardUserRows applies the request-log owner, group, and channel
// filters to the user ranking. Keeping this query separate avoids returning an
// unfiltered top-50 aggregate beside a filtered request table.
func BuildAdminDashboardUserRows(filters DashboardUsageLogFilters, startTimestamp int64, endTimestamp int64) ([]DashboardUsageDimensionRow, error) {
	return BuildAdminDashboardUserRowsContext(context.Background(), filters, startTimestamp, endTimestamp)
}

// BuildAdminDashboardUserRowsContext returns the user rows of a filtered
// usage log. PostgreSQL reads hourly usage slices: joining a filter's logs to
// their routing outcomes took 1.7 s (one channel) to 6 s (one group) for
// thirty days on 2026-10-16.
func BuildAdminDashboardUserRowsContext(ctx context.Context, filters DashboardUsageLogFilters, startTimestamp int64, endTimestamp int64) ([]DashboardUsageDimensionRow, error) {
	username := strings.TrimSpace(filters.Username)
	group := strings.TrimSpace(filters.Group)
	window := normalizeDashboardUsageWindow(startTimestamp, endTimestamp)
	if usageSlicesAvailable() {
		return buildAdminDashboardUserRowsFromSlicesContext(ctx, filters, window)
	}
	userNameExpr := "COALESCE(NULLIF(logs.username, ''), 'unknown')"
	tx := dashboardUsageBaseQuery(window).WithContext(ctx)
	limit := 50
	if filters.UserID > 0 {
		tx = tx.Where("logs.user_id = ?", filters.UserID)
		limit = 1
	} else if username != "" {
		tx = tx.Where("logs.username = ?", username)
		limit = 1
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	if filters.ChannelID > 0 {
		tx = tx.Where("logs.channel_id = ?", filters.ChannelID)
	}
	groupExpr := "logs.user_id, " + userNameExpr
	if filters.UserID > 0 {
		// Historical rows keep the username that existed when the request was
		// made. An immutable-ID scope must still aggregate them as one account.
		userNameExpr = "MAX(" + userNameExpr + ")"
		groupExpr = "logs.user_id"
	}
	return scanDashboardUsageRows(tx, "logs.user_id", userNameExpr, groupExpr, limit)
}

// BuildAdminDashboardModelRows returns aggregates for an explicit bounded
// model set. Unlike the dashboard's top-20 leaderboard, this is suitable for a
// paginated catalog whose current page may contain lower-volume models.
func BuildAdminDashboardModelRows(modelNames []string, startTimestamp int64, endTimestamp int64) ([]DashboardUsageDimensionRow, error) {
	return BuildAdminDashboardModelRowsContext(context.Background(), modelNames, startTimestamp, endTimestamp)
}

func BuildAdminDashboardModelRowsContext(ctx context.Context, modelNames []string, startTimestamp int64, endTimestamp int64) ([]DashboardUsageDimensionRow, error) {
	seen := make(map[string]struct{}, len(modelNames))
	names := make([]string, 0, len(modelNames))
	for _, name := range modelNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return []DashboardUsageDimensionRow{}, nil
	}
	window := normalizeDashboardUsageWindow(startTimestamp, endTimestamp)
	modelNameExpr := "COALESCE(NULLIF(logs.model_name, ''), 'unknown')"
	return scanDashboardUsageRows(
		dashboardUsageBaseQuery(window).WithContext(ctx).Where("logs.model_name IN ?", names),
		"0",
		modelNameExpr,
		modelNameExpr,
		len(names),
	)
}

// TTFT is measured independently from complete request duration. Only text
// streaming observations with an actual first-token measurement contribute.
func scanDashboardTTFTStats(userID int, role int, window DashboardUsageWindow, totals *DashboardUsageTotals) error {
	return scanDashboardTTFTStatsContext(context.Background(), userID, role, window, totals)
}

func scanDashboardTTFTStatsContext(ctx context.Context, userID int, role int, window DashboardUsageWindow, totals *DashboardUsageTotals) error {
	if TelemetryDB() == nil || totals == nil || (!routingFactsAreViews(TelemetryDB()) && !TelemetryDB().WithContext(ctx).Migrator().HasColumn(&RoutingOutcome{}, "ttft_ms")) {
		return nil
	}
	tx := dashboardOutcomeWindowQuery(TelemetryDB().WithContext(ctx), window, dashboardMemberID(userID, role)).
		Where("routing_outcome.was_streaming = ? AND routing_outcome.ttft_ms IS NOT NULL AND routing_outcome.ttft_ms >= 0", true).
		Where("COALESCE(routing_outcome.audio_output_tokens, 0) = 0 AND COALESCE(routing_outcome.image_output_tokens, 0) = 0")
	if role < common.RoleRootUser {
		tx = tx.Where(dashboardOutcomeUserPredicate(), userID)
	}
	var row struct {
		Count int64
		P95   float64
	}
	if TelemetryDB().Dialector.Name() == "postgres" {
		if err := tx.Select("COUNT(*) AS count, " + dashboardLatencySelect("routing_outcome.ttft_ms") + " AS p95").Scan(&row).Error; err != nil {
			return err
		}
	} else {
		// Match percentile_cont on SQLite/MySQL rather than labelling MAX as P95.
		if err := tx.Session(&gorm.Session{}).Count(&row.Count).Error; err != nil {
			return err
		}
		if row.Count > 0 {
			rank := float64(row.Count-1) * 0.95
			lower := int(rank)
			var samples []float64
			if err := tx.Order("routing_outcome.ttft_ms ASC").Offset(lower).Limit(2).Pluck("routing_outcome.ttft_ms", &samples).Error; err != nil {
				return err
			}
			if len(samples) > 0 {
				row.P95 = samples[0]
				if len(samples) > 1 {
					row.P95 += (samples[1] - samples[0]) * (rank - float64(lower))
				}
			}
		}
	}
	totals.TTFTSampleCount = row.Count
	if row.Count > 0 {
		totals.P95TTFTMs = &row.P95
	}
	return nil
}

func BuildDashboardUsageForRole(userID int, role int, startTimestamp int64, endTimestamp int64) (DashboardUsage, error) {
	return BuildDashboardUsageForRoleContext(context.Background(), userID, role, startTimestamp, endTimestamp)
}

func BuildDashboardUsageForRoleContext(ctx context.Context, userID int, role int, startTimestamp int64, endTimestamp int64) (DashboardUsage, error) {
	window := normalizeDashboardUsageWindow(startTimestamp, endTimestamp)
	if cached, ok := loadDashboardUsageCache(userID, role, window); ok {
		return cached, nil
	}
	if usage, ok, err := BuildDashboardUsageFromRollupForRoleContext(ctx, userID, role, window.StartTimestamp, window.EndTimestamp); err != nil {
		if !dashboardRollupFallbackEnabled() {
			return usage, err
		}
		common.SysLog("dashboard rollup unavailable, falling back to raw usage query: " + err.Error())
	} else if ok {
		// The rollup's own TTFT histogram answers whole-hour windows; only an
		// exact-second window reads raw outcomes below.
		saveDashboardUsageCache(userID, role, window, usage)
		return usage, nil
	}
	var (
		usage DashboardUsage
		err   error
	)
	if routingFactsAreViews(TelemetryDB()) {
		usage, err = buildPostgresDashboardUsageForRole(ctx, userID, role, window)
	} else if role >= common.RoleRootUser {
		usage, err = BuildAdminDashboardUsageContext(ctx, window.StartTimestamp, window.EndTimestamp)
	} else {
		usage, err = BuildUserDashboardUsageContext(ctx, userID, window.StartTimestamp, window.EndTimestamp)
	}
	if err == nil && !routingFactsAreViews(TelemetryDB()) {
		err = scanDashboardTTFTStatsContext(ctx, userID, role, window, &usage.Totals)
	}
	if err == nil {
		saveDashboardUsageCache(userID, role, window, usage)
	}
	return usage, err
}

func dashboardUsageCacheKey(userID int, role int, window DashboardUsageWindow) string {
	return fmt.Sprintf("dashboard_usage:v7:%d:%d:%d:%d", role, userID, window.StartTimestamp, window.EndTimestamp)
}

func dashboardUsageCacheEnabled() bool {
	return common.RedisEnabled && common.RDB != nil
}

func loadDashboardUsageCache(userID int, role int, window DashboardUsageWindow) (DashboardUsage, bool) {
	if !dashboardUsageCacheEnabled() {
		return DashboardUsage{}, false
	}
	payload, err := common.RedisGet(dashboardUsageCacheKey(userID, role, window))
	if err != nil || payload == "" {
		return DashboardUsage{}, false
	}
	var usage DashboardUsage
	if err := json.Unmarshal([]byte(payload), &usage); err != nil {
		return DashboardUsage{}, false
	}
	return usage, true
}

func saveDashboardUsageCache(userID int, role int, window DashboardUsageWindow, usage DashboardUsage) {
	if !dashboardUsageCacheEnabled() {
		return
	}
	payload, err := json.Marshal(usage)
	if err != nil {
		return
	}
	_ = common.RedisSet(dashboardUsageCacheKey(userID, role, window), string(payload), dashboardUsageCacheTTL)
}

func BuildDashboardUsageCSV(usage DashboardUsage) ([]byte, error) {
	buf := bytes.NewBuffer([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(buf)
	if err := writer.Write([]string{"section", "id", "name", "requests", "amount_microusd", "prompt_tokens", "cached_input_tokens", "completion_tokens", "cache_hit_ratio", "error_count", "avg_latency_ms", "p95_latency_ms", "last_used_at"}); err != nil {
		return nil, err
	}
	writeRows := func(section string, rows []DashboardUsageDimensionRow) error {
		for _, row := range rows {
			if err := writer.Write([]string{
				section,
				strconv.Itoa(row.ID),
				row.Name,
				strconv.FormatInt(row.RequestCount, 10),
				strconv.FormatInt(row.AmountMicrousd, 10),
				strconv.FormatInt(row.PromptTokens, 10),
				strconv.FormatInt(row.CachedInputTokens, 10),
				strconv.FormatInt(row.CompletionTokens, 10),
				strconv.FormatFloat(row.CacheHitRatio, 'f', 6, 64),
				strconv.FormatInt(row.ErrorCount, 10),
				strconv.FormatFloat(row.AvgLatencyMs, 'f', 2, 64),
				strconv.FormatFloat(row.P95LatencyMs, 'f', 2, 64),
				strconv.FormatInt(row.LastUsedAt, 10),
			}); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeRows("model", usage.ByModel); err != nil {
		return nil, err
	}
	if err := writeRows("key", usage.ByKey); err != nil {
		return nil, err
	}
	if err := writeRows("user", usage.ByUser); err != nil {
		return nil, err
	}
	if err := writeRows("channel", usage.ByChannel); err != nil {
		return nil, err
	}
	writer.Flush()
	return buf.Bytes(), writer.Error()
}

func BuildDashboardUsageXLSX(usage DashboardUsage) ([]byte, error) {
	file := excelize.NewFile()
	defer func() { _ = file.Close() }()
	sheet := "Usage"
	file.SetSheetName("Sheet1", sheet)
	headers := []string{"section", "id", "name", "requests", "amount_microusd", "prompt_tokens", "cached_input_tokens", "completion_tokens", "cache_hit_ratio", "error_count", "avg_latency_ms", "p95_latency_ms", "last_used_at"}
	for col, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		_ = file.SetCellValue(sheet, cell, header)
	}
	rowIndex := 2
	writeRows := func(section string, rows []DashboardUsageDimensionRow) {
		for _, row := range rows {
			values := []any{section, row.ID, row.Name, row.RequestCount, row.AmountMicrousd, row.PromptTokens, row.CachedInputTokens, row.CompletionTokens, row.CacheHitRatio, row.ErrorCount, row.AvgLatencyMs, row.P95LatencyMs, row.LastUsedAt}
			for col, value := range values {
				cell, _ := excelize.CoordinatesToCellName(col+1, rowIndex)
				_ = file.SetCellValue(sheet, cell, value)
			}
			rowIndex++
		}
	}
	writeRows("model", usage.ByModel)
	writeRows("key", usage.ByKey)
	writeRows("user", usage.ByUser)
	writeRows("channel", usage.ByChannel)
	_ = file.SetColWidth(sheet, "A", "M", 18)
	xlsx, err := file.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return xlsx.Bytes(), nil
}

type DashboardUsageLogFilters struct {
	ChannelID int
	Group     string
	LogType   int
	ModelName string
	TokenName string
	UserID    int
	Username  string
}

func ListDashboardUsageLogs(userID int, role int, window DashboardUsageWindow, filters DashboardUsageLogFilters, limit int) ([]Log, error) {
	return ListDashboardUsageLogsContext(context.Background(), userID, role, window, filters, limit)
}

func ListDashboardUsageLogsContext(ctx context.Context, userID int, role int, window DashboardUsageWindow, filters DashboardUsageLogFilters, limit int) ([]Log, error) {
	if limit <= 0 || limit > 50000 {
		limit = 50000
	}
	tx := TelemetryDB().WithContext(ctx).Model(&Log{}).Where("logs.type <> ?", LogTypeUnknown)
	if role < common.RoleRootUser {
		tx = tx.Where("logs.user_id = ?", userID)
	}
	if filters.LogType != LogTypeUnknown {
		tx = tx.Where("logs.type = ?", filters.LogType)
	}
	if filters.ModelName != "" {
		tx = tx.Where("logs.model_name LIKE ?", filters.ModelName)
	}
	if role >= common.RoleRootUser && filters.UserID > 0 {
		tx = tx.Where("logs.user_id = ?", filters.UserID)
	} else if role >= common.RoleRootUser && filters.Username != "" {
		tx = tx.Where("logs.username = ?", filters.Username)
	}
	if filters.TokenName != "" {
		tx = tx.Where("logs.token_name = ?", filters.TokenName)
	}
	if role >= common.RoleRootUser && filters.ChannelID != 0 {
		tx = tx.Where("logs.channel_id = ?", filters.ChannelID)
	}
	if filters.Group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", filters.Group)
	}
	if window.StartTimestamp > 0 {
		tx = tx.Where("logs.created_at >= ?", window.StartTimestamp)
	}
	if window.EndTimestamp > 0 {
		tx = tx.Where("logs.created_at <= ?", window.EndTimestamp)
	}
	var logs []Log
	if err := orderLogsNewestFirst(tx).Limit(limit).Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

func BuildUsageLogsCSV(logs []Log) ([]byte, error) {
	buf := bytes.NewBuffer([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(buf)
	if err := writer.Write([]string{
		"id", "request_id", "created_at", "username", "token_id", "token_name",
		"model_name", "amount_microusd", "prompt_tokens", "completion_tokens",
		"use_time", "is_stream", "group", "ip",
	}); err != nil {
		return nil, err
	}
	for _, log := range logs {
		if err := writer.Write([]string{
			strconv.Itoa(log.Id),
			log.RequestID,
			strconv.FormatInt(log.CreatedAt, 10),
			log.Username,
			strconv.Itoa(log.TokenId),
			log.TokenName,
			log.ModelName,
			strconv.FormatInt(log.AmountMicrousd.Int64(), 10),
			strconv.Itoa(log.PromptTokens),
			strconv.Itoa(log.CompletionTokens),
			strconv.Itoa(log.UseTime),
			strconv.FormatBool(log.IsStream),
			log.Group,
			log.Ip,
		}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	return buf.Bytes(), writer.Error()
}

func BuildUsageLogsXLSX(logs []Log) ([]byte, error) {
	file := excelize.NewFile()
	defer func() { _ = file.Close() }()
	sheet := "Logs"
	file.SetSheetName("Sheet1", sheet)
	headers := []string{
		"id", "request_id", "created_at", "username", "token_id", "token_name",
		"model_name", "amount_microusd", "prompt_tokens", "completion_tokens",
		"use_time", "is_stream", "group", "ip",
	}
	for col, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		_ = file.SetCellValue(sheet, cell, header)
	}
	for rowIndex, log := range logs {
		values := []any{
			log.Id, log.RequestID, log.CreatedAt, log.Username, log.TokenId,
			log.TokenName, log.ModelName, log.AmountMicrousd, log.PromptTokens,
			log.CompletionTokens, log.UseTime, log.IsStream, log.Group, log.Ip,
		}
		for col, value := range values {
			cell, _ := excelize.CoordinatesToCellName(col+1, rowIndex+2)
			_ = file.SetCellValue(sheet, cell, value)
		}
	}
	_ = file.SetColWidth(sheet, "A", "N", 18)
	xlsx, err := file.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return xlsx.Bytes(), nil
}

func UsageFilename(prefix string, window DashboardUsageWindow, ext string) string {
	start := time.Unix(window.StartTimestamp, 0).UTC().Format("20060102")
	end := time.Unix(window.EndTimestamp, 0).UTC().Format("20060102")
	return fmt.Sprintf("%s-%s-%s.%s", prefix, start, end, ext)
}
