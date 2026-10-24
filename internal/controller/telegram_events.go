package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
)

// New accounts are reported to the Telegram events chat (when one is set)
// from the signup hook, off the request path.
func init() {
	RegisterSignupHook(func(_ *gin.Context, user *model.User) {
		if user == nil {
			return
		}
		u := *user
		common.SafeGo("telegram-signup-notice", func() { service.ReportSignup(&u) })
	})
}
