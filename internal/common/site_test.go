package common

import (
	"os"
	"path/filepath"
	"testing"
)

func withSiteConfig(t *testing.T, site SiteConfig, adminEmail string) {
	t.Helper()
	original := AppConfig
	originalOIDC := OIDCFrontendBaseURL
	t.Cleanup(func() { AppConfig = original; OIDCFrontendBaseURL = originalOIDC })
	AppConfig = defaultConfig()
	AppConfig.Site = site
	AppConfig.EmailNotifications.AdminEmail = adminEmail
	OIDCFrontendBaseURL = ""
}

// An unconfigured deployment names itself, never another deployment.
func TestSiteDefaultsPointAtThisDeployment(t *testing.T) {
	t.Setenv("FRONTEND_BASE_URL", "")
	withSiteConfig(t, SiteConfig{}, "")
	if got := SiteWebOrigin(); got != DefaultLocalWebOrigin {
		t.Fatalf("web origin = %q", got)
	}
	if SiteAPIOrigin() != "" || SiteOperator() != "" || SiteSupportEmail() != "" || SiteSourceURL() != "" {
		t.Fatalf("unconfigured site leaked values: %q %q %q", SiteAPIOrigin(), SiteOperator(), SiteSupportEmail())
	}
	if SiteHost() != "localhost" {
		t.Fatalf("host = %q", SiteHost())
	}
	if got := MailSiteURL("/console"); got != DefaultLocalWebOrigin+"/console" {
		t.Fatalf("mail link = %q", got)
	}
}

func TestSiteWebOriginPrecedence(t *testing.T) {
	withSiteConfig(t, SiteConfig{WebOrigin: "https://console.example.com"}, "")
	t.Setenv("FRONTEND_BASE_URL", "")
	if got := SiteWebOrigin(); got != "https://console.example.com" {
		t.Fatalf("config origin = %q", got)
	}
	OIDCFrontendBaseURL = "https://oidc.example.com/"
	if got := SiteWebOrigin(); got != "https://oidc.example.com" {
		t.Fatalf("OIDC origin = %q", got)
	}
	t.Setenv("FRONTEND_BASE_URL", "https://front.example.com")
	if got := SiteWebOrigin(); got != "https://front.example.com" {
		t.Fatalf("env origin = %q", got)
	}
	t.Setenv("FRONTEND_BASE_URL", "not-a-url")
	if got := SiteWebOrigin(); got != "https://oidc.example.com" {
		t.Fatalf("a malformed env value must be skipped, got %q", got)
	}
}

func TestSiteSupportEmailFallsBackToOperatorAlerts(t *testing.T) {
	withSiteConfig(t, SiteConfig{}, "ops@example.com")
	if got := SiteSupportEmail(); got != "ops@example.com" {
		t.Fatalf("support email = %q", got)
	}
	AppConfig.Site.SupportEmail = "help@example.com"
	if got := SiteSupportEmail(); got != "help@example.com" {
		t.Fatalf("support email = %q", got)
	}
}

// Hooks run after every load, so packages common cannot import still see the
// loaded values.
func TestSiteConfigLoadsAndRunsHooks(t *testing.T) {
	t.Setenv("FRONTEND_BASE_URL", "")
	withSiteConfig(t, SiteConfig{}, "")
	originalHooks := appConfigLoadedHooks
	t.Cleanup(func() { appConfigLoadedHooks = originalHooks })
	ran := 0
	appConfigLoadedHooks = nil
	OnAppConfigLoaded(func() { ran++ })

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
site:
  web_origin: "https://console.example.com/"
  api_origin: "https://api.example.com"
  operator: "Example Inc."
  support_email: "help@example.com"
  source_url: "https://github.com/example/fork/"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_CONFIG_FILE", path)
	LoadAppConfig()
	if ran != 1 {
		t.Fatalf("hooks ran %d times", ran)
	}
	if SiteWebOrigin() != "https://console.example.com" || SiteAPIOrigin() != "https://api.example.com" ||
		SiteOperator() != "Example Inc." || SiteSupportEmail() != "help@example.com" ||
		SiteSourceURL() != "https://github.com/example/fork" {
		t.Fatalf("site = %+v", AppConfig.Site)
	}
}
