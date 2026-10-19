package controller

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

// freshAccountWindow is how recently an account must have been created for a
// login to count as its registration.
const freshAccountWindow = 10 * time.Minute

// SignupHook runs once for a newly registered account, on the request that
// finished the registration.
type SignupHook func(c *gin.Context, user *model.User)

var signupHooks []SignupHook

// RegisterSignupHook adds work an extension does when an account registers.
func RegisterSignupHook(hook SignupHook) {
	signupHooks = append(signupHooks, hook)
}

// recordSignupAttributionIfNew runs on every session setup and acts only for
// an account created moments ago. Existing accounts cost a single time
// comparison.
func recordSignupAttributionIfNew(c *gin.Context, user *model.User) {
	if c == nil || user == nil || user.Id <= 0 || user.CreatedAtSource != model.UserCreatedAtSourceExact {
		return
	}
	if user.CreatedAt <= 0 || time.Since(time.Unix(user.CreatedAt, 0)) > freshAccountWindow {
		return
	}
	recordHomeSurfaceSignup(c, user)
	for _, hook := range signupHooks {
		hook(c, user)
	}
}

// surfaceCookie is written by a product's own sign-in and sign-up pages so an
// account that registers there is recorded under that product, whichever
// sign-in method finishes the registration.
const surfaceCookie = "lz_surface"

// requestHomeSurface is the surface the browser is signing up through.
func requestHomeSurface(c *gin.Context) string {
	if c == nil {
		return ""
	}
	raw, err := c.Cookie(surfaceCookie)
	if err != nil {
		return ""
	}
	return model.NormalizeUserHomeSurface(raw)
}

func recordHomeSurfaceSignup(c *gin.Context, user *model.User) {
	surface := requestHomeSurface(c)
	if surface == "" || user.HomeSurface == surface {
		return
	}
	if err := model.SetUserHomeSurface(user.Id, surface); err != nil {
		common.SysLog("failed to record signup surface: " + err.Error())
		return
	}
	user.HomeSurface = surface
}
