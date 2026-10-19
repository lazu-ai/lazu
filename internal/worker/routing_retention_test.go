package worker

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
)

func TestRoutingRetention(t *testing.T) {
	cleanup := setupRoutingWorkerTestDB(t)
	defer cleanup()

	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	oldDecisionAt := now.AddDate(0, 0, -91)
	freshDecisionAt := now.AddDate(0, 0, -10)
	oldOutcomeAt := now.AddDate(-1, 0, -1)
	freshOutcomeAt := now.AddDate(0, 0, -10)
	oldAttemptAt := now.AddDate(0, 0, -91)
	freshAttemptAt := now.AddDate(0, 0, -10)

	if err := model.CoreDB().Create(&model.RoutingDecision{
		RequestID:    "old-decision",
		DecisionAt:   oldDecisionAt,
		IsPrimary:    true,
		StrategyName: "baseline",
	}).Error; err != nil {
		t.Fatalf("failed to seed old decision: %v", err)
	}
	if err := model.CoreDB().Create(&model.RoutingDecision{
		RequestID:    "fresh-decision",
		DecisionAt:   freshDecisionAt,
		IsPrimary:    true,
		StrategyName: "baseline",
	}).Error; err != nil {
		t.Fatalf("failed to seed fresh decision: %v", err)
	}
	if err := model.CoreDB().Create(&model.RoutingOutcome{
		RequestID:  "old-outcome",
		OutcomeAt:  oldOutcomeAt,
		DecisionID: 1,
	}).Error; err != nil {
		t.Fatalf("failed to seed old outcome: %v", err)
	}
	if err := model.CoreDB().Create(&model.RoutingOutcome{
		RequestID:  "fresh-outcome",
		OutcomeAt:  freshOutcomeAt,
		DecisionID: 2,
	}).Error; err != nil {
		t.Fatalf("failed to seed fresh outcome: %v", err)
	}
	if err := model.CoreDB().Create(&model.RoutingAttemptOutcome{
		RequestID: "old-attempt", AttemptIndex: 0, OutcomeAt: oldAttemptAt,
		DecisionID: 1, ChannelID: 10, FinalGroup: model.ChannelLaneDirect, StatusCode: 502,
	}).Error; err != nil {
		t.Fatalf("failed to seed old attempt: %v", err)
	}
	if err := model.CoreDB().Create(&model.RoutingAttemptOutcome{
		RequestID: "fresh-attempt", AttemptIndex: 0, OutcomeAt: freshAttemptAt,
		DecisionID: 2, ChannelID: 11, FinalGroup: model.ChannelLaneDirect, StatusCode: 503,
	}).Error; err != nil {
		t.Fatalf("failed to seed fresh attempt: %v", err)
	}
	if err := PurgeOldRoutingDataAt(now); err != nil {
		t.Fatalf("purge failed: %v", err)
	}

	assertCount(t, &model.RoutingDecision{}, "request_id = ?", "old-decision", 0)
	assertCount(t, &model.RoutingDecision{}, "request_id = ?", "fresh-decision", 1)
	assertCount(t, &model.RoutingOutcome{}, "request_id = ?", "old-outcome", 0)
	assertCount(t, &model.RoutingOutcome{}, "request_id = ?", "fresh-outcome", 1)
	assertCount(t, &model.RoutingAttemptOutcome{}, "request_id = ?", "old-attempt", 0)
	assertCount(t, &model.RoutingAttemptOutcome{}, "request_id = ?", "fresh-attempt", 1)
}

func assertCount(t *testing.T, table any, query string, arg any, want int64) {
	t.Helper()
	var count int64
	if err := model.CoreDB().Model(table).Where(query, arg).Count(&count).Error; err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != want {
		t.Fatalf("count=%d want=%d for %v", count, want, arg)
	}
}
