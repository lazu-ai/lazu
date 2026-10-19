package dto

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGeminiChatRequestIsStreamFromPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1beta/models/gemini-3-flash-preview:streamGenerateContent", nil)

	req := GeminiChatRequest{}
	if !req.IsStream(c) {
		t.Fatalf("expected stream detection from request path")
	}
}
