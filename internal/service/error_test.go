package service

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
)

func TestRelayErrorHandlerPreservesMappedCodeForOpenAIError(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body: io.NopCloser(strings.NewReader(`{
			"error": {
				"message": "rate limit exceeded",
				"type": "rate_limit_error",
				"code": null
			}
		}`)),
	}

	// nil *gin.Context exercises the fallback path (channel type=0 → MapDefault).
	err := RelayErrorHandler(nil, resp, false)
	if err == nil {
		t.Fatal("expected relay error")
	}
	if got := err.GetErrorCode(); got != types.ErrorCodeUpstreamRateLimited {
		t.Fatalf("error code=%q want %q", got, types.ErrorCodeUpstreamRateLimited)
	}
	openAIError := err.ToOpenAIError()
	if got := fmt.Sprint(openAIError.Code); got != string(types.ErrorCodeUpstreamRateLimited) {
		t.Fatalf("openai error code=%v want %q", got, types.ErrorCodeUpstreamRateLimited)
	}
	if got := openAIError.Type; got != types.ErrorTypeRateLimit {
		t.Fatalf("openai error type=%q want %q", got, types.ErrorTypeRateLimit)
	}
}
