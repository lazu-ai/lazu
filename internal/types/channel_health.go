package types

import "net/http"

// ChannelHealthVerdict converts a completed relay result into the two facts
// persisted for channel-health aggregation. Keeping this decision in Go means
// the SQL readers do not have to duplicate the error catalog's taxonomy.
//
// Eligible is false for gateway-local failures, caller cancellations, content
// policy refusals, context-window errors and ordinary 4xx responses. Failed is
// true only when eligible provider evidence represents a channel fault.
func ChannelHealthVerdict(statusCode int, errorCode ErrorCode, providerAttempted bool) (eligible bool, failed bool) {
	if !providerAttempted {
		return false, false
	}
	if errorCode != "" {
		// Protocol conversion happens inside the gateway after the provider has
		// responded. It is useful request evidence, but says nothing about the
		// channel's reliability and must never feed the breaker window.
		if errorCode == ErrorCodeConvertRequestFailed {
			return false, false
		}
		switch GetFallbackCategory(errorCode) {
		case FallbackContentPolicy, FallbackContextWindow:
			return false, false
		}
		if errorCode == ErrorCodeClientCanceled || errorCode == ErrorCodeDownstreamWriteFailed {
			return false, false
		}
		// Invalid credentials are channel configuration evidence even though
		// providers commonly report them with an ordinary 401 or 403. Treating
		// all non-429 4xx responses as caller faults would hide a broken key.
		if errorCode == ErrorCodeChannelInvalidKey {
			return true, true
		}
		// An exhausted provider account is the route failing until someone
		// tops it up, whatever status the provider chose to report it with.
		if errorCode == ErrorCodeUpstreamBillingFailed {
			return true, true
		}
		// A stream may already have committed HTTP 200 before the provider
		// disconnects or emits an error frame. The recorded error is the only
		// durable failure signal in that case.
		if statusCode >= 200 && statusCode < 300 {
			return true, true
		}
	}
	switch {
	case statusCode == http.StatusSwitchingProtocols:
		return true, false
	case statusCode >= 200 && statusCode < 300:
		return true, false
	case statusCode == 429:
		return true, true
	// The gateway answers its own customers' insufficient balance before any
	// provider is attempted, so a 402 that reaches here came from upstream: our
	// account there has run dry. That is the route failing, and it does not
	// recover until someone tops it up.
	case statusCode == http.StatusPaymentRequired:
		return true, true
	case statusCode >= 500 || statusCode <= 0:
		return true, true
	default:
		return false, false
	}
}
