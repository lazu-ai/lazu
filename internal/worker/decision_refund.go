package worker

import (
	"context"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
)

type DecisionRefundTask struct{}

func NewDecisionRefundTask() *DecisionRefundTask    { return &DecisionRefundTask{} }
func (*DecisionRefundTask) Name() string            { return "decision_refunds" }
func (*DecisionRefundTask) Interval() time.Duration { return 15 * time.Second }
func (*DecisionRefundTask) Run(context.Context) error {
	return model.ReconcileDecisionGenerationRefunds()
}
