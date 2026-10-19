package router

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/controller"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"
)

var browserRouteTargets = map[string]string{
	"/user/reset":       "/user/reset",
	"/console/usage":    "/console/usage",
	"/console/wallet":   "/console/wallet",
	"/payment/success":  "/payment/success",
	"/payment/document": "/payment/document",
}

func browserRedirectTarget(request *http.Request, frontendBaseURL string) (string, bool) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return "", false
	}

	targetPath, ok := browserRouteTargets[request.URL.Path]
	if !ok {
		return "", false
	}

	destination, err := url.Parse(strings.TrimSpace(frontendBaseURL))
	if err != nil || destination.Host == "" || (destination.Scheme != "http" && destination.Scheme != "https") {
		return "", false
	}

	destination.Path = strings.TrimSuffix(destination.Path, "/") + targetPath
	destination.RawPath = ""
	destination.RawQuery = request.URL.RawQuery
	destination.Fragment = ""
	return destination.String(), true
}

func SetRouter(router *gin.Engine) {
	// CORS must be registered before any route so both preflight requests and
	// the actual API responses receive the configured cross-origin headers.
	router.Use(middleware.CORS())
	router.GET("/models.json", controller.GetModelsJSON)
	router.GET("/metrics", controller.GetPrometheusMetrics)
	// One-line installers the API Keys page hands out, e.g.
	// curl -fsSL <origin>/setup/claude-code | sh -s -- --key sk-...
	router.GET("/setup/:tool", controller.GetAgentSetupScript)
	for _, register := range rootRoutes {
		register(router)
	}
	SetPublicSiteRouter(router)
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)

	frontendBaseURL := os.Getenv("FRONTEND_BASE_URL")
	if strings.TrimSpace(frontendBaseURL) == "" {
		frontendBaseURL = setting.StripeReturnBase
	}
	router.NoRoute(func(c *gin.Context) {
		if destination, ok := browserRedirectTarget(c.Request, frontendBaseURL); ok {
			c.Header("Cache-Control", "no-store")
			c.Redirect(http.StatusPermanentRedirect, destination)
			return
		}
		err := types.Err(types.ErrorCodeEndpointNotSupported, types.D{"path": c.Request.Method + " " + c.Request.URL.Path})
		if c.Request.URL.Path == "/api" || strings.HasPrefix(c.Request.URL.Path, "/api/") {
			httperr.Abort(c, err)
		} else {
			httperr.AbortGateway(c, err)
		}
	})
}

// SetPublicSiteRouter registers the server-rendered public pages: the model
// catalog, one page per model (with .md / .json siblings) and the legal pages,
// plus any pages extensions register.
// English is unprefixed; zh, zh-TW and ja mirror it under a locale prefix.
func SetPublicSiteRouter(router *gin.Engine) {
	// Pages answer HEAD as well as GET: link checkers, SEO audits and some
	// crawlers probe with HEAD, and a 404 there reads as a missing page.
	// net/http drops the body of a HEAD response.
	page := func(path string, handler gin.HandlerFunc) {
		router.GET(path, handler)
		router.HEAD(path, handler)
	}
	page("/models.md", controller.PublicModelsMarkdown)
	for _, locale := range []string{"en", "zh", "zh-TW", "ja"} {
		prefix := ""
		if locale != "en" {
			prefix = "/" + locale
		}
		for _, kind := range []string{"privacy", "terms"} {
			page(prefix+"/"+kind, controller.PublicLegal(locale, kind, false))
			page(prefix+"/"+kind+".md", controller.PublicLegal(locale, kind, true))
		}
		page(prefix+"/models", controller.PublicModelsIndex(locale))
		page(prefix+"/models/:vendor", controller.PublicModelMaker(locale))
		page(prefix+"/models/:vendor/:model", controller.PublicModelPage(locale))
		for _, register := range localePages {
			register(page, prefix, locale)
		}
	}
	for _, register := range publicSiteRoutes {
		register(router, page)
	}
}
