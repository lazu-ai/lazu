package router

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestSemanticHashSeparatesStateWithoutCountingItAsText(t *testing.T) {
	base := &textgen.Request{Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "continue"}}}}}
	baseline := ExtractSemanticFeatures(context.Background(), nil, base)
	for name, change := range map[string]func(*textgen.Request){
		"prior response": func(r *textgen.Request) { r.Lifecycle.PreviousResponseID = "resp_1" },
		"conversation":   func(r *textgen.Request) { r.Lifecycle.Conversation = json.RawMessage(`{"id":"conv_1"}`) },
		"prompt": func(r *textgen.Request) {
			r.Lifecycle.PromptTemplate = json.RawMessage(`{"id":"prompt_1","version":"2"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := base.Clone()
			change(r)
			f := ExtractSemanticFeatures(context.Background(), nil, r)
			if f.PromptHash == baseline.PromptHash {
				t.Fatalf("state identity/counting mismatch: %+v vs %+v", f, baseline)
			}
			if f.PromptHash != ExtractSemanticFeatures(context.Background(), nil, r.Clone()).PromptHash {
				t.Fatal("identical retry changed identity")
			}
		})
	}
	first, second := base.Clone(), base.Clone()
	first.Lifecycle.PromptTemplate = json.RawMessage(`{"id":"p","version":"2"}`)
	second.Lifecycle.PromptTemplate = json.RawMessage(`{ "version": "2", "id": "p" }`)
	if ExtractSemanticFeatures(context.Background(), nil, first).PromptHash != ExtractSemanticFeatures(context.Background(), nil, second).PromptHash {
		t.Fatal("JSON key order changed semantic retry identity")
	}
}

func TestExtractFeatures(t *testing.T) {
	t.Parallel()
	raw := []byte("{\"model\":\"gpt-test\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"please solve $x+1$ and write ```go\\nfunc main(){}\\n```\"},{\"role\":\"tool\",\"content\":\"ok\"},{\"role\":\"user\",\"content\":[{\"type\":\"image_url\",\"image_url\":{\"url\":\"https://example.com/a.png\"}}]}],\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"lookup\"}}]}")
	f := ExtractFeatures(context.Background(), &RequestContext{}, raw)
	if f.PromptHash == "" {
		t.Fatalf("expected prompt hash: %#v", f)
	}
	if !f.HasVision || f.VisionImageCount != 1 {
		t.Fatalf("expected vision features: %#v", f)
	}
}

func TestExtractSemanticFeaturesIgnoresInlineImageData(t *testing.T) {
	t.Parallel()
	imageData := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 2<<20)))
	request := &textgen.Request{
		Conversation: []textgen.InputItem{{
			Role: "user",
			Content: []textgen.ContentPart{
				{Kind: textgen.ContentText, Text: "describe this image"},
				{Kind: textgen.ContentImage, Data: imageData},
			},
		}},
	}
	f := ExtractSemanticFeatures(context.Background(), &RequestContext{}, request)
	if !f.HasVision || f.VisionImageCount != 1 {
		t.Fatalf("expected one vision image: %#v", f)
	}
	if len(f.PromptHash) == 0 || strings.Contains(f.PromptHash, imageData) {
		t.Fatalf("expected compact semantic hash: %#v", f)
	}
	other := request.Clone()
	other.Conversation[0].Content[1].Data = strings.Repeat("y", 2<<20)
	if otherFeatures := ExtractSemanticFeatures(context.Background(), &RequestContext{}, other); otherFeatures.PromptHash == f.PromptHash {
		t.Fatal("different inline media should produce different prompt hashes")
	}
}

func TestExtractSemanticFeaturesIncludesOutputConstraintInPromptHash(t *testing.T) {
	t.Parallel()
	base := &textgen.Request{
		Conversation: []textgen.InputItem{{
			Role:    "user",
			Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "return a result"}},
		}},
	}
	jsonSchema := []byte(`{"type":"object","properties":{"answer":{"type":"string"}}}`)
	withoutConstraint := ExtractSemanticFeatures(context.Background(), &RequestContext{}, base.Clone())
	withConstraintRequest := base.Clone()
	withConstraintRequest.Output = textgen.OutputConstraint{
		Type:        "json_schema",
		Name:        "answer",
		Description: "structured answer",
		Schema:      jsonSchema,
		Strict:      textgen.Some(true),
	}
	withConstraint := ExtractSemanticFeatures(context.Background(), &RequestContext{}, withConstraintRequest)
	if withoutConstraint.PromptHash == withConstraint.PromptHash {
		t.Fatal("different output constraints should produce different prompt hashes")
	}
}

func TestCanonicalRequestHashPreservesLargeStateNumbers(t *testing.T) {
	first := canonicalize([]byte(`{"prompt":{"variables":{"id":9007199254740992}}}`))
	second := canonicalize([]byte(`{"prompt":{"variables":{"id":9007199254740993}}}`))
	if string(first) == string(second) {
		t.Fatal("different prompt state numbers collided through float64 conversion")
	}
}
