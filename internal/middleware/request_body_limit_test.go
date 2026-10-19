package middleware

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestRelayRequestBodyLimitRejectsOversizedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	called := false
	router := gin.New()
	router.Use(relayRequestBodyLimit(8, 16))
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		called = true
		c.Status(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("123456789"))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", recorder.Code)
	}
	if called {
		t.Fatal("handler should not be called for an oversized request")
	}
	if !strings.Contains(recorder.Body.String(), "request_body_too_large") {
		t.Fatalf("expected canonical request_body_too_large error, got %s", recorder.Body.String())
	}
}

func TestRelayBodyLimitAcrossReadersAndCompression(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/images/generations", "/v1/messages", "/v1/videos"} {
		for _, mode := range []string{"known", "unknown", "gzip"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				payload := []byte(`{"model":"m","input":"` + strings.Repeat("x", 256) + `"}`)
				var encoded bytes.Buffer
				if mode == "gzip" {
					writer := gzip.NewWriter(&encoded)
					if _, err := writer.Write(payload); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					payload = encoded.Bytes()
				}
				called := false
				router := gin.New()
				router.Use(func(c *gin.Context) { c.Set(common.RequestIdKey, "req_limit"); c.Next() }, DecompressRequestMiddleware(), relayRequestBodyLimit(128, 256))
				switch path {
				case "/v1/chat/completions":
					router.Use(TextgenParse(textgen.ProtocolOpenAIChat))
				case "/v1/responses":
					router.Use(TextgenParse(textgen.ProtocolOpenAIResponses))
				default:
					router.Use(func(c *gin.Context) {
						_, _, err := getModelRequest(c)
						if err != nil {
							abortRequestBodyError(c, err)
							return
						}
						c.Next()
					})
				}
				router.POST(path, func(c *gin.Context) { called = true; c.Status(200) })
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
				req.Header.Set("Content-Type", "application/json")
				if mode == "unknown" {
					req.ContentLength = -1
				}
				if mode == "gzip" {
					req.Header.Set("Content-Encoding", "gzip")
				}
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, req)
				var body struct {
					Type      string
					RequestID string `json:"request_id"`
					Error     struct {
						Code, Type string
						RequestID  string `json:"request_id"`
					}
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				wantType, requestID := "invalid_request_error", body.Error.RequestID
				if path == "/v1/messages" {
					wantType, requestID = "request_too_large", body.RequestID
					if body.Type != "error" {
						t.Fatalf("missing Anthropic envelope: %s", recorder.Body)
					}
				}
				if called || recorder.Code != 413 || body.Error.Code != "request_body_too_large" || body.Error.Type != wantType || requestID != "req_limit" {
					t.Fatalf("inconsistent body limit: %d %s called=%v", recorder.Code, recorder.Body, called)
				}
			})
		}
	}
}

func TestBodyReadErrorSurvivesRepeatedReads(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("123456789"))
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8)
	_, first := common.GetRequestBody(c)
	_, second := common.GetRequestBody(c)
	if first == nil || second != first {
		t.Fatalf("body error lost on reread: %v / %v", first, second)
	}
}

func TestMultipartLimitDoesNotBecomeMissingModel(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/images/edits"} {
		t.Run(path, func(t *testing.T) {
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			if err := form.WriteField("model", "m"); err != nil {
				t.Fatal(err)
			}
			file, err := form.CreateFormFile("file", "fixture.txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte(strings.Repeat("x", 1024))); err != nil {
				t.Fatal(err)
			}
			if err := form.Close(); err != nil {
				t.Fatal(err)
			}
			router := gin.New()
			router.Use(relayRequestBodyLimit(128, 512))
			router.POST(path, func(c *gin.Context) {
				_, _, err := getModelRequest(c)
				if err != nil {
					abortRequestBodyError(c, err)
					return
				}
				c.Status(200)
			})
			req := httptest.NewRequest(http.MethodPost, path, &body)
			req.ContentLength = -1
			req.Header.Set("Content-Type", form.FormDataContentType())
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			if req.MultipartForm != nil {
				_ = req.MultipartForm.RemoveAll()
			}
			if recorder.Code != 413 || !strings.Contains(recorder.Body.String(), "request_body_too_large") {
				t.Fatalf("multipart limit was swallowed: %d %s", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestMultipartRoutingPreservesModelAndFileForAdaptor(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("model", "whisper-1"); err != nil {
		t.Fatal(err)
	}
	file, err := form.CreateFormFile("file", "audio.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("audio fixture")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(relayRequestBodyLimit(128, 2048))
	router.POST("/v1/audio/transcriptions", func(c *gin.Context) {
		request, _, err := getModelRequest(c)
		if err != nil || request.Model != "whisper-1" {
			t.Errorf("model unavailable after routing: %v %+v", err, request)
			c.Status(500)
			return
		}
		file, _, err := c.Request.FormFile("file")
		if err != nil {
			t.Error(err)
			c.Status(500)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil || string(data) != "audio fixture" {
			t.Errorf("adaptor lost upload: %s %v", data, err)
			c.Status(500)
			return
		}
		c.Status(200)
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	req.ContentLength = -1
	req.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if req.MultipartForm != nil {
		_ = req.MultipartForm.RemoveAll()
	}
	if w.Code != 200 {
		t.Fatalf("valid upload failed: %d", w.Code)
	}
}

func TestDefaultRelayRequestBodyLimitIs64MiB(t *testing.T) {
	const want = int64(64 << 20)
	if DefaultRelayRequestBodyLimitBytes != want {
		t.Fatalf("default relay request body limit = %d, want %d", DefaultRelayRequestBodyLimitBytes, want)
	}
}

func TestRelayRequestBodyLimitUsesMultipartLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(relayRequestBodyLimit(8, 16))
	router.POST("/v1/files", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/files", strings.NewReader("123456789"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected multipart request to use larger limit, got %d", recorder.Code)
	}
}
