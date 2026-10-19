package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

func TestLoginLocksAccountAfterRepeatedFailures(t *testing.T) {
	common.ResetAttempts()
	t.Cleanup(common.ResetAttempts)
	gin.SetMode(gin.TestMode)

	for i := 0; i < loginFailureLimit; i++ {
		recordLoginFailure("Victim@Example.com")
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if !loginLocked(c, "victim@example.com") {
		t.Fatal("expected the account to be locked after the failure limit, whatever the case")
	}
	clearLoginFailures("victim@example.com")
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	if loginLocked(c, "victim@example.com") {
		t.Fatal("expected a successful login to clear the lock")
	}
}

func TestMailRecipientCapHoldsAcrossCallers(t *testing.T) {
	common.ResetAttempts()
	t.Cleanup(common.ResetAttempts)
	gin.SetMode(gin.TestMode)

	send := func() int {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		if allowMailTo(c, "target@example.com") {
			return http.StatusOK
		}
		if recorder.Header().Get("Retry-After") == "" {
			t.Fatal("expected Retry-After on a refused send")
		}
		return recorder.Code
	}
	if got := send(); got != http.StatusOK {
		t.Fatalf("first mail should go out, got %d", got)
	}
	if got := send(); got != http.StatusTooManyRequests {
		t.Fatalf("a second mail within the minute should be refused, got %d", got)
	}
}
