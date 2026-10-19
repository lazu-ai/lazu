package router

import (
	"time"

	"github.com/lazu-ai/lazu/internal/model"
)

// WriteDecisionLog records the baseline route selected for one real request.
func WriteDecisionLog(req *RequestContext, decision *Decision) int64 {
	if model.CoreDB() == nil || req == nil || decision == nil {
		return 0
	}
	row := &model.RoutingDecision{
		RequestID:      req.RequestID,
		DecisionAt:     time.Now().UTC(),
		IsPrimary:      true,
		UserID:         decisionIntPtr(req.UserID),
		TokenID:        decisionIntPtr(req.TokenID),
		SessionID:      req.SessionID,
		RequestedModel: req.RequestedModel,
		ResolvedModel:  req.ResolvedModel,
		StrategyName:   decisionMetaString(decision.Meta, "strategy_name", "router"),
		ChoseChannel:   decisionIntPtr(decision.ChannelID),
		ChoseModel:     decision.Model,
		ChoseProvider:  decision.Provider,
		DecisionReason: decision.Reason,
	}
	if row.StrategyName == "" {
		row.StrategyName = "baseline"
	}
	if features := req.Features; features != nil {
		row.PromptHash = features.PromptHash
	}
	return model.QueueRoutingDecision(req.GinContext, row)
}

func decisionMetaString(meta map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := meta[key].(string); ok {
			return value
		}
	}
	return ""
}

func decisionIntPtr(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}
