package common

import "strings"

// AuthProviderEnabled is the shared capability gate for login surfaces,
// account projections, and admin cohort filters. Historical identity rows do
// not imply that the corresponding sign-in method is currently usable.
func AuthProviderEnabled(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "google":
		return strings.TrimSpace(GoogleOAuthClientID) != "" && strings.TrimSpace(GoogleOAuthClientSecret) != ""
	case "github":
		return strings.TrimSpace(GitHubOAuthClientID) != "" && strings.TrimSpace(GitHubOAuthClientSecret) != ""
	case "oidc":
		return OIDCEnabled &&
			strings.TrimSpace(OIDCIssuerURL) != "" &&
			strings.TrimSpace(OIDCClientID) != "" &&
			strings.TrimSpace(OIDCClientSecret) != ""
	case "email":
		return EmailCodeAuthEnabled && EmailTransportConfigured()
	case "password":
		return PasswordLoginEnabled
	default:
		return false
	}
}
