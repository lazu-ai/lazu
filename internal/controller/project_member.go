package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

// projectMemberKeysPageSize is how many of a member's Keys one detail read
// returns; keys_page walks the rest and key_count gives the total.
const projectMemberKeysPageSize = 50

func projectPagination(c *gin.Context) (int, int, bool) {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, sizeErr := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil || sizeErr != nil || page < 1 || page > 1000000 || size < 1 || size > 100 {
		httperr.Code(c, types.ErrorCodeInvalidPagination)
		return 0, 0, false
	}
	return page, size, true
}

func projectTarget(c *gin.Context, context middleware.ProjectContext) (model.ProjectMember, bool) {
	var user model.User
	if err := model.CoreDB().Select("id").Where("public_id = ?", c.Param("uid")).First(&user).Error; err != nil {
		projectFailure(c, projectMemberError(err))
		return model.ProjectMember{}, false
	}
	var member model.ProjectMember
	if err := model.CoreDB().Where("project_id = ? AND user_id = ?", context.Project.ID, user.Id).First(&member).Error; err != nil {
		projectFailure(c, projectMemberError(err))
		return member, false
	}
	return member, true
}

// projectMemberError names a missing user or membership row after the member.
func projectMemberError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return types.Err(types.ErrorCodeProjectMemberNotFound)
	}
	return err
}

func ListProjectMembers(c *gin.Context) {
	context, ok := middleware.CurrentProject(c)
	if !ok || !context.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	page, size, ok := projectPagination(c)
	if !ok {
		return
	}
	var total int64
	query := model.CoreDB().Model(&model.ProjectMember{}).Where("project_id = ? AND status <> ?", context.Project.ID, model.ProjectMemberRemoved)
	if err := query.Count(&total).Error; err != nil {
		projectFailure(c, err)
		return
	}
	var members []model.ProjectMember
	if err := query.Order("joined_at ASC, id ASC").Offset((page - 1) * size).Limit(size).Find(&members).Error; err != nil {
		projectFailure(c, err)
		return
	}
	items, err := model.ReadProjectMemberViews(context.Project, members, time.Now().UTC())
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items, "total": total, "page": page, "page_size": size}})
}

func UpdateProjectMemberRole(c *gin.Context)   { updateProjectMemberState(c, true) }
func UpdateProjectMemberStatus(c *gin.Context) { updateProjectMemberState(c, false) }
func updateProjectMemberState(c *gin.Context, isRole bool) {
	context, ok := middleware.CurrentProject(c)
	if !ok || !context.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	target, ok := projectTarget(c, context)
	if !ok {
		return
	}
	var input struct {
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || isRole && input.Role != "admin" && input.Role != "member" || !isRole && input.Status != "active" && input.Status != "suspended" {
		if isRole {
			httperr.Invalid(c, "role")
		} else {
			httperr.Invalid(c, "status")
		}
		return
	}
	if target.Role == model.ProjectRoleOwner {
		httperr.Code(c, types.ErrorCodeCannotChangeOwner)
		return
	}
	if isRole {
		input.Status = ""
	} else {
		input.Role = ""
	}
	result, err := model.UpdateProjectMemberRoleOrStatus(context.Project.ID, context.Member.UserID, target.UserID, input.Role, input.Status)
	if err != nil {
		projectFailure(c, err)
		return
	}
	view, err := model.ReadProjectMemberView(context.Project, result, time.Now().UTC())
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

func UpdateProjectMemberBudget(c *gin.Context) {
	context, ok := middleware.CurrentProject(c)
	if !ok || !context.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	target, ok := projectTarget(c, context)
	if !ok {
		return
	}
	if target.UserID == context.Member.UserID {
		httperr.Code(c, types.ErrorCodeCannotEditOwnBudget)
		return
	}
	var input struct {
		BudgetMicrousd     json.RawMessage `json:"budget_microusd"`
		WarningPct         int             `json:"warning_pct"`
		ExpectedVersion    int64           `json:"expected_version"`
		AllowedModels      *[]string       `json:"allowed_models"`
		ReasoningEffortCap *string         `json:"reasoning_effort_cap"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.ExpectedVersion <= 0 || len(input.BudgetMicrousd) == 0 {
		httperr.Invalid(c, "budget_microusd")
		return
	}
	var budget *billing.MicroUSD
	if err := json.Unmarshal(input.BudgetMicrousd, &budget); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidBudget)
		return
	}
	validation := model.DefaultProjectSettings()
	validation.DefaultMemberBudgetMicrousd, validation.WarningPct = budget, input.WarningPct
	if err := validation.Validate(); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidBudget)
		return
	}
	if input.ReasoningEffortCap != nil && !model.ValidEffortCap(model.NormalizeEffortCap(*input.ReasoningEffortCap)) {
		httperr.Code(c, types.ErrorCodeInvalidEffortCap)
		return
	}
	policy := model.ProjectMemberPolicy{AllowedModels: input.AllowedModels, EffortCap: input.ReasoningEffortCap}
	result, err := model.UpdateProjectMemberBudget(context.Project.ID, context.Member.UserID, target.UserID, input.ExpectedVersion, budget, input.WarningPct, "manual", policy)
	if err != nil {
		projectFailure(c, err)
		return
	}
	details, err := readProjectDetails(context.Project, context.Member)
	if err != nil {
		projectFailure(c, err)
		return
	}
	view, err := model.ReadProjectMemberView(context.Project, result, time.Now().UTC())
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"member": view, "budget_microusd": budget, "warning_pct": input.WarningPct, "version": result.Version, "allocation": details.Allocation}})
}

func RemoveProjectMember(c *gin.Context) {
	context, ok := middleware.CurrentProject(c)
	if !ok {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	target, ok := projectTarget(c, context)
	if !ok {
		return
	}
	receipt, err := model.RemoveProjectMember(context.Project.ID, context.Member.UserID, target.UserID, time.Now().UTC())
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"removal_id": receipt.PublicID, "undo_until": receipt.UndoUntil}})
}

func UndoProjectMemberRemoval(c *gin.Context) {
	context, ok := middleware.CurrentProject(c)
	if !ok {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	target, ok := projectTarget(c, context)
	if !ok {
		return
	}
	var input struct {
		RemovalID string `json:"removal_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.RemovalID == "" {
		httperr.Code(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "removal_id"})
		return
	}
	result, err := model.UndoProjectRemoval(context.Project.ID, context.Member.UserID, target.UserID, input.RemovalID, time.Now().UTC())
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func DissolveProject(c *gin.Context) {
	context, ok := middleware.CurrentProject(c)
	if !ok || context.Member.Role != model.ProjectRoleOwner {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	var input struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.ConfirmName != context.Project.Name {
		httperr.Code(c, types.ErrorCodeConfirmMismatch)
		return
	}
	if err := model.DissolveProject(context.Project.ID, context.Member.UserID, input.ConfirmName); err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"dissolved": true}})
}

