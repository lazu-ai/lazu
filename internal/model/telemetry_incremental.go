package model

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TelemetryEventReceipt is committed with every derived counter. A redelivery
// can never publish a log without its counters or count an event twice.
type TelemetryEventReceipt struct {
	EventKey  string `gorm:"primaryKey;size:128"`
	RequestTS int64  `gorm:"not null;index"`
	CreatedAt time.Time
}

type ModelRequestCounter struct {
	ModelName     string `gorm:"primaryKey;size:128"`
	TotalRequests int64  `gorm:"not null;default:0"`
	UpdatedAt     time.Time
}

type DashboardUsageDailyDimension struct {
	DayStart         int64  `gorm:"primaryKey;autoIncrement:false"`
	DimensionType    string `gorm:"primaryKey;size:16"`
	DimensionID      int    `gorm:"primaryKey;autoIncrement:false"`
	ModelName        string `gorm:"primaryKey;size:128"`
	RequestCount     int64  `gorm:"not null;default:0"`
	ErrorCount       int64  `gorm:"not null;default:0"`
	AmountMicrousd   int64  `gorm:"not null;default:0"`
	PromptTokens     int64  `gorm:"not null;default:0"`
	CompletionTokens int64  `gorm:"not null;default:0"`
	UpdatedAt        time.Time
}

func incrementalAssignments(table string, fields []string) map[string]interface{} {
	updates := make(map[string]interface{}, len(fields)+1)
	for _, field := range fields {
		updates[field] = gorm.Expr(table + "." + field + " + excluded." + field)
	}
	updates["updated_at"] = time.Now().UTC()
	return updates
}

func recordModelRequestTx(tx *gorm.DB, model string) error {
	if strings.TrimSpace(model) == "" {
		return nil
	}
	row := ModelRequestCounter{ModelName: model, TotalRequests: 1, UpdatedAt: time.Now().UTC()}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "model_name"}}, DoUpdates: clause.Assignments(incrementalAssignments("model_request_counters", []string{"total_requests"}))}).Create(&row).Error
}

func recordDailyUsageTx(tx *gorm.DB, log *Log) error {
	if log == nil || (log.Type != LogTypeConsume && log.Type != LogTypeError) {
		return nil
	}
	if err := validateTelemetryLogNumbers(log); err != nil {
		return err
	}
	at := log.CreatedAt
	if at <= 0 {
		at = time.Now().Unix()
	}
	day := at - at%86400
	dimensions := []struct {
		kind string
		id   int
	}{{"user", log.UserId}, {"key", log.TokenId}, {"project", log.ProjectID}, {"model", 0}}
	for _, dimension := range dimensions {
		if dimension.id <= 0 && dimension.kind != "model" {
			continue
		}
		row := DashboardUsageDailyDimension{DayStart: day, DimensionType: dimension.kind, DimensionID: dimension.id, ModelName: log.ModelName, UpdatedAt: time.Now().UTC()}
		if log.Type == LogTypeError {
			row.ErrorCount = 1
		} else {
			row.RequestCount = 1
			row.AmountMicrousd = log.AmountMicrousd.Int64()
			row.PromptTokens = int64(log.PromptTokens)
			row.CompletionTokens = int64(log.CompletionTokens)
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "day_start"}, {Name: "dimension_type"}, {Name: "dimension_id"}, {Name: "model_name"}}, DoUpdates: clause.Assignments(incrementalAssignments("dashboard_usage_daily_dimensions", []string{"request_count", "error_count", "amount_microusd", "prompt_tokens", "completion_tokens"}))}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func telemetryOutcomeRow(outcome *RoutingOutcome, log *Log) *dashboardRollupOutcomeRow {
	if outcome == nil {
		return nil
	}
	row := &dashboardRollupOutcomeRow{RequestID: outcome.RequestID, UserID: outcome.UserID, ChannelID: outcome.ChannelID, ChannelName: outcome.ChannelName, StatusCode: outcome.StatusCode, ActualInputTokens: outcome.ActualInputTokens, CachedInputTokens: outcome.CachedInputTokens, ActualOutputTokens: outcome.ActualOutputTokens, UpstreamCostUSD: outcome.UpstreamCostUSD, ChargedCostUSD: outcome.ChargedCostUSD, GrossMarginUSD: outcome.GrossMarginUSD, TotalDurationMs: outcome.TotalDurationMs, WasStreaming: outcome.WasStreaming, TTFTMs: outcome.TTFTMs, AudioOutputTokens: outcome.AudioOutputTokens, ImageOutputTokens: outcome.ImageOutputTokens, OutcomeAt: outcome.OutcomeAt}
	if row.UserID == nil && log != nil {
		id := log.UserId
		row.UserID = &id
	}
	return row
}

