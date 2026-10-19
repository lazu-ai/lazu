package metrics

import (
	"context"

	"gorm.io/gorm"
)

const (
	ZeroTokenQuotaZeroMetricName = "relay_zero_token_quota_zero_rate" // secret-scan: allow metric name
	logTypeConsume               = 2
)

type ZeroTokenQuotaZeroSnapshot struct {
	Total int64
	Zero  int64
	Rate  float64
}

func ComputeZeroTokenQuotaZeroRate(total int64, zero int64) float64 {
	if total <= 0 || zero <= 0 {
		return 0
	}
	return float64(zero) / float64(total)
}

func QueryZeroTokenQuotaZeroRate(ctx context.Context, db *gorm.DB, since int64, until int64) (ZeroTokenQuotaZeroSnapshot, error) {
	if db == nil {
		return ZeroTokenQuotaZeroSnapshot{}, nil
	}
	base := db.WithContext(ctx).Table("logs").
		Where("type = ? AND created_at >= ? AND created_at < ?", logTypeConsume, since, until)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return ZeroTokenQuotaZeroSnapshot{}, err
	}
	var zero int64
	if err := base.Where("amount_microusd = 0 AND prompt_tokens = 0 AND completion_tokens = 0").Count(&zero).Error; err != nil {
		return ZeroTokenQuotaZeroSnapshot{}, err
	}
	return ZeroTokenQuotaZeroSnapshot{
		Total: total,
		Zero:  zero,
		Rate:  ComputeZeroTokenQuotaZeroRate(total, zero),
	}, nil
}
