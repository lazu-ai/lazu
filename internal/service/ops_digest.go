package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	relaymetrics "github.com/lazu-ai/lazu/internal/relay/metrics"
	"github.com/lazu-ai/lazu/internal/service/alerting"
	"github.com/lazu-ai/lazu/internal/storage"
	"gorm.io/gorm"
)

// Thresholds for the attention items the daily digest raises. They flag a
// day worth a look, not an outage: outages page through incidents.
const (
	opsDigestMinSuccessRate     = 0.98
	opsDigestMinOutcomes        = 100
	opsDigestDropRatio          = 0.5
	opsDigestMinAverageRequests = 100
	opsDigestMinAverageAmount   = 10_000_000 // $10 in microUSD
	opsDigestZeroTokenRate      = 0.01
	opsDigestMinZeroTokenTotal  = 1000
	opsDigestBackupMaxAge       = 36 * time.Hour
	opsDigestBackupShrinkRatio  = 0.8
	opsDigestDiskWarnPercent    = 85
	opsDigestDiskWarnDays       = 14
	opsDigestRedisWarnRatio     = 0.85
	opsDigestCapacityKey        = "lazu:ops:capacity:v1"
	opsDigestCapacityDays       = 15
)

// RegisterOpsDigestSections adds yesterday's business numbers and the data
// and capacity checks to the daily operations digest.
func RegisterOpsDigestSections() {
	alerting.RegisterDigestSection("昨日业务", buildBusinessDigestSection)
	alerting.RegisterDigestSection("数据与容量", buildCapacityDigestSection)
}

func buildBusinessDigestSection(ctx context.Context, now time.Time) (alerting.DigestSection, error) {
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayStart := todayStart.AddDate(0, 0, -1)
	_, offset := now.Zone()
	day, err := model.BuildOverviewContext(ctx, model.OverviewRequest{
		Platform: true,
		Start:    dayStart.Unix(),
		End:      todayStart.Unix(),
		Bucket:   model.OverviewBucketDay,
		TZOffset: int64(offset),
		Now:      now.Unix(),
	})
	if err != nil {
		return alerting.DigestSection{}, fmt.Errorf("yesterday overview: %w", err)
	}
	week, err := model.BuildOverviewContext(ctx, model.OverviewRequest{
		Start:    dayStart.AddDate(0, 0, -7).Unix(),
		End:      dayStart.Unix(),
		Bucket:   model.OverviewBucketDay,
		TZOffset: int64(offset),
		Now:      now.Unix(),
	})
	if err != nil {
		return alerting.DigestSection{}, fmt.Errorf("previous week overview: %w", err)
	}
	db := model.TelemetryDB()
	if db == nil {
		db = model.CoreDB()
	}
	zero, err := relaymetrics.QueryZeroTokenQuotaZeroRate(ctx, db, dayStart.Unix(), todayStart.Unix())
	if err != nil {
		return alerting.DigestSection{}, fmt.Errorf("zero-token rate: %w", err)
	}
	return formatBusinessDigest(dayStart, day, week.Totals, 7, zero), nil
}

