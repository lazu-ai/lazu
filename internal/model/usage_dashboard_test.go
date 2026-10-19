package model

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func setupDashboardUsageTestDB(t *testing.T) func() {
	t.Helper()
	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	telemetryDB = db
	initCol()
	if err := db.Exec(`
		CREATE TABLE logs (
			id integer primary key autoincrement,
			public_id text,
			request_id text,
			user_id integer,
			username text,
			token_id integer,
			token_name text,
			model_name text,
			type integer,
			created_at integer,
			amount_microusd integer,
			prompt_tokens integer,
			completion_tokens integer,
			channel_id integer,
			channel_name text default '',
			"group" text
		)
	`).Error; err != nil {
		t.Fatalf("failed to create logs table: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE channels (
			id integer primary key,
			name text
		)
	`).Error; err != nil {
		t.Fatalf("failed to create channels table: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE routing_decision (
			id integer primary key,
			request_id text,
			user_id integer
		)
	`).Error; err != nil {
		t.Fatalf("failed to create routing_decision table: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE routing_outcome (
			request_id text primary key,
			outcome_at datetime,
			decision_id integer,
			user_id integer,
			channel_id integer,
			channel_name text default '',
			status_code integer,
			actual_input_tokens integer,
			cached_input_tokens integer,
			actual_output_tokens integer,
			upstream_cost_usd real,
			charged_cost_usd real,
			gross_margin_usd real,
			total_duration_ms integer,
			ttft_ms integer,
			was_streaming boolean,
			audio_output_tokens integer,
			image_output_tokens integer
		)
	`).Error; err != nil {
		t.Fatalf("failed to create routing_outcome table: %v", err)
	}
	if err := db.AutoMigrate(&DashboardUsageHourlyTotal{}, &DashboardUsageHourlyDimension{}); err != nil {
		t.Fatalf("failed to migrate dashboard rollup tables: %v", err)
	}
	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = nil
		telemetryDB = nil
	}
}

func insertDashboardUsageLog(t *testing.T, log Log) {
	t.Helper()
	if err := telemetryDB.Exec(`
		INSERT INTO logs (
			public_id, request_id, user_id, username, token_id, token_name, model_name,
			type, created_at, amount_microusd, prompt_tokens, completion_tokens, channel_id, "group"
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		log.PublicID,
		log.RequestID,
		log.UserId,
		log.Username,
		log.TokenId,
		log.TokenName,
		log.ModelName,
		log.Type,
		log.CreatedAt,
		log.AmountMicrousd,
		log.PromptTokens,
		log.CompletionTokens,
		log.ChannelId,
		log.Group,
	).Error; err != nil {
		t.Fatalf("failed to insert log %s: %v", log.PublicID, err)
	}
}

func insertDashboardChannel(t *testing.T, id int, name string) {
	t.Helper()
	if err := coreDB.Exec(`INSERT INTO channels (id, name) VALUES (?, ?)`, id, name).Error; err != nil {
		t.Fatalf("failed to insert channel %d: %v", id, err)
	}
}

func insertDashboardRoutingOutcome(t *testing.T, decisionID int64, requestID string, userID int, channelID int, statusCode int, inputTokens int, cachedTokens int, outputTokens int, durationMs int, chargedCostUSD float64, at int64, costFields ...float64) {
	t.Helper()
	if err := coreDB.Exec(`INSERT INTO routing_decision (id, request_id, user_id) VALUES (?, ?, ?)`, decisionID, requestID, userID).Error; err != nil {
		t.Fatalf("failed to insert decision %d: %v", decisionID, err)
	}
	var upstreamCost interface{}
	var grossMargin interface{}
	if len(costFields) > 0 {
		upstreamCost = costFields[0]
	}
	if len(costFields) > 1 {
		grossMargin = costFields[1]
	}
	if err := coreDB.Exec(`
		INSERT INTO routing_outcome (
			request_id, outcome_at, decision_id, channel_id, status_code,
			actual_input_tokens, cached_input_tokens, actual_output_tokens,
			upstream_cost_usd, charged_cost_usd, gross_margin_usd, total_duration_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		requestID,
		time.Unix(at, 0).UTC(),
		decisionID,
		channelID,
		statusCode,
		inputTokens,
		cachedTokens,
		outputTokens,
		upstreamCost,
		chargedCostUSD,
		grossMargin,
		durationMs,
	).Error; err != nil {
		t.Fatalf("failed to insert outcome %s: %v", requestID, err)
	}
}

func findDashboardRow(rows []DashboardUsageDimensionRow, name string) (DashboardUsageDimensionRow, bool) {
	for _, row := range rows {
		if row.Name == name {
			return row, true
		}
	}
	return DashboardUsageDimensionRow{}, false
}

func TestBuildUserDashboardUsageFiltersByUser(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()

	start := int64(1_700_000_000)
	end := start + secondsInDay
	insertDashboardChannel(t, 11, "deepseek-official")
	insertDashboardChannel(t, 12, "reseller-direct")
	insertDashboardUsageLog(t, Log{
		PublicID:       "usage-user-a",
		RequestID:      "req-user-a",
		UserId:         1,
		Username:       "alice",
		TokenId:        10,
		TokenName:      "alice-key",
		ModelName:      "deepseek-v3.2",
		Type:           LogTypeConsume,
		CreatedAt:      start + 600,
		AmountMicrousd: 100,
		PromptTokens:   20,
		ChannelId:      11,
	})
	insertDashboardRoutingOutcome(t, 100, "req-user-a", 1, 11, 200, 20, 5, 0, 120, 0.0001, start+600)
	insertDashboardUsageLog(t, Log{
		PublicID:         "usage-user-b",
		RequestID:        "req-user-b",
		UserId:           2,
		Username:         "bob",
		TokenId:          20,
		TokenName:        "bob-key",
		ModelName:        "deepseek-v4-flash",
		Type:             LogTypeConsume,
		CreatedAt:        start + 1200,
		AmountMicrousd:   300,
		CompletionTokens: 30,
		ChannelId:        12,
	})
	insertDashboardRoutingOutcome(t, 101, "req-user-b", 2, 12, 200, 0, 0, 30, 300, 0.0003, start+1200)

	usage, err := BuildUserDashboardUsage(1, start, end)
	if err != nil {
		t.Fatalf("BuildUserDashboardUsage returned error: %v", err)
	}
	if usage.Scope != "user" {
		t.Fatalf("expected user scope, got %q", usage.Scope)
	}
	if usage.Totals.RequestCount != 1 || usage.Totals.AmountMicrousd != 100 {
		t.Fatalf("unexpected user totals: %#v", usage.Totals)
	}
	if usage.Totals.ActiveKeys != 1 {
		t.Fatalf("expected one active key for user scope, got %#v", usage.Totals)
	}
	if usage.Totals.CachedInputTokens != 5 || usage.Totals.OutcomeCount != 1 || usage.Totals.P95LatencyMs != 120 {
		t.Fatalf("unexpected user telemetry totals: %#v", usage.Totals)
	}
	if len(usage.ByModel) != 1 || usage.ByModel[0].Name != "deepseek-v3.2" {
		t.Fatalf("expected only alice model row, got %#v", usage.ByModel)
	}
	if len(usage.ByChannel) != 1 || usage.ByChannel[0].Name != "deepseek-official" {
		t.Fatalf("expected only alice channel row, got %#v", usage.ByChannel)
	}
}

func TestBuildAdminDashboardUsageAggregatesGlobalRankings(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()

	start := int64(1_700_000_000)
	end := start + secondsInDay
	insertDashboardChannel(t, 11, "deepseek-official")
	insertDashboardChannel(t, 12, "reseller-direct")
	insertDashboardUsageLog(t, Log{
		PublicID:       "usage-admin-a",
		RequestID:      "req-admin-a",
		UserId:         1,
		Username:       "alice",
		TokenId:        10,
		TokenName:      "alice-key",
		ModelName:      "deepseek-v3.2",
		Type:           LogTypeConsume,
		CreatedAt:      start + 600,
		AmountMicrousd: 100,
		PromptTokens:   20,
		ChannelId:      11,
	})
	insertDashboardRoutingOutcome(t, 200, "req-admin-a", 1, 11, 200, 20, 5, 0, 100, 0.0001, start+600, 0.00004, 0.00006)
	insertDashboardUsageLog(t, Log{
		PublicID:         "usage-admin-b",
		RequestID:        "req-admin-b",
		UserId:           2,
		Username:         "bob",
		TokenId:          20,
		TokenName:        "bob-key",
		ModelName:        "deepseek-v4-flash",
		Type:             LogTypeConsume,
		CreatedAt:        start + 1200,
		AmountMicrousd:   300,
		CompletionTokens: 30,
		ChannelId:        12,
	})
	insertDashboardRoutingOutcome(t, 201, "req-admin-b", 2, 12, 200, 0, 0, 30, 300, 0.0003, start+1200, 0.00012, 0.00018)
	insertDashboardUsageLog(t, Log{
		PublicID:       "usage-admin-c",
		RequestID:      "req-admin-c",
		UserId:         2,
		Username:       "bob",
		TokenId:        21,
		TokenName:      "bob-batch",
		ModelName:      "deepseek-v3.2",
		Type:           LogTypeConsume,
		CreatedAt:      start + 1800,
		AmountMicrousd: 200,
		PromptTokens:   50,
		ChannelId:      11,
	})
	insertDashboardRoutingOutcome(t, 202, "req-admin-c", 2, 11, 503, 50, 25, 0, 500, 0.0002, start+1800)
	insertDashboardRoutingOutcome(t, 203, "req-admin-error", 2, 12, 500, 0, 0, 0, 900, 0, start+2000)
	insertDashboardUsageLog(t, Log{
		PublicID:       "usage-admin-ignored",
		RequestID:      "req-admin-ignored",
		UserId:         3,
		Username:       "charlie",
		TokenId:        30,
		TokenName:      "charlie-key",
		ModelName:      "deepseek-v3.2",
		Type:           LogTypeError,
		CreatedAt:      start + 2400,
		AmountMicrousd: 999,
		ChannelId:      11,
	})

	usage, err := BuildAdminDashboardUsage(start, end)
	if err != nil {
		t.Fatalf("BuildAdminDashboardUsage returned error: %v", err)
	}
	if usage.Scope != "global" || usage.Role != "admin" {
		t.Fatalf("expected admin global usage, got role=%q scope=%q", usage.Role, usage.Scope)
	}
	if usage.Totals.RequestCount != 3 || usage.Totals.AmountMicrousd != 600 {
		t.Fatalf("unexpected admin totals: %#v", usage.Totals)
	}
	if usage.Totals.CachedInputTokens != 30 || usage.Totals.OutcomeCount != 4 || usage.Totals.ErrorCount != 2 {
		t.Fatalf("unexpected admin telemetry totals: %#v", usage.Totals)
	}
	if usage.Totals.UpstreamMicrousd != 160 || usage.Totals.GrossMicrousd != 240 || usage.Totals.CostSampleCount != 2 {
		t.Fatalf("unexpected admin cost totals: %#v", usage.Totals)
	}
	if usage.Totals.GrossMarginRatio < 0.39 || usage.Totals.GrossMarginRatio > 0.41 {
		t.Fatalf("unexpected gross margin ratio: %#v", usage.Totals)
	}
	if usage.Totals.CacheHitRatio < 0.42 || usage.Totals.CacheHitRatio > 0.43 {
		t.Fatalf("unexpected cache hit ratio: %#v", usage.Totals)
	}
	if usage.Totals.ActiveUsers != 2 || usage.Totals.ActiveKeys != 3 {
		t.Fatalf("unexpected active counts: %#v", usage.Totals)
	}
	if len(usage.ByUser) != 2 || usage.ByUser[0].Name != "bob" || usage.ByUser[0].AmountMicrousd != 500 {
		t.Fatalf("expected bob to lead user ranking, got %#v", usage.ByUser)
	}
	if len(usage.ByModel) != 2 {
		t.Fatalf("expected 2 model rows, got %#v", usage.ByModel)
	}
	modelRow, ok := findDashboardRow(usage.ByModel, "deepseek-v3.2")
	if !ok || modelRow.ErrorCount != 1 {
		t.Fatalf("expected deepseek-v3.2 model error count, got %#v", usage.ByModel)
	}
	userRow, ok := findDashboardRow(usage.ByUser, "bob")
	if !ok || userRow.ErrorCount != 1 {
		t.Fatalf("expected bob user error count, got %#v", usage.ByUser)
	}
	keyRow, ok := findDashboardRow(usage.ByKey, "bob-batch")
	if !ok || keyRow.ErrorCount != 1 {
		t.Fatalf("expected bob-batch key error count, got %#v", usage.ByKey)
	}
	if len(usage.TimeSeries) != 1 || usage.TimeSeries[0].RequestCount != 3 {
		t.Fatalf("unexpected time series: %#v", usage.TimeSeries)
	}
	if usage.TimeSeries[0].CachedInputTokens != 30 {
		t.Fatalf("unexpected time series cache tokens: %#v", usage.TimeSeries)
	}
	if usage.TimeSeries[0].ErrorCount != 1 {
		t.Fatalf("unexpected time series error count: %#v", usage.TimeSeries)
	}
	if usage.TimeSeries[0].UpstreamMicrousd != 160 || usage.TimeSeries[0].GrossMicrousd != 240 || usage.TimeSeries[0].CostSampleCount != 2 {
		t.Fatalf("unexpected time series cost data: %#v", usage.TimeSeries)
	}
	channelRow, ok := findDashboardRow(usage.ByChannel, "reseller-direct")
	if !ok || channelRow.ErrorCount != 1 || channelRow.RequestCount != 2 || channelRow.UpstreamMicrousd != 120 {
		t.Fatalf("expected reseller-direct channel telemetry, got %#v", usage.ByChannel)
	}
	channelRow, ok = findDashboardRow(usage.ByChannel, "deepseek-official")
	if !ok || channelRow.ErrorCount != 1 {
		t.Fatalf("expected deepseek-official channel error count, got %#v", usage.ByChannel)
	}
}

func TestBuildAdminDashboardUserRowsAppliesExactUsernameFilter(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()

	start := int64(1_700_000_000)
	end := start + secondsInDay
	insertDashboardUsageLog(t, Log{
		PublicID:       "usage-filter-alice",
		RequestID:      "req-filter-alice",
		UserId:         1,
		Username:       "alice",
		TokenId:        10,
		TokenName:      "alice-key",
		ModelName:      "deepseek-v3.2",
		Type:           LogTypeConsume,
		CreatedAt:      start + 600,
		AmountMicrousd: 100,
	})
	insertDashboardUsageLog(t, Log{
		PublicID:       "usage-filter-alicia",
		RequestID:      "req-filter-alicia",
		UserId:         2,
		Username:       "alicia",
		TokenId:        20,
		TokenName:      "alicia-key",
		ModelName:      "deepseek-v3.2",
		Type:           LogTypeConsume,
		CreatedAt:      start + 700,
		AmountMicrousd: 200,
	})

	rows, err := BuildAdminDashboardUserRows(DashboardUsageLogFilters{Username: " alice "}, start, end)
	if err != nil {
		t.Fatalf("BuildAdminDashboardUserRows returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "alice" || rows[0].AmountMicrousd != 100 {
		t.Fatalf("expected only the exact alice row, got %#v", rows)
	}
}

func TestBuildAdminDashboardUserRowsPrefersImmutableUserID(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()

	start := int64(1_700_000_000)
	end := start + secondsInDay
	fixtures := []Log{
		{PublicID: "usage-user-id-old", RequestID: "req-user-id-old", UserId: 1, Username: "alice-old", ModelName: "model-a", Type: LogTypeConsume, CreatedAt: start + 1, AmountMicrousd: 100},
		{PublicID: "usage-user-id-new", RequestID: "req-user-id-new", UserId: 1, Username: "alice-new", ModelName: "model-a", Type: LogTypeConsume, CreatedAt: start + 2, AmountMicrousd: 200},
		{PublicID: "usage-user-id-collision", RequestID: "req-user-id-collision", UserId: 2, Username: "alice-new", ModelName: "model-a", Type: LogTypeConsume, CreatedAt: start + 3, AmountMicrousd: 900},
	}
	for _, fixture := range fixtures {
		insertDashboardUsageLog(t, fixture)
	}

	rows, err := BuildAdminDashboardUserRows(DashboardUsageLogFilters{UserID: 1, Username: "alice-new"}, start, end)
	if err != nil {
		t.Fatalf("BuildAdminDashboardUserRows returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != 1 || rows[0].AmountMicrousd != 300 {
		t.Fatalf("expected one immutable-id aggregate with historical usage, got %#v", rows)
	}
}

func TestBuildAdminDashboardUserRowsAppliesGroupAndChannelFilters(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()

	start := int64(1_700_000_000)
	end := start + secondsInDay
	fixtures := []Log{
		{PublicID: "usage-filter-a", RequestID: "req-filter-a", UserId: 1, Username: "alice", ModelName: "model-a", Group: "direct", ChannelId: 7, Type: LogTypeConsume, CreatedAt: start + 1, AmountMicrousd: 100},
		{PublicID: "usage-filter-b", RequestID: "req-filter-b", UserId: 2, Username: "bob", ModelName: "model-a", Group: "direct", ChannelId: 8, Type: LogTypeConsume, CreatedAt: start + 2, AmountMicrousd: 200},
		{PublicID: "usage-filter-c", RequestID: "req-filter-c", UserId: 3, Username: "carol", ModelName: "model-a", Group: "cheap", ChannelId: 7, Type: LogTypeConsume, CreatedAt: start + 3, AmountMicrousd: 300},
	}
	for _, fixture := range fixtures {
		insertDashboardUsageLog(t, fixture)
	}

	rows, err := BuildAdminDashboardUserRows(DashboardUsageLogFilters{Group: "direct", ChannelID: 7}, start, end)
	if err != nil {
		t.Fatalf("BuildAdminDashboardUserRows returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "alice" || rows[0].AmountMicrousd != 100 {
		t.Fatalf("expected only direct channel-7 usage, got %#v", rows)
	}
}

func TestBuildAdminDashboardModelRowsIncludesModelOutsideTopTwenty(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()

	start := int64(1_700_000_000)
	end := start + secondsInDay
	for index := 1; index <= 21; index++ {
		insertDashboardUsageLog(t, Log{
			PublicID:       fmt.Sprintf("usage-model-%02d", index),
			RequestID:      fmt.Sprintf("req-model-%02d", index),
			UserId:         1,
			Username:       "alice",
			TokenId:        10,
			TokenName:      "alice-key",
			ModelName:      fmt.Sprintf("model-%02d", index),
			Type:           LogTypeConsume,
			CreatedAt:      start + int64(index),
			AmountMicrousd: billing.MicroUSD(22 - index),
		})
	}

	dashboard, err := BuildAdminDashboardUsage(start, end)
	if err != nil {
		t.Fatalf("BuildAdminDashboardUsage returned error: %v", err)
	}
	if _, ok := findDashboardRow(dashboard.ByModel, "model-21"); ok {
		t.Fatal("test fixture should place model-21 outside the top-20 dashboard")
	}
	rows, err := BuildAdminDashboardModelRows([]string{"model-21"}, start, end)
	if err != nil {
		t.Fatalf("BuildAdminDashboardModelRows returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "model-21" || rows[0].AmountMicrousd != 1 {
		t.Fatalf("expected exact low-volume model aggregate, got %#v", rows)
	}
}

func TestListDashboardUsageLogsAppliesVisibleTableFilters(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()

	start := int64(1_700_000_000)
	end := start + secondsInDay
	insertDashboardUsageLog(t, Log{
		PublicID:       "export-match",
		RequestID:      "request-match",
		UserId:         1,
		Username:       "alice",
		TokenId:        10,
		TokenName:      "alice-key",
		ModelName:      "model-a",
		Type:           LogTypeConsume,
		CreatedAt:      start + 10,
		AmountMicrousd: 100,
		ChannelId:      11,
	})
	insertDashboardUsageLog(t, Log{
		PublicID:       "export-other-user",
		RequestID:      "request-other-user",
		UserId:         2,
		Username:       "bob",
		TokenId:        20,
		TokenName:      "bob-key",
		ModelName:      "model-a",
		Type:           LogTypeConsume,
		CreatedAt:      start + 20,
		AmountMicrousd: 200,
		ChannelId:      11,
	})
	insertDashboardUsageLog(t, Log{
		PublicID:       "export-other-model",
		RequestID:      "request-other-model",
		UserId:         1,
		Username:       "alice",
		TokenId:        10,
		TokenName:      "alice-key",
		ModelName:      "model-b",
		Type:           LogTypeConsume,
		CreatedAt:      start + 30,
		AmountMicrousd: 300,
		ChannelId:      12,
	})

	logs, err := ListDashboardUsageLogs(0, common.RoleRootUser, DashboardUsageWindow{
		StartTimestamp: start,
		EndTimestamp:   end,
	}, DashboardUsageLogFilters{
		ChannelID: 11,
		LogType:   LogTypeConsume,
		ModelName: "model-a",
		TokenName: "alice-key",
		UserID:    1,
		Username:  "renamed-alice",
	}, 50000)
	if err != nil {
		t.Fatalf("ListDashboardUsageLogs returned error: %v", err)
	}
	if len(logs) != 1 || logs[0].PublicID != "export-match" {
		t.Fatalf("expected export to match the visible filtered row, got %#v", logs)
	}
}

func TestBuildAdminDashboardUsageIncludesEveryChannelForIncidentDetection(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()

	start := int64(1_700_000_000)
	end := start + secondsInDay
	var decisionID int64 = 1_000

	for channelIndex := 1; channelIndex <= 20; channelIndex++ {
		channelID := 100 + channelIndex
		insertDashboardChannel(t, channelID, fmt.Sprintf("healthy-%02d", channelIndex))
		for requestIndex := 0; requestIndex < 4; requestIndex++ {
			requestID := fmt.Sprintf("healthy-%02d-%d", channelIndex, requestIndex)
			insertDashboardRoutingOutcome(
				t,
				decisionID,
				requestID,
				1,
				channelID,
				200,
				0,
				0,
				0,
				100,
				0,
				start+decisionID,
			)
			decisionID++
		}
	}

	const degradedChannelID = 999
	insertDashboardChannel(t, degradedChannelID, "degraded-low-volume")
	for requestIndex := 0; requestIndex < 3; requestIndex++ {
		insertDashboardRoutingOutcome(
			t,
			decisionID,
			fmt.Sprintf("degraded-%d", requestIndex),
			1,
			degradedChannelID,
			503,
			0,
			0,
			0,
			100,
			0,
			start+decisionID,
		)
		decisionID++
	}

	usage, err := BuildAdminDashboardUsage(start, end)
	if err != nil {
		t.Fatalf("BuildAdminDashboardUsage returned error: %v", err)
	}
	if len(usage.ByChannel) != 21 {
		t.Fatalf("expected all 21 channels, got %d", len(usage.ByChannel))
	}
	degraded, ok := findDashboardRow(usage.ByChannel, "degraded-low-volume")
	if !ok || degraded.RequestCount != 3 || degraded.ErrorCount != 3 {
		t.Fatalf("expected degraded low-volume channel telemetry, got %#v", usage.ByChannel)
	}
}

func TestBuildDashboardUsageFromRollupIncludesEveryAdminChannel(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()

	start := dashboardRollupHour(1_700_000_000)
	if err := coreDB.Create(&DashboardUsageHourlyTotal{
		BucketHour:  start,
		ScopeUserID: dashboardRollupScopeGlobal,
	}).Error; err != nil {
		t.Fatalf("failed to create rollup total: %v", err)
	}
	for channelIndex := 1; channelIndex <= 21; channelIndex++ {
		row := DashboardUsageHourlyDimension{
			BucketHour:    start,
			ScopeUserID:   dashboardRollupScopeGlobal,
			DimensionType: dashboardDimensionChannel,
			DimensionID:   strconv.Itoa(channelIndex),
			DimensionName: fmt.Sprintf("channel-%02d", channelIndex),
			RequestCount:  int64(channelIndex),
		}
		if err := coreDB.Create(&row).Error; err != nil {
			t.Fatalf("failed to create channel rollup %d: %v", channelIndex, err)
		}
	}

	usage, ok, err := BuildDashboardUsageFromRollupForRole(
		0,
		common.RoleRootUser,
		start,
		start+dashboardRollupHourSeconds-1,
	)
	if err != nil {
		t.Fatalf("BuildDashboardUsageFromRollupForRole returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected rollup coverage to be ready")
	}
	if len(usage.ByChannel) != 21 {
		t.Fatalf("expected all 21 rollup channels, got %d", len(usage.ByChannel))
	}
}

func TestDashboardPartialHoursPreserveExactUsageWindow(t *testing.T) {
	defer setupDashboardUsageTestDB(t)()
	t.Setenv("DASHBOARD_ROLLUP_ENABLED", "true")
	start := dashboardRollupHour(1_700_000_000)
	// Both bucket boundaries and the inclusive end second matter. The
	// neighboring users/keys must not affect totals, rankings or active counts.
	for i, offset := range []int64{0, 1799, 1800, 3600, 5400, 5401, 7199} {
		user, token, model := 9, 90, "outside"
		if offset >= 1800 && offset <= 5400 {
			user, token, model = 7, 70, "inside"
		}
		insertDashboardUsageLog(t, Log{PublicID: fmt.Sprint("exact-", i), RequestID: fmt.Sprint("exact-", i), UserId: user, TokenId: token, ModelName: model, Type: LogTypeConsume, CreatedAt: start + offset, AmountMicrousd: 10})
	}
	for _, hour := range []int64{start, start + 3600} {
		if err := telemetryDB.Create(&DashboardUsageHourlyTotal{ScopeUserID: 0, BucketHour: hour, RequestCount: 99, AmountMicrousd: 999}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range []int{common.RoleRootUser, common.RoleCommonUser} {
		got, err := BuildDashboardUsageForRole(7, role, start+1800, start+5400)
		if err != nil || got.Totals.RequestCount != 3 || got.Totals.AmountMicrousd != 30 || got.Totals.ActiveKeys != 1 {
			t.Fatalf("role=%d totals=%+v err=%v", role, got.Totals, err)
		}
		if role == common.RoleRootUser && got.Totals.ActiveUsers != 1 {
			t.Fatalf("outside user counted: %+v", got.Totals)
		}
		if len(got.ByModel) != 1 || got.ByModel[0].Name != "inside" || got.ByModel[0].RequestCount != 3 {
			t.Fatalf("rankings escaped window: %+v", got.ByModel)
		}
		csv, err := BuildDashboardUsageCSV(got)
		if err != nil || strings.Contains(string(csv), "outside") || !strings.Contains(string(csv), "inside") {
			t.Fatalf("export escaped window: %s %v", csv, err)
		}
	}
	// An end on the hour includes only that second, not the following hour.
	if _, ok, err := BuildDashboardUsageFromRollupForRole(7, common.RoleRootUser, start, start+3600); err != nil || ok {
		t.Fatalf("inclusive hour boundary rounded up: served=%v err=%v", ok, err)
	}
}

func TestDashboardRollupBuildAdminUsageUsesHourlyTables(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()

	start := dashboardRollupHour(1_700_000_000)
	insertDashboardChannel(t, 11, "deepseek-official")
	insertDashboardChannel(t, 12, "reseller-direct")
	insertDashboardUsageLog(t, Log{
		PublicID:       "rollup-admin-a",
		RequestID:      "rollup-req-a",
		UserId:         1,
		Username:       "alice",
		TokenId:        10,
		TokenName:      "alice-key",
		ModelName:      "deepseek-v3.2",
		Type:           LogTypeConsume,
		CreatedAt:      start + 600,
		AmountMicrousd: 100,
		PromptTokens:   20,
		ChannelId:      11,
	})
	insertDashboardRoutingOutcome(t, 300, "rollup-req-a", 1, 11, 200, 20, 5, 0, 120, 0.0001, start+600, 0.00004, 0.00006)
	insertDashboardUsageLog(t, Log{
		PublicID:         "rollup-admin-b",
		RequestID:        "rollup-req-b",
		UserId:           1,
		Username:         "alice",
		TokenId:          11,
		TokenName:        "alice-batch",
		ModelName:        "deepseek-v4-flash",
		Type:             LogTypeConsume,
		CreatedAt:        start + dashboardRollupHourSeconds + 60,
		AmountMicrousd:   300,
		CompletionTokens: 30,
		ChannelId:        12,
	})
	insertDashboardRoutingOutcome(t, 301, "rollup-req-b", 1, 12, 503, 0, 0, 30, 300, 0.0003, start+dashboardRollupHourSeconds+60, 0.0002, 0.0001)

	if err := RefreshDashboardUsageRollups(context.Background(), time.Unix(start, 0), time.Unix(start+2*dashboardRollupHourSeconds, 0)); err != nil {
		t.Fatalf("RefreshDashboardUsageRollups returned error: %v", err)
	}

	usage, ok, err := BuildDashboardUsageFromRollupForRole(0, common.RoleRootUser, start, start+2*dashboardRollupHourSeconds-1)
	if err != nil {
		t.Fatalf("BuildDashboardUsageFromRollupForRole returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected rollup coverage to be ready")
	}
	if usage.Totals.RequestCount != 2 || usage.Totals.AmountMicrousd != 400 {
		t.Fatalf("unexpected rollup totals: %#v", usage.Totals)
	}
	if usage.Totals.OutcomeCount != 2 || usage.Totals.ErrorCount != 1 {
		t.Fatalf("unexpected rollup telemetry totals: %#v", usage.Totals)
	}
	if usage.Totals.UpstreamMicrousd != 240 || usage.Totals.GrossMicrousd != 160 || usage.Totals.CostSampleCount != 2 {
		t.Fatalf("unexpected rollup cost totals: %#v", usage.Totals)
	}
	if usage.Totals.ActiveUsers != 1 || usage.Totals.ActiveKeys != 2 {
		t.Fatalf("expected distinct active users/keys across hours, got %#v", usage.Totals)
	}
	if usage.Totals.P95LatencyMs != 500 {
		t.Fatalf("expected bucketed p95 latency upper bound 500ms, got %.2f", usage.Totals.P95LatencyMs)
	}
	userRow, ok := findDashboardRow(usage.ByUser, "alice")
	if !ok || userRow.RequestCount != 2 || userRow.ErrorCount != 1 {
		t.Fatalf("expected alice rollup row, got %#v", usage.ByUser)
	}
	channelRow, ok := findDashboardRow(usage.ByChannel, "reseller-direct")
	if !ok || channelRow.RequestCount != 1 || channelRow.ErrorCount != 1 || channelRow.UpstreamMicrousd != 200 {
		t.Fatalf("expected reseller rollup channel row, got %#v", usage.ByChannel)
	}
	if len(usage.TimeSeries) != 1 || usage.TimeSeries[0].RequestCount != 2 {
		t.Fatalf("unexpected rollup time series: %#v", usage.TimeSeries)
	}
	if usage.TimeSeries[0].UpstreamMicrousd != 240 || usage.TimeSeries[0].GrossMicrousd != 160 || usage.TimeSeries[0].CostSampleCount != 2 {
		t.Fatalf("unexpected rollup time series cost data: %#v", usage.TimeSeries)
	}

	// The usage log's user tab reads the same hourly rows for whole hours, and
	// the raw logs once a filter or an exact second is asked for.
	users, err := BuildAdminDashboardUsersContext(context.Background(), DashboardUsageLogFilters{}, start, start+2*dashboardRollupHourSeconds-1)
	if err != nil || len(users) != 1 || users[0].Name != "alice" || users[0].RequestCount != 2 || users[0].ErrorCount != 1 || users[0].AmountMicrousd != 400 {
		t.Fatalf("whole-hour user rows=%+v err=%v", users, err)
	}
	if err := TelemetryDB().Exec("UPDATE dashboard_usage_hourly_dimensions SET request_count = 99 WHERE dimension_type = 'user'").Error; err != nil {
		t.Fatal(err)
	}
	if users, err = BuildAdminDashboardUsersContext(context.Background(), DashboardUsageLogFilters{}, start, start+2*dashboardRollupHourSeconds-1); err != nil || users[0].RequestCount != 198 {
		t.Fatalf("whole hours did not read the rollup: %+v %v", users, err)
	}
	for _, raw := range []struct {
		filters DashboardUsageLogFilters
		end     int64
	}{{DashboardUsageLogFilters{}, start + 2*dashboardRollupHourSeconds}, {DashboardUsageLogFilters{Username: "alice"}, start + 2*dashboardRollupHourSeconds - 1}} {
		if users, err = BuildAdminDashboardUsersContext(context.Background(), raw.filters, start, raw.end); err != nil || len(users) != 1 || users[0].RequestCount != 2 {
			t.Fatalf("raw user rows %+v: %+v %v", raw, users, err)
		}
	}

	userUsage, ok, err := BuildDashboardUsageFromRollupForRole(1, common.RoleCommonUser, start, start+2*dashboardRollupHourSeconds-1)
	if err != nil || !ok {
		t.Fatalf("BuildDashboardUsageFromRollupForRole user scope ok=%v err=%v", ok, err)
	}
	if userUsage.Totals.ActiveKeys != 2 {
		t.Fatalf("expected two active keys for rollup user scope, got %#v", userUsage.Totals)
	}
}

func TestDashboardRollupRequiresCompleteGlobalCoverage(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()

	start := dashboardRollupHour(1_700_000_000)
	if err := RefreshDashboardUsageRollupHour(context.Background(), start); err != nil {
		t.Fatalf("RefreshDashboardUsageRollupHour returned error: %v", err)
	}
	_, ok, err := BuildDashboardUsageFromRollupForRole(0, common.RoleRootUser, start, start+2*dashboardRollupHourSeconds-1)
	if err != nil {
		t.Fatalf("BuildDashboardUsageFromRollupForRole returned error: %v", err)
	}
	if ok {
		t.Fatal("expected rollup to be unavailable when the requested range has a missing hour")
	}
}

// A quiet hour gets an empty platform row, so a window across it is still
// answered from the rollup; hours before the rollup began stay uncovered.
func TestDashboardRollupQuietHoursKeepCoverage(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()
	start := dashboardRollupHour(time.Now().Add(-10 * time.Hour).Unix())
	for _, hour := range []int64{start, start + 3*dashboardRollupHourSeconds} {
		if err := TelemetryDB().Create(&DashboardUsageHourlyTotal{BucketHour: hour, ScopeUserID: dashboardRollupScopeGlobal, RequestCount: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	window := normalizeDashboardUsageWindow(start, start+4*dashboardRollupHourSeconds-1)
	if ready, err := dashboardRollupCoverageReady(window); err != nil || ready {
		t.Fatalf("quiet hours counted as covered before the fill: %v %v", ready, err)
	}
	if err := EnsureDashboardRollupHours(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if ready, err := dashboardRollupCoverageReady(window); err != nil || !ready {
		t.Fatalf("quiet hours still uncovered: %v %v", ready, err)
	}
	totals, err := dashboardRollupTotals(dashboardRollupScopeGlobal, start, start+4*dashboardRollupHourSeconds)
	if err != nil || totals.RequestCount != 2 || totals.P95TTFTMs != nil {
		t.Fatalf("empty hours changed totals: %+v %v", totals, err)
	}
	if ready, err := dashboardRollupCoverageReady(normalizeDashboardUsageWindow(start-dashboardRollupHourSeconds, start+dashboardRollupHourSeconds-1)); err != nil || ready {
		t.Fatalf("an hour before the rollup began was filled: %v %v", ready, err)
	}
	if err := EnsureDashboardRollupHours(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	// The open hour has no row until its first request and is not required.
	open := dashboardRollupHour(time.Now().Unix())
	if err := TelemetryDB().Where("bucket_hour = ?", open).Delete(&DashboardUsageHourlyTotal{}).Error; err != nil {
		t.Fatal(err)
	}
	if ready, err := dashboardRollupCoverageReady(normalizeDashboardUsageWindow(start, open+dashboardRollupHourSeconds-1)); err != nil || !ready {
		t.Fatalf("a window ending in the quiet open hour went raw: %v %v", ready, err)
	}
}

func TestDashboardTTFTExcludesUnmeasuredAndNonTextStreams(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()
	for _, column := range []string{"video_seconds_charged real"} {
		if err := coreDB.Exec("ALTER TABLE routing_outcome ADD COLUMN " + column).Error; err != nil {
			t.Fatal(err)
		}
	}
	start := int64(1700000000)
	for i := 1; i <= 5; i++ {
		insertDashboardRoutingOutcome(t, int64(100+i), fmt.Sprintf("ttft-%d", i), i, 11, 200, 10, 0, 10, 9000, 0, start+1)
	}
	updates := []string{
		"ttft_ms=150, was_streaming=1",
		"ttft_ms=600, was_streaming=0",
		"ttft_ms=800, was_streaming=1, audio_output_tokens=20",
		"ttft_ms=900, was_streaming=1, image_output_tokens=1",
		"ttft_ms=NULL, was_streaming=1",
	}
	for i, update := range updates {
		if err := coreDB.Exec("UPDATE routing_outcome SET "+update+" WHERE request_id = ?", fmt.Sprintf("ttft-%d", i+1)).Error; err != nil {
			t.Fatal(err)
		}
	}
	var totals DashboardUsageTotals
	if err := scanDashboardTTFTStats(0, common.RoleRootUser, normalizeDashboardUsageWindow(start, start+60), &totals); err != nil {
		t.Fatal(err)
	}
	if totals.TTFTSampleCount != 1 || totals.P95TTFTMs == nil || *totals.P95TTFTMs != 150 {
		t.Fatalf("wrong text TTFT sample: %#v", totals)
	}
	var other DashboardUsageTotals
	if err := scanDashboardTTFTStats(2, common.RoleCommonUser, normalizeDashboardUsageWindow(start, start+60), &other); err != nil {
		t.Fatal(err)
	}
	if other.TTFTSampleCount != 0 || other.P95TTFTMs != nil {
		t.Fatalf("another user's measurement leaked: %#v", other)
	}
}

func TestDashboardTTFTPercentileInterpolatesInsteadOfUsingMaximum(t *testing.T) {
	cleanup := setupDashboardUsageTestDB(t)
	defer cleanup()
	for _, column := range []string{"video_seconds_charged real"} {
		if err := coreDB.Exec("ALTER TABLE routing_outcome ADD COLUMN " + column).Error; err != nil {
			t.Fatal(err)
		}
	}
	start := int64(1700000000)
	for i := 1; i <= 20; i++ {
		id := fmt.Sprintf("percentile-%d", i)
		insertDashboardRoutingOutcome(t, int64(200+i), id, 1, 11, 200, 10, 0, 10, 9000, 0, start+1)
		if err := coreDB.Exec("UPDATE routing_outcome SET ttft_ms = ?, was_streaming = 1 WHERE request_id = ?", i*100, id).Error; err != nil {
			t.Fatal(err)
		}
	}
	var totals DashboardUsageTotals
	if err := scanDashboardTTFTStats(1, common.RoleCommonUser, normalizeDashboardUsageWindow(start, start+60), &totals); err != nil {
		t.Fatal(err)
	}
	if totals.TTFTSampleCount != 20 || totals.P95TTFTMs == nil || *totals.P95TTFTMs != 1905 {
		t.Fatalf("expected interpolated P95 1905, got %#v", totals)
	}
}
