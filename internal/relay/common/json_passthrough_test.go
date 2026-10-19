package common

import (
	"encoding/json"
	"testing"
)

func TestMergeUnknownTopLevelFields(t *testing.T) {
	t.Parallel()

	original := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"x_future":{"enabled":true},"temperature":0.2}`)
	converted := []byte(`{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"hi"}],"temperature":0.5}`)

	merged, err := MergeUnknownTopLevelFields(original, converted)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(merged, &obj); err != nil {
		t.Fatalf("decode merged json failed: %v", err)
	}

	if got, _ := obj["model"].(string); got != "gpt-4.1-mini" {
		t.Fatalf("converted value must win for model, got %q", got)
	}
	if got, _ := obj["temperature"].(float64); got != 0.5 {
		t.Fatalf("converted value must win for temperature, got %v", got)
	}
	if _, ok := obj["x_future"]; !ok {
		t.Fatalf("expected unknown field x_future to be preserved")
	}
}

func TestMergeUnknownTopLevelFields_NonObjectBodyFallback(t *testing.T) {
	t.Parallel()

	original := []byte(`[1,2,3]`)
	converted := []byte(`{"model":"gpt-4.1-mini"}`)

	merged, err := MergeUnknownTopLevelFields(original, converted)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if string(merged) != string(converted) {
		t.Fatalf("expected converted body unchanged when original is not object")
	}
}
