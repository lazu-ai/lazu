package model

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

// Slices answer from latency histograms; everything else must match the raw
// view-join queries exactly. A bucket midpoint is within 9.6% of its values,
// and PERCENTILE_CONT interpolates between neighbours, so allow 20% + 10 ms.
func requireUsageSliceLatency(t *testing.T, label string, want, got []DashboardUsageDimensionRow) {
	t.Helper()
	if len(want) != len(got) {
		return // the DeepEqual below reports the difference
	}
	for i := range want {
		if math.Abs(want[i].P95LatencyMs-got[i].P95LatencyMs) > 0.2*want[i].P95LatencyMs+10 {
			t.Fatalf("%s row %d p95 %v, raw %v", label, i, got[i].P95LatencyMs, want[i].P95LatencyMs)
		}
		if math.Abs(want[i].AvgLatencyMs-got[i].AvgLatencyMs) > 1e-9*math.Max(1, want[i].AvgLatencyMs) {
			t.Fatalf("%s row %d avg %v, raw %v", label, i, got[i].AvgLatencyMs, want[i].AvgLatencyMs)
		}
		got[i].P95LatencyMs, got[i].AvgLatencyMs = want[i].P95LatencyMs, want[i].AvgLatencyMs
	}
}

func requireSameUsage(t *testing.T, want, got interface{}) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		a, _ := json.Marshal(want)
		b, _ := json.Marshal(got)
		t.Fatalf("slices differ from the raw read\nraw=%s\nslices=%s", a, b)
	}
}

// legacyAdminDashboardUserRows is the raw filtered user tab before slices.
func legacyAdminDashboardUserRows(filters DashboardUsageLogFilters, start, end int64) ([]DashboardUsageDimensionRow, error) {
	window := normalizeDashboardUsageWindow(start, end)
	userNameExpr := "COALESCE(NULLIF(logs.username, ''), 'unknown')"
	tx := dashboardUsageBaseQuery(window)
	limit := 50
	if filters.UserID > 0 {
		tx = tx.Where("logs.user_id = ?", filters.UserID)
		limit = 1
	} else if filters.Username != "" {
		tx = tx.Where("logs.username = ?", filters.Username)
		limit = 1
	}
	if filters.Group != "" {
		tx = tx.Where(`logs."group" = ?`, filters.Group)
	}
	if filters.ChannelID > 0 {
		tx = tx.Where("logs.channel_id = ?", filters.ChannelID)
	}
	groupExpr := "logs.user_id, " + userNameExpr
	if filters.UserID > 0 {
		userNameExpr = "MAX(" + userNameExpr + ")"
		groupExpr = "logs.user_id"
	}
	return legacyScanDashboardUsageRows(tx, "logs.user_id", userNameExpr, groupExpr, limit)
}

func legacyOverviewClientRows(start, end int64) ([]overviewClientRow, error) {
	var rows []overviewClientRow
	err := TelemetryDB().Model(&Log{}).
		Select("COALESCE(NULLIF(client_framework, ''), 'unknown') AS client, COUNT(DISTINCT user_id) AS users, COUNT(*) AS reqs, "+
			"COALESCE(SUM(prompt_tokens + completion_tokens), 0) AS tokens, COALESCE(SUM(amount_microusd), 0) AS amount").
		Where("type = ? AND created_at >= ? AND created_at < ?", LogTypeConsume, start, end).
		Group("COALESCE(NULLIF(client_framework, ''), 'unknown')").Scan(&rows).Error
	sort.Slice(rows, func(i, j int) bool { return rows[i].Client < rows[j].Client })
	return rows, err
}

