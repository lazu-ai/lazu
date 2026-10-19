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
		apiRouter.GET("/dashboard/billing/subscription", func(c *gin.Context) {
			markDeprecatedAlias(c, "/v1/dashboard/billing/subscription")
			controller.GetSubscription(c)
		})
		apiRouter.GET("/v1/dashboard/billing/subscription", controller.GetSubscription)
		apiRouter.GET("/dashboard/billing/usage", func(c *gin.Context) {
			markDeprecatedAlias(c, "/v1/dashboard/billing/usage")
			controller.GetUsage(c)
		})
		apiRouter.GET("/v1/dashboard/billing/usage", controller.GetUsage)
	}
}
