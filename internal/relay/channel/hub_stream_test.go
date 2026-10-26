package channel

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	"github.com/lazu-ai/lazu/internal/types"
)

// Only the client going away is a client cancellation; a deadline stays the
// scanner's to classify as a timeout, with its error row.
func TestClientCanceledIsOnlyACancel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest("POST", "/", nil).WithContext(canceled)
	if err := clientCanceled(c); err == nil || err.GetErrorCode() != types.ErrorCodeClientCanceled {
		t.Fatalf("a canceled request is a client cancellation: %v", err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	c.Request = httptest.NewRequest("POST", "/", nil).WithContext(expired)
	if err := clientCanceled(c); err != nil {
		t.Fatalf("a deadline is not a client cancellation: %v", err)
	}
}

// A channel's thinking_to_content is kept on the hub's Chat writer: the
// reasoning is written as content inside <think> tags, as the native Chat
// path writes it, for clients that read only content.
func TestChatHubClientWritesThinkingAsContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	client := &chatHubClient{emitter: openai_chat.NewStreamEmitter("resp_1", "m"), thinkToContent: true}
	if _, err := client.Emit(c, []textgen.Event{
		{Kind: textgen.EventReasoningStart},
		{Kind: textgen.EventReasoning, Text: "weighing it"},
		{Kind: textgen.EventText, Text: "Answer."},
		{Kind: textgen.EventStop, Stop: textgen.StopEnd},
	}); err != nil {
		t.Fatal(err)
	}
	got := recorder.Body.String()
	if strings.Contains(got, "reasoning_content") || !strings.Contains(got, `\u003cthink\u003e\nweighing it`) || !strings.Contains(got, `\n\u003c/think\u003e\nAnswer.`) {
		t.Fatalf("thinking as content: %s", got)
	}
}
