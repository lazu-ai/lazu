package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

// ReadSpendBudgetUsedBatch reads a console page's current finalized spend in
// grouped SQL, independent of whether a budget existed when the spend occurred.
// It has the same scopes, UTC periods and legacy replacement rules as the
// single-budget reader. Reservations are not finalized spend.
func ReadSpendBudgetUsedBatch(budgets []SpendBudget, now time.Time) (map[uint]billing.MicroUSD, error) {
	return readSpendBudgetUsedBatchWithDB(CoreDB(), budgets, now)
}

func readSpendBudgetUsedBatchWithDB(db *gorm.DB, budgets []SpendBudget, now time.Time) (map[uint]billing.MicroUSD, error) {
	result := make(map[uint]billing.MicroUSD, len(budgets))
	if len(budgets) == 0 {
		return result, nil
	}
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	wanted := make([]SpendBudget, 0, len(budgets))
	hasProject, hasGlobal := false, false
	for _, budget := range budgets {
		if _, seen := result[budget.ID]; seen {
			continue
		}
		if budget.ProjectID <= 0 && budget.Scope != SpendBudgetScopeOrg && budget.Scope != SpendBudgetScopeUser && budget.Scope != SpendBudgetScopeToken {
			return nil, fmt.Errorf("unsupported spend budget scope %q", budget.Scope)
		}
		result[budget.ID] = 0
		wanted = append(wanted, budget)
		hasProject = hasProject || budget.ProjectID > 0
		hasGlobal = hasGlobal || budget.ProjectID <= 0
	}
	hasOperations := hasGlobal && db.Migrator().HasTable(&RedisLedgerOperation{})
	// Seven parameters per budget stay below embedded SQLite's older 999 bind
	// limit. Query count grows by pages rather than by individual budgets.
	const batchSize = 100
	for offset := 0; offset < len(wanted); offset += batchSize {
		batch := wanted[offset:min(offset+batchSize, len(wanted))]
		values := make([]string, 0, len(batch))
		args := make([]interface{}, 0, len(batch)*7)
		value := "(CAST(? AS BIGINT), CAST(? AS BIGINT), CAST(? AS TEXT), CAST(? AS BIGINT), CAST(? AS BIGINT), ?, ?)"
		if db.Dialector.Name() == "postgres" {
			value = "(CAST(? AS BIGINT), CAST(? AS BIGINT), CAST(? AS TEXT), CAST(? AS BIGINT), CAST(? AS BIGINT), CAST(? AS TIMESTAMPTZ), CAST(? AS TIMESTAMPTZ))"
		}
		for _, budget := range batch {
			start, end := spendBudgetPeriodWindow(budget.Period, now)
			values = append(values, value)
			args = append(args, budget.ID, budget.ProjectID, budget.Scope, budget.UserID, budget.TokenID, time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC())
		}
		parts := make([]string, 0, 4)
		if hasProject {
			parts = append(parts, `SELECT b.id, ps.amount_microusd AS amount FROM budgets b
JOIN project_spends ps ON b.project_id > 0 AND ps.project_id = b.project_id
AND ps.occurred_at >= b.period_start AND ps.occurred_at < b.period_end
AND (b.scope <> 'user' OR ps.actor_user_id = b.user_id)
AND (b.scope <> 'token' OR ps.token_id = b.token_id)`)
			parts = append(parts, `SELECT b.id, c.amount_microusd AS amount FROM budgets b
JOIN spend_budget_charges c ON b.project_id > 0 AND c.budget_id = b.id
AND c.created_at >= b.period_start AND c.created_at < b.period_end
AND (c.ledger_operation_id = '' OR c.ledger_operation_id IS NULL)
WHERE NOT EXISTS (SELECT 1 FROM project_spends ps WHERE ps.project_id = b.project_id
AND ps.request_id = c.request_id AND ps.occurred_at >= b.period_start AND ps.occurred_at < b.period_end)`)
		}
		if hasGlobal {
			legacy := `SELECT b.id, c.amount_microusd AS amount FROM budgets b
JOIN spend_budget_charges c ON b.project_id <= 0 AND c.budget_id = b.id
AND c.created_at >= b.period_start AND c.created_at < b.period_end
AND (b.scope <> 'user' OR c.user_id = b.user_id)
AND (b.scope <> 'token' OR c.token_id = b.token_id)`
			if hasOperations {
				legacy += " AND (c.ledger_operation_id = '' OR c.ledger_operation_id IS NULL)"
				parts = append(parts, `SELECT b.id, o.amount FROM budgets b
JOIN redis_ledger_operations o ON b.project_id <= 0 AND o.kind IN ('settle', 'refund')
AND o.created_at >= b.period_start AND o.created_at < b.period_end
AND (b.scope <> 'user' OR o.actor_id = b.user_id)
AND (b.scope <> 'token' OR o.token_id = b.token_id)`)
			}
			parts = append(parts, legacy)
		}
		query := "WITH budgets (id, project_id, scope, user_id, token_id, period_start, period_end) AS (VALUES " + strings.Join(values, ",") + ") " +
			"SELECT id AS budget_id, SUM(amount) AS amount_microusd FROM (" + strings.Join(parts, " UNION ALL ") + ") spends GROUP BY id"
		var rows []struct {
			BudgetID       uint
			AmountMicrousd billing.MicroUSD
		}
		if err := db.Raw(query, args...).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			result[row.BudgetID] = row.AmountMicrousd
		}
	}
	return result, nil
}
