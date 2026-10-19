package model

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

// PostgreSQL routing_outcome overlays JSON snapshots onto physical history.
// Joining that view directly lets the planner aggregate/sort the entire retained
// history before applying a small log window. Bound both branches by the exact
// request IDs of this query instead. In particular, match the JSON request_id,
// not the containing log's ID, and retain the view's global legacy suppression.
func dashboardUsageWithBoundedOutcome(tx *gorm.DB) *gorm.DB {
	source := dashboardBoundedOutcomeSource(tx)
	return tx.Joins("LEFT JOIN (?) AS routing_outcome ON routing_outcome.request_id = logs.request_id", source)
}

func dashboardBoundedOutcomeSource(tx *gorm.DB) *gorm.DB {
	keys := tx.Session(&gorm.Session{}).Select("logs.request_id").Distinct("logs.request_id")
	// A fresh statement on tx's own connection, so a caller's transaction
	// (and its session settings) also covers the outcome lookup.
	source := tx.Session(&gorm.Session{NewDB: true}).Raw(`
		SELECT snap.ro_request_id AS request_id,
			snap.ro_upstream_cost_usd AS upstream_cost_usd,
			snap.ro_gross_margin_usd AS gross_margin_usd,
			snap.ro_cached_input_tokens AS cached_input_tokens,
			snap.ro_status_code AS status_code,
			snap.ro_total_duration_ms AS total_duration_ms
		FROM logs snap
		WHERE snap.ro_request_id IS NOT NULL
			AND snap.ro_request_id IN (?)
		UNION ALL
		SELECT old.request_id, old.upstream_cost_usd, old.gross_margin_usd,
			old.cached_input_tokens, old.status_code, old.total_duration_ms
		FROM (?) scoped_keys
		JOIN LATERAL (
			SELECT request_id, upstream_cost_usd, gross_margin_usd,
				cached_input_tokens, status_code, total_duration_ms
			FROM routing_outcome_legacy
			WHERE request_id = scoped_keys.request_id OFFSET 0
		) old ON TRUE
		WHERE NOT EXISTS (
			SELECT 1 FROM logs newer
			WHERE newer.request_id = old.request_id
				AND newer.ro_request_id IS NOT NULL OFFSET 0
		)`, keys, keys)
	// OFFSET 0 keeps the two indexed, correlated history lookups parameterized;
	// flattening either one reinstates the unbounded history anti-join.
	return source
}

