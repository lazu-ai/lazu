package common

import "strings"

var (
	// OpenAIResponseOnlyModels is a list of models that are only available for OpenAI responses.
	OpenAIResponseOnlyModels = []string{
		"o3-pro",
		"o3-deep-research",
		"o4-mini-deep-research",
	}
	ImageGenerationModels = []string{
		"prefix:imagen-",
		"prefix:gpt-image-",
		"prefix:ideogram_generate_",
		"gemini-nano-banana-2",
		"flux-",
		"flux.1-",
	}
	VideoGenerationModels = []string{
		"prefix:veo",
		"prefix:wan",
		"sora",
		"kling",
		"-video",
		"video-generation",
		"video_generation",
	}
)

func IsOpenAIResponseOnlyModel(modelName string) bool {
	for _, m := range OpenAIResponseOnlyModels {
		if strings.Contains(modelName, m) {
			return true
		}
	}
	return false
}

// ChatToolsNeedReasoningOff reports whether a model refuses function tools on
// Chat Completions while it reasons and accepts reasoning_effort "none" as the
// way out. gpt-6 answers such a request with a 400 naming two exits —
// /v1/responses, or effort "none" — on every upstream, official included.
// gpt-6-astra takes only low through max, so for it Responses is the only exit
// and the request is left to fail with that instruction.
func ChatToolsNeedReasoningOff(modelName string) bool {
	name := strings.ToLower(strings.TrimSpace(modelName))
	return strings.HasPrefix(name, "gpt-6") && !strings.Contains(name, "astra")
}

// IsClaudeModel 判断模型名称是否为 Claude 系列模型。
func IsClaudeModel(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "claude")
}

// IsGeminiModel 判断模型名称是否为 Gemini 系列模型。
func IsGeminiModel(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "gemini")
}

func IsImageGenerationModel(modelName string) bool {
	modelName = strings.ToLower(modelName)
	if strings.HasPrefix(modelName, "gemini-") &&
		(strings.HasSuffix(modelName, "-image") || strings.Contains(modelName, "-image-generation")) {
		return true
	}
	for _, m := range ImageGenerationModels {
		if strings.Contains(modelName, m) {
			return true
		}
		if strings.HasPrefix(m, "prefix:") && strings.HasPrefix(modelName, strings.TrimPrefix(m, "prefix:")) {
			return true
		}
	}
	return false
}

func IsVideoGenerationModel(modelName string) bool {
	modelName = strings.ToLower(modelName)
	for _, m := range VideoGenerationModels {
		if strings.Contains(modelName, m) {
			return true
		}
		if strings.HasPrefix(m, "prefix:") && strings.HasPrefix(modelName, strings.TrimPrefix(m, "prefix:")) {
			return true
		}
	}
	return false
}
