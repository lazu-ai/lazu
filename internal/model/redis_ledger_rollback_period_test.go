package model

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestLegacyLedgerRollbackGlobalBudgetPeriodChangeIntegration(t *testing.T) {
	redisURL := os.Getenv("TEST_LEDGER_ROLLBACK_REDIS_URL")
	if redisURL == "" {
		t.Skip("isolated TEST_LEDGER_ROLLBACK_REDIS_URL required")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	if size, err := client.DBSize(ctx).Result(); err != nil || size != 0 {
		client.Close()
		t.Fatalf("test requires an empty isolated Redis database: keys=%d err=%v", size, err)
	}
	oldRedis := common.RDB
	common.RDB = client
	t.Cleanup(func() {
		client.Del(ctx, ledger.Prefix+"maintenance")
		client.Close()
		common.RDB = oldRedis
	})
	if err := client.Set(ctx, ledger.Prefix+"maintenance", "rollback regression", 0).Err(); err != nil {
		t.Fatal(err)
	}
	db := boundaryTestDB(t)
	if err := db.AutoMigrate(&RedisLedgerOperation{}, &LedgerCounterRefresh{}, &LedgerCreditSync{}, &ProjectSpend{}, &SpendBudget{}, &SpendBudgetCharge{}); err != nil {
		t.Fatal(err)
	}
	t.Run("live writer lease prevents rollback", func(t *testing.T) {
		lease := ledger.Prefix + "nodes:review-live-writer"
		if err := client.Set(ctx, lease, "active", time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		defer client.Del(ctx, lease)
		if _, err := PrepareLegacyLedgerRollback(ctx, true); err == nil || !strings.Contains(err.Error(), "writer leases") {
			t.Fatalf("live writer accepted: %v", err)
		}
	})
	at := time.Date(2026, 10, 3, 12, 34, 56, 0, time.UTC)
	budget := SpendBudget{ID: 1, Scope: SpendBudgetScopeOrg, Period: SpendBudgetPeriodDay, LimitMicrousd: 1000, Enabled: true}
	event := ledger.Event{Input: ledger.Input{RequestID: "global-period-change", PayerID: 1, ActorID: 1, TokenID: 2, Amount: 700, OccurredAt: at, Budgets: LedgerBudgets([]SpendBudget{budget}, at)}, OperationID: "global-period-change", Kind: "settle"}
	immutable, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	op := RedisLedgerOperation{ID: ledgerOperationID(event.OperationID, event.Kind), Amount: event.Amount, PayerID: event.PayerID, ActorID: event.ActorID, TokenID: event.TokenID, Kind: event.Kind, Input: string(immutable), CreatedAt: at}
	if err := db.Create(&op).Error; err != nil {
		t.Fatal(err)
	}
	budget.Period = SpendBudgetPeriodMonth
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	assertLegacySpend := func() SpendBudgetCharge {
		t.Helper()
		report, err := PrepareLegacyLedgerRollback(ctx, true)
		if err != nil || report.BudgetCharges != 1 {
			t.Fatalf("rollback=%+v err=%v", report, err)
		}
		start, end := spendBudgetPeriodWindow(budget.Period, at)
		// This is the old release's global-budget SUM: it deliberately does
		// not filter period metadata or exclude modern ledger_operation_id.
		query, err := scopedSpendBudgetChargeQuery(db, budget, start, end)
		if err != nil {
			t.Fatal(err)
		}
		var sum billing.MicroUSD
		if err := query.Select("COALESCE(SUM(amount_microusd),0)").Scan(&sum).Error; err != nil {
			t.Fatal(err)
		}
		if sum != 700 {
			t.Fatalf("legacy global budget double counted the operation: %d want700", sum)
		}
		var charges []SpendBudgetCharge
		if err := db.Where("ledger_operation_id = ? AND budget_id = ?", op.ID, budget.ID).Find(&charges).Error; err != nil || len(charges) != 1 {
			t.Fatalf("projection rows=%+v err=%v", charges, err)
		}
		row := charges[0]
		if row.Period != budget.Period || row.PeriodStart != start || row.PeriodEnd != end || row.Scope != budget.Scope || row.AmountMicrousd != 700 {
			t.Fatalf("rollback did not prefer current matching budget: %+v", row)
		}
		var retained RedisLedgerOperation
		if err := db.First(&retained, "id = ?", op.ID).Error; err != nil || retained.Input != string(immutable) {
			t.Fatalf("rollback changed immutable financial proof: err=%v", err)
		}
		return row
	}
	first := assertLegacySpend()
	if repeated := assertLegacySpend(); repeated.ID != first.ID {
		t.Fatal("rollback retry replaced the same operation projection")
	}
	// Re-preparing a rollback after another admin period change updates the
	// same derived row; the original day proof stays in permanent op.Input.
	budget.Period = SpendBudgetPeriodDay
	if err := db.Model(&budget).Update("period", budget.Period).Error; err != nil {
		t.Fatal(err)
	}
	if updated := assertLegacySpend(); updated.ID != first.ID {
		t.Fatal("period change appended another legacy charge")
	}
	duplicate := first
	duplicate.ID = 0
	if err := db.Create(&duplicate).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareLegacyLedgerRollback(ctx, true); err == nil {
		t.Fatal("pre-existing duplicate proofs were silently accepted")
	}
	t.Run("same-amount-wrong-subject-preserves-daily", func(t *testing.T) {
		db := boundaryTestDB(t)
		if err := db.AutoMigrate(&RedisLedgerOperation{}, &LedgerCounterRefresh{}, &LedgerCreditSync{}, &ProjectSpend{}, &SpendBudget{}, &SpendBudgetCharge{}); err != nil {
			t.Fatal(err)
		}
		event := ledger.Event{Input: ledger.Input{RequestID: "project-conflict", PayerID: 1, ActorID: 1, ProjectID: 7, TokenID: 2, Amount: 700, OccurredAt: at}, OperationID: "project-conflict", Kind: "settle"}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&RedisLedgerOperation{ID: ledgerOperationID(event.OperationID, event.Kind), ProjectID: event.ProjectID, ActorID: event.ActorID, PayerID: event.PayerID, TokenID: event.TokenID, Amount: event.Amount, Input: string(raw)}).Error; err != nil {
			t.Fatal(err)
		}
		input := SpendBudgetChargeInput{ProjectID: event.ProjectID, ActorUserID: event.ActorID, PayerUserID: event.PayerID, TokenID: event.TokenID, RequestID: event.OperationID, OccurredAt: at}
		for _, row := range []ProjectSpend{
			{EventKey: "daily:protected", ProjectID: 7, AmountMicrousd: 700, OccurredAt: at, Aggregated: true},
			{EventKey: projectSpendEventKey(input), ProjectID: 7, ActorUserID: 99, PayerUserID: 1, TokenID: 2, RequestID: event.OperationID, AmountMicrousd: 700, OccurredAt: at},
		} {
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if _, err := PrepareLegacyLedgerRollback(ctx, true); err == nil || !strings.Contains(err.Error(), "legacy project spend conflicts") {
			t.Fatalf("rollback accepted wrong scoped subject: %v", err)
		}
		var retained int64
		if err := db.Model(&ProjectSpend{}).Where("aggregated = ?", true).Count(&retained).Error; err != nil || retained != 1 {
			t.Fatalf("failed rollback removed daily proof: retained=%d err=%v", retained, err)
		}
	})
	t.Run("daily-amount-overflow-preserves-source", func(t *testing.T) {
		db := boundaryTestDB(t)
		if err := db.AutoMigrate(&RedisLedgerOperation{}, &LedgerCounterRefresh{}, &LedgerCreditSync{}, &ProjectSpend{}, &SpendBudget{}, &SpendBudgetCharge{}); err != nil {
			t.Fatal(err)
		}
		for _, row := range []ProjectSpend{
			{EventKey: "daily:maximum", ProjectID: 7, AmountMicrousd: billing.MicroUSD(math.MaxInt64), OccurredAt: at, Aggregated: true},
			{EventKey: "daily:one", ProjectID: 7, AmountMicrousd: 1, OccurredAt: at, Aggregated: true},
		} {
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if _, err := PrepareLegacyLedgerRollback(ctx, true); !errors.Is(err, ErrLedgerCounterRange) {
			t.Fatalf("rollback summed out-of-range money: %v", err)
		}
		var retained int64
		if err := db.Model(&ProjectSpend{}).Where("aggregated = ?", true).Count(&retained).Error; err != nil || retained != 2 {
			t.Fatalf("overflow removed the source rows: retained=%d err=%v", retained, err)
		}
	})
}

func TestLegacyRollbackRequiresWriterAttestationWithoutDependencies(t *testing.T) {
	if _, err := PrepareLegacyLedgerRollback(context.Background(), false); err == nil || !strings.Contains(err.Error(), "provide -writers-stopped") {
		t.Fatalf("missing attestation accepted: %v", err)
	}
}