// formatBusinessDigest compares yesterday with the average day of the
// previous week, so a weekday dip reads against a full week rather than
// against a single neighbouring day.
func formatBusinessDigest(day time.Time, current model.Overview, previous model.OverviewTotals, previousDays int, zero relaymetrics.ZeroTokenQuotaZeroSnapshot) alerting.DigestSection {
	section := alerting.DigestSection{Title: "昨日业务（" + day.Format("01-02") + "）"}
	totals := current.Totals
	avgRequests := float64(previous.RequestCount) / float64(previousDays)
	avgAmount := float64(previous.AmountMicrousd) / float64(previousDays)

	requestLine := "请求 " + formatDigestCount(totals.RequestCount) + compareDigestAverage(float64(totals.RequestCount), avgRequests, formatDigestCount(int64(avgRequests+0.5)))
	if totals.SuccessRate != nil {
		requestLine += fmt.Sprintf(" · 成功率 %.1f%%", *totals.SuccessRate*100)
	}
	if totals.P95LatencyMs != nil {
		requestLine += " · P95 延迟 " + formatDigestMillis(*totals.P95LatencyMs)
	}
	section.Lines = append(section.Lines, requestLine)

	amountLine := "消耗 " + formatDigestUSD(totals.AmountMicrousd) + compareDigestAverage(float64(totals.AmountMicrousd), avgAmount, formatDigestUSD(int64(avgAmount+0.5)))
	if totals.CostSampleCount > 0 {
		amountLine += " · 毛利 " + formatDigestUSD(totals.GrossMarginMicrousd)
		if totals.AmountMicrousd > 0 {
			amountLine += fmt.Sprintf("（%.0f%%）", float64(totals.GrossMarginMicrousd)/float64(totals.AmountMicrousd)*100)
		}
	}
	section.Lines = append(section.Lines, amountLine)

	var people []string
	if current.Topups != nil {
		people = append(people, fmt.Sprintf("充值 %s（%d 笔）", formatDigestUSD(current.Topups.AmountMicrousd), current.Topups.Count))
	}
	if growth := current.Growth; growth != nil {
		people = append(people, fmt.Sprintf("新注册 %d 人（已付费 %d）", growth.NewUsers, growth.Paid))
		people = append(people, fmt.Sprintf("活跃用户 %d", growth.ActiveUsers))
	}
	if len(people) > 0 {
		section.Lines = append(section.Lines, strings.Join(people, " · "))
	}

	models := append([]model.DashboardUsageDimensionRow(nil), current.Models...)
	sort.SliceStable(models, func(i, j int) bool { return models[i].AmountMicrousd > models[j].AmountMicrousd })
	if len(models) > 3 {
		models = models[:3]
	}
	if len(models) > 0 {
		names := make([]string, 0, len(models))
		for _, row := range models {
			names = append(names, row.Name+" "+formatDigestUSD(row.AmountMicrousd))
		}
		section.Lines = append(section.Lines, "消耗最多："+strings.Join(names, "、"))
	}
	if zero.Total > 0 {
		section.Lines = append(section.Lines, fmt.Sprintf("0 token 计费：%.2f%%（%s / %s 个请求）",
			zero.Rate*100, formatDigestCount(zero.Zero), formatDigestCount(zero.Total)))
	}

	if totals.SuccessRate != nil && totals.OutcomeCount >= opsDigestMinOutcomes && *totals.SuccessRate < opsDigestMinSuccessRate {
		section.Attention = append(section.Attention, fmt.Sprintf("昨日成功率 %.1f%%，低于 %.0f%%，请看错误最多的渠道和模型",
			*totals.SuccessRate*100, opsDigestMinSuccessRate*100))
	}
	if avgRequests >= opsDigestMinAverageRequests && float64(totals.RequestCount) < avgRequests*opsDigestDropRatio {
		section.Attention = append(section.Attention, fmt.Sprintf("昨日请求 %s，比前 7 天日均 %s 少了 %.0f%%",
			formatDigestCount(totals.RequestCount), formatDigestCount(int64(avgRequests+0.5)), (1-float64(totals.RequestCount)/avgRequests)*100))
	}
	if avgAmount >= opsDigestMinAverageAmount && float64(totals.AmountMicrousd) < avgAmount*opsDigestDropRatio {
		section.Attention = append(section.Attention, fmt.Sprintf("昨日消耗 %s，比前 7 天日均 %s 少了 %.0f%%",
			formatDigestUSD(totals.AmountMicrousd), formatDigestUSD(int64(avgAmount+0.5)), (1-float64(totals.AmountMicrousd)/avgAmount)*100))
	}
	if totals.CostSampleCount > 0 && totals.GrossMarginMicrousd < 0 {
		section.Attention = append(section.Attention, "昨日毛利为负（"+formatDigestUSD(totals.GrossMarginMicrousd)+"），请检查定价和上游成本")
	}
	if zero.Total >= opsDigestMinZeroTokenTotal && zero.Rate >= opsDigestZeroTokenRate {
		section.Attention = append(section.Attention, fmt.Sprintf("昨日 0 token 计费比例 %.2f%%（%s 个请求），高于 %.0f%%，可能有请求没收到费",
			zero.Rate*100, formatDigestCount(zero.Zero), opsDigestZeroTokenRate*100))
	}
	return section
}

