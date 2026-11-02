package model

import (
	"fmt"

	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

// LogRangeStat is the strip above the API log: the range's spend and its
// hourly series, what the range before it cost, and how the requests went.
// Every figure honors the log's filters; none looks at only the last minute.
type LogRangeStat struct {
	AmountMicrousd billing.MicroUSD `json:"amount_microusd"`
	// The same filters over the window just before this one, as long as it.
	// Absent when the range has no start.
	PreviousAmountMicrousd *billing.MicroUSD `json:"previous_amount_microusd,omitempty"`
	RequestCount           int64             `json:"request_count"`
	ErrorCount             int64             `json:"error_count"`
	PromptTokens           int64             `json:"prompt_tokens"`
	CachedInputTokens      int64             `json:"cached_input_tokens"`
	CacheHitRatio          float64           `json:"cache_hit_ratio"`
	// Median time to the first token of streamed requests; absent when none
	// streamed.
	MedianTTFTMs *float64 `json:"median_ttft_ms,omitempty"`
	// Hour buckets (unix seconds at the top of the hour), oldest first. The
	// console folds them into the reader's own days.
	Series []LogStatBucket `json:"series"`
}

type LogStatBucket struct {
	Bucket         int64            `json:"bucket"`
	AmountMicrousd billing.MicroUSD `json:"amount_microusd"`
	RequestCount   int64            `json:"request_count"`
}

// logStatQuery returns the caller's filtered logs of one type within
// [start, end]; zero bounds are open.
type logStatQuery func(logType int, start, end int64) *gorm.DB

func buildLogRangeStat(query logStatQuery, start, end int64) (LogRangeStat, error) {
	stat := LogRangeStat{Series: []LogStatBucket{}}
	var totals struct {
		AmountMicrousd    billing.MicroUSD
		RequestCount      int64
		PromptTokens      int64
		CachedInputTokens int64
	}
	if err := query(LogTypeConsume, start, end).Select("COALESCE(SUM(logs.amount_microusd),0) AS amount_microusd, COUNT(*) AS request_count, " +
		"COALESCE(SUM(logs.prompt_tokens),0) AS prompt_tokens, COALESCE(SUM(logs.ro_cached_input_tokens),0) AS cached_input_tokens").Scan(&totals).Error; err != nil {
		return stat, err
	}
	stat.AmountMicrousd, stat.RequestCount = totals.AmountMicrousd, totals.RequestCount
	stat.PromptTokens, stat.CachedInputTokens = totals.PromptTokens, totals.CachedInputTokens
	stat.CacheHitRatio = dashboardCacheHitRatio(totals.CachedInputTokens, totals.PromptTokens)
	if err := query(LogTypeError, start, end).Count(&stat.ErrorCount).Error; err != nil {
		return stat, err
	}

	hour := "logs.created_at - (logs.created_at % 3600)"
	if err := query(LogTypeConsume, start, end).Select(hour + " AS bucket, COALESCE(SUM(logs.amount_microusd),0) AS amount_microusd, COUNT(*) AS request_count").
		Group(hour).Order("bucket ASC").Scan(&stat.Series).Error; err != nil {
		return stat, err
	}

	if start > 0 {
		upper := end
		if upper <= start {
			upper = start
		}
		var previous billing.MicroUSD
		if err := query(LogTypeConsume, start-(upper-start)-1, start-1).Select("COALESCE(SUM(logs.amount_microusd),0)").Scan(&previous).Error; err != nil {
			return stat, err
		}
		stat.PreviousAmountMicrousd = &previous
	}

	streamed := func() *gorm.DB {
		return query(LogTypeConsume, start, end).Where("logs.ro_was_streaming = ? AND logs.ro_ttft_ms IS NOT NULL AND logs.ro_ttft_ms >= 0", true)
	}
	median, err := medianOf(streamed, "logs.ro_ttft_ms")
	if err != nil {
		return stat, err
	}
	stat.MedianTTFTMs = median
	return stat, nil
}

// medianOf is the median of column over the rows query returns, nil when
// there are none. PostgreSQL computes it; elsewhere the middle rows are read.
func medianOf(query func() *gorm.DB, column string) (*float64, error) {
	if dashboardUsingPostgreSQL() {
		var row struct {
			Count  int64
			Median float64
		}
		if err := query().Select(fmt.Sprintf("COUNT(*) AS count, COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY %s), 0) AS median", column)).Scan(&row).Error; err != nil {
			return nil, err
		}
		if row.Count == 0 {
			return nil, nil
		}
		return &row.Median, nil
	}
	var count int64
	if err := query().Count(&count).Error; err != nil || count == 0 {
		return nil, err
	}
	var samples []float64
	if err := query().Order(column+" ASC").Offset(int((count-1)/2)).Limit(int(2-count%2)).Pluck(column, &samples).Error; err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, nil
	}
	value := samples[0]
	if len(samples) > 1 {
		value = (samples[0] + samples[1]) / 2
	}
	return &value, nil
}

// ProjectLogsRangeStat answers the strip for a project's log under the same
// filters as the list, whatever result the list itself is narrowed to.
func ProjectLogsRangeStat(access ProjectLogAccess, filter ProjectLogFilter) (LogRangeStat, error) {
	return buildLogRangeStat(func(logType int, start, end int64) *gorm.DB {
		f := filter
		f.Type, f.Rejected, f.Start, f.End = logType, false, start, end
		return ProjectLogsQuery(access, f)
	}, filter.Start, filter.End)
}

// AdminLogFilter is the operator log's filter set.
type AdminLogFilter struct {
	Start, End                     int64
	ModelName, Username, TokenName string
	Group                          string
	UserID, Channel                int
}

// AdminLogsRangeStat answers the strip for the operator's log.
func AdminLogsRangeStat(filter AdminLogFilter) (LogRangeStat, error) {
	return buildLogRangeStat(func(logType int, start, end int64) *gorm.DB {
		tx := TelemetryDB().Model(&Log{}).Where("logs.type = ?", logType)
		if filter.UserID > 0 {
			tx = tx.Where("logs.user_id = ?", filter.UserID)
		} else if filter.Username != "" {
			tx = tx.Where("logs.username = ?", filter.Username)
		}
		if filter.TokenName != "" {
			tx = tx.Where("logs.token_name = ?", filter.TokenName)
		}
		if start > 0 {
			tx = tx.Where("logs.created_at >= ?", start)
		}
		if end > 0 {
			tx = tx.Where("logs.created_at <= ?", end)
		}
		if filter.ModelName != "" {
			tx = tx.Where("logs.model_name LIKE ?", filter.ModelName)
		}
		if filter.Channel != 0 {
			tx = tx.Where("logs.channel_id = ?", filter.Channel)
		}
		if filter.Group != "" {
			tx = tx.Where("logs."+logGroupCol+" = ?", filter.Group)
		}
		return tx
	}, filter.Start, filter.End)
}
