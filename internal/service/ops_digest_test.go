package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	relaymetrics "github.com/lazu-ai/lazu/internal/relay/metrics"
	"github.com/lazu-ai/lazu/internal/storage"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func floatPtr(v float64) *float64 { return &v }

func TestFormatBusinessDigestComparesWithTheWeek(t *testing.T) {
	day := time.Date(2026, 10, 19, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	current := model.Overview{
		Totals: model.OverviewTotals{
			RequestCount: 124_000, OutcomeCount: 124_000, SuccessRate: floatPtr(0.996), P95LatencyMs: floatPtr(2300),
			AmountMicrousd: 3_210_500_000, GrossMarginMicrousd: 820_000_000, CostSampleCount: 124_000,
		},
		Growth: &model.OverviewGrowth{NewUsers: 23, Paid: 4, ActiveUsers: 310},
		Topups: &model.OverviewTopups{Count: 4, AmountMicrousd: 5_000_000_000},
		Models: []model.DashboardUsageDimensionRow{
			{Name: "deepseek-v4", AmountMicrousd: 600_000_000},
			{Name: "claude-sonnet-5", AmountMicrousd: 1_200_000_000},
			{Name: "gpt-5.5", AmountMicrousd: 300_000_000},
			{Name: "tiny", AmountMicrousd: 1},
		},
	}
	week := model.OverviewTotals{RequestCount: 805_000, AmountMicrousd: 21_350_000_000}
	section := formatBusinessDigest(day, current, week, 7, relaymetrics.ZeroTokenQuotaZeroSnapshot{Zero: 15, Total: 12_400, Rate: 15.0 / 12_400})
	want := []string{
		"请求 12.4 万（前 7 天日均 11.5 万，+8%） · 成功率 99.6% · P95 延迟 2.3 秒",
		"消耗 $3,210.50（前 7 天日均 $3,050.00，+5%） · 毛利 $820.00（26%）",
		"充值 $5,000.00（4 笔） · 新注册 23 人（已付费 4） · 活跃用户 310",
		"消耗最多：claude-sonnet-5 $1,200.00、deepseek-v4 $600.00、gpt-5.5 $300.00",
		"0 token 计费：0.12%（15 / 1.2 万 个请求）",
	}
	if section.Title != "昨日业务（10-19）" || strings.Join(section.Lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("section =\n%s\n%s\nwant\n%s", section.Title, strings.Join(section.Lines, "\n"), strings.Join(want, "\n"))
	}
	if len(section.Attention) != 0 {
		t.Fatalf("a normal day needs no attention: %v", section.Attention)
	}
}

func TestFormatBusinessDigestRaisesAttention(t *testing.T) {
	day := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	current := model.Overview{Totals: model.OverviewTotals{
		RequestCount: 4_000, OutcomeCount: 4_000, SuccessRate: floatPtr(0.95),
		AmountMicrousd: 20_000_000, GrossMarginMicrousd: -5_000_000, CostSampleCount: 4_000,
	}}
	week := model.OverviewTotals{RequestCount: 70_000, AmountMicrousd: 700_000_000}
	section := formatBusinessDigest(day, current, week, 7, relaymetrics.ZeroTokenQuotaZeroSnapshot{Zero: 80, Total: 4_000, Rate: 0.02})
	joined := strings.Join(section.Attention, "\n")
	for _, expected := range []string{"成功率 95.0%", "昨日请求 4,000，比前 7 天日均 1.0 万 少了 60%", "昨日消耗 $20.00", "毛利为负（-$5.00）", "0 token 计费比例 2.00%"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("attention missing %q:\n%s", expected, joined)
		}
	}
}