// opsCapacitySample is one day's footprint, kept for a couple of weeks so
// the digest can show growth and estimate when the disk fills.
type opsCapacitySample struct {
	DiskUsed  uint64 `json:"disk_used"`
	DiskTotal uint64 `json:"disk_total"`
	// DiskFree is what unprivileged writers can still use; it excludes the
	// filesystem's reserved blocks, so it can be less than total minus used.
	DiskFree       uint64 `json:"disk_free,omitempty"`
	CoreBytes      int64  `json:"core_bytes,omitempty"`
	TelemetryBytes int64  `json:"telemetry_bytes,omitempty"`
}

func buildCapacityDigestSection(ctx context.Context, now time.Time) (alerting.DigestSection, error) {
	section := alerting.DigestSection{Title: "数据与容量"}

	if common.AppConfig.ArtifactStorage.S3.IsConfigured() {
		backups, err := listDigestBackups(ctx)
		if err != nil {
			section.Lines = append(section.Lines, "⚠️ 读取 R2 备份列表失败："+err.Error())
		} else {
			line, attention := formatBackupDigest(now, backups)
			section.Lines = append(section.Lines, line)
			section.Attention = append(section.Attention, attention...)
		}
	}

	disk := common.GetDiskSpaceInfo()
	sample := opsCapacitySample{DiskUsed: disk.Used, DiskTotal: disk.Total, DiskFree: disk.Free}
	if common.UsingPostgreSQL {
		sample.CoreBytes = digestDatabaseSize(ctx, model.CoreDB())
		if telemetry := model.TelemetryDB(); telemetry != nil && telemetry != model.CoreDB() {
			sample.TelemetryBytes = digestDatabaseSize(ctx, telemetry)
		}
	}
	history := recordCapacitySample(ctx, now, sample)
	lines, attention := formatCapacityDigest(now, sample, history)
	section.Lines = append(section.Lines, lines...)
	section.Attention = append(section.Attention, attention...)

	if line, warn := redisMemoryDigest(ctx); line != "" {
		section.Lines = append(section.Lines, line)
		if warn != "" {
			section.Attention = append(section.Attention, warn)
		}
	}
	return section, nil
}

func listDigestBackups(ctx context.Context) ([]storage.ObjectInfo, error) {
	client, err := storage.NewS3(common.AppConfig.ArtifactStorage.S3)
	if err != nil {
		return nil, err
	}
	return client.ListByPrefix(ctx, "pg-dump/")
}

// formatBackupDigest checks the bucket itself, not the task's own report:
// the newest dump must be recent and about as large as the ones before it.
func formatBackupDigest(now time.Time, objects []storage.ObjectInfo) (string, []string) {
	dumps := make([]storage.ObjectInfo, 0, len(objects))
	for _, object := range objects {
		if strings.HasSuffix(object.Key, ".sql.gz") {
			dumps = append(dumps, object)
		}
	}
	if len(dumps) == 0 {
		return "数据库备份：R2 里没有备份文件", []string{"R2 的 pg-dump/ 下没有任何数据库备份"}
	}
	sort.Slice(dumps, func(i, j int) bool { return dumps[i].LastModified.After(dumps[j].LastModified) })
	newest := dumps[0]
	age := now.Sub(newest.LastModified)
	line := fmt.Sprintf("最新备份：%s，%s", newest.LastModified.In(now.Location()).Format("01-02 15:04"), formatDigestBytes(newest.Size))
	var attention []string
	if age > opsDigestBackupMaxAge {
		attention = append(attention, fmt.Sprintf("最新的数据库备份是 %d 天前（%s），之后没有新备份",
			int(age/(24*time.Hour)), newest.LastModified.In(now.Location()).Format("01-02 15:04")))
	}
	previous := dumps[1:]
	if len(previous) > 7 {
		previous = previous[:7]
	}
	if len(previous) > 0 {
		sizes := make([]int64, 0, len(previous))
		for _, object := range previous {
			sizes = append(sizes, object.Size)
		}
		sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
		median := sizes[len(sizes)/2]
		line += fmt.Sprintf("（此前 %d 份中位数 %s）", len(sizes), formatDigestBytes(median))
		if median > 0 && float64(newest.Size) < float64(median)*opsDigestBackupShrinkRatio {
			attention = append(attention, fmt.Sprintf("最新数据库备份 %s，比此前中位数 %s 小了 %.0f%%，可能不完整",
				formatDigestBytes(newest.Size), formatDigestBytes(median), (1-float64(newest.Size)/float64(median))*100))
		}
	}
	return line, attention
}

