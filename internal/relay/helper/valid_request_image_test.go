package helper

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
)

func TestGetAndValidOpenAIImageRequestRejectsUnsupportedStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/images/generations",
		strings.NewReader(`{"model":"gpt-image-2","prompt":"test","stream":true}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")

	_, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesGenerations)
	if err == nil {
		t.Fatal("expected streaming image request to be rejected")
	}
	if !strings.Contains(err.Error(), "not supported") || !strings.Contains(err.Error(), "600 seconds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetAndValidOpenAIImageRequestRejectsMultipartEditStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{
		"model":  "gpt-image-2",
		"prompt": "test",
		"stream": "true",
	} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write multipart field %q: %v", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	_, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
	if err == nil {
		t.Fatal("expected multipart streaming image edit to be rejected")
	}
	if !strings.Contains(err.Error(), "not supported") || !strings.Contains(err.Error(), "600 seconds") {
		t.Fatalf("unexpected error: %v", err)
	}
}
