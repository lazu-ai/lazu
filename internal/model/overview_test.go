package model

import (
	"context"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func withOverviewTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db := withPricingTestDB(t, name)
	if err := db.AutoMigrate(&DashboardUsageHourlyTotal{}, &DashboardUsageHourlyDimension{}, &AgentSession{}, &SignupAttribution{}); err != nil {
		t.Fatalf("migrate overview tables: %v", err)
	}
	previousLogDB := telemetryDB
	telemetryDB = db
	t.Cleanup(func() { telemetryDB = previousLogDB })
	return db
}

func seedOverviewHours(t *testing.T, db *gorm.DB, rows ...DashboardUsageHourlyTotal) {
	t.Helper()
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed rollup: %v", err)
		}
	}
}

func TestPostgresOverviewReadsTelemetryInsteadOfStaleCoreRollups(t *testing.T) {
	core, telemetry := postgresTelemetryFixture(t)
	if err := core.AutoMigrate(&DashboardUsageHourlyTotal{}, &TopUp{}, &SignupAttribution{}); err != nil {
		t.Fatal(err)
	}
	start := dashboardRollupHour(time.Now().Add(-48 * time.Hour).Unix())
	for _, scope := range []int{0, 7} {
		seedOverviewHours(t, core, DashboardUsageHourlyTotal{ScopeUserID: scope, BucketHour: start, RequestCount: 999})
		seedOverviewHours(t, telemetry,
			DashboardUsageHourlyTotal{ScopeUserID: scope, BucketHour: start, RequestCount: 3},
			DashboardUsageHourlyTotal{ScopeUserID: scope, BucketHour: start - 3600, RequestCount: 2})
	}
	for _, platform := range []bool{false, true} {
		out, err := BuildOverviewContext(context.Background(), OverviewRequest{ScopeUserID: 7, Platform: platform, Start: start, End: start + 3600, Bucket: OverviewBucketHour})
		if err != nil || out.Totals.RequestCount != 3 || out.Previous.RequestCount != 2 || len(out.Series) != 1 || out.Series[0].RequestCount != 3 {
			t.Fatalf("platform=%v totals=%+v previous=%+v series=%+v err=%v", platform, out.Totals, out.Previous, out.Series, err)
		}
	}
}

func overviewRequests(series []OverviewBucket) []int64 {
	out := make([]int64, len(series))
	for i, b := range series {
		out[i] = b.RequestCount
	}
	return out
}

