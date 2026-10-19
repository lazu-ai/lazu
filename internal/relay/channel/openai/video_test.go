package openai

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestConvertVideoRequestKeepsOpenAIShape(t *testing.T) {
	adaptor := &Adaptor{}
	converted, err := adaptor.ConvertVideoRequest(nil, nil, dto.VideoRequest{
		Model:          "sora-2",
		Prompt:         "A cinematic city flyover",
		Seconds:        8,
		Size:           "1280x720",
		ResponseFormat: "url",
	})
	if err != nil {
		t.Fatalf("ConvertVideoRequest error: %v", err)
	}
	payload, ok := converted.(dto.OpenAIVideoRequest)
	if !ok {
		t.Fatalf("expected OpenAIVideoRequest, got %T", converted)
	}
	if payload.Model != "sora-2" || payload.Prompt == "" || payload.Seconds != 8 || payload.Size != "1280x720" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
