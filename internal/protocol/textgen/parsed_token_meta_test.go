package textgen

import (
	"reflect"
	"testing"
)

func tokenMetaTestRequests() []*Request {
	return []*Request{
		{},
		{Conversation: []InputItem{{Kind: InputMessage, Role: "user", Content: []ContentPart{{Kind: ContentText, Text: "hello"}}}}},
		{
			Instructions: []Instruction{{Role: "system", Text: "terse"}},
			Conversation: []InputItem{
				{Kind: InputMessage, Role: "user", Name: "a", Content: []ContentPart{{Kind: ContentText, Text: "look"}, {Kind: ContentImage, URL: "https://example.test/1.png"}, {Kind: ContentImage, URL: "https://example.test/2.png", Detail: "low"}}},
				{Kind: InputFunctionCall, FunctionName: "f", Arguments: `{"x":1}`},
				{Kind: InputFunctionResult, CallID: "c", Output: "ok"},
			},
			Tools:    []ToolDefinition{{Type: "function", Name: "f", Parameters: []byte(`{"type":"object"}`)}},
			Sampling: SamplingOptions{MaxOutput: Some(uint(64)), N: Some(3)},
		},
	}
}

func TestParsedRequestTokenCountMetaMatchesFreshProjection(t *testing.T) {
	for index, request := range tokenMetaTestRequests() {
		parsed := &ParsedRequest{Semantic: request}
		want := TokenCountMeta(request)
		first := parsed.TokenCountMeta()
		if !reflect.DeepEqual(first, want) {
			t.Fatalf("request %d: cached meta %#v, fresh %#v", index, first, want)
		}
		// A caller editing its copy (the distributor drops Files) must not
		// change what the next caller gets.
		first.Files = nil
		first.CombineText = "edited"
		if second := parsed.TokenCountMeta(); !reflect.DeepEqual(second, want) {
			t.Fatalf("request %d: edit leaked into the cache: %#v", index, second)
		}
	}
}

func TestParsedRequestTokenCountMetaFollowsReplacedSemantic(t *testing.T) {
	requests := tokenMetaTestRequests()
	parsed := &ParsedRequest{Semantic: requests[1]}
	_ = parsed.TokenCountMeta()
	// Gateway enrichment copies the parsed request and swaps Semantic.
	copied := *parsed
	copied.Semantic = requests[2]
	if got, want := copied.TokenCountMeta(), TokenCountMeta(requests[2]); !reflect.DeepEqual(got, want) {
		t.Fatalf("stale meta after Semantic changed: %#v, want %#v", got, want)
	}
	if got, want := parsed.TokenCountMeta(), TokenCountMeta(requests[1]); !reflect.DeepEqual(got, want) {
		t.Fatalf("original parsed request changed: %#v, want %#v", got, want)
	}
	var nilParsed *ParsedRequest
	if got := nilParsed.TokenCountMeta(); got == nil {
		t.Fatal("nil parsed request must still return empty metadata")
	}
}
