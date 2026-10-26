package gemini

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestEncodeBridgedRequestRejectsImageOverflowBeforeFetching(t *testing.T) {
	previous := constant.GeminiVisionMaxImageNum
	constant.GeminiVisionMaxImageNum = 16
	t.Cleanup(func() { constant.GeminiVisionMaxImageNum = previous })

	parts := make([]textgen.ContentPart, 17)
	for index := range parts {
		parts[index] = textgen.ContentPart{Kind: textgen.ContentImage, URL: fmt.Sprintf("https://example.test/%d.png", index)}
	}
	request := &textgen.Request{Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: parts}}}
	fetches := 0
	_, err := encodeBridgedRequest(&relaycommon.RelayInfo{}, request, func(string) (string, string, error) {
		fetches++
		return "image/png", "AA", nil
	})
	if err == nil || !strings.Contains(err.Error(), "too many images") {
		t.Fatalf("expected image limit error, got %v", err)
	}
	if fetches != 0 {
		t.Fatalf("image limit must be checked before remote fetches, got %d fetches", fetches)
	}
}

func TestInputImageLimitAccumulatesGroupedFunctionResults(t *testing.T) {
	previous := constant.GeminiVisionMaxImageNum
	constant.GeminiVisionMaxImageNum = 1
	t.Cleanup(func() { constant.GeminiVisionMaxImageNum = previous })

	image := []textgen.ContentPart{{Kind: textgen.ContentImage, URL: "https://example.test/a.png"}}
	request := &textgen.Request{Conversation: []textgen.InputItem{
		{Kind: textgen.InputFunctionCall, CallID: "a", FunctionName: "lookup", Arguments: `{}`},
		{Kind: textgen.InputFunctionResult, CallID: "a", Content: image},
		{Kind: textgen.InputFunctionResult, CallID: "b", Content: image},
	}}
	if err := checkInputImageCount(request); err == nil {
		t.Fatal("grouped function results must share the per-message image limit")
	}
}
