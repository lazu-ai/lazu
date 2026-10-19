package model

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GatewayTimingDay struct {
	Day         string   `json:"day"`
	Samples     int64    `json:"samples"`
	PostSamples int64    `json:"post_samples"`
	PreP50      *float64 `json:"gateway_pre_p50_ms"`
	PreP95      *float64 `json:"gateway_pre_p95_ms"`
	PostP50     *float64 `json:"gateway_post_p50_ms"`
	PostP95     *float64 `json:"gateway_post_p95_ms"`
}

// The timings are typed columns (gateway_timing_columns.go), so a day costs
// a scan of its rows, not a JSON decode of each. Formatting distinct UTC dates
// avoids formatting every log; aggregating by the formatted label keeps the
// historical YYYY behavior for BC dates as well.
const postgresGatewayPerformanceQuery = `WITH measured AS MATERIALIZED (
 SELECT (to_timestamp(request_ts / 1000.0) AT TIME ZONE 'UTC')::date AS day,
 gateway_pre_ms AS pre, gateway_post_ms AS post
 FROM logs WHERE request_ts >= ? AND request_ts < ?),
 day_names AS MATERIALIZED (
 SELECT day,to_char(day, 'YYYY-MM-DD') AS label FROM (SELECT DISTINCT day FROM measured) observed)
 SELECT day_names.label AS day,count(*) AS row_count,count(pre) AS samples,count(post) AS post_samples,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY pre) AS pre_p50,
 percentile_cont(0.95) WITHIN GROUP(ORDER BY pre) AS pre_p95,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY post) AS post_p50,
 percentile_cont(0.95) WITHIN GROUP(ORDER BY post) AS post_p95
 FROM measured JOIN day_names USING(day) GROUP BY day_names.label ORDER BY day_names.label`

// GatewayTimingDaily is one finished UTC day of gateway timing. A day ends
// for good an hour after midnight, once late diagnostics have merged, and is
// computed once instead of on every read: the thirty-day operations page
// scanned 350k log rows per open, 1.3 s on 2026-10-16. RowCount keeps a day
// that had requests but no timing visible, as the scan reported it.
type GatewayTimingDaily struct {
	Day         string    `gorm:"primaryKey;size:16"`
	RowCount    int64     `gorm:"not null;default:0"`
	Samples     int64     `gorm:"not null;default:0"`
	PostSamples int64     `gorm:"not null;default:0"`
	PreP50      *float64  `gorm:"type:float8"`
	PreP95      *float64  `gorm:"type:float8"`
	PostP50     *float64  `gorm:"type:float8"`
	PostP95     *float64  `gorm:"type:float8"`
	ComputedAt  time.Time `gorm:"not null"`
}

func (GatewayTimingDaily) TableName() string { return "gateway_timing_daily" }

// gatewayTimingStoredDays bounds the stored days a read may fill; logs only
// live thirty days, and older requests are scanned as before.
const gatewayTimingStoredDays = 60

type gatewayTimingDayRow struct {
	GatewayTimingDay
	RowCount int64
}

func (row GatewayTimingDaily) day() GatewayTimingDay {
	return GatewayTimingDay{Day: row.Day, Samples: row.Samples, PostSamples: row.PostSamples, PreP50: row.PreP50, PreP95: row.PreP95, PostP50: row.PostP50, PostP95: row.PostP95}
}

func scanGatewayTimingDays(db *gorm.DB, start, end time.Time) ([]gatewayTimingDayRow, error) {
	var rows []gatewayTimingDayRow
	if !end.After(start) {
		return rows, nil
	}
	err := db.Raw(postgresGatewayPerformanceQuery, start.UnixMilli(), end.UnixMilli()).Scan(&rows).Error
	return rows, err
}

