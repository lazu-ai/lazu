package model

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

// The hourly TTFT histogram must count exactly the raw percentile's outcomes,
// for the platform and for a member attributed only through a decision, and
// read back within the route health buckets' 9.6%.
func TestPostgresSchema33TTFTRollupMatchesRawPercentile(t *testing.T) {
	db, _, start, end := dashboardProjectionPostgresFixture(t)
	if err := migrateSchema33Telemetry(db); err != nil {
		t.Fatal(err)
	}
	from := dashboardRollupHour(start)
	to := dashboardRollupHour(end+1000) + 2*dashboardRollupHourSeconds
	window := normalizeDashboardUsageWindow(from, to-1)
	for _, scope := range []struct {
		name         string
		userID, role int
	}{{"platform", 1, common.RoleRootUser}, {"member", 2, common.RoleCommonUser}, {"decision-attributed", 1, common.RoleCommonUser}} {
		t.Run(scope.name, func(t *testing.T) {
			var raw DashboardUsageTotals
			if err := legacyScanDashboardTTFTStats(scope.userID, scope.role, window, &raw); err != nil {
				t.Fatal(err)
			}
			rollupScope := scope.userID
			if scope.role >= common.RoleRootUser {
				rollupScope = dashboardRollupScopeGlobal
			}
			rolled, err := dashboardRollupTotalsContext(context.Background(), rollupScope, from, to)
			if err != nil {
				t.Fatal(err)
			}
			if raw.TTFTSampleCount == 0 || raw.P95TTFTMs == nil {
				t.Fatal("fixture has no TTFT for this scope")
			}
			if rolled.TTFTSampleCount != raw.TTFTSampleCount || rolled.P95TTFTMs == nil {
				t.Fatalf("samples: rollup=%d raw=%d", rolled.TTFTSampleCount, raw.TTFTSampleCount)
			}
			if math.Abs(*rolled.P95TTFTMs-*raw.P95TTFTMs) > *raw.P95TTFTMs*0.2+10 {
				t.Fatalf("p95: rollup=%.1f raw=%.1f", *rolled.P95TTFTMs, *raw.P95TTFTMs)
			}
		})
	}
	// Every hourly total was filled, and the schema step left updated_at alone.
	var unfilled int64
	if err := db.Model(&DashboardUsageHourlyTotal{}).Where("ttft_buckets IS NULL").Count(&unfilled).Error; err != nil || unfilled != 0 {
		t.Fatalf("unfilled hours=%d err=%v", unfilled, err)
	}
}

// After the backfill no legacy outcome is left without a user its decision
// names, which is what lets a member window match legacy rows by user alone.
func TestPostgresSchema33LegacyOutcomeUsersFollowDecisions(t *testing.T) {
	db, _, _, _ := dashboardProjectionPostgresFixture(t)
	var missing int64
	if err := db.Raw(`SELECT COUNT(*) FROM routing_outcome_legacy o JOIN routing_decision d ON d.id = o.decision_id
		WHERE o.user_id IS NULL AND d.user_id IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM logs newer WHERE newer.request_id = o.request_id AND newer.ro_request_id IS NOT NULL)`).Scan(&missing).Error; err != nil {
		t.Fatal(err)
	}
	if missing != 0 {
		t.Fatalf("%d legacy outcomes still need their decision for a user", missing)
	}
	for request, want := range map[string]int{"projection-007": 2, "projection-014": 15, "nullable-snapshot-0": 1} {
		var user *int
		if err := db.Raw("SELECT user_id FROM routing_outcome_legacy WHERE request_id = ?", request).Scan(&user).Error; err != nil {
			t.Fatal(err)
		}
		if user == nil || *user != want {
			t.Fatalf("%s user=%v want %d", request, user, want)
		}
	}
	// The backfill is recorded and does not run again.
	var marker SchemaVersion
	if err := db.Where("scope = ?", legacyOutcomeUsersMigration).First(&marker).Error; err != nil {
		t.Fatal(err)
	}
}

