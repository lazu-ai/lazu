package model

import (
	"fmt"

	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

// ProjectMigrationAudit is emitted with startup migration evidence. Counts
// include soft-deleted rows because refunds and historical attribution survive
// account/credential deletion. It contains no customer names or credentials.
type ProjectMigrationAudit struct {
	Rows                   map[string]int64 `json:"rows"`
	Missing                map[string]int64 `json:"missing_attribution"`
	WalletMicrousd         billing.MicroUSD `json:"wallet_microusd"`
	BudgetRows             int64            `json:"budget_rows"`
	BudgetChargesMicrousd  billing.MicroUSD `json:"budget_charges_microusd"`
	BudgetReservedMicrousd billing.MicroUSD `json:"budget_reserved_microusd"`
	UnsettledMedia         int64            `json:"unsettled_media"`
}

func readProjectMigrationAudit(db *gorm.DB) (ProjectMigrationAudit, error) {
	result := ProjectMigrationAudit{Rows: map[string]int64{}, Missing: map[string]int64{}}
	for _, table := range []string{"tokens", "top_ups", "spend_budgets", "spend_budget_alerts", "spend_budget_reservations", "spend_budget_charges", "media_jobs", "files", "settlement_outboxes", "settlement_failures"} {
		if !db.Migrator().HasTable(table) {
			continue
		}
		var rows, missing int64
		if err := db.Table(table).Count(&rows).Error; err != nil {
			return result, err
		}
		query := db.Table(table)
		if table == "spend_budgets" {
			query = query.Where("scope <> ?", SpendBudgetScopeOrg)
		}
		if err := query.Where("project_id IS NULL OR project_id = 0 OR NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = " + table + ".project_id)").Count(&missing).Error; err != nil {
			return result, err
		}
		result.Rows[table], result.Missing[table] = rows, missing
	}
	queries := []struct {
		table, column, where string
		dest                 interface{}
	}{
		{"users", "COALESCE(SUM(quota),0)", "1=1", &result.WalletMicrousd},
		{"spend_budgets", "COUNT(*)", "1=1", &result.BudgetRows},
		{"spend_budget_charges", "COALESCE(SUM(amount_microusd),0)", "1=1", &result.BudgetChargesMicrousd},
		{"spend_budget_reservations", "COALESCE(SUM(reserved_microusd),0)", "1=1", &result.BudgetReservedMicrousd},
		{"media_jobs", "COUNT(*)", "billing_settled_at = 0", &result.UnsettledMedia},
	}
	for _, q := range queries {
		if db.Migrator().HasTable(q.table) {
			if err := db.Table(q.table).Where(q.where).Select(q.column).Scan(q.dest).Error; err != nil {
				return result, err
			}
		}
	}
	var duplicates int64
	if err := db.Raw("SELECT COUNT(*) FROM (SELECT stripe_customer_id FROM projects WHERE stripe_customer_id <> '' GROUP BY stripe_customer_id HAVING COUNT(*) > 1) reused_customers").Scan(&duplicates).Error; err != nil {
		return result, err
	}
	result.Missing["duplicate_stripe_customers"] = duplicates
	return result, nil
}
func (report ProjectMigrationAudit) Validate() error {
	for table, count := range report.Missing {
		if count > 0 {
			return fmt.Errorf("project migration incomplete: %s has %d rows without valid project attribution", table, count)
		}
	}
	return nil
}
