package textgen

import "testing"

func TestTokenCountMetaCoversStructuredConversationAndTools(t *testing.T) {
	request := &Request{
		Instructions: []Instruction{{Role: "developer", Text: "Be concise"}},
		Conversation: []InputItem{
			{Kind: InputMessage, Role: "user", Name: "Jessy", Content: []ContentPart{{Kind: ContentText, Text: "hello"}, {Kind: ContentImage, URL: "https://example.test/a.png", Detail: "low"}}},
			{Kind: InputFunctionCall, FunctionName: "lookup", Arguments: `{"q":"x"}`},
			{Kind: InputFunctionResult, CallID: "call_1", Output: "done"},
		},
		Tools:    []ToolDefinition{{Type: "function", Name: "lookup", Description: "Lookup data", Parameters: []byte(`{"type":"object"}`)}},
		Sampling: SamplingOptions{MaxOutput: Some(uint(128)), N: Some(2)},
	}
	meta := TokenCountMeta(request)
	if meta.MessagesCount != 1 || meta.NameCount != 1 || meta.ToolsCount != 1 || len(meta.Files) != 1 {
		t.Fatalf("unexpected token meta counts: %#v", meta)
	}
	if meta.MaxTokens != 128 || meta.CompletionCount != 2 {
		t.Fatalf("unexpected output token metadata: %#v", meta)
	}
	for _, expected := range []string{"Be concise", "hello", "lookup", "done"} {
		if !contains(meta.CombineText, expected) {
			t.Fatalf("combined text %q does not contain %q", meta.CombineText, expected)
		}
	}
}

func contains(value, part string) bool {
	for index := 0; index+len(part) <= len(value); index++ {
		if value[index:index+len(part)] == part {
			return true
		}
	}
	return false
}
