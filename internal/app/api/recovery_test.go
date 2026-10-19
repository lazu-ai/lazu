package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

func TestRecoveryPreservesErrorProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/api/user/self", "/v1/messages", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			r := gin.New()
			r.Use(gin.CustomRecovery(recoverPanic))
			r.GET(path, func(c *gin.Context) { c.Set(common.RequestIdKey, "req_panic"); panic("private database detail") })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != 500 || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("unexpected response: %d %s", w.Code, w.Body)
			}
			e := body["error"].(map[string]any)
			if e["code"] != "internal_error" || w.Header().Get("X-Lazu-Error") != "LZ-9001" {
				t.Fatalf("missing identity: %v", body)
			}
			if path == "/v1/messages" {
				if body["type"] != "error" || body["request_id"] != "req_panic" || e["type"] != "api_error" {
					t.Fatalf("invalid Anthropic envelope: %v", body)
				}
			} else if e["request_id"] != "req_panic" {
				t.Fatalf("missing request ID: %v", body)
			}
			if strings.HasPrefix(path, "/api/") && body["success"] != false {
				t.Fatalf("invalid console envelope: %v", body)
			}
		})
	}
}
