package openai

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestRecordResponsesFinishReasonMapsStatus(t *testing.T) {
	cases := []struct {
		response dto.OpenAIResponsesResponse
		want     string
	}{
		{dto.OpenAIResponsesResponse{Status: "completed"}, "stop"},
		{dto.OpenAIResponsesResponse{Status: "incomplete",
			IncompleteDetails: &dto.IncompleteDetails{Reason: "max_output_tokens"}}, "length"},
		{dto.OpenAIResponsesResponse{Status: "incomplete",
			IncompleteDetails: &dto.IncompleteDetails{Reason: "content_filter"}}, "content_filter"},
		{dto.OpenAIResponsesResponse{Status: "in_progress"}, ""},
	}
	for _, tc := range cases {
		info := &relaycommon.RelayInfo{}
		recordResponsesFinishReason(info, &tc.response)
		if info.FinishReason != tc.want {
			t.Errorf("status %s = %q, want %q", tc.response.Status, info.FinishReason, tc.want)
		}
	}
}
