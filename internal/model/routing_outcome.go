package model

import (
	"time"

	"gorm.io/gorm"
)

// RoutingOutcome is what actually happened to one request: status, tokens,
// cost, margin, latency. UserID, ProjectID and ModelName are copied here from
// the decision so the row still says whose request it was after the decision
// row is purged (decisions are kept 90 days, outcomes a year).
//
// Schema version 15 dropped the columns the retired routing research platform
// added and nothing reads (see retiredRoutingOutcomeColumns).
type RoutingOutcome struct {
	ChannelName        string    `gorm:"size:191;default:''" json:"channel_name,omitempty"`
	RequestID          string    `gorm:"size:64;primaryKey" json:"request_id"`
	OutcomeAt          time.Time `gorm:"not null;index:idx_outcome_at" json:"outcome_at"`
	DecisionID         int64     `gorm:"not null;index:idx_outcome_decision" json:"decision_id"`
	UserID             *int      `gorm:"index:idx_outcome_user,priority:1" json:"user_id,omitempty"`
	ProjectID          *int      `gorm:"index" json:"project_id,omitempty"`
	ModelName          string    `gorm:"size:128" json:"model_name,omitempty"`
	ChannelID          *int      `gorm:"index:idx_outcome_channel" json:"channel_id,omitempty"`
	FinalGroup         string    `gorm:"size:64;index:idx_outcome_final_group" json:"final_group,omitempty"`
	StatusCode         *int      `gorm:"index:idx_outcome_status" json:"status_code,omitempty"`
	ErrorCode          string    `gorm:"size:64" json:"error_code,omitempty"`
	ErrorType          string    `gorm:"size:64" json:"error_type,omitempty"`
	ProviderAttempted  *bool     `json:"provider_attempted,omitempty"`
	HealthEligible     *bool     `json:"health_eligible,omitempty"`
	HealthFailed       *bool     `json:"health_failed,omitempty"`
	IsRetry            *bool     `json:"is_retry,omitempty"`
	RetryCount         *int      `json:"retry_count,omitempty"`
	ActualInputTokens  *int      `json:"actual_input_tokens,omitempty"`
	CachedInputTokens  *int      `json:"cached_input_tokens,omitempty"`
	CacheWriteTokens   *int      `json:"cache_write_tokens,omitempty"`
	CacheWrite5mTokens *int      `gorm:"column:cache_write_5m_tokens" json:"cache_write_5m_tokens,omitempty"`
	CacheWrite1hTokens *int      `gorm:"column:cache_write_1h_tokens" json:"cache_write_1h_tokens,omitempty"`
	CacheMissTokens    *int      `json:"cache_miss_tokens,omitempty"`
	ActualOutputTokens *int      `json:"actual_output_tokens,omitempty"`
	ReasoningTokens    *int      `json:"reasoning_tokens,omitempty"`
	AudioInputTokens   *int      `json:"audio_input_tokens,omitempty"`
	AudioOutputTokens  *int      `json:"audio_output_tokens,omitempty"`
	ImageInputTokens   *int      `json:"image_input_tokens,omitempty"`
	ImageOutputTokens  *int      `json:"image_output_tokens,omitempty"`
	UpstreamCostUSD    *float64  `gorm:"type:decimal(12,8)" json:"upstream_cost_usd,omitempty"`
	ChargedCostUSD     *float64  `gorm:"type:decimal(12,8)" json:"charged_cost_usd,omitempty"`
	GrossMarginUSD     *float64  `gorm:"type:decimal(12,8)" json:"gross_margin_usd,omitempty"`
	TotalDurationMs    *int      `json:"total_duration_ms,omitempty"`
	TTFTMs             *int      `gorm:"column:ttft_ms" json:"ttft_ms,omitempty"`
	// AnswerTTFTMs is the time to the first answer token, after any reasoning.
	// Decode speed runs from here to the end over output minus reasoning
	// tokens; TTFTMs stays the first token of any kind.
	AnswerTTFTMs *int `gorm:"column:answer_ttft_ms" json:"answer_ttft_ms,omitempty"`
	// The longest wall-clock gap between two output chunks of this reply.
	// Average throughput cannot see a stream that stalls; this can.
	MaxStallMs *int `json:"max_stall_ms,omitempty"`
	// What the upstream said ended the reply, in OpenAI's vocabulary. "length"
	// is the one that matters: it means the reply was cut off at the output
	// ceiling rather than finished.
	FinishReason          string     `gorm:"size:32" json:"finish_reason,omitempty"`
	WasStreaming          *bool      `json:"was_streaming,omitempty"`
	StreamAborted         *bool      `json:"stream_aborted,omitempty"`
	UserRetriedWithin5Min *bool      `gorm:"column:user_retried_within_5min" json:"user_retried_within_5min,omitempty"`
	UserSwitchedModel     *bool      `json:"user_switched_model,omitempty"`
	CreatedAt             *time.Time `gorm:"autoCreateTime" json:"created_at,omitempty"`
}

type RoutingOutcomeUsageSnapshot struct {
	ActualInputTokens  int
	CachedInputTokens  int
	CacheWriteTokens   int
	CacheWrite5mTokens int
	CacheWrite1hTokens int
	CacheMissTokens    int
	ActualOutputTokens int
	ReasoningTokens    int
	AudioInputTokens   int
	AudioOutputTokens  int
	ImageInputTokens   int
	ImageOutputTokens  int
	ChargedCostUSD     float64
	HasChargedCost     bool
	UpstreamCostUSD    float64
	HasUpstreamCost    bool
	GrossMarginUSD     float64
	HasGrossMargin     bool
}

func (RoutingOutcome) TableName() string {
	return "routing_outcome"
}

func (r *RoutingOutcome) BeforeSave(*gorm.DB) error {
	r.FinalGroup = NormalizeChannelLane(r.FinalGroup)
	return nil
}

// retiredRoutingOutcomeColumns were added by the routing research platform and
// are either never written or never read. The first nine were already removed
// from the struct in 2026-09 but AutoMigrate never drops a column.
var retiredRoutingOutcomeColumns = []string{
	"predicted_input_tokens", "inter_token_p50_ms", "inter_token_p95_ms",
	"response_truncated", "response_length_chars", "response_length_tokens",
	"tool_calls_returned", "tool_call_iterations", "panic_recovered",
	"any_provider_attempted", "upstream_request_id", "fallback_chain",
	"video_seconds_charged", "promo_credits_used_usd", "queue_wait_ms",
	"dns_lookup_ms", "tcp_handshake_ms", "tls_handshake_ms", "ttfb_ms",
	"stream_chunks_sent", "stream_total_bytes", "user_session_continued",
	"user_explicit_feedback", "user_feedback_reason", "feedback_received_at",
}
