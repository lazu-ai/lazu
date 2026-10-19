package common

import "testing"

func TestIsImageGenerationModelCurrentFamilies(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-image-1.5", want: true},
		{model: "gpt-image-2", want: true},
		{model: "gemini-2.5-flash-image", want: true},
		{model: "gemini-3.1-flash-image", want: true},
		{model: "gemini-3.1-flash-lite-image", want: true},
		{model: "gemini-2.0-flash-exp-image-generation", want: true},
		{model: "gemini-nano-banana-2", want: true},
		{model: "imagen-4.0-generate-001", want: true},
		{model: "ideogram_generate_V_2_TURBO", want: true},
		{model: "ideogram_remix_V_2", want: false},
		{model: "ideogram_describe", want: false},
		{model: "gemini-2.5-flash", want: false},
		{model: "gpt-5.4", want: false},
	}

	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			if got := IsImageGenerationModel(test.model); got != test.want {
				t.Fatalf("IsImageGenerationModel(%q)=%t want %t", test.model, got, test.want)
			}
		})
	}
}
