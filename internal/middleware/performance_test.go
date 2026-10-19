package middleware

import (
	"errors"
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
)

func TestLocalOverloadDoesNotBlameOrRetryAnUpstream(t *testing.T) {
	err := newSystemOverloadedError(errors.New("CPU capacity reached"))
	if err.GetErrorCode() != types.ErrorCodeGatewayOverloaded || err.StatusCode != 503 || !types.IsSkipRetryError(err) {
		t.Fatalf("local overload classification is wrong: %+v", err)
	}
	spec := types.Catalog[err.GetErrorCode()]
	if !spec.Retry || spec.FallbackCat != types.FallbackNone {
		t.Fatalf("client retry and internal failover conflated: %+v", spec)
	}
}
