package video

import "strings"

// FailureClass says whose fault a terminal generation failure is, in the
// terms channel health and the route breaker use.
type FailureClass int

const (
	// FailureUpstream is the provider failing to make the video. It counts
	// against the route. Unknown codes land here: a code we have never seen
	// is more often an outage than a refusal, and its raw value stays on the
	// job and the routing outcome so it can be added below.
	FailureUpstream FailureClass = iota
	// FailureCaller is the request refused for its content or parameters, or
	// withdrawn. It says nothing about the route.
	FailureCaller
	// FailureRateLimited is the provider's quota or rate limit. It counts
	// against the route, as a 429 does on every other path.
	FailureRateLimited
)

// Provider codes are matched case-insensitively as substrings, because the
// families spell the same refusal differently and nest it under prefixes
// (Ark: "InputTextSensitiveContentDetected.PolicyViolation").
//
// Sources:
//   - Ark / BytePlus ModelArk error codes:
//     https://docs.byteplus.com/en/docs/ModelArk/1299023
//     (Input*/Output*SensitiveContentDetected, InvalidParameter,
//     RateLimitExceeded, QuotaExceeded)
//   - OpenAI videos: moderation_blocked, invalid_request_error
//   - Gemini (Veo) long-running operations: google.rpc.Code names
//     INVALID_ARGUMENT, RESOURCE_EXHAUSTED, and RAI "safety" filtering
var (
	callerFailureMarkers = []string{
		"sensitive",
		"moderation",
		"content_policy",
		"content_filter",
		"safety",
		"invalid_prompt",
		"invalidparameter",
		"invalid_parameter",
		"invalidargument",
		"invalid_argument",
		"cancel",
	}
	rateLimitFailureMarkers = []string{
		"ratelimit",
		"rate_limit",
		"quotaexceeded",
		"quota_exceeded",
		"resource_exhausted",
		"toomanyrequests",
		"too_many_requests",
	}
)

// ClassifyFailure reads a terminal failure code — the provider's own, or one
// the worker assigned — into a FailureClass.
func ClassifyFailure(code string) FailureClass {
	normalized := strings.ToLower(strings.TrimSpace(code))
	if normalized == "" {
		return FailureUpstream
	}
	for _, marker := range callerFailureMarkers {
		if strings.Contains(normalized, marker) {
			return FailureCaller
		}
	}
	for _, marker := range rateLimitFailureMarkers {
		if strings.Contains(normalized, marker) {
			return FailureRateLimited
		}
	}
	return FailureUpstream
}