func digestDatabaseSize(ctx context.Context, db *gorm.DB) int64 {
	if db == nil {
		return 0
	}
	var size int64
	if err := db.WithContext(ctx).Raw("SELECT pg_database_size(current_database())").Scan(&size).Error; err != nil {
		common.SysError("ops digest: database size query failed: " + err.Error())
		return 0
	}
	return size
}

// recordCapacitySample stores today's sample and returns the stored history
// by date. Without Redis there is no history and only today's numbers show.
func recordCapacitySample(ctx context.Context, now time.Time, sample opsCapacitySample) map[string]opsCapacitySample {
	history := map[string]opsCapacitySample{}
	if !common.RedisEnabled || common.RDB == nil {
		return history
	}
	today := now.Format("2006-01-02")
	if raw, err := json.Marshal(sample); err == nil {
		if err := common.RDB.HSet(ctx, opsDigestCapacityKey, today, raw).Err(); err != nil {
			common.SysError("ops digest: failed to store capacity sample: " + err.Error())
		}
	}
	values, err := common.RDB.HGetAll(ctx, opsDigestCapacityKey).Result()
	if err != nil {
		common.SysError("ops digest: failed to load capacity history: " + err.Error())
		return history
	}
	cutoff := now.AddDate(0, 0, -opsDigestCapacityDays).Format("2006-01-02")
	for date, raw := range values {
		if date < cutoff {
			_ = common.RDB.HDel(ctx, opsDigestCapacityKey, date).Err()
			continue
		}
		var stored opsCapacitySample
		if json.Unmarshal([]byte(raw), &stored) == nil {
			history[date] = stored
		}
	}
	return history
}

// formatCapacityDigest shows today's footprint with its growth over the last
// week, and estimates when the disk fills at that pace.
func formatCapacityDigest(now time.Time, today opsCapacitySample, history map[string]opsCapacitySample) ([]string, []string) {
	var lines, attention []string
	base, baseDays, hasBase := capacityBaseline(now, history)
	if today.DiskTotal > 0 {
		percent := float64(today.DiskUsed) / float64(today.DiskTotal) * 100
		free := today.DiskFree
		if free == 0 && today.DiskTotal > today.DiskUsed {
			free = today.DiskTotal - today.DiskUsed
		}
		line := fmt.Sprintf("磁盘已用 %.0f%%（剩余 %s）", percent, formatDigestBytes(int64(free)))
		daysLeft := -1.0
		if hasBase && today.DiskUsed > base.DiskUsed {
			perDay := float64(today.DiskUsed-base.DiskUsed) / baseDays
			daysLeft = float64(free) / perDay
			line += fmt.Sprintf(" · 近 %.0f 天每天多用 %s，约 %.0f 天写满", baseDays, formatDigestBytes(int64(perDay)), daysLeft)
		}
		lines = append(lines, line)
		switch {
		case percent >= opsDigestDiskWarnPercent:
			attention = append(attention, fmt.Sprintf("磁盘已用 %.0f%%，只剩 %s", percent, formatDigestBytes(int64(free))))
		case daysLeft >= 0 && daysLeft < opsDigestDiskWarnDays:
			attention = append(attention, fmt.Sprintf("按近 %.0f 天的增速，磁盘约 %.0f 天后写满", baseDays, daysLeft))
		}
	}
	var dbParts []string
	if today.CoreBytes > 0 {
		part := "主库 " + formatDigestBytes(today.CoreBytes)
		if hasBase && base.CoreBytes > 0 {
			part += "（" + formatDigestGrowth(today.CoreBytes-base.CoreBytes, baseDays) + "）"
		}
		dbParts = append(dbParts, part)
	}
	if today.TelemetryBytes > 0 {
		part := "遥测库 " + formatDigestBytes(today.TelemetryBytes)
		if hasBase && base.TelemetryBytes > 0 {
			part += "（" + formatDigestGrowth(today.TelemetryBytes-base.TelemetryBytes, baseDays) + "）"
		}
		dbParts = append(dbParts, part)
	}
	if len(dbParts) > 0 {
		lines = append(lines, "数据库："+strings.Join(dbParts, " · "))
	}
	return lines, attention
}

