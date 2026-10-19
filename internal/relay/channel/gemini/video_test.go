package gemini

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestConvertVideoRequestBuildsVeoPredictShape(t *testing.T) {
	adaptor := &Adaptor{}
	converted, err := adaptor.ConvertVideoRequest(nil, nil, dto.VideoRequest{
		Model:       "veo-3.0-generate-preview",
		Prompt:      "A product reveal shot",
		Seconds:     6,
		AspectRatio: "9:16",
		Quality:     "high",
	})
	if err != nil {
		t.Fatalf("ConvertVideoRequest error: %v", err)
	}
	payload, ok := converted.(dto.GeminiVideoRequest)
	if !ok {
		t.Fatalf("expected GeminiVideoRequest, got %T", converted)
	}
	if len(payload.Instances) != 1 || payload.Instances[0].Prompt == "" {
		t.Fatalf("unexpected instances: %+v", payload.Instances)
	}
	if payload.Parameters.DurationSeconds != 6 ||
		payload.Parameters.AspectRatio != "9:16" ||
		payload.Parameters.SampleCount != 1 ||
		payload.Parameters.Resolution != "1080p" {
		t.Fatalf("unexpected parameters: %+v", payload.Parameters)
	}
}
