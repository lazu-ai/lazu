package model

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RoutingAttemptOutcome records an upstream failure that was superseded by a
// retry. The final attempt remains represented by RoutingOutcome, which keeps
// request-level usage, TTFT and throughput single-counted.
type RoutingAttemptOutcome struct {
	RequestTS         int64     `gorm:"primaryKey;autoIncrement:false" json:"request_ts"`
	RequestID         string    `gorm:"size:64;primaryKey" json:"request_id"`
	AttemptIndex      int       `gorm:"primaryKey;autoIncrement:false" json:"attempt_index"`
	OutcomeAt         time.Time `gorm:"not null;index:idx_attempt_outcome_at" json:"outcome_at"`
	DecisionID        int64     `gorm:"not null;index:idx_attempt_decision" json:"decision_id"`
	ChannelID         int       `gorm:"not null;index:idx_attempt_channel" json:"channel_id"`
	FinalGroup        string    `gorm:"size:64;index:idx_attempt_final_group" json:"final_group"`
	StatusCode        int       `gorm:"index:idx_attempt_status" json:"status_code"`
	ErrorCode         string    `gorm:"size:64" json:"error_code,omitempty"`
	ErrorType         string    `gorm:"size:64" json:"error_type,omitempty"`
	ProviderAttempted *bool     `json:"provider_attempted,omitempty"`
	HealthEligible    *bool     `json:"health_eligible,omitempty"`
	HealthFailed      *bool     `json:"health_failed,omitempty"`
	TotalDurationMs   int       `json:"total_duration_ms"`
}

func (RoutingAttemptOutcome) TableName() string {
	return "routing_attempt_outcome"
}

func (r *RoutingAttemptOutcome) BeforeSave(*gorm.DB) error {
	r.FinalGroup = NormalizeChannelLane(r.FinalGroup)
	if r.RequestTS == 0 {
		r.RequestTS = requestTimestamp(r.RequestID, r.OutcomeAt.Unix())
	}
	return nil
}

func RecordRoutingAttemptOutcome(outcome *RoutingAttemptOutcome) error {
	if TelemetryDB() == nil || outcome == nil || outcome.RequestID == "" {
		return nil
	}
	if relayEventWriterRequired() {
		return PublishRelayEvent(RelayEventTypeRoutingAttemptOutcome, outcome)
	}
	if relayEventQueueAvailable() {
		if err := PublishRelayEvent(RelayEventTypeRoutingAttemptOutcome, outcome); err == nil {
			return nil
		}
	}
	return recordRoutingAttemptOutcomeSync(outcome)
}

func recordRoutingAttemptOutcomeSync(outcome *RoutingAttemptOutcome) error {
	if TelemetryDB() == nil || outcome == nil || outcome.RequestID == "" {
		return nil
	}
	return TelemetryDB().Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "request_ts"}, {Name: "request_id"}, {Name: "attempt_index"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"outcome_at", "decision_id", "channel_id", "final_group", "status_code",
			"error_code", "error_type", "provider_attempted", "health_eligible",
			"health_failed", "total_duration_ms",
		}),
	}).Create(outcome).Error
}

// UpstreamAccountFailure is how often one channel's upstream refused attempts
// for an account reason in a window: its credentials, or its ability to pay.
type UpstreamAccountFailure struct {
	ChannelID  int
	ErrorCode  string
	StatusCode int
	Failures   int64
	// LastMs is the start of the latest failing request, in milliseconds.
	LastMs int64
}

// RecentUpstreamAccountFailures counts attempts since `since` that failed
// with one of codes, per channel and code. The window is bounded by the
// indexed outcome time and by the partition key, so the scan stays small.
func RecentUpstreamAccountFailures(ctx context.Context, since time.Time, codes []string) ([]UpstreamAccountFailure, error) {
	db := TelemetryDB()
	if db == nil {
		return nil, nil
	}
	var rows []UpstreamAccountFailure
	// request_ts is the request's start in milliseconds; a request that began
	// up to an hour before the window can still finish inside it.
	err := db.WithContext(ctx).Model(&RoutingAttemptOutcome{}).
		Select("channel_id, error_code, max(status_code) AS status_code, count(*) AS failures, max(request_ts) AS last_ms").
		Where("request_ts >= ? AND outcome_at >= ? AND channel_id > 0 AND error_code IN ?", since.Add(-time.Hour).UnixMilli(), since, codes).
		Group("channel_id, error_code").
		Scan(&rows).Error
	return rows, err
}
