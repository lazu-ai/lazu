package controller

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
)

func TestBuildTestRequestBuildsVideoRequestForVideoEndpoint(t *testing.T) {
	request := buildTestRequest("veo-3-fast", string(constant.EndpointTypeVideoGeneration))
	videoRequest, ok := request.(*dto.VideoRequest)
	if !ok {
		t.Fatalf("expected video request, got %T", request)
	}
	if videoRequest.Model != "veo-3-fast" || videoRequest.Prompt == "" || videoRequest.Seconds <= 0 {
		t.Fatalf("unexpected video test request: %+v", videoRequest)
	}
}
