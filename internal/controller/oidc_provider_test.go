package controller

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestOIDCProvider(t *testing.T) {
	cases := []struct{ issuer, want string }{
		{"", ""},
		// GitHub OAuth is not a generic OIDC issuer; it has a dedicated flow.
		{"https://github.com/login/oauth", ""},
		{"https://accounts.google.com", "google"},
		{"https://gitlab.com", "gitlab"},
		{"https://login.microsoftonline.com/common/v2.0", "microsoft"},
		{"https://auth.acme.internal/realms/main", ""},
		// Lookalike hosts must NOT get a trusted provider's mark.
		{"https://github.evil.example", ""},
		{"https://google.attacker.test/o/oauth2", ""},
		{"https://gitlab.example.com", ""},
		{"https://login.live.example.org", ""},
		// Real subdomains still resolve.
		{"https://token.actions.githubusercontent.com", ""},
		{"https://accounts.google.com:443", "google"},
	}
	original := common.OIDCIssuerURL
	defer func() { common.OIDCIssuerURL = original }()
	for _, tc := range cases {
		common.OIDCIssuerURL = tc.issuer
		if got := oidcProvider(); got != tc.want {
			t.Fatalf("issuer %q: got %q, want %q", tc.issuer, got, tc.want)
		}
	}
}
