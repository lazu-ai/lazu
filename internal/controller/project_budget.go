package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

type projectBudgetHistoryView struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"created_at"`
	// Null when nobody changed it — a system default or a migrated row. An empty
	// UserRef would render as a nameless person in the console.
	ChangedBy         *model.ProjectUserRef `json:"changed_by"`
	OldBudgetMicrousd *billing.MicroUSD     `json:"old_budget_microusd"`
	NewBudgetMicrousd *billing.MicroUSD     `json:"new_budget_microusd"`
	Reason            string                `json:"reason"`
}

func readBudgetHistory(projectID, userID, page, size int) ([]projectBudgetHistoryView, int64, error) {
	query := model.CoreDB().Model(&model.ProjectBudgetHistory{}).Where("project_id = ? AND user_id = ?", projectID, userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.ProjectBudgetHistory
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	result := make([]projectBudgetHistoryView, 0, len(rows))
	for _, row := range rows {
		var changedBy *model.ProjectUserRef
		if row.ChangedBy > 0 {
			var actor model.User
			if err := model.CoreDB().Unscoped().Select("public_id", "username").First(&actor, row.ChangedBy).Error; err != nil {
				return nil, 0, err
			}
			changedBy = &model.ProjectUserRef{ID: actor.PublicID, Username: actor.Username}
		}
		result = append(result, projectBudgetHistoryView{ID: row.PublicID, CreatedAt: row.CreatedAt, ChangedBy: changedBy, OldBudgetMicrousd: row.OldBudgetMicrousd, NewBudgetMicrousd: row.NewBudgetMicrousd, Reason: row.Reason})
	}
	return result, total, nil
}
func GetProjectBudgetHistory(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	target, ok := projectTarget(c, pc)
	if !ok {
		return
	}
	if pc.Member.UserID != target.UserID && !pc.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	page, size, ok := projectPagination(c)
	if !ok {
		return
	}
	rows, total, err := readBudgetHistory(pc.Project.ID, target.UserID, page, size)
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": rows, "total": total, "page": page, "page_size": size}})
}
func GetProjectMyBudget(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	now := time.Now().UTC()
	member, err := model.ReadProjectMemberView(pc.Project, pc.Member, now)
	if err != nil {
		projectFailure(c, err)
		return
	}
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	spend, err := model.ReadProjectSpend(pc.Project, start.Unix(), now.Unix()+1)
	if err != nil {
		projectFailure(c, err)
		return
	}
	settings, err := pc.Project.GetSettings()
	if err != nil {
		projectFailure(c, err)
		return
	}
	summary, err := model.ReadProjectSummary(pc.Project, pc.Member)
	if err != nil {
		projectFailure(c, err)
		return
	}
	history, _, err := readBudgetHistory(pc.Project.ID, pc.Member.UserID, 1, 20)
	if err != nil {
		projectFailure(c, err)
		return
	}
	var remaining *billing.MicroUSD
	if member.BudgetMicrousd != nil {
		value := max(billing.MicroUSD(0), *member.BudgetMicrousd-member.MonthSpendMicrousd)
		remaining = &value
	}
	var daysLeft *float64
	if spend > 0 && summary.BalanceMicrousd != nil {
		days := float64(*summary.BalanceMicrousd) * float64(now.Day()) / float64(spend)
		daysLeft = &days
	}
	var projectCap interface{}
	if pc.Member.Status == model.ProjectMemberActive {
		projectCap = gin.H{"cap_microusd": settings.ProjectCapMicrousd, "spent_microusd": spend}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"budget_microusd": member.BudgetMicrousd, "spent_microusd": member.MonthSpendMicrousd, "remaining_microusd": remaining,
		"daily_avg_microusd": member.MonthSpendMicrousd / billing.MicroUSD(now.Day()), "resets_at": start.AddDate(0, 1, 0).Unix(),
		"warning_pct": member.WarningPct, "version": member.Version, "project_cap": projectCap,
		"balance_days_left": daysLeft, "history": history,
		"reasoning_effort_cap": member.ReasoningEffortCap, "allowed_models": member.AllowedModels,
	}})
}

type projectBudgetRequestView struct {
	ApprovedBudgetMicrousd *billing.MicroUSD     `json:"approved_budget_microusd"`
	ID                     string                `json:"id"`
	User                   model.ProjectUserRef  `json:"user"`
	RequestedMicrousd      billing.MicroUSD      `json:"requested_microusd"`
	Reason                 string                `json:"reason"`
	Status                 string                `json:"status"`
	CreatedAt              int64                 `json:"created_at"`
	HandledBy              *model.ProjectUserRef `json:"handled_by"`
	HandledNote            string                `json:"handled_note"`
	HandledAt              *int64                `json:"handled_at"`
}

func readBudgetRequestView(row model.BudgetRequest) (projectBudgetRequestView, error) {
	result := projectBudgetRequestView{ApprovedBudgetMicrousd: row.ApprovedBudgetMicrousd, ID: row.PublicID, RequestedMicrousd: row.RequestedMicrousd, Reason: row.Reason, Status: row.Status, CreatedAt: row.CreatedAt, HandledNote: row.HandledNote, HandledAt: row.HandledAt}
	var user model.User
	if err := model.CoreDB().Unscoped().Select("public_id", "username").First(&user, row.UserID).Error; err != nil {
		return result, err
	}
	result.User = model.ProjectUserRef{ID: user.PublicID, Username: user.Username}
	if row.HandledBy > 0 {
		var handler model.User
		if err := model.CoreDB().Unscoped().Select("public_id", "username").First(&handler, row.HandledBy).Error; err != nil {
			return result, err
		}
		result.HandledBy = &model.ProjectUserRef{ID: handler.PublicID, Username: handler.Username}
	}
	return result, nil
}
func ListProjectBudgetRequests(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	page, size, ok := projectPagination(c)
	if !ok {
		return
	}
	query := model.CoreDB().Model(&model.BudgetRequest{}).Where("project_id = ?", pc.Project.ID)
	if !pc.Member.IsManager() {
		query = query.Where("user_id = ?", pc.Member.UserID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		projectFailure(c, err)
		return
	}
	var rows []model.BudgetRequest
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		projectFailure(c, err)
		return
	}
	items := make([]projectBudgetRequestView, 0, len(rows))
	for _, row := range rows {
		view, err := readBudgetRequestView(row)
		if err != nil {
			projectFailure(c, err)
			return
		}
		items = append(items, view)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items, "total": total, "page": page, "page_size": size}})
}
func CreateProjectBudgetRequest(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	var input struct {
		RequestedMicrousd *billing.MicroUSD `json:"requested_microusd"`
		Reason            string            `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.RequestedMicrousd == nil {
		projectFailure(c, model.ErrProjectInvalidInput)
		return
	}
	row, err := model.CreateProjectBudgetRequest(pc.Project.ID, pc.Member.UserID, *input.RequestedMicrousd, input.Reason)
	if err != nil {
		projectFailure(c, err)
		return
	}
	view, err := readBudgetRequestView(row)
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// budgetRequestError names a missing request row after the request.
func budgetRequestError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return types.Err(types.ErrorCodeBudgetRequestNotFound)
	}
	return err
}

