package openai

import (
	"github.com/lazu-ai/lazu/internal/dto"
	"testing"
)

func TestSeedreamUsageCountsOnlySuccessfulOutputs(t *testing.T) {
	u, err := seedreamUsage([]byte(`{"data":[{"url":"https://example.org/a","size":"2048x2048"},{"error":{"code":"moderation"}},{"b64_json":"image","size":"4096x4096"}],"usage":{"generated_images":3,"output_tokens":100}}`), &dto.ImageRequest{Model: "seedream-5.0-lite", SequentialImageGeneration: "auto", SequentialImageGenerationOptions: &dto.SequentialImageOptions{MaxImages: 4}})
	if err != nil || len(u.ImageUsage.Outputs) != 2 {
		t.Fatalf("usage=%+v err=%v", u, err)
	}
	if *u.GeneratedImages != 2 {
		t.Fatal("trusted requested/provider count instead of returned successes")
	}
}

func TestSeedreamUsagePreservesActualLayerPixels(t *testing.T) {
	u, err := seedreamUsage([]byte(`{"data":[{"url":"https://example.org/a","size":"2048x2048","z_index":0},{"url":"https://example.org/b","size":"3000x100","z_index":1}],"usage":{"generated_images":2}}`), &dto.ImageRequest{Model: "seedream-5.0-pro", LayerDecomposition: true})
	if err != nil || u.ImageUsage.Outputs[1].Pixels != 300000 || !u.ImageUsage.Outputs[0].Layer {
		t.Fatalf("usage=%+v err=%v", u, err)
	}
}

func TestSeedreamUsageRejectsUnusableResponses(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":[{"error":{"code":"moderation"}}]}`, `{"data":[{"url":"https://example.org/a"}]}`} {
		if _, err := seedreamUsage([]byte(body), &dto.ImageRequest{}); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestSeedreamUsageRejectsExcessOutputsAndUsesReportedReferences(t *testing.T) {
	req := &dto.ImageRequest{Model: "seedream-5.0-pro", Image: []byte(`["https://example.org/a", "https://example.org/b"]`)}
	body := []byte(`{"data":[{"url":"https://example.org/out","size":"1536x1536"}],"usage":{"input_images":1}}`)
	u, err := seedreamUsage(body, req)
	if err != nil || u.ImageUsage.InputImages != 1 {
		t.Fatalf("references: %+v %v", u, err)
	}
	if _, err := seedreamUsage([]byte(`{"data":[{"url":"a","size":"1536x1536"},{"url":"b","size":"1536x1536"}]}`), req); err == nil {
		t.Fatal("accepted unrequested extra output")
	}
}
