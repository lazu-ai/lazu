package service

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestValidUsageAcceptsCanonicalAndProviderAliases(t *testing.T) {
	tests := []struct {
		name  string
		usage *dto.Usage
		want  bool
	}{
		{name: "nil", usage: nil, want: false},
		{name: "empty", usage: &dto.Usage{}, want: false},
		{name: "canonical prompt", usage: &dto.Usage{PromptTokens: 1}, want: true},
		{name: "canonical completion", usage: &dto.Usage{CompletionTokens: 1}, want: true},
		{name: "responses input alias", usage: &dto.Usage{InputTokens: 1}, want: true},
		{name: "responses output alias", usage: &dto.Usage{OutputTokens: 1}, want: true},
		{name: "total only is insufficient", usage: &dto.Usage{TotalTokens: 1}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidUsage(tc.usage); got != tc.want {
				t.Fatalf("ValidUsage() = %v, want %v for %+v", got, tc.want, tc.usage)
			}
		})
	}
}
