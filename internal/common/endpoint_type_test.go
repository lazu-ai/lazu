package common

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
)

func hasEndpoint(eps []constant.EndpointType, target constant.EndpointType) bool {
	for _, ep := range eps {
		if ep == target {
			return true
		}
	}
	return false
}

func countEndpoint(eps []constant.EndpointType, target constant.EndpointType) int {
	n := 0
	for _, ep := range eps {
		if ep == target {
			n++
		}
	}
	return n
}

// --- OpenAI channel ---

func TestOpenAI_ChatAndResponses(t *testing.T) {
	eps := GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "gpt-4o")
	if !hasEndpoint(eps, constant.EndpointTypeOpenAI) {
		t.Errorf("OpenAI channel should include chat endpoint, got: %v", eps)
	}
	if !hasEndpoint(eps, constant.EndpointTypeOpenAIResponse) {
		t.Errorf("OpenAI channel should include responses endpoint, got: %v", eps)
	}
}

func TestOpenAI_ResponseOnlyModel_NoDuplicate(t *testing.T) {
	eps := GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "o3-pro")
	if c := countEndpoint(eps, constant.EndpointTypeOpenAIResponse); c != 1 {
		t.Errorf("o3-pro should have exactly 1 openai-response, got %d in: %v", c, eps)
	}
}

// --- Claude model on OpenAI channel ---

func TestOpenAI_ClaudeModel_HasAnthropicEndpoint(t *testing.T) {
	eps := GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "claude-sonnet-4-20250514")
	if !hasEndpoint(eps, constant.EndpointTypeAnthropic) {
		t.Errorf("Claude model on OpenAI channel should include anthropic endpoint, got: %v", eps)
	}
	if !hasEndpoint(eps, constant.EndpointTypeOpenAI) {
		t.Errorf("Claude model on OpenAI channel should still include openai endpoint, got: %v", eps)
	}
}

// --- Gemini model on OpenAI channel ---

func TestOpenAI_GeminiModel_HasGeminiEndpoint(t *testing.T) {
	eps := GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "gemini-2.5-flash")
	if !hasEndpoint(eps, constant.EndpointTypeGemini) {
		t.Errorf("Gemini model on OpenAI channel should include gemini endpoint, got: %v", eps)
	}
	if !hasEndpoint(eps, constant.EndpointTypeOpenAI) {
		t.Errorf("Gemini model on OpenAI channel should still include openai endpoint, got: %v", eps)
	}
}

func TestGeminiImageModelAdvertisesImageGenerationFirst(t *testing.T) {
	eps := GetEndpointTypesByChannelType(constant.ChannelTypeGemini, "gemini-3.1-flash-image")
	if len(eps) == 0 || eps[0] != constant.EndpointTypeImageGeneration {
		t.Fatalf("Gemini image model should advertise image generation first, got: %v", eps)
	}
	if !hasEndpoint(eps, constant.EndpointTypeGemini) || !hasEndpoint(eps, constant.EndpointTypeOpenAI) {
		t.Fatalf("Gemini image model should retain native and OpenAI-compatible endpoints, got: %v", eps)
	}
}

func TestIdeogramGenerateModelAdvertisesImageGenerationFirst(t *testing.T) {
	eps := GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "ideogram_generate_V_2_TURBO")
	if len(eps) == 0 || eps[0] != constant.EndpointTypeImageGeneration {
		t.Fatalf("Ideogram generate model should advertise image generation first, got: %v", eps)
	}
}

func TestOpenAI_VideoGenerationModel_DoesNotAdvertiseVideoEndpoint(t *testing.T) {
	eps := GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "veo-3-fast")
	if hasEndpoint(eps, constant.EndpointTypeVideoGeneration) {
		t.Errorf("video endpoint should not be advertised until provider execution is implemented, got: %v", eps)
	}
}

// --- Anthropic channel (native) - no duplication ---

func TestAnthropic_NoDuplicateEndpoint(t *testing.T) {
	eps := GetEndpointTypesByChannelType(constant.ChannelTypeAnthropic, "claude-sonnet-4-20250514")
	if c := countEndpoint(eps, constant.EndpointTypeAnthropic); c != 1 {
		t.Errorf("Anthropic channel with Claude model should have exactly 1 anthropic endpoint, got %d in: %v", c, eps)
	}
}

// --- Gemini channel (native) - no duplication ---

func TestGemini_NoDuplicateEndpoint(t *testing.T) {
	eps := GetEndpointTypesByChannelType(constant.ChannelTypeGemini, "gemini-2.5-flash")
	if c := countEndpoint(eps, constant.EndpointTypeGemini); c != 1 {
		t.Errorf("Gemini channel with Gemini model should have exactly 1 gemini endpoint, got %d in: %v", c, eps)
	}
}

// --- Non-Claude/Gemini models should NOT get extra endpoints ---

func TestCustomModel_NoExtraEndpoints(t *testing.T) {
	eps := GetEndpointTypesByChannelType(constant.ChannelTypeCustom, "custom-chat")
	if hasEndpoint(eps, constant.EndpointTypeAnthropic) {
		t.Errorf("Custom model should NOT include anthropic endpoint, got: %v", eps)
	}
	if hasEndpoint(eps, constant.EndpointTypeGemini) {
		t.Errorf("Custom model should NOT include gemini endpoint, got: %v", eps)
	}
}