// Materialize the exact scoped logs and their outcome join once for the totals,
// rankings and daily series. Repeating the compatibility overlay for each card
// turns one bounded history lookup into several full-history scans.
func scanPostgresDashboardLogUsage(tx *gorm.DB, userLimit int) (DashboardUsage, error) {
	var result DashboardUsage
	internalIDs, err := hiddenTokenIDs()
	if err != nil {
		return result, err
	}
	keyPredicate := "logs.token_id > 0"
	if len(internalIDs) > 0 {
		ids := make([]string, len(internalIDs))
		for i, id := range internalIDs {
			ids[i] = strconv.Itoa(id)
		}
		keyPredicate += " AND logs.token_id NOT IN (" + strings.Join(ids, ",") + ")"
	}
	scoped := tx.Session(&gorm.Session{}).Select("logs.request_id, logs.user_id, logs.token_id, logs.model_name, logs.token_name, logs.username, logs.created_at, logs.amount_microusd, logs.prompt_tokens, logs.completion_tokens")
	store := TelemetryDB().WithContext(tx.Statement.Context)
	outcomes := dashboardBoundedOutcomeSource(store.Table("dashboard_scope AS logs"))
	metrics := `COUNT(*) AS request_count,
		COALESCE(SUM(logs.amount_microusd),0) AS amount_microusd,
		CAST(COALESCE(SUM(logs.upstream_cost_usd),0)*1000000 AS BIGINT) AS upstream_cost_microusd,
		CAST(COALESCE(SUM(logs.gross_margin_usd),0)*1000000 AS BIGINT) AS gross_margin_microusd,
		COALESCE(SUM(CASE WHEN logs.upstream_cost_usd IS NOT NULL THEN 1 ELSE 0 END),0) AS cost_sample_count,
		COALESCE(SUM(logs.prompt_tokens),0) AS prompt_tokens,
		COALESCE(SUM(logs.cached_input_tokens),0) AS cached_input_tokens,
		COALESCE(SUM(logs.completion_tokens),0) AS completion_tokens`
	rowMetrics := metrics + `,
		COALESCE(SUM(CASE WHEN logs.status_code >= 400 THEN 1 ELSE 0 END),0) AS error_count,
		COALESCE(AVG(logs.total_duration_ms),0) AS avg_latency_ms,
		COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP(ORDER BY logs.total_duration_ms) FILTER (WHERE logs.total_duration_ms IS NOT NULL),0) AS p95_latency_ms,
		MAX(logs.created_at) AS last_used_at`
	dimension := func(id, name, group string, limit int) string {
		return fmt.Sprintf(`COALESCE((SELECT jsonb_agg(to_jsonb(r)) FROM (
			SELECT %s AS id, %s AS name, %s FROM dashboard_rows logs
			GROUP BY %s ORDER BY amount_microusd DESC, request_count DESC, name ASC LIMIT %d
		) r),'[]'::jsonb)`, id, name, rowMetrics, group, limit)
	}
	modelName := "COALESCE(NULLIF(logs.model_name,''),'unknown')"
	keyName := "COALESCE(NULLIF(logs.token_name,''),'unknown')"
	userName := "COALESCE(NULLIF(logs.username,''),'unknown')"
	users := "'[]'::jsonb"
	if userLimit > 0 {
		users = dimension("logs.user_id", userName, "logs.user_id, "+userName, userLimit)
	}
	query := `WITH dashboard_scope AS MATERIALIZED (?),
		dashboard_outcomes AS MATERIALIZED (?),
		dashboard_rows AS MATERIALIZED (
			SELECT logs.*, outcome.upstream_cost_usd, outcome.gross_margin_usd,
				outcome.cached_input_tokens, outcome.status_code, outcome.total_duration_ms
			FROM dashboard_scope logs LEFT JOIN dashboard_outcomes outcome ON outcome.request_id=logs.request_id
		)
		SELECT jsonb_build_object(
			'totals', (SELECT to_jsonb(r) FROM (SELECT ` + metrics + `,
				COUNT(DISTINCT CASE WHEN logs.user_id > 0 THEN logs.user_id END) AS active_users,
				COUNT(DISTINCT CASE WHEN ` + keyPredicate + ` THEN logs.token_id END) AS active_keys
				FROM dashboard_rows logs) r),
			'by_model', ` + dimension("0", modelName, modelName, 20) + `,
			'by_key', ` + dimension("logs.token_id", keyName, "logs.token_id, "+keyName, 50) + `,
			'by_user', ` + users + `,
			'time_series', COALESCE((SELECT jsonb_agg(to_jsonb(r)) FROM (
				SELECT (logs.created_at/86400)*86400 AS bucket, ` + metrics + `,
					COALESCE(SUM(CASE WHEN logs.status_code >= 400 THEN 1 ELSE 0 END),0) AS error_count
				FROM dashboard_rows logs GROUP BY (logs.created_at/86400)*86400 ORDER BY bucket ASC
			) r),'[]'::jsonb)
		)::text AS payload`
	var row struct{ Payload string }
	if err := store.Raw(query, scoped, outcomes).Scan(&row).Error; err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(row.Payload), &result); err != nil {
		return result, fmt.Errorf("decode dashboard aggregates: %w", err)
	}
	populateDashboardUsageTotalsDerived(&result.Totals)
	for _, rows := range [][]DashboardUsageDimensionRow{result.ByModel, result.ByKey, result.ByUser} {
		populateDashboardUsageRowsDerived(rows)
	}
	return result, nil
}

// Decision compatibility history needs the same protection as outcome history.
// Filter outcomes by their actual outcome_at, then resolve only the referenced
// decision IDs. Keep legacy suppression global and log-row IDs authoritative.
// dashboardMemberID is the user a member-scoped outcome window narrows to, or
// zero for an operator's platform-wide window.
func dashboardMemberID(userID, role int) int {
	if role >= common.RoleRootUser {
		return 0
	}
	return userID
}