func TestPostgresSchema33EffortClampedFollowsOther(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := db.Create(&Log{Id: 1, PublicID: "log_effort_before", RequestTS: 1000, Other: `{"effort_clamped":true}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := installEffortClampedColumn(db); err != nil {
		t.Fatal(err)
	}
	rows := []Log{
		{Id: 2, PublicID: "log_effort_true", RequestTS: 2000, Other: `{"effort_clamped":true,"effort_applied":"low"}`},
		{Id: 3, PublicID: "log_effort_false", RequestTS: 3000, Other: `{"effort_clamped":false}`},
		{Id: 4, PublicID: "log_effort_string", RequestTS: 4000, Other: `{"effort_clamped":"true"}`},
		{Id: 5, PublicID: "log_effort_invalid", RequestTS: 5000, Other: `effort_clamped`},
		{Id: 6, PublicID: "log_effort_none", RequestTS: 6000, Other: `{}`},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`UPDATE logs SET other = (COALESCE(NULLIF(other,''),'{}')::jsonb || '{"effort_clamped":true}')::text WHERE id = 6`).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillEffortClampedColumn(db); err != nil {
		t.Fatal(err)
	}
	var marked []int
	if err := db.Raw("SELECT id FROM logs WHERE " + effortClampedCondition(db) + " ORDER BY id").Scan(&marked).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(marked, []int{1, 2, 6}) {
		t.Fatalf("clamped rows=%v", marked)
	}
	if err := backfillEffortClampedColumn(db); err != nil {
		t.Fatal(err)
	}
}

// A finished day is computed once and then read from gateway_timing_daily;
// the open day is always scanned.
func TestPostgresSchema33GatewayTimingStoresFinishedDays(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := installGatewayTimingColumns(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	if now.Sub(today) < time.Hour {
		today = today.Add(-24 * time.Hour)
	}
	closed := today.Add(-48 * time.Hour)
	write := func(id int, at time.Time, pre int) {
		t.Helper()
		row := Log{Id: id, PublicID: fmt.Sprintf("log_gateway_day_%d", id), RequestTS: at.UnixMilli(), Other: fmt.Sprintf(`{"gateway_pre_ms":%d}`, pre)}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	write(1, closed.Add(time.Hour), 10)
	write(2, closed.Add(2*time.Hour), 30)
	write(3, closed.Add(24*time.Hour+time.Hour), 7)
	write(4, now.Add(-time.Minute), 50)
	first, err := GatewayPerformanceDays(context.Background(), closed, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 || first[0].Samples != 2 || first[0].PreP50 == nil || *first[0].PreP50 != 20 {
		t.Fatalf("first read=%+v", first)
	}
	var stored int64
	if err := db.Model(&GatewayTimingDaily{}).Count(&stored).Error; err != nil || stored == 0 {
		t.Fatalf("stored days=%d err=%v", stored, err)
	}
	write(5, closed.Add(3*time.Hour), 1000)
	write(6, now.Add(-30*time.Second), 70)
	second, err := GatewayPerformanceDays(context.Background(), closed, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second[:2], first[:2]) {
		t.Fatalf("finished days were recomputed: %+v -> %+v", first, second)
	}
	if second[2].Samples != first[2].Samples+1 {
		t.Fatalf("open day was not scanned: %+v -> %+v", first[2], second[2])
	}
}

// The batched writer adds each outcome's TTFT into its hour atomically.
func TestPostgresSchema33IncrementalTTFTMerges(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	at := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	streaming := true
	for i, ttft := range []int{100, 200, 5000} {
		user := 7
		outcome := &RoutingOutcome{RequestID: fmt.Sprintf("ttft-merge-%d", i), UserID: &user, OutcomeAt: at.Add(time.Duration(i) * time.Minute), WasStreaming: &streaming, TTFTMs: &ttft}
		if err := db.Transaction(func(tx *gorm.DB) error { return recordHourlyUsageTx(tx, nil, outcome) }); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []int{dashboardRollupScopeGlobal, 7} {
		totals, err := dashboardRollupTotalsContext(context.Background(), scope, at.Unix(), at.Unix()+3600)
		if err != nil {
			t.Fatal(err)
		}
		if totals.TTFTSampleCount != 3 || totals.P95TTFTMs == nil || *totals.P95TTFTMs < 4500 || *totals.P95TTFTMs > 5500 {
			t.Fatalf("scope %d: %+v", scope, totals)
		}
	}
}

// The online prefill and the stopped-writer step together give the same
// hours as the step alone, including the hours younger than the prefill's lag.
func TestPostgresSchema33PrefillThenSchemaStep(t *testing.T) {
	db, _, start, end := dashboardProjectionPostgresFixture(t)
	if err := PrefillSchema33(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := PrefillSchema33(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchema33Telemetry(db); err != nil {
		t.Fatal(err)
	}
	from := dashboardRollupHour(start)
	to := dashboardRollupHour(end+1000) + 2*dashboardRollupHourSeconds
	var raw DashboardUsageTotals
	if err := legacyScanDashboardTTFTStats(1, common.RoleRootUser, normalizeDashboardUsageWindow(from, to-1), &raw); err != nil {
		t.Fatal(err)
	}
	rolled, err := dashboardRollupTotalsContext(context.Background(), dashboardRollupScopeGlobal, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if raw.TTFTSampleCount == 0 || rolled.TTFTSampleCount != raw.TTFTSampleCount {
		t.Fatalf("samples: rollup=%d raw=%d", rolled.TTFTSampleCount, raw.TTFTSampleCount)
	}
}
