package channel

import "testing"

func TestFilterAnthropicBeta(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"single allowed", "interleaved-thinking-2025-05-14", "interleaved-thinking-2025-05-14"},
		{"single blocked", "adaptive-thinking-2025-11-24", ""},
		{"mixed", "interleaved-thinking-2025-05-14,adaptive-thinking-2025-11-24,token-efficient-tools-2025-02-19", "interleaved-thinking-2025-05-14,token-efficient-tools-2025-02-19"},
		{"all blocked", "adaptive-thinking-2025-11-24", ""},
		{"with spaces", " interleaved-thinking-2025-05-14 , adaptive-thinking-2025-11-24 ", "interleaved-thinking-2025-05-14"},
		{"multiple allowed", "interleaved-thinking-2025-05-14,token-efficient-tools-2025-02-19,output-128k-2025-02-19", "interleaved-thinking-2025-05-14,token-efficient-tools-2025-02-19,output-128k-2025-02-19"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterAnthropicBeta(tt.input)
			if got != tt.want {
				t.Errorf("FilterAnthropicBeta(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
