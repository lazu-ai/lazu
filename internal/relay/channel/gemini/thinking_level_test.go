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
		Model:     "gemini-3.7-flash",
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		ExtraBody: json.RawMessage(`{"google":{"thinking_config":{"thinking_level":"low"}}}`),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-3.7-flash"},
	}

	converted, err := convertChatRequest(t, request, info)
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
		Model:     "gemini-3.7-flash",
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		ExtraBody: json.RawMessage(`{"google":{"thinking_config":{"thinking_level":"fast"}}}`),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-3.7-flash"},
	}

	_, err := convertChatRequest(t, request, info)
	if err == nil || !strings.Contains(err.Error(), "thinking_level must be one of") {
		t.Fatalf("expected invalid thinking-level error, got %v", err)
	}
}

func TestEffortThinkingUsesBudgetBeforeGemini3(t *testing.T) {
	for _, upstream := range []string{"gemini-2.5-pro", "models/gemini-2.5-flash", "Gemini-2.5-Flash-Lite"} {
		config := effortThinking(upstream, "high")
		if config.ThinkingLevel != "" {
			t.Fatalf("%s: thinkingLevel %q sent to a model that refuses it", upstream, config.ThinkingLevel)
		}
		if config.ThinkingBudget == nil || *config.ThinkingBudget != 16384 || !config.IncludeThoughts {
			t.Fatalf("%s: expected a 16384-token budget with thoughts, got %#v", upstream, config)
		}
	}
}

func TestEffortThinkingUsesLevelFromGemini3(t *testing.T) {
	for _, upstream := range []string{"gemini-3.1-pro-preview", "gemini-3.5-flash", "models/gemini-3-pro-preview"} {
		config := effortThinking(upstream, "low")
		if config.ThinkingLevel != "low" || config.ThinkingBudget != nil || !config.IncludeThoughts {
			t.Fatalf("%s: expected thinkingLevel low, got %#v", upstream, config)
		}
	}
}

// convertChatRequest encodes a Chat request for Gemini the way the adaptor
// does: decoded into the IR, then EncodeBridgedRequest.
func convertChatRequest(t *testing.T, request dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) (*dto.GeminiChatRequest, error) {
	t.Helper()
	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
	if err != nil {
		return nil, err
	}
	return converted.(*dto.GeminiChatRequest), nil
}
