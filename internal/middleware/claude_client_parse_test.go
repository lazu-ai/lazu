package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
)

// Claude clients on /v1/messages are always parsed for the hub.
func TestClaudeClientParseParsesForTheHub(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`)
	var parsed *textgen.ParsedRequest
	router := gin.New()
	router.POST("/v1/messages", ClaudeClientParse(), func(c *gin.Context) {
		parsed, _ = common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed)
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status %d %s", recorder.Code, recorder.Body.String())
	}
	if parsed == nil || parsed.Client.Family != textgen.ProtocolAnthropic {
		t.Fatalf("parsed = %+v", parsed)
	}
}

// Claude Code's session (inside metadata.user_id) reads the same from the
// parsed request as it did from the body before /v1/messages was parsed:
// stickiness and per-session budgets hang on it.
func TestClaudeCodeSessionSurvivesTheParse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := fixtures.ClientRequest(t, "claude_code/tool_image_turn.json")
	legacy := claudeCodeMetadataSession(body)
	if legacy == "" {
		t.Fatal("the fixture carries no session")
	}
	parsed, err := textgenruntime.Parse(textgen.ProtocolAnthropic, body)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	common.SetContextKey(c, constant.ContextKeyTextgenParsed, parsed)
	if got := claudeCodeMetadataSessionFromContext(c, body); got != legacy {
		t.Fatalf("session from the parsed request = %q, from the body %q", got, legacy)
	}
}
