package channel

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/types"
)

// Only the client going away is a client cancellation; a deadline stays the
// scanner's to classify as a timeout, with its error row.
func TestClientCanceledIsOnlyACancel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest("POST", "/", nil).WithContext(canceled)
	if err := clientCanceled(c); err == nil || err.GetErrorCode() != types.ErrorCodeClientCanceled {
		t.Fatalf("a canceled request is a client cancellation: %v", err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	c.Request = httptest.NewRequest("POST", "/", nil).WithContext(expired)
	if err := clientCanceled(c); err != nil {
		t.Fatalf("a deadline is not a client cancellation: %v", err)
	}
}
