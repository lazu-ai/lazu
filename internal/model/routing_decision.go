package model

import (
	"time"
)

// RoutingDecision records which source the baseline router picked for one
// request and why. It is the "before" half of a request; RoutingOutcome is the
// "after" half. Only the columns below are read (session audit, dashboard
// rollup, health, model ops); the ~45 experiment and request-feature columns
// that came with the retired routing research platform were dropped in schema
// version 15 (see dropRetiredRoutingColumns).
type RoutingDecision struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	RequestID      string    `gorm:"size:64;not null;index:idx_decision_request" json:"request_id"`
	IdempotencyKey *string   `gorm:"size:80;uniqueIndex:idx_decision_idempotency" json:"-"`
	DecisionAt     time.Time `gorm:"not null;index:idx_decision_strategy,priority:2;index:idx_decision_user,priority:2" json:"decision_at"`
	IsPrimary      bool      `gorm:"not null;index" json:"is_primary"`
	UserID         *int      `gorm:"index:idx_decision_user,priority:1" json:"user_id,omitempty"`
	TokenID        *int      `json:"token_id,omitempty"`
	SessionID      string    `gorm:"size:64;index:idx_decision_session" json:"session_id,omitempty"`
	RequestedModel string    `gorm:"size:128" json:"requested_model,omitempty"`
	ResolvedModel  string    `gorm:"size:128" json:"resolved_model,omitempty"`
	// PromptHash feeds the retry / model-switch backfill
	// (worker/routing_quality_backfill.go), which session audit reads.
	PromptHash     string     `gorm:"size:64;index:idx_decision_prompt" json:"prompt_hash,omitempty"`
	StrategyName   string     `gorm:"size:64;not null;index:idx_decision_strategy,priority:1" json:"strategy_name"`
	ChoseChannel   *int       `json:"chose_channel,omitempty"`
	ChoseModel     string     `gorm:"size:128" json:"chose_model,omitempty"`
	ChoseProvider  string     `gorm:"size:64" json:"chose_provider,omitempty"`
	DecisionReason string     `gorm:"size:64" json:"decision_reason,omitempty"`
	CreatedAt      *time.Time `gorm:"autoCreateTime" json:"created_at,omitempty"`
}

func (RoutingDecision) TableName() string {
	return "routing_decision"
}

// retiredRoutingDecisionColumns are the experiment and request-feature
// columns the routing research platform added. Nothing reads them; most were
// never written or were written and never read.
var retiredRoutingDecisionColumns = []string{
	"experiment_id", "experiment_name", "variant_name", "assignment_seed",
	"user_tier", "user_org_id", "token_group", "token_metadata", "agent_id",
	"region", "hour_of_day", "day_of_week", "routing_modifier",
	"input_modality", "output_modality", "prompt_tokens", "prompt_prefix_hash",
	"embedding_hash", "embedding_model", "embedding_version",
	"has_tools", "tool_count", "tool_definitions_hash",
	"has_vision", "vision_image_count", "has_audio", "audio_seconds",
	"has_video", "video_seconds", "contains_code", "contains_math", "language",
	"is_agent_loop", "is_streaming", "strategy_version", "config_snapshot_id",
	"decision_score", "candidate_channels", "features_used", "decision_meta",
	"predicted_input_cost_usd", "predicted_output_cost_usd",
	"predicted_total_cost_usd", "predicted_p50_latency_ms",
	"predicted_p95_latency_ms",
}
