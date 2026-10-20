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
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
)

// Claude clients are parsed for the hub unless the operator switched them
// back to the legacy conversion, which then sees no parsed request at all.
func TestClaudeClientParseHonoursTheLegacySwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := model_setting.GetGlobalSettings()
	previous := settings.LegacyClaudeClientConversion
	t.Cleanup(func() { settings.LegacyClaudeClientConversion = previous })
	body := []byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`)
	for _, legacy := range []bool{false, true} {
		settings.LegacyClaudeClientConversion = legacy
		var parsed *textgen.ParsedRequest
		router := gin.New()
		router.POST("/v1/messages", ClaudeClientParse(), func(c *gin.Context) {
			parsed, _ = common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed)
			c.Status(http.StatusNoContent)
		})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)))
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("legacy=%t: status %d %s", legacy, recorder.Code, recorder.Body.String())
		}
		if legacy != (parsed == nil) {
			t.Fatalf("legacy=%t: parsed=%v", legacy, parsed)
		}
		if !legacy && parsed.Client.Family != textgen.ProtocolAnthropic {
			t.Fatalf("parsed as %+v", parsed.Client)
		}
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
