package relay

import (
	"encoding/json"
	"github.com/lazu-ai/lazu/internal/common"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

func TestTerminalStreamSettlementStampsFailureBeforeSnapshot(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	err := types.NewError(io.ErrUnexpectedEOF, types.ErrorCodeUpstreamNetworkError)
	// No reported billable usage: there must be no financial side effect, but
	// the routing snapshot still needs the actual terminal error immediately.
	settleTerminalProtocolStream(c, nil, nil, err)
	if common.GetContextKeyString(c, constant.ContextKeyRequestErrorCode) != string(err.GetErrorCode()) ||
		common.GetContextKeyString(c, constant.ContextKeyRequestErrorType) != string(err.GetErrorType()) {
		t.Fatal("terminal failure was unavailable to the combined settlement envelope")
	}
}

// Once a stream is open — a keep-alive ping is enough — a terminal error can
// only reach the client as a frame of that stream. Getting the frame wrong is
// indistinguishable from sending nothing: the caller sees the turn stop.
func TestTerminalStreamErrorUsesTheProtocolsOwnFrame(t *testing.T) {
	gin.SetMode(gin.TestMode)

	openStream := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		// Stand in for a keep-alive ping: commits the response, carries nothing.
		_, _ = c.Writer.Write([]byte(": PING\n\n"))
		return c, w
	}

	lazuErr := func() *types.LazuError {
		return types.NewErrorWithStatusCode(
			errString("servers are currently overloaded"),
			types.ErrorCodeUpstreamOverloaded,
			http.StatusBadGateway,
		)
	}

	t.Run("responses emits a named, flat error event", func(t *testing.T) {
		c, w := openStream()
		SafeStreamLazuErrorForFormat(c, types.RelayFormatOpenAIResponses, lazuErr())
		body := w.Body.String()

		if !strings.Contains(body, "event: error\n") {
			t.Fatalf("unnamed event is delivered as a generic message and dropped: %q", body)
		}
		// Responses has no [DONE] sentinel; emitting one is protocol noise.
		if strings.Contains(body, "[DONE]") {
			t.Fatalf("chat sentinel leaked into a responses stream: %q", body)
		}

		payload := lastDataFrame(t, body)
		if strings.Contains(payload, `"error":{`) {
			t.Fatalf("chat-completions envelope in a responses error event: %s", payload)
		}

		// The client reads these from the top level, and so does our own
		// decoder — round-tripping through it is the real assertion.
		var decoded dto.ResponsesStreamResponse
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			t.Fatalf("emitted frame is not a responses event: %v (%s)", err, payload)
		}
		if decoded.Type != "error" {
			t.Fatalf("our own decoder would not see this as a failure: type=%q", decoded.Type)
		}
		if !strings.Contains(decoded.Message, "busy") && !strings.Contains(decoded.Message, "overload") {
			t.Fatalf("error text did not survive: %q", decoded.Message)
		}
	})

	t.Run("chat keeps its envelope and sentinel", func(t *testing.T) {
		c, w := openStream()
		SafeStreamLazuErrorForFormat(c, types.RelayFormatOpenAI, lazuErr())
		body := w.Body.String()

		if strings.Contains(body, "event: error") {
			t.Fatalf("named event in a chat stream: %q", body)
		}
		if !strings.Contains(body, "[DONE]") {
			t.Fatalf("chat stream not terminated: %q", body)
		}
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(firstDataFrame(t, body)), &envelope); err != nil || envelope.Error.Message == "" {
			t.Fatalf("chat error envelope lost: %v (%s)", err, body)
		}
	})

	t.Run("before any byte it is still plain JSON", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		SafeStreamLazuErrorForFormat(c, types.RelayFormatOpenAIResponses, lazuErr())
		// upstream_overloaded is written as 503 so SDKs back off; the
		// runtime status that drives retries stays 502.
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		if strings.Contains(w.Body.String(), "event: error") {
			t.Fatalf("SSE frame sent for an unstarted response: %q", w.Body)
		}
	})
}

type errString string

func (e errString) Error() string { return string(e) }

func dataFrames(t *testing.T, body string) []string {
	t.Helper()
	var frames []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			frames = append(frames, strings.TrimPrefix(line, "data: "))
		}
	}
	if len(frames) == 0 {
		t.Fatalf("no data frame in %q", body)
	}
	return frames
}

func lastDataFrame(t *testing.T, body string) string {
	t.Helper()
	frames := dataFrames(t, body)
	return frames[len(frames)-1]
}

func firstDataFrame(t *testing.T, body string) string {
	t.Helper()
	return dataFrames(t, body)[0]
}

func TestResponsesTerminalFrameKeepsSupportReference(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "req_terminal")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()
	SafeStreamLazuErrorForFormat(c, types.RelayFormatOpenAIResponses, types.NewError(nil, types.ErrorCodeUpstreamTimeout))
	var frame map[string]any
	if err := json.Unmarshal([]byte(firstDataFrame(t, w.Body.String())), &frame); err != nil {
		t.Fatal(err)
	}
	if frame["type"] != "error" || frame["code"] != "upstream_timeout" || frame["request_id"] != "req_terminal" || frame["lazu"] != "LZ-6101" || frame["docs"] != types.ErrorDocsURL("LZ-6101") {
		t.Fatalf("reference lost: %v", frame)
	}
}