func equalInt64s(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOverviewBucketStartUsesViewerMidnight(t *testing.T) {
	ts := time.Date(2026, 9, 28, 3, 0, 0, 0, time.UTC).Unix()
	west := OverviewRequest{Bucket: OverviewBucketDay, TZOffset: -5 * 3600}
	if got, want := west.bucketStart(ts), time.Date(2026, 9, 27, 5, 0, 0, 0, time.UTC).Unix(); got != want {
		t.Fatalf("UTC-5 day bucket = %d, want %d (local midnight of the 27th)", got, want)
	}
	east := OverviewRequest{Bucket: OverviewBucketDay, TZOffset: 8 * 3600}
	if got, want := east.bucketStart(ts), time.Date(2026, 9, 27, 16, 0, 0, 0, time.UTC).Unix(); got != want {
		t.Fatalf("UTC+8 day bucket = %d, want %d (local midnight of the 28th)", got, want)
	}
	hour := OverviewRequest{Bucket: OverviewBucketHour, TZOffset: 8 * 3600}
	if got := hour.bucketStart(ts + 1799); got != ts {
		t.Fatalf("hour bucket = %d, want %d", got, ts)
	}
}

func TestBuildOverviewDayBucketsFollowTimezoneAndStripOperatorColumns(t *testing.T) {
	db := withOverviewTestDB(t, "overview_day_buckets")
	loc := time.FixedZone("UTC+8", 8*3600)
	start := time.Date(2026, 9, 28, 0, 0, 0, 0, loc).Unix()
	end := start + 3*86400
	const self = 7
	seedOverviewHours(t, db,
		DashboardUsageHourlyTotal{ScopeUserID: self, BucketHour: start + 3600, RequestCount: 2, OutcomeCount: 2, AmountMicrousd: 100, UpstreamMicrousd: 40},
		DashboardUsageHourlyTotal{ScopeUserID: self, BucketHour: start + 23*3600, RequestCount: 3, OutcomeCount: 3, ErrorCount: 1},
		DashboardUsageHourlyTotal{ScopeUserID: self, BucketHour: start + 24*3600, RequestCount: 5, OutcomeCount: 5, PromptTokens: 11},
		// The previous range of equal length.
		DashboardUsageHourlyTotal{ScopeUserID: self, BucketHour: start - 2*3600, RequestCount: 11},
		// Another account, and the platform total: neither is this scope.
		DashboardUsageHourlyTotal{ScopeUserID: 8, BucketHour: start + 3600, RequestCount: 99},
		DashboardUsageHourlyTotal{ScopeUserID: 0, BucketHour: start + 3600, RequestCount: 99},
	)

	out, err := BuildOverviewContext(context.Background(), OverviewRequest{ScopeUserID: self, Start: start, End: end, Bucket: OverviewBucketDay,
		TZOffset: 8 * 3600, Now: start + 30*3600})
	if err != nil {
		t.Fatalf("BuildOverview: %v", err)
	}
	if len(out.Series) != 3 || out.Series[0].Start != start || out.Series[1].Start != start+86400 {
		t.Fatalf("series should start at local midnight, got %+v", out.Series)
	}
	if got := overviewRequests(out.Series); !equalInt64s(got, []int64{5, 5, 0}) {
		t.Fatalf("day buckets = %v, want [5 5 0]", got)
	}
	if out.Series[0].Partial || !out.Series[1].Partial || !out.Series[2].Partial {
		t.Fatalf("only buckets ending after now are partial: %+v", out.Series)
	}
	if out.Totals.RequestCount != 10 || out.Previous.RequestCount != 11 {
		t.Fatalf("totals=%d previous=%d, want 10 / 11", out.Totals.RequestCount, out.Previous.RequestCount)
	}
	if out.Totals.SuccessRate == nil || *out.Totals.SuccessRate != 0.9 {
		t.Fatalf("success rate = %v, want 0.9", out.Totals.SuccessRate)
	}
	if out.Totals.UpstreamCostMicrousd != 0 || out.Series[0].UpstreamCostMicrousd != 0 {
		t.Fatalf("a self view must not carry upstream cost: totals=%+v bucket=%+v", out.Totals, out.Series[0])
	}
	if out.Growth != nil || out.Topups != nil || out.Models == nil {
		t.Fatalf("self view shape: growth=%v topups=%v models=%v", out.Growth, out.Topups, out.Models)
	}
}

func TestBuildOverviewHourAndWeekBuckets(t *testing.T) {
	db := withOverviewTestDB(t, "overview_hour_week_buckets")
	const self = 3
	hourStart := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC).Unix()
	seedOverviewHours(t, db,
		DashboardUsageHourlyTotal{ScopeUserID: self, BucketHour: hourStart, RequestCount: 1},
		DashboardUsageHourlyTotal{ScopeUserID: self, BucketHour: hourStart + 2*3600, RequestCount: 4},
	)
	hourly, err := BuildOverviewContext(context.Background(), OverviewRequest{ScopeUserID: self, Start: hourStart, End: hourStart + 3*3600,
		Bucket: OverviewBucketHour, TZOffset: 8 * 3600, Now: hourStart + 10*3600})
	if err != nil {
		t.Fatalf("hourly overview: %v", err)
	}
	if got := overviewRequests(hourly.Series); !equalInt64s(got, []int64{1, 0, 4}) {
		t.Fatalf("hour buckets = %v, want [1 0 4]", got)
	}

	loc := time.FixedZone("UTC+8", 8*3600)
	monday := time.Date(2026, 8, 3, 0, 0, 0, 0, loc)
	if monday.Weekday() != time.Monday {
		t.Fatalf("fixture date is a %s, not a Monday", monday.Weekday())
	}
	mon := monday.Unix()
	start := mon + 2*86400 // Wednesday: the first week bucket reaches back to Monday
	end := mon + 12*86400
	const weekUser = 4
	seedOverviewHours(t, db,
		// Monday–Tuesday of the first week falls before Start: previous range.
		DashboardUsageHourlyTotal{ScopeUserID: weekUser, BucketHour: mon + 86400, RequestCount: 6},
		DashboardUsageHourlyTotal{ScopeUserID: weekUser, BucketHour: start + 3600, RequestCount: 2},
		DashboardUsageHourlyTotal{ScopeUserID: weekUser, BucketHour: mon + 7*86400 + 5*3600, RequestCount: 3},
	)
	weekly, err := BuildOverviewContext(context.Background(), OverviewRequest{ScopeUserID: weekUser, Start: start, End: end,
		Bucket: OverviewBucketWeek, TZOffset: 8 * 3600, Now: end + 86400})
	if err != nil {
		t.Fatalf("weekly overview: %v", err)
	}
	if len(weekly.Series) != 2 || weekly.Series[0].Start != mon || weekly.Series[1].Start != mon+7*86400 {
		t.Fatalf("week buckets should start on local Mondays, got %+v", weekly.Series)
	}
	if got := overviewRequests(weekly.Series); !equalInt64s(got, []int64{2, 3}) {
		t.Fatalf("week buckets = %v, want [2 3]", got)
	}
	if weekly.Totals.RequestCount != 5 || weekly.Previous.RequestCount != 6 {
		t.Fatalf("week totals=%d previous=%d, want 5 / 6", weekly.Totals.RequestCount, weekly.Previous.RequestCount)
	}
}

