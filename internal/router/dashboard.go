package router

import (
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/controller"
	"github.com/lazu-ai/lazu/internal/middleware"
)

func SetDashboardRouter(router *gin.Engine) {
	apiRouter := router.Group("/")
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	apiRouter.Use(middleware.APIRateLimit())
	apiRouter.Use(middleware.TokenAuth())
	apiRouter.Use(middleware.RejectEphemeralCredentialOutsideRelay())
	{
		// The OpenAI-era balance endpoints, kept for third-party balance
		// checkers; the console wallet replaces them.
		billing := retiring(userSunset, "")
		apiRouter.GET("/dashboard/billing/subscription", billing, controller.GetSubscription)
		apiRouter.GET("/v1/dashboard/billing/subscription", billing, controller.GetSubscription)
		apiRouter.GET("/dashboard/billing/usage", billing, controller.GetUsage)
		apiRouter.GET("/v1/dashboard/billing/usage", billing, controller.GetUsage)
	}
}
