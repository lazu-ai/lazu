package model

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func telemetryRangeFixture(t *testing.T, size int) relayWriterBatch {
	t.Helper()
	batch := relayWriterBatch{}
	for i := 0; i < size; i++ {
		event, receipt := completeWriterFixture(common.GenerateRequestID())
		event.ConsumeLog.PromptTokens = 0
		event.ConsumeLog.CompletionTokens = 0
		event.RoutingOutcome.ActualInputTokens = nil
		event.RoutingOutcome.ActualOutputTokens = nil
		event.RoutingOutcome.CachedInputTokens = nil
		batch.results = append(batch.results, event)
		batch.receipts = append(batch.receipts, receipt)
		batch.diagnostics = append(batch.diagnostics, RequestDiagnosticsEvent{RequestID: receipt.RequestID, Fields: map[string]interface{}{"must_rollback": true}})
	}
	return batch
}

func telemetryRangeCacheFact(t *testing.T, quantity int64) string {
	t.Helper()
	raw, err := json.Marshal(usagepkg.Fact{Version: usagepkg.FactVersion, Dimensions: []usagepkg.DimensionUsage{{Name: usagepkg.DimensionCacheRead, Quantity: quantity, Unit: usagepkg.UnitToken}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func telemetryRangeOfficialQuote(t *testing.T, amount int64) string {
	t.Helper()
	raw, err := json.Marshal(billing.QuoteResult{Official: &billing.OfficialComparison{TotalMicrousd: billing.MicroUSD(amount)}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func telemetryRangeModels() []interface{} {
	return []interface{}{
		&TelemetryFinancialReceipt{}, &TelemetryEventReceipt{}, &Log{},
		&AgentSession{}, &AgentSessionThread{}, &AgentSessionModel{}, &AgentSessionMessage{},
		&ModelRequestCounter{}, &DigestUsageHour{}, &DashboardUsageDailyDimension{},
		&DashboardUsageHourlyTotal{}, &DashboardUsageHourlyDimension{}, &RoutingAttemptOutcome{},
	}
}

func assertTelemetryRangeRollback(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, model := range telemetryRangeModels() {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%T survived rejected telemetry transaction: rows=%d err=%v", model, count, err)
		}
	}
}

func TestPostgresRelayWriterRollsBackStoredCountAndHistogramOverflow(t *testing.T) {
	for _, overflow := range []string{"request_count", "latency_bucket"} {
		t.Run(overflow, func(t *testing.T) {
			_, db := postgresTelemetryFixture(t)
			batch := telemetryRangeFixture(t, 1)
			hour := batch.results[0].RoutingOutcome.OutcomeAt.Unix()
			hour -= hour % 3600
			buckets, err := addDashboardLatencyBucket(nil, *batch.results[0].RoutingOutcome.TotalDurationMs)
			if err != nil {
				t.Fatal(err)
			}
			seed := DashboardUsageHourlyTotal{BucketHour: hour, ScopeUserID: 0}
			if overflow == "request_count" {
				seed.RequestCount = math.MaxInt64
			} else {
				for i, count := range buckets {
					if count == 1 {
						buckets[i] = math.MaxInt64
					}
				}
			}
			seed.LatencyBuckets = encodeDashboardLatencyBuckets(buckets)
			if err := db.Create(&seed).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := persistRelayWriterBatch(batch); err == nil {
				t.Fatal("stored bigint/histogram overflow was committed")
			}
			for _, model := range telemetryRangeModels() {
				want := int64(0)
				if _, ok := model.(*DashboardUsageHourlyTotal); ok {
					want = 1
				}
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil || count != want {
					t.Fatalf("%T changed during late SQL overflow: rows=%d want=%d err=%v", model, count, want, err)
				}
			}
			var stored DashboardUsageHourlyTotal
			if err := db.Where("bucket_hour = ? AND scope_user_id = ?", hour, 0).First(&stored).Error; err != nil || stored.RequestCount != seed.RequestCount || stored.OutcomeCount != 0 || stored.AmountMicrousd != 0 || stored.LatencyBuckets != seed.LatencyBuckets {
				t.Fatalf("overflow changed pre-existing hourly facts: %+v err=%v", stored, err)
			}
			if err := db.Model(&DashboardUsageHourlyTotal{}).Where("bucket_hour = ? AND scope_user_id = ?", hour, 0).Updates(map[string]interface{}{"request_count": 0, "latency_buckets": encodeDashboardLatencyBuckets(nil)}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := persistRelayWriterBatch(batch); err != nil {
				t.Fatalf("rolled-back receipts prevented same-fact retry: %v", err)
			}
		})
	}
}

// The SQL-overflow regression seeds an existing bigint at its maximum. These
// cases exercise overflow before SQL, where native Go addition used to wrap
// into a representable negative value and commit the receipt with bad totals.
func TestPostgresRelayWriterRejectsPreSQLCounterRangeAtomically(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	cases := []struct {
		name string
		edit func(*relayWriterBatch)
	}{
		{"batch_prompt_tokens", func(batch *relayWriterBatch) {
			for i := range batch.results {
				batch.results[i].ConsumeLog.PromptTokens = math.MaxInt64
			}
		}},
		{"batch_completion_tokens", func(batch *relayWriterBatch) {
			for i := range batch.results {
				batch.results[i].ConsumeLog.CompletionTokens = math.MaxInt64
			}
		}},
		{"single_total_tokens", func(batch *relayWriterBatch) {
			batch.results[0].ConsumeLog.PromptTokens = math.MaxInt64
			batch.results[0].ConsumeLog.CompletionTokens = 1
		}},
		{"batch_money_positive", func(batch *relayWriterBatch) {
			for i := range batch.results {
				batch.results[i].ConsumeLog.AmountMicrousd = billing.MicroUSD(math.MaxInt64)
				batch.receipts[i].AmountMicrousd = math.MaxInt64
			}
		}},
		{"batch_money_negative", func(batch *relayWriterBatch) {
			for i := range batch.results {
				batch.results[i].ConsumeLog.AmountMicrousd = billing.MicroUSD(math.MinInt64)
				batch.receipts[i].AmountMicrousd = math.MinInt64
				batch.receipts[i].Kind = "refund"
			}
		}},
		{"batch_upstream_cost", func(batch *relayWriterBatch) {
			for i := range batch.results {
				cost := int64(math.MaxInt64)
				batch.results[i].ConsumeLog.UpstreamCost = &cost
			}
		}},
		{"batch_cache_read", func(batch *relayWriterBatch) {
			for i := range batch.results {
				batch.results[i].ConsumeLog.UsageFact = telemetryRangeCacheFact(t, math.MaxInt64)
			}
		}},
		{"negative_prompt", func(batch *relayWriterBatch) { batch.results[0].ConsumeLog.PromptTokens = -1 }},
		{"negative_completion", func(batch *relayWriterBatch) { batch.results[0].ConsumeLog.CompletionTokens = -1 }},
		{"negative_cache_read", func(batch *relayWriterBatch) { batch.results[0].ConsumeLog.UsageFact = telemetryRangeCacheFact(t, -1) }},
		{"batch_outcome_input", func(batch *relayWriterBatch) {
			for i := range batch.results {
				value := int(math.MaxInt64)
				batch.results[i].RoutingOutcome.ActualInputTokens = &value
			}
		}},
		{"batch_outcome_output", func(batch *relayWriterBatch) {
			for i := range batch.results {
				value := int(math.MaxInt64)
				batch.results[i].RoutingOutcome.ActualOutputTokens = &value
			}
		}},
		{"single_outcome_total", func(batch *relayWriterBatch) {
			input, output := int(math.MaxInt64), 1
			batch.results[0].RoutingOutcome.ActualInputTokens = &input
			batch.results[0].RoutingOutcome.ActualOutputTokens = &output
		}},
		{"batch_outcome_cache", func(batch *relayWriterBatch) {
			for i := range batch.results {
				value := int(math.MaxInt64)
				batch.results[i].RoutingOutcome.CachedInputTokens = &value
			}
		}},
		{"batch_outcome_latency", func(batch *relayWriterBatch) {
			for i := range batch.results {
				value := int(math.MaxInt64)
				batch.results[i].RoutingOutcome.TotalDurationMs = &value
			}
		}},
		{"negative_outcome_input", func(batch *relayWriterBatch) {
			value := -1
			batch.results[0].RoutingOutcome.ActualInputTokens = &value
		}},
		{"negative_outcome_cache", func(batch *relayWriterBatch) {
			value := -1
			batch.results[0].RoutingOutcome.CachedInputTokens = &value
		}},
		{"negative_outcome_latency", func(batch *relayWriterBatch) {
			value := -1
			batch.results[0].RoutingOutcome.TotalDurationMs = &value
		}},
		{"usd_cost_range", func(batch *relayWriterBatch) {
			value := math.MaxFloat64
			batch.results[0].RoutingOutcome.UpstreamCostUSD = &value
		}},
		{"usd_charge_range", func(batch *relayWriterBatch) {
			value := math.MaxFloat64
			batch.results[0].RoutingOutcome.ChargedCostUSD = &value
		}},
		{"usd_margin_range", func(batch *relayWriterBatch) {
			cost, margin := 1.0, math.MaxFloat64
			batch.results[0].RoutingOutcome.UpstreamCostUSD = &cost
			batch.results[0].RoutingOutcome.GrossMarginUSD = &margin
		}},
		{"usd_nan", func(batch *relayWriterBatch) {
			value := math.NaN()
			batch.results[0].RoutingOutcome.UpstreamCostUSD = &value
		}},
		{"usd_infinity", func(batch *relayWriterBatch) {
			value := math.Inf(1)
			batch.results[0].RoutingOutcome.ChargedCostUSD = &value
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			batch := telemetryRangeFixture(t, 2)
			tc.edit(&batch)
			if _, err := persistRelayWriterBatch(batch); !errors.Is(err, ErrTelemetryCounterRange) {
				t.Fatalf("malformed telemetry accepted or returned wrong error: %v", err)
			}
			assertTelemetryRangeRollback(t, db)
		})
	}
}

func TestPostgresRelayWriterPreservesExactBoundaryAndSignedMoney(t *testing.T) {
	t.Run("int64_boundary", func(t *testing.T) {
		_, db := postgresTelemetryFixture(t)
		batch := telemetryRangeFixture(t, 2)
		for i, value := range []int64{math.MaxInt64 - 1, 1} {
			row := batch.results[i].ConsumeLog
			row.AmountMicrousd = billing.MicroUSD(value)
			row.PromptTokens = int(value)
			row.UpstreamCost = &value
			row.UsageFact = telemetryRangeCacheFact(t, value)
			row.LineItems = telemetryRangeOfficialQuote(t, value)
			batch.receipts[i].AmountMicrousd = value
			cache, latency, input := int(value), int(value), int(value)
			batch.results[i].RoutingOutcome.CachedInputTokens = &cache
			batch.results[i].RoutingOutcome.TotalDurationMs = &latency
			batch.results[i].RoutingOutcome.ActualInputTokens = &input
		}
		for replay := 0; replay < 2; replay++ {
			if _, err := persistRelayWriterBatch(batch); err != nil {
				t.Fatal(err)
			}
		}
		var session AgentSession
		if err := db.First(&session).Error; err != nil || session.RequestCount != 2 || session.AmountMicrousd != math.MaxInt64 || session.PromptTokens != math.MaxInt64 || session.CachedTokens != math.MaxInt64 || session.UpstreamCostMicrousd != math.MaxInt64 || session.OfficialMicrousd != math.MaxInt64 || session.ComparedMicrousd != math.MaxInt64 {
			t.Fatalf("boundary session or replay lost exact facts: %+v err=%v", session, err)
		}
		var digest DigestUsageHour
		if err := db.First(&digest).Error; err != nil || digest.Requests != 2 || digest.Spend != math.MaxInt64 || digest.Tokens != math.MaxInt64 || digest.Cache != math.MaxInt64 || digest.Official != math.MaxInt64 || digest.Compared != math.MaxInt64 {
			t.Fatalf("boundary digest lost exact facts: %+v err=%v", digest, err)
		}
		var hourly DashboardUsageHourlyTotal
		if err := db.Where("scope_user_id = ?", 0).First(&hourly).Error; err != nil || hourly.RequestCount != 2 || hourly.AmountMicrousd != math.MaxInt64 || hourly.PromptTokens != math.MaxInt64 || hourly.CachedInputTokens != math.MaxInt64 || hourly.LatencyMsSum != math.MaxInt64 {
			t.Fatalf("boundary hourly aggregate lost exact facts: %+v err=%v", hourly, err)
		}
		for _, model := range []interface{}{&Log{}, &TelemetryEventReceipt{}, &TelemetryFinancialReceipt{}} {
			var count int64
			if err := db.Model(model).Count(&count).Error; err != nil || count != 2 {
				t.Fatalf("boundary %T was duplicated on replay: count=%d err=%v", model, count, err)
			}
		}
	})
	t.Run("signed_refund", func(t *testing.T) {
		_, db := postgresTelemetryFixture(t)
		batch := telemetryRangeFixture(t, 2)
		for i, amount := range []int64{90, -40} {
			batch.results[i].ConsumeLog.AmountMicrousd = billing.MicroUSD(amount)
			batch.results[i].ConsumeLog.UpstreamCost = &amount
			batch.results[i].ConsumeLog.PromptTokens = 1
			batch.receipts[i].AmountMicrousd = amount
			if amount < 0 {
				batch.receipts[i].Kind = "refund"
				batch.receipts[i].EventID = "refund:" + batch.receipts[i].RequestID
			}
		}
		for replay := 0; replay < 2; replay++ {
			if _, err := persistRelayWriterBatch(batch); err != nil {
				t.Fatal(err)
			}
		}
		var session AgentSession
		if err := db.First(&session).Error; err != nil || session.AmountMicrousd != 50 || session.UpstreamCostMicrousd != 50 || session.RequestCount != 2 || session.PromptTokens != 2 {
			t.Fatalf("signed money or replay changed semantics: %+v err=%v", session, err)
		}
		var digest DigestUsageHour
		if err := db.First(&digest).Error; err != nil || digest.Spend != 50 || digest.Requests != 2 {
			t.Fatalf("signed digest changed semantics: %+v err=%v", digest, err)
		}
		var refund TelemetryFinancialReceipt
		if err := db.Where("kind = ?", "refund").First(&refund).Error; err != nil || refund.AmountMicrousd != -40 {
			t.Fatalf("signed refund receipt changed facts: %+v err=%v", refund, err)
		}
	})
}
