package common

import (
	"net/url"
	"strings"
)

// SameOrigin reports whether two URLs share scheme, host and effective port
// (an absent port is the scheme's default). A nil URL matches nothing.
func SameOrigin(left *url.URL, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectivePort(left) == effectivePort(right)
}

// URLOrigin renders scheme://host[:port] of a URL, without user info, path or
// query, so it can be logged even when the full URL carries a signed token.
func URLOrigin(value *url.URL) string {
	if value == nil {
		return ""
	}
	return strings.ToLower(value.Scheme) + "://" + value.Host
}

func effectivePort(value *url.URL) string {
	if value.Port() != "" {
		return value.Port()
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return "80"
}