func buildHourlyState(state *dashboardRollupBuildState, log *Log, outcome *RoutingOutcome) error {
	if log != nil {
		if err := validateTelemetryLogNumbers(log); err != nil {
			return err
		}
	}

	orow := telemetryOutcomeRow(outcome, log)
	if orow != nil {
		if err := dashboardAddOutcomeMetricsToTotal(state.total(0), *orow); err != nil {
			return err
		}
		if orow.UserID != nil && *orow.UserID > 0 {
			if err := dashboardAddOutcomeMetricsToTotal(state.total(*orow.UserID), *orow); err != nil {
				return err
			}
		}
		cid := strconv.Itoa(dashboardPointerInt(orow.ChannelID))
		if err := dashboardAddChannelOutcomeDimension(state.dimension(0, dashboardDimensionChannel, cid, orow.ChannelName), *orow); err != nil {
			return err
		}
		if orow.UserID != nil && *orow.UserID > 0 {
			if err := dashboardAddChannelOutcomeDimension(state.dimension(*orow.UserID, dashboardDimensionChannel, cid, orow.ChannelName), *orow); err != nil {
				return err
			}
		}
	}
	if log != nil && log.Type == LogTypeConsume {
		r := dashboardRollupLogRow{RequestID: log.RequestID, UserID: log.UserId, Username: log.Username, TokenID: log.TokenId, TokenName: log.TokenName, ModelName: log.ModelName, AmountMicrousd: log.AmountMicrousd.Int64(), PromptTokens: log.PromptTokens, CompletionTokens: log.CompletionTokens, CreatedAt: log.CreatedAt, ChannelID: log.ChannelId}
		if err := dashboardAddLogToTotal(state.total(0), r, orow); err != nil {
			return err
		}
		if log.UserId > 0 {
			if err := dashboardAddLogToTotal(state.total(log.UserId), r, orow); err != nil {
				return err
			}
		}
		for _, scope := range []int{0, log.UserId} {
			if scope == log.UserId && scope == 0 {
				continue
			}
			if err := dashboardAddLogDimension(state.dimension(scope, dashboardDimensionModel, dashboardRollupName(log.ModelName), dashboardRollupName(log.ModelName)), r, orow); err != nil {
				return err
			}
			if err := dashboardAddLogDimension(state.dimension(scope, dashboardDimensionKey, strconv.Itoa(log.TokenId), dashboardRollupName(log.TokenName)), r, orow); err != nil {
				return err
			}
		}
		if err := dashboardAddLogDimension(state.dimension(0, dashboardDimensionUser, strconv.Itoa(log.UserId), dashboardRollupName(log.Username)), r, orow); err != nil {
			return err
		}
	}
	return nil
}

