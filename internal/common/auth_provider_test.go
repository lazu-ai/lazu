package common

import "testing"

func TestAuthProviderEnabledUsesRuntimeCapabilityGates(t *testing.T) {
	previousGoogleID, previousGoogleSecret := GoogleOAuthClientID, GoogleOAuthClientSecret
	previousPassword := PasswordLoginEnabled
	GoogleOAuthClientID, GoogleOAuthClientSecret = "client", "secret"
	PasswordLoginEnabled = false
	t.Cleanup(func() {
		GoogleOAuthClientID, GoogleOAuthClientSecret = previousGoogleID, previousGoogleSecret
		PasswordLoginEnabled = previousPassword
	})

	if !AuthProviderEnabled("google") {
		t.Fatal("configured Google provider should be enabled")
	}
	if AuthProviderEnabled("password") {
		t.Fatal("disabled password login should not be enabled")
	}
	GoogleOAuthClientSecret = ""
	if AuthProviderEnabled("google") {
		t.Fatal("incomplete Google provider should not be enabled")
	}
}
