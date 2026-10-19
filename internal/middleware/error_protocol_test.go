package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestEarlyGatewayFailuresPreserveProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens", "/v1/chat/completions"} {
		for _, code := range []types.ErrorCode{types.ErrorCodeInvalidAPIKey, types.ErrorCodeRequestBodyTooLarge, types.ErrorCodeTokenModelDenied, types.ErrorCodeChannelInvalidKey} {
			t.Run(path+"/"+string(code), func(t *testing.T) {
				r := gin.New()
				r.POST(path, func(c *gin.Context) {
					c.Set(common.RequestIdKey, "req_early")
					abortWithCode(c, code, types.D{"model": "example-model", "reason": "private diagnostic"})
				}, func(c *gin.Context) { t.Error("aborted request reached handler") })
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				spec := types.RenderWire(code, nil, 0)
				e := body["error"].(map[string]any)
				if w.Code != spec.Status || e["code"] != string(spec.Code) || w.Header().Get("X-Lazu-Error") != spec.ID {
					t.Fatalf("identity lost: %d %v", w.Code, body)
				}
				if strings.HasPrefix(path, "/v1/messages") {
					if body["type"] != "error" || body["request_id"] != "req_early" || e["type"] != types.ClaudeErrorType(spec.Type, spec.Status) {
						t.Fatalf("invalid Anthropic response: %v", body)
					}
				} else if body["type"] != nil || e["request_id"] != "req_early" {
					t.Fatalf("invalid OpenAI response: %v", body)
				}
				if spec.Audience != types.AudienceCustomer && e["details"] != nil {
					t.Fatalf("private details leaked: %v", e)
				}
			})
		}
	}
}

func TestCORSMakesErrorIdentityReadable(t *testing.T) {
	t.Setenv("CORS_ALLOW_ORIGINS", "https://client.example")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS())
	r.GET("/v1/models", func(c *gin.Context) { abortWithCode(c, types.ErrorCodeInvalidAPIKey) })
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Origin", "https://client.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if !strings.Contains(strings.ToLower(w.Header().Get("Access-Control-Expose-Headers")), "x-lazu-error") {
		t.Fatalf("error header not exposed: %v", w.Header())
	}
}
