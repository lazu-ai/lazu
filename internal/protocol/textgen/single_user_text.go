package textgen

import "strings"

// SingleUserText validates the restricted channel contract before any request
// is flattened. In particular, role boundaries and opaque items must never be
// lost merely to accommodate an upstream's string-only input parser.
func SingleUserText(request *Request) (string, error) {
	unsupported := func(path string) (string, error) {
		return "", &UnsupportedFeatureError{Feature: FeatureInputText, Path: path,
			Reason: "this channel accepts only one user text message; instructions, history, tools, media and stored conversations are unavailable"}
	}
	if request == nil || len(request.Conversation) != 1 {
		return unsupported("input")
	}
	if request.Raw.Protocol == ProtocolOpenAIResponses && strings.TrimSpace(string(request.Raw.TopLevel["input"])) == "null" {
		return unsupported("input")
	}
	if len(request.Instructions) != 0 {
		return unsupported("instructions")
	}
	if len(request.Tools) != 0 || (request.ToolPolicy.Mode != "" && request.ToolPolicy.Mode != "auto" && request.ToolPolicy.Mode != "none") {
		return unsupported("tools")
	}
	lifecycle := request.Lifecycle
	meaningfulJSON := func(value []byte) bool {
		text := strings.TrimSpace(string(value))
		return text != "" && text != "null"
	}
	if lifecycle.PreviousResponseID != "" || meaningfulJSON(lifecycle.Conversation) || meaningfulJSON(lifecycle.PromptTemplate) || (lifecycle.Store.Set && lifecycle.Store.Value) {
		return unsupported("conversation")
	}
	if len(request.Raw.UnknownFields) != 0 {
		return unsupported("extensions")
	}
	item := request.Conversation[0]
	if item.Kind != InputMessage || item.Role != "user" || item.Name != "" || len(item.Opaque) != 0 || len(item.Content) != 1 || item.Content[0].Kind != ContentText {
		return unsupported("input[0]")
	}
	return item.Content[0].Text, nil
}
