package worker

import (
	"time"

	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// Reconcile accounting from the immutable retry payload even after membership
// or Key changes. Never read current membership to decide a refund.
func finalizeProjectSettlement(projectID, payerID, actorID, tokenID int, requestID string, amount billing.MicroUSD, createdAt time.Time, metadata map[string]any) error {
	if projectID <= 0 {
		return nil
	}
	occurredAt := createdAt
	if value, ok := metadata["budget_occurred_at"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil && !parsed.IsZero() {
			occurredAt = parsed
		}
	}
	sessionID, _ := metadata["session_id"].(string)
	agentID, _ := metadata["agent_id"].(string)
	return model.FinalizeProjectSpendBudget(model.SpendBudgetChargeInput{
		ProjectID: projectID, PayerUserID: payerID, ActorUserID: actorID,
		UserID: actorID, TokenID: tokenID, RequestID: requestID,
		SessionID: sessionID, AgentID: agentID, DeltaMicrousd: amount, OccurredAt: occurredAt,
	})
}
