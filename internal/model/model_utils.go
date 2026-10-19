package model

import "strings"

// OpenSourcePrefixes are model name prefixes that indicate open-source models.
var OpenSourcePrefixes = []string{
	"deepseek", "qwen", "llama", "mistral", "gemma", "phi-",
	"yi-", "glm", "baichuan", "internlm", "mixtral", "codestral",
	"command-r", "dbrx", "falcon", "mpt-", "vicuna", "wizardlm",
}

// ModalityResult holds derived input/output modality lists.
type ModalityResult struct {
	Input  []string
	Output []string
}

// IsOpenSourceModel checks whether a model is open-source by matching its
// lowercased name against known prefixes and scanning tags for "开源" / "open-source".
func IsOpenSourceModel(modelName string, tags string) bool {
	modelLower := strings.ToLower(modelName)
	for _, prefix := range OpenSourcePrefixes {
		if strings.HasPrefix(modelLower, prefix) {
			return true
		}
	}
	if tags != "" {
		for _, t := range strings.Split(tags, ",") {
			tl := strings.ToLower(strings.TrimSpace(t))
			if strings.Contains(tl, "开源") || strings.Contains(tl, "open-source") || strings.Contains(tl, "open source") {
				return true
			}
		}
	}
	return false
}

// DeriveModality infers input/output modality lists from a comma-separated
// tag string. It always includes "text" as the base modality and appends
// additional modalities based on tag keywords.
func DeriveModality(tags string) ModalityResult {
	inputMods := []string{"text"}
	outputMods := []string{"text"}
	if tags == "" {
		return ModalityResult{Input: inputMods, Output: outputMods}
	}
	for _, t := range strings.Split(tags, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		tl := strings.ToLower(t)
		isImageGenerationTag := strings.Contains(tl, "图片生成") || strings.Contains(tl, "image generation")
		isVideoGenerationTag := strings.Contains(tl, "视频生成") || strings.Contains(tl, "video generation")
		isAudioGenerationTag := strings.Contains(tl, "音频生成") || strings.Contains(tl, "audio generation")

		if strings.Contains(tl, "图片理解") || strings.Contains(tl, "多模态") || strings.Contains(tl, "vision") {
			if !SliceContains(inputMods, "image") {
				inputMods = append(inputMods, "image")
			}
		}
		if isImageGenerationTag {
			if !SliceContains(outputMods, "image") {
				outputMods = append(outputMods, "image")
			}
		}
		if isVideoGenerationTag {
			if !SliceContains(outputMods, "video") {
				outputMods = append(outputMods, "video")
			}
		}
		if isAudioGenerationTag {
			if !SliceContains(outputMods, "audio") {
				outputMods = append(outputMods, "audio")
			}
		}
		if !isAudioGenerationTag && (strings.Contains(tl, "音频") || strings.Contains(tl, "audio")) {
			if !SliceContains(inputMods, "audio") {
				inputMods = append(inputMods, "audio")
			}
		}
		if !isVideoGenerationTag && (strings.Contains(tl, "视频") || strings.Contains(tl, "video")) {
			if !SliceContains(inputMods, "video") {
				inputMods = append(inputMods, "video")
			}
		}
		if strings.Contains(tl, "文件理解") || strings.Contains(tl, "file") {
			if !SliceContains(inputMods, "file") {
				inputMods = append(inputMods, "file")
			}
		}
	}
	return ModalityResult{Input: inputMods, Output: outputMods}
}

// SliceContains checks whether a string slice contains the given value.
func SliceContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
