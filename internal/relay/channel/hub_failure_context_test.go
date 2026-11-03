package channel

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
)

// An in-band context-window failure is worded as Anthropic words it, which
// Claude Code compacts on, and keeps none of the upstream's other text.
func TestStreamFailureFrameContextLength(t *testing.T) {
	for _, tc := range []struct{ upstream, want string }{
		{"This model's maximum context length is 128000 tokens. However, your messages resulted in 130512 tokens. customer-message", "prompt is too long: 130512 tokens > 128000 maximum"},
		{"Input is too long for requested model. customer-message", "prompt is too long"},
	} {
		frame, _ := StreamFailureFrame(types.ErrorCodeUpstreamError, &types.OpenAIError{Message: tc.upstream})
		if frame.Code != string(types.ErrorCodeContextLengthExceeded) || frame.Message != tc.want {
			t.Fatalf("frame = %+v, want %q", frame, tc.want)
		}
	}
}
