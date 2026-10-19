package middleware

import (
	"errors"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/types"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

type ProjectContext struct {
	Project model.Project
	Member  model.ProjectMember
}

func CurrentProject(c *gin.Context) (ProjectContext, bool) {
	value, ok := c.Get("console_project_context")
	if !ok {
		return ProjectContext{}, false
	}
	result, ok := value.(ProjectContext)
	return result, ok
}

// ProjectConsoleContext runs after UserAuth. A platform role is never used as
// a project grant, and removed memberships are not visible through these APIs.
func ProjectConsoleContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !model.ProjectsEnabledForUser(c.GetInt("id")) {
			httperr.Abort(c, types.Err(types.ErrorCodeFeatureUnavailable))
			return
		}
		projectID := c.Param("id")
		for _, queryID := range c.QueryArray("project_id") {
			if queryID != projectID {
				httperr.Abort(c, types.Err(types.ErrorCodeProjectContextMismatch))
				return
			}
		}
		project, member, err := model.LoadVisibleProject(projectID, c.GetInt("id"))
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				httperr.Abort(c, types.Err(types.ErrorCodeProjectNotFound))
				return
			}
			httperr.Abort(c, types.WrapErr(err, types.ErrorCodeInternalError))
			return
		}
		if member.Status != model.ProjectMemberActive && c.Request.Method != http.MethodGet {
			httperr.Abort(c, types.Err(types.ErrorCodeProjectAccessSuspended))
			return
		}
		c.Set("console_project_context", ProjectContext{Project: project, Member: member})
		c.Next()
	}
}

// ResolveConsoleProject is shared by legacy JSON and multipart endpoints.
// Neither URL parameters nor a platform role supply a project grant.
func ResolveConsoleProject(c *gin.Context, bodyProjectID string) (ProjectContext, error) {
	id := bodyProjectID
	for _, candidate := range c.QueryArray("project_id") {
		if id != "" && id != candidate {
			return ProjectContext{}, types.Err(types.ErrorCodeProjectContextMismatch)
		}
		id = candidate
	}
	if id == "" {
		var project model.Project
		if err := model.CoreDB().Where("owner_user_id = ?", c.GetInt("id")).First(&project).Error; err != nil {
			return ProjectContext{}, err
		}
		id = project.PublicID
	}
	project, member, err := model.LoadVisibleProject(id, c.GetInt("id"))
	if err != nil {
		return ProjectContext{}, err
	}
	if (project.OwnerUserID != c.GetInt("id") || project.UpgradedAt != nil) && !model.ProjectsEnabledForUser(c.GetInt("id")) {
		return ProjectContext{}, model.ErrProjectFeatureUnavailable
	}
	result := ProjectContext{Project: project, Member: member}
	c.Set("console_project_context", result)
	return result, nil
}
