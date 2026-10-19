package model_setting

import "testing"

func TestDefaultGeminiImageModelsIncludeCurrentProductionModels(t *testing.T) {
	models := []string{
		"gemini-2.5-flash-image",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-lite-image",
	}
	for _, model := range models {
		if !IsGeminiModelSupportImagine(model) {
			t.Errorf("expected %q to support image generation", model)
		}
	}
}
