package controller

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// Legacy console routes use explicit project selectors. Omission means only
// the signed-in user's default project. The resolver's own failures (selector
// mismatch, feature off) are catalog errors; a missing row is the project.
func legacyProjectContext(c *gin.Context, bodyProjectID string) (middleware.ProjectContext, bool) {
	pc, err := middleware.ResolveConsoleProject(c, bodyProjectID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = types.Err(types.ErrorCodeProjectNotFound)
		}
		projectFailure(c, err)
		return pc, false
	}
	return pc, true
}
