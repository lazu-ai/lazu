package model

import (
	"math"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm/clause"
)

// ModelMetadata stores release dates and editorial metadata for models.
type ModelMetadata struct {
	ID        uint64 `json:"id" gorm:"primaryKey"`
	ModelName string `json:"model_name" gorm:"uniqueIndex;size:255"`

	FirstSeenAt time.Time  `json:"first_seen_at"`
	ReleasedAt  *time.Time `json:"released_at,omitempty"`
	IsFeatured  bool       `json:"is_featured" gorm:"index"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func EnsureModelMetadataForNames(names []string) error {
	if len(names) == 0 {
		return nil
	}
	unique := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		unique[name] = struct{}{}
	}
	if len(unique) == 0 {
		return nil
	}
	now := time.Now()
	records := make([]ModelMetadata, 0, len(unique))
	for name := range unique {
		records = append(records, ModelMetadata{ModelName: name, FirstSeenAt: now})
	}
	return CoreDB().Clauses(clause.OnConflict{DoNothing: true}).Create(&records).Error
}

// EnsureModelMetadataWithReleasedAt creates or updates metadata for a model,
// setting both FirstSeenAt and ReleasedAt. Used when admin manually adds a model.
func EnsureModelMetadataWithReleasedAt(modelName string, releasedAt time.Time) {
	if modelName == "" {
		return
	}
	record := ModelMetadata{
		ModelName:   modelName,
		FirstSeenAt: releasedAt,
		ReleasedAt:  &releasedAt,
	}
	result := CoreDB().Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "model_name"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"released_at": releasedAt,
			"updated_at":  releasedAt,
		}),
	}).Create(&record)
	if result.Error != nil {
		common.SysLog("failed to ensure model metadata for " + modelName + ": " + result.Error.Error())
	}
}

// IncrementModelRequests records lifetime usage in the telemetry store.
func IncrementModelRequests(modelName string) error {
	return recordModelRequestTx(TelemetryDB(), modelName)
}

// CatalogPopularity reads the existing daily model totals. Normalizing against
// the same maximum keeps the public catalog score independent of list filters.
func CatalogPopularity(now time.Time) map[string]float64 {
	db := TelemetryDB()
	if db == nil {
		return nil
	}
	var rows []struct {
		ModelName string
		Requests  int64
	}
	today := now.UTC().Truncate(24 * time.Hour)
	since := today.AddDate(0, 0, -7).Unix()
	if err := db.Model(&DashboardUsageDailyDimension{}).Select("model_name, SUM(request_count) AS requests").Where("dimension_type = ? AND day_start BETWEEN ? AND ?", "model", since, today.Unix()).Group("model_name").Scan(&rows).Error; err != nil {
		return nil
	}
	var peak int64
	for _, r := range rows {
		peak = max(peak, r.Requests)
	}
	if peak == 0 {
		return nil
	}
	scores := make(map[string]float64, len(rows))
	for _, r := range rows {
		scores[r.ModelName] = 100 * math.Log1p(float64(r.Requests)) / math.Log1p(float64(peak))
	}
	return scores
}
