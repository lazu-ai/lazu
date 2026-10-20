package video

import (
	"fmt"
	"net/http"
	"strings"
)

// A liveness check asks a video route's provider one question that costs
// nothing: "what is the state of this task?" It reuses each family's own
// BuildStatus, so a family needs no extra method to be checkable.
//
// The task asked about is, in order of what its answer proves:
//  1. the route's most recent delivered task. A 200 is unambiguous: the key
//     works and this is the path real jobs are polled on.
//  2. LivenessSentinelID, which no provider has issued. A structured 4xx
//     still proves the request was authenticated and routed to an API that
//     understood it; it cannot prove the path is the one generations use.
//
// A liveness check speaks for reachability only. Whether the route can make
// a video is answered by generations, real or canary.

// LivenessSentinelID is the task id asked about when the route has no
// delivered task to ask about.
const LivenessSentinelID = "lazu-liveness-check"

// LivenessClass is what one liveness answer says about the route.
type LivenessClass string

const (
	// LivenessTask: a real task was read back. The strongest answer.
	LivenessTask LivenessClass = "task"
	// LivenessAnswered: the provider authenticated the call and answered in
	// its own error format about a task it does not know.
	LivenessAnswered LivenessClass = "answered"
	// LivenessAuth: the provider refused the credential.
	LivenessAuth LivenessClass = "auth"
	// LivenessRateLimited: the provider throttled even a status read.
	LivenessRateLimited LivenessClass = "rate_limited"
	// LivenessUpstream: the provider failed (5xx).
	LivenessUpstream LivenessClass = "upstream"
	// LivenessUnmounted: something answered that is not the provider's task
	// API — an HTML 404, a 405, a redirect. On a reseller gateway this is a
	// path the gateway does not serve.
	LivenessUnmounted LivenessClass = "unmounted"
	// LivenessTransport: no HTTP answer at all.
	LivenessTransport LivenessClass = "transport"
)

// Liveness is one check's verdict.
type Liveness struct {
	Class      LivenessClass
	StatusCode int
	Message    string
}

// Reachable reports whether the provider answered as the task API it is
// configured as, with the credential accepted.
func (l Liveness) Reachable() bool {
	return l.Class == LivenessTask || l.Class == LivenessAnswered
}

// ReadLiveness classifies a status read's HTTP answer. hinted says the id
// asked about was a real task of this route, which is what makes a 2xx mean
// LivenessTask; a 2xx about the sentinel is a gateway that answers anything
// and proves only that it answered.
func ReadLiveness(statusCode int, body []byte, hinted bool) Liveness {
	verdict := Liveness{StatusCode: statusCode}
	_, structured := decodeObject(body)
	switch {
	case statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices:
		if hinted && structured {
			verdict.Class = LivenessTask
		} else {
			verdict.Class = LivenessAnswered
		}
		return verdict
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		verdict.Class = LivenessAuth
	case statusCode == http.StatusTooManyRequests:
		verdict.Class = LivenessRateLimited
	case statusCode >= http.StatusInternalServerError:
		verdict.Class = LivenessUpstream
	case structured && (statusCode == http.StatusBadRequest ||
		statusCode == http.StatusNotFound ||
		statusCode == http.StatusUnprocessableEntity):
		verdict.Class = LivenessAnswered
		return verdict
	default:
		verdict.Class = LivenessUnmounted
	}
	verdict.Message = fmt.Sprintf("video status read returned %d: %s", statusCode, compactLivenessBody(body))
	return verdict
}

// TransportLiveness is the verdict for a status read that got no answer.
func TransportLiveness(err error) Liveness {
	return Liveness{Class: LivenessTransport, Message: "video status read failed: " + err.Error()}
}

func compactLivenessBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 200 {
		text = text[:200]
	}
	if text == "" {
		return "empty response"
	}
	return text
}
