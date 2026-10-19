package model

import (
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
)

// A Key's periodic spend cap, as the console asks for it and reads it back.
//
// There is deliberately no new column behind this. A per-token, per-period cap
// with block enforcement is exactly what SpendBudget already is, and it is
// already checked on the request path by CheckSpendBudgetBeforeCharge. A second
// copy of the limit on the Token row would give PreConsumeQuota two independent
// reasons to reject the same request, with two different error codes and two
// ways to disagree about what the Key has already spent.
//
// So this is a projection: one SpendBudget row, scoped to the token, in the
// vocabulary the Key editor uses.
type TokenPeriodCap struct {
	// "day", "month", or "" when the Key has no cap.
	Period string `json:"period"`
	// The ceiling, in USD, for one period.
	LimitUSD float64 `json:"limit_usd"`
	// What this Key has spent in the period that is running now. Read-only: the
	// editor shows it so the number above has a scale.
	SpentUSD float64 `json:"spent_usd"`
}

func microUSDToUSD(value billing.MicroUSD) float64 {
	return float64(value.Int64()) / float64(billing.MicroUSDPerUSD)
}

// ReadTokenPeriodCap returns the Key's cap, or nil when it has none.
func ReadTokenPeriodCap(projectID int, tokenID int) *TokenPeriodCap {
	if CoreDB() == nil || tokenID <= 0 {
		return nil
	}
	var budget SpendBudget
	err := CoreDB().Where("scope = ? AND token_id = ? AND project_id = ?",
		SpendBudgetScopeToken, tokenID, projectID).
		Order("id ASC").First(&budget).Error
	if err != nil || !budget.Enabled || budget.LimitMicrousd <= 0 {
		return nil
	}
	cap := &TokenPeriodCap{
		Period:   normalizeSpendBudgetPeriod(budget.Period),
		LimitUSD: microUSDToUSD(budget.LimitMicrousd),
	}
	start, end := spendBudgetPeriodWindow(budget.Period, time.Now())
	if spent, err := sumSpendMicrousdForBudgetWithDB(CoreDB(), budget, start, end); err == nil {
		cap.SpentUSD = microUSDToUSD(spent)
	}
	return cap
}

// SaveTokenPeriodCap writes the Key's cap. A nil cap, an empty period or a
// non-positive limit all mean "no cap", and remove the row rather than leaving
// a disabled one behind for the next reader to interpret.
func SaveTokenPeriodCap(projectID int, tokenID int, userID int, cap *TokenPeriodCap) error {
	if CoreDB() == nil || tokenID <= 0 {
		return nil
	}
	remove := cap == nil || cap.LimitUSD <= 0
	if !remove {
		switch normalizeSpendBudgetPeriod(cap.Period) {
		case SpendBudgetPeriodDay, SpendBudgetPeriodMonth:
		default:
			remove = true
		}
	}
	if remove {
		return CoreDB().Where("scope = ? AND token_id = ? AND project_id = ?",
			SpendBudgetScopeToken, tokenID, projectID).Delete(&SpendBudget{}).Error
	}

	var existing SpendBudget
	err := CoreDB().Where("scope = ? AND token_id = ? AND project_id = ?",
		SpendBudgetScopeToken, tokenID, projectID).
		Order("id ASC").First(&existing).Error
	limit := billing.USD(cap.LimitUSD)
	period := normalizeSpendBudgetPeriod(cap.Period)
	if err != nil {
		return CoreDB().Create(&SpendBudget{
			ProjectID:     projectID,
			Scope:         SpendBudgetScopeToken,
			UserID:        userID,
			TokenID:       tokenID,
			Period:        period,
			LimitMicrousd: limit,
			// The Key editor offers a ceiling, not a warning: a cap that only
			// emails while the spend keeps going is not what "上限" says.
			Enforcement: SpendBudgetEnforcementBlock,
			Enabled:     true,
		}).Error
	}
	return CoreDB().Model(&SpendBudget{}).Where("id = ?", existing.ID).Updates(map[string]any{
		"period":         period,
		"limit_microusd": limit,
		"enforcement":    SpendBudgetEnforcementBlock,
		"enabled":        true,
	}).Error
}
