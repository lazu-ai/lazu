package common

import "testing"

func TestConfigureLogLevelFromEnvDefaultsToInfo(t *testing.T) {
	original := LogLevel
	defer func() { LogLevel = original }()

	t.Setenv("LOG_LEVEL", "")
	t.Setenv("DEBUG", "")
	ConfigureLogLevelFromEnv()

	if LogLevel != LogLevelInfo {
		t.Fatalf("expected default log level %q, got %q", LogLevelInfo, LogLevel)
	}
}

func TestConfigureLogLevelFromEnvUsesDebugFallback(t *testing.T) {
	original := LogLevel
	defer func() { LogLevel = original }()

	t.Setenv("LOG_LEVEL", "")
	t.Setenv("DEBUG", "true")
	ConfigureLogLevelFromEnv()

	if LogLevel != LogLevelDebug {
		t.Fatalf("expected debug log level when DEBUG=true, got %q", LogLevel)
	}
}

func TestConfigureLogLevelFromEnvSupportsSilent(t *testing.T) {
	original := LogLevel
	defer func() { LogLevel = original }()

	t.Setenv("LOG_LEVEL", "off")
	ConfigureLogLevelFromEnv()

	if ShouldLog(LogLevelError) {
		t.Fatal("expected silent log level to suppress error logs")
	}
}
