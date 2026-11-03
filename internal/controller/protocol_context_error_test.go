package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/types"
)

// A planner refusal for a too-long input reads as Anthropic's, which Claude
// Code compacts on; other refusals stay protocol_bridge_unsupported.
func TestProtocolBridgeContextOverflow(t *testing.T) {
	err := &textgen.UnsupportedFeatureError{Feature: textgen.FeatureInputText, Path: "input", Reason: "x", Tokens: 205000, Limit: 200000}
	e := protocolBridgeLazuError(nil, err)
	if e.GetErrorCode() != types.ErrorCodeContextLengthExceeded || e.StatusCode != http.StatusBadRequest {
		t.Fatalf("code = %s status %d", e.GetErrorCode(), e.StatusCode)
	}
	if got := e.ToClaudeError(); got.Message != "prompt is too long: 205000 tokens > 200000 maximum" || got.Type != types.ErrorTypeInvalidRequest {
		t.Fatalf("claude error = %+v", got)
	}
	other := protocolBridgeLazuError(nil, &textgen.UnsupportedFeatureError{Feature: textgen.FeatureInputText, Path: "messages[0]", Reason: "single text"})
	if other.GetErrorCode() != types.ErrorCodeProtocolBridgeUnsupported {
		t.Fatalf("non-overflow input refusal = %s", other.GetErrorCode())
	}
	if wrapped := protocolBridgeLazuError(nil, errors.Join(errors.New("plan"), err)); wrapped.GetErrorCode() != types.ErrorCodeContextLengthExceeded {
		t.Fatalf("wrapped = %s", wrapped.GetErrorCode())
	}
}
