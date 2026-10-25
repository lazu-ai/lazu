package common

import (
	"fmt"
	"os"
	"strconv"
)

func GetEnvOrDefault(env string, defaultValue int) int {
	if env == "" || os.Getenv(env) == "" {
		return defaultValue
	}
	num, err := strconv.Atoi(os.Getenv(env))
	if err != nil {
		SysError(fmt.Sprintf("failed to parse %s: %s, using default value: %d", env, err.Error(), defaultValue))
		return defaultValue
	}
	return num
}

func GetEnvOrDefaultString(env string, defaultValue string) string {
	if env == "" || os.Getenv(env) == "" {
		return defaultValue
	}
	return os.Getenv(env)
}

func GetEnvOrDefaultBool(env string, defaultValue bool) bool {
	if env == "" || os.Getenv(env) == "" {
		return defaultValue
	}
	b, err := strconv.ParseBool(os.Getenv(env))
	if err != nil {
		SysError(fmt.Sprintf("failed to parse %s: %s, using default value: %t", env, err.Error(), defaultValue))
		return defaultValue
	}
	return b
}

// RequestContentRetentionDays covers every copy of request content, including
// object-storage captures and the first message/preview in Agent Sessions.
// Keep the existing environment name so deployed retention choices still apply.
func RequestContentRetentionDays() int {
	return GetEnvOrDefault("R2_ARCHIVE_RETENTION_DAYS", 180)
}

// NormalizeRelayTimeoutSeconds keeps outbound relay/webhook HTTP clients from
// silently falling back to net/http's unbounded zero timeout.
func NormalizeRelayTimeoutSeconds(seconds int) int {
	if seconds <= 0 {
		return DefaultRelayTimeoutSeconds
	}
	return seconds
}

func RelayTimeoutFromEnv() int {
	return NormalizeRelayTimeoutSeconds(GetEnvOrDefault("RELAY_TIMEOUT", DefaultRelayTimeoutSeconds))
}
