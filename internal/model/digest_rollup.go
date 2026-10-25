package model

import (
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"gorm.io/gorm"
)

// DigestUsageHour is one payer's usage in one UTC hour, per model and source.
// Usage digests read it instead of the request logs, which production keeps
// for only 30 days: a monthly digest compares two whole months. Rows are
// kept for 400 days.
type DigestUsageHour struct {
	PayerUserID int    `gorm:"primaryKey;autoIncrement:false"`
	HourStart   int64  `gorm:"primaryKey;autoIncrement:false;index"`
	ModelName   string `gorm:"primaryKey;size:128"`
	Source      string `gorm:"primaryKey;size:32"`
	Spend       int64  `gorm:"not null;default:0"`
	Requests    int64  `gorm:"not null;default:0"`
	Errors      int64  `gorm:"not null;default:0"`
	Tokens      int64  `gorm:"not null;default:0"`
	Cache       int64  `gorm:"not null;default:0"`
	Official    int64  `gorm:"not null;default:0"`
	Compared    int64  `gorm:"not null;default:0"`
}

// DigestRollupSinceOption records the first hour the rollup holds in full;
// a digest period that starts earlier is reported as incomplete.
const DigestRollupSinceOption = "UsageDigestRollupSince"

const digestRollupRetention = 400 * 24 * time.Hour

type digestHourKey struct {
	payer  int
	hour   int64
	model  string
	source string
}

// RollupDigestHours recomputes [from, to) (whole hours) from the logs and
// replaces those hours in the rollup. Running it twice gives the same rows.
func RollupDigestHours(from, to time.Time) error {
	from, to = from.UTC().Truncate(time.Hour), to.UTC().Truncate(time.Hour)
	if !to.After(from) || TelemetryDB() == nil || TelemetryDB() == nil {
		return nil
	}
	type row struct {
		CreatedAt        int64
		Type             int
		UserId           int
		PayerUserID      int
		AmountMicrousd   int64
		PromptTokens     int64
		CompletionTokens int64
		ModelName        string
		ClientFramework  string
		LineItems        string
		UsageFact        string
	}
	rows, err := TelemetryDB().Model(&Log{}).
		Select("created_at, type, user_id, payer_user_id, amount_microusd, prompt_tokens, completion_tokens, model_name, client_framework, line_items, usage_fact").
		Where("created_at >= ? AND created_at < ? AND type IN ?", from.Unix(), to.Unix(), []int{LogTypeConsume, LogTypeError}).
		Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	agg := map[digestHourKey]*DigestUsageHour{}
	for rows.Next() {
		var r row
		if err := TelemetryDB().ScanRows(rows, &r); err != nil {
			rows.Close()
			return err
		}
		payer := r.PayerUserID
		if payer <= 0 {
			payer = r.UserId
		}
		if payer <= 0 {
			continue
		}
		tokens, err := checkedTelemetryInt64Total(r.PromptTokens, r.CompletionTokens)
		if err != nil {
			return err
		}
		if err := validateTelemetryUsageFactNumbers(r.UsageFact); err != nil {
			return err
		}
		key := digestHourKey{payer: payer, hour: r.CreatedAt - r.CreatedAt%3600, model: truncateString(strings.TrimSpace(r.ModelName), 128), source: DigestSourceKey(r.ClientFramework)}
		item := agg[key]
		if item == nil {
			item = &DigestUsageHour{PayerUserID: key.payer, HourStart: key.hour, ModelName: key.model, Source: key.source}
			agg[key] = item
		}
		if r.Type == LogTypeError {
			if err := addTelemetryCounters(telemetryDelta{&item.Errors, 1}); err != nil {
				return err
			}
			continue
		}
		if err := addTelemetryCounters(telemetryDelta{&item.Requests, 1}, telemetryDelta{&item.Spend, r.AmountMicrousd}, telemetryDelta{&item.Tokens, tokens}); err != nil {
			return err
		}
		if r.UsageFact != "" {
			if fact, ok := usagepkg.ParseFact(r.UsageFact); ok {
				cache, err := telemetryDimensionQuantity(fact, usagepkg.DimensionCacheRead)
				if err != nil {
					return err
				}
				if err := addTelemetryCounters(telemetryDelta{&item.Cache, cache}); err != nil {
					return err
				}
			}
		}
		if official, ok := sessionOfficialPrice(r.LineItems, r.AmountMicrousd); ok {
			if err := addTelemetryCounters(telemetryDelta{&item.Official, official}, telemetryDelta{&item.Compared, r.AmountMicrousd}); err != nil {
				return err
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	batch := make([]DigestUsageHour, 0, len(agg))
	for _, item := range agg {
		batch = append(batch, *item)
	}
	return TelemetryDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("hour_start >= ? AND hour_start < ?", from.Unix(), to.Unix()).Delete(&DigestUsageHour{}).Error; err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		return tx.CreateInBatches(batch, 500).Error
	})
}

// DigestRollupSince is the first fully rolled-up hour, or 0 before the
// first backfill.
func DigestRollupSince() int64 {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[DigestRollupSinceOption]
	common.OptionMapRWMutex.RUnlock()
	since, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return since
}

func backfillDigestRollupForMigration(now time.Time) error {
	return backfillDigestRollup(now, updateOptionForMigration)
}

func backfillDigestRollup(now time.Time, recordProgress func(string, string) error) error {
	if DigestRollupSince() > 0 || TelemetryDB() == nil {
		return nil
	}
	var first struct{ Min *int64 }
	if err := TelemetryDB().Model(&Log{}).Select("MIN(created_at) AS min").Where("type = ?", LogTypeConsume).Scan(&first).Error; err != nil {
		return err
	}
	end := now.UTC().Truncate(time.Hour)
	if first.Min == nil {
		return recordProgress(DigestRollupSinceOption, strconv.FormatInt(end.Unix(), 10))
	}
	// The oldest hour may already be half purged: start at the next one.
	start := time.Unix(*first.Min, 0).UTC().Truncate(time.Hour).Add(time.Hour)
	for day := start; day.Before(end); day = day.Add(24 * time.Hour) {
		stop := day.Add(24 * time.Hour)
		if stop.After(end) {
			stop = end
		}
		if err := RollupDigestHours(day, stop); err != nil {
			return err
		}
	}
	return recordProgress(DigestRollupSinceOption, strconv.FormatInt(start.Unix(), 10))
}

// PurgeDigestRollup drops rollup hours past retention.
func PurgeDigestRollup(now time.Time) error {
	return TelemetryDB().Where("hour_start < ?", now.Add(-digestRollupRetention).Unix()).Delete(&DigestUsageHour{}).Error
}
