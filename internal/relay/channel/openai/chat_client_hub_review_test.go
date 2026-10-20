package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

func runChatClientHub(t *testing.T, writer http.ResponseWriter, frames ...string) (*gin.Context, *relaycommon.RelayInfo, *dto.Usage, *types.LazuError) {
	t.Helper()
	service.InitTokenEncoders()
	disableTerminalModelCapture(t)
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := terminalResponsesInfo(true)
	var body strings.Builder
	for _, frame := range frames {
		body.WriteString("data: " + frame + "\n\n")
	}
	usage, err := chatClientResponsesStream(c, info, &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body.String()))})
	return c, info, usage, err
}

// Without provider usage a call is billed as its name, its arguments and
// the framing every call carries.
func TestChatClientHubEstimatesToolCallsWithTheirFraming(t *testing.T) {
	arguments := `{"city":"Taipei"}`
	_, info, usage, err := runChatClientHub(t, httptest.NewRecorder(),
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","status":"completed","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Taipei\"}"}}`,
		`{"type":"response.completed","response":{"status":"completed"}}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := service.CountTextToken("get_weather"+arguments, info.UpstreamModelName) + 7
	if usage.CompletionTokens != want {
		t.Fatalf("completion tokens = %d, want %d", usage.CompletionTokens, want)
	}
}

// Metadata then a cut-off: nothing reached the client, so the attempt is
// retryable and refunded.
func TestChatClientHubCutOffBeforeOutputIsRetryable(t *testing.T) {
	w := httptest.NewRecorder()
	c, info, _, err := runChatClientHub(t, w, `{"type":"response.created","response":{"id":"resp_1","model":"gpt-4o-mini"}}`)
	if err == nil || types.IsSkipRetryError(err) {
		t.Fatalf("a cut-off before output must be a retryable error: %v", err)
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyStreamFirstEventFailed) || !info.StreamFailedWithoutUsage {
		t.Fatal("a cut-off before output must be marked as a first-event failure and refunded")
	}
	if w.Body.Len() != 0 {
		t.Fatalf("wrote to the client: %s", w.Body)
	}
}

// A failure before output that still reported usage is absorbed by the
// gateway: retryable and refunded.
func TestChatClientHubHeldFailureWithUsageKeepsTheRefund(t *testing.T) {
	_, info, _, err := runChatClientHub(t, httptest.NewRecorder(),
		`{"type":"response.failed","response":{"status":"failed","usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15},"error":{"type":"server_error","message":"overloaded"}}}`,
	)
	if err == nil || types.IsSkipRetryError(err) {
		t.Fatalf("a failure before output must be retryable: %v", err)
	}
	if !info.StreamFailedWithoutUsage {
		t.Fatal("the refund signal was cleared by the failure's usage")
	}
}