func TestPostgresUsageSlicesMatchRawReads(t *testing.T) {
	db, access, start, end := dashboardProjectionPostgresFixture(t)
	// Slices fold each log with the outcomes the raw reads join to it, so a
	// request with two outcome copies counts twice in both, but the client mix
	// reads logs alone. Production has no such request (2026-10-16: no request
	// id on two log rows' outcomes); drop the fixture's.
	if err := db.Exec("DELETE FROM logs WHERE request_id IN ('duplicate-json-physical', 'mismatched-json-id')").Error; err != nil {
		t.Fatal(err)
	}
	// Spread the fixture over the facts the slices key on.
	if err := db.Exec(`UPDATE logs SET "group" = CASE WHEN user_id % 2 = 0 THEN 'cheap' ELSE 'direct' END,
		channel_id = user_id % 3 + 1,
		client_framework = CASE user_id % 3 WHEN 0 THEN 'claude-code' WHEN 1 THEN '' ELSE 'codex' END`).Error; err != nil {
		t.Fatal(err)
	}
	// Rejected requests count toward the project's error total only.
	for i := 0; i < 4; i++ {
		row := Log{PublicID: fmt.Sprintf("log_rejected_%d", i), RequestID: fmt.Sprintf("rejected-%d", i), RequestTS: (start + 40 + int64(i)) * 1000,
			CreatedAt: start + 40 + int64(i)*1500, Type: LogTypeError, UserId: 2 + i%2, TokenId: 5, ModelName: "model-01", ProjectID: 42, PayerUserID: 1}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	first := dashboardRollupHour(start) - 3*3600
	if err := rebuildUsageSlices(context.Background(), db, time.Unix(first, 0), time.Unix(dashboardRollupHour(end)+4*3600, 0)); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&DashboardUsageHourlySlice{}).Count(&count).Error; err != nil || count == 0 {
		t.Fatalf("no slices built: %d %v", count, err)
	}
	hour := dashboardRollupHour(start)
	windows := []struct {
		name     string
		from, to int64
	}{
		{"partial-hours-read-raw", start, end},
		{"whole-hours-read-slices", hour - 3600, hour + 2*3600 - 1},
		{"mixed", start + 300, hour + 3*3600 - 1},
		{"empty", end + 100000, end + 100100},
	}
	accesses := map[string]ProjectLogAccess{"manager": access}
	member := access
	member.Viewer.Role, member.Viewer.UserID = ProjectRoleMember, 2
	accesses["member"] = member
	actor := access
	actorID := 3
	actor.ActorID = &actorID
	accesses["actor"] = actor
	token := access
	token.TokenID = 5
	accesses["token"] = token
	stranger := access
	stranger.Viewer.ProjectID = 7
	accesses["foreign-viewer"] = stranger
	for _, window := range windows {
		t.Run(window.name, func(t *testing.T) {
			for name, scoped := range accesses {
				want, err := legacyBuildProjectDashboardUsage(scoped, window.from, window.to)
				if err != nil {
					t.Fatal(err)
				}
				got, err := BuildProjectDashboardUsageContext(context.Background(), scoped, window.from, window.to)
				if err != nil {
					t.Fatal(err)
				}
				if len(want.ByUser) == 0 && len(got.ByUser) == 0 {
					want.ByUser, got.ByUser = nil, nil
				}
				if want.TimeSeries == nil {
					want.TimeSeries = []DashboardUsageTimeRow{}
				}
				requireUsageSliceLatency(t, name+" by model", want.ByModel, got.ByModel)
				requireUsageSliceLatency(t, name+" by key", want.ByKey, got.ByKey)
				requireUsageSliceLatency(t, name+" by user", want.ByUser, got.ByUser)
				requireSameUsage(t, want, got)
				if name == "manager" && window.name != "empty" && (got.Totals.RequestCount == 0 || got.Totals.ErrorCount == 0 && window.name != "mixed") {
					t.Fatalf("%s window read nothing: %+v", window.name, got.Totals)
				}
			}
			for _, filters := range []DashboardUsageLogFilters{
				{UserID: 2}, {Username: "user-03"}, {Group: "cheap"}, {ChannelID: 2}, {Group: "direct", ChannelID: 3}, {UserID: 99999},
			} {
				want, err := legacyAdminDashboardUserRows(filters, window.from, window.to)
				if err != nil {
					t.Fatal(err)
				}
				got, err := BuildAdminDashboardUserRowsContext(context.Background(), filters, window.from, window.to)
				if err != nil {
					t.Fatal(err)
				}
				if len(want) == 0 && len(got) == 0 {
					continue
				}
				requireUsageSliceLatency(t, fmt.Sprintf("users %+v", filters), want, got)
				requireSameUsage(t, want, got)
			}
			want, err := legacyOverviewClientRows(window.from, window.to+1)
			if err != nil {
				t.Fatal(err)
			}
			got, err := overviewClientRowsFromSlicesContext(context.Background(), window.from, window.to+1)
			if err != nil {
				t.Fatal(err)
			}
			sort.Slice(got, func(i, j int) bool { return got[i].Client < got[j].Client })
			if len(want) == 0 && len(got) == 0 {
				return
			}
			requireSameUsage(t, want, got)
		})
	}
}

