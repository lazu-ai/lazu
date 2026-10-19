package controller

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

type projectAttentionToken struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type projectAttentionRow struct {
	Actor  *model.ProjectUserRef  `json:"actor"`
	Token  *projectAttentionToken `json:"token"`
	Reason string                 `json:"reason"`
	Count  int64                  `json:"count"`
	LastAt int64                  `json:"last_at"`
}
type projectNearBudgetRow struct {
	Actor          *model.ProjectUserRef `json:"actor"`
	SpentMicrousd  int64                 `json:"spent_microusd"`
	BudgetMicrousd int64                 `json:"budget_microusd"`
	WarningPct     int                   `json:"warning_pct"`
	Exhausted      bool                  `json:"exhausted"`
}

type projectRejectionGroup struct {
	UserID  int
	TokenID int
	Reason  string
	Count   int64
	LastAt  int64
}

func GetProjectAttention(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok || !pc.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	page, size, ok := projectPagination(c)
	if !ok {
		return
	}
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var groups []projectRejectionGroup
	var rejected, total int64
	if model.TelemetryDB() == nil {
		projectFailure(c, errors.New("project attention: log database is not initialized"))
		return
	}
	err := model.TelemetryDB().Transaction(func(tx *gorm.DB) error {
		base := func() *gorm.DB {
			return model.ProjectLogScope(tx.Model(&model.Log{}), pc.Project).Where("type = ? AND created_at >= ? AND created_at <= ?", model.LogTypeError, start.Unix(), now.Unix())
		}
		if err := base().Count(&rejected).Error; err != nil {
			return err
		}
		groupQuery := func() *gorm.DB {
			return base().Select("user_id, token_id, COALESCE(NULLIF(rejection_reason, ''), 'unknown') AS reason, COUNT(*) AS count, MAX(created_at) AS last_at").
				Group("user_id, token_id, COALESCE(NULLIF(rejection_reason, ''), 'unknown')")
		}
		if err := tx.Table("(?) AS project_rejections", groupQuery()).Count(&total).Error; err != nil {
			return err
		}
		return groupQuery().Order("last_at DESC, user_id ASC, token_id ASC, reason ASC").Offset((page - 1) * size).Limit(size).Scan(&groups).Error
	})
	if err != nil {
		projectFailure(c, err)
		return
	}
	rows := make([]projectAttentionRow, 0, len(groups))
	for _, group := range groups {
		row := projectAttentionRow{Reason: group.Reason, Count: group.Count, LastAt: group.LastAt}
		if group.UserID > 0 {
			var user model.User
			if err := model.CoreDB().Unscoped().Select("public_id", "username").First(&user, group.UserID).Error; err != nil {
				projectFailure(c, err)
				return
			}
			row.Actor = &model.ProjectUserRef{ID: user.PublicID, Username: user.Username}
		}
		if group.TokenID > 0 {
			var token model.Token
			if err := model.CoreDB().Unscoped().Select("public_id", "name").First(&token, group.TokenID).Error; err != nil {
				projectFailure(c, err)
				return
			}
			row.Token = &projectAttentionToken{ID: token.PublicID, Name: token.Name}
		}
		rows = append(rows, row)
	}
	// Members past their warning line, except those already listed above as
	// refused for their budget today: one line per person.
	var refused []int
	if err := model.ProjectLogScope(model.TelemetryDB().Model(&model.Log{}), pc.Project).
		Where("type = ? AND created_at >= ? AND created_at <= ? AND rejection_reason = ?", model.LogTypeError, start.Unix(), now.Unix(), "member_budget_exceeded").
		Distinct("user_id").Pluck("user_id", &refused).Error; err != nil {
		projectFailure(c, err)
		return
	}
	skip := make(map[int]bool, len(refused))
	for _, id := range refused {
		skip[id] = true
	}
	near, err := model.ReadProjectNearBudget(pc.Project, now)
	if err != nil {
		projectFailure(c, err)
		return
	}
	nearRows := make([]projectNearBudgetRow, 0, len(near))
	for _, row := range near {
		if skip[row.UserID] {
			continue
		}
		var user model.User
		if err := model.CoreDB().Unscoped().Select("public_id", "username").First(&user, row.UserID).Error; err != nil {
			projectFailure(c, err)
			return
		}
		nearRows = append(nearRows, projectNearBudgetRow{Actor: &model.ProjectUserRef{ID: user.PublicID, Username: user.Username},
			SpentMicrousd: row.SpentMicrousd.Int64(), BudgetMicrousd: row.BudgetMicrousd.Int64(), WarningPct: row.WarningPct,
			Exhausted: row.SpentMicrousd >= row.BudgetMicrousd})
	}
	var invites, requests int64
	if err := model.ProjectInvitePendingScope(model.CoreDB().Model(&model.ProjectInvite{}).Where("project_id = ?", pc.Project.ID), now).Count(&invites).Error; err != nil {
		projectFailure(c, err)
		return
	}
	if err := model.CoreDB().Model(&model.BudgetRequest{}).Where("project_id = ? AND status = ?", pc.Project.ID, "pending").Count(&requests).Error; err != nil {
		projectFailure(c, err)
		return
	}
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	spend, err := model.ReadProjectSpend(pc.Project, monthStart.Unix(), now.Unix()+1)
	if err != nil {
		projectFailure(c, err)
		return
	}
	summary, err := model.ReadProjectSummary(pc.Project, pc.Member)
	if err != nil {
		projectFailure(c, err)
		return
	}
	var daysLeft *float64
	if spend > 0 && summary.BalanceMicrousd != nil {
		value := float64(*summary.BalanceMicrousd) * float64(now.Day()) / float64(spend)
		daysLeft = &value
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"as_of": now.Unix(), "window_start": start.Unix(), "window_end": start.AddDate(0, 0, 1).Unix(),
		"rejected_count_today": rejected, "pending_invites_count": invites, "pending_budget_requests_count": requests, "balance_days_left": daysLeft,
		"rejections":  gin.H{"items": rows, "total": total, "page": page, "page_size": size},
		"near_budget": gin.H{"items": nearRows, "total": len(nearRows)},
	}})
}
