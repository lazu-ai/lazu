package openai

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

type failingStreamReader struct{}

type contentNotificationWriter struct {
	*httptest.ResponseRecorder
	content chan struct{}
	once    sync.Once
}

func (w *contentNotificationWriter) Write(body []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(body)
	if strings.Contains(string(body), "first-token") {
		w.once.Do(func() { close(w.content) })
	}
	return n, err
}

func (w *contentNotificationWriter) WriteString(body string) (int, error) {
	return w.Write([]byte(body))
}

func TestNativeChatFirstContentDoesNotWaitForNextProviderEvent(t *testing.T) {
	w := &contentNotificationWriter{ResponseRecorder: httptest.NewRecorder(), content: make(chan struct{})}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	reader, writer := io.Pipe()
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions,
		ShouldIncludeUsage: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}
	done := make(chan *types.LazuError, 1)
	go func() {
		_, err := OaiStreamHandler(c, info, &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader})
		done <- err
	}()
	_, _ = io.WriteString(writer, "data: {\"id\":\"first\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first-token\"}}]}\n\n")
	forwarded := false
	select {
	case <-w.content:
		forwarded = true
	case <-time.After(time.Second):
	}
	_, _ = io.WriteString(writer, "data: {\"id\":\"first\",\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":3,\"total_tokens\":11}}\n\ndata: [DONE]\n\n")
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	if !forwarded {
		t.Fatal("first content waited for the next provider event")
	}
	if got := strings.Count(w.Body.String(), "first-token"); got != 1 {
		t.Fatalf("first content was forwarded %d times", got)
	}
}

func TestNativeChatStreamIgnoresBlankHeartbeats(t *testing.T) {
	for _, heartbeat := range []string{"data: \n\n", "data:    \n\n", "data:\t\r\n\r\n"} {
		t.Run(strings.TrimSuffix(heartbeat, "\n\n"), func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			body := heartbeat +
				"data: {\"id\":\"chatcmpl-heartbeat\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\n" + heartbeat +
				"data: {\"id\":\"chatcmpl-heartbeat\",\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":3,\"total_tokens\":11}}\n\n" + heartbeat + "data: [DONE]\n\n"
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions,
				ShouldIncludeUsage: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}
			usage, err := OaiStreamHandler(c, info, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))})
			if err != nil {
				t.Fatalf("heartbeat interrupted valid stream: %v", err)
			}
			if usage.PromptTokens != 8 || usage.CompletionTokens != 3 || usage.Measurement().Input != dto.UsageSourceProvider {
				t.Fatalf("heartbeat changed provider usage: %+v", usage)
			}
			if !strings.Contains(w.Body.String(), "hello") || strings.Count(w.Body.String(), "[DONE]") != 1 {
				t.Fatalf("lost content or terminal event: %s", w.Body)
			}
		})
	}
}

func (failingStreamReader) Read([]byte) (int, error) {
	return 0, errors.New("fixture upstream disconnect")
}

func TestNativeChatStreamFailureRetainsOnlyObservedUsage(t *testing.T) {
	service.InitTokenEncoders()
	for _, tc := range []struct {
		name, prefix            string
		readError, output, skip bool
	}{
		{"empty transport failure", "", true, false, false},
		{"clean EOF before first event", "", false, false, false},
		{"malformed nonempty payload", "data: not-json\n\n", false, false, true},
		{"partial transport failure", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"billable output\"}}]}\n\n", true, true, true},
		{"partial missing terminal", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"billable output\"}}]}\n\n", false, true, true},
		// An in-band failure that reached no client byte is retryable, exactly
		// like the clean-EOF case above: no output was forwarded, so sending
		// again cannot duplicate anything.
		{"explicit provider failure", "data: {\"error\":{\"type\":\"server_error\",\"code\":\"server_error\",\"message\":\"failed\"}}\n\n", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			var body io.Reader = strings.NewReader(tc.prefix)
			if tc.readError {
				body = io.MultiReader(body, failingStreamReader{})
			}
			info := &relaycommon.RelayInfo{PromptTokens: 9000, UsagePromptTokens: 12, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
				RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}
			// Production arms this in GenRelayInfo and the stream scanner stamps
			// it on the first UPSTREAM event, before any handler callback runs.
			// A struct literal leaves it off, which silently exempts the handler
			// from the very condition it has to cope with.
			info.ResetFirstResponse()
			usage, err := OaiStreamHandler(c, info, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(body)})
			if err == nil || types.IsSkipRetryError(err) != tc.skip {
				t.Fatalf("stream failure became success or retry: %v", err)
			}
			if strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatal("failure emitted success terminator")
			}
			if common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) != tc.output {
				t.Fatalf("wrong settlement signal: %+v", usage)
			}
			if info.StreamFailedWithoutUsage == tc.output {
				t.Fatalf("incorrect no-usage refund signal: %+v", usage)
			}
			if tc.output {
				if usage.PromptTokens != 12 || usage.CompletionTokens <= 0 || !strings.Contains(w.Body.String(), "billable output") {
					t.Fatalf("lost partial result: %+v body=%s", usage, w.Body)
				}
			} else if service.ValidUsage(usage) {
				t.Fatalf("empty failure used admission: %+v", usage)
			}
		})
	}
}