func TestFormatBusinessDigestLabelsPartialMargin(t *testing.T) {
	day := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	current := model.Overview{Totals: model.OverviewTotals{
		RequestCount: 1_000, OutcomeCount: 1_000, SuccessRate: floatPtr(1),
		AmountMicrousd: 50_000_000, GrossMarginMicrousd: -2_000_000, CostSampleCount: 40,
	}}
	section := formatBusinessDigest(day, current, model.OverviewTotals{}, 7, relaymetrics.ZeroTokenQuotaZeroSnapshot{})
	if !strings.Contains(section.Lines[1], "毛利 -$2.00（仅 40 / 1,000 个请求有成本数据）") {
		t.Fatalf("partial margin should say how much it covers: %q", section.Lines[1])
	}
	if len(section.Attention) != 0 {
		t.Fatalf("a margin over part of the traffic must not raise attention: %v", section.Attention)
	}
}

func TestDigestDayWindowAlignsToRollupHours(t *testing.T) {
	kolkata := time.FixedZone("IST", 5*3600+30*60)
	today, day := digestDayWindow(time.Date(2026, 10, 20, 9, 10, 0, 0, kolkata))
	if today.Unix()%3600 != 0 || today.Sub(day) != 24*time.Hour {
		t.Fatalf("window must be 24 whole rollup hours: %s to %s", day, today)
	}
	if want := time.Date(2026, 10, 19, 23, 30, 0, 0, kolkata); !today.Equal(want) {
		t.Fatalf("half-hour zone should floor midnight to the hour: got %s want %s", today, want)
	}
	shanghai := time.FixedZone("CST", 8*3600)
	today, _ = digestDayWindow(time.Date(2026, 10, 20, 9, 10, 0, 0, shanghai))
	if want := time.Date(2026, 10, 20, 0, 0, 0, 0, shanghai); !today.Equal(want) {
		t.Fatalf("whole-hour zone keeps local midnight: got %s want %s", today, want)
	}
}

func TestDigestDayWindowKeepsDaylightSavingDays(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	for _, tc := range []struct {
		now   time.Time
		hours time.Duration
	}{
		{time.Date(2026, 10, 26, 9, 10, 0, 0, berlin), 25}, // clocks went back on 10-25
		{time.Date(2026, 3, 30, 9, 10, 0, 0, berlin), 23},  // clocks went forward on 03-29
	} {
		today, day := digestDayWindow(tc.now)
		if got := today.Sub(day); got != tc.hours*time.Hour {
			t.Fatalf("%s: window is %s, want %dh", tc.now.Format("01-02"), got, tc.hours)
		}
		if day.Hour() != 0 || today.Hour() != 0 {
			t.Fatalf("%s: both ends must be local midnight: %s to %s", tc.now.Format("01-02"), day, today)
		}
	}
}

func TestCapacityHistoryReadFailureIsReturned(t *testing.T) {
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	down := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	common.RedisEnabled, common.RDB = true, down
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		_ = down.Close()
	})
	if _, err := recordCapacitySample(context.Background(), time.Now(), opsCapacitySample{}); err == nil {
		t.Fatal("an unreachable Redis must surface as an error, not an empty history")
	}
}

func TestDiskGrowthIgnoresADifferentFilesystem(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	const gib = 1 << 30
	today := opsCapacitySample{DiskUsed: 41 * gib, DiskTotal: 100 * gib}
	other := map[string]opsCapacitySample{"2026-10-19": {DiskUsed: 1 * gib, DiskTotal: 50 * gib}}
	lines, attention := formatCapacityDigest(now, today, other)
	if strings.Contains(lines[0], "写满") || len(attention) != 0 {
		t.Fatalf("growth must not be measured against another replica's disk: %v %v", lines, attention)
	}
}