// dashboardOutcomeWindowQuery reads the window's outcomes with their decision
// user. A positive memberID narrows both branches to that user's outcomes and
// to outcomes without a user of their own, which only the decision can
// attribute; callers still apply dashboardOutcomeUserPredicate for the exact
// answer. Without it a member's page resolved every user's outcomes first.
func dashboardOutcomeWindowQuery(tx *gorm.DB, window DashboardUsageWindow, memberID int) *gorm.DB {
	store := TelemetryDB().WithContext(tx.Statement.Context)
	windowed := store.Table("routing_outcome").Where("routing_outcome.outcome_at >= ? AND routing_outcome.outcome_at <= ?", dashboardWindowStartTime(window), dashboardWindowEndTime(window))
	if !routingFactsAreViews(store) {
		if memberID > 0 {
			windowed = windowed.Where("(routing_outcome.user_id = ? OR routing_outcome.user_id IS NULL)", memberID)
		}
		return windowed.Joins("LEFT JOIN routing_decision ON routing_decision.id = routing_outcome.decision_id")
	}
	// The window bound is written into both branches so it reaches the
	// ro_outcome_at and legacy outcome_at indexes before the history check.
	// A decision stamped on its outcome's own log row gives the user from that
	// row; only outcomes whose decision lives elsewhere are resolved by id.
	// Resolving all of them by id self-joined logs: 1.1 s of a 1.75 s seven-day
	// TTFT read on 2026-10-14.
	outcomes := dashboardWindowOutcomeSource(store, dashboardWindowStartTime(window), dashboardWindowEndTime(window), memberID)
	if memberID == 0 {
		// A platform-wide window applies no user predicate, so nothing reads
		// the decision's user. Resolving it anyway looked up every pre-cutover
		// outcome's legacy decision: 3.3 s of a thirty-day TTFT read on
		// 2026-10-16. Without the materialized scope, the callers' filters
		// also reach both branches of the union.
		return store.Table(`(SELECT platform.*, NULL::bigint AS decision_user_id FROM (?) platform) AS routing_outcome`, outcomes)
	}
	windowed = store.Table("(?) AS routing_outcome", outcomes)
	decisions := store.Raw(`SELECT snap.rd_id AS id,
		snap.rd_user_id AS user_id
		FROM logs snap WHERE snap.rd_id IS NOT NULL
		AND snap.rd_id IN (SELECT DISTINCT decision_id FROM dashboard_outcome_scope WHERE NOT decision_on_row)
		UNION ALL
		SELECT old.id, old.user_id FROM (SELECT DISTINCT decision_id FROM dashboard_outcome_scope WHERE NOT decision_on_row) keys
		JOIN LATERAL (SELECT id,user_id,request_id FROM routing_decision_legacy WHERE id=keys.decision_id OFFSET 0) old ON TRUE
		WHERE NOT EXISTS (SELECT 1 FROM logs newer WHERE newer.request_id=old.request_id AND newer.rd_id IS NOT NULL OFFSET 0)`)
	source := store.Raw(`WITH dashboard_outcome_scope AS MATERIALIZED (?)
		SELECT o.*, CASE WHEN o.decision_on_row THEN o.row_decision_user_id ELSE d.user_id END AS decision_user_id
		FROM dashboard_outcome_scope o
		LEFT JOIN (?) d ON d.id=o.decision_id AND NOT o.decision_on_row OFFSET 0`, windowed, decisions)
	// Keep outer TTFT/user predicates above the complete join. Pushing them into
	// the materialized scope can estimate one row and repeatedly rescan decisions
	// for thousands of actual rows. Preserve the join's duplicate multiplicity.
	return store.Table("(?) AS routing_outcome", source)
}