func applyHourlyStateTx(tx *gorm.DB, hour int64, state *dashboardRollupBuildState) error {
	scopes := make([]int, 0, len(state.totals))
	for scope := range state.totals {
		scopes = append(scopes, scope)
	}
	sort.Ints(scopes)
	fields := []string{"request_count", "outcome_count", "amount_microusd", "upstream_cost_microusd", "gross_margin_microusd", "cost_sample_count", "prompt_tokens", "cached_input_tokens", "completion_tokens", "error_count", "latency_ms_sum"}
	totalFields := append(append([]string{}, fields...), "ttft_samples")
	totals := make([]DashboardUsageHourlyTotal, 0, len(scopes))
	for _, scope := range scopes {
		a := state.totals[scope]
		row := DashboardUsageHourlyTotal{BucketHour: hour, ScopeUserID: scope, RequestCount: a.RequestCount, OutcomeCount: a.OutcomeCount, AmountMicrousd: a.AmountMicrousd, UpstreamMicrousd: a.UpstreamMicrousd, GrossMicrousd: a.GrossMicrousd, CostSampleCount: a.CostSampleCount, PromptTokens: a.PromptTokens, CachedInputTokens: a.CachedInputTokens, CompletionTokens: a.CompletionTokens, ErrorCount: a.ErrorCount, LatencyMsSum: a.LatencyMsSum, LatencyBuckets: encodeDashboardLatencyBuckets(a.LatencyBuckets), TTFTSamples: a.TTFTSamples, TTFTBuckets: encodeDashboardTTFTBuckets(a.TTFTBuckets), UpdatedAt: time.Now().UTC()}
		if tx.Dialector.Name() == "postgres" {
			totals = append(totals, row)
		} else if err := mergeHourlyTotalTx(tx, &row, totalFields); err != nil {
			return err
		}
	}
	if len(totals) > 0 {
		if err := batchUpsert(tx, &totals, "dashboard_usage_hourly_totals", []string{"bucket_hour", "scope_user_id"}, totalFields, map[string]interface{}{"latency_buckets": mergeLatencyBucketsSQL("dashboard_usage_hourly_totals"), "ttft_buckets": mergeHistogramSQL("dashboard_usage_hourly_totals", "ttft_buckets"), "updated_at": time.Now().UTC()}); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(state.dimensions))
	for key := range state.dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	dimensions := make([]DashboardUsageHourlyDimension, 0, len(keys))
	for _, key := range keys {
		p := strings.SplitN(key, "|", 3)
		scope, err := strconv.Atoi(p[0])
		if err != nil {
			return err
		}
		a := state.dimensions[key]
		row := DashboardUsageHourlyDimension{BucketHour: hour, ScopeUserID: scope, DimensionType: p[1], DimensionID: a.ID, DimensionName: a.Name, RequestCount: a.RequestCount, OutcomeCount: a.OutcomeCount, AmountMicrousd: a.AmountMicrousd, UpstreamMicrousd: a.UpstreamMicrousd, GrossMicrousd: a.GrossMicrousd, CostSampleCount: a.CostSampleCount, PromptTokens: a.PromptTokens, CachedInputTokens: a.CachedInputTokens, CompletionTokens: a.CompletionTokens, ErrorCount: a.ErrorCount, LatencyMsSum: a.LatencyMsSum, LatencyBuckets: encodeDashboardLatencyBuckets(a.LatencyBuckets), LastUsedAt: a.LastUsedAt, UpdatedAt: time.Now().UTC()}
		if tx.Dialector.Name() == "postgres" {
			dimensions = append(dimensions, row)
		} else if err := mergeHourlyDimensionTx(tx, &row, fields); err != nil {
			return err
		}
	}
	if len(dimensions) > 0 {
		return batchUpsert(tx, &dimensions, "dashboard_usage_hourly_dimensions", []string{"bucket_hour", "scope_user_id", "dimension_type", "dimension_id"}, fields, map[string]interface{}{"latency_buckets": mergeLatencyBucketsSQL("dashboard_usage_hourly_dimensions"), "dimension_name": gorm.Expr("excluded.dimension_name"), "last_used_at": gorm.Expr("GREATEST(dashboard_usage_hourly_dimensions.last_used_at, excluded.last_used_at)"), "updated_at": time.Now().UTC()})
	}
	return nil
}

// A correlated expression in ON CONFLICT merges histogram bins atomically
// while PostgreSQL holds the row lock, eliminating per-dimension reads.
func mergeLatencyBucketsSQL(table string) clause.Expr {
	return mergeHistogramSQL(table, "latency_buckets")
}

func mergeHistogramSQL(table, column string) clause.Expr {
	a := "COALESCE(NULLIF(" + table + "." + column + ",''),'[]')::jsonb"
	b := "COALESCE(NULLIF(excluded." + column + ",''),'[]')::jsonb"
	return gorm.Expr("(SELECT COALESCE(jsonb_agg(COALESCE((" + a + "->>bin)::bigint,0) + COALESCE((" + b + "->>bin)::bigint,0) ORDER BY bin),'[]'::jsonb)::text FROM generate_series(0,GREATEST(jsonb_array_length(" + a + "),jsonb_array_length(" + b + "))-1) AS bin)")
}

