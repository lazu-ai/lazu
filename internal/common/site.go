package common

import (
	"net/url"
	"os"
	"strings"
)

var appConfigLoadedHooks []func()

// OnAppConfigLoaded registers fn to run every time LoadAppConfig finishes.
// Packages that common cannot import (the public site) use it to pick up
// their configuration. Register from an init function.
func OnAppConfigLoaded(fn func()) { appConfigLoadedHooks = append(appConfigLoadedHooks, fn) }

func runAppConfigLoadedHooks() {
	for _, fn := range appConfigLoadedHooks {
		fn()
	}
}

// DefaultLocalWebOrigin is where a gateway started from this repository
// serves its console when nothing else is configured.
const DefaultLocalWebOrigin = "http://localhost:3000"

func cleanOrigin(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	return ""
}

// SiteWebOrigin is the absolute origin of this deployment's web console:
// FRONTEND_BASE_URL, then OIDC_FRONTEND_BASE_URL, then site.web_origin, then
// the local default.
func SiteWebOrigin() string {
	for _, raw := range []string{os.Getenv("FRONTEND_BASE_URL"), OIDCFrontendBaseURL, AppConfig.Site.WebOrigin} {
		if origin := cleanOrigin(raw); origin != "" {
			return origin
		}
	}
	return DefaultLocalWebOrigin
}

// SiteAPIOrigin is site.api_origin, or empty when it is not configured.
func SiteAPIOrigin() string { return cleanOrigin(AppConfig.Site.APIOrigin) }

// SiteHost is the host name of SiteWebOrigin.
func SiteHost() string {
	if parsed, err := url.Parse(SiteWebOrigin()); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return "localhost"
}

// SiteSourceURL is where this deployment's source is published, or empty.
func SiteSourceURL() string { return cleanOrigin(AppConfig.Site.SourceURL) }

// SiteOperator is the legal operator named on legal pages, or empty.
func SiteOperator() string { return strings.TrimSpace(AppConfig.Site.Operator) }

// SiteSupportEmail is the contact address mail footers link to: site.support_email,
// then the operator alert address, or empty.
func SiteSupportEmail() string {
	if email := strings.TrimSpace(AppConfig.Site.SupportEmail); email != "" {
		return email
	}
	return strings.TrimSpace(AppConfig.EmailNotifications.AdminEmail)
}
