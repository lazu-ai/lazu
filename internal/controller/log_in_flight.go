package controller

import (
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
)

// inFlightMediaJobLimit bounds each status's slice of the in-flight list.
const inFlightMediaJobLimit = 50

// GetUserInFlightMediaJobs lists the project's media jobs that are still
// queued or generating. A job writes its usage-log row only when it settles,
// so the console shows these above the log until then. The scope follows the
// log's: a member sees their own jobs, a manager everyone's, and the log's
// member and Key filters apply.
func GetUserInFlightMediaJobs(c *gin.Context) {
	access, ok := projectLogAccess(c)
	if !ok {
		return
	}
	principal := model.ProjectPrincipal{
		ProjectID:      access.Project.ID,
		PayerUserID:    access.Project.OwnerUserID,
		ActorUserID:    access.Viewer.UserID,
		ConsoleReadAll: access.Viewer.IsManager() && access.ActorID == nil,
	}
	if access.ActorID != nil {
		principal.ActorUserID = *access.ActorID
	}
	rows := make([]model.MediaJob, 0)
	for _, status := range []string{model.MediaJobStatusPending, model.MediaJobStatusRunning} {
		part, err := model.ListMediaJobsByUser(c.GetInt("id"), "", status, inFlightMediaJobLimit, principal)
		if err != nil {
			logFailure(c, err)
			return
		}
		rows = append(rows, part...)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	items := make([]dto.MediaJobObject, 0, len(rows))
	for _, row := range rows {
		if access.TokenID > 0 && (row.TokenID == nil || *row.TokenID != access.TokenID) {
			continue
		}
		items = append(items, row.ToDTO(false))
	}
	common.ApiSuccess(c, items)
}
