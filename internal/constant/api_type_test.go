package constant_test

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/packages/domain/providercap"
)

func TestAPITypeSupportsResponsesAlignedWithProviderProfile(t *testing.T) {
	for apiType := 0; apiType < constant.APITypeDummy; apiType++ {
		got := constant.APITypeSupportsResponses(apiType)
		want := providercap.GetProfile(apiType).SupportsResponses
		if got != want {
			t.Errorf(
				"APITypeSupportsResponses mismatch for apiType=%d: got %v, want %v",
				apiType,
				got,
				want,
			)
		}
	}
}
