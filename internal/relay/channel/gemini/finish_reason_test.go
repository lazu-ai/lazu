package gemini

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestRecordGeminiFinishReasonUsesChatVocabulary(t *testing.T) {
	for raw, want := range map[string]string{
		"STOP": "stop", "MAX_TOKENS": "length", "SAFETY": "content_filter", "OTHER": "other",
	} {
		reason := raw
		info := &relaycommon.RelayInfo{}
		recordGeminiFinishReason(info, &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{FinishReason: &reason}},
		})
		if info.FinishReason != want {
			t.Errorf("finish reason %s = %q, want %q", raw, info.FinishReason, want)
		}
	}
	info := &relaycommon.RelayInfo{FinishReason: "length"}
	recordGeminiFinishReason(info, &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{}}})
	if info.FinishReason != "length" {
		t.Errorf("a chunk without a reason overwrote the recorded one: %q", info.FinishReason)
	}
}
