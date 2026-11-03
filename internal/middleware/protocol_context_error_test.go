package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// Routing that finds every candidate's context too small answers the way
// Anthropic does, which Claude Code compacts on.
func TestAbortProtocolUnsupportedContextOverflow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	abortProtocolUnsupported(c, &textgen.UnsupportedFeatureError{Feature: textgen.FeatureInputText, Path: "input", Tokens: 205000, Limit: 200000})
	var body struct {
		Error struct{ Type, Message, Code string } `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusBadRequest ||
		body.Error.Code != "context_length_exceeded" || body.Error.Type != "invalid_request_error" ||
		body.Error.Message != "prompt is too long: 205000 tokens > 200000 maximum" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
