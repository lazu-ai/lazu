package errmap

import (
	"net/http"
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
)

func TestMapOpenAI(t *testing.T) {
	t.Parallel()
	got, _ := MapOpenAI(http.StatusTooManyRequests, nil)
	if got != types.ErrorCodeUpstreamRateLimited {
		t.Fatalf("got %q", got)
	}
}
