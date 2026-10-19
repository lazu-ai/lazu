package model

import (
	"strings"
	"testing"
)

func TestTokenNormalizeProductMetadata(t *testing.T) {
	token := &Token{
		Product:  " search-api ",
		Metadata: JSONValue([]byte(`{"env":"prod","cost_center":"search"}`)),
	}
	if err := token.NormalizeProductMetadata(); err != nil {
		t.Fatalf("normalize metadata failed: %v", err)
	}
	if token.Product != "search-api" {
		t.Fatalf("expected product to be trimmed, got %q", token.Product)
	}
	metadata := string(token.Metadata)
	if !strings.Contains(metadata, `"env":"prod"`) || !strings.Contains(metadata, `"cost_center":"search"`) {
		t.Fatalf("expected metadata object to round-trip, got %s", string(token.Metadata))
	}
}

func TestTokenNormalizeProductMetadataRejectsInvalidJSON(t *testing.T) {
	token := &Token{Metadata: JSONValue([]byte(`[]`))}
	if err := token.NormalizeProductMetadata(); err == nil {
		t.Fatal("expected non-object metadata to fail")
	}
}

func TestTokenModelLimitAllows(t *testing.T) {
	limits := map[string]bool{
		"gpt-4-gizmo-*":             true,
		"claude-sonnet-4-20250514":  true,
		"gemini-2.5-pro-thinking-*": true,
		"plain-exact-model":         true,
	}

	tests := []struct {
		name      string
		modelName string
		want      bool
	}{
		{
			name:      "allows exact model name",
			modelName: "plain-exact-model",
			want:      true,
		},
		{
			name:      "allows normalized gizmo model",
			modelName: "gpt-4-gizmo-demo",
			want:      true,
		},
		{
			name:      "allows claude thinking suffix normalized to base model",
			modelName: "claude-sonnet-4-20250514#thinking",
			want:      true,
		},
		{
			name:      "allows gemini thinking budget normalized wildcard",
			modelName: "gemini-2.5-pro-thinking-8192",
			want:      true,
		},
		{
			name:      "rejects model outside allow-list",
			modelName: "not-allowed-model",
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TokenModelLimitAllows(limits, tc.modelName); got != tc.want {
				t.Fatalf("expected %v for model %q, got %v", tc.want, tc.modelName, got)
			}
		})
	}
}
