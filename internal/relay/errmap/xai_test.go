package errmap

import (
	"net/http"
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
)

func TestMapXAI(t *testing.T) {
	t.Parallel()
	got, _ := MapXAI(http.StatusBadGateway, []byte(`context length exceeded`))
	if got != types.ErrorCodeContextLengthExceeded {
		t.Fatalf("got %q", got)
	}
}
