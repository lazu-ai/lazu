package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
)

// The console playground's Chat request is not parsed, so it reaches a
// Gemini channel without a plan; ConvertOpenAIRequest encodes it through the
// codec, and its reply is served as a planned Chat client's.
func TestUnplannedChatReplyIsServedByTheHub(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() { constant.StreamingTimeout = oldStreamingTimeout }()
	for _, stream := range []bool{true, false} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/pg/chat/completions", nil)
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, IsStream: stream, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-test"}}
		body := fixtures.Raw(t, "gemini/text.sse")
		if !stream {
			body = `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello, nice to meet you"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":6,"totalTokenCount":11}}`
		}
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
		if _, err := (&Adaptor{}).DoResponse(c, resp, info); err != nil {
			t.Fatalf("stream=%v: %v", stream, err)
		}
		got := recorder.Body.String()
		if !strings.Contains(got, "Hello, nice to meet you") || !strings.Contains(got, "chat.completion") {
			t.Fatalf("stream=%v: %s", stream, got)
		}
		if stream != strings.Contains(got, "data: [DONE]") {
			t.Fatalf("stream=%v framing: %s", stream, got)
		}
	}
}
