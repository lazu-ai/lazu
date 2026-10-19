package model

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
)

func TestTelemetryCounterPreflightPreservesEveryAccumulator(t *testing.T) {
	for _, boundary := range []int64{math.MaxInt64, math.MinInt64} {
		first, second := int64(7), boundary
		delta := int64(1)
		if boundary < 0 {
			delta = -1
		}
		if err := addTelemetryCounters(telemetryDelta{&first, 1}, telemetryDelta{&second, delta}); !errors.Is(err, ErrTelemetryCounterRange) {
			t.Fatalf("out-of-range sum was accepted: %v", err)
		}
		if first != 7 || second != boundary {
			t.Fatalf("failed preflight changed an accumulator: %d %d", first, second)
		}
	}
	value := int64(math.MaxInt64 - 1)
	if err := addTelemetryCounters(telemetryDelta{&value, 1}); err != nil || value != math.MaxInt64 {
		t.Fatalf("exact boundary rejected: %d %v", value, err)
	}
	if err := addTelemetryCounters(telemetryDelta{&value, -math.MaxInt64}); err != nil || value != 0 {
		t.Fatalf("signed monetary delta rejected: %d %v", value, err)
	}
}

func TestTelemetryRawUsageCountersAreCheckedBeforeNormalization(t *testing.T) {
	maximum := int(^uint(0) >> 1)
	for _, fact := range []usagepkg.Fact{
		{PromptTokens: maximum, CompletionTokens: 1},
		{InputTokens: maximum, OutputTokens: 1},
		{TotalTokens: -1},
		{MediaUnits: &usagepkg.MediaUnits{ImageCount: -1}},
		{Dimensions: []usagepkg.DimensionUsage{{Name: usagepkg.DimensionCacheRead, Quantity: -1}}},
	} {
		body, err := json.Marshal(fact)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateTelemetryUsageFactNumbers(string(body)); !errors.Is(err, ErrTelemetryCounterRange) {
			t.Fatalf("invalid raw counters accepted: %s %v", body, err)
		}
	}
	if total, err := checkedTelemetryTokenTotal(maximum, 0); err != nil || total != int64(maximum) {
		t.Fatalf("native int boundary rejected: %d %v", total, err)
	}
}

func TestTelemetryHistogramAndUSDRangeChecks(t *testing.T) {
	for _, value := range []float64{math.MaxFloat64, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := dashboardCostUSDToMicrousd(value); !errors.Is(err, ErrTelemetryCounterRange) {
			t.Fatalf("non-representable USD value accepted: %v %v", value, err)
		}
	}
	if value, err := dashboardCostUSDToMicrousd(-0.00004); err != nil || value != -40 {
		t.Fatalf("signed margin was changed: %d %v", value, err)
	}
	if _, err := addDashboardLatencyBucket(nil, -1); !errors.Is(err, ErrTelemetryCounterRange) {
		t.Fatalf("negative latency accepted: %v", err)
	}
	buckets := newDashboardLatencyBuckets()
	buckets[0] = math.MaxInt64
	encoded := encodeDashboardLatencyBuckets(buckets)
	buckets[0] = 1
	if _, err := mergeLatencyBuckets(encoded, encodeDashboardLatencyBuckets(buckets)); !errors.Is(err, ErrTelemetryCounterRange) {
		t.Fatalf("histogram wrapped before SQL: %v", err)
	}
}

func TestDigestRebuildRejectsOverflowBeforeReplacingHistoricalRollup(t *testing.T) {
	_, db := isolatedTelemetryStores(t)
	at := time.Now().UTC().Truncate(time.Hour)
	seed := DigestUsageHour{PayerUserID: 1, HourStart: at.Unix(), ModelName: "boundary", Requests: 7, Spend: 20, Tokens: 30}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		row := Log{RequestID: common.GenerateRequestID(), Type: LogTypeConsume, UserId: 1, PayerUserID: 1, CreatedAt: at.Unix(), ModelName: "boundary", PromptTokens: int(^uint(0) >> 1)}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := RollupDigestHours(at, at.Add(time.Hour)); !errors.Is(err, ErrTelemetryCounterRange) {
		t.Fatalf("rebuild accepted overflow: %v", err)
	}
	var stored DigestUsageHour
	if err := db.First(&stored).Error; err != nil || stored != seed {
		t.Fatalf("failed rebuild replaced valid historical totals: %+v %v", stored, err)
	}
}
