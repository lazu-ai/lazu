package model

import (
	"math"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ModelMetadata stores ranking-related data for models.
type ModelMetadata struct {
	ID        uint64 `json:"id" gorm:"primaryKey"`
	ModelName string `json:"model_name" gorm:"uniqueIndex;size:255"`

	FirstSeenAt   time.Time  `json:"first_seen_at"`
	ReleasedAt    *time.Time `json:"released_at,omitempty"`
	LastUpdatedAt *time.Time `json:"last_updated_at,omitempty"`

	TotalRequests   int64 `json:"total_requests"`
	WeeklyRequests  int64 `json:"weekly_requests"`
	MonthlyRequests int64 `json:"monthly_requests"`

	PopularityScore float64 `json:"popularity_score"`
	FreshnessScore  float64 `json:"freshness_score"`
	ValueScore      float64 `json:"value_score"`
	VendorScore     float64 `json:"vendor_score"`
	FinalRankScore  float64 `json:"final_rank_score" gorm:"index"`

	ManualBoost int  `json:"manual_boost"`
	IsFeatured  bool `json:"is_featured" gorm:"index"`
	IsNew       bool `json:"is_new"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ModelRankingWindowDays is the longest rolling request window. Automatic log
// retention must keep at least this much source data for a complete snapshot.
const ModelRankingWindowDays = 30

const (
	popularityWeight = 0.30
	freshnessWeight  = 0.20
	valueWeight      = 0.15
	vendorWeight     = 0.10
	rankingWeightSum = popularityWeight + freshnessWeight + valueWeight + vendorWeight
)

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

// IncrementModelRequests increments lifetime usage and creates metadata if needed.
// Rolling counters are owned exclusively by the hourly log refresh: incrementing
// them here races with publication of a log snapshot and loses or doubles usage.
func IncrementModelRequests(modelName string) error {
	return recordModelRequestTx(TelemetryDB(), modelName)
}

func resetModelRequestCounter(db *gorm.DB, column string) error {
	return db.Session(&gorm.Session{AllowGlobalUpdate: true}).Model(&ModelMetadata{}).Update(column, 0).Error
}

// UpdateAllRankScores recalculates ranking metrics for all models.
func UpdateAllRankScores() error {
	var allMeta []ModelMetadata
	if err := CoreDB().Find(&allMeta).Error; err != nil {
		return err
	}
	if len(allMeta) == 0 {
		return nil
	}

	maxWeekly := int64(0)
	for _, item := range allMeta {
		if item.WeeklyRequests > maxWeekly {
			maxWeekly = item.WeeklyRequests
		}
	}

	priceMap := buildPerCallPriceMap()

	for i := range allMeta {
		meta := &allMeta[i]
		meta.PopularityScore = calculatePopularityScore(meta.WeeklyRequests, maxWeekly)
		meta.FreshnessScore, meta.IsNew = calculateFreshnessScore(meta)
		meta.ValueScore = calculateValueScore(meta.ModelName, priceMap)
		meta.VendorScore = clampScore(meta.VendorScore)
		meta.FinalRankScore = calculateFinalScore(meta)

		if err := CoreDB().Model(meta).Updates(map[string]interface{}{
			"popularity_score": meta.PopularityScore,
			"freshness_score":  meta.FreshnessScore,
			"value_score":      meta.ValueScore,
			"vendor_score":     meta.VendorScore,
			"final_rank_score": meta.FinalRankScore,
			"is_new":           meta.IsNew,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func calculatePopularityScore(weeklyRequests, maxWeekly int64) float64 {
	if maxWeekly <= 0 || weeklyRequests <= 0 {
		return 0
	}
	return math.Log1p(float64(weeklyRequests)) / math.Log1p(float64(maxWeekly)) * 100
}

func calculateFreshnessScore(meta *ModelMetadata) (float64, bool) {
	reference := meta.FirstSeenAt
	if meta.ReleasedAt != nil {
		reference = *meta.ReleasedAt
	}
	if reference.IsZero() {
		return 50, false
	}
	days := time.Since(reference).Hours() / 24
	if days <= 30 {
		return 100, true
	}
	if days <= 90 {
		return 100 - (days-30)*0.5, false
	}
	if days <= 365 {
		return 70 - (days-90)*0.1, false
	}
	return 50, false
}

func calculateValueScore(modelName string, priceMap map[string]float64) float64 {
	price, ok := priceMap[modelName]
	if !ok || price <= 0 {
		return 50
	}
	minPrice, maxPrice := priceMap["__min"], priceMap["__max"]
	if maxPrice <= minPrice {
		return 50
	}
	normalized := 1 - (price-minPrice)/(maxPrice-minPrice)
	return clampScore(normalized * 100)
}

func calculateFinalScore(meta *ModelMetadata) float64 {
	base := meta.PopularityScore*popularityWeight +
		meta.FreshnessScore*freshnessWeight +
		meta.ValueScore*valueWeight +
		meta.VendorScore*vendorWeight
	// Quality was retired with model ratings. Normalize the remaining weights so
	// the retained score keeps its 0–100 scale instead of capping at 75.
	base /= rankingWeightSum

	boostFactor := 1.0 + float64(meta.ManualBoost)/200.0
	finalScore := base * boostFactor
	if meta.IsFeatured {
		finalScore *= 1.2
	}
	return clampScore(finalScore)
}

func clampScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func buildPerCallPriceMap() map[string]float64 {
	pricing := GetPricing()
	priceMap := make(map[string]float64, len(pricing)+2)
	minPrice := math.MaxFloat64
	maxPrice := 0.0
	for _, item := range pricing {
		if item.RequestUSD == nil || *item.RequestUSD <= 0 {
			continue
		}
		priceMap[item.ModelName] = *item.RequestUSD
		if *item.RequestUSD < minPrice {
			minPrice = *item.RequestUSD
		}
		if *item.RequestUSD > maxPrice {
			maxPrice = *item.RequestUSD
		}
	}
	if minPrice == math.MaxFloat64 {
		minPrice = 0
	}
	priceMap["__min"] = minPrice
	priceMap["__max"] = maxPrice
	return priceMap
}

// ListModelRankings returns ranking list sorted by final score.
func ListModelRankings(limit int, order string) ([]ModelMetadata, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	safeOrders := map[string]string{
		"final":    "final_rank_score desc",
		"popular":  "popularity_score desc",
		"fresh":    "freshness_score desc",
		"value":    "value_score desc",
		"newest":   "first_seen_at desc",
		"requests": "weekly_requests desc",
	}
	orderExpr := safeOrders[order]
	if orderExpr == "" {
		orderExpr = safeOrders["final"]
	}
	var list []ModelMetadata
	if err := CoreDB().Order(orderExpr).Limit(limit).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, hydrateLifetimeModelCounts(list)
}

// EnsureModelMetadataFromModels seeds metadata for existing model table entries.
func EnsureModelMetadataFromModels() error {
	var models []Model
	if err := CoreDB().Select("model_name").Find(&models).Error; err != nil {
		return err
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.ModelName)
	}
	return EnsureModelMetadataForNames(names)
}

// UpdateWeeklyRequestsFromLogs aggregates usage logs into weekly counters.
func UpdateWeeklyRequestsFromLogs() error { return updateModelWindowRequestsFromDaily(7) }

func UpdateMonthlyRequestsFromLogs() error { return updateModelWindowRequestsFromDaily(30) }

func updateModelWindowRequestsFromDaily(days int) error {
	if TelemetryDB() == nil {
		return nil
	}
	var rows []struct {
		ModelName string
		Requests  int64
	}
	since := time.Now().UTC().AddDate(0, 0, -days).Truncate(24 * time.Hour).Unix()
	if err := TelemetryDB().Model(&DashboardUsageDailyDimension{}).Select("model_name,SUM(request_count) AS requests").Where("dimension_type = ? AND day_start >= ?", "model", since).Group("model_name").Scan(&rows).Error; err != nil {
		return err
	}
	column := "monthly_requests"
	if days == 7 {
		column = "weekly_requests"
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := resetModelRequestCounter(tx, column); err != nil {
			return err
		}
		for _, row := range rows {
			if err := tx.Model(&ModelMetadata{}).Where("model_name = ?", row.ModelName).Update(column, row.Requests).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
func hydrateLifetimeModelCounts(list []ModelMetadata) error {
	if len(list) == 0 || TelemetryDB() == nil {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, row := range list {
		names = append(names, row.ModelName)
	}
	var counters []ModelRequestCounter
	if err := TelemetryDB().Where("model_name IN ?", names).Find(&counters).Error; err != nil {
		return err
	}
	values := make(map[string]int64, len(counters))
	for _, row := range counters {
		values[row.ModelName] = row.TotalRequests
	}
	for i := range list {
		list[i].TotalRequests = values[list[i].ModelName]
	}
	return nil
}
