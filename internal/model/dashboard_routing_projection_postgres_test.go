package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

// These baseline queries preserve the pre-optimization view joins for native
// PostgreSQL comparisons, including exact percentile and legacy overlay behavior.
func legacyScanDashboardUsageTotals(tx *gorm.DB, totals *DashboardUsageTotals, includeActiveCounts bool) error {
	tx = tx.Joins("LEFT JOIN routing_outcome ON routing_outcome.request_id = logs.request_id")
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

func legacyScanDashboardUsageRows(tx *gorm.DB, idExpr string, nameExpr string, groupBy string, limit int) ([]DashboardUsageDimensionRow, error) {
	if limit <= 0 {
		limit = 20
	}
	tx = tx.Joins("LEFT JOIN routing_outcome ON routing_outcome.request_id = logs.request_id")
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

func legacyScanDashboardUsageTimeRows(tx *gorm.DB) ([]DashboardUsageTimeRow, error) {
	tx = tx.Joins("LEFT JOIN routing_outcome ON routing_outcome.request_id = logs.request_id")
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

func legacyScanDashboardOutcomeStats(userID int, role int, window DashboardUsageWindow, totals *DashboardUsageTotals) error {
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
	tx := TelemetryDB().Table("routing_outcome").
		Joins("LEFT JOIN routing_decision ON routing_decision.id = routing_outcome.decision_id").
		Where("routing_outcome.outcome_at >= ? AND routing_outcome.outcome_at <= ?", dashboardWindowStartTime(window), dashboardWindowEndTime(window))
	if role < common.RoleRootUser {
		tx = tx.Where("COALESCE(routing_outcome.user_id, routing_decision.user_id) = ?", userID)
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

func legacyScanDashboardChannelRows(userID int, role int, window DashboardUsageWindow, limit int) ([]DashboardUsageDimensionRow, error) {
	if TelemetryDB() == nil {
		return nil, nil
	}
	channelIDExpr := "COALESCE(routing_outcome.channel_id, 0)"
	channelNameExpr := "COALESCE(NULLIF(channels.name, ''), 'unknown')"
	tx := TelemetryDB().Table("routing_outcome").
		Joins("LEFT JOIN routing_decision ON routing_decision.id = routing_outcome.decision_id").
		Joins("LEFT JOIN "+TelemetryChannelTable()+" channels ON channels.id = routing_outcome.channel_id").
		Where("routing_outcome.outcome_at >= ? AND routing_outcome.outcome_at <= ?", dashboardWindowStartTime(window), dashboardWindowEndTime(window))
	if role < common.RoleRootUser {
		tx = tx.Where("COALESCE(routing_outcome.user_id, routing_decision.user_id) = ?", userID)
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

func legacyScanDashboardTTFTStats(userID int, role int, window DashboardUsageWindow, totals *DashboardUsageTotals) error {
	if TelemetryDB() == nil || totals == nil || !TelemetryDB().Migrator().HasColumn(&RoutingOutcome{}, "ttft_ms") {
		return nil
	}
	tx := TelemetryDB().Table("routing_outcome").
		Joins("LEFT JOIN routing_decision ON routing_decision.id = routing_outcome.decision_id").
		Where("routing_outcome.outcome_at >= ? AND routing_outcome.outcome_at <= ?", dashboardWindowStartTime(window), dashboardWindowEndTime(window)).
		Where("routing_outcome.was_streaming = ? AND routing_outcome.ttft_ms IS NOT NULL AND routing_outcome.ttft_ms >= 0", true).
		Where("COALESCE(routing_outcome.audio_output_tokens, 0) = 0 AND COALESCE(routing_outcome.image_output_tokens, 0) = 0")
	if role < common.RoleRootUser {
		tx = tx.Where("COALESCE(routing_outcome.user_id, routing_decision.user_id) = ?", userID)
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

func legacyBuildAdminDashboardUsage(startTimestamp int64, endTimestamp int64) (DashboardUsage, error) {
	window := normalizeDashboardUsageWindow(startTimestamp, endTimestamp)
	result := DashboardUsage{
		Role:   "admin",
		Scope:  "global",
		Window: window,
	}

	if err := legacyScanDashboardUsageTotals(dashboardUsageBaseQuery(window), &result.Totals, true); err != nil {
		return result, err
	}
	if err := legacyScanDashboardOutcomeStats(0, common.RoleRootUser, window, &result.Totals); err != nil {
		return result, err
	}

	var err error
	modelNameExpr := "COALESCE(NULLIF(logs.model_name, ''), 'unknown')"
	result.ByModel, err = legacyScanDashboardUsageRows(dashboardUsageBaseQuery(window), "0", modelNameExpr, modelNameExpr, 20)
	if err != nil {
		return result, err
	}

	keyNameExpr := "COALESCE(NULLIF(logs.token_name, ''), 'unknown')"
	result.ByKey, err = legacyScanDashboardUsageRows(dashboardUsageBaseQuery(window), "logs.token_id", keyNameExpr, "logs.token_id, "+keyNameExpr, 50)
	if err != nil {
		return result, err
	}

	userNameExpr := "COALESCE(NULLIF(logs.username, ''), 'unknown')"
	result.ByUser, err = legacyScanDashboardUsageRows(dashboardUsageBaseQuery(window), "logs.user_id", userNameExpr, "logs.user_id, "+userNameExpr, 50)
	if err != nil {
		return result, err
	}
	result.ByChannel, err = legacyScanDashboardChannelRows(0, common.RoleRootUser, window, 0)
	if err != nil {
		return result, err
	}
	result.TimeSeries, err = legacyScanDashboardUsageTimeRows(dashboardUsageBaseQuery(window))
	if err != nil {
		return result, err
	}
	return result, nil
}

func legacyBuildUserDashboardUsage(userID int, startTimestamp int64, endTimestamp int64) (DashboardUsage, error) {
	window := normalizeDashboardUsageWindow(startTimestamp, endTimestamp)
	result := DashboardUsage{
		Role:   "member",
		Scope:  "user",
		Window: window,
	}

	if err := legacyScanDashboardUsageTotals(userUsageBaseQuery(userID, window), &result.Totals, true); err != nil {
		return result, err
	}
	if err := legacyScanDashboardOutcomeStats(userID, common.RoleCommonUser, window, &result.Totals); err != nil {
		return result, err
	}

	var err error
	modelNameExpr := "COALESCE(NULLIF(logs.model_name, ''), 'unknown')"
	result.ByModel, err = legacyScanDashboardUsageRows(userUsageBaseQuery(userID, window), "0", modelNameExpr, modelNameExpr, 20)
	if err != nil {
		return result, err
	}
	keyNameExpr := "COALESCE(NULLIF(logs.token_name, ''), 'unknown')"
	result.ByKey, err = legacyScanDashboardUsageRows(userUsageBaseQuery(userID, window), "logs.token_id", keyNameExpr, "logs.token_id, "+keyNameExpr, 50)
	if err != nil {
		return result, err
	}
	result.ByChannel, err = legacyScanDashboardChannelRows(userID, common.RoleCommonUser, window, 20)
	if err != nil {
		return result, err
	}
	result.TimeSeries, err = legacyScanDashboardUsageTimeRows(userUsageBaseQuery(userID, window))
	if err != nil {
		return result, err
	}
	return result, nil
}

func legacyBuildProjectDashboardUsage(access ProjectLogAccess, start, end int64) (DashboardUsage, error) {
	window := normalizeDashboardUsageWindow(start, end)
	result := DashboardUsage{Role: access.Viewer.Role, Scope: "project", Window: window, ByModel: []DashboardUsageDimensionRow{}, ByKey: []DashboardUsageDimensionRow{}, TimeSeries: []DashboardUsageTimeRow{}}
	base := func() *gorm.DB { return ApplyProjectLogAccess(dashboardUsageBaseQuery(window), access) }
	if err := legacyScanDashboardUsageTotals(base(), &result.Totals, true); err != nil {
		return result, err
	}
	var err error
	name := "COALESCE(NULLIF(logs.model_name,''),'unknown')"
	result.ByModel, err = legacyScanDashboardUsageRows(base(), "0", name, name, 20)
	if err != nil {
		return result, err
	}
	name = "COALESCE(NULLIF(logs.token_name,''),'unknown')"
	result.ByKey, err = legacyScanDashboardUsageRows(base(), "logs.token_id", name, "logs.token_id, "+name, 50)
	if err != nil {
		return result, err
	}
	if access.Viewer.IsManager() {
		name = "COALESCE(NULLIF(logs.username,''),'unknown')"
		result.ByUser, err = legacyScanDashboardUsageRows(base(), "logs.user_id", name, "logs.user_id, "+name, 100)
		if err != nil {
			return result, err
		}
	}
	result.TimeSeries, err = legacyScanDashboardUsageTimeRows(base())
	if err != nil {
		return result, err
	}
	if err := ProjectLogsQuery(access, ProjectLogFilter{Type: LogTypeError, Start: window.StartTimestamp, End: window.EndTimestamp}).Count(&result.Totals.ErrorCount).Error; err != nil {
		return result, err
	}
	result.Totals.OutcomeCount = result.Totals.RequestCount + result.Totals.ErrorCount
	// Project managers remain customers; vendor costs/margin and routing-channel
	// identities are platform-only, regardless of their platform account role.
	result.Totals.UpstreamMicrousd = 0
	result.Totals.GrossMicrousd = 0
	result.Totals.GrossMarginRatio = 0
	result.Totals.CostSampleCount = 0
	for _, rows := range [][]DashboardUsageDimensionRow{result.ByModel, result.ByKey, result.ByUser} {
		for i := range rows {
			rows[i].UpstreamMicrousd = 0
			rows[i].GrossMicrousd = 0
			rows[i].GrossMarginRatio = 0
			rows[i].CostSampleCount = 0
		}
	}
	for i := range result.TimeSeries {
		result.TimeSeries[i].UpstreamMicrousd = 0
		result.TimeSeries[i].GrossMicrousd = 0
		result.TimeSeries[i].CostSampleCount = 0
	}
	return result, nil
}

func dashboardProjectionPostgresFixture(t *testing.T) (*gorm.DB, ProjectLogAccess, int64, int64) {
	t.Helper()
	_, db := postgresTelemetryFixture(t)
	oldPG, oldSQLite := common.UsingPostgreSQL, common.UsingSQLite
	oldGroup, oldKey, oldTrue, oldFalse, oldLogGroup, oldLogKey := commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal, logGroupCol, logKeyCol
	common.UsingPostgreSQL, common.UsingSQLite = true, false
	initCol()
	t.Cleanup(func() {
		common.UsingPostgreSQL, common.UsingSQLite = oldPG, oldSQLite
		commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal, logGroupCol, logKeyCol = oldGroup, oldKey, oldTrue, oldFalse, oldLogGroup, oldLogKey
	})
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Hour).Add(-2*time.Hour).Unix() + 17
	end := start + 3599
	access := ProjectLogAccess{Project: Project{ID: 42, OwnerUserID: 1}, Viewer: ProjectMember{ProjectID: 42, UserID: 1, Role: ProjectRoleOwner, Status: ProjectMemberActive}}
	for i := 0; i < 110; i++ {
		requestID := fmt.Sprintf("projection-%03d", i)
		if i == 1 {
			requestID = "projection-000"
		}
		uid, channel, status, cached, duration := i%105+1, i%3+1, 200, i%17, i*7
		if i%3 == 0 {
			status = 429
		}
		upstream, gross := 0.000003, 0.000007
		outcome := RoutingOutcome{RequestID: requestID, DecisionID: int64(i + 1), OutcomeAt: time.Unix(start+100+int64(i), 0), UserID: &uid, ChannelID: &channel, StatusCode: &status, CachedInputTokens: &cached, TotalDurationMs: &duration, UpstreamCostUSD: &upstream, GrossMarginUSD: &gross}
		streaming, ttft := true, i*3+1
		outcome.WasStreaming, outcome.TTFTMs = &streaming, &ttft
		if i%9 == 0 {
			audio := 2
			outcome.AudioOutputTokens = &audio
		}
		if i%5 == 0 {
			outcome.CachedInputTokens = nil
			outcome.UpstreamCostUSD = nil
			outcome.GrossMarginUSD = nil
			outcome.TotalDurationMs = nil
		}
		if i%7 == 0 {
			outcome.UserID = nil
		}
		if i != 1 {
			if err := db.Table("routing_outcome_legacy").Create(&outcome).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Table("routing_decision_legacy").Create(&RoutingDecision{ID: int64(i + 1), RequestID: requestID, UserID: &uid, DecisionAt: time.Unix(start, 0)}).Error; err != nil {
				t.Fatal(err)
			}
		}
		row := Log{RequestID: requestID, PublicID: fmt.Sprintf("log_projection_%d", i), RequestTS: (start + int64(i)) * 1000, CreatedAt: start + 100 + int64(i), Type: LogTypeConsume, UserId: uid, Username: fmt.Sprintf("user-%02d", uid), TokenId: i%75 + 1, TokenName: fmt.Sprintf("key-%02d", i%75+1), ModelName: fmt.Sprintf("model-%02d", i%25), AmountMicrousd: 10, PromptTokens: i % 13, CompletionTokens: i % 7, ProjectID: 42, PayerUserID: 1}
		if i%4 == 0 {
			outcome.CachedInputTokens = &cached
			payload, _ := json.Marshal(outcome)
			row.RoutingOutcomeJSON = string(payload)
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Preserve unusual but readable history too: a JSON identity can differ
	// from its containing log, and suppression uses that physical log globally.
	for i, requestID := range []string{"missing-json-id", "mismatched-json-id", "outside-snapshot", "no-outcome"} {
		row := Log{PublicID: fmt.Sprintf("log_edge_%d", i), RequestID: requestID, RequestTS: (start + 500 + int64(i)) * 1000, CreatedAt: start + 500 + int64(i), Type: LogTypeConsume, UserId: 1, TokenId: 1, ModelName: "edges", AmountMicrousd: 17, ProjectID: 42, PayerUserID: 1}
		if i == 0 {
			row.RoutingOutcomeJSON = "{}"
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			status, cached := 400, 9
			if err := db.Table("routing_outcome_legacy").Create(&RoutingOutcome{RequestID: requestID, DecisionID: 1, OutcomeAt: time.Unix(end+1000, 0), StatusCode: &status, CachedInputTokens: &cached}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	for i, pair := range [][2]string{{"different-physical-id", "mismatched-json-id"}, {"outside-snapshot", "outside-snapshot"}, {"duplicate-json-physical", "mismatched-json-id"}} {
		status, cached, latency := 201, 3, 123
		raw, _ := json.Marshal(&RoutingOutcome{RequestID: pair[1], DecisionID: 1, OutcomeAt: time.Unix(start+300, 0), StatusCode: &status, CachedInputTokens: &cached, TotalDurationMs: &latency})
		if err := db.Create(&Log{PublicID: fmt.Sprintf("log_snapshot_%d", i), RequestID: pair[0], RequestTS: (start - 7200 + int64(i)) * 1000, CreatedAt: start - 7200 + int64(i), RoutingOutcomeJSON: string(raw)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Decision view semantics also use JSON identity for the projection and
	// physical request identity for global suppression, independently of the
	// outcome's scope or time. Multiple JSON decisions with one ID stay visible.
	for i, decision := range []struct {
		physical string
		id       int64
		user     int
	}{
		{"projection-007", 9998, 77}, {"decision-outside-physical", 8, 2},
		{"decision-duplicate-one", 1, 2}, {"decision-duplicate-two", 1, 3},
	} {
		raw := fmt.Sprintf(`{"id":%d,"request_id":"json-decision-%d","user_id":%d}`, decision.id, i, decision.user)
		if err := db.Create(&Log{PublicID: fmt.Sprintf("log_decision_%d", i), RequestID: decision.physical, RequestTS: (start - 10800 + int64(i)) * 1000, CreatedAt: start - 10800 + int64(i), RoutingDecisionJSON: raw}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// SQL NULL and empty snapshots both retain legacy facts. A nonempty JSON
	// null still suppresses the physical request's legacy fact, despite having
	// no projected JSON identity. Keep these distinct in both routing branches.
	for i, snapshot := range []string{"sql-null", "", "null"} {
		requestID := fmt.Sprintf("nullable-snapshot-%d", i)
		row := Log{PublicID: fmt.Sprintf("log_nullable_%d", i), RequestID: requestID, RequestTS: (start + 600 + int64(i)) * 1000, CreatedAt: start + 600 + int64(i), Type: LogTypeConsume, UserId: 1, TokenId: 1, ModelName: "nullable", AmountMicrousd: 11, ProjectID: 42, PayerUserID: 1}
		if snapshot == "null" {
			row.RoutingOutcomeJSON, row.RoutingDecisionJSON = snapshot, snapshot
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if snapshot == "sql-null" {
			if err := db.Model(&Log{}).Where("public_id = ?", row.PublicID).Updates(map[string]interface{}{"routing_outcome_json": nil, "routing_decision_json": nil}).Error; err != nil {
				t.Fatal(err)
			}
		}
		uid, streaming, ttft := 1, true, 7
		decisionID := int64(20000 + i)
		if err := db.Table("routing_decision_legacy").Create(&RoutingDecision{ID: decisionID, RequestID: requestID, UserID: &uid, DecisionAt: time.Unix(start, 0)}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Table("routing_outcome_legacy").Create(&RoutingOutcome{RequestID: requestID, DecisionID: decisionID, OutcomeAt: time.Unix(start+600+int64(i), 0), WasStreaming: &streaming, TTFTMs: &ttft}).Error; err != nil {
			t.Fatal(err)
		}
	}
	syncRoutingFactColumns(t, db)
	// Start from production's state: decision IDs are unique there (legacy
	// decisions are a small sequence, snapshot rd_id values random 63-bit
	// numbers), and schema 33 copied decision users onto legacy outcomes.
	if err := db.Exec("DELETE FROM logs WHERE request_id IN ('decision-duplicate-one','decision-duplicate-two')").Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillLegacyOutcomeUsers(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	// Unrelated history makes a global routing scan observable in EXPLAIN.
	if err := db.Exec(`INSERT INTO routing_outcome_legacy(request_id,outcome_at,decision_id) SELECT 'unrelated-'||i, ?, 0 FROM generate_series(1,12000) i`, time.Unix(start-86400, 0)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ANALYZE").Error; err != nil {
		t.Fatal(err)
	}
	return db, access, start, end
}

func TestPostgresDashboardOutcomeProjectionKeepsOverlayAndJoinBoundary(t *testing.T) {
	db, _, start, end := dashboardProjectionPostgresFixture(t)
	var originalView string
	if err := db.Raw("SELECT pg_get_viewdef('routing_outcome'::regclass,true)").Scan(&originalView).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(originalView, "ro_request_id") || strings.Contains(originalView, "jsonb") {
		t.Fatal("compatibility view must read the typed routing columns")
	}
	fields := "request_id,outcome_at,decision_id,user_id,channel_id,status_code,actual_input_tokens,actual_output_tokens,cached_input_tokens,charged_cost_usd,upstream_cost_usd,gross_margin_usd,total_duration_ms,was_streaming,ttft_ms,audio_output_tokens,image_output_tokens"
	old := db.Table("routing_outcome").Select(fields)
	projected := db.Table("(?) AS p", dashboardWindowOutcomeSource(db, time.Unix(0, 0), time.Unix(1<<40, 0), 0)).Select(fields)
	for _, scoped := range []bool{false, true} {
		t.Run(fmt.Sprintf("window=%t", scoped), func(t *testing.T) {
			canonical := func(source *gorm.DB) string {
				t.Helper()
				where := ""
				args := []interface{}{source}
				if scoped {
					where = " WHERE r.outcome_at >= ? AND r.outcome_at <= ?"
					args = append(args, time.Unix(start, 0), time.Unix(end, 0))
				}
				var value string
				if err := db.Raw("SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb)::text FROM (?) r"+where, args...).Scan(&value).Error; err != nil {
					t.Fatal(err)
				}
				return value
			}
			if canonical(old) != canonical(projected) {
				t.Fatal("explicit outcome projection changed snapshot identity, suppression or nullable facts")
			}
		})
	}

	explain := func(memberID int) map[string]interface{} {
		t.Helper()
		query := dashboardOutcomeWindowQuery(db, normalizeDashboardUsageWindow(start, end), memberID).
			Where("routing_outcome.was_streaming = true AND routing_outcome.ttft_ms IS NOT NULL AND routing_outcome.ttft_ms > 0").
			Select("COUNT(*)")
		dry := query.Session(&gorm.Session{DryRun: true}).Find(&[]Log{})
		var raw string
		if err := db.Raw("EXPLAIN (FORMAT JSON) "+dry.Statement.SQL.String(), dry.Statement.Vars...).Scan(&raw).Error; err != nil {
			t.Fatal(err)
		}
		var plans []struct{ Plan map[string]interface{} }
		if err := json.Unmarshal([]byte(raw), &plans); err != nil {
			t.Fatal(err)
		}
		return plans[0].Plan
	}
	// A platform window has no decision to resolve: the TTFT predicate reaches
	// the legacy branch and no decision table is read.
	pushed := false
	var inspectPlatform func(map[string]interface{})
	inspectPlatform = func(p map[string]interface{}) {
		if p["Relation Name"] == "routing_decision_legacy" {
			t.Fatal("platform window resolved decisions it never reads")
		}
		if p["Relation Name"] == "routing_outcome_legacy" && strings.Contains(fmt.Sprint(p["Filter"]), "ttft_ms") {
			pushed = true
		}
		if children, ok := p["Plans"].([]interface{}); ok {
			for _, child := range children {
				inspectPlatform(child.(map[string]interface{}))
			}
		}
	}
	inspectPlatform(explain(0))
	if !pushed {
		t.Fatal("platform TTFT predicate did not reach the legacy branch")
	}
	outerFilter := false
	var inspect func(map[string]interface{})
	inspect = func(p map[string]interface{}) {
		filter := fmt.Sprint(p["Filter"])
		if strings.Contains(filter, "ttft_ms") {
			if p["Node Type"] != "Subquery Scan" || p["Alias"] != "routing_outcome" {
				t.Fatal("TTFT predicate crossed the complete outcome/decision join boundary")
			}
			outerFilter = true
		}
		if children, ok := p["Plans"].([]interface{}); ok {
			for _, child := range children {
				inspect(child.(map[string]interface{}))
			}
		}
	}
	inspect(explain(2))
	if !outerFilter {
		t.Fatal("native plan did not exercise the outer TTFT predicate")
	}
	var afterView string
	if err := db.Raw("SELECT pg_get_viewdef('routing_outcome'::regclass,true)").Scan(&afterView).Error; err != nil {
		t.Fatal(err)
	}
	if afterView != originalView {
		t.Fatal("dashboard query changed the canonical compatibility view")
	}
}

func TestPostgresDashboardMaterializationMatchesCompatibilityQueries(t *testing.T) {
	_, access, start, end := dashboardProjectionPostgresFixture(t)
	window := normalizeDashboardUsageWindow(start, end)
	compareTTFT := func(role int) {
		t.Helper()
		var want, got DashboardUsageTotals
		if err := legacyScanDashboardTTFTStats(2, role, window, &want); err != nil {
			t.Fatal(err)
		}
		if err := scanDashboardTTFTStatsContext(context.Background(), 2, role, window, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("TTFT changed: %+v %+v", want, got)
		}
		if got.TTFTSampleCount == 0 || got.P95TTFTMs == nil {
			t.Fatal("positive exact TTFT percentile was not exercised")
		}
	}
	compareTTFT(common.RoleCommonUser)
	for _, test := range []struct {
		name     string
		old, new func() (DashboardUsage, error)
	}{
		{"user", func() (DashboardUsage, error) { return legacyBuildUserDashboardUsage(2, start, end) }, func() (DashboardUsage, error) {
			return BuildUserDashboardUsageContext(context.Background(), 2, start, end)
		}},
		{"project-manager", func() (DashboardUsage, error) { return legacyBuildProjectDashboardUsage(access, start, end) }, func() (DashboardUsage, error) {
			return BuildProjectDashboardUsageContext(context.Background(), access, start, end)
		}},
		{"project-member", func() (DashboardUsage, error) {
			a := access
			a.Viewer.Role = ProjectRoleMember
			a.Viewer.UserID = 2
			return legacyBuildProjectDashboardUsage(a, start, end)
		}, func() (DashboardUsage, error) {
			a := access
			a.Viewer.Role = ProjectRoleMember
			a.Viewer.UserID = 2
			return BuildProjectDashboardUsageContext(context.Background(), a, start, end)
		}},
		{"platform", func() (DashboardUsage, error) { return legacyBuildAdminDashboardUsage(start, end) }, func() (DashboardUsage, error) {
			return BuildAdminDashboardUsageContext(context.Background(), start, end)
		}},
		{"empty-window", func() (DashboardUsage, error) { return legacyBuildAdminDashboardUsage(end+100000, end+100100) }, func() (DashboardUsage, error) {
			return BuildAdminDashboardUsageContext(context.Background(), end+100000, end+100100)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			want, err := test.old()
			if err != nil {
				t.Fatal(err)
			}
			got, err := test.new()
			if err != nil {
				t.Fatal(err)
			}
			// Empty slices and nil both represent no rows; the optimized SQL is
			// deliberately explicit about empty dimension arrays.
			if len(want.ByUser) == 0 && len(got.ByUser) == 0 {
				want.ByUser = nil
				got.ByUser = nil
			}
			if strings.HasPrefix(test.name, "project") {
				// Project usage reads usage slices: latency percentiles come
				// from their histograms.
				requireUsageSliceLatency(t, "by model", want.ByModel, got.ByModel)
				requireUsageSliceLatency(t, "by key", want.ByKey, got.ByKey)
				requireUsageSliceLatency(t, "by user", want.ByUser, got.ByUser)
			}
			if test.name == "platform" && (len(got.ByModel) != 20 || len(got.ByKey) != 50 || len(got.ByUser) != 50) {
				t.Fatalf("ranking limits not exercised: %d %d %d", len(got.ByModel), len(got.ByKey), len(got.ByUser))
			}
			if test.name == "project-manager" && len(got.ByUser) != 100 {
				t.Fatalf("manager limit not exercised: %d", len(got.ByUser))
			}
			if !reflect.DeepEqual(want, got) {
				a, _ := json.Marshal(want)
				b, _ := json.Marshal(got)
				t.Fatalf("dashboard semantic difference\nold=%s\nnew=%s", a, b)
			}
		})
	}
	compareTTFT(common.RoleRootUser)
}

func TestPostgresDashboardCombinedRoutingMatchesCompleteRoleUsage(t *testing.T) {
	db, _, start, end := dashboardProjectionPostgresFixture(t)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	var combined, batches int
	if err := db.Callback().Row().Before("gorm:row").Register("test:combined-routing", func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if strings.Contains(sql, "WITH dashboard_routing AS MATERIALIZED") {
			combined++
			if strings.Count(sql, "WITH dashboard_outcome_scope AS MATERIALIZED") > 1 {
				t.Error("routing aggregates repeated the complete outcome/decision source")
			}
		}
		if strings.Contains(sql, "WITH dashboard_scope AS MATERIALIZED") {
			batches++
		}
	}); err != nil {
		t.Fatal(err)
	}
	compare := func(t *testing.T, userID, role int, from, to int64) DashboardUsage {
		t.Helper()
		var want DashboardUsage
		var err error
		if role >= common.RoleRootUser {
			want, err = legacyBuildAdminDashboardUsage(from, to)
		} else {
			want, err = legacyBuildUserDashboardUsage(userID, from, to)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = legacyScanDashboardTTFTStats(userID, role, normalizeDashboardUsageWindow(from, to), &want.Totals); err != nil {
			t.Fatal(err)
		}
		combined, batches = 0, 0
		got, err := BuildDashboardUsageForRoleContext(context.Background(), userID, role, from, to)
		if err != nil {
			t.Fatal(err)
		}
		if combined != 1 || batches != 1 {
			t.Fatalf("raw role path must read logs once and joined routing once: batch=%d combined=%d", batches, combined)
		}
		if len(want.ByUser) == 0 && len(got.ByUser) == 0 {
			want.ByUser, got.ByUser = nil, nil
		}
		if !reflect.DeepEqual(want, got) {
			a, _ := json.Marshal(want)
			b, _ := json.Marshal(got)
			t.Fatalf("complete role dashboard changed\nold=%s\nnew=%s", a, b)
		}
		return got
	}
	for _, tc := range []struct {
		name       string
		user, role int
		from, to   int64
	}{
		{"member-decision-user-fallback", 2, common.RoleCommonUser, start, end},
		{"other-member", 3, common.RoleCommonUser, start, end},
		{"member-empty", 99999, common.RoleCommonUser, start, end},
		{"platform-non-hour-window", 1, common.RoleRootUser, start, end},
		{"empty-window", 1, common.RoleRootUser, end + 100000, end + 100100},
	} {
		t.Run(tc.name, func(t *testing.T) { compare(t, tc.user, tc.role, tc.from, tc.to) })
	}
	// Unknown TTFT must stay nil after aggregation; no source row is dropped from
	// outcome/channel totals when it is ineligible for the TTFT-only percentile.
	if err := db.Exec("UPDATE routing_outcome_legacy SET was_streaming = false").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`UPDATE logs SET routing_outcome_json = jsonb_set(routing_outcome_json::jsonb,'{was_streaming}','false'::jsonb)::text,
		ro_was_streaming = false
		WHERE routing_outcome_json <> '' AND jsonb_typeof(routing_outcome_json::jsonb) = 'object'`).Error; err != nil {
		t.Fatal(err)
	}
	t.Run("no-eligible-ttft-preserves-other-aggregates", func(t *testing.T) {
		got := compare(t, 1, common.RoleRootUser, start, end)
		if got.Totals.TTFTSampleCount != 0 || got.Totals.P95TTFTMs != nil || got.Totals.OutcomeCount == 0 {
			t.Fatal("unknown TTFT was fabricated or valid outcomes disappeared")
		}
	})
}

func TestPostgresDashboardBoundedHistoryPlan(t *testing.T) {
	db, _, start, end := dashboardProjectionPostgresFixture(t)
	joined := dashboardUsageWithBoundedOutcome(dashboardUsageBaseQuery(normalizeDashboardUsageWindow(start, end)))
	dry := joined.Session(&gorm.Session{DryRun: true}).Select("COUNT(*),SUM(routing_outcome.cached_input_tokens)").Find(&[]Log{})
	var raw string
	if err := db.Raw("EXPLAIN (FORMAT JSON) "+dry.Statement.SQL.String(), dry.Statement.Vars...).Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	var plans []struct{ Plan map[string]interface{} }
	if err := json.Unmarshal([]byte(raw), &plans); err != nil {
		t.Fatal(err)
	}
	indexed := false
	var visit func(map[string]interface{})
	visit = func(p map[string]interface{}) {
		if p["Relation Name"] == "routing_outcome_legacy" {
			if p["Node Type"] == "Seq Scan" {
				t.Fatal("bounded lookup scanned complete legacy history")
			}
			if strings.Contains(fmt.Sprint(p["Node Type"]), "Index") && strings.Contains(fmt.Sprint(p["Index Cond"]), "request_id") {
				indexed = true
			}
		}
		if children, ok := p["Plans"].([]interface{}); ok {
			for _, child := range children {
				visit(child.(map[string]interface{}))
			}
		}
	}
	visit(plans[0].Plan)
	if !indexed {
		t.Fatal("legacy request ID lookup was not indexed")
	}
	old := dashboardUsageBaseQuery(normalizeDashboardUsageWindow(start, end)).Joins("LEFT JOIN routing_outcome ON routing_outcome.request_id=logs.request_id")
	oldDry := old.Session(&gorm.Session{DryRun: true}).Select("COUNT(*),SUM(routing_outcome.cached_input_tokens)").Find(&[]Log{})
	if err := db.Raw("EXPLAIN (FORMAT JSON) "+oldDry.Statement.SQL.String(), oldDry.Statement.Vars...).Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &plans); err != nil {
		t.Fatal(err)
	}
	globalHistory := false
	var inspectOld func(map[string]interface{})
	inspectOld = func(p map[string]interface{}) {
		if p["Relation Name"] == "routing_outcome_legacy" && p["Plan Rows"].(float64) > 1000 {
			globalHistory = true
		}
		if children, ok := p["Plans"].([]interface{}); ok {
			for _, child := range children {
				inspectOld(child.(map[string]interface{}))
			}
		}
	}
	inspectOld(plans[0].Plan)
	if !globalHistory {
		t.Fatal("baseline fixture did not expose the unbounded retained-history scan")
	}
}

func TestPostgresDashboardContextCancelsBlockedReads(t *testing.T) {
	db, access, start, end := dashboardProjectionPostgresFixture(t)
	for _, test := range []struct {
		name, table string
		run         func(context.Context) error
	}{
		{"platform-raw", "logs", func(ctx context.Context) error {
			_, err := BuildDashboardUsageForRoleContext(ctx, 1, common.RoleRootUser, start, end)
			return err
		}},
		{"platform-routing", "routing_outcome_legacy", func(ctx context.Context) error {
			_, err := BuildDashboardUsageForRoleContext(ctx, 1, common.RoleRootUser, start, end)
			return err
		}},
		{"project-raw", "logs", func(ctx context.Context) error {
			_, err := BuildProjectDashboardUsageContext(ctx, access, start, end)
			return err
		}},
		{"platform-rollup", "dashboard_usage_hourly_totals", func(ctx context.Context) error {
			_, err := BuildDashboardUsageForRoleContext(ctx, 1, common.RoleRootUser, start-start%3600, start-start%3600+3599)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			lock := db.Begin()
			if lock.Error != nil {
				t.Fatal(lock.Error)
			}
			defer lock.Rollback()
			if err := lock.Exec("LOCK TABLE " + test.table + " IN ACCESS EXCLUSIVE MODE").Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			began := time.Now()
			err := test.run(ctx)
			if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) || time.Since(began) > 3*time.Second {
				t.Fatalf("read did not promptly cancel: error=%v elapsed=%s", err, time.Since(began))
			}
		})
	}
}