func LeaveProject(c *gin.Context) {
	context, ok := middleware.CurrentProject(c)
	if !ok || context.Member.Role == model.ProjectRoleOwner {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	if _, err := model.RemoveProjectMember(context.Project.ID, context.Member.UserID, context.Member.UserID, time.Now().UTC()); err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"left": true}})
}

func GetProjectMember(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok || !pc.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	target, ok := projectTarget(c, pc)
	if !ok {
		return
	}
	if target.Status == model.ProjectMemberRemoved {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	now := time.Now().UTC()
	member, err := model.ReadProjectMemberView(pc.Project, target, now)
	if err != nil {
		projectFailure(c, err)
		return
	}
	keysPage, err := strconv.Atoi(c.DefaultQuery("keys_page", "1"))
	if err != nil || keysPage < 1 || keysPage > 1000000 {
		httperr.Code(c, types.ErrorCodeInvalidPagination)
		return
	}
	var keys []*model.Token
	// The same owner fallback key_count and the Key limit use: a retired service
	// row with user_id 0 belongs to the project owner, so the owner's detail
	// must list it rather than silently drop it. key_count is the total; keys
	// is one page of it.
	if err := model.ProjectMemberTokenScope(model.ProjectVisibleTokensQuery(model.CoreDB(), pc.Project, pc.Member, true), target.UserID).
		Order("id ASC").Offset((keysPage - 1) * projectMemberKeysPageSize).Limit(projectMemberKeysPageSize).Find(&keys).Error; err != nil {
		projectFailure(c, err)
		return
	}
	payloads, err := projectTokenPayloads(c, pc, keys)
	if err != nil {
		projectFailure(c, err)
		return
	}
	history, _, err := readBudgetHistory(pc.Project.ID, target.UserID, 1, 20)
	if err != nil {
		projectFailure(c, err)
		return
	}
	type dayUsage struct {
		Date     string           `json:"date"`
		Spent    billing.MicroUSD `json:"spent_microusd"`
		Requests int64            `json:"request_count"`
		Rejected int64            `json:"rejected_count"`
	}
	days := make([]dayUsage, 0, 7)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -6)
	for i := 0; i < 7; i++ {
		day := start.AddDate(0, 0, i)
		row := dayUsage{Date: day.Format("2006-01-02")}
		if model.TelemetryDB() != nil {
			if err := model.ProjectLogScope(model.TelemetryDB().Model(&model.Log{}), pc.Project).
				Where("user_id = ? AND created_at >= ? AND created_at < ?", target.UserID, day.Unix(), day.AddDate(0, 0, 1).Unix()).
				Select("COALESCE(SUM(CASE WHEN type = ? THEN amount_microusd ELSE 0 END), 0) AS spent, COALESCE(SUM(CASE WHEN type = ? THEN 1 ELSE 0 END), 0) AS requests, COALESCE(SUM(CASE WHEN type = ? THEN 1 ELSE 0 END), 0) AS rejected", model.LogTypeConsume, model.LogTypeConsume, model.LogTypeError).Scan(&row).Error; err != nil {
				projectFailure(c, err)
				return
			}
		}
		days = append(days, row)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": struct {
		model.ProjectMemberView
		Keys         []gin.H                    `json:"keys"`
		KeysPage     int                        `json:"keys_page"`
		KeysPageSize int                        `json:"keys_page_size"`
		History      []projectBudgetHistoryView `json:"budget_history"`
		Usage        []dayUsage                 `json:"usage_7d"`
	}{member, payloads, keysPage, projectMemberKeysPageSize, history, days}})
}
