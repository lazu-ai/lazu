package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestGeminiClientParseTreatsGenerateContentAltSSEAsStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(GeminiClientParse())
	var parsed *textgen.ParsedRequest
	router.POST("/v1beta/models/:action", func(c *gin.Context) {
		parsed, _ = common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed)
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-test:generateContent?alt=sse", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || parsed == nil || !parsed.Semantic.Lifecycle.Stream {
		t.Fatalf("status=%d parsed=%+v", response.Code, parsed)
	}
}
