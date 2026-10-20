package openai_responses

import (
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestDecoderKeepsUsageOfAFailedResponse(t *testing.T) {
	decoder := &StreamDecoder{}
	events, err := decoder.Decode(`{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_error","message":"boom"},"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != textgen.EventUsage || events[0].Usage.CompletionTokens != 4 || events[1].Kind != textgen.EventError {
		t.Fatalf("want usage then error, got %+v", events)
	}
}

func TestEmitterCountsSealedReasoningAgainstTheRetentionLimit(t *testing.T) {
	emitter := NewStreamEmitter("id", "m", nil, 64)
	sealed := `{"type":"reasoning","id":"rs_1","encrypted_content":"` + strings.Repeat("x", 128) + `"}`
	if _, err := emitter.Emit(textgen.Event{Kind: textgen.EventReasoningSealed, Sealed: []byte(sealed), SealedBy: SealedBy}); err == nil {
		t.Fatal("a sealed item larger than the retention limit must fail the stream")
	}
}
