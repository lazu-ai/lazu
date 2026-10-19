package relay

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func requestWithGeminiToolMetadata() *dto.GeneralOpenAIRequest {
	request := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "assistant", Content: ""}},
	}
	request.Messages[0].SetToolCalls([]dto.ToolCallRequest{{
		ID:   "call_123",
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      "get_weather",
			Arguments: `{"city":"Taipei"}`,
		},
		ExtraContent: &dto.ToolCallExtraContent{
			Google: &dto.GoogleToolCallExtraContent{
				ThoughtSignature: "signature-123",
			},
		},
	}})
	return request
}

func TestStripProviderSpecificToolCallMetadataForOpenAIRelay(t *testing.T) {
	request := requestWithGeminiToolMetadata()

	stripProviderSpecificToolCallMetadata(request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeOpenAI},
	})

	toolCalls := request.Messages[0].ParseToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].ExtraContent != nil {
		t.Fatalf("expected provider metadata to be stripped, got %+v", toolCalls[0].ExtraContent)
	}
}

func TestStripProviderSpecificToolCallMetadataKeepsGeminiSignature(t *testing.T) {
	request := requestWithGeminiToolMetadata()

	stripProviderSpecificToolCallMetadata(request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeGemini},
	})

	toolCalls := request.Messages[0].ParseToolCalls()
	if len(toolCalls) != 1 || toolCalls[0].ExtraContent == nil || toolCalls[0].ExtraContent.Google == nil {
		t.Fatalf("expected Gemini provider metadata to be preserved, got %+v", toolCalls)
	}
	if got := toolCalls[0].ExtraContent.Google.ThoughtSignature; got != "signature-123" {
		t.Fatalf("expected signature-123, got %q", got)
	}
}
