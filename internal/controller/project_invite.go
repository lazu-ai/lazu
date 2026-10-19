package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

type projectInviteView struct {
	ID             string               `json:"id"`
	Email          string               `json:"email"`
	Role           string               `json:"role"`
	BudgetMicrousd *billing.MicroUSD    `json:"budget_microusd"`
	Status         string               `json:"status"`
	ExpiresAt      int64                `json:"expires_at"`
	InvitedBy      model.ProjectUserRef `json:"invited_by"`
	ResentAt       *int64               `json:"resent_at"`
}

func readInviteView(invite model.ProjectInvite, now time.Time) (projectInviteView, error) {
	var user model.User
	if err := model.CoreDB().Unscoped().Select("public_id", "username").First(&user, invite.InvitedBy).Error; err != nil {
		return projectInviteView{}, err
	}
	return projectInviteView{ID: invite.PublicID, Email: invite.Email, Role: invite.Role, BudgetMicrousd: invite.BudgetMicrousd, Status: invite.EffectiveStatus(now), ExpiresAt: invite.EffectiveExpiresAt(), InvitedBy: model.ProjectUserRef{ID: user.PublicID, Username: user.Username}, ResentAt: invite.ResentAt}, nil
}

var wakeProjectNotifications = func() {
	enqueueAuthEmailJob("project-email-delivery", func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = service.DeliverProjectNotifications(ctx, time.Now().UTC())
	})
}

// projectInviteFailure answers an invitation lookup or change. A missing row
// is the invitation itself; every other failure already carries its code.
func projectInviteFailure(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = types.Err(types.ErrorCodeInviteNotFound)
	}
	projectFailure(c, err)
}
func ListProjectInvites(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok || !pc.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	page, size, ok := projectPagination(c)
	if !ok {
		return
	}
	query := model.CoreDB().Model(&model.ProjectInvite{}).Where("project_id = ?", pc.Project.ID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		projectFailure(c, err)
		return
	}
	var rows []model.ProjectInvite
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		projectFailure(c, err)
		return
	}
	items := make([]projectInviteView, 0, len(rows))
	for _, row := range rows {
		view, err := readInviteView(row, time.Now().UTC())
		if err != nil {
			projectFailure(c, err)
			return
		}
		items = append(items, view)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items, "total": total, "page": page, "page_size": size}})
}
func CreateProjectInvite(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok || !pc.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	var input struct {
		Email       string          `json:"email"`
		Role        string          `json:"role"`
		Budget      json.RawMessage `json:"budget_microusd"`
		ProjectName *string         `json:"project_name"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	var budget *billing.MicroUSD
	if len(input.Budget) > 0 {
		if err := json.Unmarshal(input.Budget, &budget); err != nil {
			httperr.Code(c, types.ErrorCodeInvalidBudget)
			return
		}
	}
	validation := model.DefaultProjectSettings()
	validation.DefaultMemberBudgetMicrousd = budget
	if err := validation.Validate(); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidBudget)
		return
	}
	row, err := model.CreateProjectInvite(pc.Project.ID, pc.Member.UserID, model.CreateProjectInviteInput{Email: input.Email, Role: input.Role, BudgetProvided: len(input.Budget) > 0, BudgetMicrousd: budget, ProjectName: input.ProjectName, BaseURL: authEmailBaseURL()}, time.Now().UTC())
	if err != nil {
		projectInviteFailure(c, err)
		return
	}
	view, err := readInviteView(row, time.Now().UTC())
	if err != nil {
		projectFailure(c, err)
		return
	}
	wakeProjectNotifications()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}
func ResendProjectInvite(c *gin.Context) { changeProjectInvite(c, true) }
func RevokeProjectInvite(c *gin.Context) { changeProjectInvite(c, false) }
func changeProjectInvite(c *gin.Context, resend bool) {
	pc, ok := middleware.CurrentProject(c)
	if !ok || !pc.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	row, err := model.ChangeProjectInvite(pc.Project.ID, pc.Member.UserID, c.Param("iid"), resend, authEmailBaseURL(), time.Now().UTC())
	if err != nil {
		projectInviteFailure(c, err)
		return
	}
	view, err := readInviteView(row, time.Now().UTC())
	if err != nil {
		projectFailure(c, err)
		return
	}
	if resend {
		wakeProjectNotifications()
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}
func PreviewProjectInvite(c *gin.Context) {
	row, err := model.LookupProjectInvite(c.Param("token"))
	if err != nil {
		projectInviteFailure(c, err)
		return
	}
	var project model.Project
	if err := model.CoreDB().First(&project, row.ProjectID).Error; err != nil {
		projectInviteFailure(c, err)
		return
	}
	view, err := readInviteView(row, time.Now().UTC())
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"project_name": project.Name, "inviter": view.InvitedBy, "email": row.Email, "role": row.Role, "budget_microusd": row.BudgetMicrousd, "expires_at": view.ExpiresAt, "status": view.Status}})
}
func AcceptProjectInvite(c *gin.Context) {
	member, err := model.AcceptProjectInvite(c.Param("token"), c.GetInt("id"), time.Now().UTC(), authEmailBaseURL())
	if err != nil {
		projectInviteFailure(c, err)
		return
	}
	var project model.Project
	if err := model.CoreDB().First(&project, member.ProjectID).Error; err != nil {
		projectFailure(c, err)
		return
	}
	wakeProjectNotifications()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"project_id": project.PublicID, "role": member.Role, "status": member.Status, "version": member.Version}})
}
