package dto

import (
	"encoding/json"
	"testing"
)

func TestImageRequestPreservesStreamField(t *testing.T) {
	var request ImageRequest
	if err := json.Unmarshal([]byte(`{"model":"gpt-image-2","prompt":"test","stream":true}`), &request); err != nil {
		t.Fatalf("unmarshal image request: %v", err)
	}
	if !request.Stream {
		t.Fatal("expected stream=true to be preserved")
	}

	body, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal image request: %v", err)
	}
	var encoded struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &encoded); err != nil {
		t.Fatalf("unmarshal encoded image request: %v", err)
	}
	if !encoded.Stream {
		t.Fatalf("expected marshaled stream=true, got %s", string(body))
	}
}
