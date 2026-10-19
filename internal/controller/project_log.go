package controller

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

func projectLogAccess(c *gin.Context) (model.ProjectLogAccess, bool) {
	pc, ok := legacyProjectContext(c, "")
	if !ok {
		return model.ProjectLogAccess{}, false
	}
	access, err := model.NewProjectLogAccess(pc.Project, pc.Member)
	if err != nil {
		projectFailure(c, err)
		return access, false
	}
	if raw := c.Query("user_id"); raw != "" {
		var target model.User
		if err := model.CoreDB().Select("id").Where("public_id = ?", raw).First(&target).Error; err != nil {
			projectFailure(c, projectMemberError(err))
			return access, false
		}
		var member model.ProjectMember
		if err := model.CoreDB().Where("project_id = ? AND user_id = ?", pc.Project.ID, target.Id).First(&member).Error; err != nil {
			projectFailure(c, projectMemberError(err))
			return access, false
		}
		if target.Id != pc.Member.UserID && !pc.Member.IsManager() {
			projectFailure(c, model.ErrProjectForbidden)
			return access, false
		}
		access.ActorID = &target.Id
	}
	if raw := c.Query("token_id"); raw != "" {
		var token model.Token
		if err := model.CoreDB().Where("public_id = ? AND project_id = ?", raw, pc.Project.ID).First(&token).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				err = types.Err(types.ErrorCodeKeyNotFound)
			}
			projectFailure(c, err)
			return access, false
		}
		if pc.Member.Status == model.ProjectMemberSuspended {
			if token.UserId != pc.Member.UserID {
				projectFailure(c, model.ErrProjectForbidden)
				return access, false
			}
		} else if _, err := model.CheckProjectTokenAction(model.CoreDB(), pc.Project.ID, pc.Member.UserID, token.Id, model.ProjectReadPersonalResource); err != nil {
			projectFailure(c, err)
			return access, false
		}
		access.TokenID = token.Id
	}
	return access, true
}
func projectLogFilter(c *gin.Context) model.ProjectLogFilter {
	kind, _ := strconv.Atoi(c.Query("type"))
	start, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	return model.ProjectLogFilter{Type: kind, Start: start, End: end, Model: c.Query("model_name"), TokenName: c.Query("token_name"), Group: c.Query("group"), Reason: c.Query("reason"), Rejected: c.Query("result") == "rejected", Keyword: c.Query("keyword"), EffortClamped: c.Query("effort_clamped") == "true"}
}
