package relay

import (
	"reflect"
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/relay/channel/openai"
	"github.com/lazu-ai/lazu/internal/relay/channel/xai"
)

func TestGetAdaptor_ReturnsExpectedAliases(t *testing.T) {
	openAIType := reflect.TypeOf(&openai.Adaptor{})
	xaiType := reflect.TypeOf(&xai.Adaptor{})

	tests := []struct {
		name    string
		apiType int
		wantNil bool
		wantTyp reflect.Type
	}{
		{
			name:    "openai",
			apiType: constant.APITypeOpenAI,
			wantTyp: openAIType,
		},
		{
			name:    "xai",
			apiType: constant.APITypeXai,
			wantTyp: xaiType,
		},
		{
			name:    "unknown type",
			apiType: -999,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetAdaptor(tt.apiType)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil adaptor for apiType %d, got %T", tt.apiType, got)
				}
				return
			}

			if got == nil {
				t.Fatalf("expected adaptor for apiType %d, got nil", tt.apiType)
			}
			if gotType := reflect.TypeOf(got); gotType != tt.wantTyp {
				t.Fatalf("unexpected adaptor type for apiType %d, want %v got %v", tt.apiType, tt.wantTyp, gotType)
			}
		})
	}
}

func TestGetAdaptor_FallsBackToFamilyAdaptorWhenProviderFactoryMissing(t *testing.T) {
	openAIFactory, hadOpenAIFactory := adaptorFactories[constant.APITypeOpenAI]
	delete(adaptorFactories, constant.APITypeOpenAI)
	defer func() {
		if hadOpenAIFactory {
			adaptorFactories[constant.APITypeOpenAI] = openAIFactory
		}
	}()

	got := GetAdaptor(constant.APITypeOpenAI)
	if got == nil {
		t.Fatalf("expected family fallback adaptor for openai apiType")
	}
	if gotType := reflect.TypeOf(got); gotType != reflect.TypeOf(&openai.Adaptor{}) {
		t.Fatalf("unexpected fallback adaptor type, got %v", gotType)
	}
}
