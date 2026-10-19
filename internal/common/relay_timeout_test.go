package common

import "testing"

func TestRelayTimeoutFromEnvUsesSafeDefaultWhenUnset(t *testing.T) {
	t.Setenv("RELAY_TIMEOUT", "")

	if got := RelayTimeoutFromEnv(); got != DefaultRelayTimeoutSeconds {
		t.Fatalf("expected default relay timeout %d, got %d", DefaultRelayTimeoutSeconds, got)
	}
}

func TestRelayTimeoutFromEnvAllowsPositiveOverride(t *testing.T) {
	t.Setenv("RELAY_TIMEOUT", "123")

	if got := RelayTimeoutFromEnv(); got != 123 {
		t.Fatalf("expected configured relay timeout 123, got %d", got)
	}
}

func TestRelayTimeoutFromEnvRejectsZeroOverride(t *testing.T) {
	t.Setenv("RELAY_TIMEOUT", "0")

	if got := RelayTimeoutFromEnv(); got != DefaultRelayTimeoutSeconds {
		t.Fatalf("expected zero relay timeout to fall back to %d, got %d", DefaultRelayTimeoutSeconds, got)
	}
}

func TestNormalizeRelayTimeoutSecondsRejectsNegativeValues(t *testing.T) {
	if got := NormalizeRelayTimeoutSeconds(-1); got != DefaultRelayTimeoutSeconds {
		t.Fatalf("expected negative relay timeout to fall back to %d, got %d", DefaultRelayTimeoutSeconds, got)
	}
}
