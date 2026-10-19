package model

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	dashboardRollupScopeGlobal = 0
	dashboardRollupHourSeconds = int64(3600)

	dashboardDimensionModel   = "model"
	dashboardDimensionKey     = "key"
	dashboardDimensionUser    = "user"
	dashboardDimensionChannel = "channel"
)

var dashboardLatencyBucketUpperBoundsMs = []int{
	100,
	250,
	500,
	1000,
	2000,
	5000,
	10000,
	30000,
	60000,
	120000,
}

type DashboardUsageHourlyTotal struct {
	ID                uint   `json:"id" gorm:"primaryKey"`
	BucketHour        int64  `json:"bucket_hour" gorm:"index:idx_dashboard_hourly_total_scope,unique,priority:1;index"`
	ScopeUserID       int    `json:"scope_user_id" gorm:"index:idx_dashboard_hourly_total_scope,unique,priority:2;index"`
	RequestCount      int64  `json:"request_count" gorm:"default:0"`
	OutcomeCount      int64  `json:"outcome_count" gorm:"default:0"`
	AmountMicrousd    int64  `json:"amount_microusd" gorm:"default:0"`
	UpstreamMicrousd  int64  `json:"upstream_cost_microusd" gorm:"column:upstream_cost_microusd;default:0"`
	GrossMicrousd     int64  `json:"gross_margin_microusd" gorm:"column:gross_margin_microusd;default:0"`
	CostSampleCount   int64  `json:"cost_sample_count" gorm:"default:0"`
	PromptTokens      int64  `json:"prompt_tokens" gorm:"default:0"`
	CachedInputTokens int64  `json:"cached_input_tokens" gorm:"default:0"`
	CompletionTokens  int64  `json:"completion_tokens" gorm:"default:0"`
	ErrorCount        int64  `json:"error_count" gorm:"default:0"`
	LatencyMsSum      int64  `json:"latency_ms_sum" gorm:"default:0"`
	LatencyBuckets    string `json:"latency_buckets" gorm:"type:text"`
	// Time to first token of eligible streamed text outcomes (see
	// dashboardTTFTEligible), as a dense histogram on the route health
	// buckets. NULL buckets mark an hour written before schema 33 that the
	// upgrade has not yet filled.
	TTFTSamples int64     `json:"ttft_samples" gorm:"column:ttft_samples;default:0"`
	TTFTBuckets *string   `json:"ttft_buckets" gorm:"column:ttft_buckets;type:text"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type DashboardUsageHourlyDimension struct {
	ID                uint      `json:"id" gorm:"primaryKey"`
	BucketHour        int64     `json:"bucket_hour" gorm:"index:idx_dashboard_hourly_dimension,unique,priority:1;index"`
	ScopeUserID       int       `json:"scope_user_id" gorm:"index:idx_dashboard_hourly_dimension,unique,priority:2;index"`
	DimensionType     string    `json:"dimension_type" gorm:"type:varchar(32);index:idx_dashboard_hourly_dimension,unique,priority:3;index"`
	DimensionID       string    `json:"dimension_id" gorm:"type:varchar(191);index:idx_dashboard_hourly_dimension,unique,priority:4;index"`
	DimensionName     string    `json:"dimension_name" gorm:"type:varchar(191);index"`
	RequestCount      int64     `json:"request_count" gorm:"default:0"`
	OutcomeCount      int64     `json:"outcome_count" gorm:"default:0"`
	AmountMicrousd    int64     `json:"amount_microusd" gorm:"default:0"`
	UpstreamMicrousd  int64     `json:"upstream_cost_microusd" gorm:"column:upstream_cost_microusd;default:0"`
	GrossMicrousd     int64     `json:"gross_margin_microusd" gorm:"column:gross_margin_microusd;default:0"`
	CostSampleCount   int64     `json:"cost_sample_count" gorm:"default:0"`
	PromptTokens      int64     `json:"prompt_tokens" gorm:"default:0"`
	CachedInputTokens int64     `json:"cached_input_tokens" gorm:"default:0"`
	CompletionTokens  int64     `json:"completion_tokens" gorm:"default:0"`
	ErrorCount        int64     `json:"error_count" gorm:"default:0"`
	LatencyMsSum      int64     `json:"latency_ms_sum" gorm:"default:0"`
	LatencyBuckets    string    `json:"latency_buckets" gorm:"type:text"`
	LastUsedAt        int64     `json:"last_used_at" gorm:"default:0"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (DashboardUsageHourlyTotal) TableName() string {
	return "dashboard_usage_hourly_totals"
}

func (DashboardUsageHourlyDimension) TableName() string {
	return "dashboard_usage_hourly_dimensions"
}

type dashboardRollupLogRow struct {
	RequestID        string
	UserID           int
	Username         string
	TokenID          int
	TokenName        string
	ModelName        string
	AmountMicrousd   int64
	PromptTokens     int
	CompletionTokens int
	CreatedAt        int64
	ChannelID        int
}

type dashboardRollupOutcomeRow struct {
	RequestID          string
	UserID             *int
	ChannelID          *int
	ChannelName        string
	StatusCode         *int
	ActualInputTokens  *int
	CachedInputTokens  *int
	ActualOutputTokens *int
	UpstreamCostUSD    *float64
	ChargedCostUSD     *float64
	GrossMarginUSD     *float64
	TotalDurationMs    *int
	WasStreaming       *bool
	TTFTMs             *int
	AudioOutputTokens  *int
	ImageOutputTokens  *int
	OutcomeAt          time.Time
}

type dashboardRollupTotalAccumulator struct {
	RequestCount      int64
	OutcomeCount      int64
	AmountMicrousd    int64
	PromptTokens      int64
	CachedInputTokens int64
	CompletionTokens  int64
	ErrorCount        int64
	UpstreamMicrousd  int64 `gorm:"column:upstream_cost_microusd"`
	GrossMicrousd     int64 `gorm:"column:gross_margin_microusd"`
	CostSampleCount   int64
	LatencyMsSum      int64
	LatencyBuckets    []int64
	TTFTSamples       int64
	TTFTBuckets       []int64
}

type dashboardRollupDimensionAccumulator struct {
	ID                string
	Name              string
	RequestCount      int64
	OutcomeCount      int64
	AmountMicrousd    int64
	PromptTokens      int64
	CachedInputTokens int64
	CompletionTokens  int64
	ErrorCount        int64
	UpstreamMicrousd  int64 `gorm:"column:upstream_cost_microusd"`
	GrossMicrousd     int64 `gorm:"column:gross_margin_microusd"`
	CostSampleCount   int64
	LatencyMsSum      int64
	LatencyBuckets    []int64
	LastUsedAt        int64
}

type dashboardRollupBuildState struct {
	totals     map[int]*dashboardRollupTotalAccumulator
	dimensions map[string]*dashboardRollupDimensionAccumulator
}

type dashboardRollupDimensionQueryRow struct {
	DimensionID       string
	DimensionName     string
	RequestCount      int64
	OutcomeCount      int64
	AmountMicrousd    int64
	PromptTokens      int64
	CachedInputTokens int64
	CompletionTokens  int64
	ErrorCount        int64
	UpstreamMicrousd  int64 `gorm:"column:upstream_cost_microusd"`
	GrossMicrousd     int64 `gorm:"column:gross_margin_microusd"`
	CostSampleCount   int64
	LatencyMsSum      int64
	LatencyBuckets    string
	LastUsedAt        int64
}

func dashboardRollupEnabled() bool {
	return common.GetEnvOrDefaultBool("DASHBOARD_ROLLUP_ENABLED", true)
}

func dashboardRollupFallbackEnabled() bool {
	return common.GetEnvOrDefaultBool("DASHBOARD_ROLLUP_FALLBACK_ENABLED", true)
}

func dashboardRollupHour(ts int64) int64 {
	if ts <= 0 {
		return 0
	}
	return ts - (ts % dashboardRollupHourSeconds)
}

func dashboardRollupBucketEndExclusive(window DashboardUsageWindow) int64 {
	end := window.EndTimestamp
	now := common.GetTimestamp()
	if end <= 0 || end > now {
		end = now
	}
	if end < window.StartTimestamp {
		end = window.StartTimestamp
	}
	return dashboardRollupHour(end) + dashboardRollupHourSeconds
}

func dashboardRollupBucketRange(window DashboardUsageWindow) (int64, int64, int64) {
	start := dashboardRollupHour(window.StartTimestamp)
	endExclusive := dashboardRollupBucketEndExclusive(window)
	if endExclusive <= start {
		endExclusive = start + dashboardRollupHourSeconds
	}
	expected := (endExclusive - start) / dashboardRollupHourSeconds
	return start, endExclusive, expected
}

func newDashboardLatencyBuckets() []int64 {
	return make([]int64, len(dashboardLatencyBucketUpperBoundsMs)+1)
}

func addDashboardLatencyBucket(buckets []int64, latencyMs int) ([]int64, error) {
	if err := telemetryNonnegative(int64(latencyMs)); err != nil {
		return nil, err
	}
	if buckets == nil || len(buckets) != len(dashboardLatencyBucketUpperBoundsMs)+1 {
		buckets = newDashboardLatencyBuckets()
	}
	for i, upper := range dashboardLatencyBucketUpperBoundsMs {
		if latencyMs <= upper {
			if err := telemetryNonnegative(buckets[i]); err != nil {
				return nil, err
			}
			if err := addTelemetryCounters(telemetryDelta{&buckets[i], 1}); err != nil {
				return nil, err
			}
			return buckets, nil
		}
	}
	last := len(buckets) - 1
	if err := telemetryNonnegative(buckets[last]); err != nil {
		return nil, err
	}
	if err := addTelemetryCounters(telemetryDelta{&buckets[last], 1}); err != nil {
		return nil, err
	}
	return buckets, nil
}

func mergeDashboardLatencyBuckets(dst []int64, encoded string) []int64 {
	if dst == nil || len(dst) != len(dashboardLatencyBucketUpperBoundsMs)+1 {
		dst = newDashboardLatencyBuckets()
	}
	if strings.TrimSpace(encoded) == "" {
		return dst
	}
	var src []int64
	if err := json.Unmarshal([]byte(encoded), &src); err != nil {
		return dst
	}
	for i := range dst {
		if i < len(src) {
			dst[i] += src[i]
		}
	}
	return dst
}

func encodeDashboardLatencyBuckets(buckets []int64) string {
	if buckets == nil || len(buckets) != len(dashboardLatencyBucketUpperBoundsMs)+1 {
		buckets = newDashboardLatencyBuckets()
	}
	payload, err := json.Marshal(buckets)
	if err != nil {
		return "[]"
	}
	return string(payload)
}

func dashboardLatencyP95FromBuckets(buckets []int64, outcomeCount int64) float64 {
	if outcomeCount <= 0 || len(buckets) == 0 {
		return 0
	}
	target := int64(math.Ceil(float64(outcomeCount) * 0.95))
	if target <= 0 {
		target = 1
	}
	var cumulative int64
	for i, count := range buckets {
		cumulative += count
		if cumulative >= target {
			if i < len(dashboardLatencyBucketUpperBoundsMs) {
				return float64(dashboardLatencyBucketUpperBoundsMs[i])
			}
			return float64(dashboardLatencyBucketUpperBoundsMs[len(dashboardLatencyBucketUpperBoundsMs)-1])
		}
	}
	return 0
}

// dashboardTTFTEligible is the TTFT population of every dashboard read: a
// streamed text outcome with a measured first token. The raw percentile
// (scanDashboardTTFTStatsContext), the incremental writer, the hour refresh
// and the schema-33 backfill all count exactly these.
func dashboardTTFTEligible(outcome dashboardRollupOutcomeRow) (int64, bool) {
	if outcome.WasStreaming == nil || !*outcome.WasStreaming || outcome.TTFTMs == nil || *outcome.TTFTMs < 0 {
		return 0, false
	}
	if dashboardPointerInt(outcome.AudioOutputTokens) != 0 || dashboardPointerInt(outcome.ImageOutputTokens) != 0 {
		return 0, false
	}
	return int64(*outcome.TTFTMs), true
}

// TTFT histograms are dense JSON arrays over the route health buckets, so the
// ON CONFLICT merge that adds latency bins adds these the same way.
func newDashboardTTFTBuckets() []int64 { return make([]int64, routeHealthHistogramSize) }

func addDashboardTTFTBucket(buckets []int64, ms int64) []int64 {
	if len(buckets) != routeHealthHistogramSize {
		buckets = newDashboardTTFTBuckets()
	}
	buckets[routeHealthBucketOf(ms)]++
	return buckets
}

func encodeDashboardTTFTBuckets(buckets []int64) *string {
	if len(buckets) != routeHealthHistogramSize {
		buckets = newDashboardTTFTBuckets()
	}
	payload, err := json.Marshal(buckets)
	if err != nil {
		payload = []byte("[]")
	}
	encoded := string(payload)
	return &encoded
}

func mergeDashboardTTFTBuckets(dst []int64, encoded *string) []int64 {
	if len(dst) != routeHealthHistogramSize {
		dst = newDashboardTTFTBuckets()
	}
	if encoded == nil || strings.TrimSpace(*encoded) == "" {
		return dst
	}
	var src []int64
	if err := json.Unmarshal([]byte(*encoded), &src); err != nil {
		return dst
	}
	for i := range dst {
		if i < len(src) {
			dst[i] += src[i]
		}
	}
	return dst
}

// dashboardTTFTP95FromBuckets reads the 95th percentile back as its bucket's
// midpoint, within 9.6% of the exact value (route_health.go).
func dashboardTTFTP95FromBuckets(buckets []int64) (int64, *float64) {
	var histogram routeHealthHistogram
	for i := range histogram {
		if i < len(buckets) {
			histogram[i] = buckets[i]
		}
	}
	samples := histogram.total()
	if samples == 0 {
		return 0, nil
	}
	p95 := histogram.quantile(0.95)
	return samples, &p95
}

func dashboardRollupState() *dashboardRollupBuildState {
	return &dashboardRollupBuildState{
		totals:     map[int]*dashboardRollupTotalAccumulator{},
		dimensions: map[string]*dashboardRollupDimensionAccumulator{},
	}
}

func (s *dashboardRollupBuildState) total(scopeUserID int) *dashboardRollupTotalAccumulator {
	total, ok := s.totals[scopeUserID]
	if !ok {
		total = &dashboardRollupTotalAccumulator{LatencyBuckets: newDashboardLatencyBuckets(), TTFTBuckets: newDashboardTTFTBuckets()}
		s.totals[scopeUserID] = total
	}
	return total
}

func dashboardDimensionMapKey(scopeUserID int, dimensionType string, dimensionID string) string {
	return strconv.Itoa(scopeUserID) + "|" + dimensionType + "|" + dimensionID
}

func (s *dashboardRollupBuildState) dimension(scopeUserID int, dimensionType string, dimensionID string, name string) *dashboardRollupDimensionAccumulator {
	if dimensionID == "" {
		dimensionID = "unknown"
	}
	if name == "" {
		name = "unknown"
	}
	key := dashboardDimensionMapKey(scopeUserID, dimensionType, dimensionID)
	dim, ok := s.dimensions[key]
	if !ok {
		dim = &dashboardRollupDimensionAccumulator{
			ID:             dimensionID,
			Name:           name,
			LatencyBuckets: newDashboardLatencyBuckets(),
		}
		s.dimensions[key] = dim
	}
	if dim.Name == "" || dim.Name == "unknown" {
		dim.Name = name
	}
	return dim
}

func dashboardPointerInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func dashboardPointerFloat(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func dashboardStatusIsError(status *int) bool {
	return status != nil && *status >= 400
}

func dashboardCostUSDToMicrousd(costUSD float64) (int64, error) {
	value := math.Round(costUSD * float64(billing.MicroUSDPerUSD))
	// float64(MaxInt64) rounds to 2^63 and cannot be converted safely.
	if math.IsNaN(value) || math.IsInf(value, 0) || value >= float64(math.MaxInt64) || value < float64(math.MinInt64) {
		return 0, ErrTelemetryCounterRange
	}
	return int64(value), nil
}

func validateDashboardOutcomeNumbers(outcome dashboardRollupOutcomeRow) error {
	if _, err := checkedTelemetryTokenTotal(dashboardPointerInt(outcome.ActualInputTokens), dashboardPointerInt(outcome.ActualOutputTokens)); err != nil {
		return err
	}
	if err := telemetryNonnegative(int64(dashboardPointerInt(outcome.CachedInputTokens)), int64(dashboardPointerInt(outcome.TotalDurationMs))); err != nil {
		return err
	}
	for _, value := range []*float64{outcome.ChargedCostUSD, outcome.UpstreamCostUSD, outcome.GrossMarginUSD} {
		if value != nil {
			if _, err := dashboardCostUSDToMicrousd(*value); err != nil {
				return err
			}
		}
	}
	return nil
}

func dashboardAddCostMetrics(upstreamMicrousd *int64, grossMicrousd *int64, costSampleCount *int64, upstreamCostUSD *float64, grossMarginUSD *float64) error {
	if upstreamMicrousd == nil || grossMicrousd == nil || costSampleCount == nil || upstreamCostUSD == nil {
		return nil
	}
	upstream, err := dashboardCostUSDToMicrousd(*upstreamCostUSD)
	if err != nil {
		return err
	}
	gross := int64(0)
	if grossMarginUSD != nil {
		gross, err = dashboardCostUSDToMicrousd(*grossMarginUSD)
		if err != nil {
			return err
		}
	}
	return addTelemetryCounters(telemetryDelta{upstreamMicrousd, upstream}, telemetryDelta{grossMicrousd, gross}, telemetryDelta{costSampleCount, 1})
}

func dashboardAddOutcomeMetricsToTotal(total *dashboardRollupTotalAccumulator, outcome dashboardRollupOutcomeRow) error {
	if total == nil {
		return nil
	}
	if err := validateDashboardOutcomeNumbers(outcome); err != nil {
		return err
	}
	if err := addTelemetryCounters(telemetryDelta{&total.OutcomeCount, 1}, telemetryDelta{&total.CachedInputTokens, int64(dashboardPointerInt(outcome.CachedInputTokens))}); err != nil {
		return err
	}
	if dashboardStatusIsError(outcome.StatusCode) {
		if err := addTelemetryCounters(telemetryDelta{&total.ErrorCount, 1}); err != nil {
			return err
		}
	}
	latencyMs := dashboardPointerInt(outcome.TotalDurationMs)
	if latencyMs > 0 {
		if err := addTelemetryCounters(telemetryDelta{&total.LatencyMsSum, int64(latencyMs)}); err != nil {
			return err
		}
		var err error
		total.LatencyBuckets, err = addDashboardLatencyBucket(total.LatencyBuckets, latencyMs)
		if err != nil {
			return err
		}
	}
	if ttft, ok := dashboardTTFTEligible(outcome); ok {
		if err := addTelemetryCounters(telemetryDelta{&total.TTFTSamples, 1}); err != nil {
			return err
		}
		total.TTFTBuckets = addDashboardTTFTBucket(total.TTFTBuckets, ttft)
	}
	return nil
}

func dashboardAddLogToTotal(total *dashboardRollupTotalAccumulator, logRow dashboardRollupLogRow, outcome *dashboardRollupOutcomeRow) error {
	if total == nil {
		return nil
	}
	if _, err := checkedTelemetryTokenTotal(logRow.PromptTokens, logRow.CompletionTokens); err != nil {
		return err
	}
	if err := addTelemetryCounters(telemetryDelta{&total.RequestCount, 1}, telemetryDelta{&total.AmountMicrousd, logRow.AmountMicrousd}, telemetryDelta{&total.PromptTokens, int64(logRow.PromptTokens)}, telemetryDelta{&total.CompletionTokens, int64(logRow.CompletionTokens)}); err != nil {
		return err
	}
	if outcome != nil {
		return dashboardAddCostMetrics(&total.UpstreamMicrousd, &total.GrossMicrousd, &total.CostSampleCount, outcome.UpstreamCostUSD, outcome.GrossMarginUSD)
	}
	return nil
}

func dashboardAddLogDimension(dim *dashboardRollupDimensionAccumulator, logRow dashboardRollupLogRow, outcome *dashboardRollupOutcomeRow) error {
	if dim == nil {
		return nil
	}
	if _, err := checkedTelemetryTokenTotal(logRow.PromptTokens, logRow.CompletionTokens); err != nil {
		return err
	}
	if err := addTelemetryCounters(telemetryDelta{&dim.RequestCount, 1}, telemetryDelta{&dim.AmountMicrousd, logRow.AmountMicrousd}, telemetryDelta{&dim.PromptTokens, int64(logRow.PromptTokens)}, telemetryDelta{&dim.CompletionTokens, int64(logRow.CompletionTokens)}); err != nil {
		return err
	}
	if logRow.CreatedAt > dim.LastUsedAt {
		dim.LastUsedAt = logRow.CreatedAt
	}
	if outcome == nil {
		return nil
	}
	if err := validateDashboardOutcomeNumbers(*outcome); err != nil {
		return err
	}
	if err := addTelemetryCounters(telemetryDelta{&dim.OutcomeCount, 1}, telemetryDelta{&dim.CachedInputTokens, int64(dashboardPointerInt(outcome.CachedInputTokens))}); err != nil {
		return err
	}
	if err := dashboardAddCostMetrics(&dim.UpstreamMicrousd, &dim.GrossMicrousd, &dim.CostSampleCount, outcome.UpstreamCostUSD, outcome.GrossMarginUSD); err != nil {
		return err
	}
	if dashboardStatusIsError(outcome.StatusCode) {
		if err := addTelemetryCounters(telemetryDelta{&dim.ErrorCount, 1}); err != nil {
			return err
		}
	}
	latencyMs := dashboardPointerInt(outcome.TotalDurationMs)
	if latencyMs > 0 {
		if err := addTelemetryCounters(telemetryDelta{&dim.LatencyMsSum, int64(latencyMs)}); err != nil {
			return err
		}
		var err error
		dim.LatencyBuckets, err = addDashboardLatencyBucket(dim.LatencyBuckets, latencyMs)
		if err != nil {
			return err
		}
	}
	return nil
}

func dashboardAddChannelOutcomeDimension(dim *dashboardRollupDimensionAccumulator, outcome dashboardRollupOutcomeRow) error {
	if dim == nil {
		return nil
	}
	if err := validateDashboardOutcomeNumbers(outcome); err != nil {
		return err
	}
	charged, err := dashboardCostUSDToMicrousd(dashboardPointerFloat(outcome.ChargedCostUSD))
	if err != nil {
		return err
	}
	if err := addTelemetryCounters(telemetryDelta{&dim.RequestCount, 1}, telemetryDelta{&dim.OutcomeCount, 1}, telemetryDelta{&dim.AmountMicrousd, charged}, telemetryDelta{&dim.PromptTokens, int64(dashboardPointerInt(outcome.ActualInputTokens))}, telemetryDelta{&dim.CachedInputTokens, int64(dashboardPointerInt(outcome.CachedInputTokens))}, telemetryDelta{&dim.CompletionTokens, int64(dashboardPointerInt(outcome.ActualOutputTokens))}); err != nil {
		return err
	}
	if err := dashboardAddCostMetrics(&dim.UpstreamMicrousd, &dim.GrossMicrousd, &dim.CostSampleCount, outcome.UpstreamCostUSD, outcome.GrossMarginUSD); err != nil {
		return err
	}
	if dashboardStatusIsError(outcome.StatusCode) {
		if err := addTelemetryCounters(telemetryDelta{&dim.ErrorCount, 1}); err != nil {
			return err
		}
	}
	latencyMs := dashboardPointerInt(outcome.TotalDurationMs)
	if latencyMs > 0 {
		if err := addTelemetryCounters(telemetryDelta{&dim.LatencyMsSum, int64(latencyMs)}); err != nil {
			return err
		}
		dim.LatencyBuckets, err = addDashboardLatencyBucket(dim.LatencyBuckets, latencyMs)
		if err != nil {
			return err
		}
	}
	if !outcome.OutcomeAt.IsZero() {
		lastUsedAt := outcome.OutcomeAt.Unix()
		if lastUsedAt > dim.LastUsedAt {
			dim.LastUsedAt = lastUsedAt
		}
	}
	return nil
}

func dashboardRefreshHourWindow(bucketHour int64) (time.Time, time.Time) {
	start := time.Unix(bucketHour, 0).UTC()
	return start, start.Add(time.Hour)
}

func RefreshDashboardUsageRollups(ctx context.Context, start time.Time, end time.Time) error {
	if TelemetryDB() == nil || TelemetryDB() == nil {
		return nil
	}
	startHour := dashboardRollupHour(start.Unix())
	endHour := dashboardRollupHour(end.Unix())
	if end.After(time.Unix(endHour, 0).UTC()) {
		endHour += dashboardRollupHourSeconds
	}
	if endHour <= startHour {
		endHour = startHour + dashboardRollupHourSeconds
	}
	for bucket := startHour; bucket < endHour; bucket += dashboardRollupHourSeconds {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := RefreshDashboardUsageRollupHour(ctx, bucket); err != nil {
				return err
			}
		}
	}
	return nil
}

func RefreshDashboardUsageRollupHour(ctx context.Context, bucketHour int64) error {
	if TelemetryDB() == nil || TelemetryDB() == nil {
		return nil
	}
	startTime, endTime := dashboardRefreshHourWindow(bucketHour)
	// The rollups outlive the request log, and a refresh deletes the hour
	// before rebuilding it from logs. An hour whose logs may already be purged
	// is therefore final: rebuilding it would replace real history with zeros.
	if dashboardHourPastLogRetention(startTime, time.Now()) {
		return nil
	}
	startUnix := startTime.Unix()
	endUnix := endTime.Unix()

	var logs []dashboardRollupLogRow
	if err := TelemetryDB().WithContext(ctx).Table("logs").
		Select("request_id, user_id, username, token_id, token_name, model_name, amount_microusd, prompt_tokens, completion_tokens, created_at, channel_id").
		Where("type = ? AND created_at >= ? AND created_at < ?", LogTypeConsume, startUnix, endUnix).
		Find(&logs).Error; err != nil {
		return err
	}

	var outcomes []dashboardRollupOutcomeRow
	if err := TelemetryDB().WithContext(ctx).Table("routing_outcome").
		Select("routing_outcome.request_id, COALESCE(routing_outcome.user_id, routing_decision.user_id) AS user_id, routing_outcome.channel_id, COALESCE(NULLIF(routing_outcome.channel_name, ''), NULLIF(channels.name,''), 'unknown') AS channel_name, routing_outcome.status_code, routing_outcome.actual_input_tokens, routing_outcome.cached_input_tokens, routing_outcome.actual_output_tokens, routing_outcome.upstream_cost_usd, routing_outcome.charged_cost_usd, routing_outcome.gross_margin_usd, routing_outcome.total_duration_ms, routing_outcome.was_streaming, routing_outcome.ttft_ms, routing_outcome.audio_output_tokens, routing_outcome.image_output_tokens, routing_outcome.outcome_at").
		Joins("LEFT JOIN routing_decision ON routing_decision.id = routing_outcome.decision_id").
		Joins("LEFT JOIN "+TelemetryChannelTable()+" channels ON channels.id = routing_outcome.channel_id").
		Where("routing_outcome.outcome_at >= ? AND routing_outcome.outcome_at < ?", startTime, endTime).
		Find(&outcomes).Error; err != nil {
		return err
	}

	state := dashboardRollupState()
	internalTokenIDs, err := hiddenTokenIDs()
	if err != nil {
		return err
	}
	internalTokenIDSet := make(map[int]struct{}, len(internalTokenIDs))
	for _, id := range internalTokenIDs {
		internalTokenIDSet[id] = struct{}{}
	}
	outcomeByRequestID := map[string]dashboardRollupOutcomeRow{}
	for _, outcome := range outcomes {
		outcomeByRequestID[outcome.RequestID] = outcome
		if err := dashboardAddOutcomeMetricsToTotal(state.total(dashboardRollupScopeGlobal), outcome); err != nil {
			return err
		}
		if outcome.UserID != nil && *outcome.UserID > 0 {
			if err := dashboardAddOutcomeMetricsToTotal(state.total(*outcome.UserID), outcome); err != nil {
				return err
			}
		}

		channelID := strconv.Itoa(dashboardPointerInt(outcome.ChannelID))
		channelName := outcome.ChannelName
		if err := dashboardAddChannelOutcomeDimension(state.dimension(dashboardRollupScopeGlobal, dashboardDimensionChannel, channelID, channelName), outcome); err != nil {
			return err
		}
		if outcome.UserID != nil && *outcome.UserID > 0 {
			if err := dashboardAddChannelOutcomeDimension(state.dimension(*outcome.UserID, dashboardDimensionChannel, channelID, channelName), outcome); err != nil {
				return err
			}
		}
	}

	for _, logRow := range logs {
		outcome, hasOutcome := outcomeByRequestID[logRow.RequestID]
		var outcomePtr *dashboardRollupOutcomeRow
		if hasOutcome {
			outcomePtr = &outcome
		}

		if err := dashboardAddLogToTotal(state.total(dashboardRollupScopeGlobal), logRow, outcomePtr); err != nil {

			return err

		}
		if logRow.UserID > 0 {
			if err := dashboardAddLogToTotal(state.total(logRow.UserID), logRow, outcomePtr); err != nil {
				return err
			}
		}

		modelName := dashboardRollupName(logRow.ModelName)
		keyID := strconv.Itoa(logRow.TokenID)
		keyName := dashboardRollupName(logRow.TokenName)
		userID := strconv.Itoa(logRow.UserID)
		userName := dashboardRollupName(logRow.Username)

		if err := dashboardAddLogDimension(state.dimension(dashboardRollupScopeGlobal, dashboardDimensionModel, modelName, modelName), logRow, outcomePtr); err != nil {

			return err

		}
		if _, hidden := internalTokenIDSet[logRow.TokenID]; !hidden {
			if err := dashboardAddLogDimension(state.dimension(dashboardRollupScopeGlobal, dashboardDimensionKey, keyID, keyName), logRow, outcomePtr); err != nil {
				return err
			}
		}
		if err := dashboardAddLogDimension(state.dimension(dashboardRollupScopeGlobal, dashboardDimensionUser, userID, userName), logRow, outcomePtr); err != nil {
			return err
		}

		if logRow.UserID > 0 {
			if err := dashboardAddLogDimension(state.dimension(logRow.UserID, dashboardDimensionModel, modelName, modelName), logRow, outcomePtr); err != nil {
				return err
			}
			if _, hidden := internalTokenIDSet[logRow.TokenID]; !hidden {
				if err := dashboardAddLogDimension(state.dimension(logRow.UserID, dashboardDimensionKey, keyID, keyName), logRow, outcomePtr); err != nil {
					return err
				}
			}
		}
	}

	if _, ok := state.totals[dashboardRollupScopeGlobal]; !ok {
		state.total(dashboardRollupScopeGlobal)
	}

	now := time.Now().UTC()
	totals := make([]DashboardUsageHourlyTotal, 0, len(state.totals))
	for scopeUserID, total := range state.totals {
		totals = append(totals, DashboardUsageHourlyTotal{
			BucketHour:        bucketHour,
			ScopeUserID:       scopeUserID,
			RequestCount:      total.RequestCount,
			OutcomeCount:      total.OutcomeCount,
			AmountMicrousd:    total.AmountMicrousd,
			UpstreamMicrousd:  total.UpstreamMicrousd,
			GrossMicrousd:     total.GrossMicrousd,
			CostSampleCount:   total.CostSampleCount,
			PromptTokens:      total.PromptTokens,
			CachedInputTokens: total.CachedInputTokens,
			CompletionTokens:  total.CompletionTokens,
			ErrorCount:        total.ErrorCount,
			LatencyMsSum:      total.LatencyMsSum,
			LatencyBuckets:    encodeDashboardLatencyBuckets(total.LatencyBuckets),
			TTFTSamples:       total.TTFTSamples,
			TTFTBuckets:       encodeDashboardTTFTBuckets(total.TTFTBuckets),
			UpdatedAt:         now,
		})
	}

	dimensions := make([]DashboardUsageHourlyDimension, 0, len(state.dimensions))
	for key, dim := range state.dimensions {
		parts := strings.SplitN(key, "|", 3)
		if len(parts) != 3 {
			continue
		}
		scopeUserID, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		dimensions = append(dimensions, DashboardUsageHourlyDimension{
			BucketHour:        bucketHour,
			ScopeUserID:       scopeUserID,
			DimensionType:     parts[1],
			DimensionID:       dim.ID,
			DimensionName:     dim.Name,
			RequestCount:      dim.RequestCount,
			OutcomeCount:      dim.OutcomeCount,
			AmountMicrousd:    dim.AmountMicrousd,
			UpstreamMicrousd:  dim.UpstreamMicrousd,
			GrossMicrousd:     dim.GrossMicrousd,
			CostSampleCount:   dim.CostSampleCount,
			PromptTokens:      dim.PromptTokens,
			CachedInputTokens: dim.CachedInputTokens,
			CompletionTokens:  dim.CompletionTokens,
			ErrorCount:        dim.ErrorCount,
			LatencyMsSum:      dim.LatencyMsSum,
			LatencyBuckets:    encodeDashboardLatencyBuckets(dim.LatencyBuckets),
			LastUsedAt:        dim.LastUsedAt,
			UpdatedAt:         now,
		})
	}

	return TelemetryDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("bucket_hour = ?", bucketHour).Delete(&DashboardUsageHourlyTotal{}).Error; err != nil {
			return err
		}
		if err := tx.Where("bucket_hour = ?", bucketHour).Delete(&DashboardUsageHourlyDimension{}).Error; err != nil {
			return err
		}
		if len(totals) > 0 {
			if err := tx.CreateInBatches(totals, 100).Error; err != nil {
				return err
			}
		}
		if len(dimensions) > 0 {
			if err := tx.CreateInBatches(dimensions, 200).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func dashboardRollupName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unknown"
	}
	return name
}

func BuildDashboardUsageFromRollupForRole(userID int, role int, startTimestamp int64, endTimestamp int64) (DashboardUsage, bool, error) {
	return BuildDashboardUsageFromRollupForRoleContext(context.Background(), userID, role, startTimestamp, endTimestamp)
}

func BuildDashboardUsageFromRollupForRoleContext(ctx context.Context, userID int, role int, startTimestamp int64, endTimestamp int64) (DashboardUsage, bool, error) {
	window := normalizeDashboardUsageWindow(startTimestamp, endTimestamp)
	result := DashboardUsage{
		Window: window,
	}
	if !dashboardRollupEnabled() || TelemetryDB() == nil {
		return result, false, nil
	}
	if !dashboardRollupWindowWholeHours(window) {
		return result, false, nil
	}
	if ready, err := dashboardRollupCoverageReadyContext(ctx, window); err != nil || !ready {
		return result, false, err
	}
	scopeUserID := dashboardRollupScopeGlobal
	if role >= common.RoleRootUser {
		result.Role = "admin"
		result.Scope = "global"
	} else {
		result.Role = "member"
		result.Scope = "user"
		scopeUserID = userID
	}

	startBucket, endExclusive, _ := dashboardRollupBucketRange(window)
	totals, err := dashboardRollupTotalsContext(ctx, scopeUserID, startBucket, endExclusive)
	if err != nil {
		return result, false, err
	}
	result.Totals = totals
	if role >= common.RoleRootUser {
		activeUsers, err := dashboardRollupDistinctDimensionCountContext(ctx, dashboardRollupScopeGlobal, dashboardDimensionUser, startBucket, endExclusive)
		if err != nil {
			return result, false, err
		}
		activeKeys, err := dashboardRollupDistinctDimensionCountContext(ctx, dashboardRollupScopeGlobal, dashboardDimensionKey, startBucket, endExclusive)
		if err != nil {
			return result, false, err
		}
		result.Totals.ActiveUsers = activeUsers
		result.Totals.ActiveKeys = activeKeys
	} else {
		activeKeys, err := dashboardRollupDistinctDimensionCountContext(ctx, scopeUserID, dashboardDimensionKey, startBucket, endExclusive)
		if err != nil {
			return result, false, err
		}
		result.Totals.ActiveKeys = activeKeys
	}

	result.ByModel, err = dashboardRollupDimensionRowsContext(ctx, scopeUserID, dashboardDimensionModel, startBucket, endExclusive, 20)
	if err != nil {
		return result, false, err
	}
	result.ByKey, err = dashboardRollupDimensionRowsContext(ctx, scopeUserID, dashboardDimensionKey, startBucket, endExclusive, 50)
	if err != nil {
		return result, false, err
	}
	if role >= common.RoleRootUser {
		result.ByUser, err = dashboardRollupDimensionRowsContext(ctx, scopeUserID, dashboardDimensionUser, startBucket, endExclusive, 50)
		if err != nil {
			return result, false, err
		}
	}
	channelLimit := 20
	if role >= common.RoleRootUser {
		channelLimit = 0
	}
	result.ByChannel, err = dashboardRollupDimensionRowsContext(ctx, scopeUserID, dashboardDimensionChannel, startBucket, endExclusive, channelLimit)
	if err != nil {
		return result, false, err
	}
	result.TimeSeries, err = dashboardRollupTimeSeriesContext(ctx, scopeUserID, startBucket, endExclusive)
	if err != nil {
		return result, false, err
	}
	return result, true, nil
}

// dashboardRollupWindowWholeHours reports whether hourly aggregates answer
// the window exactly. Raw usage uses an inclusive end, so both ends must cover
// whole hours; otherwise the caller uses its exact raw query rather than
// silently expanding the requested range.
func dashboardRollupWindowWholeHours(window DashboardUsageWindow) bool {
	return window.StartTimestamp%dashboardRollupHourSeconds == 0 && window.EndTimestamp%dashboardRollupHourSeconds == dashboardRollupHourSeconds-1
}

// BuildAdminDashboardUsersContext returns the platform's user rows. An
// unfiltered whole-hour window reads the hourly user dimension; the raw read
// joined every routing outcome of the range, 6.6 s for thirty days on
// 2026-10-16. Filters, or hours the rollup does not cover, read raw logs.
func BuildAdminDashboardUsersContext(ctx context.Context, filters DashboardUsageLogFilters, startTimestamp int64, endTimestamp int64) ([]DashboardUsageDimensionRow, error) {
	window := normalizeDashboardUsageWindow(startTimestamp, endTimestamp)
	unfiltered := filters.UserID <= 0 && filters.ChannelID <= 0 && strings.TrimSpace(filters.Username) == "" && strings.TrimSpace(filters.Group) == ""
	if unfiltered && dashboardRollupEnabled() && TelemetryDB() != nil && dashboardRollupWindowWholeHours(window) {
		ready, err := dashboardRollupCoverageReadyContext(ctx, window)
		if err != nil {
			return nil, err
		}
		if ready {
			startBucket, endExclusive, _ := dashboardRollupBucketRange(window)
			return dashboardRollupDimensionRowsContext(ctx, dashboardRollupScopeGlobal, dashboardDimensionUser, startBucket, endExclusive, 50)
		}
	}
	return BuildAdminDashboardUserRowsContext(ctx, filters, startTimestamp, endTimestamp)
}

func dashboardRollupCoverageReady(window DashboardUsageWindow) (bool, error) {
	return dashboardRollupCoverageReadyContext(context.Background(), window)
}

// dashboardRollupCoverageReadyContext reports whether every finished hour of
// the window has its platform row. The open hour is not required: until its
// first request it has no row and nothing to add, and requiring it sent every
// window ending now to the raw read for the first minutes of each quiet hour.
func dashboardRollupCoverageReadyContext(ctx context.Context, window DashboardUsageWindow) (bool, error) {
	startBucket, endExclusive, _ := dashboardRollupBucketRange(window)
	if open := dashboardRollupHour(common.GetTimestamp()); endExclusive > open {
		endExclusive = open
	}
	expected := (endExclusive - startBucket) / dashboardRollupHourSeconds
	if expected <= 0 {
		return true, nil
	}
	var count int64
	err := TelemetryDB().WithContext(ctx).Model(&DashboardUsageHourlyTotal{}).
		Where("scope_user_id = ? AND bucket_hour >= ? AND bucket_hour < ?", dashboardRollupScopeGlobal, startBucket, endExclusive).
		Count(&count).Error
	return count >= expected, err
}

func dashboardRollupTotals(scopeUserID int, startBucket int64, endExclusive int64) (DashboardUsageTotals, error) {
	return dashboardRollupTotalsContext(context.Background(), scopeUserID, startBucket, endExclusive)
}

func dashboardRollupTotalsContext(ctx context.Context, scopeUserID int, startBucket int64, endExclusive int64) (DashboardUsageTotals, error) {
	var rows []DashboardUsageHourlyTotal
	if err := TelemetryDB().WithContext(ctx).Where("scope_user_id = ? AND bucket_hour >= ? AND bucket_hour < ?", scopeUserID, startBucket, endExclusive).
		Find(&rows).Error; err != nil {
		return DashboardUsageTotals{}, err
	}
	var totals DashboardUsageTotals
	buckets := newDashboardLatencyBuckets()
	ttft := newDashboardTTFTBuckets()
	var latencyMsSum int64
	for _, row := range rows {
		totals.RequestCount += row.RequestCount
		totals.OutcomeCount += row.OutcomeCount
		totals.AmountMicrousd += row.AmountMicrousd
		totals.UpstreamMicrousd += row.UpstreamMicrousd
		totals.GrossMicrousd += row.GrossMicrousd
		totals.CostSampleCount += row.CostSampleCount
		totals.PromptTokens += row.PromptTokens
		totals.CachedInputTokens += row.CachedInputTokens
		totals.CompletionTokens += row.CompletionTokens
		totals.ErrorCount += row.ErrorCount
		latencyMsSum += row.LatencyMsSum
		buckets = mergeDashboardLatencyBuckets(buckets, row.LatencyBuckets)
		ttft = mergeDashboardTTFTBuckets(ttft, row.TTFTBuckets)
	}
	totals.TTFTSampleCount, totals.P95TTFTMs = dashboardTTFTP95FromBuckets(ttft)
	if totals.OutcomeCount > 0 {
		totals.AvgLatencyMs = float64(latencyMsSum) / float64(totals.OutcomeCount)
	}
	totals.P95LatencyMs = dashboardLatencyP95FromBuckets(buckets, totals.OutcomeCount)
	populateDashboardUsageTotalsDerived(&totals)
	return totals, nil
}

func dashboardRollupDistinctDimensionCount(scopeUserID int, dimensionType string, startBucket int64, endExclusive int64) (int64, error) {
	return dashboardRollupDistinctDimensionCountContext(context.Background(), scopeUserID, dimensionType, startBucket, endExclusive)
}

func dashboardRollupDistinctDimensionCountContext(ctx context.Context, scopeUserID int, dimensionType string, startBucket int64, endExclusive int64) (int64, error) {
	var count int64
	query := TelemetryDB().WithContext(ctx).Model(&DashboardUsageHourlyDimension{}).
		Where("scope_user_id = ? AND dimension_type = ? AND bucket_hour >= ? AND bucket_hour < ?", scopeUserID, dimensionType, startBucket, endExclusive)
	if dimensionType == dashboardDimensionKey {
		query = query.Where("dimension_id <> ?", "0")
		if internalTokenIDs, err := hiddenTokenIDs(); err != nil {
			return 0, err
		} else if len(internalTokenIDs) > 0 {
			ids := make([]string, 0, len(internalTokenIDs))
			for _, id := range internalTokenIDs {
				ids = append(ids, strconv.Itoa(id))
			}
			query = query.Where("dimension_id NOT IN ?", ids)
		}
	}
	err := query.
		Distinct("dimension_id").
		Count(&count).Error
	return count, err
}

func dashboardRollupDimensionRows(scopeUserID int, dimensionType string, startBucket int64, endExclusive int64, limit int) ([]DashboardUsageDimensionRow, error) {
	return dashboardRollupDimensionRowsContext(context.Background(), scopeUserID, dimensionType, startBucket, endExclusive, limit)
}

func dashboardRollupDimensionRowsContext(ctx context.Context, scopeUserID int, dimensionType string, startBucket int64, endExclusive int64, limit int) ([]DashboardUsageDimensionRow, error) {
	var rows []dashboardRollupDimensionQueryRow
	if err := TelemetryDB().WithContext(ctx).Model(&DashboardUsageHourlyDimension{}).
		Where("scope_user_id = ? AND dimension_type = ? AND bucket_hour >= ? AND bucket_hour < ?", scopeUserID, dimensionType, startBucket, endExclusive).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	acc := map[string]*dashboardRollupDimensionAccumulator{}
	for _, row := range rows {
		id := row.DimensionID
		if id == "" {
			id = "unknown"
		}
		current, ok := acc[id]
		if !ok {
			current = &dashboardRollupDimensionAccumulator{
				ID:             id,
				Name:           dashboardRollupName(row.DimensionName),
				LatencyBuckets: newDashboardLatencyBuckets(),
			}
			acc[id] = current
		}
		current.RequestCount += row.RequestCount
		current.OutcomeCount += row.OutcomeCount
		current.AmountMicrousd += row.AmountMicrousd
		current.UpstreamMicrousd += row.UpstreamMicrousd
		current.GrossMicrousd += row.GrossMicrousd
		current.CostSampleCount += row.CostSampleCount
		current.PromptTokens += row.PromptTokens
		current.CachedInputTokens += row.CachedInputTokens
		current.CompletionTokens += row.CompletionTokens
		current.ErrorCount += row.ErrorCount
		current.LatencyMsSum += row.LatencyMsSum
		current.LatencyBuckets = mergeDashboardLatencyBuckets(current.LatencyBuckets, row.LatencyBuckets)
		if row.LastUsedAt > current.LastUsedAt {
			current.LastUsedAt = row.LastUsedAt
		}
	}

	result := make([]DashboardUsageDimensionRow, 0, len(acc))
	for _, row := range acc {
		id, _ := strconv.Atoi(row.ID)
		item := DashboardUsageDimensionRow{
			ID:                id,
			Name:              row.Name,
			RequestCount:      row.RequestCount,
			AmountMicrousd:    row.AmountMicrousd,
			UpstreamMicrousd:  row.UpstreamMicrousd,
			GrossMicrousd:     row.GrossMicrousd,
			CostSampleCount:   row.CostSampleCount,
			PromptTokens:      row.PromptTokens,
			CachedInputTokens: row.CachedInputTokens,
			CompletionTokens:  row.CompletionTokens,
			ErrorCount:        row.ErrorCount,
			LastUsedAt:        row.LastUsedAt,
		}
		if row.OutcomeCount > 0 {
			item.AvgLatencyMs = float64(row.LatencyMsSum) / float64(row.OutcomeCount)
		}
		item.P95LatencyMs = dashboardLatencyP95FromBuckets(row.LatencyBuckets, row.OutcomeCount)
		item.CacheHitRatio = dashboardCacheHitRatio(item.CachedInputTokens, item.PromptTokens)
		if item.CostSampleCount > 0 && item.AmountMicrousd != 0 {
			item.GrossMarginRatio = float64(item.GrossMicrousd) / float64(item.AmountMicrousd)
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i int, j int) bool {
		if result[i].AmountMicrousd != result[j].AmountMicrousd {
			return result[i].AmountMicrousd > result[j].AmountMicrousd
		}
		if result[i].RequestCount != result[j].RequestCount {
			return result[i].RequestCount > result[j].RequestCount
		}
		return result[i].Name < result[j].Name
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func dashboardRollupTimeSeries(scopeUserID int, startBucket int64, endExclusive int64) ([]DashboardUsageTimeRow, error) {
	return dashboardRollupTimeSeriesContext(context.Background(), scopeUserID, startBucket, endExclusive)
}

func dashboardRollupTimeSeriesContext(ctx context.Context, scopeUserID int, startBucket int64, endExclusive int64) ([]DashboardUsageTimeRow, error) {
	var rows []DashboardUsageHourlyTotal
	if err := TelemetryDB().WithContext(ctx).Where("scope_user_id = ? AND bucket_hour >= ? AND bucket_hour < ?", scopeUserID, startBucket, endExclusive).
		Order("bucket_hour ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	byDay := map[int64]*DashboardUsageTimeRow{}
	for _, row := range rows {
		day := row.BucketHour - (row.BucketHour % secondsInDay)
		item, ok := byDay[day]
		if !ok {
			item = &DashboardUsageTimeRow{Bucket: day}
			byDay[day] = item
		}
		item.RequestCount += row.RequestCount
		item.AmountMicrousd += row.AmountMicrousd
		item.UpstreamMicrousd += row.UpstreamMicrousd
		item.GrossMicrousd += row.GrossMicrousd
		item.CostSampleCount += row.CostSampleCount
		item.PromptTokens += row.PromptTokens
		item.CachedInputTokens += row.CachedInputTokens
		item.CompletionTokens += row.CompletionTokens
		item.ErrorCount += row.ErrorCount
	}
	result := make([]DashboardUsageTimeRow, 0, len(byDay))
	for _, row := range byDay {
		result = append(result, *row)
	}
	sort.Slice(result, func(i int, j int) bool {
		return result[i].Bucket < result[j].Bucket
	})
	return result, nil
}

// dashboardHourPastLogRetention reports whether the hour starting at start
// may have lost logs to retention (with a day of margin for the purge's own
// schedule).
func dashboardHourPastLogRetention(start time.Time, now time.Time) bool {
	if common.LogRetentionDays <= 0 {
		return false
	}
	days := common.LogRetentionDays
	if days < ModelRankingWindowDays {
		days = ModelRankingWindowDays
	}
	return start.Before(now.AddDate(0, 0, -(days - 1)))
}

// dashboardRollupQuietHourLookback bounds the hours EnsureDashboardRollupHours
// inspects; the dashboard serves windows of at most ninety days.
const dashboardRollupQuietHourLookback = 100 * 24 * time.Hour

// EnsureDashboardRollupHours writes an empty platform total for every
// finished hour that has none. The telemetry writer creates an hour's row
// with its first request, so a quiet hour had no row, and every window
// spanning one failed the coverage check and read raw outcomes instead (six
// seconds for an account's thirty days on 2026-10-16). Hours before the
// oldest platform row stay uncovered: the rollup did not exist then.
func EnsureDashboardRollupHours(ctx context.Context, now time.Time) error {
	if TelemetryDB() == nil {
		return nil
	}
	db := TelemetryDB().WithContext(ctx)
	current := dashboardRollupHour(now.Unix())
	from := dashboardRollupHour(now.Add(-dashboardRollupQuietHourLookback).Unix())
	var first struct{ First *int64 }
	if err := db.Model(&DashboardUsageHourlyTotal{}).Where("scope_user_id = ?", dashboardRollupScopeGlobal).Select("MIN(bucket_hour) AS first").Scan(&first).Error; err != nil {
		return err
	}
	if first.First == nil {
		return nil
	}
	if *first.First > from {
		from = *first.First
	}
	var present []int64
	if err := db.Model(&DashboardUsageHourlyTotal{}).Where("scope_user_id = ? AND bucket_hour >= ? AND bucket_hour < ?", dashboardRollupScopeGlobal, from, current).Pluck("bucket_hour", &present).Error; err != nil {
		return err
	}
	seen := make(map[int64]struct{}, len(present))
	for _, hour := range present {
		seen[hour] = struct{}{}
	}
	quiet := make([]DashboardUsageHourlyTotal, 0)
	for hour := from; hour < current; hour += dashboardRollupHourSeconds {
		if _, found := seen[hour]; !found {
			quiet = append(quiet, DashboardUsageHourlyTotal{BucketHour: hour, ScopeUserID: dashboardRollupScopeGlobal, LatencyBuckets: encodeDashboardLatencyBuckets(nil), TTFTBuckets: encodeDashboardTTFTBuckets(nil), UpdatedAt: now.UTC()})
		}
	}
	if len(quiet) == 0 {
		return nil
	}
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "bucket_hour"}, {Name: "scope_user_id"}}, DoNothing: true}).CreateInBatches(quiet, 200).Error
}
