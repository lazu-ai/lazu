package router

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
)

// benchmarkChatRequest decodes a chat request of roughly size bytes, shaped
// like the gateway bench's large case (one long user message).
func benchmarkChatRequest(b *testing.B, size int) *textgen.Request {
	b.Helper()
	body, err := json.Marshal(map[string]any{
		"model":    "bench-model",
		"stream":   true,
		"messages": []map[string]any{{"role": "system", "content": "You are terse."}, {"role": "user", "content": strings.Repeat("lorem ipsum dolor sit amet ", size/27)}},
	})
	if err != nil {
		b.Fatal(err)
	}
	request, _, err := openai_chat.Decode(body)
	if err != nil {
		b.Fatal(err)
	}
	return request
}

func BenchmarkSemanticFeatures600KB(b *testing.B) {
	request := benchmarkChatRequest(b, 600<<10)
	b.ReportAllocs()
	for b.Loop() {
		ExtractSemanticFeatures(context.Background(), nil, request)
	}
}

func BenchmarkTokenCountMeta600KB(b *testing.B) {
	request := benchmarkChatRequest(b, 600<<10)
	b.ReportAllocs()
	for b.Loop() {
		textgen.TokenCountMeta(request)
	}
}

func TestSemanticImageCountMatchesFeatures(t *testing.T) {
	image := textgen.ContentPart{Kind: textgen.ContentImage, URL: "https://example.test/a.png"}
	text := textgen.ContentPart{Kind: textgen.ContentText, Text: "hi"}
	for _, request := range []*textgen.Request{
		nil,
		{},
		{Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Content: []textgen.ContentPart{text}}}},
		{Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Content: []textgen.ContentPart{text, image}}}},
		{Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Content: []textgen.ContentPart{image, image}}, {Kind: textgen.InputMessage, Content: []textgen.ContentPart{image}}}},
	} {
		features := ExtractSemanticFeatures(context.Background(), nil, request)
		if got := semanticImageCount(request); got != features.VisionImageCount {
			t.Fatalf("semanticImageCount = %d, features say %d", got, features.VisionImageCount)
		}
		if got, want := preferredChannelIntents(&Features{VisionImageCount: semanticImageCount(request)}), preferredChannelIntents(features); !reflect.DeepEqual(got, want) {
			t.Fatalf("intents = %v, want %v", got, want)
		}
	}
}

func BenchmarkSemanticImageCount600KB(b *testing.B) {
	request := benchmarkChatRequest(b, 600<<10)
	b.ReportAllocs()
	for b.Loop() {
		semanticImageCount(request)
	}
}