func dashboardWindowOutcomeSource(store *gorm.DB, start, end time.Time, memberID int) *gorm.DB {
	current, history := "", ""
	currentArgs, historyArgs := []interface{}{start, end}, []interface{}{start, end}
	if memberID > 0 {
		current = " AND (snap.ro_user_id = ? OR snap.ro_user_id IS NULL)"
		// Schema 33 copied each legacy outcome's decision user onto it
		// (backfillLegacyOutcomeUsers), so one still NULL has no user to
		// attribute it to. Matching NULL here pulled 98.7% of the legacy rows
		// into every member window: 61 s for ninety days on 2026-10-16.
		history = " AND old.user_id = ?"
		currentArgs, historyArgs = append(currentArgs, memberID), append(historyArgs, memberID)
	}
	args := append(currentArgs, historyArgs...)
	return store.Raw(`SELECT
		snap.ro_request_id AS request_id,
		snap.ro_outcome_at AS outcome_at,
		snap.ro_decision_id AS decision_id,
		snap.ro_user_id AS user_id,
		snap.ro_channel_id AS channel_id,
		snap.ro_status_code AS status_code,
		snap.ro_actual_input_tokens AS actual_input_tokens,
		snap.ro_actual_output_tokens AS actual_output_tokens,
		snap.ro_cached_input_tokens AS cached_input_tokens,
		snap.ro_charged_cost_usd AS charged_cost_usd,
		snap.ro_upstream_cost_usd AS upstream_cost_usd,
		snap.ro_gross_margin_usd AS gross_margin_usd,
		snap.ro_total_duration_ms AS total_duration_ms,
		snap.ro_was_streaming AS was_streaming,
		snap.ro_ttft_ms AS ttft_ms,
		snap.ro_audio_output_tokens AS audio_output_tokens,
		snap.ro_image_output_tokens AS image_output_tokens,
		snap.rd_id IS NOT DISTINCT FROM snap.ro_decision_id AS decision_on_row,
		snap.rd_user_id AS row_decision_user_id
		FROM logs snap WHERE snap.ro_request_id IS NOT NULL
			AND snap.ro_outcome_at >= ? AND snap.ro_outcome_at <= ?`+current+`
		UNION ALL
		SELECT old.request_id, old.outcome_at, old.decision_id, old.user_id,
			old.channel_id, old.status_code, old.actual_input_tokens,
			old.actual_output_tokens, old.cached_input_tokens, old.charged_cost_usd,
			old.upstream_cost_usd, old.gross_margin_usd, old.total_duration_ms,
			old.was_streaming, old.ttft_ms, old.audio_output_tokens, old.image_output_tokens,
			FALSE, NULL::bigint
		FROM routing_outcome_legacy old WHERE old.outcome_at >= ? AND old.outcome_at <= ?`+history+` AND NOT EXISTS (
			SELECT 1 FROM logs newer WHERE newer.request_id=old.request_id
				AND newer.ro_request_id IS NOT NULL OFFSET 0)`, args...)
}

func dashboardOutcomeUserPredicate() string {
	if routingFactsAreViews(TelemetryDB()) {
		return "COALESCE(routing_outcome.user_id,routing_outcome.decision_user_id) = ?"
	}
	return "COALESCE(routing_outcome.user_id,routing_decision.user_id) = ?"
}

// Keep the complete outcome/decision join once for the raw dashboard's routing
// totals, channels and TTFT. The public admin/user builders retain their historic
// no-TTFT contract; only the role-aware raw path uses this combined scanner.
func buildPostgresDashboardUsageForRole(ctx context.Context, userID, role int, window DashboardUsageWindow) (DashboardUsage, error) {
	result := DashboardUsage{Role: "member", Scope: "user", Window: window}
	base := userUsageBaseQuery(userID, window).WithContext(ctx)
	userLimit, channelLimit := 0, 20
	if role >= common.RoleRootUser {
		result.Role, result.Scope = "admin", "global"
		base = dashboardUsageBaseQuery(window).WithContext(ctx)
		userLimit, channelLimit = 50, 0
	}
	logs, err := scanPostgresDashboardLogUsage(base, userLimit)
	if err != nil {
		return result, err
	}
	result.Totals, result.ByModel, result.ByKey, result.ByUser, result.TimeSeries = logs.Totals, logs.ByModel, logs.ByKey, logs.ByUser, logs.TimeSeries
	result.ByChannel, err = scanPostgresDashboardRoutingUsage(ctx, userID, role, window, channelLimit, &result.Totals)
	return result, err
}

