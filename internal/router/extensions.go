package router

import "github.com/gin-gonic/gin"

// PageFunc registers a public page for GET and HEAD.
type PageFunc func(path string, handler gin.HandlerFunc)

// Extensions compiled into a build add routes here from init().
var (
	apiRoutes        []func(api *gin.RouterGroup)
	rootRoutes       []func(router *gin.Engine)
	localePages      []func(page PageFunc, prefix string, locale string)
	publicSiteRoutes []func(router *gin.Engine, page PageFunc)
)

// RegisterAPIRoutes adds routes under /api, after the core's, with the same
// compression and rate limit.
func RegisterAPIRoutes(register func(api *gin.RouterGroup)) {
	apiRoutes = append(apiRoutes, register)
}

// RegisterRootRoutes adds routes outside /api, before the public site.
func RegisterRootRoutes(register func(router *gin.Engine)) {
	rootRoutes = append(rootRoutes, register)
}

// RegisterLocalePages adds server-rendered pages once per locale; prefix is
// "" for English and "/zh", "/zh-TW" or "/ja" otherwise.
func RegisterLocalePages(register func(page PageFunc, prefix string, locale string)) {
	localePages = append(localePages, register)
}

// RegisterPublicSiteRoutes adds locale-independent public-site routes.
func RegisterPublicSiteRoutes(register func(router *gin.Engine, page PageFunc)) {
	publicSiteRoutes = append(publicSiteRoutes, register)
}

// RegisterBrowserRouteTargets adds API-host paths that redirect to the web
// app (links in mail or payment returns that reach the API host).
func RegisterBrowserRouteTargets(targets map[string]string) {
	for from, to := range targets {
		browserRouteTargets[from] = to
	}
}