func TestFormatBackupDigest(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, loc)
	at := func(daysAgo int) time.Time { return time.Date(2026, 10, 20-daysAgo, 5, 31, 0, 0, loc) }
	const mib = 1 << 20
	history := []storage.ObjectInfo{
		{Key: "pg-dump/2026-10/lazu-a.sql.gz", Size: 660 * mib, LastModified: at(3)},
		{Key: "pg-dump/2026-10/lazu-b.sql.gz", Size: 668 * mib, LastModified: at(2)},
		{Key: "pg-dump/2026-10/lazu-c.sql.gz", Size: 670 * mib, LastModified: at(1)},
		{Key: "pg-dump/2026-10/", Size: 0, LastModified: at(0)},
	}

	line, attention := formatBackupDigest(now, append(history, storage.ObjectInfo{Key: "pg-dump/2026-10/lazu-d.sql.gz", Size: 671 * mib, LastModified: at(0)}))
	if line != "最新备份：10-20 05:31，671.0 MiB（此前 3 份中位数 668.0 MiB）" || len(attention) != 0 {
		t.Fatalf("healthy backup: line=%q attention=%v", line, attention)
	}

	_, attention = formatBackupDigest(now, append(history, storage.ObjectInfo{Key: "pg-dump/2026-10/lazu-d.sql.gz", Size: 300 * mib, LastModified: at(0)}))
	if len(attention) != 1 || !strings.Contains(attention[0], "可能不完整") {
		t.Fatalf("shrunken backup should raise attention: %v", attention)
	}

	_, attention = formatBackupDigest(now, history[:1])
	if len(attention) != 1 || !strings.Contains(attention[0], "3 天前") {
		t.Fatalf("old backup should raise attention: %v", attention)
	}

	_, attention = formatBackupDigest(now, nil)
	if len(attention) != 1 {
		t.Fatalf("missing backups should raise attention: %v", attention)
	}
}

func TestFormatCapacityDigest(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, time.UTC)
	const gib = 1 << 30
	today := opsCapacitySample{DiskUsed: 41 * gib, DiskTotal: 100 * gib, CoreBytes: 3 * gib, TelemetryBytes: 18 * gib}
	history := map[string]opsCapacitySample{
		"2026-10-13": {DiskUsed: 34 * gib, DiskTotal: 100 * gib, CoreBytes: 3*gib - 120<<20, TelemetryBytes: 17 * gib},
		"2026-10-20": today,
	}
	lines, attention := formatCapacityDigest(now, today, history)
	want := "磁盘已用 41%（剩余 59.0 GiB） · 近 7 天每天多用 1.0 GiB，约 59 天写满\n数据库：主库 3.0 GiB（7 天 +120.0 MiB） · 遥测库 18.0 GiB（7 天 +1.0 GiB）"
	if strings.Join(lines, "\n") != want || len(attention) != 0 {
		t.Fatalf("lines=\n%s\nwant\n%s\nattention=%v", strings.Join(lines, "\n"), want, attention)
	}

	fast := map[string]opsCapacitySample{"2026-10-19": {DiskUsed: 31 * gib, DiskTotal: 100 * gib}}
	_, attention = formatCapacityDigest(now, today, fast)
	if len(attention) != 1 || !strings.Contains(attention[0], "约 6 天后写满") {
		t.Fatalf("fast growth should raise attention: %v", attention)
	}

	full := opsCapacitySample{DiskUsed: 90 * gib, DiskTotal: 100 * gib}
	_, attention = formatCapacityDigest(now, full, nil)
	if len(attention) != 1 || !strings.Contains(attention[0], "磁盘已用 90%") {
		t.Fatalf("full disk should raise attention: %v", attention)
	}
}

func TestDigestNumberFormats(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{formatDigestCount(999), "999"},
		{formatDigestCount(9_999), "9,999"},
		{formatDigestCount(124_000), "12.4 万"},
		{formatDigestUSD(3_210_504_999), "$3,210.50"},
		{formatDigestUSD(-5_000_000), "-$5.00"},
		{formatDigestUSD(0), "$0.00"},
		{formatDigestMillis(850), "850 毫秒"},
	} {
		if tc.got != tc.want {
			t.Fatalf("got %q, want %q", tc.got, tc.want)
		}
	}
	info := "# Memory\r\nused_memory:1048576\r\nused_memory_human:1.00M\r\nmaxmemory:0\r\n"
	if used, max := redisInfoInt(info, "used_memory"), redisInfoInt(info, "maxmemory"); used != 1048576 || max != 0 {
		t.Fatalf("redisInfoInt used=%d max=%d", used, max)
	}
}

