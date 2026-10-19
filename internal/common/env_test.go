package common

import "testing"

func TestGetEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_INT_ENV", "42")
	if got := GetEnvOrDefault("TEST_INT_ENV", 7); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestGetEnvOrDefault_InvalidIntUsesDefault(t *testing.T) {
	t.Setenv("TEST_INT_ENV_INVALID", "not-an-int")
	if got := GetEnvOrDefault("TEST_INT_ENV_INVALID", 9); got != 9 {
		t.Fatalf("expected default 9, got %d", got)
	}
}

func TestGetEnvOrDefault_EmptyUsesDefault(t *testing.T) {
	t.Setenv("TEST_INT_ENV_EMPTY", "")
	if got := GetEnvOrDefault("TEST_INT_ENV_EMPTY", 11); got != 11 {
		t.Fatalf("expected default 11, got %d", got)
	}
}

func TestGetEnvOrDefaultString(t *testing.T) {
	t.Setenv("TEST_STRING_ENV", "abc")
	if got := GetEnvOrDefaultString("TEST_STRING_ENV", "fallback"); got != "abc" {
		t.Fatalf("expected abc, got %s", got)
	}
}

func TestGetEnvOrDefaultString_UsesDefaultWhenEmpty(t *testing.T) {
	t.Setenv("TEST_STRING_ENV_EMPTY", "")
	if got := GetEnvOrDefaultString("TEST_STRING_ENV_EMPTY", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %s", got)
	}
}

func TestGetEnvOrDefaultBool(t *testing.T) {
	t.Setenv("TEST_BOOL_ENV", "true")
	if got := GetEnvOrDefaultBool("TEST_BOOL_ENV", false); !got {
		t.Fatalf("expected true, got false")
	}
}

func TestGetEnvOrDefaultBool_InvalidUsesDefault(t *testing.T) {
	t.Setenv("TEST_BOOL_ENV_INVALID", "not-a-bool")
	if got := GetEnvOrDefaultBool("TEST_BOOL_ENV_INVALID", true); !got {
		t.Fatalf("expected default true, got false")
	}
}

func TestGetEnvOrDefaultBool_UsesDefaultWhenEmpty(t *testing.T) {
	t.Setenv("TEST_BOOL_ENV_EMPTY", "")
	if got := GetEnvOrDefaultBool("TEST_BOOL_ENV_EMPTY", false); got {
		t.Fatalf("expected default false, got true")
	}
}
