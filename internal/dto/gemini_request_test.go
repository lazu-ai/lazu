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

func TestGeminiChatRequestIsStreamFromAltSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1beta/models/gemini-3-flash-preview:generateContent?alt=sse", nil)

	req := GeminiChatRequest{}
	if !req.IsStream(c) || !IsGeminiStreamRequest(c) {
		t.Fatal("expected stream detection from alt=sse")
	}
}
