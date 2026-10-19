package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProjectSpend is independent of budget configuration. Setting, removing or
// re-enabling a limit never resets consumption. A request's final settlement
// replaces its reservation; retries retain the original accounting month.
type ProjectSpend struct {
	Aggregated     bool             `gorm:"not null;default:false"`
	ID             uint             `gorm:"primaryKey"`
	EventKey       string           `gorm:"size:64;not null;uniqueIndex"`
	ProjectID      int              `gorm:"not null;index:idx_project_spend_window,priority:1"`
	ActorUserID    int              `gorm:"not null;index"`
	PayerUserID    int              `gorm:"not null"`
	TokenID        int              `gorm:"not null;index"`
	RequestID      string           `gorm:"size:64;index"`
	AmountMicrousd billing.MicroUSD `gorm:"type:bigint;not null"`
	OccurredAt     time.Time        `gorm:"not null;index:idx_project_spend_window,priority:2"`
}

func projectSpendEventKey(input SpendBudgetChargeInput) string {
	start, _ := spendBudgetPeriodWindow(SpendBudgetPeriodMonth, input.OccurredAt)
	request := input.RequestID
	if request == "" {
		request = common.GenerateRequestID()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", input.ProjectID, start, request)))
	return hex.EncodeToString(sum[:])
}

func recordProjectSpendTx(tx *gorm.DB, input SpendBudgetChargeInput) error {
	if input.ProjectID <= 0 || input.DeltaMicrousd <= 0 {
		return nil
	}
	input = normalizeSpendBudgetChargeInput(input)
	row := ProjectSpend{EventKey: projectSpendEventKey(input), ProjectID: input.ProjectID, ActorUserID: input.ActorUserID, PayerUserID: input.PayerUserID, TokenID: input.TokenID, RequestID: input.RequestID, AmountMicrousd: input.DeltaMicrousd, OccurredAt: input.OccurredAt}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(&row).Error
}

// ReadProjectTokenMonthSpend sums the current UTC month's finalized spend for
// each of the given Keys in one query, so listing N Keys stays a single round
// trip. It reads the same ledger as the member and project figures.
func ReadProjectTokenMonthSpend(projectID int, tokenIDs []int, now time.Time) (map[int]billing.MicroUSD, error) {
	result := make(map[int]billing.MicroUSD, len(tokenIDs))
	if CoreDB() == nil || projectID <= 0 || len(tokenIDs) == 0 {
		return result, nil
	}
	wanted := make([]int, 0, len(tokenIDs))
	for _, id := range tokenIDs {
		if id <= 0 {
			continue
		}
		if _, seen := result[id]; seen {
			continue
		}
		result[id] = 0
		wanted = append(wanted, id)
	}
	if len(wanted) == 0 {
		return result, nil
	}
	start, end := spendBudgetPeriodWindow(SpendBudgetPeriodMonth, now)
	var rows []struct {
		TokenID        int              `gorm:"column:token_id"`
		AmountMicrousd billing.MicroUSD `gorm:"column:amount_microusd"`
	}
	if err := CoreDB().Model(&ProjectSpend{}).
		Select("token_id, COALESCE(SUM(amount_microusd),0) AS amount_microusd").
		Where("project_id = ? AND token_id IN ? AND occurred_at >= ? AND occurred_at < ?", projectID, wanted, time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()).
		Group("token_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.TokenID] = row.AmountMicrousd
	}
	return result, nil
}

func projectSpendQuery(tx *gorm.DB, budget SpendBudget, start, end int64) *gorm.DB {
	query := tx.Model(&ProjectSpend{}).Where("project_id = ? AND occurred_at >= ? AND occurred_at < ?", budget.ProjectID, time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC())
	if budget.Scope == SpendBudgetScopeUser {
		query = query.Where("actor_user_id = ?", budget.UserID)
	}
	if budget.Scope == SpendBudgetScopeToken {
		query = query.Where("token_id = ?", budget.TokenID)
	}
	return query
}

func sumProjectSpendTx(tx *gorm.DB, budget SpendBudget, start, end int64) (billing.MicroUSD, error) {
	var amount billing.MicroUSD
	if err := projectSpendQuery(tx, budget, start, end).Select("COALESCE(SUM(amount_microusd),0)").Scan(&amount).Error; err != nil {
		return 0, err
	}
	// Previously enforced budgets retain their exact ledger, including charges
	// whose logs were purged. Canonical records replace matching requests only.
	legacy := tx.Model(&SpendBudgetCharge{}).Where("ledger_operation_id = ? OR ledger_operation_id IS NULL", "").Where("budget_id = ? AND created_at >= ? AND created_at < ?", budget.ID, time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()).
		Where("NOT EXISTS (SELECT 1 FROM project_spends ps WHERE ps.project_id = ? AND ps.request_id = spend_budget_charges.request_id AND ps.occurred_at >= ? AND ps.occurred_at < ?)", budget.ProjectID, time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC())
	var prior billing.MicroUSD
	if err := legacy.Select("COALESCE(SUM(amount_microusd),0)").Scan(&prior).Error; err != nil {
		return 0, err
	}
	return amount + prior, nil
}

func projectSpendDailyKey(projectID, actorID, tokenID int, at time.Time) string {
	day := at.UTC().Format("2006-01-02")
	sum := sha256.Sum256([]byte(fmt.Sprintf("daily:%d:%d:%d:%s", projectID, actorID, tokenID, day)))
	return hex.EncodeToString(sum[:])
}
