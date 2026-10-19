package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestHandleStreamFormatWritesGeminiChunk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatGemini,
		PromptTokens: 2,
	}

	data := `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`
	if err := HandleStreamFormat(c, info, data, false, false); err != nil {
		t.Fatalf("HandleStreamFormat failed: %v", err)
	}

	body := recorder.Body.String()
	if got := strings.Count(body, "data: "); got != 1 || !strings.Contains(body, "candidates") || !strings.Contains(body, "hello") {
		t.Fatalf("expected one Gemini stream chunk carrying the delta, got %d frames: %q", got, body)
	}
}

func TestHandleFinalResponseWritesGeminiChunk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatGemini,
		PromptTokens: 2,
	}

	lastStreamData := `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	HandleFinalResponse(c, info, lastStreamData, "", 0, "", "", nil, false)

	body := recorder.Body.String()
	if got := strings.Count(body, "data: "); got != 1 || !strings.Contains(body, "candidates") {
		t.Fatalf("expected one final Gemini stream chunk, got %d frames: %q", got, body)
	}
}
