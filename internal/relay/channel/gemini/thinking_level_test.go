package gemini

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestConvertOpenAIExtraBodyMapsNativeGeminiThinkingLevel(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		ExtraBody: json.RawMessage(`{"google":{"thinking_config":{"thinking_level":"low"}}}`),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-3.7-flash"},
	}

	converted, err := CovertGemini2OpenAI(nil, request, info)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}
	thinkingConfig := converted.GenerationConfig.ThinkingConfig
	if thinkingConfig == nil || thinkingConfig.ThinkingLevel != "low" {
		t.Fatalf("expected native low thinking level, got %#v", thinkingConfig)
	}

	raw, err := json.Marshal(converted)
	if err != nil {
		t.Fatalf("marshal Gemini request: %v", err)
	}
	if !strings.Contains(string(raw), `"thinkingLevel":"low"`) {
		t.Fatalf("expected Gemini thinkingLevel field in upstream request: %s", raw)
	}
}

func TestConvertOpenAIExtraBodyRejectsInvalidGeminiThinkingLevel(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		ExtraBody: json.RawMessage(`{"google":{"thinking_config":{"thinking_level":"fast"}}}`),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-3.7-flash"},
	}

	_, err := CovertGemini2OpenAI(nil, request, info)
	if err == nil || !strings.Contains(err.Error(), "thinking_level must be one of") {
		t.Fatalf("expected invalid thinking-level error, got %v", err)
	}
}
