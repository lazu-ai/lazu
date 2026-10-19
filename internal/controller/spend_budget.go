package controller

import (
	"strconv"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

// spendBudgetFailure answers a spend-budget route; a missing row is the budget.
func spendBudgetFailure(c *gin.Context, err error) {
	writeDomainFailure(c, err, types.ErrorCodeSpendBudgetNotFound)
}

type spendBudgetPayload struct {
	ID                  uint    `json:"id"`
	Scope               string  `json:"scope"`
	UserID              int     `json:"user_id"`
	TokenID             int     `json:"token_id"`
	Period              string  `json:"period"`
	LimitMicrousd       int64   `json:"limit_microusd"`
	WarningThresholdPct int     `json:"warning_threshold_pct"`
	BurstMultiplier     float64 `json:"burst_multiplier"`
	Enforcement         string  `json:"enforcement"`
	Enabled             *bool   `json:"enabled"`
}

func ListSpendBudgets(c *gin.Context) {
	if c.Query("p") != "" || c.Query("page_size") != "" {
		pageInfo := common.GetPageQuery(c)
		rows, total, err := model.ListSpendBudgetsPage(pageInfo.GetPageSize(), pageInfo.GetStartIdx())
		if err != nil {
			spendBudgetFailure(c, err)
			return
		}
		pageInfo.SetTotal(int(total))
		pageInfo.SetItems(rows)
		common.ApiSuccess(c, pageInfo)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	rows, err := model.ListSpendBudgets(limit)
	if err != nil {
		spendBudgetFailure(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func SaveSpendBudget(c *gin.Context) {
	var payload spendBudgetPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	if c.Param("id") != "" {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || id == 0 {
			httperr.Invalid(c, "id")
			return
		}
		payload.ID = uint(id)
	}
	row := model.SpendBudget{
		ID:                  payload.ID,
		Scope:               payload.Scope,
		UserID:              payload.UserID,
		TokenID:             payload.TokenID,
		Period:              payload.Period,
		LimitMicrousd:       billing.MicroUSD(payload.LimitMicrousd),
		WarningThresholdPct: payload.WarningThresholdPct,
		BurstMultiplier:     payload.BurstMultiplier,
		Enforcement:         payload.Enforcement,
	}
	if payload.Enabled == nil {
		if payload.ID == 0 {
			row.Enabled = true
		} else {
			existing, err := model.GetSpendBudget(payload.ID)
			if err != nil {
				spendBudgetFailure(c, err)
				return
			}
			row.Enabled = existing.Enabled
		}
	} else {
		row.Enabled = *payload.Enabled
	}
	saved, err := model.SaveSpendBudget(row)
	if err != nil {
		spendBudgetFailure(c, err)
		return
	}
	common.ApiSuccess(c, saved)
}

func DeleteSpendBudget(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	if id == 0 {
		httperr.Invalid(c, "id")
		return
	}
	if err := model.DeleteSpendBudget(uint(id)); err != nil {
		spendBudgetFailure(c, err)
		return
	}
	common.ApiSuccess(c, true)
}

func ListSpendBudgetAlerts(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	rows, err := model.ListSpendBudgetAlerts(limit)
	if err != nil {
		spendBudgetFailure(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}
