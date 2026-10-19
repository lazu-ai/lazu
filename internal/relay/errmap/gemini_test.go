package errmap

import (
	"net/http"
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
)

func TestMapGemini(t *testing.T) {
	t.Parallel()
	got, _ := MapGemini(http.StatusBadRequest, []byte(`{"message":"safety blocked"}`))
	if got != types.ErrorCodePromptBlocked {
		t.Fatalf("got %q", got)
	}
}
