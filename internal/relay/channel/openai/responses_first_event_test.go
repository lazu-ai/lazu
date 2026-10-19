package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

func nativeResponsesInfo() *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		PromptTokens:               64000,
		UsagePromptTokens:          64000,
		UsagePromptTokensSet:       true,
		UsagePromptTokensAvailable: true,
		RelayFormat:                types.RelayFormatOpenAIResponses,
		RelayMode:                  relayconstant.RelayModeResponses,
		ChannelMeta:                &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.6-sol"},
	}
	// A struct literal leaves the unexported first-response marker false, which
	// makes SetFirstResponseTime a no-op and HasSendResponse permanently false —
	// the opposite of production, where GenRelayInfo arms it and the stream
	// scanner stamps it on the first UPSTREAM event, before any handler sees it.
	// Arming it here is what makes these tests able to catch a hold condition
	// that leans on HasSendResponse.
	info.ResetFirstResponse()
	return info
}

// A Responses stream reports failure in-band under a transport-level 200.
// Forwarding that first event made the attempt look successful: the request was
// billed as a zero-token "consumption", produced no error row, fed nothing to
// channel health, and could not be retried because the response had started.
func TestResponsesStreamFirstEventFailureIsRetryableError(t *testing.T) {
	service.InitTokenEncoders()
	for _, tc := range []struct {
		name, body, wantCode string
	}{
		{
			name:     "explicit error event",
			body:     "data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"OpenAI servers are currently overloaded. / 当前 OpenAI 服务器过载\"}\n\n",
			wantCode: string(types.ErrorCodeUpstreamOverloaded),
		},
		{
			name:     "response.failed terminal",
			body:     "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"type\":\"server_error\",\"message\":\"rate limit reached for this group\"}}}\n\n",
			wantCode: string(types.ErrorCodeUpstreamRateLimited),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := nativeResponsesInfo()

			usage, err := OaiResponsesStreamHandler(c, info, &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			})

			if err == nil {
				t.Fatal("an in-band stream failure was reported as success")
			}
			if types.IsSkipRetryError(err) {
				t.Fatal("a failure that sent no output must stay retryable")
			}
			if got := string(err.GetErrorCode()); got != tc.wantCode {
				t.Fatalf("error code = %q, want %q", got, tc.wantCode)
			}
			if err.StatusCode/100 == 2 {
				t.Fatalf("transport 200 leaked into the relay error: status=%d", err.StatusCode)
			}
			// Nothing may reach the client: a forwarded byte both starts the
			// response (barring the retry) and duplicates on the next attempt.
			if w.Body.Len() != 0 {
				t.Fatalf("forwarded a failed stream to the client: %s", w.Body)
			}
			if !info.StreamFailedWithoutUsage {
				t.Fatal("no-usage refund signal not raised")
			}
			if service.ValidUsage(usage) {
				t.Fatalf("billed admission tokens for a stream that produced nothing: %+v", usage)
			}
			if common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
				t.Fatal("asked settlement to charge for a failed attempt")
			}
			if !common.GetContextKeyBool(c, constant.ContextKeyStreamFirstEventFailed) {
				t.Fatal("first-event failure was not marked for observability")
			}
		})
	}
}

// Once output has been forwarded, a later failure must not be replayed: the
// client already holds a partial response.
func TestResponsesStreamFailureAfterOutputIsNotHeld(t *testing.T) {
	service.InitTokenEncoders()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := nativeResponsesInfo()

	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"overloaded\"}}}\n\n"

	_, err := OaiResponsesStreamHandler(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	})
	if err != nil {
		t.Fatalf("a stream that produced output must stay a passthrough: %v", err)
	}
	if !strings.Contains(w.Body.String(), "partial answer") {
		t.Fatalf("dropped output the client had already been promised: %s", w.Body)
	}
	if common.GetContextKeyBool(c, constant.ContextKeyStreamFirstEventFailed) {
		t.Fatal("marked a mid-stream failure as a first-event failure")
	}
}

// A healthy stream must be untouched by the first-event check.
func TestResponsesStreamPassthroughUnaffected(t *testing.T) {
	service.InitTokenEncoders()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := nativeResponsesInfo()

	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":12,\"output_tokens\":3}}}\n\n"

	usage, err := OaiResponsesStreamHandler(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	})
	if err != nil {
		t.Fatalf("healthy stream failed: %v", err)
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 3 {
		t.Fatalf("provider usage lost: %+v", usage)
	}
	if !strings.Contains(w.Body.String(), "hello") {
		t.Fatalf("content not forwarded: %s", w.Body)
	}
}

// A keep-alive ping commits the SSE response long before a stalled provider
// says anything. The hold must judge "nothing forwarded" by content, not by
// whether bytes have been written, or the slow-failure case this whole change
// targets silently stops being covered.
func TestResponsesStreamFirstEventHoldSurvivesKeepAlivePing(t *testing.T) {
	service.InitTokenEncoders()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := nativeResponsesInfo()

	// Stand in for the keep-alive that fires during a long stall.
	if err := helper.PingData(c); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	if !c.Writer.Written() {
		t.Fatal("fixture ping did not commit the response")
	}

	_, err := OaiResponsesStreamHandler(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"servers are currently overloaded\"}}}\n\n")),
	})
	if err == nil {
		t.Fatal("a ping before the failure turned it back into a success")
	}
	if types.IsSkipRetryError(err) {
		t.Fatal("a ping is not content; the attempt must stay retryable")
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyStreamFirstEventFailed) {
		t.Fatal("first-event failure not marked after a ping")
	}
	if strings.Contains(w.Body.String(), "overloaded") {
		t.Fatalf("forwarded the failed event: %s", w.Body)
	}
}

// An in-band failure the provider means permanently must not be dressed up as
// a transient one: retrying replays a prompt that fails identically, and the
// customer is told to wait or to pay for the direct lane for no reason.
func TestResponsesStreamFirstEventClassifiesNonTransientFailures(t *testing.T) {
	service.InitTokenEncoders()
	for _, tc := range []struct {
		name, message string
		wantCode      types.ErrorCode
		wantRetryable bool
	}{
		{"overload", "servers are currently overloaded", types.ErrorCodeUpstreamOverloaded, true},
		{"rate limit", "rate limit reached for this group", types.ErrorCodeUpstreamRateLimited, true},
		{"content policy", "request was blocked by our content filter", types.ErrorCodePromptBlocked, false},
		{"context window", "maximum context length is 128000 tokens", types.ErrorCodeContextLengthExceeded, false},
		{"unrecognised", "something went sideways", types.ErrorCodeUpstreamError, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			body := "data: {\"type\":\"error\",\"message\":" + strconv.Quote(tc.message) + "}\n\n"

			_, err := OaiResponsesStreamHandler(c, nativeResponsesInfo(), &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			})
			if err == nil {
				t.Fatal("in-band failure reported as success")
			}
			if got := err.GetErrorCode(); got != tc.wantCode {
				t.Fatalf("error code = %q, want %q", got, tc.wantCode)
			}
			// A non-transient class must land on a status the retry policy
			// refuses, so the classification actually stops the replay. The
			// policy retries 429 and 5xx and nothing else in this range.
			retryable := err.StatusCode == http.StatusTooManyRequests || err.StatusCode/100 == 5
			if retryable != tc.wantRetryable {
				t.Fatalf("status %d retryable = %v, want %v", err.StatusCode, retryable, tc.wantRetryable)
			}
		})
	}
}
