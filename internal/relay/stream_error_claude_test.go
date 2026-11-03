package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestWriteClaudeErrorAfterPing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	err := types.NewErrorWithStatusCode(nil, types.ErrorCodeContextLengthExceeded, http.StatusBadRequest)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	WriteClaudeError(c, http.StatusBadRequest, err)
	if w.Code != http.StatusBadRequest || !strings.HasPrefix(w.Body.String(), `{"error"`) {
		t.Fatalf("before stream: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Header("Content-Type", "text/event-stream")
	c.Status(http.StatusOK)
	_, _ = c.Writer.WriteString(": PING\n\n")
	WriteClaudeError(c, http.StatusBadRequest, err)
	body := w.Body.String()
	frame, ok := strings.CutPrefix(body, ": PING\n\nevent: error\ndata: ")
	if !ok || !strings.HasSuffix(frame, "\n\n") {
		t.Fatalf("after ping: %q", body)
	}
	var event struct {
		Type  string `json:"type"`
		Error struct{ Type, Message string }
	}
	if json.Unmarshal([]byte(strings.TrimSpace(frame)), &event) != nil || event.Type != "error" ||
		event.Error.Type != types.ErrorTypeInvalidRequest || !strings.HasPrefix(event.Error.Message, "prompt is too long") {
		t.Fatalf("frame: %q", frame)
	}
}
