package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
	relaymetrics "github.com/lazu-ai/lazu/internal/relay/metrics"
	"github.com/lazu-ai/lazu/internal/service/alerting"
	"gorm.io/gorm"
)

const (
	zeroTokenMonitorThreshold        = 0.01
	zeroTokenMonitorConsecutiveLimit = 5
	zeroTokenMonitorMinSamples       = 20
	zeroTokenMonitorWindow           = time.Minute
	zeroTokenIncidentKey             = "relay-zero-token"
)

type RelayZeroTokenMonitorTask struct {
	consecutiveBreaches int
	oneShot             bool
}

func NewRelayZeroTokenMonitorTask() *RelayZeroTokenMonitorTask {
	return &RelayZeroTokenMonitorTask{}
}

// NewRelayZeroTokenMonitorOneShotTask evaluates five complete one-minute
// windows in one invocation. This preserves the consecutive-window contract
// when an external scheduler starts a fresh process for each run.
func NewRelayZeroTokenMonitorOneShotTask() *RelayZeroTokenMonitorTask {
	return &RelayZeroTokenMonitorTask{oneShot: true}
}

func (t *RelayZeroTokenMonitorTask) Name() string {
	return "relay_zero_token_quota_zero_monitor"
}

func (t *RelayZeroTokenMonitorTask) Interval() time.Duration {
	return zeroTokenMonitorWindow
}

func (t *RelayZeroTokenMonitorTask) Run(ctx context.Context) error {
	db := model.TelemetryDB()
	if db == nil {
		db = model.CoreDB()
	}
	now := time.Now().UTC()
	if t.oneShot {
		return runZeroTokenOneShot(ctx, db, now)
	}
	snapshot, err := relaymetrics.QueryZeroTokenQuotaZeroRate(ctx, db, now.Add(-zeroTokenMonitorWindow).Unix(), now.Unix())
	if err != nil {
		reportZeroTokenQueryFailure(err)
		return err
	}
	if snapshot.Total < zeroTokenMonitorMinSamples {
		recordZeroTokenUnknown(snapshot)
		return nil
	}
	t.consecutiveBreaches = NextZeroTokenBreachCount(t.consecutiveBreaches, snapshot.Rate, snapshot.Total)
	summary := fmt.Sprintf("rate=%.2f%% zero=%d total=%d", snapshot.Rate*100, snapshot.Zero, snapshot.Total)
	if t.consecutiveBreaches < zeroTokenMonitorConsecutiveLimit {
		alerting.RecordHealth(zeroTokenIncidentKey, "Zero-token billing monitor", "ok", summary)
		if t.consecutiveBreaches == 0 {
			alerting.Resolve(zeroTokenIncidentKey, "✅ Lazu zero-token billing anomaly recovered")
		}
		return nil
	}
	alerting.RecordHealth(zeroTokenIncidentKey, "Zero-token billing monitor", "failed", summary)
	alerting.Report(alerting.Incident{
		Key:         zeroTokenIncidentKey,
		Fingerprint: relaymetrics.ZeroTokenQuotaZeroMetricName,
		Priority:    alerting.PriorityHigh,
		Message: fmt.Sprintf("⚠️ %s breached\nrate=%.2f%% zero=%d total=%d consecutive_minutes=%d",
			relaymetrics.ZeroTokenQuotaZeroMetricName,
			snapshot.Rate*100,
			snapshot.Zero,
			snapshot.Total,
			t.consecutiveBreaches,
		),
	})
	return nil
}

func runZeroTokenOneShot(ctx context.Context, db *gorm.DB, now time.Time) error {
	windowEnd := now.Truncate(zeroTokenMonitorWindow)
	var latest relaymetrics.ZeroTokenQuotaZeroSnapshot
	for remaining := zeroTokenMonitorConsecutiveLimit; remaining > 0; remaining-- {
		windowStart := windowEnd.Add(-time.Duration(remaining) * zeroTokenMonitorWindow)
		snapshot, err := relaymetrics.QueryZeroTokenQuotaZeroRate(
			ctx,
			db,
			windowStart.Unix(),
			windowStart.Add(zeroTokenMonitorWindow).Unix(),
		)
		if err != nil {
			reportZeroTokenQueryFailure(err)
			return err
		}
		if snapshot.Total < zeroTokenMonitorMinSamples {
			recordZeroTokenUnknown(snapshot)
			return nil
		}
		if NextZeroTokenBreachCount(0, snapshot.Rate, snapshot.Total) == 0 {
			alerting.RecordHealth(zeroTokenIncidentKey, "Zero-token billing monitor", "ok",
				fmt.Sprintf("rate=%.2f%% zero=%d total=%d", snapshot.Rate*100, snapshot.Zero, snapshot.Total))
			alerting.Resolve(zeroTokenIncidentKey, "✅ Lazu zero-token billing anomaly recovered")
			return nil
		}
		latest = snapshot
	}
	summary := fmt.Sprintf("rate=%.2f%% zero=%d total=%d", latest.Rate*100, latest.Zero, latest.Total)
	alerting.RecordHealth(zeroTokenIncidentKey, "Zero-token billing monitor", "failed", summary)
	alerting.Report(alerting.Incident{
		Key:         zeroTokenIncidentKey,
		Fingerprint: relaymetrics.ZeroTokenQuotaZeroMetricName,
		Priority:    alerting.PriorityHigh,
		Message: fmt.Sprintf("⚠️ %s breached\nrate=%.2f%% zero=%d total=%d consecutive_minutes=%d",
			relaymetrics.ZeroTokenQuotaZeroMetricName,
			latest.Rate*100,
			latest.Zero,
			latest.Total,
			zeroTokenMonitorConsecutiveLimit,
		),
	})
	return nil
}

func recordZeroTokenUnknown(snapshot relaymetrics.ZeroTokenQuotaZeroSnapshot) {
	alerting.RecordHealth(zeroTokenIncidentKey, "Zero-token billing monitor", "unknown",
		fmt.Sprintf("not evaluated: %d requests in the last minute, needs %d",
			snapshot.Total, zeroTokenMonitorMinSamples))
}

func reportZeroTokenQueryFailure(err error) {
	if err == nil {
		return
	}
	summary := "query error=" + truncateZeroTokenError(err.Error(), 180)
	alerting.RecordHealth(zeroTokenIncidentKey, "Zero-token billing monitor", "failed", summary)
	alerting.Report(alerting.Incident{
		Key:         zeroTokenIncidentKey,
		Fingerprint: "query:" + truncateZeroTokenError(strings.ToLower(err.Error()), 180),
		Priority:    alerting.PriorityHigh,
		Message:     "❌ Lazu zero-token billing monitor query failed\nerror: " + truncateZeroTokenError(err.Error(), 360),
	})
}

func truncateZeroTokenError(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if max <= 0 || len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}

func NextZeroTokenBreachCount(previous int, rate float64, total int64) int {
	if total < zeroTokenMonitorMinSamples {
		return previous
	}
	if rate <= zeroTokenMonitorThreshold {
		return 0
	}
	return previous + 1
}
