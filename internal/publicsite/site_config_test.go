package publicsite

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

func withPublicSiteConfig(t *testing.T, site common.SiteConfig) {
	t.Helper()
	original := common.AppConfig.Site
	site0, api0 := SiteOrigin, APIOrigin
	t.Cleanup(func() { common.AppConfig.Site = original; SiteOrigin, APIOrigin = site0, api0 })
	t.Setenv("FRONTEND_BASE_URL", "")
	common.AppConfig.Site = site
	applySiteConfig()
}

// Canonical URLs and structured-data ids follow this deployment's origin.
func TestPublicSiteFollowsConfiguredOrigins(t *testing.T) {
	withPublicSiteConfig(t, common.SiteConfig{WebOrigin: "https://console.example.com", APIOrigin: "https://api.example.com"})
	if SiteOrigin != "https://console.example.com" || APIOrigin != "https://api.example.com" {
		t.Fatalf("origins = %q %q", SiteOrigin, APIOrigin)
	}
	if organizationID() != "https://console.example.com/#organization" || websiteID() != "https://console.example.com/#website" {
		t.Fatalf("ids = %q %q", organizationID(), websiteID())
	}
}

// Without configuration the public site names this deployment's console, not
// lazu.ai, and the gateway samples point at the same origin.
func TestPublicSiteUnconfiguredUsesOwnOrigin(t *testing.T) {
	withPublicSiteConfig(t, common.SiteConfig{})
	if SiteOrigin != common.DefaultLocalWebOrigin || APIOrigin != common.DefaultLocalWebOrigin {
		t.Fatalf("origins = %q %q", SiteOrigin, APIOrigin)
	}
}
