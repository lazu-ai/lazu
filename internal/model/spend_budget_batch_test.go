package model

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func spendBudgetBatchFixture(t *testing.T, dialect string, operations bool) *gorm.DB {
	t.Helper()
	config := &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}
	var db *gorm.DB
	var err error
	if dialect == "postgres" {
		dsn := os.Getenv("TEST_GATEWAY_CORE_DSN")
		if dsn == "" {
			t.Skip("isolated TEST_GATEWAY_CORE_DSN required")
		}
		base, err := gorm.Open(postgres.Open(dsn), config)
		if err != nil {
			t.Fatal(err)
		}
		schema := "budget_batch_" + strings.ReplaceAll(strings.ToLower(common.GenerateRequestID()), "-", "")
		if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			base.Exec("DROP SCHEMA " + schema + " CASCADE")
			connection, _ := base.DB()
			connection.Close()
		})
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		db, err = gorm.Open(postgres.Open(parsed.String()), config)
	} else {
		db, err = gorm.Open(gormsqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), config)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection, _ := db.DB(); connection.Close() })
	models := []interface{}{&SpendBudget{}, &SpendBudgetCharge{}, &ProjectSpend{}}
	if operations {
		models = append(models, &RedisLedgerOperation{})
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSpendBudgetUsedBatchMatchesPeriodsScopesRefundsAndLegacy(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := spendBudgetBatchFixture(t, dialect, true)
			now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.FixedZone("local", 8*3600))
			day := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
			monthEnd := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
			budgets := []SpendBudget{
				{ID: 1, ProjectID: 1, Scope: SpendBudgetScopeOrg, Period: "month"},
				{ID: 2, ProjectID: 1, Scope: SpendBudgetScopeOrg, Period: "day"},
				{ID: 3, ProjectID: 1, Scope: SpendBudgetScopeUser, UserID: 7, Period: "month"},
				{ID: 4, ProjectID: 1, Scope: SpendBudgetScopeToken, TokenID: 42, Period: "day"},
				{ID: 5, ProjectID: 2, Scope: SpendBudgetScopeOrg, Period: "month"},
				{ID: 6, Scope: SpendBudgetScopeOrg, Period: "month"},
				{ID: 7, Scope: SpendBudgetScopeUser, UserID: 7, Period: "day"},
				{ID: 8, Scope: SpendBudgetScopeToken, TokenID: 42, Period: "month"},
				{ID: 9, ProjectID: 99, Scope: SpendBudgetScopeOrg, Period: "month"},
			}
			spends := []ProjectSpend{
				{EventKey: "canonical", ProjectID: 1, ActorUserID: 7, TokenID: 42, RequestID: "canonical", AmountMicrousd: 100, OccurredAt: day, Aggregated: true},
				{EventKey: "refund", ProjectID: 1, ActorUserID: 7, TokenID: 42, RequestID: "refund", AmountMicrousd: -20, OccurredAt: day.Add(time.Hour), Aggregated: true},
				{EventKey: "other-actor", ProjectID: 1, ActorUserID: 8, TokenID: 43, RequestID: "other-actor", AmountMicrousd: 50, OccurredAt: day.Add(-time.Hour)},
				{EventKey: "actorless", ProjectID: 1, TokenID: 42, RequestID: "actorless", AmountMicrousd: 30, OccurredAt: day.Add(2 * time.Hour)},
				{EventKey: "day-end", ProjectID: 1, ActorUserID: 7, TokenID: 42, RequestID: "day-end", AmountMicrousd: 40, OccurredAt: day.AddDate(0, 0, 1)},
				{EventKey: "month-end", ProjectID: 1, ActorUserID: 7, TokenID: 42, RequestID: "month-end", AmountMicrousd: 1000, OccurredAt: monthEnd},
				{EventKey: "other-project", ProjectID: 2, ActorUserID: 7, TokenID: 42, RequestID: "other-project", AmountMicrousd: 200, OccurredAt: day},
			}
			if err := db.Create(&spends).Error; err != nil {
				t.Fatal(err)
			}
			charges := []SpendBudgetCharge{
				{BudgetID: 1, RequestID: "canonical", AmountMicrousd: 999, CreatedAt: day},
				{BudgetID: 1, RequestID: "legacy", AmountMicrousd: 10, CreatedAt: day},
				{BudgetID: 1, LedgerOperationID: "already-modern", AmountMicrousd: 333, CreatedAt: day},
				{BudgetID: 2, RequestID: "day-legacy", AmountMicrousd: 11, CreatedAt: day},
				{BudgetID: 3, UserID: 88, RequestID: "legacy-member-definition", AmountMicrousd: 5, CreatedAt: day},
				{BudgetID: 6, AmountMicrousd: 7, CreatedAt: day},
				{BudgetID: 6, LedgerOperationID: "modern-copy", AmountMicrousd: 999, CreatedAt: day},
				{BudgetID: 7, UserID: 7, AmountMicrousd: 3, CreatedAt: day},
				{BudgetID: 7, UserID: 8, AmountMicrousd: 90, CreatedAt: day},
				{BudgetID: 8, TokenID: 42, AmountMicrousd: 4, CreatedAt: day},
				{BudgetID: 8, TokenID: 43, AmountMicrousd: 90, CreatedAt: day},
			}
			if err := db.Create(&charges).Error; err != nil {
				t.Fatal(err)
			}
			operations := []RedisLedgerOperation{
				{ID: "settle", Kind: "settle", ActorID: 7, TokenID: 42, ProjectID: 1, Amount: 100, CreatedAt: day},
				{ID: "refund", Kind: "refund", ActorID: 7, TokenID: 42, ProjectID: 1, Amount: -25, CreatedAt: day},
				{ID: "other", Kind: "settle", ActorID: 8, TokenID: 43, ProjectID: 1, Amount: 50, CreatedAt: day.Add(-time.Hour)},
				{ID: "other-project", Kind: "settle", ActorID: 7, TokenID: 42, ProjectID: 2, Amount: 200, CreatedAt: day},
				{ID: "credit", Kind: "credit", ActorID: 7, TokenID: 42, Amount: 999, CreatedAt: day},
			}
			if err := db.Create(&operations).Error; err != nil {
				t.Fatal(err)
			}
			want := map[uint]billing.MicroUSD{1: 210, 2: 121, 3: 125, 4: 110, 5: 200, 6: 332, 7: 278, 8: 279, 9: 0}
			queries := 0
			callback := "test:budget_grouped_sum"
			db.Callback().Row().After("gorm:row").Register(callback, func(tx *gorm.DB) {
				if strings.Contains(tx.Statement.SQL.String(), "WITH budgets") {
					queries++
				}
			})
			t.Cleanup(func() { db.Callback().Row().Remove(callback) })
			got, err := readSpendBudgetUsedBatchWithDB(db, append(budgets, budgets[0]), now)
			if err != nil {
				t.Fatal(err)
			}
			if queries != 1 {
				t.Fatalf("budget page used %d SUM statements, want one grouped statement", queries)
			}
			for _, budget := range budgets {
				start, end := spendBudgetPeriodWindow(budget.Period, now)
				single, err := sumSpendMicrousdForBudgetWithDB(db, budget, start, end)
				if err != nil || single != want[budget.ID] || got[budget.ID] != single {
					t.Fatalf("budget %d: batch=%d single=%d want=%d error=%v", budget.ID, got[budget.ID], single, want[budget.ID], err)
				}
			}
		})
	}
}

