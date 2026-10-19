package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
)

type projectAllocation struct {
	MemberCapsMicrousd billing.MicroUSD `json:"member_caps_microusd"`
	UnlimitedMembers   int64            `json:"unlimited_members"`
}
type projectDetails struct {
	model.ProjectSummary
	Version            int64                  `json:"version"`
	PendingInvites     *int64                 `json:"pending_invites,omitempty"`
	MonthSpendMicrousd *billing.MicroUSD      `json:"month_spend_microusd,omitempty"`
	ProjectCapMicrousd *billing.MicroUSD      `json:"project_cap_microusd"`
	Allocation         *projectAllocation     `json:"allocation,omitempty"`
	Settings           *model.ProjectSettings `json:"settings,omitempty"`
}

// projectError puts the project model's failures on the error catalog. The
// project sentinels already are catalog errors; this adds the Key-state errors
// owned by the token model and the invitation wait, which carries a value.
// Anything else passes through: a catalog error keeps its code, a database
// error is logged and answered as internal_error.
func projectError(err error) error {
	var rate model.ProjectInviteRateError
	switch {
	case errors.As(err, &rate):
		return rate.APIError()
	// The Key itself is disabled or expired: an ordinary credential failure,
	// not a suspended membership.
	case errors.Is(err, model.ErrTokenExpired):
		return types.Err(types.ErrorCodeTokenExpired)
	case errors.Is(err, model.ErrTokenDisabled):
		return types.Err(types.ErrorCodeTokenDisabled)
	case errors.Is(err, model.ErrTokenAccessInactive):
		return types.Err(types.ErrorCodeInvalidAPIKey)
	case errors.Is(err, model.ErrTokenBudgetExhausted):
		return types.Err(types.ErrorCodeKeyBudgetExceeded)
	}
	return err
}

// projectFailure is the single exit of every team-project handler.
func projectFailure(c *gin.Context, err error) {
	httperr.Write(c, projectError(err))
}

func GetProject(c *gin.Context) {
	context, ok := middleware.CurrentProject(c)
	if !ok {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	detail, err := readProjectDetails(context.Project, context.Member)
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": detail})
}

func readProjectDetails(project model.Project, member model.ProjectMember) (projectDetails, error) {
	result := projectDetails{Version: project.Version}
	summary, err := model.ReadProjectSummary(project, member)
	if err != nil {
		return result, err
	}
	result.ProjectSummary = summary
	if member.Status != model.ProjectMemberActive {
		return result, nil
	}
	settings, err := project.GetSettings()
	if err != nil {
		return result, err
	}
	if !member.IsManager() {
		return result, nil
	}
	result.Settings, result.ProjectCapMicrousd = &settings, settings.ProjectCapMicrousd
	var pending int64
	if err := model.ProjectInvitePendingScope(model.CoreDB().Model(&model.ProjectInvite{}).Where("project_id = ?", project.ID), time.Now()).Count(&pending).Error; err != nil {
		return result, err
	}
	result.PendingInvites = &pending
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	spend, err := model.ReadProjectSpend(project, start.Unix(), now.Unix()+1)
	if err != nil {
		return result, err
	}
	result.MonthSpendMicrousd = &spend
	allocation := projectAllocation{}
	currentMembers := model.CoreDB().Model(&model.ProjectMember{}).Select("user_id").Where("project_id = ? AND status <> ?", project.ID, model.ProjectMemberRemoved)
	if err := model.CoreDB().Model(&model.SpendBudget{}).Where("user_id IN (?)", currentMembers).Where("project_id = ? AND scope = ? AND enabled = ? AND period = ?", project.ID, model.SpendBudgetScopeUser, true, model.SpendBudgetPeriodMonth).
		Select("COALESCE(SUM(limit_microusd),0)").Scan(&allocation.MemberCapsMicrousd).Error; err != nil {
		return result, err
	}
	cappedMembers := model.CoreDB().Model(&model.SpendBudget{}).Select("user_id").Where("project_id = ? AND scope = ? AND enabled = ? AND period = ?", project.ID, model.SpendBudgetScopeUser, true, model.SpendBudgetPeriodMonth)
	if err := model.CoreDB().Model(&model.ProjectMember{}).Where("project_id = ? AND role <> ? AND status <> ? AND user_id NOT IN (?)", project.ID, model.ProjectRoleOwner, model.ProjectMemberRemoved, cappedMembers).
		Count(&allocation.UnlimitedMembers).Error; err != nil {
		return result, err
	}
	result.Allocation = &allocation
	return result, nil
}

func UpdateProject(c *gin.Context) {
	context, ok := middleware.CurrentProject(c)
	if !ok || context.Member.Role != model.ProjectRoleOwner {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	var input struct {
		Name            *string         `json:"name"`
		Settings        json.RawMessage `json:"settings"`
		ExpectedVersion int64           `json:"expected_version"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.ExpectedVersion <= 0 {
		httperr.Invalid(c, "expected_version")
		return
	}
	var settings *model.ProjectSettings
	if len(input.Settings) > 0 && string(input.Settings) != "null" {
		current, err := context.Project.GetSettings()
		if err != nil {
			projectFailure(c, err)
			return
		}
		if err := json.Unmarshal(input.Settings, &current); err != nil {
			httperr.Invalid(c, "settings")
			return
		}
		current.DefaultReasoningEffortCap = model.NormalizeEffortCap(current.DefaultReasoningEffortCap)
		settings = &current
		if err := settings.Validate(); err != nil {
			projectFailure(c, err)
			return
		}
	}
	project, err := model.UpdateProjectSettings(context.Project.ID, context.Member.UserID, input.ExpectedVersion, input.Name, settings)
	if err != nil {
		projectFailure(c, err)
		return
	}
	detail, err := readProjectDetails(project, context.Member)
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": detail})
}
