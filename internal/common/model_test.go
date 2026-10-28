package common

import "testing"

// The one spelling of "this name is a vector model", shared by the catalog's
// endpoint derivation, the channel test, the probe and the health page.
func TestIsEmbeddingModel(t *testing.T) {
	for name, want := range map[string]bool{
		"BAAI/bge-m3":               true,
		"@cf/baai/bge-m3":           true,
		"text-embedding-3-small":    true,
		"gemini-embedding-001":      true,
		"jina-embeddings-v4":        true,
		"voyage-multimodal-3-embed": true,
		"m3e-base":                  true,
		"  Text-Embedding-Ada-002 ": true,
		"bge-reranker-v2-m3":        false,
		"jina-reranker-v2":          false,
		"gpt-5.4":                   false,
		"claude-sonnet-5":           false,
		"":                          false,
	} {
		if got := IsEmbeddingModel(name); got != want {
			t.Errorf("IsEmbeddingModel(%q) = %t, want %t", name, got, want)
		}
	}
}