func ApproveProjectBudgetRequest(c *gin.Context)  { handleProjectBudgetRequest(c, "approved") }
func RejectProjectBudgetRequest(c *gin.Context)   { handleProjectBudgetRequest(c, "rejected") }
func WithdrawProjectBudgetRequest(c *gin.Context) { handleProjectBudgetRequest(c, "withdrawn") }
func handleProjectBudgetRequest(c *gin.Context, action string) {
	pc, ok := middleware.CurrentProject(c)
	if !ok {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	var input struct {
		BudgetMicrousd json.RawMessage `json:"budget_microusd"`
		Note           string          `json:"note"`
	}
	if action != "withdrawn" {
		if err := c.ShouldBindJSON(&input); err != nil {
			projectFailure(c, model.ErrProjectInvalidInput)
			return
		}
	}
	var budget *billing.MicroUSD
	if action == "approved" {
		if len(input.BudgetMicrousd) == 0 {
			var request model.BudgetRequest
			if err := model.CoreDB().Where("project_id = ? AND public_id = ?", pc.Project.ID, c.Param("rid")).First(&request).Error; err != nil {
				projectFailure(c, budgetRequestError(err))
				return
			}
			budget = &request.RequestedMicrousd
		} else if err := json.Unmarshal(input.BudgetMicrousd, &budget); err != nil {
			projectFailure(c, model.ErrProjectInvalidInput)
			return
		}
	}
	row, err := model.HandleProjectBudgetRequest(pc.Project.ID, pc.Member.UserID, c.Param("rid"), action, budget, input.Note, time.Now().UTC())
	if err != nil {
		projectFailure(c, budgetRequestError(err))
		return
	}
	view, err := readBudgetRequestView(row)
	if err != nil {
		projectFailure(c, err)
		return
	}
	wakeProjectNotifications()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}