// capacityBaseline picks the oldest sample within the last week, at least a
// day old, to measure growth against.
func capacityBaseline(now time.Time, history map[string]opsCapacitySample) (opsCapacitySample, float64, bool) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for back := 7; back >= 1; back-- {
		date := today.AddDate(0, 0, -back).Format("2006-01-02")
		if sample, ok := history[date]; ok {
			return sample, float64(back), true
		}
	}
	return opsCapacitySample{}, 0, false
}

func redisMemoryDigest(ctx context.Context) (string, string) {
	if !common.RedisEnabled || common.RDB == nil {
		return "", ""
	}
	info, err := common.RDB.Info(ctx, "memory").Result()
	if err != nil {
		return "⚠️ 读取 Redis 内存失败：" + err.Error(), ""
	}
	used, max := redisInfoInt(info, "used_memory"), redisInfoInt(info, "maxmemory")
	if used <= 0 {
		return "", ""
	}
	if max <= 0 {
		return "Redis 内存 " + formatDigestBytes(used) + "（未设上限）", ""
	}
	ratio := float64(used) / float64(max)
	line := fmt.Sprintf("Redis 内存 %s / 上限 %s（%.0f%%）", formatDigestBytes(used), formatDigestBytes(max), ratio*100)
	if ratio >= opsDigestRedisWarnRatio {
		return line, fmt.Sprintf("Redis 内存已用到上限的 %.0f%%，满了会开始淘汰或拒绝写入", ratio*100)
	}
	return line, ""
}

func redisInfoInt(info string, field string) int64 {
	for _, line := range strings.Split(info, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), field+":"); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			return n
		}
	}
	return 0
}

func compareDigestAverage(value float64, average float64, averageText string) string {
	if average <= 0 {
		return ""
	}
	return fmt.Sprintf("（前 7 天日均 %s，%+.0f%%）", averageText, (value/average-1)*100)
}

// formatDigestCount writes counts the way a Chinese reader scans them:
// thousands separators below ten thousand, then 万.
func formatDigestCount(n int64) string {
	if n >= 10_000 || n <= -10_000 {
		return strconv.FormatFloat(float64(n)/10_000, 'f', 1, 64) + " 万"
	}
	return groupDigestDigits(strconv.FormatInt(n, 10))
}

func formatDigestUSD(microusd int64) string {
	sign := ""
	if microusd < 0 {
		sign, microusd = "-", -microusd
	}
	cents := (microusd + 5_000) / 10_000
	return fmt.Sprintf("%s$%s.%02d", sign, groupDigestDigits(strconv.FormatInt(cents/100, 10)), cents%100)
}

func groupDigestDigits(digits string) string {
	negative := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	if negative {
		return "-" + digits
	}
	return digits
}

func formatDigestMillis(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.1f 秒", ms/1000)
	}
	return fmt.Sprintf("%.0f 毫秒", ms)
}

func formatDigestGrowth(delta int64, days float64) string {
	if delta >= 0 {
		return fmt.Sprintf("%.0f 天 +%s", days, formatDigestBytes(delta))
	}
	return fmt.Sprintf("%.0f 天 -%s", days, formatDigestBytes(-delta))
}

func formatDigestBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
