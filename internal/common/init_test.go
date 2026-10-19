package common

import (
	"net/http"
	"testing"
)

func TestIsProductionRuntimeHonorsLazuEnv(t *testing.T) {
	for _, envName := range []string{"APP_ENV", "LAZU_ENV", "ENVIRONMENT", "NODE_ENV", "STRIPE_ENV", "GIN_MODE"} {
		t.Setenv(envName, "")
	}
	t.Setenv("LAZU_ENV", "production")

	if !IsProductionRuntime() {
		t.Fatal("expected LAZU_ENV=production to enable production runtime")
	}
}

func TestResolveSessionCookieSameSite(t *testing.T) {
	t.Setenv("SESSION_COOKIE_SAME_SITE", "")
	if got := ResolveSessionCookieSameSite(); got != http.SameSiteStrictMode {
		t.Fatalf("default SameSite = %v, want strict", got)
	}
	t.Setenv("SESSION_COOKIE_SAME_SITE", "lax")
	if got := ResolveSessionCookieSameSite(); got != http.SameSiteLaxMode {
		t.Fatalf("lax SameSite = %v, want lax", got)
	}
	t.Setenv("SESSION_COOKIE_SAME_SITE", "NONE")
	if got := ResolveSessionCookieSameSite(); got != http.SameSiteNoneMode {
		t.Fatalf("none SameSite = %v, want none", got)
	}
}

func TestProductionEnvironmentFailsClosedForUnrecognizedMarkers(t *testing.T) {
	for _, tc := range []struct {
		name        string
		environment []string
		want        bool
	}{
		{"canonical", []string{"APP_ENV=production"}, true},
		{"retired marker", []string{"RETIRED_APP_ENV= Production "}, true},
		{"mixed markers", []string{"APP_ENV=development", "CUSTOM_ENV=production"}, true},
		{"environment", []string{"ENVIRONMENT=production"}, true},
		{"release", []string{"GIN_MODE=release"}, true},
		{"local default", nil, false},
		{"development", []string{"APP_ENV=development"}, false},
		{"unrelated value", []string{"DATABASE_NAME=production"}, false},
		{"malformed", []string{"APP_ENV"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := productionEnvironment(tc.environment); got != tc.want {
				t.Fatalf("productionEnvironment() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRetiredProductionMarkerKeepsSessionCookiesSecure(t *testing.T) {
	t.Setenv("RETIRED_APP_ENV", "production")
	t.Setenv("SESSION_COOKIE_SECURE", "auto")
	if !ResolveSessionCookieSecure() {
		t.Fatal("an unfamiliar production marker must keep cookies Secure")
	}
}