func scanPostgresDashboardRoutingUsage(ctx context.Context, userID, role int, window DashboardUsageWindow, channelLimit int, totals *DashboardUsageTotals) ([]DashboardUsageDimensionRow, error) {
	store := TelemetryDB().WithContext(ctx)
	source := dashboardOutcomeWindowQuery(store, window, dashboardMemberID(userID, role))
	if role < common.RoleRootUser {
		source = source.Where(dashboardOutcomeUserPredicate(), userID)
	}
	source = source.Select("routing_outcome.*")
	limit := ""
	if channelLimit > 0 {
		limit = fmt.Sprintf(" LIMIT %d", channelLimit)
	}
	query := `WITH dashboard_routing AS MATERIALIZED (?)
		SELECT jsonb_build_object(
			'stats', (SELECT to_jsonb(r) FROM (SELECT COUNT(*) AS outcome_count,
				COALESCE(SUM(CASE WHEN routing_outcome.status_code >= 400 THEN 1 ELSE 0 END),0) AS error_count,
				COALESCE(AVG(routing_outcome.total_duration_ms),0) AS avg_latency_ms,
				` + dashboardLatencySelect("routing_outcome.total_duration_ms") + ` AS p95_latency_ms
				FROM dashboard_routing routing_outcome) r),
			'ttft', (SELECT to_jsonb(r) FROM (SELECT COUNT(*) AS count,
				` + dashboardLatencySelect("routing_outcome.ttft_ms") + ` AS p95
				FROM dashboard_routing routing_outcome
				WHERE routing_outcome.was_streaming = true AND routing_outcome.ttft_ms IS NOT NULL AND routing_outcome.ttft_ms >= 0
				AND COALESCE(routing_outcome.audio_output_tokens,0) = 0 AND COALESCE(routing_outcome.image_output_tokens,0) = 0) r),
			'by_channel', COALESCE((SELECT jsonb_agg(to_jsonb(r)) FROM (
				SELECT COALESCE(routing_outcome.channel_id,0) AS id,
				COALESCE(NULLIF(channels.name,''),'unknown') AS name, COUNT(*) AS request_count,
				` + dashboardMicrousdSelect("routing_outcome.charged_cost_usd") + ` AS amount_microusd,
				` + dashboardMicrousdSelect("routing_outcome.upstream_cost_usd") + ` AS upstream_cost_microusd,
				` + dashboardMicrousdSelect("routing_outcome.gross_margin_usd") + ` AS gross_margin_microusd,
				COALESCE(SUM(CASE WHEN routing_outcome.upstream_cost_usd IS NOT NULL THEN 1 ELSE 0 END),0) AS cost_sample_count,
				COALESCE(SUM(routing_outcome.actual_input_tokens),0) AS prompt_tokens,
				COALESCE(SUM(routing_outcome.cached_input_tokens),0) AS cached_input_tokens,
				COALESCE(SUM(routing_outcome.actual_output_tokens),0) AS completion_tokens,
				COALESCE(SUM(CASE WHEN routing_outcome.status_code >= 400 THEN 1 ELSE 0 END),0) AS error_count,
				COALESCE(AVG(routing_outcome.total_duration_ms),0) AS avg_latency_ms,
				` + dashboardLatencySelect("routing_outcome.total_duration_ms") + ` AS p95_latency_ms,
				` + dashboardUnixTimeSelect("routing_outcome.outcome_at") + ` AS last_used_at
				FROM dashboard_routing routing_outcome
				LEFT JOIN ` + TelemetryChannelTable() + ` channels ON channels.id = routing_outcome.channel_id
				GROUP BY COALESCE(routing_outcome.channel_id,0), COALESCE(NULLIF(channels.name,''),'unknown')
				ORDER BY request_count DESC, amount_microusd DESC, name ASC` + limit + `
			) r),'[]'::jsonb))::text AS payload`
	var row struct{ Payload string }
	if err := store.Raw(query, source).Scan(&row).Error; err != nil {
		return nil, err
	}
	var decoded struct {
		Stats struct {
			OutcomeCount int64   `json:"outcome_count"`
			ErrorCount   int64   `json:"error_count"`
			AvgLatencyMs float64 `json:"avg_latency_ms"`
			P95LatencyMs float64 `json:"p95_latency_ms"`
		} `json:"stats"`
		TTFT struct {
			Count int64   `json:"count"`
			P95   float64 `json:"p95"`
		} `json:"ttft"`
		ByChannel []DashboardUsageDimensionRow `json:"by_channel"`
	}
	if err := json.Unmarshal([]byte(row.Payload), &decoded); err != nil {
		return nil, fmt.Errorf("decode dashboard routing aggregates: %w", err)
	}
	totals.OutcomeCount, totals.ErrorCount = decoded.Stats.OutcomeCount, decoded.Stats.ErrorCount
	totals.AvgLatencyMs, totals.P95LatencyMs = decoded.Stats.AvgLatencyMs, decoded.Stats.P95LatencyMs
	totals.TTFTSampleCount = decoded.TTFT.Count
	if decoded.TTFT.Count > 0 {
		totals.P95TTFTMs = &decoded.TTFT.P95
	}
	populateDashboardUsageRowsDerived(decoded.ByChannel)
	return decoded.ByChannel, nil
}