func TestSpendBudgetUsedBatchWithoutModernLedgerAndBoundedPages(t *testing.T) {
	db := spendBudgetBatchFixture(t, "sqlite", false)
	assertSpendBudgetBatchPages(t, db, false)
}

func TestSpendBudgetUsedBatchPostgresBoundedPages(t *testing.T) {
	db := spendBudgetBatchFixture(t, "postgres", true)
	assertSpendBudgetBatchPages(t, db, true)
}

func assertSpendBudgetBatchPages(t *testing.T, db *gorm.DB, modern bool) {
	t.Helper()
	now := time.Now().UTC()
	budgets := make([]SpendBudget, 201)
	charges := make([]SpendBudgetCharge, 201)
	operations := make([]RedisLedgerOperation, 201)
	for i := range budgets {
		budgets[i] = SpendBudget{ID: uint(i + 1), Scope: SpendBudgetScopeToken, TokenID: i + 1, Period: "month"}
		charges[i] = SpendBudgetCharge{BudgetID: uint(i + 1), TokenID: i + 1, AmountMicrousd: billing.MicroUSD(i + 1), CreatedAt: now, LedgerOperationID: fmt.Sprintf("legacy-%d", i)}
		operations[i] = RedisLedgerOperation{ID: fmt.Sprintf("modern-%d", i), Kind: "settle", TokenID: i + 1, Amount: int64(i + 1), CreatedAt: now}
	}
	if err := db.Create(&charges).Error; err != nil {
		t.Fatal(err)
	}
	if modern {
		// Once modern operations exist, their copied per-budget charges must
		// be excluded. Each Key still receives its exact finalized amount.
		if err := db.Create(&operations).Error; err != nil {
			t.Fatal(err)
		}
	}
	queries := 0
	db.Callback().Row().After("gorm:row").Register("test:budget_pages", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "WITH budgets") {
			queries++
		}
	})
	t.Cleanup(func() { db.Callback().Row().Remove("test:budget_pages") })
	// Duplicates across all three batch boundaries neither add a fourth query
	// nor duplicate an individual budget's finalized amount.
	wanted := append(append([]SpendBudget(nil), budgets...), budgets[0], budgets[100], budgets[200])
	got, err := readSpendBudgetUsedBatchWithDB(db, wanted, now)
	if err != nil || queries != 3 || len(got) != 201 {
		t.Fatalf("bounded batch: rows=%d queries=%d error=%v", len(got), queries, err)
	}
	for i := range budgets {
		if got[uint(i+1)] != billing.MicroUSD(i+1) {
			t.Fatalf("legacy budget %d missing its charge", i+1)
		}
	}
}