// Ensure the row exists before taking the lock, including the first writer.
// Sorted lock ordering prevents batches for several scopes from deadlocking.
func mergeHourlyTotalTx(tx *gorm.DB, row *DashboardUsageHourlyTotal, fields []string) error {
	key := DashboardUsageHourlyTotal{BucketHour: row.BucketHour, ScopeUserID: row.ScopeUserID}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&key).Error; err != nil {
		return err
	}
	var old DashboardUsageHourlyTotal
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("bucket_hour = ? AND scope_user_id = ?", row.BucketHour, row.ScopeUserID).First(&old).Error; err != nil {
		return err
	}
	updates := incrementalAssignments("dashboard_usage_hourly_totals", fields)
	// UPDATE does not have excluded; use the delta's concrete values.
	values := hourlyValues(row.RequestCount, row.OutcomeCount, row.AmountMicrousd, row.UpstreamMicrousd, row.GrossMicrousd, row.CostSampleCount, row.PromptTokens, row.CachedInputTokens, row.CompletionTokens, row.ErrorCount, row.LatencyMsSum)
	values["ttft_samples"] = row.TTFTSamples
	for field, value := range values {
		updates[field] = gorm.Expr(field+" + ?", value)
	}
	histogram, err := mergeLatencyBuckets(old.LatencyBuckets, row.LatencyBuckets)
	if err != nil {
		return err
	}
	updates["latency_buckets"] = histogram
	oldTTFT, newTTFT := "", ""
	if old.TTFTBuckets != nil {
		oldTTFT = *old.TTFTBuckets
	}
	if row.TTFTBuckets != nil {
		newTTFT = *row.TTFTBuckets
	}
	ttft, err := mergeLatencyBuckets(oldTTFT, newTTFT)
	if err != nil {
		return err
	}
	updates["ttft_buckets"] = ttft
	return tx.Model(&old).Updates(updates).Error
}
func mergeHourlyDimensionTx(tx *gorm.DB, row *DashboardUsageHourlyDimension, fields []string) error {
	key := DashboardUsageHourlyDimension{BucketHour: row.BucketHour, ScopeUserID: row.ScopeUserID, DimensionType: row.DimensionType, DimensionID: row.DimensionID}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&key).Error; err != nil {
		return err
	}
	var old DashboardUsageHourlyDimension
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("bucket_hour = ? AND scope_user_id = ? AND dimension_type = ? AND dimension_id = ?", row.BucketHour, row.ScopeUserID, row.DimensionType, row.DimensionID).First(&old).Error; err != nil {
		return err
	}
	updates := make(map[string]interface{})
	for field, value := range hourlyValues(row.RequestCount, row.OutcomeCount, row.AmountMicrousd, row.UpstreamMicrousd, row.GrossMicrousd, row.CostSampleCount, row.PromptTokens, row.CachedInputTokens, row.CompletionTokens, row.ErrorCount, row.LatencyMsSum) {
		updates[field] = gorm.Expr(field+" + ?", value)
	}
	histogram, err := mergeLatencyBuckets(old.LatencyBuckets, row.LatencyBuckets)
	if err != nil {
		return err
	}
	updates["latency_buckets"] = histogram
	updates["dimension_name"] = row.DimensionName
	updates["last_used_at"] = greaterExpr("dashboard_usage_hourly_dimensions", "last_used_at", row.LastUsedAt)
	updates["updated_at"] = time.Now().UTC()
	return tx.Model(&old).Updates(updates).Error
}
func hourlyValues(requests, outcomes, amount, upstream, gross, samples, prompt, cache, completion, errors, latency int64) map[string]int64 {
	return map[string]int64{"request_count": requests, "outcome_count": outcomes, "amount_microusd": amount, "upstream_cost_microusd": upstream, "gross_margin_microusd": gross, "cost_sample_count": samples, "prompt_tokens": prompt, "cached_input_tokens": cache, "completion_tokens": completion, "error_count": errors, "latency_ms_sum": latency}
}
func mergeLatencyBuckets(a, b string) (string, error) {
	var aa, bb []int64
	if strings.TrimSpace(a) != "" {
		if err := json.Unmarshal([]byte(a), &aa); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(b) != "" {
		if err := json.Unmarshal([]byte(b), &bb); err != nil {
			return "", err
		}
	}
	if len(aa) < len(bb) {
		aa = append(aa, make([]int64, len(bb)-len(aa))...)
	}
	for _, value := range aa {
		if err := telemetryNonnegative(value); err != nil {
			return "", err
		}
	}
	for i, v := range bb {
		if err := telemetryNonnegative(v); err != nil {
			return "", err
		}
		if err := addTelemetryCounters(telemetryDelta{&aa[i], v}); err != nil {
			return "", err
		}
	}
	raw, err := json.Marshal(aa)
	return string(raw), err
}

func telemetryEventKey(event relayResultEvent) string {
	if event.RoutingOutcome != nil && event.RoutingOutcome.RequestID != "" {
		return "relay:" + event.RoutingOutcome.RequestID
	}
	if event.ConsumeLog != nil {
		if event.ConsumeLog.RequestID != "" {
			if event.ConsumeLog.Type != LogTypeConsume && event.ConsumeLog.Type != LogTypeError && event.ConsumeLog.Type != LogTypeUnknown {
				return "log:" + event.ConsumeLog.PublicID
			}
			return "relay:" + event.ConsumeLog.RequestID
		}
		return "log:" + event.ConsumeLog.PublicID
	}
	return ""
}
func claimTelemetryEventsTx(tx *gorm.DB, events []relayResultEvent) ([]relayResultEvent, error) {
	candidates := make(map[string]relayResultEvent)
	keys := []string{}
	for _, event := range events {
		if event.ConsumeLog != nil && event.ConsumeLog.PublicID == "" {
			copy := *event.ConsumeLog
			row := copy.toLog()
			ensureConsumeLogPublicID(&row)
			if row.PublicID == "" {
				row.PublicID = common.GeneratePublicID(common.PublicLogIDPrefix)
			}
			copy.PublicID = row.PublicID
			event.ConsumeLog = &copy
		}
		key := telemetryEventKey(event)
		if key == "" {
			continue
		}
		if _, ok := candidates[key]; !ok {
			keys = append(keys, key)
			candidates[key] = event
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	sort.Strings(keys)
	args := make([]interface{}, 0, len(keys)*3)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		event := candidates[key]
		at := time.Now().UnixMilli()
		if event.ConsumeLog != nil {
			at = requestTimestamp(event.ConsumeLog.RequestID, event.ConsumeLog.CreatedAt)
		} else if event.RoutingOutcome != nil {
			at = requestTimestamp(event.RoutingOutcome.RequestID, event.RoutingOutcome.OutcomeAt.Unix())
		}
		values = append(values, "(?, ?, ?)")
		args = append(args, key, at, time.Now().UTC())
	}
	var inserted []struct{ EventKey string }
	query := fmt.Sprintf("INSERT INTO telemetry_event_receipts (event_key, request_ts, created_at) VALUES %s ON CONFLICT (event_key) DO NOTHING RETURNING event_key", strings.Join(values, ","))
	if err := tx.Raw(query, args...).Scan(&inserted).Error; err != nil {
		return nil, err
	}
	result := make([]relayResultEvent, 0, len(inserted))
	for _, row := range inserted {
		result = append(result, candidates[row.EventKey])
	}
	return result, nil
}

func recordDigestUsageTx(tx *gorm.DB, log *Log) error {
	if log == nil || (log.Type != LogTypeConsume && log.Type != LogTypeError) {
		return nil
	}
	if err := validateTelemetryLogNumbers(log); err != nil {
		return err
	}
	payer := log.PayerUserID
	if payer <= 0 {
		payer = log.UserId
	}
	if payer <= 0 {
		return nil
	}
	at := log.CreatedAt
	row := DigestUsageHour{PayerUserID: payer, HourStart: at - at%3600, ModelName: truncateString(log.ModelName, 128), Source: DigestSourceKey(log.ClientFramework)}
	if log.Type == LogTypeError {
		row.Errors = 1
	} else {
		row.Requests = 1
		row.Spend = log.AmountMicrousd.Int64()
		var err error
		row.Tokens, err = checkedTelemetryTokenTotal(log.PromptTokens, log.CompletionTokens)
		if err != nil {
			return err
		}
		if price, ok := sessionOfficialPrice(log.LineItems, log.AmountMicrousd.Int64()); ok {
			row.Official = price
			row.Compared = log.AmountMicrousd.Int64()
		}
	}
	updates := make(map[string]interface{})
	for _, field := range []string{"spend", "requests", "errors", "tokens", "cache", "official", "compared"} {
		updates[field] = gorm.Expr("digest_usage_hours." + field + " + excluded." + field)
	}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "payer_user_id"}, {Name: "hour_start"}, {Name: "model_name"}, {Name: "source"}}, DoUpdates: clause.Assignments(updates)}).Create(&row).Error
}

func recordHourlyBatchTx(tx *gorm.DB, logs []Log, events []relayResultEvent) error {
	outcomes := map[string]*RoutingOutcome{}
	for _, event := range events {
		if event.RoutingOutcome != nil {
			outcomes[event.RoutingOutcome.RequestID] = event.RoutingOutcome
		}
	}
	states := map[int64]*dashboardRollupBuildState{}
	hours := []int64{}
	for i := range logs {
		row := &logs[i]
		outcome := outcomes[row.RequestID]
		at := row.CreatedAt
		if outcome != nil {
			at = outcome.OutcomeAt.Unix()
		}
		hour := at - at%3600
		state := states[hour]
		if state == nil {
			fresh := dashboardRollupState()
			state = fresh
			states[hour] = state
			hours = append(hours, hour)
		}
		if err := buildHourlyState(state, row, outcome); err != nil {
			return err
		}
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	for _, hour := range hours {
		if err := applyHourlyStateTx(tx, hour, states[hour]); err != nil {
			return err
		}
	}
	return nil
}
