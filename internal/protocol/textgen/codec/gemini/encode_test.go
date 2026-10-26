package gemini

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestEncodePreservesLegacyNamedFunctionResult(t *testing.T) {
	request := &textgen.Request{Conversation: []textgen.InputItem{{Kind: textgen.InputFunctionResult, Role: "function", Name: "lookup", Output: `{"ok":true}`}}}
	wire, err := Encode(request, EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	response := wire.Contents[0].Parts[0].FunctionResponse
	if response == nil || response.Name != "lookup" || response.ID != "" {
		t.Fatalf("legacy function response lost: %+v", response)
	}
}
