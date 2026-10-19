package common

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
