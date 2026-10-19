package worker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func setupRoutingWorkerTestDB(t *testing.T) func() {
	t.Helper()
	previousDB := model.CoreDB()
	previousTelemetry := model.ConfiguredTelemetryDB()
	// Canceled SQLite transactions can discard the last pooled connection.
	// A unique temporary file retains the fixture for the post-cancel checks.
	db, err := gorm.Open(gormsqlite.Open(filepath.Join(t.TempDir(), "routing_worker.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	if err := db.AutoMigrate(
		&model.RoutingDecision{},
		&model.RoutingOutcome{},
		&model.RoutingAttemptOutcome{},
	); err != nil {
		t.Fatalf("failed to migrate routing worker tables: %v", err)
	}
	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		model.SetTelemetryDB(previousTelemetry)
	}
}

type routingBackfillContextKey struct{}

func seedRoutingQualityCancellationFixture(t *testing.T, now time.Time) {
	t.Helper()
	userID := 12
	decision := model.RoutingDecision{
		RequestID: "req-context-backfill", DecisionAt: now.Add(-21 * time.Minute),
		IsPrimary: true, UserID: &userID, SessionID: "session-context-backfill",
		PromptHash: "prompt-context-backfill", ResolvedModel: "model-a", StrategyName: "baseline",
	}
	if err := model.TelemetryDB().Create(&decision).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.TelemetryDB().Create(&model.RoutingOutcome{
		RequestID: decision.RequestID, OutcomeAt: now.Add(-20 * time.Minute), DecisionID: decision.ID,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func observeRoutingBackfillQueries(t *testing.T, callback func(*gorm.DB)) func() {
	t.Helper()
	db := model.TelemetryDB()
	name := "test:routing-backfill-context"
	if err := db.Callback().Query().Before("gorm:query").Register(name, callback); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Row().Before("gorm:row").Register(name, callback); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Update().Before("gorm:update").Register(name, callback); err != nil {
		t.Fatal(err)
	}
	return func() {
		_ = db.Callback().Query().Remove(name)
		_ = db.Callback().Row().Remove(name)
		_ = db.Callback().Update().Remove(name)
	}
}

func TestRoutingQualityBackfillPropagatesCancellationThroughEveryPhase(t *testing.T) {
	for phase, name := range []string{"candidate", "decision", "retry-exists", "switch-exists", "update"} {
		t.Run(name, func(t *testing.T) {
			cleanup := setupRoutingWorkerTestDB(t)
			defer cleanup()
			now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
			seedRoutingQualityCancellationFixture(t, now)
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), routingBackfillContextKey{}, true))
			defer cancel()
			calls := 0
			remove := observeRoutingBackfillQueries(t, func(tx *gorm.DB) {
				if tx.Statement.Context.Value(routingBackfillContextKey{}) != true {
					t.Error("quality backfill lost its caller context")
				}
				// GORM builds an EXISTS subquery with a DryRun Query callback
				// before the actual Row call; it issues no database round trip.
				if tx.DryRun {
					return
				}
				calls++
				if calls == phase+1 {
					cancel()
				}
			})
			err := BackfillQualitySignalsAtContext(ctx, now)
			remove()
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("phase %s cancellation must be returned, got %v", name, err)
			}
			if calls != phase+1 {
				t.Fatalf("canceled phase %s issued later work: calls=%d", name, calls)
			}
			var outcome model.RoutingOutcome
			if err := model.TelemetryDB().First(&outcome).Error; err != nil {
				t.Fatal(err)
			}
			if outcome.UserRetriedWithin5Min != nil || outcome.UserSwitchedModel != nil {
				t.Fatal("canceled quality backfill changed the outcome")
			}
		})
	}
}

func TestRoutingQualityBackfillTaskHasBoundedDeadline(t *testing.T) {
	cleanup := setupRoutingWorkerTestDB(t)
	defer cleanup()
	stop := errors.New("observed scheduled query")
	calls := 0
	remove := observeRoutingBackfillQueries(t, func(tx *gorm.DB) {
		calls++
		deadline, ok := tx.Statement.Context.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
			t.Errorf("scheduled diagnostic query lacks its <=30s budget: deadline=%v present=%v", deadline, ok)
		}
		tx.AddError(stop)
	})
	defer remove()
	if err := NewRoutingQualityBackfillTask().Run(context.Background()); !errors.Is(err, stop) {
		t.Fatalf("query error must be preserved: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one bounded candidate read, got %d", calls)
	}
}

