package channel

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/types"
)

// How a stream's in-band failure is classified and shown. The transport said
// 200, so the upstream's words are the only signal: these decide whether the
// attempt is retried (a failure before any output) and what the client is
// told (a failure after). Shared by the native passthroughs and the protocol
// hubs.

// FirstEventFailure converts an in-band stream failure that reached
// no client byte into a normal relay error. The upstream's own message is kept
// (it is what the client will display) but the status is re-derived locally: the
// transport said 200, which would otherwise classify as success and suppress
// both the retry and the error row.
func FirstEventFailure(c *gin.Context, upstreamError *types.OpenAIError) *types.LazuError {
	message := "upstream stream failed before sending any output"
	if upstreamError != nil && strings.TrimSpace(upstreamError.Message) != "" {
		message = strings.TrimSpace(upstreamError.Message)
	}
	// Default to a generic upstream failure, NOT to "overloaded". Only a text
	// that actually names a transient condition earns a retry: a provider that
	// reports a blocked prompt or an oversized context in-band would otherwise
	// be re-sent three times to fail identically, and be described to the
	// customer as a busy model they should wait out or pay more to avoid.
	code := types.ErrorCodeUpstreamError
	if upstreamError != nil {
		if mapped, ok := StreamErrorCode(upstreamError); ok {
			code = mapped
		}
	}
	logger.LogError(c, fmt.Sprintf("upstream stream failed on its first event: %s", message))
	return types.NewErrorWithStatusCode(
		fmt.Errorf("%s", message),
		code,
		types.StatusForCode(code, http.StatusBadGateway),
	)
}

// StreamErrorCode classifies an in-band stream error by its text, the
// only signal available when the transport status is 200.
//
// The wrong-fit classes come first and on purpose: they map onto codes the
// retry policy refuses to repeat, because the same prompt on the same channel
// produces the same rejection. Everything that is not recognised stays a
// generic upstream failure for the caller to decide about.
func StreamErrorCode(upstreamError *types.OpenAIError) (types.ErrorCode, bool) {
	text := strings.ToLower(fmt.Sprint(upstreamError.Code) + " " + upstreamError.Type + " " + upstreamError.Message)
	switch {
	case strings.Contains(text, "insufficient_quota") || strings.Contains(text, "billing_error") ||
		strings.Contains(text, "exceeded your current quota") || strings.Contains(text, "credit balance") ||
		strings.Contains(text, "insufficient balance") || strings.Contains(text, "insufficient_balance"):
		return types.ErrorCodeUpstreamBillingFailed, true
	case strings.Contains(text, "invalid_api_key") || strings.Contains(text, "authentication_error") ||
		strings.Contains(text, "invalid api key") || strings.Contains(text, "unauthorized"):
		return types.ErrorCodeChannelInvalidKey, true
	case strings.Contains(text, "content filter") || strings.Contains(text, "content_filter") ||
		strings.Contains(text, "content policy") || strings.Contains(text, "safety") ||
		strings.Contains(text, "blocked"):
		return types.ErrorCodePromptBlocked, true
	// Same classification the HTTP-status error map uses for these texts
	// (internal/relay/errmap), so an in-band failure and a 400 carrying the
	// same words are treated alike.
	case strings.Contains(text, "context_length_exceeded") || strings.Contains(text, "context length") || strings.Contains(text, "context window") ||
		strings.Contains(text, "maximum context") || strings.Contains(text, "prompt is too long"):
		return types.ErrorCodeContextLengthExceeded, true
	case strings.Contains(text, "invalid_request") || strings.Contains(text, "invalid request"):
		return types.ErrorCodeBadRequestBody, true
	case strings.Contains(text, "rate limit") || strings.Contains(text, "rate_limit") || strings.Contains(text, "too many requests"):
		return types.ErrorCodeUpstreamRateLimited, true
	case strings.Contains(text, "overload") || strings.Contains(text, "capacity") || strings.Contains(text, "busy") || strings.Contains(text, "过载"):
		return types.ErrorCodeUpstreamOverloaded, true
	}
	return "", false
}

// StreamFailureFrame is the chat-completions error frame for a stream the
// gateway ends mid-flight. Its code, type and LZ number come from the same
// catalog row; operator failures never expose the provider's diagnostics.
func StreamFailureFrame(code types.ErrorCode, upstreamError *types.OpenAIError) (types.OpenAIError, string) {
	if upstreamError != nil {
		if mapped, ok := StreamErrorCode(upstreamError); ok {
			code = mapped
		}
	}
	w := types.RenderWire(code, nil, 0)
	frame := types.OpenAIError{
		Message: w.Message,
		Type:    w.Type,
		Code:    string(w.Code),
		Lazu:    w.ID,
		Docs:    w.Docs,
	}
	if upstreamError != nil && w.Audience == types.AudienceCustomer {
		if value := strings.TrimSpace(upstreamError.Message); value != "" {
			frame.Message = common.MaskSensitiveInfo(value)
		}
	}
	return frame, frame.Message
}