// GatewayPerformanceDays uses the telemetry store and request partition key.
// Missing instrumentation remains null; it must not paint a fictitious 0ms.
// Whole finished days come from gateway_timing_daily, filled on first read;
// the open day and any partial edge are scanned.
func GatewayPerformanceDays(ctx context.Context, start, end time.Time) ([]GatewayTimingDay, error) {
	store := TelemetryDB()
	if store == nil {
		return nil, errors.New("telemetry database unavailable")
	}
	db := store.WithContext(ctx)
	if db.Dialector.Name() == "postgres" {
		return postgresGatewayPerformanceDays(db, start.UTC(), end.UTC(), time.Now().UTC())
	}
	// SQLite/MySQL development fixtures use the same percentile interpolation.
	var rows []struct {
		RequestTS int64
		Other     string
	}
	if err := db.Model(&Log{}).Select("request_ts", "other").Where("request_ts >= ? AND request_ts < ?", start.UnixMilli(), end.UnixMilli()).Find(&rows).Error; err != nil {
		return nil, err
	}
	type samples struct{ pre, post []float64 }
	days := map[string]*samples{}
	for _, row := range rows {
		day := time.UnixMilli(row.RequestTS).UTC().Format("2006-01-02")
		var facts map[string]interface{}
		if json.Unmarshal([]byte(row.Other), &facts) != nil {
			continue
		}
		values := days[day]
		if values == nil {
			values = &samples{}
			days[day] = values
		}
		if value, ok := facts["gateway_pre_ms"].(float64); ok && value >= 0 {
			values.pre = append(values.pre, value)
		}
		if value, ok := facts["gateway_post_ms"].(float64); ok && value >= 0 {
			values.post = append(values.post, value)
		}
	}
	result := make([]GatewayTimingDay, 0, len(days))
	for day, values := range days {
		result = append(result, GatewayTimingDay{Day: day, Samples: int64(len(values.pre)), PostSamples: int64(len(values.post)), PreP50: timingPercentile(values.pre, .5), PreP95: timingPercentile(values.pre, .95), PostP50: timingPercentile(values.post, .5), PostP95: timingPercentile(values.post, .95)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Day < result[j].Day })
	return result, nil
}
func postgresGatewayPerformanceDays(db *gorm.DB, start, end, now time.Time) ([]GatewayTimingDay, error) {
	closed := now.Add(-time.Hour).Truncate(24 * time.Hour)
	from := start.Truncate(24 * time.Hour)
	if from.Before(start) {
		from = from.Add(24 * time.Hour)
	}
	if floor := closed.AddDate(0, 0, -gatewayTimingStoredDays); from.Before(floor) {
		from = floor
	}
	to := end.Truncate(24 * time.Hour)
	if to.After(closed) {
		to = closed
	}
	var result []GatewayTimingDay
	appendScanned := func(rows []gatewayTimingDayRow) {
		for _, row := range rows {
			result = append(result, row.GatewayTimingDay)
		}
	}
	if !from.Before(to) {
		rows, err := scanGatewayTimingDays(db, start, end)
		appendScanned(rows)
		return result, err
	}
	head, err := scanGatewayTimingDays(db, start, from)
	if err != nil {
		return nil, err
	}
	appendScanned(head)
	days, err := storedGatewayTimingDays(db, from, to, now)
	if err != nil {
		return nil, err
	}
	result = append(result, days...)
	tail, err := scanGatewayTimingDays(db, to, end)
	if err != nil {
		return nil, err
	}
	appendScanned(tail)
	sort.Slice(result, func(i, j int) bool { return result[i].Day < result[j].Day })
	return result, nil
}

// storedGatewayTimingDays returns the finished days in [from, to), scanning
// and storing the ones not yet stored in a single pass over their span.
func storedGatewayTimingDays(db *gorm.DB, from, to, now time.Time) ([]GatewayTimingDay, error) {
	var stored []GatewayTimingDaily
	if err := db.Where("day >= ? AND day < ?", from.Format("2006-01-02"), to.Format("2006-01-02")).Find(&stored).Error; err != nil {
		return nil, err
	}
	byDay := make(map[string]GatewayTimingDaily, len(stored))
	for _, row := range stored {
		byDay[row.Day] = row
	}
	var first, last time.Time
	for day := from; day.Before(to); day = day.Add(24 * time.Hour) {
		if _, found := byDay[day.Format("2006-01-02")]; !found {
			if first.IsZero() {
				first = day
			}
			last = day
		}
	}
	if !first.IsZero() {
		scanned, err := scanGatewayTimingDays(db, first, last.Add(24*time.Hour))
		if err != nil {
			return nil, err
		}
		found := make(map[string]gatewayTimingDayRow, len(scanned))
		for _, row := range scanned {
			found[row.Day] = row
		}
		fill := make([]GatewayTimingDaily, 0)
		for day := first; !day.After(last); day = day.Add(24 * time.Hour) {
			key := day.Format("2006-01-02")
			if _, done := byDay[key]; done {
				continue
			}
			row := found[key]
			daily := GatewayTimingDaily{Day: key, RowCount: row.RowCount, Samples: row.Samples, PostSamples: row.PostSamples, PreP50: row.PreP50, PreP95: row.PreP95, PostP50: row.PostP50, PostP95: row.PostP95, ComputedAt: now}
			fill = append(fill, daily)
			byDay[key] = daily
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(fill, 100).Error; err != nil {
			return nil, err
		}
	}
	result := make([]GatewayTimingDay, 0, len(byDay))
	for _, row := range byDay {
		if row.RowCount > 0 {
			result = append(result, row.day())
		}
	}
	return result, nil
}

func timingPercentile(values []float64, fraction float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sort.Float64s(values)
	rank := float64(len(values)-1) * fraction
	lower, upper := int(math.Floor(rank)), int(math.Ceil(rank))
	value := values[lower] + (values[upper]-values[lower])*(rank-float64(lower))
	return &value
}
func GatewayPreparationRegression(days []GatewayTimingDay, day string) (current, baseline float64, breached bool) {
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		return
	}
	previous := make([]float64, 0, 7)
	for _, point := range days {
		if point.PreP50 == nil || point.Samples == 0 {
			continue
		}
		if point.Day == day {
			current = *point.PreP50
		}
		pointDay, err := time.Parse("2006-01-02", point.Day)
		if err != nil {
			continue
		}
		if !pointDay.Before(parsed.AddDate(0, 0, -7)) && pointDay.Before(parsed) {
			previous = append(previous, *point.PreP50)
		}
	}
	if len(previous) != 7 {
		return
	}
	baseline = *timingPercentile(previous, .5)
	breached = current > 20 && current > baseline*1.5
	return
}
