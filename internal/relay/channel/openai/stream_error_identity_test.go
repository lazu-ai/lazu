package openai

import (
	"github.com/lazu-ai/lazu/internal/relay/channel"
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
)

func TestStreamFailureUsesOneCatalogIdentity(t *testing.T) {
	for _, tc := range []struct {
		upstream types.OpenAIError
		code     types.ErrorCode
	}{
		{types.OpenAIError{Code: "insufficient_quota", Type: "rate_limit_error", Message: "private provider balance"}, types.ErrorCodeUpstreamBillingFailed},
		{types.OpenAIError{Code: "invalid_api_key", Type: "authentication_error", Message: "private provider key"}, types.ErrorCodeUpstreamAuthFailed},
		{types.OpenAIError{Code: "context_length_exceeded", Type: "invalid_request_error", Message: "Too many tokens"}, types.ErrorCodeContextLengthExceeded},
		{types.OpenAIError{Code: "provider_specific", Type: "provider_specific", Message: "Unexpected failure"}, types.ErrorCodeUpstreamStreamFailed},
	} {
		t.Run(string(tc.code), func(t *testing.T) {
			frame, _ := channel.StreamFailureFrame(types.ErrorCodeUpstreamStreamFailed, &tc.upstream)
			spec := types.RenderWire(tc.code, nil, 0)
			if frame.Code != string(spec.Code) || frame.Type != spec.Type || frame.Lazu != spec.ID || frame.Docs != spec.Docs {
				t.Fatalf("mixed error identities: %+v", frame)
			}
			if spec.Audience != types.AudienceCustomer && frame.Message != spec.Message {
				t.Fatalf("provider diagnostic leaked: %+v", frame)
			}
		})
	}
}