func TestBuildBusinessDigestSectionReadsRollupsTopUpsAndLogs(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:ops_digest_business?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	previous := model.CoreDB()
	model.SetCoreDB(db)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previous)
	})
	if err := db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.DashboardUsageHourlyTotal{},
		&model.DashboardUsageHourlyDimension{}, &model.AgentSession{}, &model.SignupAttribution{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, loc)
	yesterday := time.Date(2026, 10, 19, 10, 0, 0, 0, loc).Unix()
	lastWeek := time.Date(2026, 10, 15, 10, 0, 0, 0, loc).Unix()
	for _, row := range []model.DashboardUsageHourlyTotal{
		{BucketHour: yesterday, RequestCount: 1200, OutcomeCount: 1200, ErrorCount: 12, AmountMicrousd: 30_000_000},
		{BucketHour: lastWeek, RequestCount: 7000, OutcomeCount: 7000, AmountMicrousd: 210_000_000},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed rollup: %v", err)
		}
	}
	if err := db.Create(&model.DashboardUsageHourlyDimension{BucketHour: yesterday, DimensionType: "model", DimensionID: "m1", DimensionName: "claude-sonnet-5", RequestCount: 1200, AmountMicrousd: 30_000_000}).Error; err != nil {
		t.Fatalf("seed dimension: %v", err)
	}
	if err := db.Create(&model.TopUp{PublicID: "top_1", TradeNo: "t1", UserId: 1, Status: "success", CompleteTime: yesterday, CreditedMicrousd: 50_000_000}).Error; err != nil {
		t.Fatalf("seed topup: %v", err)
	}
	for i := 0; i < 4; i++ {
		row := model.Log{Type: model.LogTypeConsume, CreatedAt: yesterday, PromptTokens: 10}
		if i == 0 {
			row.PromptTokens = 0
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}

	section, err := buildBusinessDigestSection(context.Background(), now)
	if err != nil {
		t.Fatalf("buildBusinessDigestSection: %v", err)
	}
	joined := strings.Join(section.Lines, "\n")
	for _, expected := range []string{
		"请求 1,200（前 7 天日均 1,000，+20%） · 成功率 99.0%",
		"消耗 $30.00（前 7 天日均 $30.00，+0%）",
		"充值 $50.00（1 笔）",
		"消耗最多：claude-sonnet-5 $30.00",
		"0 token 计费：25.00%（1 / 4 个请求）",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("section missing %q:\n%s", expected, joined)
		}
	}
}

func TestBuildBusinessDigestSectionKeepsMetricsWhenZeroTokenQueryFails(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:ops_digest_zero_fails?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	previous, previousQuery := model.CoreDB(), queryDigestZeroTokenRate
	model.SetCoreDB(db)
	queryDigestZeroTokenRate = func(context.Context, *gorm.DB, int64, int64) (relaymetrics.ZeroTokenQuotaZeroSnapshot, error) {
		return relaymetrics.ZeroTokenQuotaZeroSnapshot{}, errors.New("logs scan timed out")
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previous)
		queryDigestZeroTokenRate = previousQuery
	})
	if err := db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.DashboardUsageHourlyTotal{},
		&model.DashboardUsageHourlyDimension{}, &model.AgentSession{}, &model.SignupAttribution{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 20, 9, 10, 0, 0, loc)
	if err := db.Create(&model.DashboardUsageHourlyTotal{BucketHour: time.Date(2026, 10, 19, 10, 0, 0, 0, loc).Unix(), RequestCount: 1200, OutcomeCount: 1200}).Error; err != nil {
		t.Fatalf("seed rollup: %v", err)
	}
	section, err := buildBusinessDigestSection(context.Background(), now)
	if err != nil {
		t.Fatalf("a failed zero-token query must not fail the section: %v", err)
	}
	if !strings.Contains(strings.Join(section.Lines, "\n"), "请求 1,200") {
		t.Fatalf("rollup metrics should still show: %v", section.Lines)
	}
	if len(section.Attention) != 1 || !strings.Contains(section.Attention[0], "无法读取昨日 0 token 计费比例：logs scan timed out") {
		t.Fatalf("the failed query should raise attention: %v", section.Attention)
	}
}
