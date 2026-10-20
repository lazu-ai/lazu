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

// A reply that ended well but said nothing and reported no usage still
// consumed its prompt.
func TestChatClientHubEstimatesThePromptOfAnEmptyReply(t *testing.T) {
	_, _, usage, err := runChatClientHub(t, httptest.NewRecorder(), `{"type":"response.completed","response":{"status":"completed"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.PromptTokens == 0 {
		t.Fatalf("the prompt of an empty reply must be estimated: %+v", usage)
	}
}

// include_usage gets its own chunk after the finish: no choices, Chat's
// usage shape.
func TestChatClientHubWritesAUsageOnlyChunk(t *testing.T) {
	w := httptest.NewRecorder()
	_, _, _, err := runChatClientHub(t, w,
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15}}}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	var data []string
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
			data = append(data, strings.TrimPrefix(line, "data: "))
		}
	}
	finish, usage := data[len(data)-2], data[len(data)-1]
	if !strings.Contains(finish, `"finish_reason":"stop"`) || strings.Contains(finish, `"prompt_tokens"`) {
		t.Fatalf("finish chunk: %s", finish)
	}
	if !strings.Contains(usage, `"choices":[]`) || !strings.Contains(usage, `"prompt_tokens":12`) || strings.Contains(usage, `"input_tokens"`) {
		t.Fatalf("usage chunk: %s", usage)
	}
}

// A stream cut off while a call was held still bills the call: its name,
// arguments and framing.
func TestChatClientHubBillsAHeldCallOnACutOff(t *testing.T) {
	_, info, usage, err := runChatClientHub(t, httptest.NewRecorder(),
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"city\":\"Taipei\"}"}`,
	)
	if err == nil {
		t.Fatal("a cut-off is an error")
	}
	want := service.CountTextToken("hello"+"get_weather"+`{"city":"Taipei"}`, info.UpstreamModelName) + 7
	if usage.CompletionTokens != want {
		t.Fatalf("completion tokens = %d, want %d", usage.CompletionTokens, want)
	}
}

// A provider report with the output count but no input count still gets
// its prompt estimated.
func TestChatClientHubEstimatesAMissingInputCount(t *testing.T) {
	_, _, usage, err := runChatClientHub(t, httptest.NewRecorder(),
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"output_tokens":3,"total_tokens":3}}}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if usage.PromptTokens == 0 || usage.CompletionTokens != 3 {
		t.Fatalf("usage: %+v", usage)
	}
}