// The batched consumer and a rebuild from the log must produce the same
// slices, and a replayed batch must not count twice.
func TestPostgresUsageSliceWriterMatchesRebuild(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	events := []relayResultEvent{}
	for i := 0; i < 6; i++ {
		event := telemetryFixture(common.GenerateRequestID())
		log := *event.ConsumeLog
		log.ClientFramework, log.Group = []string{"codex", "claude-code"}[i%2], "direct"
		cached, upstream, gross, status := 3*i, 0.0001*float64(i+1), 0.00005, 200+300*(i%2)
		outcome := *event.RoutingOutcome
		outcome.CachedInputTokens, outcome.UpstreamCostUSD, outcome.GrossMarginUSD, outcome.StatusCode = &cached, &upstream, &gross, &status
		if i == 5 {
			log.Type = LogTypeError
			log.AmountMicrousd = billing.MicroUSD(0)
		}
		event.ConsumeLog, event.RoutingOutcome = &log, &outcome
		events = append(events, event)
	}
	for replay := 0; replay < 2; replay++ {
		if err := persistRelayResultBatch(events); err != nil {
			t.Fatal(err)
		}
	}
	read := func() []DashboardUsageHourlySlice {
		var rows []DashboardUsageHourlySlice
		if err := db.Order("log_type, client").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			rows[i].ID = 0
			rows[i].LatencyBuckets = strings.ReplaceAll(rows[i].LatencyBuckets, " ", "")
		}
		return rows
	}
	written := read()
	if len(written) != 3 || written[0].RequestCount+written[1].RequestCount != 5 || written[2].LogType != LogTypeError {
		t.Fatalf("writer slices: %+v", written)
	}
	now := time.Now().UTC()
	if err := rebuildUsageSlices(context.Background(), db, now.Add(-2*time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rebuilt := read()
	for i := range rebuilt {
		// The rebuild sums dollars in SQL row order; compare to the microdollar.
		if i < len(written) && math.Abs(rebuilt[i].UpstreamCostUSD-written[i].UpstreamCostUSD) < 1e-12 {
			rebuilt[i].UpstreamCostUSD = written[i].UpstreamCostUSD
		}
		if i < len(written) && math.Abs(rebuilt[i].GrossMarginUSD-written[i].GrossMarginUSD) < 1e-12 {
			rebuilt[i].GrossMarginUSD = written[i].GrossMarginUSD
		}
	}
	requireSameUsage(t, written, rebuilt)
}

// Reading a partial hour must stay on the hour's own logs.
func TestPostgresUsageSliceEdgesStayBounded(t *testing.T) {
	db, access, start, _ := dashboardProjectionPostgresFixture(t)
	if err := rebuildUsageSlices(context.Background(), db, time.Unix(dashboardRollupHour(start)-3*3600, 0), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var statements []string
	record := func(tx *gorm.DB) { statements = append(statements, tx.Statement.SQL.String()) }
	if err := db.Callback().Query().After("gorm:query").Register("test:slice-sql", record); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Row().After("gorm:row").Register("test:slice-row-sql", record); err != nil {
		t.Fatal(err)
	}
	hour := dashboardRollupHour(start)
	if _, err := BuildProjectDashboardUsageContext(context.Background(), access, hour-3600, hour+3*3600-1); err != nil {
		t.Fatal(err)
	}
	if len(statements) == 0 {
		t.Fatal("no statement observed")
	}
	for _, sql := range statements {
		if strings.Contains(sql, "FROM logs") || strings.Contains(sql, `FROM "logs"`) {
			t.Fatalf("a whole-hour window read the log: %s", sql)
		}
	}
}

// The online prefill builds closed hours and records where it stopped; the
// stopped-writer step builds the rest once.
func TestPostgresSchema34PrefillThenStep(t *testing.T) {
	db, _, start, _ := dashboardProjectionPostgresFixture(t)
	// A request in the current hour, which only the schema step may build.
	if err := db.Create(&Log{PublicID: "log_live", RequestID: "live", RequestTS: time.Now().UnixMilli(), CreatedAt: time.Now().Unix(), Type: LogTypeConsume, UserId: 1, ModelName: "live", AmountMicrousd: 5, ProjectID: 42}).Error; err != nil {
		t.Fatal(err)
	}
	count := func() int64 {
		var n int64
		if err := db.Model(&DashboardUsageHourlySlice{}).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := PrefillSchema34(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var marker SchemaVersion
	if err := db.Where("scope = ?", usageSlicesPrefillMarker).First(&marker).Error; err != nil {
		t.Fatal(err)
	}
	var live int64
	if err := db.Model(&DashboardUsageHourlySlice{}).Where("model_name = ?", "live").Count(&live).Error; err != nil || live != 0 {
		t.Fatalf("prefill built a young hour: %d %v", live, err)
	}
	prefilled := count()
	for i := 0; i < 2; i++ {
		if err := migrateSchema34Telemetry(db); err != nil {
			t.Fatal(err)
		}
	}
	stepped := count()
	if err := db.Model(&DashboardUsageHourlySlice{}).Where("model_name = ?", "live").Count(&live).Error; err != nil || live != 1 {
		t.Fatalf("schema step left the current hour: %d %v", live, err)
	}
	if stepped < prefilled+1 {
		t.Fatalf("schema step lost prefilled slices: %d -> %d", prefilled, stepped)
	}
	// The result equals one rebuild of everything.
	if err := rebuildUsageSlices(context.Background(), db, time.Unix(dashboardRollupHour(start)-6*3600, 0), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if rebuilt := count(); rebuilt != stepped {
		t.Fatalf("prefill + step built %d slices, a full rebuild %d", stepped, rebuilt)
	}
}

// A media job's charge is written outside the batched consumer; it must land
// in the same slice a rebuild would give it, outcome join included.
func TestPostgresUsageSliceDirectLogMatchesRebuild(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	write := func(i int) {
		t.Helper()
		requestID := common.GenerateRequestID()
		upstream, status := 0.002*float64(i+1), 200
		outcome := RoutingOutcome{RequestID: requestID, OutcomeAt: now.Add(-time.Duration(i) * time.Minute), UpstreamCostUSD: &upstream, StatusCode: &status}
		if err := db.Table("routing_outcome_legacy").Create(&outcome).Error; err != nil {
			t.Fatal(err)
		}
		log := &Log{RequestID: requestID, Type: LogTypeConsume, CreatedAt: now.Add(-time.Duration(i) * time.Minute).Unix(), UserId: 9, ProjectID: 3, TokenId: 4, ModelName: "video-model", AmountMicrousd: billing.MicroUSD(1000 * (i + 1))}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(log).Error; err != nil {
				return err
			}
			return recordUsageSlicesForLogTx(tx, log)
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(0)
	write(1)
	read := func() []DashboardUsageHourlySlice {
		var rows []DashboardUsageHourlySlice
		if err := db.Order("bucket_hour").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			rows[i].ID = 0
			rows[i].LatencyBuckets = strings.ReplaceAll(rows[i].LatencyBuckets, " ", "")
		}
		return rows
	}
	written := read()
	var requests, amount int64
	for _, row := range written {
		requests += row.RequestCount
		amount += row.AmountMicrousd
	}
	if requests != 2 || amount != 3000 {
		t.Fatalf("direct slices: %+v", written)
	}
	if err := rebuildUsageSlices(context.Background(), db, now.Add(-2*time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rebuilt := read()
	for i := range rebuilt {
		if i < len(written) && math.Abs(rebuilt[i].UpstreamCostUSD-written[i].UpstreamCostUSD) < 1e-12 {
			rebuilt[i].UpstreamCostUSD = written[i].UpstreamCostUSD
		}
	}
	requireSameUsage(t, written, rebuilt)
}
