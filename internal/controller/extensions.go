package controller

import "github.com/gin-gonic/gin"

// Extensions compiled into a build register here from init(); without them
// the controller behaves as the core.

// StatusFields adds fields to the public /api/status payload.
type StatusFields func(c *gin.Context, data gin.H)

// PublicPageCachePolicy chooses Cache-Control headers for a server-rendered
// public page. ok=false leaves the page to the next policy or the default.
type PublicPageCachePolicy func(path string) (cacheControl string, cdnCacheControl string, ok bool)

var (
	statusFields            []StatusFields
	publicPageCachePolicies []PublicPageCachePolicy
)

func RegisterStatusFields(fields StatusFields) {
	statusFields = append(statusFields, fields)
}

func RegisterPublicPageCachePolicy(policy PublicPageCachePolicy) {
	publicPageCachePolicies = append(publicPageCachePolicies, policy)
}

func publicPageCacheHeaders(path string) (string, string) {
	for _, policy := range publicPageCachePolicies {
		if cacheControl, cdn, ok := policy(path); ok {
			return cacheControl, cdn
		}
	}
	return "public, max-age=60, s-maxage=60", ""
}

// WelcomeMail renders the welcome message for an account that signed up on a
// surface other than the console.
type WelcomeMail func(lang string, displayName string) (subject string, body string)

var surfaceWelcomeMails = map[string]WelcomeMail{}

// RegisterSurfaceWelcomeMail replaces the welcome mail for accounts that
// registered through surface; the mail preview lists it as welcome_<surface>.
func RegisterSurfaceWelcomeMail(surface string, mail WelcomeMail) {
	surfaceWelcomeMails[surface] = mail
}