func TestRoutingQualityBackfillTaskDeadlineInterruptsExecutingSQL(t *testing.T) {
	cleanup := setupRoutingWorkerTestDB(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	remove := observeRoutingBackfillQueries(t, func(tx *gorm.DB) {
		deadline, ok := tx.Statement.Context.Deadline()
		if !ok || deadline.After(time.Now().Add(100*time.Millisecond)) {
			// The old Background query must fail immediately, rather than
			// leaving this deliberately expensive query running in a test.
			tx.AddError(errors.New("quality query lost short caller deadline"))
			return
		}
		// This driver defers its final SELECT until Rows.Next, outside its
		// interrupt handler. Put the expensive read first so QueryContext
		// executes it while the driver's cancellation handler is active.
		// There is no callback sleep or simulated timeout error.
		tx.Statement.SQL.Reset()
		tx.Statement.SQL.WriteString("WITH RECURSIVE slow(n) AS (VALUES(0) UNION ALL SELECT n+1 FROM slow WHERE n<1000000000) SELECT n FROM slow ORDER BY n DESC LIMIT 1; SELECT '' AS request_id")
		tx.Statement.Vars = nil
	})
	defer remove()
	started := time.Now()
	err := NewRoutingQualityBackfillTask().Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("executing query must return deadline cancellation: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("canceled statement kept running for %s", elapsed)
	}
}

func TestRoutingQualityTimeoutDoesNotSkipFollowingTask(t *testing.T) {
	cleanup := setupRoutingWorkerTestDB(t)
	defer cleanup()
	remove := observeRoutingBackfillQueries(t, func(tx *gorm.DB) { tx.AddError(context.DeadlineExceeded) })
	defer remove()
	financial := &routingQualityFollowingTask{}
	err := RunOnce(context.Background(), NewRoutingQualityBackfillTask(), financial)
	if !errors.Is(err, context.DeadlineExceeded) || financial.runs != 1 || financial.contextErr != nil {
		t.Fatalf("diagnostic timeout must remain visible and allow next task with live context: err=%v following_runs=%d following_ctx=%v", err, financial.runs, financial.contextErr)
	}
}

type routingQualityFollowingTask struct {
	runOnceTestTask
	contextErr error
}

func (t *routingQualityFollowingTask) Run(ctx context.Context) error {
	t.contextErr = ctx.Err()
	return t.runOnceTestTask.Run(ctx)
}

func TestRoutingQualityBackfill(t *testing.T) {
	cleanup := setupRoutingWorkerTestDB(t)
	defer cleanup()

	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	outcomeAt := now.Add(-5*time.Minute - 30*time.Second)
	userID := 12
	if err := model.CoreDB().Create(&model.RoutingDecision{
		RequestID:     "req-original",
		DecisionAt:    outcomeAt.Add(-time.Second),
		IsPrimary:     true,
		UserID:        &userID,
		SessionID:     "session-a",
		PromptHash:    "prompt-a",
		ResolvedModel: "gpt-a",
		StrategyName:  "baseline",
	}).Error; err != nil {
		t.Fatalf("failed to seed original decision: %v", err)
	}
	if err := model.CoreDB().Create(&model.RoutingOutcome{
		RequestID:  "req-original",
		OutcomeAt:  outcomeAt,
		DecisionID: 1,
	}).Error; err != nil {
		t.Fatalf("failed to seed outcome: %v", err)
	}
	if err := model.CoreDB().Create(&model.RoutingDecision{
		RequestID:     "req-retry",
		DecisionAt:    outcomeAt.Add(time.Minute),
		IsPrimary:     true,
		UserID:        &userID,
		SessionID:     "session-a",
		PromptHash:    "prompt-a",
		ResolvedModel: "gpt-a",
		StrategyName:  "baseline",
	}).Error; err != nil {
		t.Fatalf("failed to seed retry decision: %v", err)
	}
	if err := model.CoreDB().Create(&model.RoutingDecision{
		RequestID:     "req-switched",
		DecisionAt:    outcomeAt.Add(2 * time.Minute),
		IsPrimary:     true,
		UserID:        &userID,
		SessionID:     "session-a",
		PromptHash:    "prompt-b",
		ResolvedModel: "gpt-b",
		StrategyName:  "baseline",
	}).Error; err != nil {
		t.Fatalf("failed to seed switched decision: %v", err)
	}

	if err := BackfillQualitySignalsAt(now); err != nil {
		t.Fatalf("backfill failed: %v", err)
	}

	var outcome model.RoutingOutcome
	if err := model.CoreDB().Where("request_id = ?", "req-original").First(&outcome).Error; err != nil {
		t.Fatalf("failed to reload outcome: %v", err)
	}
	if outcome.UserRetriedWithin5Min == nil || !*outcome.UserRetriedWithin5Min {
		t.Fatalf("expected retried=true, got %#v", outcome.UserRetriedWithin5Min)
	}
	if outcome.UserSwitchedModel == nil || !*outcome.UserSwitchedModel {
		t.Fatalf("expected switched=true, got %#v", outcome.UserSwitchedModel)
	}
}

func TestRoutingQualityBackfillCatchesDelayedOutcomes(t *testing.T) {
	cleanup := setupRoutingWorkerTestDB(t)
	defer cleanup()

	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	outcomeAt := now.Add(-20 * time.Minute)
	decision := &model.RoutingDecision{
		RequestID:     "req-delayed",
		DecisionAt:    outcomeAt.Add(-time.Second),
		IsPrimary:     true,
		SessionID:     "session-delayed",
		PromptHash:    "prompt-delayed",
		ResolvedModel: "gpt-a",
		StrategyName:  "baseline",
	}
	if err := model.CoreDB().Create(decision).Error; err != nil {
		t.Fatalf("failed to seed delayed decision: %v", err)
	}
	if err := model.CoreDB().Create(&model.RoutingOutcome{
		RequestID:  "req-delayed",
		OutcomeAt:  outcomeAt,
		DecisionID: decision.ID,
	}).Error; err != nil {
		t.Fatalf("failed to seed delayed outcome: %v", err)
	}

	if err := BackfillQualitySignalsAt(now); err != nil {
		t.Fatalf("backfill failed: %v", err)
	}

	var outcome model.RoutingOutcome
	if err := model.CoreDB().Where("request_id = ?", "req-delayed").First(&outcome).Error; err != nil {
		t.Fatalf("failed to reload delayed outcome: %v", err)
	}
	if outcome.UserRetriedWithin5Min == nil || outcome.UserSwitchedModel == nil {
		t.Fatalf("expected delayed outcome to be backfilled, got retry=%#v switch=%#v", outcome.UserRetriedWithin5Min, outcome.UserSwitchedModel)
	}
}