func TestBuildOverviewPlatformGrowthCountsOnlyReliableRegistrations(t *testing.T) {
	db := withOverviewTestDB(t, "overview_platform_growth")
	withOperatedAccountRemark(t, "operated:")
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := start + 2*86400
	users := []User{
		{Username: "exact-new", Role: common.RoleCommonUser, CreatedAt: start + 3600, CreatedAtSource: UserCreatedAtSourceExact, FirstRequestAt: start + 7200},
		{Username: "terms-new", Role: common.RoleCommonUser, CreatedAt: start + 86400 + 60, CreatedAtSource: UserCreatedAtSourceTerms, FirstTopupAt: start + 86400 + 120},
		{Username: "guessed", Role: common.RoleCommonUser, CreatedAt: start + 3600, CreatedAtSource: UserCreatedAtSourceInterpolated},
		{Username: "old", Role: common.RoleCommonUser, CreatedAt: start - 30*86400, CreatedAtSource: UserCreatedAtSourceExact},
		{Username: "admin", Role: common.RoleRootUser, CreatedAt: start + 3600, CreatedAtSource: UserCreatedAtSourceExact},
		{Username: "showcase", Role: common.RoleCommonUser, CreatedAt: start + 3600, CreatedAtSource: UserCreatedAtSourceExact, Remark: "operated:seed-2026-10"},
	}
	for i := range users {
		if err := db.Create(&users[i]).Error; err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	seedOverviewHours(t, db,
		DashboardUsageHourlyTotal{ScopeUserID: 0, BucketHour: start + 7200, RequestCount: 4, UpstreamMicrousd: 30},
		DashboardUsageHourlyTotal{ScopeUserID: users[0].Id, BucketHour: start + 7200, RequestCount: 4},
		DashboardUsageHourlyTotal{ScopeUserID: users[5].Id, BucketHour: start - OverviewChurnWindow - 3600, RequestCount: 1},
		DashboardUsageHourlyTotal{ScopeUserID: users[5].Id, BucketHour: start + 7200, RequestCount: 1},
		DashboardUsageHourlyTotal{ScopeUserID: users[5].Id, BucketHour: start + 86400, RequestCount: 1},
		DashboardUsageHourlyTotal{ScopeUserID: users[5].Id, BucketHour: start - 3600, RequestCount: 1},
	)

	out, err := BuildOverviewContext(context.Background(), OverviewRequest{Platform: true, Start: start, End: end, Bucket: OverviewBucketDay, Now: end + 3600})
	if err != nil {
		t.Fatalf("BuildOverview: %v", err)
	}
	if out.Growth == nil {
		t.Fatal("platform overview has no growth")
	}
	if out.Growth.NewUsers != 2 || out.Growth.Activated != 1 || out.Growth.Paid != 1 || out.Growth.TotalCustomers != 4 {
		t.Fatalf("growth = %+v, want 2 new (1 activated, 1 paid) of 4 customers", out.Growth)
	}
	if out.Growth.ActiveUsersPrevious != 0 || out.Growth.ReturnedUsers != 0 || *out.Series[1].ActiveUsers != 0 || out.Growth.ActiveUsers != 1 || out.Series[0].NewUsers == nil || *out.Series[0].NewUsers != 1 || *out.Series[1].NewUsers != 1 {
		t.Fatalf("per-bucket growth: growth=%+v series=%+v", out.Growth, out.Series)
	}
	if out.Totals.RequestCount != 4 || out.Totals.UpstreamCostMicrousd != 30 {
		t.Fatalf("platform totals should keep operator columns: %+v", out.Totals)
	}
}

func TestSignupAttributionBreakdownsShareTheSameCohort(t *testing.T) {
	db := withOverviewTestDB(t, "attribution_breakdowns")
	rows := []SignupAttribution{{UserID: 1, Source: "community", Campaign: "guide", LandingPath: "/docs/quickstart", SelfReported: "reddit"}, {UserID: 2, Source: "community", Campaign: "cost", LandingPath: "/blog/cost"}, {UserID: 3, Source: "excluded", Campaign: "cost", SelfReported: "chatgpt"}}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	groups, err := overviewSignupSourcesContext(context.Background(), map[int]overviewNewcomer{1: {activated: true}, 2: {activated: true, paid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups["source"]) != 1 || groups["source"][0].Users != 2 || groups["source"][0].Paid != 1 || len(groups["campaign"]) != 2 || len(groups["landing_path"]) != 2 ||
		len(groups["channel"]) != 1 || groups["channel"][0].Source != AttributionChannelCommunity || groups["channel"][0].Users != 2 ||
		len(groups["self_reported"]) != 1 || groups["self_reported"][0].Source != "reddit" || groups["self_reported"][0].Users != 1 {
		t.Fatalf("incorrect cohort: %+v", groups)
	}
}
